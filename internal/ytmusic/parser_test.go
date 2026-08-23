package ytmusic

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"playlistsync/internal/model"
	"strings"
	"testing"
)

func createTestCredentials(t *testing.T, cookie string, authHeader string) string {
	t.Helper()
	tempDir := t.TempDir()
	credPath := filepath.Join(tempDir, "ytmusic_creds.json")
	payload := map[string]string{
		"Cookie":        cookie,
		"User-Agent":    "Mozilla/5.0 TestBrowser",
		"Authorization": authHeader,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	return credPath
}

func TestNewClient_OptionsAndErrors(t *testing.T) {
	t.Run("Valid SAPISID credentials", func(t *testing.T) {
		credPath := createTestCredentials(t, "VISITOR_INFO1_LIVE=abc; SAPISID=secret_sapisid_value; SID=sid1", "")
		c, err := NewClient(credPath, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.sapisid != "secret_sapisid_value" {
			t.Errorf("expected sapisid 'secret_sapisid_value', got %q", c.sapisid)
		}
		authHeader := c.buildAuthHeader()
		if !strings.HasPrefix(authHeader, "SAPISIDHASH ") {
			t.Errorf("expected SAPISIDHASH auth header, got %q", authHeader)
		}
	})

	t.Run("Valid __Secure-3PAPISID credentials", func(t *testing.T) {
		credPath := createTestCredentials(t, "__Secure-3PAPISID=secure_sapisid_val; SID=123", "")
		c, err := NewClient(credPath, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.sapisid != "secure_sapisid_val" {
			t.Errorf("expected sapisid 'secure_sapisid_val', got %q", c.sapisid)
		}
	})

	t.Run("Valid __Secure-1PAPISID credentials", func(t *testing.T) {
		credPath := createTestCredentials(t, "__Secure-1PAPISID=secure1_sapisid_val; SID=123", "")
		c, err := NewClient(credPath, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.sapisid != "secure1_sapisid_val" {
			t.Errorf("expected sapisid 'secure1_sapisid_val', got %q", c.sapisid)
		}
	})

	t.Run("SAPISID priority over __Secure-3PAPISID and __Secure-1PAPISID", func(t *testing.T) {
		credPath := createTestCredentials(t, "__Secure-3PAPISID=sec3; __Secure-1PAPISID=sec1; SAPISID=primary_val", "")
		c, err := NewClient(credPath, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.sapisid != "primary_val" {
			t.Errorf("expected primary SAPISID to win, got %q", c.sapisid)
		}
	})

	t.Run("__Secure-3PAPISID priority over __Secure-1PAPISID", func(t *testing.T) {
		credPath := createTestCredentials(t, "__Secure-1PAPISID=sec1; __Secure-3PAPISID=sec3", "")
		c, err := NewClient(credPath, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.sapisid != "sec3" {
			t.Errorf("expected __Secure-3PAPISID to win over __Secure-1PAPISID, got %q", c.sapisid)
		}
	})

	t.Run("Fallback Authorization header when sapisid missing", func(t *testing.T) {
		credPath := createTestCredentials(t, "OTHER_COOKIE=123", "Bearer fallback_token")
		c, err := NewClient(credPath, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.sapisid != "" {
			t.Errorf("expected empty sapisid, got %q", c.sapisid)
		}
		if authHdr := c.buildAuthHeader(); authHdr != "Bearer fallback_token" {
			t.Errorf("expected 'Bearer fallback_token', got %q", authHdr)
		}
	})

	t.Run("With custom valid proxy URL", func(t *testing.T) {
		credPath := createTestCredentials(t, "SAPISID=test", "")
		c, err := NewClient(credPath, "http://127.0.0.1:8888")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c == nil {
			t.Fatal("expected non-nil client")
		}
	})

	t.Run("Non-existent credential file", func(t *testing.T) {
		_, err := NewClient("non_existent_file_path_12345.json", "")
		if err == nil {
			t.Fatal("expected error for non-existent file, got nil")
		}
	})

	t.Run("Corrupted JSON credential file", func(t *testing.T) {
		tempDir := t.TempDir()
		badPath := filepath.Join(tempDir, "bad.json")
		if err := os.WriteFile(badPath, []byte("bad json"), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := NewClient(badPath, "")
		if err == nil {
			t.Fatal("expected error for corrupted json, got nil")
		}
	})

	t.Run("Invalid proxy URL", func(t *testing.T) {
		credPath := createTestCredentials(t, "SAPISID=test", "")
		_, err := NewClient(credPath, "http://invalid port:8888")
		if err == nil {
			t.Fatal("expected error for invalid proxy url, got nil")
		}
	})
}

func TestClient_EndToEndOperations(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content-type: %s", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("x-origin") != "https://music.youtube.com" {
			t.Errorf("unexpected origin: %s", r.Header.Get("x-origin"))
		}

		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)

		switch r.URL.Path {
		case "/youtubei/v1/playlist/create":
			title, _ := body["title"].(string)
			if title == "error_create" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error": {"code": 400, "message": "create error"}}`))
				return
			}
			if title == "empty_id" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"other": "data"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"playlistId": "PL_CREATED_123"}`))

		case "/youtubei/v1/browse/edit_playlist":
			playlistID, _ := body["playlistId"].(string)
			if playlistID == "PL_ERROR" {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error": "edit failed"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": "STATUS_SUCCEEDED"}`))

		case "/youtubei/v1/browse":
			browseID, _ := body["browseId"].(string)
			continuation, _ := body["continuation"].(string)
			if continuation == "" {
				continuation = r.URL.Query().Get("continuation")
			}

			if browseID == "VLPL_ERROR" {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"error": "not found"}`))
				return
			}
			if browseID == "FEmusic_liked_playlists" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{
					"contents": {
						"twoColumnBrowseResultsRenderer": {
							"tabs": [{
								"tabRenderer": {
									"content": {
										"sectionListRenderer": {
											"contents": [{
												"gridRenderer": {
													"items": [
														{
															"musicTwoRowItemRenderer": {
																"title": {"runs": [{"text": "My Favorites"}]},
																"navigationEndpoint": {
																	"browseEndpoint": {"browseId": "VLPL_FAV_1"}
																}
															}
														},
														{
															"musicTwoRowItemRenderer": {
																"title": {"runs": [{"text": "Road Trip (Spotify import)"}]},
																"navigationEndpoint": {
																	"browseEndpoint": {"browseId": "VLPL_ROAD_2"}
																}
															}
														}
													]
												}
											}]
										}
									}
								}
							}]
						}
					}
				}`))
				return
			}

			// Single vs Paginated playlist
			if browseID == "VLPL_PAGINATED" && continuation == "" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{
					"header": {
						"musicResponsiveHeaderRenderer": {
							"title": {"runs": [{"text": "Paginated Playlist"}]},
							"description": {
								"musicDescriptionShelfRenderer": {
									"description": {"runs": [{"text": "Part 1"}]}
								}
							}
						}
					},
					"contents": {
						"twoColumnBrowseResultsRenderer": {
							"tabs": [{
								"tabRenderer": {
									"content": {
										"sectionListRenderer": {
											"contents": [{
												"musicPlaylistShelfRenderer": {
													"contents": [
														{
															"musicResponsiveListItemRenderer": {
																"playlistItemData": {"videoId": "vid_page1", "playlistSetVideoId": "set_p1"},
																"flexColumns": [
																	{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Track Page 1"}]}}},
																	{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Artist 1"}]}}}
																]
															}
														}
													],
													"continuations": [{
														"nextContinuationData": {"continuation": "token_page_2"}
													}]
												}
											}]
										}
									}
								}
							}]
						}
					}
				}`))
				return
			} else if continuation == "token_page_2" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{
					"continuationContents": {
						"musicPlaylistShelfContinuation": {
							"contents": [
								{
									"musicResponsiveListItemRenderer": {
										"playlistItemData": {"videoId": "vid_page2", "playlistSetVideoId": "set_p2"},
										"flexColumns": [
											{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Track Page 2"}]}}},
											{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Artist 2"}]}}}
										]
									}
								}
							]
						}
					}
				}`))
				return
			}

			// Default simple playlist
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"header": {
					"musicResponsiveHeaderRenderer": {
						"title": {"runs": [{"text": "Simple Playlist"}]}
					}
				},
				"contents": {
					"twoColumnBrowseResultsRenderer": {
						"tabs": [{
							"tabRenderer": {
								"content": {
									"sectionListRenderer": {
										"contents": [{
											"musicPlaylistShelfRenderer": {
												"contents": [
													{
														"musicResponsiveListItemRenderer": {
															"playlistItemData": {"videoId": "vid_simple", "playlistSetVideoId": "set_s"},
															"flexColumns": [
																{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Simple Song"}]}}},
																{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Simple Artist"}]}}}
															]
														}
													}
												]
											}
										}]
									}
								}
							}
						}]
					}
				}
			}`))

		case "/youtubei/v1/search":
			query, _ := body["query"].(string)
			if query == "error_query" {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error": "search failed"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"contents": {
					"tabbedSearchResultsRenderer": {
						"tabs": [{
							"tabRenderer": {
								"content": {
									"sectionListRenderer": {
										"contents": [{
											"musicShelfRenderer": {
												"contents": [
													{
														"musicResponsiveListItemRenderer": {
															"playlistItemData": {"videoId": "search_vid_123"},
															"flexColumns": [
																{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Found Song"}]}}},
																{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Found Artist"}]}}}
															]
														}
													}
												]
											}
										}]
									}
								}
							}
						}]
					}
				}
			}`))

		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	// Redirect endpoints to mock server
	origBrowse := EndpointBrowse
	origEdit := EndpointEditPlaylist
	origSearch := EndpointSearch
	origCreate := EndpointCreatePlaylist
	defer func() {
		EndpointBrowse = origBrowse
		EndpointEditPlaylist = origEdit
		EndpointSearch = origSearch
		EndpointCreatePlaylist = origCreate
	}()

	EndpointBrowse = ts.URL + "/youtubei/v1/browse"
	EndpointEditPlaylist = ts.URL + "/youtubei/v1/browse/edit_playlist"
	EndpointSearch = ts.URL + "/youtubei/v1/search"
	EndpointCreatePlaylist = ts.URL + "/youtubei/v1/playlist/create"

	credPath := createTestCredentials(t, "SAPISID=test_sapisid", "")
	client, err := NewClient(credPath, "")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("CreatePlaylist methods", func(t *testing.T) {
		id, err := client.CreatePlaylist("My List", "Desc", "")
		if err != nil {
			t.Fatalf("CreatePlaylist failed: %v", err)
		}
		if id != "PL_CREATED_123" {
			t.Errorf("expected PL_CREATED_123, got %s", id)
		}

		// Error on invalid request
		_, err = client.CreatePlaylist("error_create", "Desc", "PUBLIC")
		if err == nil {
			t.Fatal("expected error for error_create, got nil")
		}

		// Error when no playlistId returned
		_, err = client.CreatePlaylist("empty_id", "Desc", "PUBLIC")
		if err == nil {
			t.Fatal("expected error for empty_id, got nil")
		}
	})

	t.Run("AddPlaylistItems method", func(t *testing.T) {
		if err := client.AddPlaylistItems("PL_SUCCESS", []string{"vid1", "vid2"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := client.AddPlaylistItems("PL_ERROR", []string{"vid1"}); err == nil {
			t.Fatal("expected error on PL_ERROR, got nil")
		}
	})

	t.Run("RemovePlaylistItems method", func(t *testing.T) {
		items := []model.YTMTrack{
			{VideoID: "vid1", SetVideoID: "set1"},
		}
		if err := client.RemovePlaylistItems("PL_SUCCESS", items); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := client.RemovePlaylistItems("PL_ERROR", items); err == nil {
			t.Fatal("expected error on PL_ERROR, got nil")
		}
	})

	t.Run("GetPlaylist method pagination and prefix", func(t *testing.T) {
		// Single page with "PL_SIMPLE" (auto-prepends "VL")
		pl, err := client.GetPlaylist("PL_SIMPLE")
		if err != nil {
			t.Fatalf("GetPlaylist failed: %v", err)
		}
		if pl.Title != "Simple Playlist" {
			t.Errorf("expected title 'Simple Playlist', got %s", pl.Title)
		}
		if len(pl.Tracks) != 1 || pl.Tracks[0].VideoID != "vid_simple" {
			t.Errorf("unexpected tracks: %+v", pl.Tracks)
		}

		// Paginated with "VLPL_PAGINATED"
		plPaginated, err := client.GetPlaylist("VLPL_PAGINATED")
		if err != nil {
			t.Fatalf("GetPlaylist paginated failed: %v", err)
		}
		if len(plPaginated.Tracks) != 2 {
			t.Fatalf("expected 2 tracks from 2 pages, got %d", len(plPaginated.Tracks))
		}
		if plPaginated.Tracks[0].VideoID != "vid_page1" || plPaginated.Tracks[1].VideoID != "vid_page2" {
			t.Errorf("unexpected tracks across pages: %+v", plPaginated.Tracks)
		}

		// Error on not found
		_, err = client.GetPlaylist("PL_ERROR")
		if err == nil {
			t.Fatal("expected error for PL_ERROR, got nil")
		}
	})

	t.Run("GetLibraryPlaylists and FindPlaylistByTitle methods", func(t *testing.T) {
		playlists, err := client.GetLibraryPlaylists()
		if err != nil {
			t.Fatalf("GetLibraryPlaylists failed: %v", err)
		}
		if len(playlists) != 2 {
			t.Fatalf("expected 2 playlists, got %d", len(playlists))
		}

		// Exact match
		found1, err := client.FindPlaylistByTitle("My Favorites")
		if err != nil || found1 == nil || found1.ID != "PL_FAV_1" {
			t.Errorf("failed to find 'My Favorites': %+v, err: %v", found1, err)
		}

		// Case-insensitive exact match
		found1Lower, err := client.FindPlaylistByTitle("my favorites")
		if err != nil || found1Lower == nil || found1Lower.ID != "PL_FAV_1" {
			t.Errorf("failed case-insensitive find 'my favorites': %+v, err: %v", found1Lower, err)
		}

		found1Upper, err := client.FindPlaylistByTitle("MY FAVORITES")
		if err != nil || found1Upper == nil || found1Upper.ID != "PL_FAV_1" {
			t.Errorf("failed case-insensitive find 'MY FAVORITES': %+v, err: %v", found1Upper, err)
		}

		// Suffix match
		found2, err := client.FindPlaylistByTitle("Road Trip")
		if err != nil || found2 == nil || found2.ID != "PL_ROAD_2" {
			t.Errorf("failed to find 'Road Trip': %+v, err: %v", found2, err)
		}

		// Partial-word prefix "My Fav" should NOT match "My Favorites" (no word-boundary separator)
		found3, err := client.FindPlaylistByTitle("My Fav")
		if err != nil || found3 != nil {
			t.Errorf("expected nil for non-word-boundary prefix 'My Fav': %+v, err: %v", found3, err)
		}

		// Not found
		foundNone, err := client.FindPlaylistByTitle("Completely Unknown Playlist")
		if err != nil || foundNone != nil {
			t.Errorf("expected nil for non-existent playlist, got %+v, err: %v", foundNone, err)
		}
	})

	t.Run("SearchSong method", func(t *testing.T) {
		results, err := client.SearchSong("Test Song")
		if err != nil {
			t.Fatalf("SearchSong failed: %v", err)
		}
		if len(results) != 1 || results[0].VideoID != "search_vid_123" {
			t.Errorf("unexpected search results: %+v", results)
		}

		// Error query
		_, err = client.SearchSong("error_query")
		if err == nil {
			t.Fatal("expected error for error_query, got nil")
		}
	})
}

func TestParsePlaylistResponse(t *testing.T) {
	rawJSON := `{
		"header": {
			"musicResponsiveHeaderRenderer": {
				"title": {
					"runs": [{"text": "Sample YTM Playlist"}]
				},
				"description": {
					"musicDescriptionShelfRenderer": {
						"description": {
							"runs": [{"text": "Sample description text"}]
						}
					}
				}
			}
		},
		"contents": {
			"twoColumnBrowseResultsRenderer": {
				"tabs": [
					{
						"tabRenderer": {
							"content": {
								"sectionListRenderer": {
									"contents": [
										{
											"musicPlaylistShelfRenderer": {
												"contents": [
													{
														"musicResponsiveListItemRenderer": {
															"playlistItemData": {
																"videoId": "vid001",
																"playlistSetVideoId": "set001"
															},
															"flexColumns": [
																{
																	"musicResponsiveListItemFlexColumnRenderer": {
																		"text": {
																			"runs": [{"text": "Track Title One"}]
																		}
																	}
																},
																{
																	"musicResponsiveListItemFlexColumnRenderer": {
																		"text": {
																			"runs": [
																				{"text": "Artist One"},
																				{"text": " • "},
																				{"text": "Album One"}
																			]
																		}
																	}
																}
															]
														}
													}
												],
												"continuations": [
													{
														"nextContinuationData": {
															"continuation": "token_abc_123"
														}
													}
												]
											}
										}
									]
								}
							}
						}
					}
				]
			}
		}
	}`

	pl, cont, err := parsePlaylistResponse([]byte(rawJSON))
	if err != nil {
		t.Fatalf("parsePlaylistResponse failed: %v", err)
	}

	if pl.Title != "Sample YTM Playlist" {
		t.Errorf("expected title 'Sample YTM Playlist', got %q", pl.Title)
	}
	if pl.Description != "Sample description text" {
		t.Errorf("expected description 'Sample description text', got %q", pl.Description)
	}
	if cont != "token_abc_123" {
		t.Errorf("expected continuation token 'token_abc_123', got %q", cont)
	}
	if len(pl.Tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(pl.Tracks))
	}
	if pl.Tracks[0].VideoID != "vid001" || pl.Tracks[0].SetVideoID != "set001" {
		t.Errorf("track IDs mismatch: %+v", pl.Tracks[0])
	}
	if pl.Tracks[0].Title != "Track Title One" {
		t.Errorf("track title mismatch: %s", pl.Tracks[0].Title)
	}
	if len(pl.Tracks[0].Artists) != 2 || pl.Tracks[0].Artists[0] != "Artist One" {
		t.Errorf("track artists mismatch: %+v", pl.Tracks[0].Artists)
	}

	// Invalid JSON
	_, _, err = parsePlaylistResponse([]byte("invalid json"))
	if err == nil {
		t.Errorf("expected error for invalid json, got nil")
	}
}

