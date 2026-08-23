package model_test

import (
	"encoding/json"
	"playlistsync/internal/model"
	"testing"
)

func TestSpotifyModel_JSON(t *testing.T) {
	track := model.SpotifyTrack{
		Index:      1,
		ID:         "4cOdK2wGLETKBW3PvgPWqT",
		Title:      "Never Gonna Give You Up",
		Artists:    []string{"Rick Astley"},
		Album:      "Whenever You Need Somebody",
		Duration:   "3:33",
		SpotifyURI: "spotify:track:4cOdK2wGLETKBW3PvgPWqT",
		SpotifyURL: "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT",
		Query:      "Never Gonna Give You Up Rick Astley",
	}

	playlist := model.SpotifyPlaylist{
		Platform:          "spotify",
		PlaylistName:      "My Playlist",
		SourcePlaylistURL: "https://open.spotify.com/playlist/test123",
		ExpectedCount:     1,
		CollectedCount:    1,
		Tracks:            []model.SpotifyTrack{track},
	}

	data, err := json.Marshal(playlist)
	if err != nil {
		t.Fatalf("failed to marshal SpotifyPlaylist: %v", err)
	}

	var decoded model.SpotifyPlaylist
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal SpotifyPlaylist: %v", err)
	}

	if decoded.Platform != "spotify" {
		t.Errorf("expected Platform 'spotify', got %q", decoded.Platform)
	}
	if decoded.PlaylistName != playlist.PlaylistName {
		t.Errorf("expected PlaylistName %q, got %q", playlist.PlaylistName, decoded.PlaylistName)
	}
	if decoded.SourcePlaylistURL != playlist.SourcePlaylistURL {
		t.Errorf("expected SourcePlaylistURL %q, got %q", playlist.SourcePlaylistURL, decoded.SourcePlaylistURL)
	}
	if decoded.ExpectedCount != 1 || decoded.CollectedCount != 1 {
		t.Errorf("expected count 1, got expected=%d collected=%d", decoded.ExpectedCount, decoded.CollectedCount)
	}
	if len(decoded.Tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(decoded.Tracks))
	}

	decTrack := decoded.Tracks[0]
	if decTrack.ID != track.ID || decTrack.Title != track.Title || decTrack.Duration != track.Duration {
		t.Errorf("track mismatch: %+v vs %+v", decTrack, track)
	}
	if len(decTrack.Artists) != 1 || decTrack.Artists[0] != "Rick Astley" {
		t.Errorf("artists mismatch: %+v", decTrack.Artists)
	}
}

func TestYTMModel_JSON(t *testing.T) {
	ytTrack := model.YTMTrack{
		VideoID:    "dQw4w9WgXcQ",
		SetVideoID: "SET12345",
		Title:      "Never Gonna Give You Up",
		Artists:    []string{"Rick Astley"},
		Duration:   "3:33",
	}

	ytPlaylist := model.YTMPlaylist{
		ID:          "PLtest123",
		Title:       "YTM Test Playlist",
		Description: "Playlist Description",
		Privacy:     "PRIVATE",
		TrackCount:  1,
		Tracks:      []model.YTMTrack{ytTrack},
	}

	data, err := json.Marshal(ytPlaylist)
	if err != nil {
		t.Fatalf("failed to marshal YTMPlaylist: %v", err)
	}

	var decoded model.YTMPlaylist
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal YTMPlaylist: %v", err)
	}

	if decoded.ID != ytPlaylist.ID || decoded.Title != ytPlaylist.Title || decoded.Description != ytPlaylist.Description {
		t.Errorf("playlist fields mismatch: %+v", decoded)
	}
	if len(decoded.Tracks) != 1 || decoded.Tracks[0].VideoID != "dQw4w9WgXcQ" {
		t.Errorf("tracks mismatch: %+v", decoded.Tracks)
	}

	searchRes := model.YTMSearchResult{
		VideoID:    "vid123",
		Title:      "Search Song",
		Artists:    []string{"Artist A", "Artist B"},
		ResultType: "SONG",
		Score:      95,
	}
	sData, err := json.Marshal(searchRes)
	if err != nil {
		t.Fatalf("failed to marshal YTMSearchResult: %v", err)
	}
	var decSearch model.YTMSearchResult
	if err := json.Unmarshal(sData, &decSearch); err != nil {
		t.Fatalf("failed to unmarshal YTMSearchResult: %v", err)
	}
	if decSearch.VideoID != "vid123" || decSearch.Score != 95 {
		t.Errorf("search result mismatch: %+v", decSearch)
	}

	editAction := model.YTMEditAction{
		Action:         "ACTION_ADD_VIDEO",
		AddedVideoID:   "vidAdd",
		RemovedVideoID: "vidRem",
		SetVideoID:     "set1",
	}
	eData, err := json.Marshal(editAction)
	if err != nil {
		t.Fatalf("failed to marshal YTMEditAction: %v", err)
	}
	var decAction model.YTMEditAction
	if err := json.Unmarshal(eData, &decAction); err != nil {
		t.Fatalf("failed to unmarshal YTMEditAction: %v", err)
	}
	if decAction.Action != "ACTION_ADD_VIDEO" || decAction.AddedVideoID != "vidAdd" {
		t.Errorf("edit action mismatch: %+v", decAction)
	}
}

