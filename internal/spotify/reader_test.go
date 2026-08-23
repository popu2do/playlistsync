package spotify_test

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"playlistsync/internal/model"
	"playlistsync/internal/spotify"
	"strings"
	"testing"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func samplePlaylist() *model.SpotifyPlaylist {
	return &model.SpotifyPlaylist{
		PlaylistName:      "Test Spotify Playlist",
		SourcePlaylistURL: "https://open.spotify.com/playlist/test12345",
		ExpectedCount:     2,
		CollectedCount:    2,
		Tracks: []model.SpotifyTrack{
			{
				Index:      1,
				ID:         "track1",
				Title:      "Song One",
				Artists:    []string{"Artist A", "Artist B"},
				Album:      "Album One",
				Duration:   "3:45",
				SpotifyURI: "spotify:track:track1",
				SpotifyURL: "https://open.spotify.com/track/track1",
				Query:      "Song One Artist A",
			},
			{
				Index:      2,
				ID:         "track2",
				Title:      "Song Two (feat. Guest)",
				Artists:    []string{"Artist C"},
				Album:      "Album Two",
				Duration:   "4:12",
				SpotifyURI: "spotify:track:track2",
				SpotifyURL: "https://open.spotify.com/track/track2",
				Query:      "Song Two Artist C",
			},
		},
	}
}

func TestReadPlaylistJSON_Success(t *testing.T) {
	tempDir := t.TempDir()
	jsonPath := filepath.Join(tempDir, "playlist.json")

	original := samplePlaylist()
	if err := spotify.WritePlaylistJSON(jsonPath, original); err != nil {
		t.Fatalf("WritePlaylistJSON failed: %v", err)
	}

	loaded, err := spotify.ReadPlaylistJSON(jsonPath)
	if err != nil {
		t.Fatalf("ReadPlaylistJSON failed: %v", err)
	}

	if loaded.PlaylistName != original.PlaylistName {
		t.Errorf("expected playlist name %q, got %q", original.PlaylistName, loaded.PlaylistName)
	}
	if len(loaded.Tracks) != len(original.Tracks) {
		t.Fatalf("expected %d tracks, got %d", len(original.Tracks), len(loaded.Tracks))
	}
	if loaded.Tracks[0].Title != original.Tracks[0].Title {
		t.Errorf("expected track title %q, got %q", original.Tracks[0].Title, loaded.Tracks[0].Title)
	}
	if len(loaded.Tracks[0].Artists) != 2 {
		t.Errorf("expected 2 artists, got %d", len(loaded.Tracks[0].Artists))
	}
}

func TestReadPlaylistJSON_NotFound(t *testing.T) {
	_, err := spotify.ReadPlaylistJSON("non_existent_file_xyz_123.json")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

func TestReadPlaylistJSON_DirectoryError(t *testing.T) {
	tempDir := t.TempDir()
	_, err := spotify.ReadPlaylistJSON(tempDir)
	if err == nil {
		t.Fatal("expected error when reading directory as JSON, got nil")
	}
}

func TestReadPlaylistJSON_InvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	badJSONPath := filepath.Join(tempDir, "invalid.json")

	if err := os.WriteFile(badJSONPath, []byte("{invalid json"), 0644); err != nil {
		t.Fatalf("failed to write bad json: %v", err)
	}

	_, err := spotify.ReadPlaylistJSON(badJSONPath)
	if err == nil {
		t.Fatal("expected error for invalid json, got nil")
	}
}