func TestParsePlaylistResponse_EditableHeaderAndFixedColumns(t *testing.T) {
	rawJSON := `{
		"header": {
			"musicEditablePlaylistDetailHeaderRenderer": {
				"header": {
					"musicResponsiveHeaderRenderer": {
						"title": {"runs": [{"text": "Editable Playlist Header"}]},
						"description": {
							"musicDescriptionShelfRenderer": {
								"description": {"runs": [{"text": "Line 1"}, {"text": " Line 2"}]}
							}
						}
					}
				}
			}
		},
		"contents": {
			"twoColumnBrowseResultsRenderer": {
				"tabs": [{
					"tabRenderer": {
						"content": {
							"sectionListRenderer": {
								"contents": [{
									"musicShelfRenderer": {
										"contents": [{
											"musicResponsiveListItemRenderer": {
												"playlistItemData": {"videoId": "vid_fixed"},
												"flexColumns": [
													{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Fixed Song"}]}}},
													{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Fixed Artist"}, {"text": "3:30"}]}}}
												],
												"fixedColumns": [{
													"musicResponsiveListItemFixedColumnRenderer": {
														"text": {"runs": [{"text": "3:30"}]}
													}
												}]
											}
										}]
									}
								}]
							}
						}
					}
				}]
			}
		}
	}`

	pl, _, err := parsePlaylistResponse([]byte(rawJSON))
	if err != nil {
		t.Fatalf("parsePlaylistResponse failed: %v", err)
	}

	if pl.Title != "Editable Playlist Header" {
		t.Errorf("expected title 'Editable Playlist Header', got %s", pl.Title)
	}
	if pl.Description != "Line 1 Line 2" {
		t.Errorf("expected multi-run description 'Line 1 Line 2', got %q", pl.Description)
	}
	if len(pl.Tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(pl.Tracks))
	}
	if pl.Tracks[0].Duration != "3:30" {
		t.Errorf("expected duration '3:30', got %q", pl.Tracks[0].Duration)
	}
}

