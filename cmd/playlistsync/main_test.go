package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"playlistsync/internal/auth"
	"playlistsync/internal/engine"
	"playlistsync/internal/model"
	"playlistsync/internal/spotify"
	"playlistsync/internal/ytmusic"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	origIsTerm := isTerminalFunc
	isTerminalFunc = func() bool { return false }
	reset := auth.SetBrowserLauncherForTesting(
		func() (string, error) {
			return "", fmt.Errorf("browser launch disabled in tests")
		},
		func(exe string, args []string) (*exec.Cmd, error) {
			return nil, fmt.Errorf("browser launch disabled in tests")
		},
	)
	defer func() {
		isTerminalFunc = origIsTerm
		reset()
	}()
	os.Exit(m.Run())
}

func TestNormalizePlatform(t *testing.T) {
	cases := map[string]string{
		"spotify":       "spotify",
		"SPO":           "spotify",
		"sp":            "spotify",
		"youtube-music": "youtube-music",
		"youtube_music": "youtube-music",
		"ytmusic":       "youtube-music",
		"youtube":       "youtube-music",
		"ytm":           "youtube-music",
		"yt":            "youtube-music",
		"ym":            "youtube-music",
		"custom":        "custom",
	}

	for input, expected := range cases {
		got := normalizePlatform(input)
		if got != expected {
			t.Errorf("normalizePlatform(%q) = %q; want %q", input, got, expected)
		}
	}
}

