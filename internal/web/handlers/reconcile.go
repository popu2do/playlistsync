package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"playlistsync/internal/invariants"
	"playlistsync/internal/model"
	"playlistsync/internal/web/bridge"
)

// ReconcileParams is the POST /reconcile/start body (spec 07 §3.2).
type ReconcileParams struct {
	SourcePlaylistID string `json:"source_playlist_id"`
	TargetPlaylistID string `json:"target_playlist_id"`
	CleanExtra       bool   `json:"clean_extra"`
	SyncOrder        bool   `json:"sync_order"`
	Concurrency      int    `json:"concurrency"`
}

// DiffResult is the web-shaped difference between source and target: the
// three-way Added / Removed / Retained partition plus skipped tracks. The
// RunReconcile seam produces it (cmd/web maps engine.DiffPlan); handlers treat
// it as an opaque summary they store, serve and preflight.
type DiffResult struct {
	SourceTotal int                  `json:"source_total"`
	Added       []model.SpotifyTrack `json:"added"`    // tracks to add (missing in target)
	Removed     []model.YTMTrack     `json:"removed"`  // extra tracks to prune (clean_extra)
	Retained    []model.AddedTrack   `json:"retained"` // tracks already matched
	Skipped     []model.SkippedTrack `json:"skipped"`  // unmatched / skipped tracks
}

// Counts returns the partition counts.
func (d *DiffResult) Counts() (added, removed, retained, skipped int) {
	if d == nil {
		return 0, 0, 0, 0
	}
	return len(d.Added), len(d.Removed), len(d.Retained), len(d.Skipped)
}

// ApplyResult is the outcome of POST /reconcile/apply.
type ApplyResult struct {
	Applied   bool                          `json:"applied"`
	Output    *model.SyncResult             `json:"output,omitempty"`
	Invariant *invariants.InvariantSnapshot `json:"invariant,omitempty"`
}

// RegisterReconcileHandlers mounts the reconciliation & arbitration endpoints
// (spec 02 §2.3, spec 07 §3.2):
//
//	POST /api/v1/reconcile/start        launch async diff job (SSE events)
//	GET  /api/v1/reconcile/diff         added/removed/retained + counts
//	POST /api/v1/arbitrate/decision     resolve a suspended arbitration
//	POST /api/v1/reconcile/apply        apply the diff (invariant preflight)
func RegisterReconcileHandlers(mux *http.ServeMux, cfg HandlerConfig) {
	mux.HandleFunc("POST /api/v1/reconcile/start", reconcileStart(cfg))
	mux.HandleFunc("GET /api/v1/reconcile/diff", reconcileDiff(cfg))
	mux.HandleFunc("POST /api/v1/arbitrate/decision", arbitrateDecision(cfg))
	mux.HandleFunc("POST /api/v1/reconcile/apply", reconcileApply(cfg))
}

func reconcileStart(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body ReconcileParams
		if err := readJSON(r, &body); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if body.SourcePlaylistID == "" || body.TargetPlaylistID == "" {
			writeErrorJSON(w, http.StatusBadRequest, "source_playlist_id and target_playlist_id are required")
			return
		}
		if cfg.RunReconcile == nil {
			writeErrorJSON(w, http.StatusServiceUnavailable, "reconcile runner not configured")
			return
		}

		jobID := "rec_" + time.Now().UTC().Format("20060102T150405.000000000")
		if cfg.State != nil {
			cfg.State.BeginJob(jobID)
		}

		// Emit the initial progress beacon; the runner emits further
		// DIFF_PROGRESS events through the same callback (spec 02 §3.1 #1).
		emit := func(eventType string, data interface{}) {
			if cfg.Broadcaster != nil {
				cfg.Broadcaster.Broadcast(eventType, data)
			}
		}
		emit("DIFF_PROGRESS", map[string]interface{}{
			"worker_id": jobID, "processed": 0, "total": 0, "status": "RUNNING",
		})

		// Async job: the request returns 202; the job writes to CockpitState
		// and streams events. context.Background() intentionally decouples the
		// job from the request lifetime (spec 05 §4: the backend state machine
		// survives browser refresh/tab close).
		go func() {
			diff, err := cfg.RunReconcile(context.Background(), body, emit)
			if err != nil {
				if cfg.State != nil {
					cfg.State.FailJob(jobID, err)
				}
				emit("RECONCILE_FAILED", map[string]interface{}{
					"worker_id": jobID, "error": sanitize(err.Error()),
				})
				return
			}
			if cfg.State != nil {
				cfg.State.CompleteJob(jobID, diff)
			}
			emit("DIFF_COMPLETE", map[string]interface{}{
				"worker_id": jobID, "status": "COMPLETE",
				"added": len(diff.Added), "removed": len(diff.Removed),
				"retained": len(diff.Retained), "skipped": len(diff.Skipped),
			})
		}()

		writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"job_id":                 jobID,
			"status":                 "RUNNING",
			"estimated_total_tracks": 0,
		})
	}
}