func TestParsePlaylistResponse_ContinuationContents(t *testing.T) {
	rawJSON := `{
		"continuationContents": {
			"musicPlaylistShelfContinuation": {
				"contents": [
					{
						"musicResponsiveListItemRenderer": {
							"playlistItemData": {
								"videoId": "vid002",
								"playlistSetVideoId": "set002"
							},
							"flexColumns": [
								{
									"musicResponsiveListItemFlexColumnRenderer": {
										"text": {
											"runs": [{"text": "Track Title Two"}]
										}
									}
								}
							]
						}
					}
				],
				"continuations": []
			}
		}
	}`

	pl, cont, err := parsePlaylistResponse([]byte(rawJSON))
	if err != nil {
		t.Fatalf("parsePlaylistResponse failed: %v", err)
	}
	if cont != "" {
		t.Errorf("expected empty continuation token, got %q", cont)
	}
	if len(pl.Tracks) != 1 || pl.Tracks[0].VideoID != "vid002" {
		t.Errorf("expected 1 track vid002, got %+v", pl.Tracks)
	}
}

func TestParseSearchResults(t *testing.T) {
	searchJSON := `{
		"contents": {
			"tabbedSearchResultsRenderer": {
				"tabs": [
					{
						"tabRenderer": {
							"content": {
								"sectionListRenderer": {
									"contents": [
										{
											"musicShelfRenderer": {
												"contents": [
													{
														"musicResponsiveListItemRenderer": {
															"playlistItemData": {
																"videoId": "searchVid1"
															},
															"flexColumns": [
																{
																	"musicResponsiveListItemFlexColumnRenderer": {
																		"text": {
																			"runs": [{"text": "Searched Song"}]
																		}
																	}
																},
																{
																	"musicResponsiveListItemFlexColumnRenderer": {
																		"text": {
																			"runs": [{"text": "Searched Artist"}]
																		}
																	}
																}
															]
														}
													}
												]
											}
										}
									]
								}
							}
						}
					}
				]
			}
		}
	}`

	results := parseSearchResults([]byte(searchJSON))
	if len(results) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(results))
	}
	if results[0].VideoID != "searchVid1" || results[0].Title != "Searched Song" {
		t.Errorf("search result mismatch: %+v", results[0])
	}
	if len(results[0].Artists) != 1 || results[0].Artists[0] != "Searched Artist" {
		t.Errorf("artists mismatch: %+v", results[0].Artists)
	}

	// Empty and invalid inputs
	if res := parseSearchResults([]byte("bad json")); res != nil {
		t.Errorf("expected nil for bad json")
	}
	if res := parseSearchResults([]byte("{}")); res != nil {
		t.Errorf("expected nil for empty object")
	}
	if res := parseSearchResults([]byte(`{"contents": {}}`)); res != nil {
		t.Errorf("expected nil for empty contents")
	}
	if res := parseSearchResults([]byte(`{"contents": {"tabbedSearchResultsRenderer": {"tabs": []}}}`)); res != nil {
		t.Errorf("expected nil for empty tabs")
	}
}

