package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"playlistsync/internal/model"
	"playlistsync/internal/web/bridge"
)

// fakeReconcileRunner emits a progress event and returns a fixed diff.
type fakeReconcileRunner struct {
	diff      *DiffResult
	err       error
	emitted   []string
	startedAt time.Time
	block     chan struct{} // optional: block completion until closed
}

func (f *fakeReconcileRunner) fn() func(ctx context.Context, p ReconcileParams, emit func(string, interface{})) (*DiffResult, error) {
	return func(ctx context.Context, p ReconcileParams, emit func(string, interface{})) (*DiffResult, error) {
		f.startedAt = time.Now()
		emit("DIFF_PROGRESS", map[string]interface{}{"processed": 1, "total": 3})
		f.emitted = append(f.emitted, "DIFF_PROGRESS")
		if f.block != nil {
			<-f.block
		}
		return f.diff, f.err
	}
}

func sampleConservingDiff() *DiffResult {
	return &DiffResult{
		SourceTotal: 5,
		Added: []model.SpotifyTrack{
			{Index: 1, ID: "sp1", Title: "Alpha", Artists: []string{"A"}},
			{Index: 2, ID: "sp2", Title: "Beta", Artists: []string{"B"}},
		},
		Removed: []model.YTMTrack{
			{VideoID: "yt_extra", Title: "Extra"},
		},
		Retained: []model.AddedTrack{
			{Index: 3, Title: "Gamma", TargetTrackID: "yt_gamma"},
			{Index: 4, Title: "Delta", TargetTrackID: "yt_delta"},
		},
		Skipped: []model.SkippedTrack{
			{Index: 5, Title: "Epsilon", Reason: "low confidence"},
		},
	}
}