func TestRun_HelpAndUsage(t *testing.T) {
	if code := run([]string{}); code != 0 {
		t.Errorf("expected exit code 0 for empty args, got %d", code)
	}
	if code := run([]string{"help"}); code != 0 {
		t.Errorf("expected exit code 0 for help, got %d", code)
	}
	if code := run([]string{"--help"}); code != 0 {
		t.Errorf("expected exit code 0 for --help, got %d", code)
	}
	if code := run([]string{"-h"}); code != 0 {
		t.Errorf("expected exit code 0 for -h, got %d", code)
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	if code := run([]string{"unknown_cmd_123"}); code != 1 {
		t.Errorf("expected exit code 1 for unknown command, got %d", code)
	}
}

func TestRun_SyncCommand(t *testing.T) {
	t.Run("Missing playlist name positional and flag", func(t *testing.T) {
		if code := run([]string{"sync"}); code != 1 {
			t.Errorf("expected exit code 1 for sync without playlist name, got %d", code)
		}
	})

	t.Run("Invalid flag", func(t *testing.T) {
		if code := run([]string{"sync", "--invalid-flag"}); code != 1 {
			t.Errorf("expected exit code 1 for invalid flag, got %d", code)
		}
	})

	t.Run("Sync with name flag but missing auth", func(t *testing.T) {
		code := run([]string{"sync", "--name=flag_test_pl", "--from=spotify", "--to=youtube-music", "--proxy=http://127.0.0.1:8888", "--clean-extra=true"})
		if code != 1 {
			t.Errorf("expected exit code 1 when credentials missing, got %d", code)
		}
	})

	t.Run("Sync with non-existent credentials fails cleanly", func(t *testing.T) {
		code := run([]string{"sync", "test_rock", "--from=spotify", "--to=youtube-music", "--clean-extra=false"})
		if code != 1 {
			t.Errorf("expected exit code 1 when credentials missing, got %d", code)
		}
	})

	t.Run("Sync with reverse direction flag aliases", func(t *testing.T) {
		code := run([]string{"sync", "test_rock", "--from=ytmusic", "--to=spo"})
		if code != 1 {
			t.Errorf("expected exit code 1 for sync execution failure, got %d", code)
		}
	})

	t.Run("Sync success with mock server", func(t *testing.T) {
		origIsTerm := isTerminalFunc
		isTerminalFunc = func() bool { return false }
		defer func() {
			isTerminalFunc = origIsTerm
		}()
		_ = os.MkdirAll("output", 0755)
		_ = os.MkdirAll("output/auth", 0755)
		defer os.RemoveAll("output")

		spFile := filepath.Join("output", "spotify_sync_cli_test_source.json")
		spData := model.SpotifyPlaylist{
			Platform:          "spotify",
			PlaylistName:      "sync_cli_test",
			SourcePlaylistURL: "https://open.spotify.com/playlist/sp123",
			Tracks: []model.SpotifyTrack{
				{Index: 1, Title: "Song One", Artists: []string{"Artist A"}, Duration: "3:30", Query: "Song One Artist A"},
			},
		}
		_ = spotify.WritePlaylistJSON(spFile, &spData)
		_ = auth.SaveRawCookieMap(DefaultYTMAuthPath, "SAPISID=valid_sapisid_token; __Secure-3PAPISID=valid_sec")

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			browseID, _ := req["browseId"].(string)

			if browseID == "FEmusic_liked_playlists" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"header": map[string]interface{}{
						"musicHeaderRenderer": map[string]interface{}{
							"title": map[string]interface{}{
								"runs": []map[string]interface{}{{"text": "CLI Sync User"}},
							},
						},
					},
				})
				return
			}

			if browseID == "FEmusic_library_playlists" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"contents": map[string]interface{}{
						"singleColumnBrowseResultsRenderer": map[string]interface{}{
							"tabs": []map[string]interface{}{
								{
									"tabRenderer": map[string]interface{}{
										"content": map[string]interface{}{
											"sectionListRenderer": map[string]interface{}{
												"contents": []map[string]interface{}{
													{
														"gridRenderer": map[string]interface{}{
															"items": []map[string]interface{}{
																{
																	"musicTwoRowItemRenderer": map[string]interface{}{
																		"title": map[string]interface{}{
																			"runs": []map[string]interface{}{{"text": "sync_cli_test (Spotify import)"}},
																		},
																		"navigationEndpoint": map[string]interface{}{
																			"browseEndpoint": map[string]interface{}{
																				"browseId": "VLPLmock_sync_cli_123",
																			},
																		},
																	},
																},
															},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				})
				return
			}

			if browseID == "VLPLmock_sync_cli_123" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"header": map[string]interface{}{
						"musicDetailHeaderRenderer": map[string]interface{}{
							"title": map[string]interface{}{
								"runs": []map[string]interface{}{{"text": "sync_cli_test (Spotify import)"}},
							},
						},
					},
					"contents": map[string]interface{}{
						"singleColumnBrowseResultsRenderer": map[string]interface{}{
							"tabs": []map[string]interface{}{
								{
									"tabRenderer": map[string]interface{}{
										"content": map[string]interface{}{
											"sectionListRenderer": map[string]interface{}{
												"contents": []map[string]interface{}{
													{
														"musicPlaylistShelfRenderer": map[string]interface{}{
															"contents": []map[string]interface{}{
																{
																	"musicResponsiveListItemRenderer": map[string]interface{}{
																		"playlistItemData": map[string]interface{}{
																			"videoId": "vid_song_1",
																		},
																		"flexColumns": []map[string]interface{}{
																			{
																				"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																					"text": map[string]interface{}{
																						"runs": []map[string]interface{}{{"text": "Song One"}},
																					},
																				},
																			},
																			{
																				"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																					"text": map[string]interface{}{
																						"runs": []map[string]interface{}{{"text": "Artist A"}},
																					},
																				},
																			},
																		},
																	},
																},
															},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				})
				return
			}

			if req["title"] != nil {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"playlistId": "PLmock_sync_cli_123",
				})
				return
			}

			_ = json.NewEncoder(w).Encode(map[string]interface{}{})
		}))
		defer ts.Close()

		origBrowse := ytmusic.EndpointBrowse
		origCreate := ytmusic.EndpointCreatePlaylist
		ytmusic.EndpointBrowse = ts.URL
		ytmusic.EndpointCreatePlaylist = ts.URL
		resetAuth := auth.SetEndpointsForTesting("", "", ts.URL)
		defer func() {
			ytmusic.EndpointBrowse = origBrowse
			ytmusic.EndpointCreatePlaylist = origCreate
			resetAuth()
		}()

		code := run([]string{"sync", "sync_cli_test", "--from=spotify", "--to=youtube-music"})
		if code != 0 {
			t.Errorf("expected exit code 0 for sync, got %d", code)
		}

		// Test specifier notation (e.g. spotify:"sync_cli_test" ytm:)
		codeSpec := run([]string{"sync", "spotify:sync_cli_test", "ytm:", "-y"})
		if codeSpec != 0 {
			t.Errorf("expected exit code 0 for sync with specifier notation, got %d", codeSpec)
		}

		// Test source/target flag notation
		codeFlags := run([]string{"sync", "--source=sync_cli_test", "--from=spotify", "--to=youtube-music", "-y"})
		if codeFlags != 0 {
			t.Errorf("expected exit code 0 for sync with --source flag, got %d", codeFlags)
		}

		codeYes := run([]string{"sync", "sync_cli_test", "--from=spotify", "--to=youtube-music", "-y"})
		if codeYes != 0 {
			t.Errorf("expected exit code 0 for sync with trailing -y, got %d", codeYes)
		}

		codeNonInteractive := run([]string{"sync", "--non-interactive", "sync_cli_test", "--from=spotify", "--to=youtube-music"})
		if codeNonInteractive != 0 {
			t.Errorf("expected exit code 0 for sync with --non-interactive, got %d", codeNonInteractive)
		}
	})
}

func TestRun_LoginCommand(t *testing.T) {
	t.Run("Login invalid flags", func(t *testing.T) {
		if code := run([]string{"login", "--unknown-flag"}); code != 1 {
			t.Errorf("expected exit code 1 for login with bad flag, got %d", code)
		}
	})

	t.Run("Login with unsupported platform", func(t *testing.T) {
		if code := run([]string{"login", "unsupported_platform"}); code != 1 {
			t.Errorf("expected exit code 1 for login unsupported platform, got %d", code)
		}
	})

	t.Run("Login with force and proxy flags", func(t *testing.T) {
		code := run([]string{"login", "--force=true", "--proxy=http://127.0.0.1:9999", "unsupported_p"})
		if code != 1 {
			t.Errorf("expected exit code 1 for unsupported platform with flags, got %d", code)
		}
	})

	t.Run("Login success cached fast-path", func(t *testing.T) {
		_ = os.MkdirAll("output/auth", 0755)
		defer os.RemoveAll("output")

		_ = auth.SaveCookie(auth.DefaultSpotifyAuthPath, "sp_dc=cached_spotify_user_secret")

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"accessToken": "mock_token_cached",
				"isAnonymous": false,
				"username":    "CachedCLIUser",
			})
		}))
		defer ts.Close()

		resetEndpoints := auth.SetEndpointsForTesting(ts.URL+"/token", "", "")
		defer resetEndpoints()

		if code := run([]string{"login", "spotify"}); code != 0 {
			t.Errorf("expected exit code 0 for cached login, got %d", code)
		}
	})
}

