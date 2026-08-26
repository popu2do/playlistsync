package handlers

import (
	"net/http"
	"strings"

	"playlistsync/internal/invariants"
	"playlistsync/internal/model"
)

// pendingTargetIDPrefix marks summary entries whose target id is not yet known
// (cockpit apply persists an auditable artifact without performing live YTM
// mutation; added tracks have no real video id yet). Such placeholder ids must
// never feed the invariant partition (Major-3 fix): AssertDiffComplete's
// no-leakage rule would false-fail because the placeholder is not a real
// target id.
const pendingTargetIDPrefix = "pending_"

// RegisterVerifyHandlers mounts GET /api/v1/verify/invariants (spec 02 §2.4):
// it assembles a VerifyInput from the loaded SyncResult plus supplementary
// playlists, runs the InvariantVerifier, and ratifies the flat
// InvariantSnapshot JSON (assignment: "ratify flat InvariantSnapshot JSON").
func RegisterVerifyHandlers(mux *http.ServeMux, v invariants.InvariantVerifier, cfg HandlerConfig) {
	mux.HandleFunc("GET /api/v1/verify/invariants", verifyInvariants(v, cfg))
}

func verifyInvariants(v invariants.InvariantVerifier, cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v == nil {
			writeErrorJSON(w, http.StatusServiceUnavailable, "verifier not configured")
			return
		}
		targetID := r.URL.Query().Get("target_id")
		if targetID == "" {
			writeErrorJSON(w, http.StatusBadRequest, "missing target_id query parameter")
			return
		}

		// 1. Load the sync result (the primary count source).
		if cfg.LoadResult == nil {
			writeErrorJSON(w, http.StatusServiceUnavailable, "result store not configured")
			return
		}
		res, err := cfg.LoadResult(targetID)
		if err != nil {
			writeErrorJSON(w, http.StatusNotFound, "sync result not found: "+sanitize(err.Error()))
			return
		}

		// 2. Supplementary playlists (optional — order/uniqueness domains).
		var source *model.SpotifyPlaylist
		if cfg.InspectSource != nil {
			if sp, err := cfg.InspectSource(targetID); err == nil {
				source = sp
			}
		}
		var target *model.YTMPlaylist
		if cfg.InspectTarget != nil {
			if tp, err := cfg.InspectTarget(targetID); err == nil {
				target = tp
			}
		}

		// 3. Assemble VerifyInput (SourceTotal formula per task brief) and run.
		input := buildVerifyInput(res, source, target)
		snap := v.Verify(input)

		// 4. Persist for the session radar + broadcast the snapshot event.
		if cfg.State != nil {
			cfg.State.StoreSnapshot(&snap)
		}
		if cfg.Broadcaster != nil {
			cfg.Broadcaster.Broadcast("INVARIANT_SNAPSHOT", &snap)
		}

		// Ratify the flat InvariantSnapshot JSON (spec 02 §3.1 event #3 shape).
		writeJSON(w, http.StatusOK, snap)
	}
}

// buildVerifyInput assembles invariants.VerifyInput from a SyncResult plus
// supplementary source/target playlists. The SourceTotal formula is
// SourceTotal = SyncedCount + SkippedCount + FailedCount where counts come
// from the result (SyncResult has no failed bucket, so FailedCount is 0 and
// any drift between TotalSourceTracks and Added+Skipped fails conservation).
//
// Added/Removed/Retained ids are derived from the result's per-track records;
// the retained universe is the target playlist minus this mutation's
// added/removed ids.
func buildVerifyInput(res *model.SyncResult, source *model.SpotifyPlaylist, target *model.YTMPlaylist) invariants.VerifyInput {
	in := invariants.VerifyInput{
		SourceTotal:  res.TotalSourceTracks,
		SyncedCount:  res.AddedTracks,
		SkippedCount: res.SkippedTracks,
		FailedCount:  0,
		SyncOrder:    res.SyncOrder,
	}

	// Source order (Invariant 3 needs the source id sequence).
	if source != nil {
		for _, t := range source.Tracks {
			if t.ID != "" {
				in.SourceOrder = append(in.SourceOrder, t.ID)
			}
		}
	}

	// Target universe (Invariant 2 + 4).
	if target != nil {
		for _, t := range target.Tracks {
			if t.VideoID != "" {
				in.TargetIDs = append(in.TargetIDs, t.VideoID)
			}
		}
	}

	// Added / removed ids from the result's per-track records. Placeholder
	// pending ids (Major-3) and empty ids are dropped — only real target ids
	// enter the invariant partition.
	for _, a := range res.AddedAfterReview {
		if a.TargetTrackID != "" && !strings.HasPrefix(a.TargetTrackID, pendingTargetIDPrefix) {
			in.AddedIDs = append(in.AddedIDs, a.TargetTrackID)
		}
	}
	for _, r := range res.RemovedExtraTracks {
		if r.TargetTrackID != "" {
			in.RemovedIDs = append(in.RemovedIDs, r.TargetTrackID)
		}
	}

	// Retained = target ids not part of this mutation.
	addedSet := make(map[string]bool, len(in.AddedIDs))
	for _, id := range in.AddedIDs {
		addedSet[id] = true
	}
	removedSet := make(map[string]bool, len(in.RemovedIDs))
	for _, id := range in.RemovedIDs {
		removedSet[id] = true
	}
	for _, id := range in.TargetIDs {
		if !addedSet[id] && !removedSet[id] {
			in.RetainedIDs = append(in.RetainedIDs, id)
		}
	}
	return in
}
