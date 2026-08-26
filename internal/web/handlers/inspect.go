package handlers

import (
	"net/http"

	"playlistsync/internal/model"
)

// playlistInspectResponse is the GET /inspect/playlist body (spec 02 §2.4):
// playlist metadata plus per-track match state derived from the last
// reconcile diff.
type playlistInspectResponse struct {
	Platform   string             `json:"platform"`
	PlaylistID string             `json:"playlist_id"`
	Title      string             `json:"title"`
	TrackCount int                `json:"track_count"`
	Tracks     []trackInspectView `json:"tracks"`
}

type trackInspectView struct {
	Index      int      `json:"index"`
	Title      string   `json:"title"`
	Artists    []string `json:"artists"`
	Duration   string   `json:"duration,omitempty"`
	TargetID   string   `json:"target_id,omitempty"`
	MatchState string   `json:"match_state"` // "added" | "removed" | "retained" | "skipped" | "unknown"
}

// RegisterInspectHandlers mounts GET /api/v1/inspect/playlist (spec 02 §2.4).
func RegisterInspectHandlers(mux *http.ServeMux, cfg HandlerConfig) {
	mux.HandleFunc("GET /api/v1/inspect/playlist", inspectPlaylist(cfg))
}

func inspectPlaylist(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			writeErrorJSON(w, http.StatusBadRequest, "missing id query parameter")
			return
		}
		platform := r.URL.Query().Get("platform")

		var trackCount int
		switch platform {
		case "spotify", "":
			if cfg.InspectSource == nil {
				writeErrorJSON(w, http.StatusServiceUnavailable, "source inspector not configured")
				return
			}
			pl, err := cfg.InspectSource(id)
			if err != nil {
				writeErrorJSON(w, http.StatusNotFound, "source playlist not found: "+sanitize(err.Error()))
				return
			}
			trackCount = len(pl.Tracks)
			writeJSON(w, http.StatusOK, playlistInspectResponse{
				Platform:   "spotify",
				PlaylistID: id,
				Title:      pl.PlaylistName,
				TrackCount: trackCount,
				Tracks:     sourceTrackViews(cfg, pl),
			})
		case "youtube-music", "ytmusic":
			if cfg.InspectTarget == nil {
				writeErrorJSON(w, http.StatusServiceUnavailable, "target inspector not configured")
				return
			}
			pl, err := cfg.InspectTarget(id)
			if err != nil {
				writeErrorJSON(w, http.StatusNotFound, "target playlist not found: "+sanitize(err.Error()))
				return
			}
			trackCount = len(pl.Tracks)
			writeJSON(w, http.StatusOK, playlistInspectResponse{
				Platform:   "youtube-music",
				PlaylistID: id,
				Title:      pl.Title,
				TrackCount: trackCount,
				Tracks:     targetTrackViews(cfg, pl),
			})
		default:
			writeErrorJSON(w, http.StatusBadRequest, "unsupported platform: "+platform)
		}
	}
}

// matchStateIndex pre-indexes the last diff's partitions by source track index
// (plan QC qc3-M4): O(n) build once, O(1) lookup per track, replacing the
// previous per-track O(m) linear scans (O(n*m) total). Partition priority
// matches the historic first-scan order: added > retained > skipped.
func matchStateIndex(cfg HandlerConfig) map[int]string {
	idx := make(map[int]string)
	if cfg.State == nil {
		return idx
	}
	_, _, diff, _ := cfg.State.Snapshot()
	if diff == nil {
		return idx
	}
	mark := func(index int, state string) {
		if _, ok := idx[index]; !ok {
			idx[index] = state
		}
	}
	for i := range diff.Added {
		mark(diff.Added[i].Index, "added")
	}
	for i := range diff.Retained {
		mark(diff.Retained[i].Index, "retained")
	}
	for i := range diff.Skipped {
		mark(diff.Skipped[i].Index, "skipped")
	}
	return idx
}

func sourceTrackViews(cfg HandlerConfig, pl *model.SpotifyPlaylist) []trackInspectView {
	states := matchStateIndex(cfg)
	out := make([]trackInspectView, 0, len(pl.Tracks))
	for _, t := range pl.Tracks {
		state := states[t.Index]
		if state == "" {
			state = "unknown"
		}
		out = append(out, trackInspectView{
			Index:      t.Index,
			Title:      t.Title,
			Artists:    t.Artists,
			Duration:   t.Duration,
			MatchState: state,
		})
	}
	return out
}

// removedVideoIDSet returns the set of target video ids scheduled for removal.
func removedVideoIDSet(cfg HandlerConfig) map[string]bool {
	set := make(map[string]bool)
	if cfg.State != nil {
		_, _, diff, _ := cfg.State.Snapshot()
		if diff != nil {
			for i := range diff.Removed {
				set[diff.Removed[i].VideoID] = true
			}
		}
	}
	return set
}

func targetTrackViews(cfg HandlerConfig, pl *model.YTMPlaylist) []trackInspectView {
	removed := removedVideoIDSet(cfg)
	out := make([]trackInspectView, 0, len(pl.Tracks))
	for i, t := range pl.Tracks {
		state := "retained"
		if removed[t.VideoID] {
			state = "removed"
		}
		out = append(out, trackInspectView{
			Index:      i + 1,
			Title:      t.Title,
			Artists:    t.Artists,
			Duration:   t.Duration,
			TargetID:   t.VideoID,
			MatchState: state,
		})
	}
	return out
}