func TestRun_InspectCommand(t *testing.T) {
	t.Run("Missing playlist name argument", func(t *testing.T) {
		if code := run([]string{"inspect"}); code != 1 {
			t.Errorf("expected exit code 1, got %d", code)
		}
		if code := run([]string{"status"}); code != 1 {
			t.Errorf("expected exit code 1 for status alias, got %d", code)
		}
		if code := run([]string{"summary"}); code != 1 {
			t.Errorf("expected exit code 1 for summary alias, got %d", code)
		}
	})

	t.Run("File not found error", func(t *testing.T) {
		if code := run([]string{"inspect", "non_existent_pl_xyz_123"}); code != 1 {
			t.Errorf("expected exit code 1 for missing file inspect, got %d", code)
		}
	})

	t.Run("Success inspect and status alias", func(t *testing.T) {
		_ = os.MkdirAll("output", 0755)
		defer os.RemoveAll("output")
		resPath := filepath.Join("output", "spotify_to_ytmusic_cli_test_result.json")
		resData := model.SyncResult{
			Direction:         "spotify-to-youtube-music",
			SourcePlatform:    "spotify",
			TargetPlatform:    "youtube-music",
			PlaylistURL:       "https://music.youtube.com/playlist?list=PL123",
			TotalSourceTracks: 5,
			AddedTracks:       5,
		}
		b, _ := json.Marshal(resData)
		_ = os.WriteFile(resPath, b, 0644)

		if code := run([]string{"inspect", "cli_test"}); code != 0 {
			t.Errorf("expected exit code 0 for inspect, got %d", code)
		}
		if code := run([]string{"status", "cli_test"}); code != 0 {
			t.Errorf("expected exit code 0 for status alias, got %d", code)
		}
		if code := run([]string{"summary", "cli_test"}); code != 0 {
			t.Errorf("expected exit code 0 for summary alias, got %d", code)
		}
	})
}