func TestParseListItem_Empty(t *testing.T) {
	res := parseListItem(nil)
	if res != nil {
		t.Errorf("expected nil for nil item")
	}

	emptyItem := map[string]interface{}{}
	res = parseListItem(emptyItem)
	if res != nil {
		t.Errorf("expected nil for empty item map")
	}

	missingInfo := map[string]interface{}{
		"musicResponsiveListItemRenderer": map[string]interface{}{},
	}
	if res := parseListItem(missingInfo); res != nil {
		t.Errorf("expected nil when missing both videoId and title: %+v", res)
	}
}

func TestGetContinuationToken_Empty(t *testing.T) {
	if tok := getContinuationToken(nil); tok != "" {
		t.Errorf("expected empty token for nil slice")
	}
	if tok := getContinuationToken([]interface{}{}); tok != "" {
		t.Errorf("expected empty token for empty slice")
	}
	if tok := getContinuationToken([]interface{}{map[string]interface{}{"other": "data"}}); tok != "" {
		t.Errorf("expected empty token when nextContinuationData missing")
	}
}

func TestIsDurationFormat(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"3:45", true},
		{"03:45", true},
		{"1:02:15", true},
		{"0:30", true},
		{"", false},
		{"3", false},
		{":45", false},
		{"3:", false},
		{"1::2", false},
		{"1:2:3:4", false},
		{"3:45 min", false},
		{"abc", false},
		{"12345678901", false},
	}

	for _, tt := range tests {
		got := isDurationFormat(tt.input)
		if got != tt.expected {
			t.Errorf("isDurationFormat(%q) = %v; want %v", tt.input, got, tt.expected)
		}
	}
}

