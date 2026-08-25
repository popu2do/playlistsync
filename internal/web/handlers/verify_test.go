package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"playlistsync/internal/invariants"
	"playlistsync/internal/model"
)

// fakeResultStore returns a fixed SyncResult (or error) for a target id.
type fakeResultStore struct {
	res *model.SyncResult
	err error
}

func (f *fakeResultStore) fn() func(string) (*model.SyncResult, error) {
	return func(string) (*model.SyncResult, error) { return f.res, f.err }
}

func TestBuildVerifyInputAssembly(t *testing.T) {
	// Table-driven test of the SourceTotal/Synced/Skipped formula and the
	// id-derivation rules in buildVerifyInput.
	tests := []struct {
		name         string
		res          *model.SyncResult
		source       *model.SpotifyPlaylist
		target       *model.YTMPlaylist
		wantSource   int
		wantSynced   int
		wantSkipped  int
		wantFailed   int
		wantOrder    []string
		wantTarget   []string
		wantAdded    []string
		wantRemoved  []string
		wantRetained []string
	}{
		{
			name: "fully populated",
			res: &model.SyncResult{
				TotalSourceTracks: 5, AddedTracks: 3, SkippedTracks: 2,
				SyncOrder: true,
				AddedAfterReview: []model.AddedTrack{
					{Index: 1, Title: "A", TargetTrackID: "yt_a"},
					{Index: 2, Title: "B", TargetTrackID: "yt_b"},
					{Index: 3, Title: "C", TargetTrackID: "yt_c"},
				},
				RemovedExtraTracks: []model.RemovedTrack{
					{TargetTrackID: "yt_old", Title: "Old"},
				},
			},
			source: &model.SpotifyPlaylist{Tracks: []model.SpotifyTrack{
				{ID: "sp1"}, {ID: "sp2"}, {ID: "sp3"},
			}},
			target: &model.YTMPlaylist{Tracks: []model.YTMTrack{
				{VideoID: "yt_a"}, {VideoID: "yt_keep"}, {VideoID: "yt_old"},
			}},
			wantSource: 5, wantSynced: 3, wantSkipped: 2, wantFailed: 0,
			wantOrder:    []string{"sp1", "sp2", "sp3"},
			wantTarget:   []string{"yt_a", "yt_keep", "yt_old"},
			wantAdded:    []string{"yt_a", "yt_b", "yt_c"},
			wantRemoved:  []string{"yt_old"},
			wantRetained: []string{"yt_keep"},
		},
		{
			name: "empty source and target",
			res: &model.SyncResult{
				TotalSourceTracks: 0, AddedTracks: 0, SkippedTracks: 0,
				SyncOrder: false,
			},
			wantSource: 0, wantSynced: 0, wantSkipped: 0, wantFailed: 0,
			wantOrder: nil, wantTarget: nil, wantAdded: nil, wantRemoved: nil, wantRetained: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := buildVerifyInput(tc.res, tc.source, tc.target)
			if in.SourceTotal != tc.wantSource || in.SyncedCount != tc.wantSynced ||
				in.SkippedCount != tc.wantSkipped || in.FailedCount != tc.wantFailed {
				t.Errorf("counts = %d/%d/%d/%d, want %d/%d/%d/%d",
					in.SourceTotal, in.SyncedCount, in.SkippedCount, in.FailedCount,
					tc.wantSource, tc.wantSynced, tc.wantSkipped, tc.wantFailed)
			}
			if !reflect.DeepEqual(in.SourceOrder, tc.wantOrder) {
				t.Errorf("SourceOrder = %v, want %v", in.SourceOrder, tc.wantOrder)
			}
			if !reflect.DeepEqual(in.TargetIDs, tc.wantTarget) {
				t.Errorf("TargetIDs = %v, want %v", in.TargetIDs, tc.wantTarget)
			}
			if !reflect.DeepEqual(in.AddedIDs, tc.wantAdded) {
				t.Errorf("AddedIDs = %v, want %v", in.AddedIDs, tc.wantAdded)
			}
			if !reflect.DeepEqual(in.RemovedIDs, tc.wantRemoved) {
				t.Errorf("RemovedIDs = %v, want %v", in.RemovedIDs, tc.wantRemoved)
			}
			if !reflect.DeepEqual(in.RetainedIDs, tc.wantRetained) {
				t.Errorf("RetainedIDs = %v, want %v", in.RetainedIDs, tc.wantRetained)
			}
			if in.SyncOrder != tc.res.SyncOrder {
				t.Errorf("SyncOrder = %v, want %v", in.SyncOrder, tc.res.SyncOrder)
			}
		})
	}
}

