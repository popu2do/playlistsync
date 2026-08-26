package handlers

import (
	"errors"
	"net/http"
	"testing"

	"playlistsync/internal/model"
)

func TestInspectPlaylistSource(t *testing.T) {
	cfg := HandlerConfig{
		InspectSource: func(id string) (*model.SpotifyPlaylist, error) {
			if id != "my_pl" {
				return nil, errors.New("not found")
			}
			return &model.SpotifyPlaylist{
				Platform:       "spotify",
				PlaylistName:   "My Playlist",
				ExpectedCount:  2,
				CollectedCount: 2,
				Tracks: []model.SpotifyTrack{
					{Index: 1, ID: "sp1", Title: "Alpha", Artists: []string{"A"}},
					{Index: 2, ID: "sp2", Title: "Beta", Artists: []string{"B"}},
				},
			}, nil
		},
	}
	mux := testMux(t, cfg)

	tests := []struct {
		name   string
		target string
		want   int
	}{
		{"source ok", "/api/v1/inspect/playlist?id=my_pl&platform=spotify", http.StatusOK},
		{"missing id", "/api/v1/inspect/playlist?platform=spotify", http.StatusBadRequest},
		{"not found", "/api/v1/inspect/playlist?id=ghost&platform=spotify", http.StatusNotFound},
		{"bad platform", "/api/v1/inspect/playlist?id=my_pl&platform=netflix", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(t, mux, "GET", tc.target, "")
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
			if tc.want != http.StatusOK {
				return
			}
			var resp playlistInspectResponse
			if err := decodeJSON(t, w, &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Platform != "spotify" || resp.PlaylistID != "my_pl" || resp.Title != "My Playlist" {
				t.Errorf("meta wrong: %+v", resp)
			}
			if resp.TrackCount != 2 || len(resp.Tracks) != 2 {
				t.Errorf("tracks wrong: count=%d len=%d", resp.TrackCount, len(resp.Tracks))
			}
			if resp.Tracks[0].Title != "Alpha" {
				t.Errorf("first track wrong: %+v", resp.Tracks[0])
			}
		})
	}
}

func TestInspectPlaylistMatchStates(t *testing.T) {
	state := NewCockpitState()
	state.CompleteJob("rec_1", &DiffResult{
		SourceTotal: 3,
		Added:       []model.SpotifyTrack{{Index: 1, ID: "sp1"}},
		Retained:    []model.AddedTrack{{Index: 2, TargetTrackID: "yt2"}},
		Skipped:     []model.SkippedTrack{{Index: 3, Title: "Skip"}},
	})
	cfg := HandlerConfig{
		State: state,
		InspectSource: func(id string) (*model.SpotifyPlaylist, error) {
			return &model.SpotifyPlaylist{
				PlaylistName: "P",
				Tracks: []model.SpotifyTrack{
					{Index: 1, ID: "sp1", Title: "Alpha"},
					{Index: 2, ID: "sp2", Title: "Beta"},
					{Index: 3, ID: "sp3", Title: "Skip"},
				},
			}, nil
		},
	}
	mux := testMux(t, cfg)
	w := doReq(t, mux, "GET", "/api/v1/inspect/playlist?id=p&platform=spotify", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", w.Code, w.Body.String())
	}
	var resp playlistInspectResponse
	if err := decodeJSON(t, w, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"added", "retained", "skipped"}
	for i := range want {
		if resp.Tracks[i].MatchState != want[i] {
			t.Errorf("track %d match_state = %q, want %q", i, resp.Tracks[i].MatchState, want[i])
		}
	}
}

func TestInspectPlaylistTarget(t *testing.T) {
	state := NewCockpitState()
	state.CompleteJob("rec_1", &DiffResult{
		Removed: []model.YTMTrack{{VideoID: "yt_extra", Title: "Extra"}},
	})
	cfg := HandlerConfig{
		State: state,
		InspectTarget: func(id string) (*model.YTMPlaylist, error) {
			return &model.YTMPlaylist{
				ID: id, Title: "Target",
				Tracks: []model.YTMTrack{
					{VideoID: "yt_extra", Title: "Extra"},
					{VideoID: "yt_keep", Title: "Keep"},
				},
			}, nil
		},
	}
	mux := testMux(t, cfg)
	w := doReq(t, mux, "GET", "/api/v1/inspect/playlist?id=t&platform=ytmusic", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", w.Code, w.Body.String())
	}
	var resp playlistInspectResponse
	if err := decodeJSON(t, w, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Platform != "youtube-music" || resp.TrackCount != 2 {
		t.Errorf("target meta wrong: %+v", resp)
	}
	if resp.Tracks[0].MatchState != "removed" || resp.Tracks[1].MatchState != "retained" {
		t.Errorf("target match states wrong: %+v", resp.Tracks)
	}
}