func TestExtractDurationText(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"3:45", "3:45"},
		{"03:42", "03:42"},
		{"1:02:15", "1:02:15"},
		{"3:45 min", "3:45"},
		{" • 4:18 • 1.2M views", "4:18"},
		{"Album Name", ""},
		{"", ""},
	}

	for _, c := range cases {
		got := extractDurationText(c.input)
		if got != c.expected {
			t.Errorf("extractDurationText(%q) = %q; want %q", c.input, got, c.expected)
		}
	}
}

func TestParseLibraryPlaylists_EdgeCases(t *testing.T) {
	if res := parseLibraryPlaylists([]byte("bad json")); res != nil {
		t.Errorf("expected nil for bad json")
	}
	if res := parseLibraryPlaylists([]byte("{}")); res != nil {
		t.Errorf("expected nil for empty json")
	}
	if res := parseLibraryPlaylists([]byte(`{"contents": {}}`)); res != nil {
		t.Errorf("expected nil for empty contents")
	}
	if res := parseLibraryPlaylists([]byte(`{"contents": {"twoColumnBrowseResultsRenderer": {"tabs": []}}}`)); res != nil {
		t.Errorf("expected nil for empty tabs")
	}
	if res := parseLibraryPlaylists([]byte(`{"contents": {"twoColumnBrowseResultsRenderer": {"tabs": [{"tabRenderer": {"content": {"sectionListRenderer": {"contents": []}}}}]}}}`)); res != nil {
		t.Errorf("expected nil for empty secContents")
	}
	if res := parseLibraryPlaylists([]byte(`{"contents": {"twoColumnBrowseResultsRenderer": {"tabs": [{"tabRenderer": {"content": {"sectionListRenderer": {"contents": [{"gridRenderer": {"items": [{"musicTwoRowItemRenderer": {}}]}}]}}}}]}}}`)); len(res) != 0 {
		t.Errorf("expected empty results for incomplete item renderer")
	}
}

func TestClient_APIErrors(t *testing.T) {
	tempDir := t.TempDir()
	credPath := filepath.Join(tempDir, "ytm.json")
	_ = os.WriteFile(credPath, []byte(`{"Cookie": "SAPISID=test12345"}`), 0600)

	client, err := NewClient(credPath, "")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("401 Unauthorized API error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": {"code": 401, "message": "Unauthorized"}}`))
		}))
		defer ts.Close()

		_, err := client.post(ts.URL, map[string]string{})
		if err == nil || !strings.Contains(err.Error(), "authentication error (HTTP 401)") {
			t.Errorf("expected 401 auth error, got: %v", err)
		}
	})

	t.Run("500 Internal Server API error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error": {"code": 500, "message": "Server error"}}`))
		}))
		defer ts.Close()

		_, err := client.post(ts.URL, map[string]string{})
		if err == nil || !strings.Contains(err.Error(), "API error (HTTP 500)") {
			t.Errorf("expected 500 API error, got: %v", err)
		}
	})

	t.Run("CreatePlaylist missing playlistId error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}))
		defer ts.Close()

		origCreate := EndpointCreatePlaylist
		EndpointCreatePlaylist = ts.URL
		defer func() {
			EndpointCreatePlaylist = origCreate
		}()

		_, err := client.CreatePlaylist("My Playlist", "Desc", "PRIVATE")
		if err == nil || !strings.Contains(err.Error(), "no playlistId returned") {
			t.Errorf("expected missing playlistId error, got: %v", err)
		}
	})
}