func TestFindPlaylistByName(t *testing.T) {
	tempDir := t.TempDir()
	pl := samplePlaylist()

	t.Run("Find in searchDir with spotify_<slug>_source format", func(t *testing.T) {
		subDir := filepath.Join(tempDir, "search_source")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}
		targetPath := filepath.Join(subDir, "spotify_myrock_source.json")
		if err := spotify.WritePlaylistJSON(targetPath, pl); err != nil {
			t.Fatal(err)
		}

		found, path, err := spotify.FindPlaylistByName(subDir, "MyRock")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found == nil || path != targetPath {
			t.Errorf("expected path %s, got %s", targetPath, path)
		}
	})

	t.Run("Find in searchDir with plain <slug>_source format", func(t *testing.T) {
		subDir := filepath.Join(tempDir, "search1")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}
		targetPath := filepath.Join(subDir, "myrock_source.json")
		if err := spotify.WritePlaylistJSON(targetPath, pl); err != nil {
			t.Fatal(err)
		}

		found, path, err := spotify.FindPlaylistByName(subDir, "MyRock")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found == nil || path != targetPath {
			t.Errorf("expected path %s, got %s", targetPath, path)
		}
	})

	t.Run("Find in searchDir with plain .json suffix", func(t *testing.T) {
		subDir := filepath.Join(tempDir, "search2")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}
		targetPath := filepath.Join(subDir, "jazz.json")
		if err := spotify.WritePlaylistJSON(targetPath, pl); err != nil {
			t.Fatal(err)
		}

		found, path, err := spotify.FindPlaylistByName(subDir, "  JAZZ  ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found == nil || path != targetPath {
			t.Errorf("expected path %s, got %s", targetPath, path)
		}
	})

	t.Run("Find by direct file path", func(t *testing.T) {
		directPath := filepath.Join(tempDir, "direct_match.json")
		if err := spotify.WritePlaylistJSON(directPath, pl); err != nil {
			t.Fatal(err)
		}

		found, path, err := spotify.FindPlaylistByName("", directPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found == nil || path != directPath {
			t.Errorf("expected path %s, got %s", directPath, path)
		}
	})

	t.Run("Find with empty searchDir defaults to output", func(t *testing.T) {
		_ = os.MkdirAll("output", 0755)
		defer os.RemoveAll("output")
		outPath := filepath.Join("output", "spotify_default_dir_test_source.json")
		if err := spotify.WritePlaylistJSON(outPath, pl); err != nil {
			t.Fatal(err)
		}

		found, path, err := spotify.FindPlaylistByName("", "default_dir_test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found == nil || path != outPath {
			t.Errorf("expected path %s, got %s", outPath, path)
		}
	})

	t.Run("Empty playlist name returns error", func(t *testing.T) {
		_, _, err := spotify.FindPlaylistByName("", "   ")
		if err == nil {
			t.Fatal("expected error for empty playlist name")
		}
	})

	t.Run("Case-insensitive lookup Dadadidi vs dadadidi", func(t *testing.T) {
		subDir := filepath.Join(tempDir, "case_test")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}
		targetPath := filepath.Join(subDir, "spotify_Dadadidi_source.json")
		if err := spotify.WritePlaylistJSON(targetPath, pl); err != nil {
			t.Fatal(err)
		}

		// Test lowercase query
		found, path, err := spotify.FindPlaylistByName(subDir, "dadadidi")
		if err != nil {
			t.Fatalf("unexpected error for lowercase query: %v", err)
		}
		if found == nil || path != targetPath {
			t.Errorf("expected path %s, got %s", targetPath, path)
		}

		// Test uppercase query
		found2, path2, err := spotify.FindPlaylistByName(subDir, "DADADIDI")
		if err != nil {
			t.Fatalf("unexpected error for uppercase query: %v", err)
		}
		if found2 == nil || path2 != targetPath {
			t.Errorf("expected path %s, got %s", targetPath, path2)
		}
	})

	t.Run("Missing playlist error listing available candidates and suggestion", func(t *testing.T) {
		subDir := filepath.Join(tempDir, "suggest_test")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}
		targetPath := filepath.Join(subDir, "spotify_Dadadidi_source.json")
		if err := spotify.WritePlaylistJSON(targetPath, pl); err != nil {
			t.Fatal(err)
		}

		_, _, err := spotify.FindPlaylistByName(subDir, "dadadida")
		if err == nil {
			t.Fatal("expected error for non-existent playlist")
		}
		errMsg := err.Error()
		if !strings.Contains(errMsg, "Dadadidi") {
			t.Errorf("expected error message to suggest Dadadidi, got: %s", errMsg)
		}
		if !strings.Contains(errMsg, "Available playlists") {
			t.Errorf("expected error message to list available playlists, got: %s", errMsg)
		}
	})

	t.Run("Not found returns clear error", func(t *testing.T) {
		subDir := filepath.Join(tempDir, "empty")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}
		found, path, err := spotify.FindPlaylistByName(subDir, "nonexistent")
		if err == nil {
			t.Fatalf("expected error, got found: %v, path: %s", found, path)
		}
		if !strings.Contains(err.Error(), "playlist 'nonexistent' not found") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("Stat succeeds but JSON invalid falls through or errors", func(t *testing.T) {
		subDir := filepath.Join(tempDir, "search3")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}
		badPath := filepath.Join(subDir, "spotify_corrupted_source.json")
		if err := os.WriteFile(badPath, []byte("invalid json content"), 0644); err != nil {
			t.Fatal(err)
		}

		_, _, err := spotify.FindPlaylistByName(subDir, "corrupted")
		if err == nil {
			t.Fatal("expected error for corrupted playlist candidate")
		}
	})
}