func TestVerifyInvariantsEndpointRatifiesFlatSnapshot(t *testing.T) {
	rec := NewRecordingBroadcaster(nil)
	state := NewCockpitState()
	cfg := HandlerConfig{
		Broadcaster: rec,
		Ring:        rec.Ring(),
		State:       state,
		LoadResult: (&fakeResultStore{res: &model.SyncResult{
			TotalSourceTracks: 3, AddedTracks: 2, SkippedTracks: 1, SyncOrder: true,
			AddedAfterReview: []model.AddedTrack{
				{TargetTrackID: "yt_a"}, {TargetTrackID: "yt_b"},
			},
		}}).fn(),
		InspectSource: func(string) (*model.SpotifyPlaylist, error) {
			return &model.SpotifyPlaylist{Tracks: []model.SpotifyTrack{
				{ID: "sp1"}, {ID: "sp2"}, {ID: "sp3"},
			}}, nil
		},
		InspectTarget: func(string) (*model.YTMPlaylist, error) {
			return &model.YTMPlaylist{Tracks: []model.YTMTrack{
				{VideoID: "yt_a"}, {VideoID: "yt_b"},
			}}, nil
		},
	}
	mux := testMux(t, cfg)

	w := doReq(t, mux, "GET", "/api/v1/verify/invariants?target_id=tgt_1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	// The body must be the flat InvariantSnapshot JSON (ratified verbatim).
	var snap invariants.InvariantSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode flat snapshot: %v (body %s)", err, w.Body.String())
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	for _, key := range []string{"source_total", "synced_count", "skipped_count",
		"is_count_conserved", "has_duplicate_target_ids", "lis_disorder_ratio",
		"is_diff_complete", "is_zero_trace_clean", "all_passed", "evaluated_at"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("flat snapshot missing key %q", key)
		}
	}

	// The snapshot must also be persisted for the session radar + broadcast.
	if state.lastSnapshot == nil {
		t.Error("snapshot not stored in cockpit state")
	}
	found := false
	events, _ := rec.Ring().ReadSince(0)
	for _, ev := range events {
		if ev.Type == "INVARIANT_SNAPSHOT" {
			found = true
		}
	}
	if !found {
		t.Error("INVARIANT_SNAPSHOT not broadcast")
	}
}

func TestVerifyInvariantsValidation(t *testing.T) {
	cfg := HandlerConfig{LoadResult: (&fakeResultStore{res: &model.SyncResult{}}).fn()}
	mux := testMux(t, cfg)

	tests := []struct {
		name   string
		target string
		want   int
	}{
		{"missing target_id", "/api/v1/verify/invariants", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(t, mux, "GET", tc.target, "")
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestVerifyInvariantsUnconfigured(t *testing.T) {
	mux := testMux(t, HandlerConfig{})
	w := doReq(t, mux, "GET", "/api/v1/verify/invariants", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (missing target_id before unconfigured seams)", w.Code)
	}
}

func TestVerifyInvariantsResultNotFound(t *testing.T) {
	cfg := HandlerConfig{LoadResult: (&fakeResultStore{err: errors.New("no such file")}).fn()}
	mux := testMux(t, cfg)
	w := doReq(t, mux, "GET", "/api/v1/verify/invariants?target_id=ghost", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", w.Code, w.Body.String())
	}
}

func TestVerifyInvariantsConservationFailure(t *testing.T) {
	// AddedTracks(2)+SkippedTracks(1) != TotalSourceTracks(10) => count
	// disabled; all_passed must be false.
	cfg := HandlerConfig{
		LoadResult: (&fakeResultStore{res: &model.SyncResult{
			TotalSourceTracks: 10, AddedTracks: 2, SkippedTracks: 1, SyncOrder: true,
			AddedAfterReview: []model.AddedTrack{{TargetTrackID: "yt_a"}},
		}}).fn(),
	}
	mux := testMux(t, cfg)
	w := doReq(t, mux, "GET", "/api/v1/verify/invariants?target_id=t", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", w.Code, w.Body.String())
	}
	var snap invariants.InvariantSnapshot
	if err := decodeJSON(t, w, &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.IsCountConserved {
		t.Error("count conservation should fail (2+1 != 10)")
	}
	if snap.AllPassed {
		t.Error("all_passed should be false")
	}
}

// TestVerifyInvariantsPendingPlaceholdersDoNotBreakDiffCompleteness is the
// MAJOR-3 regression: the cockpit apply seam persists an auditable artifact
// whose pending adds carry no real target id (older artifacts used `pending_N`
// placeholders). Neither may leak into the invariant partition — if a
// placeholder slipped into AddedIDs, AssertDiffComplete's no-leakage rule
// would false-fail on EVERY cockpit-applied result. The verify preflight must
// use the real target universe.
func TestVerifyInvariantsPendingPlaceholdersDoNotBreakDiffCompleteness(t *testing.T) {
	res := &model.SyncResult{
		TotalSourceTracks: 5, AddedTracks: 4, SkippedTracks: 1, SyncOrder: true,
		// Legacy apply artifact shape: added tracks carry pending_* ids (or
		// empty), retained tracks carry real ids. The real target holds the
		// retained + removed ids only.
		AddedAfterReview: []model.AddedTrack{
			{Index: 1, Title: "A", TargetTrackID: "pending_0"},
			{Index: 2, Title: "B", TargetTrackID: "pending_1"},
			{Index: 3, Title: "C", TargetTrackID: ""},
			{Index: 4, Title: "R", TargetTrackID: "yt_keep_1"},
			{Index: 5, Title: "R2", TargetTrackID: "yt_keep_2"},
		},
		RemovedExtraTracks: []model.RemovedTrack{{TargetTrackID: "yt_old"}},
	}
	cfg := HandlerConfig{
		LoadResult: (&fakeResultStore{res: res}).fn(),
		InspectTarget: func(id string) (*model.YTMPlaylist, error) {
			return &model.YTMPlaylist{Tracks: []model.YTMTrack{
				{VideoID: "yt_keep_1"}, {VideoID: "yt_keep_2"}, {VideoID: "yt_old"},
			}}, nil
		},
	}
	mux := testMux(t, cfg)
	w := doReq(t, mux, "GET", "/api/v1/verify/invariants?target_id=t", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", w.Code, w.Body.String())
	}
	var snap invariants.InvariantSnapshot
	if err := decodeJSON(t, w, &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !snap.IsDiffComplete {
		t.Errorf("diff completeness false-failed: placeholders polluted the partition (%+v)", snap)
	}
	if snap.HasDuplicateTargetIDs {
		t.Errorf("unexpected duplicate detection: %v", snap.DuplicateIDs)
	}
}
