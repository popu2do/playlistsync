package engine_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"playlistsync/internal/auth"
	"playlistsync/internal/config"
	"playlistsync/internal/engine"
	"playlistsync/internal/model"
	"playlistsync/internal/spotify"
	"playlistsync/internal/ytmusic"
	"strings"
	"testing"
	"testing/quick"
)

func TestMain(m *testing.M) {
	tempOut, _ := os.MkdirTemp("", "engine_test_output_*")
	defer os.RemoveAll(tempOut)
	config.SetOutputDir(tempOut)

	reset := auth.SetBrowserLauncherForTesting(
		func() (string, error) {
			return "", fmt.Errorf("browser launch disabled in tests")
		},
		func(exe string, args []string) (*exec.Cmd, error) {
			return nil, fmt.Errorf("browser launch disabled in tests")
		},
	)
	defer reset()
	code := m.Run()
	_ = os.RemoveAll("output")
	os.Exit(code)
}

func TestNewSyncer_NonExistentCredentials_Deferred(t *testing.T) {
	cfg := engine.SyncConfig{
		YTMHeadersPath: "non_existent_browser_12345.json",
	}

	syncer, err := engine.NewSyncer(cfg)
	if err != nil {
		t.Fatalf("expected NewSyncer to succeed when credential file does not yet exist, got error: %v", err)
	}
	if syncer == nil {
		t.Fatalf("expected non-nil syncer")
	}
}

func TestNewSyncer_InvalidProxy(t *testing.T) {
	tempDir := t.TempDir()
	browserPath := filepath.Join(tempDir, "browser.json")
	if err := os.WriteFile(browserPath, []byte(`{"User-Agent": "test"}`), 0644); err != nil {
		t.Fatalf("failed to write browser json: %v", err)
	}

	cfg := engine.SyncConfig{
		YTMHeadersPath: browserPath,
		ProxyURL:       "://invalid-url",
	}

	syncer, err := engine.NewSyncer(cfg)
	if err == nil {
		t.Errorf("expected error for invalid proxy url, got nil")
	}
	if syncer != nil {
		t.Errorf("expected syncer to be nil on error")
	}
}

func TestNewSyncer_SuccessAndDefaults(t *testing.T) {
	tempDir := t.TempDir()
	browserPath := filepath.Join(tempDir, "browser.json")
	if err := os.WriteFile(browserPath, []byte(`{"User-Agent": "test", "Cookie": "SAPISID=test123"}`), 0644); err != nil {
		t.Fatalf("failed to write browser json: %v", err)
	}

	cfg := engine.SyncConfig{
		YTMHeadersPath: browserPath,
		ProxyURL:       "http://127.0.0.1:8080",
	}

	syncer, err := engine.NewSyncer(cfg)
	if err != nil {
		t.Fatalf("expected NewSyncer to succeed, got: %v", err)
	}
	if syncer == nil {
		t.Fatal("expected non-nil syncer")
	}
}

func TestSyncer_LoadKnownMapping(t *testing.T) {
	tempDir := t.TempDir()
	browserPath := filepath.Join(tempDir, "browser.json")
	if err := os.WriteFile(browserPath, []byte(`{"User-Agent": "test"}`), 0644); err != nil {
		t.Fatalf("failed to write browser json: %v", err)
	}

	resultPath := filepath.Join(tempDir, "prev_result.json")
	prevResult := model.SyncResult{
		AddedAfterReview: []model.AddedTrack{
			{Index: 1, Title: "Track 1", TargetTrackID: "vid_review_1"},
			{Index: 27, Title: "Track 27", TargetTrackID: "vid_review_27"},
		},
	}
	data, _ := json.Marshal(prevResult)
	if err := os.WriteFile(resultPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	cfg := engine.SyncConfig{
		YTMHeadersPath: browserPath,
		ResultJSONPath: resultPath,
	}

	syncer, err := engine.NewSyncer(cfg)
	if err != nil {
		t.Fatalf("failed to init syncer: %v", err)
	}

	sp := &model.SpotifyPlaylist{
		PlaylistName: "Test",
		Tracks: []model.SpotifyTrack{
			{Index: 1, Title: "Track 1"},
			{Index: 27, Title: "Track 27"},
		},
	}

	if syncer == nil {
		t.Fatal("syncer is nil")
	}
	_ = sp
}

func TestSyncer_Run_YouTubeToSpotify_Validation(t *testing.T) {
	tempDir := t.TempDir()
	spCredPath := filepath.Join(tempDir, "sp_cred.json")
	_ = auth.SaveCookie(spCredPath, "sp_dc=valid_sp_dc_1234567890")

	// Mock token server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"accessToken": "mock_token",
			"isAnonymous": false,
			"username":    "User",
		})
	}))
	defer ts.Close()

	resetAuth := auth.SetEndpointsForTesting(ts.URL+"/token", "", "")
	defer resetAuth()

	ytmCredPath := filepath.Join(tempDir, "ytm_cred.json")
	_ = auth.SaveRawCookieMap(ytmCredPath, "SAPISID=valid_sapisid_token; __Secure-3PAPISID=valid_secure_token")

	cfg := engine.SyncConfig{
		PlaylistName:    "NonExistentYTMPlaylist",
		Direction:       engine.DirectionYouTubeToSpotify,
		SpotifyAuthPath: spCredPath,
		YTMHeadersPath:  ytmCredPath,
		AutoYes:         true,
	}

	syncer, err := engine.NewSyncer(cfg)
	if err != nil {
		t.Fatal(err)
	}

	_, err = syncer.Run()
	if err == nil {
		t.Fatal("expected error when playlist not found, got nil")
	}
}