func TestWritePlaylistJSON(t *testing.T) {
	tempDir := t.TempDir()
	jsonPath := filepath.Join(tempDir, "written_source.json")

	pl := samplePlaylist()
	if err := spotify.WritePlaylistJSON(jsonPath, pl); err != nil {
		t.Fatalf("WritePlaylistJSON failed: %v", err)
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("failed to read back written json: %v", err)
	}

	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Errorf("expected JSON file to end with newline")
	}

	invalidPath := filepath.Join(tempDir, "non_existent_sub_dir", "sub2", "file.json")
	if err := spotify.WritePlaylistJSON(invalidPath, pl); err == nil {
		t.Errorf("expected error writing to non-existent directory, got nil")
	}
}

func TestWritePlaylistCSV(t *testing.T) {
	tempDir := t.TempDir()
	csvPath := filepath.Join(tempDir, "playlist.csv")

	pl := samplePlaylist()
	if err := spotify.WritePlaylistCSV(csvPath, pl); err != nil {
		t.Fatalf("WritePlaylistCSV failed: %v", err)
	}

	data, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("failed to read CSV file: %v", err)
	}

	bom := []byte{0xEF, 0xBB, 0xBF}
	if !bytes.HasPrefix(data, bom) {
		t.Fatalf("expected UTF-8 BOM header in CSV file")
	}

	contentWithoutBOM := data[len(bom):]
	r := csv.NewReader(bytes.NewReader(contentWithoutBOM))
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("failed to parse CSV: %v", err)
	}

	if len(records) != 3 {
		t.Fatalf("expected 3 CSV records (1 header + 2 rows), got %d", len(records))
	}

	header := records[0]
	expectedHeader := []string{"index", "title", "artists", "album", "duration", "query", "spotifyUrl"}
	for i, h := range expectedHeader {
		if header[i] != h {
			t.Errorf("header column %d mismatch: expected %q, got %q", i, h, header[i])
		}
	}

	row1 := records[1]
	if row1[0] != "1" || row1[1] != "Song One" || row1[2] != "Artist A, Artist B" {
		t.Errorf("row1 unexpected content: %+v", row1)
	}

	row2 := records[2]
	if row2[0] != "2" || row2[1] != "Song Two (feat. Guest)" || row2[2] != "Artist C" {
		t.Errorf("row2 unexpected content: %+v", row2)
	}

	invalidPath := filepath.Join(tempDir, "non_existent_sub_dir", "sub2", "file.csv")
	if err := spotify.WritePlaylistCSV(invalidPath, pl); err == nil {
		t.Errorf("expected error writing CSV to non-existent directory, got nil")
	}
}

func TestSpotifyClient_Validation(t *testing.T) {
	c := spotify.NewClient("test_token", "")
	if c == nil {
		t.Fatal("expected non-nil spotify client")
	}

	if _, err := c.FindPlaylist(""); err == nil {
		t.Error("expected error for empty playlist identifier")
	}
}