func TestRun_VerifyCommand(t *testing.T) {
	t.Run("Missing playlist name", func(t *testing.T) {
		if code := run([]string{"verify"}); code != 1 {
			t.Errorf("expected exit code 1, got %d", code)
		}
		if code := run([]string{"validate"}); code != 1 {
			t.Errorf("expected exit code 1 for validate alias, got %d", code)
		}
	})

	t.Run("Files not found error", func(t *testing.T) {
		if code := run([]string{"verify", "non_existent_123"}); code != 1 {
			t.Errorf("expected exit code 1, got %d", code)
		}
	})

	t.Run("Success verify", func(t *testing.T) {
		_ = os.MkdirAll("output", 0755)
		defer os.RemoveAll("output")
		spPath := filepath.Join("output", "spotify_cli_verify_test_source.json")
		resPath := filepath.Join("output", "spotify_to_ytmusic_cli_verify_test_result.json")
		repPath := filepath.Join("output", "spotify_to_ytmusic_cli_verify_test_report.json")

		sp := model.SpotifyPlaylist{Platform: "spotify", PlaylistName: "cli_verify_test", Tracks: []model.SpotifyTrack{{Index: 1, Title: "Song"}}}
		res := model.SyncResult{
			Direction:         "spotify-to-youtube-music",
			SourcePlatform:    "spotify",
			TargetPlatform:    "youtube-music",
			PlaylistURL:       "https://music.youtube.com/playlist?list=PL123",
			TotalSourceTracks: 1,
			AddedTracks:       1,
		}

		_ = spotify.WritePlaylistJSON(spPath, &sp)
		b, _ := json.Marshal(res)
		_ = os.WriteFile(resPath, b, 0644)
		_ = os.WriteFile(repPath, b, 0644)

		if code := run([]string{"verify", "cli_verify_test"}); code != 0 {
			t.Errorf("expected exit code 0 for verify, got %d", code)
		}
		if code := run([]string{"validate", "cli_verify_test"}); code != 0 {
			t.Errorf("expected exit code 0 for validate alias, got %d", code)
		}
	})
}