func TestSyncer_Run_SpotifyToYouTube_EndToEnd(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Setup mock YTM credentials and server
	ytmCredPath := filepath.Join(tempDir, "ytm_credentials.json")
	_ = auth.SaveRawCookieMap(ytmCredPath, "SAPISID=valid_sapisid_token")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)

		switch r.URL.Path {
		case "/youtubei/v1/playlist/create":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"playlistId": "PL_NEW_123",
			})
		case "/youtubei/v1/browse/edit_playlist":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "STATUS_SUCCEEDED",
			})
		case "/youtubei/v1/browse":
			browseID, _ := body["browseId"].(string)
			if browseID == "FEmusic_liked_playlists" {
				// Return no matching playlist initially to test creation
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"header": map[string]interface{}{
						"musicHeaderRenderer": map[string]interface{}{
							"title": map[string]interface{}{
								"runs": []map[string]interface{}{{"text": "Test Channel"}},
							},
						},
					},
					"contents": map[string]interface{}{
						"twoColumnBrowseResultsRenderer": map[string]interface{}{
							"tabs": []interface{}{
								map[string]interface{}{
									"tabRenderer": map[string]interface{}{
										"content": map[string]interface{}{
											"sectionListRenderer": map[string]interface{}{
												"contents": []interface{}{
													map[string]interface{}{
														"gridRenderer": map[string]interface{}{
															"items": []interface{}{},
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
			// Browse playlist
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"header": map[string]interface{}{
					"musicResponsiveHeaderRenderer": map[string]interface{}{
						"title": map[string]interface{}{
							"runs": []map[string]interface{}{{"text": "My Spotify Rock (Spotify import)"}},
						},
						"description": map[string]interface{}{
							"musicDescriptionShelfRenderer": map[string]interface{}{
								"description": map[string]interface{}{
									"runs": []map[string]interface{}{{"text": "Imported"}},
								},
							},
						},
					},
				},
				"contents": map[string]interface{}{
					"twoColumnBrowseResultsRenderer": map[string]interface{}{
						"tabs": []interface{}{
							map[string]interface{}{
								"tabRenderer": map[string]interface{}{
									"content": map[string]interface{}{
										"sectionListRenderer": map[string]interface{}{
											"contents": []interface{}{
												map[string]interface{}{
													"musicPlaylistShelfRenderer": map[string]interface{}{
														"contents": []interface{}{
															map[string]interface{}{
																"musicResponsiveListItemRenderer": map[string]interface{}{
																	"playlistItemData": map[string]interface{}{
																		"videoId":            "extra_vid_99",
																		"playlistSetVideoId": "set_extra_99",
																	},
																	"flexColumns": []interface{}{
																		map[string]interface{}{
																			"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																				"text": map[string]interface{}{
																					"runs": []map[string]interface{}{{"text": "Extra Song"}},
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
		case "/youtubei/v1/search":
			query, _ := body["query"].(string)
			if strings.Contains(query, "High Confidence") {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"contents": map[string]interface{}{
						"tabbedSearchResultsRenderer": map[string]interface{}{
							"tabs": []interface{}{
								map[string]interface{}{
									"tabRenderer": map[string]interface{}{
										"content": map[string]interface{}{
											"sectionListRenderer": map[string]interface{}{
												"contents": []interface{}{
													map[string]interface{}{
														"musicShelfRenderer": map[string]interface{}{
															"contents": []interface{}{
																map[string]interface{}{
																	"musicResponsiveListItemRenderer": map[string]interface{}{
																		"playlistItemData": map[string]interface{}{
																			"videoId": "vid_high_conf",
																		},
																		"flexColumns": []interface{}{
																			map[string]interface{}{
																				"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																					"text": map[string]interface{}{
																						"runs": []map[string]interface{}{{"text": "High Confidence Song"}},
																					},
																				},
																			},
																			map[string]interface{}{
																				"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																					"text": map[string]interface{}{
																						"runs": []map[string]interface{}{{"text": "Band A"}},
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
			// Low confidence candidate
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"contents": map[string]interface{}{
					"tabbedSearchResultsRenderer": map[string]interface{}{
						"tabs": []interface{}{
							map[string]interface{}{
								"tabRenderer": map[string]interface{}{
									"content": map[string]interface{}{
										"sectionListRenderer": map[string]interface{}{
											"contents": []interface{}{
												map[string]interface{}{
													"musicShelfRenderer": map[string]interface{}{
														"contents": []interface{}{
															map[string]interface{}{
																"musicResponsiveListItemRenderer": map[string]interface{}{
																	"playlistItemData": map[string]interface{}{
																		"videoId": "vid_unrelated",
																	},
																	"flexColumns": []interface{}{
																		map[string]interface{}{
																			"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																				"text": map[string]interface{}{
																					"runs": []map[string]interface{}{{"text": "Totally Different Title"}},
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
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	resetAuth := auth.SetEndpointsForTesting("", "", ts.URL+"/youtubei/v1/browse")
	defer resetAuth()

	// Redirect endpoints
	origBrowse := ytmusic.EndpointBrowse
	origEdit := ytmusic.EndpointEditPlaylist
	origSearch := ytmusic.EndpointSearch
	origCreate := ytmusic.EndpointCreatePlaylist
	defer func() {
		ytmusic.EndpointBrowse = origBrowse
		ytmusic.EndpointEditPlaylist = origEdit
		ytmusic.EndpointSearch = origSearch
		ytmusic.EndpointCreatePlaylist = origCreate
	}()

	ytmusic.EndpointBrowse = ts.URL + "/youtubei/v1/browse"
	ytmusic.EndpointEditPlaylist = ts.URL + "/youtubei/v1/browse/edit_playlist"
	ytmusic.EndpointSearch = ts.URL + "/youtubei/v1/search"
	ytmusic.EndpointCreatePlaylist = ts.URL + "/youtubei/v1/playlist/create"

	// 2. Setup mock Spotify playlist
	outputDir := filepath.Join(tempDir, "output")
	_ = os.MkdirAll(outputDir, 0755)

	spPlaylist := &model.SpotifyPlaylist{
		PlaylistName:      "My Spotify Rock",
		SourcePlaylistURL: "https://open.spotify.com/playlist/sp_rock_123",
		ExpectedCount:     3,
		CollectedCount:    3,
		Tracks: []model.SpotifyTrack{
			{
				Index:    1,
				Title:    "High Confidence Song",
				Artists:  []string{"Band A"},
				Duration: "3:30",
				Query:    "High Confidence Song Band A",
			},
			{
				Index:    2,
				Title:    "Obscure Underground Song",
				Artists:  []string{"Band B"},
				Duration: "4:00",
			},
			{
				Index:    3,
				Title:    "Pre-Reviewed Song",
				Artists:  []string{"Band C"},
				Duration: "2:45",
			},
		},
	}
	spPath := filepath.Join(outputDir, "spotify_my spotify rock_source.json")
	if err := spotify.WritePlaylistJSON(spPath, spPlaylist); err != nil {
		t.Fatal(err)
	}

	resultPath := filepath.Join(tempDir, "sync_result.json")
	reportPath := filepath.Join(tempDir, "sync_report.json")

	// Pre-populate previous review for track 3
	prevResult := model.SyncResult{
		AddedAfterReview: []model.AddedTrack{
			{Index: 3, Title: "Pre-Reviewed Song", Artists: []string{"Band C"}, TargetTrackID: "vid_reviewed_3"},
		},
	}
	prevData, _ := json.Marshal(prevResult)
	_ = os.WriteFile(resultPath, prevData, 0644)

	cfg := engine.SyncConfig{
		PlaylistName:    spPath, // Pass exact path as name or search candidate
		Direction:       engine.DirectionSpotifyToYouTube,
		YTMHeadersPath:  ytmCredPath,
		ResultJSONPath:  resultPath,
		FinalReportPath: reportPath,
		CleanExtra:      true,
	}

	syncer, err := engine.NewSyncer(cfg)
	if err != nil {
		t.Fatalf("failed to init syncer: %v", err)
	}

	result, err := syncer.Run()
	if err != nil {
		t.Fatalf("syncer.Run failed: %v", err)
	}

	if result.PlaylistID != "PL_NEW_123" {
		t.Errorf("expected playlistId PL_NEW_123, got %s", result.PlaylistID)
	}
	if len(result.RemovedExtraTracks) != 1 || result.RemovedExtraTracks[0].TargetTrackID != "extra_vid_99" {
		t.Errorf("expected removed extra track extra_vid_99, got %+v", result.RemovedExtraTracks)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Title != "Obscure Underground Song" {
		t.Errorf("expected 1 skipped track for obscure song, got %+v", result.Skipped)
	}

	// Verify result and report files generated on disk
	if _, err := os.Stat(resultPath); err != nil {
		t.Errorf("result file not written at %s", resultPath)
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Errorf("report file not written at %s", reportPath)
	}
}

func TestSyncer_NewSyncer_InvalidProxy(t *testing.T) {
	cfg := engine.SyncConfig{
		PlaylistName: "test",
		ProxyURL:     "://invalid-url",
	}
	_, err := engine.NewSyncer(cfg)
	if err == nil {
		t.Fatal("expected error for invalid proxy url, got nil")
	}
}

func TestSyncer_Run_ErrorsAndBranches(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("Spotify auth error in YouTubeToSpotify direction", func(t *testing.T) {
		cfg := engine.SyncConfig{
			PlaylistName:    "test",
			Direction:       engine.DirectionYouTubeToSpotify,
			SpotifyAuthPath: filepath.Join(tempDir, "non_existent_sp.json"),
		}
		s, err := engine.NewSyncer(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Run(); err == nil {
			t.Fatal("expected error for missing spotify auth")
		}
	})

	t.Run("YTM auth error in SpotifyToYouTube direction", func(t *testing.T) {
		cfg := engine.SyncConfig{
			PlaylistName:   "test",
			Direction:      engine.DirectionSpotifyToYouTube,
			YTMHeadersPath: filepath.Join(tempDir, "non_existent_ytm.json"),
		}
		s, err := engine.NewSyncer(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Run(); err == nil {
			t.Fatal("expected error for missing ytm auth")
		}
	})

	t.Run("Spotify playlist not found error", func(t *testing.T) {
		ytmCred := filepath.Join(tempDir, "valid_ytm.json")
		_ = auth.SaveRawCookieMap(ytmCred, "SAPISID=mock_sapisid; __Secure-3PAPISID=mock_sec")

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"header": map[string]interface{}{
					"musicHeaderRenderer": map[string]interface{}{
						"title": map[string]interface{}{
							"runs": []map[string]interface{}{{"text": "YTM Test User"}},
						},
					},
				},
			})
		}))
		defer ts.Close()

		resetAuth := auth.SetEndpointsForTesting("", "", ts.URL+"/browse")
		defer resetAuth()

		cfg := engine.SyncConfig{
			PlaylistName:   "non_existent_playlist_name_xyz_123",
			Direction:      engine.DirectionSpotifyToYouTube,
			YTMHeadersPath: ytmCred,
		}
		s, err := engine.NewSyncer(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Run(); err == nil {
			t.Fatal("expected error for non-existent spotify playlist")
		}
	})
}

func TestGenerateSearchQueries(t *testing.T) {
	t.Run("Bracketed song title with single artist", func(t *testing.T) {
		track := model.SpotifyTrack{
			Title:   "《晴天》",
			Artists: []string{"周杰伦"},
		}
		queries := engine.GenerateSearchQueries(track)
		// Expected queries in order:
		// 1. Cleaned title + primary artist: "晴天 周杰伦"
		// 2. Cleaned title + all artists: "晴天 周杰伦" (deduped)
		// 3. Raw title + primary artist: "《晴天》 周杰伦"
		// 4. Cleaned title only: "晴天"
		// 5. Raw title only: "《晴天》"
		expected := []string{"晴天 周杰伦", "《晴天》 周杰伦", "晴天", "《晴天》"}
		if len(queries) != len(expected) {
			t.Fatalf("expected %d queries, got %d: %+v", len(expected), len(queries), queries)
		}
		for i := range expected {
			if queries[i] != expected[i] {
				t.Errorf("query[%d] = %q; want %q", i, queries[i], expected[i])
			}
		}
	})

	t.Run("Multiple artists and noisy title", func(t *testing.T) {
		track := model.SpotifyTrack{
			Title:   "Shape of You (feat. Stormzy)",
			Artists: []string{"Ed Sheeran", "Stormzy"},
		}
		queries := engine.GenerateSearchQueries(track)
		// Expected:
		// 1. "Shape of You Ed Sheeran"
		// 2. "Shape of You Ed Sheeran Stormzy"
		// 3. "Shape of You (feat. Stormzy) Ed Sheeran"
		// 3b. "Shape of You (feat. Stormzy) Ed Sheeran Stormzy"
		// 4. "Shape of You"
		// 5. "Shape of You (feat. Stormzy)"
		expected := []string{
			"Shape of You Ed Sheeran",
			"Shape of You Ed Sheeran Stormzy",
			"Shape of You (feat. Stormzy) Ed Sheeran",
			"Shape of You (feat. Stormzy) Ed Sheeran Stormzy",
			"Shape of You",
			"Shape of You (feat. Stormzy)",
		}
		if len(queries) != len(expected) {
			t.Fatalf("expected %d queries, got %d: %+v", len(expected), len(queries), queries)
		}
		for i := range expected {
			if queries[i] != expected[i] {
				t.Errorf("query[%d] = %q; want %q", i, queries[i], expected[i])
			}
		}
	})

	t.Run("No artists provided (instrumental track)", func(t *testing.T) {
		track := model.SpotifyTrack{
			Title:   "Interlude (Live)",
			Artists: []string{},
		}
		queries := engine.GenerateSearchQueries(track)
		expected := []string{"Interlude", "Interlude (Live)"}
		if len(queries) != len(expected) {
			t.Fatalf("expected %d queries, got %d: %+v", len(expected), len(queries), queries)
		}
		for i := range expected {
			if queries[i] != expected[i] {
				t.Errorf("query[%d] = %q; want %q", i, queries[i], expected[i])
			}
		}
	})

	t.Run("Query override field present on Spotify track", func(t *testing.T) {
		track := model.SpotifyTrack{
			Title:   "Song",
			Artists: []string{"Artist"},
			Query:   "Custom Query From Track",
		}
		queries := engine.GenerateSearchQueries(track)
		if len(queries) == 0 || queries[0] != "Custom Query From Track" {
			t.Errorf("expected first query to be custom query, got: %+v", queries)
		}
	})

	t.Run("Syntax and dual-language decomposition", func(t *testing.T) {
		// Case 1: Chinese dash dual-language title "那天我其实有点坏 — I Wasn’t That Good That Day"
		track1 := model.SpotifyTrack{
			Title:   "那天我其实有点坏 — I Wasn’t That Good That Day",
			Artists: []string{"Shi Shi"},
		}
		queries1 := engine.GenerateSearchQueries(track1)
		containsQ := func(list []string, target string) bool {
			for _, q := range list {
				if q == target {
					return true
				}
			}
			return false
		}
		if !containsQ(queries1, "那天我其实有点坏 Shi Shi") {
			t.Errorf("Expected queries to contain '那天我其实有点坏 Shi Shi', got: %+v", queries1)
		}
		if !containsQ(queries1, "I Wasn't That Good That Day Shi Shi") && !containsQ(queries1, "I Wasn’t That Good That Day Shi Shi") {
			t.Errorf("Expected queries to contain 'I Wasn't That Good That Day Shi Shi', got: %+v", queries1)
		}

		// Case 2: Bracket subtitle "十二時の夕立(Coffee Time In The Rain)"
		track2 := model.SpotifyTrack{
			Title:   "十二時の夕立(Coffee Time In The Rain)",
			Artists: []string{"Yu Hayashi"},
		}
		queries2 := engine.GenerateSearchQueries(track2)
		if !containsQ(queries2, "Coffee Time In The Rain Yu Hayashi") {
			t.Errorf("Expected queries to contain 'Coffee Time In The Rain Yu Hayashi', got: %+v", queries2)
		}
		if !containsQ(queries2, "十二時の夕立 Yu Hayashi") {
			t.Errorf("Expected queries to contain '十二時の夕立 Yu Hayashi', got: %+v", queries2)
		}

		// Case 3: Dual-language space separated title "好的一天 A Good Day"
		track3 := model.SpotifyTrack{
			Title:   "好的一天 A Good Day",
			Artists: []string{"Dean Ting"},
		}
		queries3 := engine.GenerateSearchQueries(track3)
		if !containsQ(queries3, "好的一天 A Good Day Dean Ting") {
			t.Errorf("Expected queries to contain '好的一天 A Good Day Dean Ting', got: %+v", queries3)
		}
	})
}

func TestSyncer_Run_InteractiveReviewTwoPhaseFlow(t *testing.T) {
	tempDir := t.TempDir()

	authDir := filepath.Join(tempDir, "auth")
	_ = os.MkdirAll(authDir, 0755)
	ytmCredPath := filepath.Join(authDir, "ytmusic_headers.json")
	_ = auth.SaveRawCookieMap(ytmCredPath, "SAPISID=valid_sapisid_token; __Secure-3PAPISID=valid_sapisid_token; SID=valid_sid; HSID=valid_hsid; SSID=valid_ssid; APISID=valid_apisid")

	addedVideoIDs := make([]string, 0)
	ytmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)

		switch r.URL.Path {
		case "/youtubei/v1/browse":
			browseID, _ := body["browseId"].(string)
			if browseID == "FEmusic_liked_playlists" {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"header": map[string]interface{}{
						"musicHeaderRenderer": map[string]interface{}{
							"title": map[string]interface{}{
								"runs": []map[string]interface{}{{"text": "Test Channel"}},
							},
						},
					},
					"contents": map[string]interface{}{
						"twoColumnBrowseResultsRenderer": map[string]interface{}{
							"tabs": []interface{}{
								map[string]interface{}{
									"tabRenderer": map[string]interface{}{
										"content": map[string]interface{}{
											"sectionListRenderer": map[string]interface{}{
												"contents": []interface{}{
													map[string]interface{}{
														"gridRenderer": map[string]interface{}{
															"items": []interface{}{},
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
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"header": map[string]interface{}{
					"musicResponsiveHeaderRenderer": map[string]interface{}{
						"title": map[string]interface{}{
							"runs": []map[string]interface{}{{"text": "Review Playlist"}},
						},
					},
				},
				"contents": map[string]interface{}{
					"twoColumnBrowseResultsRenderer": map[string]interface{}{
						"tabs": []interface{}{
							map[string]interface{}{
								"tabRenderer": map[string]interface{}{
									"content": map[string]interface{}{
										"sectionListRenderer": map[string]interface{}{
											"contents": []interface{}{
												map[string]interface{}{
													"musicPlaylistShelfRenderer": map[string]interface{}{
														"contents": []interface{}{},
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
		case "/youtubei/v1/search":
			// Return a mid-confidence candidate (score 55 due to artist mismatch)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"contents": map[string]interface{}{
					"tabbedSearchResultsRenderer": map[string]interface{}{
						"tabs": []interface{}{
							map[string]interface{}{
								"tabRenderer": map[string]interface{}{
									"content": map[string]interface{}{
										"sectionListRenderer": map[string]interface{}{
											"contents": []interface{}{
												map[string]interface{}{
													"musicShelfRenderer": map[string]interface{}{
														"contents": []interface{}{
															map[string]interface{}{
																"musicResponsiveListItemRenderer": map[string]interface{}{
																	"playlistItemData": map[string]interface{}{
																		"videoId": "custom_vid_123",
																	},
																	"flexColumns": []interface{}{
																		map[string]interface{}{
																			"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																				"text": map[string]interface{}{
																					"runs": []map[string]interface{}{{"text": "Rare Ambiguous Track"}},
																				},
																			},
																		},
																		map[string]interface{}{
																			"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																				"text": map[string]interface{}{
																					"runs": []map[string]interface{}{{"text": "Other Artist"}},
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
		case "/youtubei/v1/browse/edit_playlist":
			actions, _ := body["actions"].([]interface{})
			for _, act := range actions {
				if actMap, ok := act.(map[string]interface{}); ok {
					if actType, _ := actMap["action"].(string); actType == "ACTION_ADD_VIDEO" {
						addedVideoIDs = append(addedVideoIDs, actMap["addedVideoId"].(string))
					}
				}
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "STATUS_SUCCEEDED"})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ytmServer.Close()

	resetAuth := auth.SetEndpointsForTesting("", "", ytmServer.URL+"/youtubei/v1/browse")
	defer resetAuth()

	origBrowse := ytmusic.EndpointBrowse
	origEdit := ytmusic.EndpointEditPlaylist
	origSearch := ytmusic.EndpointSearch
	defer func() {
		ytmusic.EndpointBrowse = origBrowse
		ytmusic.EndpointEditPlaylist = origEdit
		ytmusic.EndpointSearch = origSearch
	}()

	ytmusic.EndpointBrowse = ytmServer.URL + "/youtubei/v1/browse"
	ytmusic.EndpointEditPlaylist = ytmServer.URL + "/youtubei/v1/browse/edit_playlist"
	ytmusic.EndpointSearch = ytmServer.URL + "/youtubei/v1/search"

	spPath := filepath.Join(tempDir, "spotify_source.json")
	sourceSP := &model.SpotifyPlaylist{
		PlaylistName:      "Review Playlist",
		SourcePlaylistURL: "https://open.spotify.com/playlist/test_pl",
		Tracks: []model.SpotifyTrack{
			{Index: 1, Title: "Rare Ambiguous Track", Artists: []string{"Original Artist"}, Duration: "3:30"},
		},
	}
	spData, _ := json.Marshal(sourceSP)
	_ = os.WriteFile(spPath, spData, 0644)

	resPath := filepath.Join(tempDir, "result.json")
	repPath := filepath.Join(tempDir, "report.json")

	reviewPromptCalled := false
	cfg := engine.SyncConfig{
		PlaylistName:     "Review Playlist",
		PlaylistJSONPath: spPath,
		Direction:        engine.DirectionSpotifyToYouTube,
		PlaylistID:       "PL_test_123",
		OutputDir:        tempDir,
		YTMHeadersPath:   ytmCredPath,
		ResultJSONPath:   resPath,
		FinalReportPath:  repPath,
		AutoYes:          false,
		ReviewPrompt: func(item engine.ReviewItem) (string, bool, bool) {
			reviewPromptCalled = true
			if len(item.Options) > 0 {
				return item.Options[0].TargetID, true, false
			}
			return "", false, false
		},
	}

	syncer, err := engine.NewSyncer(cfg)
	if err != nil {
		t.Fatalf("NewSyncer failed: %v", err)
	}

	res, err := syncer.Run()
	if err != nil {
		t.Fatalf("syncer.Run failed: %v", err)
	}

	if !reviewPromptCalled {
		t.Errorf("Expected ReviewPrompt to be called for ambiguous candidate, but was not")
	}
	if len(res.AddedAfterReview) != 1 || res.AddedAfterReview[0].TargetTrackID != "custom_vid_123" {
		t.Errorf("Expected 1 AddedAfterReview item with TargetTrackID 'custom_vid_123', got: %+v", res.AddedAfterReview)
	}
}

func TestSyncer_Run_YouTubeToSpotify_EndToEnd_CreationAndMatching(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Setup mock YTM credentials and server
	ytmCredPath := filepath.Join(tempDir, "ytm_credentials.json")
	_ = auth.SaveRawCookieMap(ytmCredPath, "SAPISID=valid_sapisid_token")

	ytmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)

		switch r.URL.Path {
		case "/youtubei/v1/browse":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"header": map[string]interface{}{
					"musicResponsiveHeaderRenderer": map[string]interface{}{
						"title": map[string]interface{}{
							"runs": []map[string]interface{}{{"text": "My YTM Rock Playlist"}},
						},
						"description": map[string]interface{}{
							"musicDescriptionShelfRenderer": map[string]interface{}{
								"description": map[string]interface{}{
									"runs": []map[string]interface{}{{"text": "Best Rock Songs"}},
								},
							},
						},
					},
				},
				"contents": map[string]interface{}{
					"twoColumnBrowseResultsRenderer": map[string]interface{}{
						"tabs": []interface{}{
							map[string]interface{}{
								"tabRenderer": map[string]interface{}{
									"content": map[string]interface{}{
										"sectionListRenderer": map[string]interface{}{
											"contents": []interface{}{
												map[string]interface{}{
													"musicPlaylistShelfRenderer": map[string]interface{}{
														"contents": []interface{}{
															map[string]interface{}{
																"musicResponsiveListItemRenderer": map[string]interface{}{
																	"playlistItemData": map[string]interface{}{
																		"videoId": "ytm_track_1",
																	},
																	"flexColumns": []interface{}{
																		map[string]interface{}{
																			"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																				"text": map[string]interface{}{
																					"runs": []map[string]interface{}{{"text": "Qingtian"}},
																				},
																			},
																		},
																		map[string]interface{}{
																			"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																				"text": map[string]interface{}{
																					"runs": []map[string]interface{}{{"text": "Jay Chou"}},
																				},
																			},
																		},
																		map[string]interface{}{
																			"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																				"text": map[string]interface{}{
																					"runs": []map[string]interface{}{{"text": "4:29"}},
																				},
																			},
																		},
																	},
																},
															},
															map[string]interface{}{
																"musicResponsiveListItemRenderer": map[string]interface{}{
																	"playlistItemData": map[string]interface{}{
																		"videoId": "ytm_track_2",
																	},
																	"flexColumns": []interface{}{
																		map[string]interface{}{
																			"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																				"text": map[string]interface{}{
																					"runs": []map[string]interface{}{{"text": "Obscure Low Confidence Song"}},
																				},
																			},
																		},
																		map[string]interface{}{
																			"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																				"text": map[string]interface{}{
																					"runs": []map[string]interface{}{{"text": "Unknown Artist"}},
																				},
																			},
																		},
																		map[string]interface{}{
																			"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																				"text": map[string]interface{}{
																					"runs": []map[string]interface{}{{"text": "3:00"}},
																				},
																			},
																		},
																	},
																},
															},
															map[string]interface{}{
																"musicResponsiveListItemRenderer": map[string]interface{}{
																	"playlistItemData": map[string]interface{}{
																		"videoId": "ytm_track_3",
																	},
																	"flexColumns": []interface{}{
																		map[string]interface{}{
																			"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																				"text": map[string]interface{}{
																					"runs": []map[string]interface{}{{"text": "Unavailable Ghost Song"}},
																				},
																			},
																		},
																		map[string]interface{}{
																			"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																				"text": map[string]interface{}{
																					"runs": []map[string]interface{}{{"text": "Phantom Artist"}},
																				},
																			},
																		},
																		map[string]interface{}{
																			"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																				"text": map[string]interface{}{
																					"runs": []map[string]interface{}{{"text": "2:50"}},
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
		default:
			http.NotFound(w, r)
		}
	}))
	defer ytmServer.Close()

	// 2. Setup mock Spotify credentials and server
	spCredPath := filepath.Join(tempDir, "sp_credentials.json")
	_ = auth.SaveCookie(spCredPath, "sp_dc=valid_sp_dc_1234567890")

	var addedSpotifyURIs []string
	var createdSpotifyPlaylists []string
	createdPlaylistTracks := make([]map[string]interface{}, 0)

	spServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/token" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"accessToken": "mock_spotify_token",
				"isAnonymous": false,
			})
			return
		}

		if r.URL.Path == "/v1/me" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "mock_spotify_user_1",
			})
			return
		}

		// Check user playlists (initially empty to test auto-creation)
		if r.URL.Path == "/v1/me/playlists" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []map[string]interface{}{},
				"next":  "",
			})
			return
		}

		// Create playlist
		if r.URL.Path == "/v1/me/playlists" && r.Method == http.MethodPost {
			var payload map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			createdName, _ := payload["name"].(string)
			createdSpotifyPlaylists = append(createdSpotifyPlaylists, createdName)

			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   "sp_created_pl_100",
				"name": createdName,
			})
			return
		}

		// Search Spotify catalog
		if strings.HasPrefix(r.URL.Path, "/v1/search") {
			q := r.URL.Query().Get("q")
			tType := r.URL.Query().Get("type")

			if tType == "playlist" {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"playlists": map[string]interface{}{
						"items": []map[string]interface{}{},
					},
				})
				return
			}

			if strings.Contains(q, "Qingtian") || strings.Contains(q, "Jay Chou") {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"tracks": map[string]interface{}{
						"items": []map[string]interface{}{
							{
								"id":          "sp_track_jay_1",
								"name":        "Qingtian",
								"duration_ms": 269000,
								"artists": []map[string]interface{}{
									{"name": "Jay Chou"},
								},
								"album": map[string]interface{}{
									"name": "Yeh Hui-mei",
								},
							},
						},
					},
				})
				return
			}

			if strings.Contains(q, "Obscure Low Confidence Song") {
				// Return low confidence candidate
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"tracks": map[string]interface{}{
						"items": []map[string]interface{}{
							{
								"id":          "sp_track_different_9",
								"name":        "Completely Different Title",
								"duration_ms": 120000,
								"artists": []map[string]interface{}{
									{"name": "Another Unrelated Artist"},
								},
								"album": map[string]interface{}{
									"name": "Another Album",
								},
							},
						},
					},
				})
				return
			}

			// Unavailable song: empty search
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"tracks": map[string]interface{}{
					"items": []map[string]interface{}{},
				},
			})
			return
		}

		// Add tracks to playlist
		if strings.HasSuffix(r.URL.Path, "/tracks") && r.Method == http.MethodPost {
			var payload struct {
				URIs []string `json:"uris"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			addedSpotifyURIs = append(addedSpotifyURIs, payload.URIs...)

			for _, u := range payload.URIs {
				trackID := strings.TrimPrefix(u, "spotify:track:")
				createdPlaylistTracks = append(createdPlaylistTracks, map[string]interface{}{
					"track": map[string]interface{}{
						"id":          trackID,
						"name":        "Qingtian",
						"duration_ms": 269000,
						"artists":     []map[string]interface{}{{"name": "Jay Chou"}},
						"album":       map[string]interface{}{"name": "Yeh Hui-mei"},
					},
				})
			}

			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"snapshot_id": "snapshot_123",
			})
			return
		}

		// Get playlist details
		if strings.HasPrefix(r.URL.Path, "/v1/playlists/sp_created_pl_100") && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "sp_created_pl_100",
				"name":        "My YTM Rock Playlist (YouTube Music import)",
				"description": "Imported from YouTube Music playlist: My YTM Rock Playlist",
				"tracks": map[string]interface{}{
					"items": createdPlaylistTracks,
					"next":  "",
					"total": len(createdPlaylistTracks),
				},
			})
			return
		}

		http.NotFound(w, r)
	}))
	defer spServer.Close()

	// Redirect test endpoints
	resetAuth := auth.SetEndpointsForTesting(spServer.URL+"/token", spServer.URL+"/v1/me", ytmServer.URL+"/youtubei/v1/browse")
	defer resetAuth()

	origYtmBrowse := ytmusic.EndpointBrowse
	defer func() { ytmusic.EndpointBrowse = origYtmBrowse }()
	ytmusic.EndpointBrowse = ytmServer.URL + "/youtubei/v1/browse"

	origSpMe := spotify.EndpointMe
	origSpMePlaylists := spotify.EndpointMePlaylists
	origSpPlaylists := spotify.EndpointPlaylists
	origSpSearch := spotify.EndpointSearch
	defer func() {
		spotify.EndpointMe = origSpMe
		spotify.EndpointMePlaylists = origSpMePlaylists
		spotify.EndpointPlaylists = origSpPlaylists
		spotify.EndpointSearch = origSpSearch
	}()

	spotify.EndpointMe = spServer.URL + "/v1/me"
	spotify.EndpointMePlaylists = spServer.URL + "/v1/me/playlists"
	spotify.EndpointPlaylists = spServer.URL + "/v1/playlists"
	spotify.EndpointSearch = spServer.URL + "/v1/search"

	resultPath := filepath.Join(tempDir, "custom_result.json")
	reportPath := filepath.Join(tempDir, "custom_report.json")

	cfg := engine.SyncConfig{
		PlaylistName:    "https://music.youtube.com/playlist?list=PL_TEST_YTM_123",
		Direction:       engine.DirectionYouTubeToSpotify,
		SpotifyAuthPath: spCredPath,
		YTMHeadersPath:  ytmCredPath,
		ResultJSONPath:  resultPath,
		FinalReportPath: reportPath,
		AutoYes:         true,
	}

	syncer, err := engine.NewSyncer(cfg)
	if err != nil {
		t.Fatalf("failed to init syncer: %v", err)
	}

	res, err := syncer.Run()
	if err != nil {
		t.Fatalf("syncer.Run failed: %v", err)
	}

	if res == nil {
		t.Fatal("expected non-nil sync result")
	}

	// Verify stats
	if res.TotalSourceTracks != 3 {
		t.Errorf("expected TotalSourceTracks = 3, got %d", res.TotalSourceTracks)
	}
	if res.AddedTracks != 1 {
		t.Errorf("expected AddedTracks = 1, got %d", res.AddedTracks)
	}
	if res.SkippedTracks != 2 {
		t.Errorf("expected SkippedTracks = 2, got %d", res.SkippedTracks)
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("expected 2 skipped tracks, got %d", len(res.Skipped))
	}
	if res.Skipped[0].Title != "Obscure Low Confidence Song" || !strings.Contains(res.Skipped[0].Reason, "low confidence") {
		t.Errorf("unexpected skipped track 0: %+v", res.Skipped[0])
	}
	if res.Skipped[1].Title != "Unavailable Ghost Song" || !strings.Contains(res.Skipped[1].Reason, "unavailable") {
		t.Errorf("unexpected skipped track 1: %+v", res.Skipped[1])
	}
	if res.Verification == nil || res.Verification.PageTrackCount != 1 {
		t.Errorf("unexpected verification: %+v", res.Verification)
	}

	// Verify files written
	if _, err := os.Stat(resultPath); err != nil {
		t.Errorf("result json file was not created: %v", err)
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Errorf("report json file was not created: %v", err)
	}
}

func TestSyncer_Run_YouTubeToSpotify_DeduplicationAndCleanExtra(t *testing.T) {
	tempDir := t.TempDir()

	// Setup mock YTM
	ytmCredPath := filepath.Join(tempDir, "ytm_credentials.json")
	_ = auth.SaveRawCookieMap(ytmCredPath, "SAPISID=valid_sapisid_token")

	ytmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"header": map[string]interface{}{
				"musicResponsiveHeaderRenderer": map[string]interface{}{
					"title": map[string]interface{}{
						"runs": []map[string]interface{}{{"text": "Pop Mix"}},
					},
				},
			},
			"contents": map[string]interface{}{
				"twoColumnBrowseResultsRenderer": map[string]interface{}{
					"tabs": []interface{}{
						map[string]interface{}{
							"tabRenderer": map[string]interface{}{
								"content": map[string]interface{}{
									"sectionListRenderer": map[string]interface{}{
										"contents": []interface{}{
											map[string]interface{}{
												"musicPlaylistShelfRenderer": map[string]interface{}{
													"contents": []interface{}{
														map[string]interface{}{
															"musicResponsiveListItemRenderer": map[string]interface{}{
																"playlistItemData": map[string]interface{}{"videoId": "ytm_song_1"},
																"flexColumns": []interface{}{
																	map[string]interface{}{"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{"text": map[string]interface{}{"runs": []map[string]interface{}{{"text": "Already Present Song"}}}}},
																	map[string]interface{}{"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{"text": map[string]interface{}{"runs": []map[string]interface{}{{"text": "Artist A"}}}}},
																	map[string]interface{}{"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{"text": map[string]interface{}{"runs": []map[string]interface{}{{"text": "3:30"}}}}},
																},
															},
														},
														map[string]interface{}{
															"musicResponsiveListItemRenderer": map[string]interface{}{
																"playlistItemData": map[string]interface{}{"videoId": "ytm_song_2"},
																"flexColumns": []interface{}{
																	map[string]interface{}{"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{"text": map[string]interface{}{"runs": []map[string]interface{}{{"text": "Brand New Song"}}}}},
																	map[string]interface{}{"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{"text": map[string]interface{}{"runs": []map[string]interface{}{{"text": "Artist B"}}}}},
																	map[string]interface{}{"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{"text": map[string]interface{}{"runs": []map[string]interface{}{{"text": "4:00"}}}}},
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
	}))
	defer ytmServer.Close()

	// Setup mock Spotify
	spCredPath := filepath.Join(tempDir, "sp_credentials.json")
	_ = auth.SaveCookie(spCredPath, "sp_dc=valid_sp_dc_1234567890")

	var addedURIs []string
	var removedURIs []string

	spServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/token" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"accessToken": "sp_token", "isAnonymous": false})
			return
		}
		if r.URL.Path == "/v1/me" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sp_user"})
			return
		}

		// User playlists (FindPlaylist finds existing "Pop Mix")
		if r.URL.Path == "/v1/me/playlists" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []map[string]interface{}{
					{"id": "sp_pop_mix_id", "name": "Pop Mix"},
				},
				"next": "",
			})
			return
		}

		// Search Spotify catalog for "Brand New Song"
		if strings.HasPrefix(r.URL.Path, "/v1/search") {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"tracks": map[string]interface{}{
					"items": []map[string]interface{}{
						{
							"id":          "sp_track_new_2",
							"name":        "Brand New Song",
							"duration_ms": 240000,
							"artists":     []map[string]interface{}{{"name": "Artist B"}},
							"album":       map[string]interface{}{"name": "Album B"},
						},
					},
				},
			})
			return
		}

		// Add tracks
		if strings.HasSuffix(r.URL.Path, "/tracks") && r.Method == http.MethodPost {
			var payload struct {
				URIs []string `json:"uris"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			addedURIs = append(addedURIs, payload.URIs...)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"snapshot_id": "snap1"})
			return
		}

		// Remove tracks
		if strings.HasSuffix(r.URL.Path, "/tracks") && r.Method == http.MethodDelete {
			var payload struct {
				Tracks []struct {
					URI string `json:"uri"`
				} `json:"tracks"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			for _, t := range payload.Tracks {
				removedURIs = append(removedURIs, t.URI)
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"snapshot_id": "snap2"})
			return
		}

		// Get playlist
		if strings.HasPrefix(r.URL.Path, "/v1/playlists/sp_pop_mix_id") && r.Method == http.MethodGet {
			// Initially contains "Already Present Song" and "Extraneous Song"
			// After additions & deletions, contains "Already Present Song" and "Brand New Song"
			var items []map[string]interface{}
			if len(removedURIs) > 0 {
				items = []map[string]interface{}{
					{
						"track": map[string]interface{}{
							"id": "sp_present_1", "name": "Already Present Song", "duration_ms": 210000,
							"artists": []map[string]interface{}{{"name": "Artist A"}}, "album": map[string]interface{}{"name": "Alb"},
						},
					},
					{
						"track": map[string]interface{}{
							"id": "sp_track_new_2", "name": "Brand New Song", "duration_ms": 240000,
							"artists": []map[string]interface{}{{"name": "Artist B"}}, "album": map[string]interface{}{"name": "Alb B"},
						},
					},
				}
			} else {
				items = []map[string]interface{}{
					{
						"track": map[string]interface{}{
							"id": "sp_present_1", "name": "Already Present Song", "duration_ms": 210000,
							"artists": []map[string]interface{}{{"name": "Artist A"}}, "album": map[string]interface{}{"name": "Alb"},
						},
					},
					{
						"track": map[string]interface{}{
							"id": "sp_extra_99", "name": "Extraneous Song", "duration_ms": 180000,
							"artists": []map[string]interface{}{{"name": "Extra Artist"}}, "album": map[string]interface{}{"name": "Extra Alb"},
						},
					},
				}
			}

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "sp_pop_mix_id",
				"name":        "Pop Mix",
				"description": "My Pop Mix",
				"tracks": map[string]interface{}{
					"items": items,
					"next":  "",
					"total": len(items),
				},
			})
			return
		}

		http.NotFound(w, r)
	}))
	defer spServer.Close()

	resetAuth := auth.SetEndpointsForTesting(spServer.URL+"/token", spServer.URL+"/v1/me", ytmServer.URL+"/youtubei/v1/browse")
	defer resetAuth()

	origYtmBrowse := ytmusic.EndpointBrowse
	defer func() { ytmusic.EndpointBrowse = origYtmBrowse }()
	ytmusic.EndpointBrowse = ytmServer.URL + "/youtubei/v1/browse"

	origSpMe := spotify.EndpointMe
	origSpMePlaylists := spotify.EndpointMePlaylists
	origSpPlaylists := spotify.EndpointPlaylists
	origSpSearch := spotify.EndpointSearch
	defer func() {
		spotify.EndpointMe = origSpMe
		spotify.EndpointMePlaylists = origSpMePlaylists
		spotify.EndpointPlaylists = origSpPlaylists
		spotify.EndpointSearch = origSpSearch
	}()

	spotify.EndpointMe = spServer.URL + "/v1/me"
	spotify.EndpointMePlaylists = spServer.URL + "/v1/me/playlists"
	spotify.EndpointPlaylists = spServer.URL + "/v1/playlists"
	spotify.EndpointSearch = spServer.URL + "/v1/search"

	cfg := engine.SyncConfig{
		PlaylistName:    "https://music.youtube.com/playlist?list=PL_POP_MIX_123",
		Direction:       engine.DirectionYouTubeToSpotify,
		SpotifyAuthPath: spCredPath,
		YTMHeadersPath:  ytmCredPath,
		CleanExtra:      true,
		AutoYes:         true,
	}

	syncer, err := engine.NewSyncer(cfg)
	if err != nil {
		t.Fatalf("failed to init syncer: %v", err)
	}

	res, err := syncer.Run()
	if err != nil {
		t.Fatalf("syncer.Run failed: %v", err)
	}

	// Verify deduplication: only "Brand New Song" was added
	if len(addedURIs) != 1 || addedURIs[0] != "spotify:track:sp_track_new_2" {
		t.Errorf("expected only Brand New Song to be added, got: %+v", addedURIs)
	}

	// Verify CleanExtra: extraneous track removed
	if len(removedURIs) != 1 || removedURIs[0] != "spotify:track:sp_extra_99" {
		t.Errorf("expected Extraneous Song to be removed, got: %+v", removedURIs)
	}

	if len(res.RemovedExtraTracks) != 1 || res.RemovedExtraTracks[0].TargetTrackID != "sp_extra_99" {
		t.Errorf("expected 1 removed extra track in result, got: %+v", res.RemovedExtraTracks)
	}

	if res.AddedTracks != 2 {
		t.Errorf("expected 2 total tracks in final playlist, got %d", res.AddedTracks)
	}
}

func TestSyncer_Run_YouTubeToSpotify_ConfirmPromptAborted(t *testing.T) {
	tempDir := t.TempDir()

	ytmCredPath := filepath.Join(tempDir, "ytm_credentials.json")
	_ = auth.SaveRawCookieMap(ytmCredPath, "SAPISID=valid_sapisid_token")

	ytmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"header": map[string]interface{}{
				"musicResponsiveHeaderRenderer": map[string]interface{}{
					"title": map[string]interface{}{"runs": []map[string]interface{}{{"text": "Prompt Playlist"}}},
				},
			},
			"contents": map[string]interface{}{
				"twoColumnBrowseResultsRenderer": map[string]interface{}{
					"tabs": []interface{}{},
				},
			},
		})
	}))
	defer ytmServer.Close()

	spCredPath := filepath.Join(tempDir, "sp_credentials.json")
	_ = auth.SaveCookie(spCredPath, "sp_dc=valid_sp_dc_1234567890")

	spServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"accessToken": "sp_token", "isAnonymous": false})
			return
		}
		if r.URL.Path == "/v1/me" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sp_user"})
			return
		}
		if r.URL.Path == "/v1/me/playlists" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": []map[string]interface{}{}, "next": ""})
			return
		}
		http.NotFound(w, r)
	}))
	defer spServer.Close()

	resetAuth := auth.SetEndpointsForTesting(spServer.URL+"/token", spServer.URL+"/v1/me", ytmServer.URL+"/youtubei/v1/browse")
	defer resetAuth()

	origYtmBrowse := ytmusic.EndpointBrowse
	defer func() { ytmusic.EndpointBrowse = origYtmBrowse }()
	ytmusic.EndpointBrowse = ytmServer.URL + "/youtubei/v1/browse"

	origSpMe := spotify.EndpointMe
	origSpMePlaylists := spotify.EndpointMePlaylists
	origSpPlaylists := spotify.EndpointPlaylists
	origSpSearch := spotify.EndpointSearch
	defer func() {
		spotify.EndpointMe = origSpMe
		spotify.EndpointMePlaylists = origSpMePlaylists
		spotify.EndpointPlaylists = origSpPlaylists
		spotify.EndpointSearch = origSpSearch
	}()

	spotify.EndpointMe = spServer.URL + "/v1/me"
	spotify.EndpointMePlaylists = spServer.URL + "/v1/me/playlists"
	spotify.EndpointPlaylists = spServer.URL + "/v1/playlists"
	spotify.EndpointSearch = spServer.URL + "/v1/search"

	cfg := engine.SyncConfig{
		PlaylistName:    "https://music.youtube.com/playlist?list=PL_PROMPT_123",
		Direction:       engine.DirectionYouTubeToSpotify,
		SpotifyAuthPath: spCredPath,
		YTMHeadersPath:  ytmCredPath,
		AutoYes:         false,
		ConfirmPrompt: func(prompt string) bool {
			return false // Abort creation
		},
	}

	syncer, err := engine.NewSyncer(cfg)
	if err != nil {
		t.Fatalf("failed to init syncer: %v", err)
	}

	_, err = syncer.Run()
	if err == nil || !strings.Contains(err.Error(), "sync aborted: playlist creation cancelled by user") {
		t.Fatalf("expected user cancellation error, got: %v", err)
	}
}

func TestSyncer_Run_YouTubeToSpotify_CleanExtra_ConfirmPromptDeclined(t *testing.T) {
	tempDir := t.TempDir()

	ytmCredPath := filepath.Join(tempDir, "ytm_credentials.json")
	_ = auth.SaveRawCookieMap(ytmCredPath, "SAPISID=valid_sapisid_token")

	ytmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"header": map[string]interface{}{
				"musicResponsiveHeaderRenderer": map[string]interface{}{
					"title": map[string]interface{}{"runs": []map[string]interface{}{{"text": "Declined Clean PL"}}},
				},
			},
			"contents": map[string]interface{}{
				"twoColumnBrowseResultsRenderer": map[string]interface{}{
					"tabs": []interface{}{
						map[string]interface{}{
							"tabRenderer": map[string]interface{}{
								"content": map[string]interface{}{
									"sectionListRenderer": map[string]interface{}{
										"contents": []interface{}{
											map[string]interface{}{
												"musicPlaylistShelfRenderer": map[string]interface{}{
													"contents": []interface{}{
														map[string]interface{}{
															"musicResponsiveListItemRenderer": map[string]interface{}{
																"playlistItemData": map[string]interface{}{"videoId": "ytm_keep_1"},
																"flexColumns": []interface{}{
																	map[string]interface{}{"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{"text": map[string]interface{}{"runs": []map[string]interface{}{{"text": "Keep Song"}}}}},
																	map[string]interface{}{"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{"text": map[string]interface{}{"runs": []map[string]interface{}{{"text": "Keep Artist"}}}}},
																	map[string]interface{}{"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{"text": map[string]interface{}{"runs": []map[string]interface{}{{"text": "3:00"}}}}},
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
	}))
	defer ytmServer.Close()

	spCredPath := filepath.Join(tempDir, "sp_credentials.json")
	_ = auth.SaveCookie(spCredPath, "sp_dc=valid_sp_dc_1234567890")

	var deleteCalled bool

	spServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"accessToken": "sp_token", "isAnonymous": false})
			return
		}
		if r.URL.Path == "/v1/me" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sp_user"})
			return
		}
		if r.URL.Path == "/v1/me/playlists" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []map[string]interface{}{
					{"id": "sp_declined_pl", "name": "Declined Clean PL"},
				},
				"next": "",
			})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/tracks") && r.Method == http.MethodDelete {
			deleteCalled = true
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"snapshot_id": "snap_del"})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/playlists/sp_declined_pl") {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "sp_declined_pl",
				"name":        "Declined Clean PL",
				"description": "Declined",
				"tracks": map[string]interface{}{
					"items": []map[string]interface{}{
						{
							"track": map[string]interface{}{
								"id": "sp_keep_1", "name": "Keep Song", "duration_ms": 180000,
								"artists": []map[string]interface{}{{"name": "Keep Artist"}}, "album": map[string]interface{}{"name": "Alb"},
							},
						},
						{
							"track": map[string]interface{}{
								"id": "sp_extra_unremoved", "name": "Extra Unremoved Song", "duration_ms": 200000,
								"artists": []map[string]interface{}{{"name": "Extra Artist"}}, "album": map[string]interface{}{"name": "Alb 2"},
							},
						},
					},
					"next":  "",
					"total": 2,
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer spServer.Close()

	resetAuth := auth.SetEndpointsForTesting(spServer.URL+"/token", spServer.URL+"/v1/me", ytmServer.URL+"/youtubei/v1/browse")
	defer resetAuth()

	origYtmBrowse := ytmusic.EndpointBrowse
	defer func() { ytmusic.EndpointBrowse = origYtmBrowse }()
	ytmusic.EndpointBrowse = ytmServer.URL + "/youtubei/v1/browse"

	origSpMe := spotify.EndpointMe
	origSpMePlaylists := spotify.EndpointMePlaylists
	origSpPlaylists := spotify.EndpointPlaylists
	origSpSearch := spotify.EndpointSearch
	defer func() {
		spotify.EndpointMe = origSpMe
		spotify.EndpointMePlaylists = origSpMePlaylists
		spotify.EndpointPlaylists = origSpPlaylists
		spotify.EndpointSearch = origSpSearch
	}()

	spotify.EndpointMe = spServer.URL + "/v1/me"
	spotify.EndpointMePlaylists = spServer.URL + "/v1/me/playlists"
	spotify.EndpointPlaylists = spServer.URL + "/v1/playlists"
	spotify.EndpointSearch = spServer.URL + "/v1/search"

	cfg := engine.SyncConfig{
		PlaylistName:    "https://music.youtube.com/playlist?list=PL_DECLINED_123",
		Direction:       engine.DirectionYouTubeToSpotify,
		SpotifyAuthPath: spCredPath,
		YTMHeadersPath:  ytmCredPath,
		CleanExtra:      true,
		AutoYes:         false,
		ConfirmPrompt: func(prompt string) bool {
			// Decline extra track removal prompt
			return false
		},
	}

	syncer, err := engine.NewSyncer(cfg)
	if err != nil {
		t.Fatalf("failed to init syncer: %v", err)
	}

	res, err := syncer.Run()
	if err != nil {
		t.Fatalf("syncer.Run failed: %v", err)
	}

	if deleteCalled {
		t.Error("expected delete NOT to be called when user declined deletion")
	}
	if len(res.RemovedExtraTracks) != 0 {
		t.Errorf("expected 0 removed extra tracks, got %d", len(res.RemovedExtraTracks))
	}
}

func TestSyncer_Run_YouTubeToSpotify_DirectTargetPlaylistID(t *testing.T) {
	tempDir := t.TempDir()

	ytmCredPath := filepath.Join(tempDir, "ytm_credentials.json")
	_ = auth.SaveRawCookieMap(ytmCredPath, "SAPISID=valid_sapisid_token")

	ytmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"header": map[string]interface{}{
				"musicResponsiveHeaderRenderer": map[string]interface{}{
					"title": map[string]interface{}{"runs": []map[string]interface{}{{"text": "Source YTM"}}},
				},
			},
			"contents": map[string]interface{}{
				"twoColumnBrowseResultsRenderer": map[string]interface{}{
					"tabs": []interface{}{
						map[string]interface{}{
							"tabRenderer": map[string]interface{}{
								"content": map[string]interface{}{
									"sectionListRenderer": map[string]interface{}{
										"contents": []interface{}{
											map[string]interface{}{
												"musicPlaylistShelfRenderer": map[string]interface{}{
													"contents": []interface{}{
														map[string]interface{}{
															"musicResponsiveListItemRenderer": map[string]interface{}{
																"playlistItemData": map[string]interface{}{"videoId": "ytm_direct_song"},
																"flexColumns": []interface{}{
																	map[string]interface{}{"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{"text": map[string]interface{}{"runs": []map[string]interface{}{{"text": "Direct Track"}}}}},
																	map[string]interface{}{"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{"text": map[string]interface{}{"runs": []map[string]interface{}{{"text": "Artist Direct"}}}}},
																	map[string]interface{}{"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{"text": map[string]interface{}{"runs": []map[string]interface{}{{"text": "3:15"}}}}},
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
	}))
	defer ytmServer.Close()

	spCredPath := filepath.Join(tempDir, "sp_credentials.json")
	_ = auth.SaveCookie(spCredPath, "sp_dc=valid_sp_dc_1234567890")

	var addedTargetPlaylistID string

	spServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"accessToken": "sp_token", "isAnonymous": false})
			return
		}
		if r.URL.Path == "/v1/me" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sp_user"})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/search") {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"tracks": map[string]interface{}{
					"items": []map[string]interface{}{
						{
							"id":          "sp_direct_song_id",
							"name":        "Direct Track",
							"duration_ms": 195000,
							"artists":     []map[string]interface{}{{"name": "Artist Direct"}},
							"album":       map[string]interface{}{"name": "Alb"},
						},
					},
				},
			})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/tracks") && r.Method == http.MethodPost {
			parts := strings.Split(r.URL.Path, "/")
			if len(parts) >= 4 {
				addedTargetPlaylistID = parts[3]
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"snapshot_id": "snap_add"})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/playlists/sp_custom_target_999") {
			var items []map[string]interface{}
			if addedTargetPlaylistID != "" {
				items = []map[string]interface{}{
					{
						"track": map[string]interface{}{
							"id": "sp_direct_song_id", "name": "Direct Track", "duration_ms": 195000,
							"artists": []map[string]interface{}{{"name": "Artist Direct"}}, "album": map[string]interface{}{"name": "Alb"},
						},
					},
				}
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "sp_custom_target_999",
				"name":        "Custom Target Spotify Playlist",
				"description": "Desc",
				"tracks": map[string]interface{}{
					"items": items,
					"next":  "",
					"total": len(items),
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer spServer.Close()

	resetAuth := auth.SetEndpointsForTesting(spServer.URL+"/token", spServer.URL+"/v1/me", ytmServer.URL+"/youtubei/v1/browse")
	defer resetAuth()

	origYtmBrowse := ytmusic.EndpointBrowse
	defer func() { ytmusic.EndpointBrowse = origYtmBrowse }()
	ytmusic.EndpointBrowse = ytmServer.URL + "/youtubei/v1/browse"

	origSpMe := spotify.EndpointMe
	origSpMePlaylists := spotify.EndpointMePlaylists
	origSpPlaylists := spotify.EndpointPlaylists
	origSpSearch := spotify.EndpointSearch
	defer func() {
		spotify.EndpointMe = origSpMe
		spotify.EndpointMePlaylists = origSpMePlaylists
		spotify.EndpointPlaylists = origSpPlaylists
		spotify.EndpointSearch = origSpSearch
	}()

	spotify.EndpointMe = spServer.URL + "/v1/me"
	spotify.EndpointMePlaylists = spServer.URL + "/v1/me/playlists"
	spotify.EndpointPlaylists = spServer.URL + "/v1/playlists"
	spotify.EndpointSearch = spServer.URL + "/v1/search"

	cfg := engine.SyncConfig{
		PlaylistName:    "https://music.youtube.com/playlist?list=PL_SOURCE_YTM_999",
		PlaylistID:      "https://open.spotify.com/playlist/sp_custom_target_999",
		Direction:       engine.DirectionYouTubeToSpotify,
		SpotifyAuthPath: spCredPath,
		YTMHeadersPath:  ytmCredPath,
		AutoYes:         true,
	}

	syncer, err := engine.NewSyncer(cfg)
	if err != nil {
		t.Fatalf("failed to init syncer: %v", err)
	}

	res, err := syncer.Run()
	if err != nil {
		t.Fatalf("syncer.Run failed: %v", err)
	}

	if addedTargetPlaylistID != "sp_custom_target_999" {
		t.Errorf("expected addedTargetPlaylistID = sp_custom_target_999, got: %s", addedTargetPlaylistID)
	}
	if res.PlaylistID != "sp_custom_target_999" {
		t.Errorf("expected result PlaylistID = sp_custom_target_999, got: %s", res.PlaylistID)
	}
}

func TestSyncer_ConcurrentCandidateResolution(t *testing.T) {
	tempDir := t.TempDir()
	ytmCredPath := filepath.Join(tempDir, "ytm_cred.json")
	_ = os.WriteFile(ytmCredPath, []byte(`{"User-Agent": "test", "Cookie": "SAPISID=test123"}`), 0600)

	spPlaylist := &model.SpotifyPlaylist{
		PlaylistName: "Batch Concurrency Test",
		Tracks: []model.SpotifyTrack{
			{Index: 1, Title: "Song 1", Artists: []string{"Artist 1"}},
			{Index: 2, Title: "Song 2", Artists: []string{"Artist 2"}},
			{Index: 3, Title: "Song 3", Artists: []string{"Artist 3"}},
			{Index: 4, Title: "Song 4", Artists: []string{"Artist 4"}},
		},
	}
	spJSONPath := filepath.Join(tempDir, "spotify_batch_concurrency_test_source.json")
	spData, _ := json.Marshal(spPlaylist)
	_ = os.WriteFile(spJSONPath, spData, 0644)

	ytmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/browse/edit_playlist") {
			_, _ = w.Write([]byte(`{"status": "STATUS_SUCCEEDED"}`))
			return
		}
		if strings.Contains(r.URL.Path, "/playlist/create") {
			_, _ = w.Write([]byte(`{"playlistId": "PL_CONCURRENT_123"}`))
			return
		}
		if strings.Contains(r.URL.Path, "/search") {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			query, _ := body["query"].(string)

			// Return mock candidates according to query
			for _, num := range []string{"1", "2", "3", "4"} {
				if strings.Contains(query, "Song "+num) {
					resp := map[string]interface{}{
						"contents": map[string]interface{}{
							"tabbedSearchResultsRenderer": map[string]interface{}{
								"tabs": []interface{}{
									map[string]interface{}{
										"tabRenderer": map[string]interface{}{
											"content": map[string]interface{}{
												"sectionListRenderer": map[string]interface{}{
													"contents": []interface{}{
														map[string]interface{}{
															"musicShelfRenderer": map[string]interface{}{
																"contents": []interface{}{
																	map[string]interface{}{
																		"musicResponsiveListItemRenderer": map[string]interface{}{
																			"flexColumns": []interface{}{
																				map[string]interface{}{
																					"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																						"text": map[string]interface{}{
																							"runs": []interface{}{
																								map[string]interface{}{"text": "Song " + num},
																							},
																						},
																					},
																				},
																				map[string]interface{}{
																					"musicResponsiveListItemFlexColumnRenderer": map[string]interface{}{
																						"text": map[string]interface{}{
																							"runs": []interface{}{
																								map[string]interface{}{"text": "Artist " + num},
																							},
																						},
																					},
																				},
																			},
																			"playlistItemData": map[string]interface{}{
																				"videoId": "vid_concurrent_" + num,
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
					}
					_ = json.NewEncoder(w).Encode(resp)
					return
				}
			}
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if strings.Contains(r.URL.Path, "/browse") {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			browseID, _ := body["browseId"].(string)
			if browseID == "FEmusic_liked_playlists" {
				resp := map[string]interface{}{
					"header": map[string]interface{}{
						"musicHeaderRenderer": map[string]interface{}{
							"title": map[string]interface{}{
								"runs": []map[string]interface{}{{"text": "Test Channel"}},
							},
						},
					},
					"contents": map[string]interface{}{
						"twoColumnBrowseResultsRenderer": map[string]interface{}{
							"tabs": []interface{}{
								map[string]interface{}{
									"tabRenderer": map[string]interface{}{
										"content": map[string]interface{}{
											"sectionListRenderer": map[string]interface{}{
												"contents": []interface{}{},
											},
										},
									},
								},
							},
						},
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
			resp := map[string]interface{}{
				"header": map[string]interface{}{
					"musicDetailHeaderRenderer": map[string]interface{}{
						"title": map[string]interface{}{
							"runs": []interface{}{
								map[string]interface{}{"text": "Batch Concurrency Test (Spotify import)"},
							},
						},
					},
				},
				"contents": map[string]interface{}{
					"singleColumnBrowseResultsRenderer": map[string]interface{}{
						"tabs": []interface{}{
							map[string]interface{}{
								"tabRenderer": map[string]interface{}{
									"content": map[string]interface{}{
										"sectionListRenderer": map[string]interface{}{
											"contents": []interface{}{},
										},
									},
								},
							},
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer ytmServer.Close()

	resetAuth := auth.SetEndpointsForTesting("", "", ytmServer.URL+"/youtubei/v1/browse")
	defer resetAuth()

	origYtmBrowse := ytmusic.EndpointBrowse
	origYtmCreate := ytmusic.EndpointCreatePlaylist
	origYtmSearch := ytmusic.EndpointSearch
	origYtmEdit := ytmusic.EndpointEditPlaylist
	defer func() {
		ytmusic.EndpointBrowse = origYtmBrowse
		ytmusic.EndpointCreatePlaylist = origYtmCreate
		ytmusic.EndpointSearch = origYtmSearch
		ytmusic.EndpointEditPlaylist = origYtmEdit
	}()
	ytmusic.EndpointBrowse = ytmServer.URL + "/youtubei/v1/browse"
	ytmusic.EndpointCreatePlaylist = ytmServer.URL + "/youtubei/v1/playlist/create"
	ytmusic.EndpointSearch = ytmServer.URL + "/youtubei/v1/search"
	ytmusic.EndpointEditPlaylist = ytmServer.URL + "/youtubei/v1/browse/edit_playlist"

	cfg := engine.SyncConfig{
		PlaylistName:     "Batch Concurrency Test",
		PlaylistJSONPath: spJSONPath,
		Direction:        engine.DirectionSpotifyToYouTube,
		YTMHeadersPath:   ytmCredPath,
		OutputDir:        tempDir,
		AutoYes:          true,
		Concurrency:      4,
	}

	syncer, err := engine.NewSyncer(cfg)
	if err != nil {
		t.Fatalf("failed to create syncer: %v", err)
	}

	res, err := syncer.Run()
	if err != nil {
		t.Fatalf("syncer.Run failed: %v", err)
	}

	if res.TotalSourceTracks != 4 {
		t.Errorf("expected 4 source tracks, got %d", res.TotalSourceTracks)
	}
	if res.SkippedTracks != 0 {
		t.Errorf("expected 0 skipped tracks, got %d", res.SkippedTracks)
	}
}

func TestProperty_GenerateSearchQueries(t *testing.T) {
	property := func(title, artist string) bool {
		track := model.SpotifyTrack{
			Title:   title,
			Artists: []string{artist},
		}
		queries := engine.GenerateSearchQueries(track)

		// 1. If title or artist is non-empty, queries should not be empty
		if strings.TrimSpace(title) != "" && len(queries) == 0 {
			return false
		}

		// 2. All queries must be non-empty and trimmed
		seen := make(map[string]bool)
		for _, q := range queries {
			if strings.TrimSpace(q) == "" || q != strings.TrimSpace(q) {
				return false
			}
			// 3. No duplicate queries
			if seen[q] {
				return false
			}
			seen[q] = true
		}

		// 4. Bounded length (<= 20)
		if len(queries) > 20 {
			return false
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 1000,
	}

	if err := quick.Check(property, cfg); err != nil {
		t.Errorf("GenerateSearchQueries property check failed: %v", err)
	}
}