func TestParseListItem_SemanticMetadata_MultiLanguage(t *testing.T) {
	t.Run("Chinese Innertube response with Type indicator, Artist, Album, View Count, Duration", func(t *testing.T) {
		itemJSON := `{
			"musicResponsiveListItemRenderer": {
				"playlistItemData": {"videoId": "zh_vid_001"},
				"flexColumns": [
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {"runs": [{"text": "晴天"}]}
						}
					},
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {
								"runs": [
									{"text": "歌曲"},
									{"text": " • "},
									{
										"text": "周杰伦",
										"navigationEndpoint": {
											"browseEndpoint": {
												"browseId": "UC_zh_artist",
												"browseEndpointContextSupportedConfigs": {
													"browseEndpointContextMusicConfig": {
														"pageType": "MUSIC_PAGE_TYPE_ARTIST"
													}
												}
											}
										}
									},
									{"text": " • "},
									{
										"text": "叶惠美",
										"navigationEndpoint": {
											"browseEndpoint": {
												"browseId": "MPREb_zh_album",
												"browseEndpointContextSupportedConfigs": {
													"browseEndpointContextMusicConfig": {
														"pageType": "MUSIC_PAGE_TYPE_ALBUM"
													}
												}
											}
										}
									},
									{"text": " • "},
									{"text": "1.2亿次播放"},
									{"text": " • "},
									{"text": "04:29"}
								]
							}
						}
					}
				]
			}
		}`
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(itemJSON), &raw); err != nil {
			t.Fatal(err)
		}
		track := parseListItem(raw)
		if track == nil {
			t.Fatal("expected non-nil track")
		}
		if track.VideoID != "zh_vid_001" {
			t.Errorf("expected videoId 'zh_vid_001', got %q", track.VideoID)
		}
		if track.Title != "晴天" {
			t.Errorf("expected title '晴天', got %q", track.Title)
		}
		if len(track.Artists) != 1 || track.Artists[0] != "周杰伦" {
			t.Errorf("expected artists ['周杰伦'], got %+v", track.Artists)
		}
		if track.Duration != "04:29" {
			t.Errorf("expected duration '04:29', got %q", track.Duration)
		}
	})

	t.Run("English Innertube response with Multi-Artists, Album, Views, Duration", func(t *testing.T) {
		itemJSON := `{
			"musicResponsiveListItemRenderer": {
				"playlistItemData": {"videoId": "en_vid_002"},
				"flexColumns": [
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {"runs": [{"text": "Get Lucky"}]}
						}
					},
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {
								"runs": [
									{
										"text": "Daft Punk",
										"navigationEndpoint": {
											"browseEndpoint": {
												"browseId": "UC_daft_punk",
												"browseEndpointContextSupportedConfigs": {
													"browseEndpointContextMusicConfig": {
														"pageType": "MUSIC_PAGE_TYPE_ARTIST"
													}
												}
											}
										}
									},
									{"text": " & "},
									{
										"text": "Pharrell Williams",
										"navigationEndpoint": {
											"browseEndpoint": {
												"browseId": "UC_pharrell",
												"browseEndpointContextSupportedConfigs": {
													"browseEndpointContextMusicConfig": {
														"pageType": "MUSIC_PAGE_TYPE_ARTIST"
													}
												}
											}
										}
									},
									{"text": " • "},
									{
										"text": "Random Access Memories",
										"navigationEndpoint": {
											"browseEndpoint": {
												"browseId": "MPREb_ram_album",
												"browseEndpointContextSupportedConfigs": {
													"browseEndpointContextMusicConfig": {
														"pageType": "MUSIC_PAGE_TYPE_ALBUM"
													}
												}
											}
										}
									},
									{"text": " • "},
									{"text": "650M views"},
									{"text": " • "},
									{"text": "4:08"}
								]
							}
						}
					}
				]
			}
		}`
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(itemJSON), &raw); err != nil {
			t.Fatal(err)
		}
		track := parseListItem(raw)
		if track == nil {
			t.Fatal("expected non-nil track")
		}
		if track.Title != "Get Lucky" {
			t.Errorf("expected title 'Get Lucky', got %q", track.Title)
		}
		if len(track.Artists) != 2 || track.Artists[0] != "Daft Punk" || track.Artists[1] != "Pharrell Williams" {
			t.Errorf("expected multi-artists ['Daft Punk', 'Pharrell Williams'], got %+v", track.Artists)
		}
		if track.Duration != "4:08" {
			t.Errorf("expected duration '4:08', got %q", track.Duration)
		}
	})

	t.Run("Japanese Innertube response with Type indicator, Artist browseId UC prefix, Album browseId MPREb prefix", func(t *testing.T) {
		itemJSON := `{
			"musicResponsiveListItemRenderer": {
				"playlistItemData": {"videoId": "ja_vid_003"},
				"flexColumns": [
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {"runs": [{"text": "夜に駆ける"}]}
						}
					},
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {
								"runs": [
									{"text": "曲"},
									{"text": " • "},
									{
										"text": "YOASOBI",
										"navigationEndpoint": {
											"browseEndpoint": {
												"browseId": "UC_yoasobi_channel"
											}
										}
									},
									{"text": " • "},
									{
										"text": "THE BOOK",
										"navigationEndpoint": {
											"browseEndpoint": {
												"browseId": "MPREb_the_book"
											}
										}
									},
									{"text": " • "},
									{"text": "2.8億回視聴"},
									{"text": " • "},
									{"text": "4:21"}
								]
							}
						}
					}
				]
			}
		}`
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(itemJSON), &raw); err != nil {
			t.Fatal(err)
		}
		track := parseListItem(raw)
		if track == nil {
			t.Fatal("expected non-nil track")
		}
		if track.Title != "夜に駆ける" {
			t.Errorf("expected title '夜に駆ける', got %q", track.Title)
		}
		if len(track.Artists) != 1 || track.Artists[0] != "YOASOBI" {
			t.Errorf("expected artists ['YOASOBI'], got %+v", track.Artists)
		}
		if track.Duration != "4:21" {
			t.Errorf("expected duration '4:21', got %q", track.Duration)
		}
	})

	t.Run("Korean Innertube response with Korean stats and fixedColumns duration", func(t *testing.T) {
		itemJSON := `{
			"musicResponsiveListItemRenderer": {
				"playlistItemData": {"videoId": "ko_vid_004"},
				"flexColumns": [
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {"runs": [{"text": "Dynamite"}]}
						}
					},
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {
								"runs": [
									{
										"text": "BTS",
										"navigationEndpoint": {
											"browseEndpoint": {
												"browseId": "UC_bts",
												"browseEndpointContextSupportedConfigs": {
													"browseEndpointContextMusicConfig": {
														"pageType": "MUSIC_PAGE_TYPE_ARTIST"
													}
												}
											}
										}
									},
									{"text": " • "},
									{
										"text": "BE",
										"navigationEndpoint": {
											"browseEndpoint": {
												"browseId": "MPREb_be",
												"browseEndpointContextSupportedConfigs": {
													"browseEndpointContextMusicConfig": {
														"pageType": "MUSIC_PAGE_TYPE_ALBUM"
													}
												}
											}
										}
									},
									{"text": " • "},
									{"text": "조회수 15억회"}
								]
							}
						}
					}
				],
				"fixedColumns": [
					{
						"musicResponsiveListItemFixedColumnRenderer": {
							"text": {"runs": [{"text": "3:19"}]}
						}
					}
				]
			}
		}`
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(itemJSON), &raw); err != nil {
			t.Fatal(err)
		}
		track := parseListItem(raw)
		if track == nil {
			t.Fatal("expected non-nil track")
		}
		if track.Title != "Dynamite" {
			t.Errorf("expected title 'Dynamite', got %q", track.Title)
		}
		if len(track.Artists) != 1 || track.Artists[0] != "BTS" {
			t.Errorf("expected artists ['BTS'], got %+v", track.Artists)
		}
		if track.Duration != "3:19" {
			t.Errorf("expected duration '3:19', got %q", track.Duration)
		}
	})

	t.Run("User Uploaded / Privately Owned Library Artist and Release", func(t *testing.T) {
		itemJSON := `{
			"musicResponsiveListItemRenderer": {
				"playlistItemData": {"videoId": "priv_005"},
				"flexColumns": [
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {"runs": [{"text": "My Demo Track"}]}
						}
					},
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {
								"runs": [
									{
										"text": "Indie Artist",
										"navigationEndpoint": {
											"browseEndpoint": {
												"browseId": "FEmusic_library_privately_owned_artist_123"
											}
										}
									},
									{"text": " • "},
									{
										"text": "Garage Sessions",
										"navigationEndpoint": {
											"browseEndpoint": {
												"browseId": "FEmusic_library_privately_owned_release_456"
											}
										}
									}
								]
							}
						}
					}
				]
			}
		}`
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(itemJSON), &raw); err != nil {
			t.Fatal(err)
		}
		track := parseListItem(raw)
		if track == nil {
			t.Fatal("expected non-nil track")
		}
		if track.Title != "My Demo Track" {
			t.Errorf("expected title 'My Demo Track', got %q", track.Title)
		}
		if len(track.Artists) != 1 || track.Artists[0] != "Indie Artist" {
			t.Errorf("expected artists ['Indie Artist'], got %+v", track.Artists)
		}
	})

	t.Run("User Channel pageType", func(t *testing.T) {
		itemJSON := `{
			"musicResponsiveListItemRenderer": {
				"playlistItemData": {"videoId": "channel_006"},
				"flexColumns": [
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {"runs": [{"text": "Podcast Episode 1"}]}
						}
					},
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {
								"runs": [
									{
										"text": "Creator Channel",
										"navigationEndpoint": {
											"browseEndpoint": {
												"browseId": "UC_channel_123",
												"browseEndpointContextSupportedConfigs": {
													"browseEndpointContextMusicConfig": {
														"pageType": "MUSIC_PAGE_TYPE_USER_CHANNEL"
													}
												}
											}
										}
									}
								]
							}
						}
					}
				]
			}
		}`
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(itemJSON), &raw); err != nil {
			t.Fatal(err)
		}
		track := parseListItem(raw)
		if track == nil {
			t.Fatal("expected non-nil track")
		}
		if len(track.Artists) != 1 || track.Artists[0] != "Creator Channel" {
			t.Errorf("expected artists ['Creator Channel'], got %+v", track.Artists)
		}
	})
}