func TestRun_ReportCommand(t *testing.T) {
	t.Run("Missing playlist name", func(t *testing.T) {
		if code := run([]string{"report"}); code != 1 {
			t.Errorf("expected exit code 1, got %d", code)
		}
	})

	t.Run("File not found error", func(t *testing.T) {
		if code := run([]string{"report", "non_existent_123"}); code != 1 {
			t.Errorf("expected exit code 1, got %d", code)
		}
	})

	t.Run("Success report generation", func(t *testing.T) {
		_ = os.MkdirAll("output", 0755)
		defer os.RemoveAll("output")
		resPath := filepath.Join("output", "spotify_to_ytmusic_cli_rep_test_result.json")
		repPath := filepath.Join("output", "spotify_to_ytmusic_cli_rep_test_report.json")

		res := model.SyncResult{
			Direction:         "spotify-to-youtube-music",
			SourcePlatform:    "spotify",
			TargetPlatform:    "youtube-music",
			PlaylistURL:       "https://music.youtube.com/playlist?list=PL123",
			TotalSourceTracks: 1,
			AddedTracks:       1,
		}
		b, _ := json.Marshal(res)
		_ = os.WriteFile(resPath, b, 0644)

		if code := run([]string{"report", "cli_rep_test"}); code != 0 {
			t.Errorf("expected exit code 0 for report, got %d", code)
		}

		if _, err := os.Stat(repPath); err != nil {
			t.Errorf("report file was not created: %v", err)
		}
	})
}

func TestPromptConfirm(t *testing.T) {
	origStdin := stdinReader
	origIsTerm := isTerminalFunc
	defer func() {
		stdinReader = origStdin
		isTerminalFunc = origIsTerm
	}()

	t.Run("AutoYes bypasses prompt", func(t *testing.T) {
		isTerminalFunc = func() bool { return true }
		stdinReader = strings.NewReader("no\n")
		if !promptConfirm("Proceed?", true) {
			t.Errorf("expected promptConfirm to return true with autoYes=true")
		}
	})

	t.Run("Non-terminal stdin defaults to true", func(t *testing.T) {
		isTerminalFunc = func() bool { return false }
		stdinReader = strings.NewReader("no\n")
		if !promptConfirm("Proceed?", false) {
			t.Errorf("expected promptConfirm to return true for non-terminal stdin")
		}
	})

	t.Run("Terminal with yes answer", func(t *testing.T) {
		isTerminalFunc = func() bool { return true }
		stdinReader = strings.NewReader("y\n")
		if !promptConfirm("Proceed?", false) {
			t.Errorf("expected promptConfirm to return true for 'y' input")
		}

		stdinReader = strings.NewReader("YES\n")
		if !promptConfirm("Proceed?", false) {
			t.Errorf("expected promptConfirm to return true for 'YES' input")
		}
	})

	t.Run("Terminal with no or empty answer", func(t *testing.T) {
		isTerminalFunc = func() bool { return true }
		stdinReader = strings.NewReader("n\n")
		if promptConfirm("Proceed?", false) {
			t.Errorf("expected promptConfirm to return false for 'n' input")
		}

		stdinReader = strings.NewReader("\n")
		if promptConfirm("Proceed?", false) {
			t.Errorf("expected promptConfirm to return false for empty input")
		}
	})
}

func TestCobra_SubcommandHelpAndFlags(t *testing.T) {
	subcommands := []string{"sync", "login", "inspect", "status", "summary", "verify", "validate", "report"}
	for _, cmdName := range subcommands {
		t.Run("Help flag for "+cmdName, func(t *testing.T) {
			if code := run([]string{cmdName, "--help"}); code != 0 {
				t.Errorf("expected exit code 0 for %s --help, got %d", cmdName, code)
			}
			if code := run([]string{cmdName, "-h"}); code != 0 {
				t.Errorf("expected exit code 0 for %s -h, got %d", cmdName, code)
			}
		})
	}
}

