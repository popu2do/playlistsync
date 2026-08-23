package report_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"playlistsync/internal/model"
	"playlistsync/internal/report"
	"testing"
)

func writeJSONFile(t *testing.T, path string, data interface{}) {
	t.Helper()
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal json for %s: %v", path, err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

func TestSummarize_Success(t *testing.T) {
	tempDir := t.TempDir()
	spPath := filepath.Join(tempDir, "spotify.json")
	resPath := filepath.Join(tempDir, "result.json")

	t.Run("With skipped and reviewed", func(t *testing.T) {
		result := model.SyncResult{
			SourcePlaylistURL: "https://open.spotify.com/playlist/test",
			PlaylistURL:       "https://music.youtube.com/playlist?list=PLtest",
			Title:             "Test Summary Playlist",
			TotalSourceTracks: 10,
			AddedTracks:       9,
			SkippedTracks:     1,
			Skipped: []model.SkippedTrack{
				{Index: 1, Title: "Skipped Song", Artists: []string{"Artist 1"}, Reason: "low confidence"},
			},
			AddedAfterReview: []model.AddedTrack{
				{Index: 2, Title: "Reviewed Song", Artists: []string{"Artist 2"}, TargetTrackID: "vid2"},
			},
		}
		writeJSONFile(t, resPath, result)

		err := report.Summarize(spPath, resPath)
		if err != nil {
			t.Errorf("Summarize failed: %v", err)
		}
	})

	t.Run("Without skipped and reviewed", func(t *testing.T) {
		resPathClean := filepath.Join(tempDir, "clean_result.json")
		cleanResult := model.SyncResult{
			SourcePlaylistURL: "https://open.spotify.com/playlist/test_clean",
			PlaylistURL:       "https://music.youtube.com/playlist?list=PLtest_clean",
			Title:             "Clean Playlist",
			TotalSourceTracks: 5,
			AddedTracks:       5,
		}
		writeJSONFile(t, resPathClean, cleanResult)
		if err := report.Summarize(spPath, resPathClean); err != nil {
			t.Errorf("Summarize clean failed: %v", err)
		}
	})
}

func TestSummarize_Errors(t *testing.T) {
	tempDir := t.TempDir()
	spPath := filepath.Join(tempDir, "spotify.json")
	nonExistentRes := filepath.Join(tempDir, "non_existent.json")

	if err := report.Summarize(spPath, nonExistentRes); err == nil {
		t.Errorf("expected error for missing result file, got nil")
	}

	badJSONPath := filepath.Join(tempDir, "bad.json")
	if err := os.WriteFile(badJSONPath, []byte("invalid json"), 0644); err != nil {
		t.Fatalf("failed to write bad json: %v", err)
	}
	if err := report.Summarize(spPath, badJSONPath); err == nil {
		t.Errorf("expected error for invalid json, got nil")
	}
}

func TestGenerateReport_SuccessAndFailure(t *testing.T) {
	tempDir := t.TempDir()
	resPath := filepath.Join(tempDir, "result.json")
	repPath := filepath.Join(tempDir, "report.json")

	result := model.SyncResult{
		PlaylistID: "PLtest123",
	}
	writeJSONFile(t, resPath, result)

	if err := report.GenerateReport(resPath, repPath); err != nil {
		t.Fatalf("GenerateReport failed: %v", err)
	}

	repData, err := os.ReadFile(repPath)
	if err != nil {
		t.Fatalf("failed to read written report: %v", err)
	}
	var rep model.SyncResult
	if err := json.Unmarshal(repData, &rep); err != nil {
		t.Fatalf("failed to unmarshal report: %v", err)
	}
	if rep.PlaylistID != "PLtest123" {
		t.Errorf("report PlaylistID mismatch: %s", rep.PlaylistID)
	}

	if err := report.GenerateReport(filepath.Join(tempDir, "missing.json"), repPath); err == nil {
		t.Errorf("expected error for missing input result, got nil")
	}

	invalidOut := filepath.Join(tempDir, "no_such_dir", "sub", "report.json")
	if err := report.GenerateReport(resPath, invalidOut); err == nil {
		t.Errorf("expected error writing to invalid path, got nil")
	}
}

func TestValidate_Success(t *testing.T) {
	tempDir := t.TempDir()
	spPath := filepath.Join(tempDir, "spotify.json")
	resPath := filepath.Join(tempDir, "result.json")
	repPath := filepath.Join(tempDir, "report.json")

	sp := model.SpotifyPlaylist{
		Tracks: make([]model.SpotifyTrack, 50),
	}
	writeJSONFile(t, spPath, sp)

	res := model.SyncResult{
		PlaylistURL:       "https://music.youtube.com/playlist?list=PLmock_test_playlist_123",
		TotalSourceTracks: 50,
		AddedTracks:       48,
		SkippedTracks:     2,
		Skipped: []model.SkippedTrack{
			{Index: 1, Title: "Song 1", Artists: []string{"Artist 1"}, Reason: "low confidence"},
			{Index: 2, Title: "Song 2", Artists: []string{"Artist 2"}, Reason: "unavailable"},
		},
		AddedAfterReview: []model.AddedTrack{
			{Index: 3, Title: "Song 3", Artists: []string{"Artist 3"}, TargetTrackID: "vid12345678"},
		},
		Verification: &model.Verification{
			PageTitle:      "Test",
			PageTrackCount: 48,
		},
	}
	writeJSONFile(t, resPath, res)
	writeJSONFile(t, repPath, res)

	if err := report.Validate(spPath, resPath, repPath); err != nil {
		t.Errorf("Validate expected to pass, got: %v", err)
	}
}

func TestValidate_Failures(t *testing.T) {
	tempDir := t.TempDir()
	spPath := filepath.Join(tempDir, "spotify.json")
	resPath := filepath.Join(tempDir, "result.json")
	repPath := filepath.Join(tempDir, "report.json")

	if err := report.Validate(spPath, resPath, repPath); err == nil {
		t.Errorf("expected error when files missing, got nil")
	}

	validSP := model.SpotifyPlaylist{Tracks: make([]model.SpotifyTrack, 50)}
	validRes := model.SyncResult{
		PlaylistURL:       "https://music.youtube.com/playlist?list=PLmock_test_playlist_123",
		TotalSourceTracks: 50,
		AddedTracks:       48,
		SkippedTracks:     2,
		Skipped: []model.SkippedTrack{
			{Index: 1, Title: "Song 1", Artists: []string{"Artist 1"}, Reason: "low confidence"},
			{Index: 2, Title: "Song 2", Artists: []string{"Artist 2"}, Reason: "unavailable"},
		},
		AddedAfterReview: []model.AddedTrack{
			{Index: 3, Title: "Song 3", Artists: []string{"Artist 3"}, TargetTrackID: "vid12345678"},
		},
		Verification: &model.Verification{
			PageTitle:      "Test",
			PageTrackCount: 48,
		},
	}

	badSP := model.SpotifyPlaylist{Tracks: make([]model.SpotifyTrack, 40)}
	writeJSONFile(t, spPath, badSP)
	writeJSONFile(t, resPath, validRes)
	writeJSONFile(t, repPath, validRes)
	if err := report.Validate(spPath, resPath, repPath); err == nil {
		t.Errorf("expected validation failure for bad spotify tracks count")
	}

	writeJSONFile(t, spPath, validSP)
	badRes := validRes
	badRes.AddedTracks = -1
	writeJSONFile(t, resPath, badRes)
	writeJSONFile(t, repPath, badRes)
	if err := report.Validate(spPath, resPath, repPath); err == nil {
		t.Errorf("expected validation failure for negative count")
	}

	badResSkipped := validRes
	badResSkipped.SkippedTracks = -5
	writeJSONFile(t, resPath, badResSkipped)
	writeJSONFile(t, repPath, badResSkipped)
	if err := report.Validate(spPath, resPath, repPath); err == nil {
		t.Errorf("expected validation failure for negative skipped count")
	}

	badRep := validRes
	badRep.PlaylistURL = "https://music.youtube.com/playlist?list=PLmock_other_playlist"
	writeJSONFile(t, resPath, validRes)
	writeJSONFile(t, repPath, badRep)
	if err := report.Validate(spPath, resPath, repPath); err == nil {
		t.Errorf("expected validation failure for report url mismatch")
	}

	badResURL := validRes
	badResURL.PlaylistURL = ""
	writeJSONFile(t, resPath, badResURL)
	writeJSONFile(t, repPath, badResURL)
	if err := report.Validate(spPath, resPath, repPath); err == nil {
		t.Errorf("expected validation failure for empty playlist URL")
	}

	badRepTracks := validRes
	badRepTracks.TotalSourceTracks = 999
	writeJSONFile(t, resPath, validRes)
	writeJSONFile(t, repPath, badRepTracks)
	if err := report.Validate(spPath, resPath, repPath); err == nil {
		t.Errorf("expected validation failure for report total source tracks mismatch")
	}

	badRepAdded := validRes
	badRepAdded.AddedTracks = 100
	writeJSONFile(t, resPath, validRes)
	writeJSONFile(t, repPath, badRepAdded)
	if err := report.Validate(spPath, resPath, repPath); err == nil {
		t.Errorf("expected validation failure for report added tracks mismatch")
	}

	badRepSkipped := validRes
	badRepSkipped.SkippedTracks = 100
	writeJSONFile(t, resPath, validRes)
	writeJSONFile(t, repPath, badRepSkipped)
	if err := report.Validate(spPath, resPath, repPath); err == nil {
		t.Errorf("expected validation failure for report skipped tracks mismatch")
	}

	t.Run("Invariant 2: Verification PageTrackCount Mismatch", func(t *testing.T) {
		badVer := validRes
		badVer.Verification = &model.Verification{
			PageTitle:      "Test",
			PageTrackCount: 999, // Mismatches AddedTracks: 48
		}
		writeJSONFile(t, resPath, badVer)
		writeJSONFile(t, repPath, badVer)
		if err := report.Validate(spPath, resPath, repPath); err == nil {
			t.Errorf("expected validation failure for Invariant 2 verification mismatch")
		}
	})

	t.Run("Invariant 3: Empty Skipped Reason", func(t *testing.T) {
		badSkip := validRes
		badSkip.Skipped = []model.SkippedTrack{
			{Index: 1, Title: "Song 1", Artists: []string{"Artist 1"}, Reason: ""}, // Empty reason
			{Index: 2, Title: "Song 2", Artists: []string{"Artist 2"}, Reason: "ok"},
		}
		writeJSONFile(t, resPath, badSkip)
		writeJSONFile(t, repPath, badSkip)
		if err := report.Validate(spPath, resPath, repPath); err == nil {
			t.Errorf("expected validation failure for Invariant 3 empty reason")
		}
	})

	t.Run("Invariant 4: Empty Added TargetTrackID", func(t *testing.T) {
		badAdded := validRes
		badAdded.AddedAfterReview = []model.AddedTrack{
			{Index: 3, Title: "Song 3", Artists: []string{"Artist 3"}, TargetTrackID: ""}, // Empty TargetTrackID
		}
		writeJSONFile(t, resPath, badAdded)
		writeJSONFile(t, repPath, badAdded)
		if err := report.Validate(spPath, resPath, repPath); err == nil {
			t.Errorf("expected validation failure for Invariant 4 empty TargetTrackID")
		}
	})

	t.Run("Missing files individual branches", func(t *testing.T) {
		writeJSONFile(t, spPath, validSP)
		if err := report.Validate(spPath, filepath.Join(tempDir, "missing_res.json"), repPath); err == nil {
			t.Errorf("expected error for missing result json")
		}
		writeJSONFile(t, resPath, validRes)
		if err := report.Validate(spPath, resPath, filepath.Join(tempDir, "missing_rep.json")); err == nil {
			t.Errorf("expected error for missing report json")
		}
	})

	t.Run("Invalid JSON branches in Validate", func(t *testing.T) {
		badJSONFile := filepath.Join(tempDir, "corrupted.json")
		_ = os.WriteFile(badJSONFile, []byte("{bad"), 0644)

		if err := report.Validate(badJSONFile, resPath, repPath); err == nil {
			t.Errorf("expected error for invalid spotify json")
		}
		if err := report.Validate(spPath, badJSONFile, repPath); err == nil {
			t.Errorf("expected error for invalid result json")
		}
		if err := report.Validate(spPath, resPath, badJSONFile); err == nil {
			t.Errorf("expected error for invalid report json")
		}
	})
}