// diffResponse is the GET /reconcile/diff body (spec 02 §2.3).
type diffResponse struct {
	JobID    string               `json:"job_id,omitempty"`
	Status   string               `json:"status"`
	Error    string               `json:"error,omitempty"`
	Added    []model.SpotifyTrack `json:"added"`
	Removed  []model.YTMTrack     `json:"removed"`
	Retained []model.AddedTrack   `json:"retained"`
	Skipped  []model.SkippedTrack `json:"skipped"`
	Counts   diffCounts           `json:"counts"`
}

type diffCounts struct {
	Added               int `json:"added"`
	Removed             int `json:"removed"`
	Retained            int `json:"retained"`
	Skipped             int `json:"skipped"`
	ArbitrationRequired int `json:"arbitration_required"`
}

func reconcileDiff(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := diffResponse{Status: "idle", Added: []model.SpotifyTrack{},
			Removed: []model.YTMTrack{}, Retained: []model.AddedTrack{}, Skipped: []model.SkippedTrack{}}
		if cfg.State != nil {
			jobID, status, diff, lastErr := cfg.State.Snapshot()
			resp.JobID = jobID
			resp.Status = status
			resp.Error = lastErr
			if diff != nil {
				resp.Added = diff.Added
				resp.Removed = diff.Removed
				resp.Retained = diff.Retained
				resp.Skipped = diff.Skipped
			}
			a, rm, rt, sk := 0, 0, 0, 0
			if diff != nil {
				a, rm, rt, sk = diff.Counts()
			}
			resp.Counts = diffCounts{Added: a, Removed: rm, Retained: rt, Skipped: sk}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// arbitrateRequest is the POST /arbitrate/decision body (spec 07 §3.2).
type arbitrateRequest struct {
	TrackID          string `json:"track_id"`
	Action           string `json:"action"`
	SelectedTargetID string `json:"selected_target_id,omitempty"`
}

func arbitrateDecision(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body arbitrateRequest
		if err := readJSON(r, &body); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if body.TrackID == "" {
			writeErrorJSON(w, http.StatusBadRequest, "track_id is required")
			return
		}
		action := bridge.ArbitrationAction(body.Action)
		switch action {
		case bridge.ActionSelectCandidate, bridge.ActionSkip, bridge.ActionCustomID:
		default:
			writeErrorJSON(w, http.StatusBadRequest, "invalid action: "+body.Action)
			return
		}
		if action == bridge.ActionSelectCandidate && body.SelectedTargetID == "" {
			writeErrorJSON(w, http.StatusBadRequest, "selected_target_id is required for SELECT_CANDIDATE")
			return
		}
		if cfg.ResolveArbitration == nil {
			writeErrorJSON(w, http.StatusServiceUnavailable, "arbitration resolver not configured")
			return
		}
		decision := &bridge.ArbitrationDecision{
			TrackID:          body.TrackID,
			Action:           action,
			SelectedTargetID: body.SelectedTargetID,
		}
		if !cfg.ResolveArbitration(decision) {
			writeErrorJSON(w, http.StatusNotFound, "no pending arbitration for track "+body.TrackID)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":    true,
			"resumed_at": time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// applyRequest is the POST /reconcile/apply body (spec 02 §2.3).
type applyRequest struct {
	ForceOverrideInvariants bool `json:"force_override_invariants,omitempty"`
}

func reconcileApply(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body applyRequest
		if err := readJSON(r, &body); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if cfg.State == nil || cfg.ApplyDiff == nil {
			writeErrorJSON(w, http.StatusServiceUnavailable, "apply not configured")
			return
		}
		_, status, diff, lastErr := cfg.State.Snapshot()
		if diff == nil {
			if status == "failed" {
				writeErrorJSON(w, http.StatusConflict, "reconcile failed: "+lastErr)
				return
			}
			writeErrorJSON(w, http.StatusConflict, "no diff to apply: run /reconcile/start first")
			return
		}

		// Invariant preflight (spec 02 §2.3: apply requires all five
		// invariants to pass, else 409 Invariant Conflict). The verification
		// snapshot is broadcast and stored so the radar can reflect it.
		var snap *invariants.InvariantSnapshot
		if cfg.Verifier != nil {
			input := verifyInputFromDiff(diff, body.ForceOverrideInvariants)
			s := cfg.Verifier.Verify(input)
			snap = &s
			if cfg.State != nil {
				cfg.State.StoreSnapshot(snap)
			}
			if cfg.Broadcaster != nil {
				cfg.Broadcaster.Broadcast("INVARIANT_SNAPSHOT", snap)
			}
			if !snap.AllPassed && !body.ForceOverrideInvariants {
				writeErrorJSON(w, http.StatusConflict, "invariant conflict: "+invariantFailureSummary(*snap))
				return
			}
		}

		res, err := cfg.ApplyDiff(r.Context(), diff, body.ForceOverrideInvariants)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "apply failed: "+sanitize(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, ApplyResult{
			Applied:   true,
			Output:    res,
			Invariant: snap,
		})
	}
}

// verifyInputFromDiff assembles a VerifyInput from the diff partition,
// deriving counts from the three-way split plus skipped tracks.
//
// The partition is expressed over the *target* universe only: added source
// tracks are pending (they have no target ID until applied), so the "current
// target" is retained ∪ removed and AddedIDs stays empty. This keeps the diff
// preflight honest — completeness after application is re-verified by
// GET /api/v1/verify/invariants with real target IDs (verify.go).
func verifyInputFromDiff(diff *DiffResult, ignoreOrder bool) invariants.VerifyInput {
	added, _, retained, skipped := diff.Counts()
	in := invariants.VerifyInput{
		SourceTotal:  diff.SourceTotal,
		SyncedCount:  added + retained,
		SkippedCount: skipped,
		FailedCount:  0,
		SyncOrder:    !ignoreOrder,
	}
	// AddedIDs stays empty: pending adds carry no target ID yet and must not
	// leak into the target partition.
	for i := range diff.Removed {
		in.RemovedIDs = append(in.RemovedIDs, diff.Removed[i].VideoID)
	}
	for i := range diff.Retained {
		in.RetainedIDs = append(in.RetainedIDs, diff.Retained[i].TargetTrackID)
	}
	// Target universe = retained + removed (added tracks do not exist in the
	// target until applied).
	for _, id := range in.RetainedIDs {
		in.TargetIDs = append(in.TargetIDs, id)
	}
	for _, id := range in.RemovedIDs {
		in.TargetIDs = append(in.TargetIDs, id)
	}
	return in
}

// invariantFailureSummary names the first failing invariant for a 409 body.
func invariantFailureSummary(snap invariants.InvariantSnapshot) string {
	switch {
	case !snap.IsCountConserved:
		return "count conservation violated"
	case snap.HasDuplicateTargetIDs:
		return "duplicate target ids: " + strings.Join(snap.DuplicateIDs, ",")
	case len(snap.DisorderedIndices) > 0:
		return "order monotonicity violated"
	case !snap.IsDiffComplete:
		return "diff partition incomplete"
	case !snap.IsZeroTraceClean:
		return "zero-trace residue detected"
	default:
		return "invariant check failed"
	}
}