func TestRun_CustomOutputDirFlag(t *testing.T) {
	tempDir := t.TempDir()

	spPath := filepath.Join(tempDir, "spotify_custom_pl_source.json")
	resPath := filepath.Join(tempDir, "spotify_to_ytmusic_custom_pl_result.json")
	repPath := filepath.Join(tempDir, "spotify_to_ytmusic_custom_pl_report.json")

	sp := model.SpotifyPlaylist{
		Platform:     "spotify",
		PlaylistName: "custom_pl",
		Tracks:       []model.SpotifyTrack{{Index: 1, Title: "Song"}},
	}
	res := model.SyncResult{
		Direction:         "spotify-to-youtube-music",
		SourcePlatform:    "spotify",
		TargetPlatform:    "youtube-music",
		PlaylistURL:       "https://music.youtube.com/playlist?list=PLcustom",
		TotalSourceTracks: 1,
		AddedTracks:       1,
	}

	_ = spotify.WritePlaylistJSON(spPath, &sp)
	b, _ := json.Marshal(res)
	_ = os.WriteFile(resPath, b, 0644)
	_ = os.WriteFile(repPath, b, 0644)

	t.Run("Inspect with --output-dir", func(t *testing.T) {
		if code := run([]string{"inspect", "custom_pl", "--output-dir", tempDir}); code != 0 {
			t.Errorf("expected exit code 0 for inspect with custom output-dir, got %d", code)
		}
	})

	t.Run("Verify with --output-dir", func(t *testing.T) {
		if code := run([]string{"verify", "custom_pl", "--output-dir", tempDir}); code != 0 {
			t.Errorf("expected exit code 0 for verify with custom output-dir, got %d", code)
		}
	})

	t.Run("Report with --output-dir", func(t *testing.T) {
		if code := run([]string{"report", "custom_pl", "--output-dir", tempDir}); code != 0 {
			t.Errorf("expected exit code 0 for report with custom output-dir, got %d", code)
		}
	})
}

func TestExtractTargetID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantID   string
		wantOK   bool
	}{
		{"Empty string", "", "", false},
		{"Whitespace only", "   \t\n", "", false},
		{"Raw 11-char YouTube ID", "dQw4w9WgXcQ", "dQw4w9WgXcQ", true},
		{"Raw 22-char Spotify ID", "4cOdK2wGLETKBW3PvgPWqT", "4cOdK2wGLETKBW3PvgPWqT", true},
		{"Spotify URI format", "spotify:track:4cOdK2wGLETKBW3PvgPWqT", "4cOdK2wGLETKBW3PvgPWqT", true},
		{"Invalid Spotify URI", "spotify:track:short_invalid", "", false},
		{"YouTube standard URL", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ", true},
		{"YouTube URL with extra query params", "https://www.youtube.com/watch?v=dQw4w9WgXcQ&feature=emb_title&t=10s", "dQw4w9WgXcQ", true},
		{"YouTube Music URL", "https://music.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ", true},
		{"YouTube short link", "https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ", true},
		{"YouTube short link with query", "https://youtu.be/dQw4w9WgXcQ?si=abc_123", "dQw4w9WgXcQ", true},
		{"Spotify track URL", "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", "4cOdK2wGLETKBW3PvgPWqT", true},
		{"Spotify track URL with query params", "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT?si=abc&context=spotify", "4cOdK2wGLETKBW3PvgPWqT", true},
		{"Fallback alphanumeric ID", "custom_id_12345", "custom_id_12345", true},
		{"Invalid URL without track", "https://open.spotify.com/album/4cOdK2wGLETKBW3PvgPWqT", "", false},
		{"Invalid URL missing video ID", "https://www.youtube.com/watch", "", false},
		{"Too short string", "abc", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotOK := extractTargetID(tt.input)
			if gotOK != tt.wantOK || gotID != tt.wantID {
				t.Errorf("extractTargetID(%q) = (%q, %v); want (%q, %v)", tt.input, gotID, gotOK, tt.wantID, tt.wantOK)
			}
		})
	}
}