func TestReconcileStartLaunchesJobAsync(t *testing.T) {
	rec := NewRecordingBroadcaster(bridge.NewSSEEventRingBuffer(0))
	fake := &fakeReconcileRunner{diff: sampleConservingDiff(), block: make(chan struct{})}
	cfg := HandlerConfig{
		Broadcaster:        rec,
		Ring:               rec.Ring(),
		State:              NewCockpitState(),
		RunReconcile:       fake.fn(),
		ResolveArbitration: func(*bridge.ArbitrationDecision) bool { return false },
		ApplyDiff: func(ctx context.Context, diff *DiffResult, force bool) (*model.SyncResult, error) {
			return &model.SyncResult{TotalSourceTracks: diff.SourceTotal}, nil
		},
	}
	mux := testMux(t, cfg)

	// The runner blocks until we close block, proving the handler returns 202
	// BEFORE the job completes (async contract).
	w := doReq(t, mux, "POST", "/api/v1/reconcile/start",
		`{"source_playlist_id":"s","target_playlist_id":"t","sync_order":true,"concurrency":2}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", w.Code, w.Body.String())
	}
	var resp struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.JobID == "" || resp.Status != "RUNNING" {
		t.Errorf("job resp = %+v", resp)
	}

	// The initial DIFF_PROGRESS event must already be broadcast (emitted by
	// the handler at launch; the fake's own event follows once it runs).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, _ := rec.Ring().ReadSince(0)
		if len(events) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	found := false
	events, _ := rec.Ring().ReadSince(0)
	for _, ev := range events {
		if ev.Type == "DIFF_PROGRESS" {
			found = true
		}
	}
	if !found {
		t.Error("no DIFF_PROGRESS event emitted at reconcile start")
	}

	// Release the job and confirm the state transitions to complete.
	close(fake.block)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, status, _, _ := cfg.State.Snapshot()
		if status == "complete" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, status, diff, _ := cfg.State.Snapshot()
	if status != "complete" {
		t.Fatalf("job status = %q, want complete", status)
	}
	if diff == nil || diff.SourceTotal != 5 {
		t.Errorf("diff not stored: %+v", diff)
	}
}

func TestReconcileStartValidation(t *testing.T) {
	cfg := HandlerConfig{State: NewCockpitState()}
	mux := testMux(t, cfg)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"missing ids", `{}`, http.StatusBadRequest},
		{"missing target", `{"source_playlist_id":"s"}`, http.StatusBadRequest},
		{"bad body", `{`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(t, mux, "POST", "/api/v1/reconcile/start", tc.body)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestReconcileDiffPartition(t *testing.T) {
	state := NewCockpitState()
	state.CompleteJob("rec_1", sampleConservingDiff())
	mux := testMux(t, HandlerConfig{State: state})

	w := doReq(t, mux, "GET", "/api/v1/reconcile/diff", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", w.Code, w.Body.String())
	}
	var resp struct {
		JobID    string               `json:"job_id"`
		Status   string               `json:"status"`
		Added    []model.SpotifyTrack `json:"added"`
		Removed  []model.YTMTrack     `json:"removed"`
		Retained []model.AddedTrack   `json:"retained"`
		Counts   struct {
			Added    int `json:"added"`
			Removed  int `json:"removed"`
			Retained int `json:"retained"`
			Skipped  int `json:"skipped"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.JobID != "rec_1" || resp.Status != "complete" {
		t.Errorf("job meta = %s / %s", resp.JobID, resp.Status)
	}
	if len(resp.Added) != 2 || len(resp.Removed) != 1 || len(resp.Retained) != 2 {
		t.Errorf("partition wrong: added=%d removed=%d retained=%d",
			len(resp.Added), len(resp.Removed), len(resp.Retained))
	}
	if resp.Counts.Added != 2 || resp.Counts.Removed != 1 || resp.Counts.Retained != 2 || resp.Counts.Skipped != 1 {
		t.Errorf("counts wrong: %+v", resp.Counts)
	}
}

func TestReconcileDiffIdle(t *testing.T) {
	mux := testMux(t, HandlerConfig{State: NewCockpitState()})
	w := doReq(t, mux, "GET", "/api/v1/reconcile/diff", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Status != "idle" {
		t.Errorf("status = %q, want idle", resp.Status)
	}
}

func TestArbitrateDecision(t *testing.T) {
	var resolved []*bridge.ArbitrationDecision
	cfg := HandlerConfig{
		State: NewCockpitState(),
		ResolveArbitration: func(d *bridge.ArbitrationDecision) bool {
			if d.TrackID == "pending_track" {
				resolved = append(resolved, d)
				return true
			}
			return false
		},
	}
	mux := testMux(t, cfg)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"select candidate", `{"track_id":"pending_track","action":"SELECT_CANDIDATE","selected_target_id":"yt_opt"}`, http.StatusOK},
		{"skip", `{"track_id":"pending_track","action":"SKIP"}`, http.StatusOK},
		{"custom id", `{"track_id":"pending_track","action":"CUSTOM_ID","selected_target_id":"yt_custom"}`, http.StatusOK},
		{"no pending", `{"track_id":"ghost","action":"SKIP"}`, http.StatusNotFound},
		{"invalid action", `{"track_id":"pending_track","action":"EXPLODE"}`, http.StatusBadRequest},
		{"missing track", `{"action":"SKIP"}`, http.StatusBadRequest},
		{"bad body", `{`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(t, mux, "POST", "/api/v1/arbitrate/decision", tc.body)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}

	if len(resolved) != 3 {
		t.Fatalf("resolved decisions = %d, want 3 (all pending_track 200 cases)", len(resolved))
	}
	// The first (SELECT_CANDIDATE) decision must carry the selected target.
	if resolved[0].Action != bridge.ActionSelectCandidate || resolved[0].SelectedTargetID != "yt_opt" {
		t.Errorf("first decision wrong: %+v", resolved[0])
	}
	// CUSTOM_ID also requires the target id.
	if resolved[2].Action != bridge.ActionCustomID || resolved[2].SelectedTargetID != "yt_custom" {
		t.Errorf("custom decision wrong: %+v", resolved[2])
	}
}

func TestArbitrateDecisionResolvesRealBridge(t *testing.T) {
	// Integration: a real WebReviewBridge pending arbitration must be woken by
	// the handler's seam. The bridge broadcasts ARBITRATION_REQUIRED through
	// the recording broadcaster.
	rec := NewRecordingBroadcaster(bridge.NewSSEEventRingBuffer(0))
	arb := bridge.NewWebReviewBridge(rec, time.Minute)
	cfg := HandlerConfig{
		Broadcaster:        rec,
		Ring:               rec.Ring(),
		State:              NewCockpitState(),
		ResolveArbitration: arb.ResolveArbitration,
	}
	mux := testMux(t, cfg)

	req := &bridge.ArbitrationRequest{TrackID: "sp_track_1", SourceTitle: "Halo"}
	go func() {
		_, _ = arb.RequestArbitration(context.Background(), req)
	}()

	// Wait for the pending registration + ARBITRATION_REQUIRED broadcast.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, _ := rec.Ring().ReadSince(0)
		for _, ev := range events {
			if ev.Type == "ARBITRATION_REQUIRED" {
				goto registered
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
registered:

	w := doReq(t, mux, "POST", "/api/v1/arbitrate/decision",
		`{"track_id":"sp_track_1","action":"SELECT_CANDIDATE","selected_target_id":"yt_match"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
}

func TestReconcileApply(t *testing.T) {
	state := NewCockpitState()
	state.CompleteJob("rec_1", sampleConservingDiff())

	applyCalls := 0
	cfg := HandlerConfig{
		State: state,
		ApplyDiff: func(ctx context.Context, diff *DiffResult, force bool) (*model.SyncResult, error) {
			applyCalls++
			return &model.SyncResult{TotalSourceTracks: diff.SourceTotal, AddedTracks: 3}, nil
		},
	}
	mux := testMux(t, cfg)

	w := doReq(t, mux, "POST", "/api/v1/reconcile/apply", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var resp ApplyResult
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Applied {
		t.Error("apply not flagged applied")
	}
	if resp.Output == nil || resp.Output.TotalSourceTracks != 5 {
		t.Errorf("output wrong: %+v", resp.Output)
	}
	if applyCalls != 1 {
		t.Errorf("ApplyDiff calls = %d, want 1", applyCalls)
	}
}

func TestReconcileApplyInvariantConflict(t *testing.T) {
	// A diff whose counts break conservation (SourceTotal=10 vs 2+0+1=3) must
	// be rejected with 409 unless force_override_invariants is set.
	state := NewCockpitState()
	state.CompleteJob("rec_bad", &DiffResult{
		SourceTotal: 10,
		Added:       []model.SpotifyTrack{{Index: 1, ID: "a"}},
		Retained:    []model.AddedTrack{{Index: 2, TargetTrackID: "r"}},
	})
	mux := testMux(t, HandlerConfig{State: state, ApplyDiff: func(context.Context, *DiffResult, bool) (*model.SyncResult, error) {
		return &model.SyncResult{}, nil
	}})

	tests := []struct {
		name string
		body string
		want int
	}{
		{"blocked by invariant conflict", `{}`, http.StatusConflict},
		{"force override passes", `{"force_override_invariants":true}`, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(t, mux, "POST", "/api/v1/reconcile/apply", tc.body)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestReconcileApplyNoDiff(t *testing.T) {
	mux := testMux(t, HandlerConfig{State: NewCockpitState(), ApplyDiff: func(context.Context, *DiffResult, bool) (*model.SyncResult, error) {
		return &model.SyncResult{}, nil
	}})
	w := doReq(t, mux, "POST", "/api/v1/reconcile/apply", `{}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (no diff)", w.Code)
	}
}