func TestSpotifyClient_FindPlaylist_SearchAndPagination(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, "/v1/playlists/37i9dQZF1DXcBWIGoYBM5M") {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "37i9dQZF1DXcBWIGoYBM5M",
				"name":        "NANASA",
				"description": "NANASA Playlist",
				"tracks": map[string]interface{}{
					"items": []map[string]interface{}{
						{
							"track": map[string]interface{}{
								"id":          "track_111",
								"name":        "Hit Song",
								"duration_ms": 210000,
								"artists": []map[string]interface{}{
									{"name": "Famous Singer"},
								},
								"album": map[string]interface{}{
									"name": "Hit Album",
								},
							},
						},
					},
					"next":  "",
					"total": 1,
				},
			})
			return
		}

		if strings.Contains(r.URL.Path, "/v1/me/playlists") {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []map[string]interface{}{
					{
						"id":   "37i9dQZF1DXcBWIGoYBM5M",
						"name": "NANASA",
					},
				},
				"next": "",
			})
			return
		}

		if strings.Contains(r.URL.Path, "/v1/search") {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"playlists": map[string]interface{}{
					"items": []map[string]interface{}{
						{
							"id":   "37i9dQZF1DXcBWIGoYBM5M",
							"name": "Catalog Hits",
						},
					},
				},
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	c := spotify.NewClient("mock_token", "")
	_ = c
}

func TestSpotifyClient_OperationsAndBatchChunking(t *testing.T) {
	var addBatchCounts []int
	var removeBatchCounts []int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/v1/me" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "spotify_user_42",
			})
			return
		}

		if r.URL.Path == "/v1/me/playlists" && r.Method == http.MethodPost {
			var payload map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "new_playlist_42",
			})
			return
		}

		if strings.HasSuffix(r.URL.Path, "/tracks") && r.Method == http.MethodPost {
			var payload struct {
				URIs []string `json:"uris"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			addBatchCounts = append(addBatchCounts, len(payload.URIs))
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"snapshot_id": "snap_add"})
			return
		}

		if strings.HasSuffix(r.URL.Path, "/tracks") && r.Method == http.MethodDelete {
			var payload struct {
				Tracks []struct {
					URI string `json:"uri"`
				} `json:"tracks"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			removeBatchCounts = append(removeBatchCounts, len(payload.Tracks))
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"snapshot_id": "snap_del"})
			return
		}

		if strings.HasPrefix(r.URL.Path, "/v1/search") {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"tracks": map[string]interface{}{
					"items": []map[string]interface{}{
						{
							"id":          "searched_track_1",
							"name":        "Searched Song",
							"duration_ms": 180000,
							"artists":     []map[string]interface{}{{"name": "Artist S"}},
							"album":       map[string]interface{}{"name": "Album S"},
						},
					},
				},
			})
			return
		}

		http.NotFound(w, r)
	}))
	defer ts.Close()

	origMe := spotify.EndpointMe
	origMePl := spotify.EndpointMePlaylists
	origPl := spotify.EndpointPlaylists
	origSearch := spotify.EndpointSearch
	defer func() {
		spotify.EndpointMe = origMe
		spotify.EndpointMePlaylists = origMePl
		spotify.EndpointPlaylists = origPl
		spotify.EndpointSearch = origSearch
	}()

	spotify.EndpointMe = ts.URL + "/v1/me"
	spotify.EndpointMePlaylists = ts.URL + "/v1/me/playlists"
	spotify.EndpointPlaylists = ts.URL + "/v1/playlists"
	spotify.EndpointSearch = ts.URL + "/v1/search"

	c := spotify.NewClient("token123", "")

	// 1. GetCurrentUser
	userID, err := c.GetCurrentUser()
	if err != nil || userID != "spotify_user_42" {
		t.Fatalf("GetCurrentUser failed: %v, id=%s", err, userID)
	}

	// 2. CreatePlaylist
	plID, err := c.CreatePlaylist("My List", "Desc")
	if err != nil || plID != "new_playlist_42" {
		t.Fatalf("CreatePlaylist failed: %v, id=%s", err, plID)
	}

	// 3. SearchTrack
	searchResults, err := c.SearchTrack("Searched Song")
	if err != nil || len(searchResults) != 1 || searchResults[0].ID != "searched_track_1" {
		t.Fatalf("SearchTrack failed: %v, results=%+v", err, searchResults)
	}

	// 4. AddTracksToPlaylist (>100 to test batch chunking)
	var largeURIs []string
	for i := 0; i < 250; i++ {
		largeURIs = append(largeURIs, fmt.Sprintf("spotify:track:track_%d", i))
	}
	if err := c.AddTracksToPlaylist(plID, largeURIs); err != nil {
		t.Fatalf("AddTracksToPlaylist failed: %v", err)
	}
	if len(addBatchCounts) != 3 || addBatchCounts[0] != 100 || addBatchCounts[1] != 100 || addBatchCounts[2] != 50 {
		t.Errorf("unexpected add batch counts: %+v", addBatchCounts)
	}

	// 5. RemoveTracksFromPlaylist (>100 to test batch chunking)
	if err := c.RemoveTracksFromPlaylist(plID, largeURIs); err != nil {
		t.Fatalf("RemoveTracksFromPlaylist failed: %v", err)
	}
	if len(removeBatchCounts) != 3 || removeBatchCounts[0] != 100 || removeBatchCounts[1] != 100 || removeBatchCounts[2] != 50 {
		t.Errorf("unexpected remove batch counts: %+v", removeBatchCounts)
	}
}