func TestSyncModel_JSON(t *testing.T) {
	syncResult := model.SyncResult{
		Direction:         "spotify-to-youtube-music",
		SourcePlatform:    "spotify",
		TargetPlatform:    "youtube-music",
		PlaylistID:        "PLmock123",
		PlaylistURL:       "https://music.youtube.com/playlist?list=PLmock123",
		WebURL:            "https://www.youtube.com/playlist?list=PLmock123",
		Title:             "Mock Synced Playlist",
		SourcePlaylistURL: "https://open.spotify.com/playlist/spMock123",
		TotalSourceTracks: 10,
		AddedTracks:       8,
		SkippedTracks:     2,
		Skipped: []model.SkippedTrack{
			{
				Index:   1,
				Title:   "Mock Skipped Track",
				Artists: []string{"Mock Artist 1"},
				Reason:  "low confidence on destination platform",
			},
		},
		AddedAfterReview: []model.AddedTrack{
			{
				Index:            2,
				Title:            "Mock Reviewed Track",
				Artists:          []string{"Mock Artist 2"},
				TargetTrackID:    "mockVid2",
				DestinationTitle: "Mock Reviewed Track (Official)",
			},
		},
		RemovedExtraTracks: []model.RemovedTrack{
			{
				TargetTrackID: "oldVid123",
				Title:         "Old Track",
				Artists:       []string{"Old Artist"},
			},
		},
		LastSyncedAt: "2026-01-01T00:00:00Z",
		Verification: &model.Verification{
			PageTitle:      "Mock Synced Playlist",
			PageTrackCount: 8,
			Description:    "Migrated playlist",
		},
	}

	data, err := json.Marshal(syncResult)
	if err != nil {
		t.Fatalf("failed to marshal SyncResult: %v", err)
	}

	var decoded model.SyncResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal SyncResult: %v", err)
	}

	if decoded.PlaylistID != syncResult.PlaylistID {
		t.Errorf("expected PlaylistID %q, got %q", syncResult.PlaylistID, decoded.PlaylistID)
	}
	if decoded.Direction != "spotify-to-youtube-music" {
		t.Errorf("expected Direction 'spotify-to-youtube-music', got %q", decoded.Direction)
	}
	if decoded.SourcePlatform != "spotify" || decoded.TargetPlatform != "youtube-music" {
		t.Errorf("platform mismatch: source=%q target=%q", decoded.SourcePlatform, decoded.TargetPlatform)
	}
	if decoded.TotalSourceTracks != 10 || decoded.AddedTracks != 8 || decoded.SkippedTracks != 2 {
		t.Errorf("counts mismatch in decoded SyncResult")
	}
	if len(decoded.Skipped) != 1 || decoded.Skipped[0].Title != "Mock Skipped Track" {
		t.Errorf("skipped tracks mismatch: %+v", decoded.Skipped)
	}
	if len(decoded.AddedAfterReview) != 1 || decoded.AddedAfterReview[0].TargetTrackID != "mockVid2" {
		t.Errorf("added after review mismatch: %+v", decoded.AddedAfterReview)
	}
	if len(decoded.RemovedExtraTracks) != 1 || decoded.RemovedExtraTracks[0].TargetTrackID != "oldVid123" {
		t.Errorf("removed extra tracks mismatch: %+v", decoded.RemovedExtraTracks)
	}
	if decoded.Verification == nil || decoded.Verification.PageTrackCount != 8 {
		t.Errorf("verification mismatch: %+v", decoded.Verification)
	}
}