func TestParseListItem_VideoIDFallbacks(t *testing.T) {
	t.Run("VideoID from navigationEndpoint.watchEndpoint", func(t *testing.T) {
		itemJSON := `{
			"musicResponsiveListItemRenderer": {
				"navigationEndpoint": {
					"watchEndpoint": {"videoId": "nav_vid_999"}
				},
				"flexColumns": [
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {"runs": [{"text": "Song From Nav"}]}
						}
					}
				]
			}
		}`
		var raw map[string]interface{}
		_ = json.Unmarshal([]byte(itemJSON), &raw)
		track := parseListItem(raw)
		if track == nil || track.VideoID != "nav_vid_999" {
			t.Errorf("expected videoId 'nav_vid_999', got %+v", track)
		}
	})

	t.Run("VideoID from overlay playNavigationEndpoint", func(t *testing.T) {
		itemJSON := `{
			"musicResponsiveListItemRenderer": {
				"overlay": {
					"musicItemThumbnailOverlayRenderer": {
						"content": {
							"musicPlayButtonRenderer": {
								"playNavigationEndpoint": {
									"watchEndpoint": {"videoId": "overlay_vid_888"}
								}
							}
						}
					}
				},
				"flexColumns": [
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {"runs": [{"text": "Song From Overlay"}]}
						}
					}
				]
			}
		}`
		var raw map[string]interface{}
		_ = json.Unmarshal([]byte(itemJSON), &raw)
		track := parseListItem(raw)
		if track == nil || track.VideoID != "overlay_vid_888" {
			t.Errorf("expected videoId 'overlay_vid_888', got %+v", track)
		}
	})

	t.Run("VideoID from flexColumns[0] run watchEndpoint", func(t *testing.T) {
		itemJSON := `{
			"musicResponsiveListItemRenderer": {
				"flexColumns": [
					{
						"musicResponsiveListItemFlexColumnRenderer": {
							"text": {
								"runs": [
									{
										"text": "Song From Col0 Run",
										"navigationEndpoint": {
											"watchEndpoint": {"videoId": "col0_vid_777"}
										}
									}
								]
							}
						}
					}
				]
			}
		}`
		var raw map[string]interface{}
		_ = json.Unmarshal([]byte(itemJSON), &raw)
		track := parseListItem(raw)
		if track == nil || track.VideoID != "col0_vid_777" {
			t.Errorf("expected videoId 'col0_vid_777', got %+v", track)
		}
	})
}