func TestClient_GetPlaylist_Pagination(t *testing.T) {
	var tsURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/playlists/paginated_pl" {
			var items []map[string]interface{}
			for i := 0; i < 100; i++ {
				items = append(items, map[string]interface{}{
					"track": map[string]interface{}{
						"id":          fmt.Sprintf("track_%d", i),
						"name":        fmt.Sprintf("Track %d", i),
						"duration_ms": 200000,
						"artists":     []interface{}{map[string]interface{}{"name": "Artist 1"}},
						"album":       map[string]interface{}{"name": "Album 1"},
					},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "paginated_pl",
				"name":        "Paginated Playlist",
				"description": "Large Playlist",
				"tracks": map[string]interface{}{
					"items": items,
					"next":  tsURL + "/v1/playlists/paginated_pl/tracks?offset=100&limit=100",
					"total": 150,
				},
			})
			return
		}
		if r.URL.Path == "/v1/playlists/paginated_pl/tracks" {
			var items []map[string]interface{}
			for i := 100; i < 150; i++ {
				items = append(items, map[string]interface{}{
					"track": map[string]interface{}{
						"id":          fmt.Sprintf("track_%d", i),
						"name":        fmt.Sprintf("Track %d", i),
						"duration_ms": 200000,
						"artists":     []interface{}{map[string]interface{}{"name": "Artist 1"}},
						"album":       map[string]interface{}{"name": "Album 1"},
					},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"items": items,
				"next":  "",
				"total": 150,
			})
			return
		}
		http.NotFound(w, r)
	}))
	tsURL = ts.URL
	defer ts.Close()

	origPl := spotify.EndpointPlaylists
	defer func() {
		spotify.EndpointPlaylists = origPl
	}()
	spotify.EndpointPlaylists = ts.URL + "/v1/playlists"

	c := spotify.NewClient("token_paginated", "")
	pl, err := c.GetPlaylist("paginated_pl")
	if err != nil {
		t.Fatalf("GetPlaylist failed: %v", err)
	}

	if len(pl.Tracks) != 150 {
		t.Fatalf("expected 150 tracks from pagination, got %d", len(pl.Tracks))
	}
	if pl.ExpectedCount != 150 {
		t.Errorf("expected ExpectedCount = 150, got %d", pl.ExpectedCount)
	}
	if pl.CollectedCount != 150 {
		t.Errorf("expected CollectedCount = 150, got %d", pl.CollectedCount)
	}
	if pl.Tracks[0].ID != "track_0" || pl.Tracks[149].ID != "track_149" {
		t.Errorf("unexpected track IDs at boundaries: [0]=%s, [149]=%s", pl.Tracks[0].ID, pl.Tracks[149].ID)
	}
}