func TestPromptReview_Scenarios(t *testing.T) {
	origStdin := stdinReader
	defer func() { stdinReader = origStdin }()

	testItem := engine.ReviewItem{
		SourceIndex:    1,
		SourceTitle:    "Test Song",
		SourceArtists:  []string{"Artist A"},
		SourceDuration: "3:30",
		SourcePlatform: "spotify",
		TargetPlatform: "youtube-music",
		Options: []engine.ReviewOption{
			{TargetID: "vid_1", Title: "Candidate 1", Artists: []string{"Artist A"}, Duration: "3:30", Score: 65, TargetURL: "https://music.youtube.com/watch?v=vid_1"},
			{TargetID: "vid_2", Title: "Candidate 2", Artists: []string{"Artist B"}, Duration: "3:40", Score: 55, TargetURL: "https://music.youtube.com/watch?v=vid_2"},
		},
	}

	t.Run("Select option 1 with CRLF", func(t *testing.T) {
		stdinReader = strings.NewReader("1\r\n")
		id, confirmed, abort := promptReview(testItem)
		if !confirmed || abort || id != "vid_1" {
			t.Errorf("expected ('vid_1', true, false), got (%q, %v, %v)", id, confirmed, abort)
		}
	})

	t.Run("Default skip on empty enter", func(t *testing.T) {
		stdinReader = strings.NewReader("\n")
		id, confirmed, abort := promptReview(testItem)
		if confirmed || abort || id != "" {
			t.Errorf("expected ('', false, false), got (%q, %v, %v)", id, confirmed, abort)
		}
	})

	t.Run("Explicit skip with 's'", func(t *testing.T) {
		stdinReader = strings.NewReader("s\n")
		id, confirmed, abort := promptReview(testItem)
		if confirmed || abort || id != "" {
			t.Errorf("expected ('', false, false), got (%q, %v, %v)", id, confirmed, abort)
		}
	})

	t.Run("Abort all with 'a'", func(t *testing.T) {
		stdinReader = strings.NewReader("a\n")
		id, confirmed, abort := promptReview(testItem)
		if confirmed || !abort || id != "" {
			t.Errorf("expected ('', false, true), got (%q, %v, %v)", id, confirmed, abort)
		}
	})

	t.Run("Abort all with 'quit'", func(t *testing.T) {
		stdinReader = strings.NewReader("quit\n")
		id, confirmed, abort := promptReview(testItem)
		if confirmed || !abort || id != "" {
			t.Errorf("expected ('', false, true), got (%q, %v, %v)", id, confirmed, abort)
		}
	})

	t.Run("Custom ID workflow success", func(t *testing.T) {
		stdinReader = strings.NewReader("c\nhttps://youtu.be/dQw4w9WgXcQ\n")
		id, confirmed, abort := promptReview(testItem)
		if !confirmed || abort || id != "dQw4w9WgXcQ" {
			t.Errorf("expected ('dQw4w9WgXcQ', true, false), got (%q, %v, %v)", id, confirmed, abort)
		}
	})

	t.Run("Custom ID cancelled with back then select candidate 2", func(t *testing.T) {
		stdinReader = strings.NewReader("c\nback\n2\n")
		id, confirmed, abort := promptReview(testItem)
		if !confirmed || abort || id != "vid_2" {
			t.Errorf("expected ('vid_2', true, false), got (%q, %v, %v)", id, confirmed, abort)
		}
	})

	t.Run("Invalid input recovery", func(t *testing.T) {
		stdinReader = strings.NewReader("invalid_choice\n99\n1\n")
		id, confirmed, abort := promptReview(testItem)
		if !confirmed || abort || id != "vid_1" {
			t.Errorf("expected ('vid_1', true, false), got (%q, %v, %v)", id, confirmed, abort)
		}
	})

	t.Run("EOF on stdin aborts gracefully", func(t *testing.T) {
		stdinReader = strings.NewReader("")
		id, confirmed, abort := promptReview(testItem)
		if confirmed || !abort || id != "" {
			t.Errorf("expected ('', false, true) on EOF, got (%q, %v, %v)", id, confirmed, abort)
		}
	})

	t.Run("Custom ID EOF aborts gracefully", func(t *testing.T) {
		stdinReader = strings.NewReader("c\n")
		id, confirmed, abort := promptReview(testItem)
		if confirmed || !abort || id != "" {
			t.Errorf("expected ('', false, true) on Custom EOF, got (%q, %v, %v)", id, confirmed, abort)
		}
	})
}