func TestParser_HelperFunctions(t *testing.T) {
	t.Run("isSeparatorText", func(t *testing.T) {
		separators := []string{"", " ", "  ", "•", " • ", "|", " | ", "·", " · ", "/", "\\", "-", "—", "–", ",", "&", " • | / "}
		for _, s := range separators {
			if !isSeparatorText(s) {
				t.Errorf("expected %q to be recognized as separator", s)
			}
		}

		nonSeparators := []string{"Taylor Swift", "123", "Song", "周杰伦", "Album"}
		for _, s := range nonSeparators {
			if isSeparatorText(s) {
				t.Errorf("expected %q NOT to be separator", s)
			}
		}
	})

	t.Run("isStatsText", func(t *testing.T) {
		stats := []string{
			"1.2M views", "500K views", "100 plays", "1.2亿次播放", "5000次观看",
			"100万回視聴", "조회수 120만회", "1,200 reproducciones", "1.2K visualizzazioni",
			"100 тыс. просмотров", "plays: 100", "views: 200", "播放次数：500", "재생횟수: 300",
		}
		for _, s := range stats {
			if !isStatsText(s) {
				t.Errorf("expected %q to be recognized as stats text", s)
			}
		}

		nonStats := []string{"Taylor Swift", "The View", "Playing Games", "Hello World"}
		for _, s := range nonStats {
			if isStatsText(s) {
				t.Errorf("expected %q NOT to be stats text", s)
			}
		}
	})

	t.Run("isTypeIndicatorText", func(t *testing.T) {
		types := []string{
			"song", "Song", "VIDEO", "Single", "Album", "EP", "Track", "Artist", "Playlist",
			"歌曲", "视频", "单曲", "专辑", "播放列表", "艺人",
			"曲", "動画", "シングル", "アルバム", "トラック",
			"노래", "동영상", "싱글", "앨범", "음악",
		}
		for _, s := range types {
			if !isTypeIndicatorText(s) {
				t.Errorf("expected %q to be recognized as type indicator", s)
			}
		}

		nonTypes := []string{"Coldplay", "A Sky Full of Stars", "Ed Sheeran"}
		for _, s := range nonTypes {
			if isTypeIndicatorText(s) {
				t.Errorf("expected %q NOT to be type indicator", s)
			}
		}
	})

	t.Run("singleColumnBrowseResultsRenderer", func(t *testing.T) {
		jsonPayload := []byte(`{
			"contents": {
				"singleColumnBrowseResultsRenderer": {
					"tabs": [{
						"tabRenderer": {
							"content": {
								"sectionListRenderer": {
									"contents": [{
										"musicPlaylistShelfRenderer": {
											"contents": [{
												"musicResponsiveListItemRenderer": {
													"playlistItemData": {"videoId": "vid_single_col"},
													"flexColumns": [
														{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Single Col Track"}]}}},
														{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Artist"}]}}}
													]
												}
											}]
										}
									}]
								}
							}
						}
					}]
				}
			}
		}`)
		pl, _, err := parsePlaylistResponse(jsonPayload)
		if err != nil {
			t.Fatalf("parsePlaylistResponse error: %v", err)
		}
		if len(pl.Tracks) != 1 || pl.Tracks[0].VideoID != "vid_single_col" {
			t.Errorf("expected 1 track with vid_single_col, got %+v", pl.Tracks)
		}
	})

	t.Run("multiRunHeaderTitle", func(t *testing.T) {
		jsonPayload := []byte(`{
			"header": {
				"musicResponsiveHeaderRenderer": {
					"title": {
						"runs": [
							{"text": "My "},
							{"text": "Awesome "},
							{"text": "Playlist 🔥"}
						]
					}
				}
			}
		}`)
		pl, _, err := parsePlaylistResponse(jsonPayload)
		if err != nil {
			t.Fatalf("parsePlaylistResponse error: %v", err)
		}
		if pl.Title != "My Awesome Playlist 🔥" {
			t.Errorf("expected combined title %q, got %q", "My Awesome Playlist 🔥", pl.Title)
		}
	})

	t.Run("musicShelfContinuationSupport", func(t *testing.T) {
		jsonPayload := []byte(`{
			"continuationContents": {
				"musicShelfContinuation": {
					"contents": [{
						"musicResponsiveListItemRenderer": {
							"playlistItemData": {"videoId": "vid_cont_shelf"},
							"flexColumns": [
								{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Shelf Track"}]}}},
								{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Shelf Artist"}]}}}
							]
						}
					}],
					"continuations": [{
						"nextContinuationData": {
							"continuation": "next_token_123"
						}
					}]
				}
			}
		}`)
		pl, token, err := parsePlaylistResponse(jsonPayload)
		if err != nil {
			t.Fatalf("parsePlaylistResponse error: %v", err)
		}
		if len(pl.Tracks) != 1 || pl.Tracks[0].VideoID != "vid_cont_shelf" {
			t.Errorf("expected 1 track from musicShelfContinuation, got %+v", pl.Tracks)
		}
		if token != "next_token_123" {
			t.Errorf("expected next token %q, got %q", "next_token_123", token)
		}
	})

	t.Run("punctuationArtistWithNavigation", func(t *testing.T) {
		jsonPayload := []byte(`{
			"contents": {
				"twoColumnBrowseResultsRenderer": {
					"tabs": [{
						"tabRenderer": {
							"content": {
								"sectionListRenderer": {
									"contents": [{
										"musicPlaylistShelfRenderer": {
											"contents": [{
												"musicResponsiveListItemRenderer": {
													"playlistItemData": {"videoId": "vid_chk_chk_chk"},
													"flexColumns": [
														{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Me and Giuliani Down by the School Yard"}]}}},
														{"musicResponsiveListItemFlexColumnRenderer": {
															"text": {
																"runs": [{
																	"text": "!!!",
																	"navigationEndpoint": {
																		"browseEndpoint": {
																			"browseId": "UC1234567890",
																			"browseEndpointContextSupportedConfigs": {
																				"browseEndpointContextMusicConfig": {
																					"pageType": "MUSIC_PAGE_TYPE_ARTIST"
																				}
																			}
																		}
																	}
																}]
															}
														}}
													]
												}
											}]
										}
									}]
								}
							}
						}
					}]
				}
			}
		}`)
		pl, _, err := parsePlaylistResponse(jsonPayload)
		if err != nil {
			t.Fatalf("parsePlaylistResponse error: %v", err)
		}
		if len(pl.Tracks) != 1 || len(pl.Tracks[0].Artists) != 1 || pl.Tracks[0].Artists[0] != "!!!" {
			t.Errorf("expected artist to be '!!!', got %+v", pl.Tracks)
		}
	})

	t.Run("onResponseReceivedActionsContinuationAndSimpleTextDuration", func(t *testing.T) {
		jsonPayload := []byte(`{
			"onResponseReceivedActions": [{
				"appendContinuationItemsAction": {
					"continuationItems": [
						{
							"musicResponsiveListItemRenderer": {
								"playlistItemData": {"videoId": "vid_action_1"},
								"flexColumns": [
									{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Action Track"}]}}},
									{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Action Artist"}]}}}
								],
								"fixedColumns": [
									{"musicResponsiveListItemFixedColumnRenderer": {"text": {"simpleText": "3:45"}}}
								]
							}
						},
						{
							"continuationItemRenderer": {
								"continuationEndpoint": {
									"continuationCommand": {
										"token": "next_action_token_999"
									}
								}
							}
						}
					]
				}
			}]
		}`)
		pl, token, err := parsePlaylistResponse(jsonPayload)
		if err != nil {
			t.Fatalf("parsePlaylistResponse error: %v", err)
		}
		if len(pl.Tracks) != 1 {
			t.Fatalf("expected 1 track, got %d", len(pl.Tracks))
		}
		if pl.Tracks[0].Duration != "3:45" {
			t.Errorf("expected duration '3:45', got %q", pl.Tracks[0].Duration)
		}
		if token != "next_action_token_999" {
			t.Errorf("expected continuation token 'next_action_token_999', got %q", token)
		}
	})
}
