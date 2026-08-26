package invariants_test

import (
	"encoding/json"
	"math"
	"playlistsync/internal/invariants"
	"reflect"
	"testing"
	"time"
)

func TestVerifyAggregation(t *testing.T) {
	tests := []struct {
		name    string
		input   invariants.VerifyInput
		traceOK bool
		want    invariants.InvariantSnapshot
	}{
		{
			name: "all pass",
			input: invariants.VerifyInput{
				SourceTotal: 100, SyncedCount: 80, SkippedCount: 15, FailedCount: 5,
				SourceOrder: []string{"A", "B", "C"},
				TargetIDs:   []string{"A", "B", "C"},
				AddedIDs:    []string{"A", "B", "C"},
				SyncOrder:   true,
			},
			traceOK: true,
			want: invariants.InvariantSnapshot{
				SourceTotal:       100,
				SyncedCount:       80,
				SkippedCount:      15,
				FailedCount:       5,
				IsCountConserved:  true,
				IsDiffComplete:    true,
				IsZeroTraceClean:  true,
				AllPassed:         true,
				DuplicateIDs:      []string{},
				DisorderedIndices: []int{},
			},
		},
		{
			name: "count violation short-circuits",
			input: invariants.VerifyInput{
				SourceTotal: 100, SyncedCount: 79, SkippedCount: 15, FailedCount: 5,
			},
			traceOK: true,
			want: invariants.InvariantSnapshot{
				SourceTotal:       100, SyncedCount: 79, SkippedCount: 15, FailedCount: 5,
				DuplicateIDs:      []string{},
				DisorderedIndices: []int{},
			},
		},
		{
			name: "duplicate target id short-circuits",
			input: invariants.VerifyInput{
				SourceTotal: 2, SyncedCount: 2,
				TargetIDs: []string{"t1", "t1"},
			},
			traceOK: true,
			want: invariants.InvariantSnapshot{
				SourceTotal:           2,
				SyncedCount:           2,
				IsCountConserved:      true,
				HasDuplicateTargetIDs: true,
				DuplicateIDs:          []string{"t1"},
				DisorderedIndices:     []int{},
			},
		},
		{
			name: "order violation short-circuits",
			input: invariants.VerifyInput{
				SourceTotal: 2, SyncedCount: 2,
				SourceOrder: []string{"A", "B"},
				TargetIDs:   []string{"B", "A"},
				SyncOrder:   true,
			},
			traceOK: true,
			want: invariants.InvariantSnapshot{
				SourceTotal:       2,
				SyncedCount:       2,
				IsCountConserved:  true,
				LISDisorderRatio:  0.5,
				DisorderedIndices: []int{0},
				DuplicateIDs:      []string{},
			},
		},
		{
			name: "order passes despite skipped source track",
			input: invariants.VerifyInput{
				SourceTotal: 5, SyncedCount: 4, SkippedCount: 1,
				SourceOrder: []string{"A", "B", "C", "D", "E"},
				TargetIDs:   []string{"B", "C", "D", "E"},
				AddedIDs:    []string{"B", "C", "D", "E"},
				SyncOrder:   true,
			},
			traceOK: true,
			want: invariants.InvariantSnapshot{
				SourceTotal:       5,
				SyncedCount:       4,
				SkippedCount:      1,
				IsCountConserved:  true,
				LISDisorderRatio:  0.2,
				IsDiffComplete:    true,
				IsZeroTraceClean:  true,
				AllPassed:         true,
				DuplicateIDs:      []string{},
				DisorderedIndices: []int{},
			},
		},
		{
			name: "order skipped when sync order off",
			input: invariants.VerifyInput{
				SourceTotal: 2, SyncedCount: 2,
				SourceOrder: []string{"A", "B"},
				TargetIDs:   []string{"B", "A"},
				AddedIDs:    []string{"B", "A"},
				SyncOrder:   false,
			},
			traceOK: true,
			want: invariants.InvariantSnapshot{
				SourceTotal:       2,
				SyncedCount:       2,
				IsCountConserved:  true,
				IsDiffComplete:    true,
				IsZeroTraceClean:  true,
				AllPassed:         true,
				DuplicateIDs:      []string{},
				DisorderedIndices: []int{},
			},
		},
		{
			name: "diff violation short-circuits",
			input: invariants.VerifyInput{
				SourceTotal: 1, SyncedCount: 1,
				SourceOrder: []string{"t1"},
				TargetIDs:   []string{"t1"},
				AddedIDs:    []string{"t1"},
				RemovedIDs:  []string{"t1"},
			},
			traceOK: true,
			want: invariants.InvariantSnapshot{
				SourceTotal:       1,
				SyncedCount:       1,
				IsCountConserved:  true,
				DuplicateIDs:      []string{},
				DisorderedIndices: []int{},
			},
		},
		{
			name: "zero trace violation",
			input: invariants.VerifyInput{
				SourceTotal: 0,
			},
			traceOK: false,
			want: invariants.InvariantSnapshot{
				IsCountConserved:  true,
				IsDiffComplete:    true,
				IsZeroTraceClean:  false,
				DuplicateIDs:      []string{},
				DisorderedIndices: []int{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := invariants.NewVerifier(invariants.WithZeroTraceProbe(func() bool { return tt.traceOK }))
			got := v.Verify(tt.input)
			got.EvaluatedAt = time.Time{} // normalize timestamp for comparison
			assertSnapshotEqual(t, got, tt.want)
		})
	}
}

// assertSnapshotEqual compares snapshots with float tolerance for
// LISDisorderRatio (1 - |LIS|/n is not exactly representable in float64 and
// the compiled literal may differ in the last ulp) and exact comparison for
// every other field.
func assertSnapshotEqual(t *testing.T, got, want invariants.InvariantSnapshot) {
	t.Helper()
	if math.Abs(got.LISDisorderRatio-want.LISDisorderRatio) > 1e-9 {
		t.Errorf("LISDisorderRatio = %v, want %v", got.LISDisorderRatio, want.LISDisorderRatio)
	}
	got.LISDisorderRatio = 0
	want.LISDisorderRatio = 0
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Verify snapshot mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestInvariantSnapshotJSON(t *testing.T) {
	snap := invariants.InvariantSnapshot{
		SourceTotal:       10,
		SyncedCount:       10,
		IsCountConserved:  true,
		LISDisorderRatio:  0.25,
		DisorderedIndices: []int{2, 5},
		EvaluatedAt:       time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
		AllPassed:         true,
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	for _, key := range []string{
		"source_total", "synced_count", "skipped_count", "failed_count",
		"is_count_conserved", "has_duplicate_target_ids", "duplicate_target_ids",
		"lis_disorder_ratio", "disordered_indices", "is_diff_complete",
		"is_zero_trace_clean", "evaluated_at", "all_passed",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("snapshot JSON missing key %q: %s", key, data)
		}
	}
}

// TestVerifySnapshotJSONArraysAlwaysEmitBrackets is the plan-wc-04 regression
// for TC-E2E-04: the web contract (contracts.ts) types duplicate_target_ids /
// disordered_indices as ARRAYS. Verify must emit `[]` (never `null`) for the
// clean case and for every short-circuit path, so the frontend radar reading
// `.length` cannot crash.
func TestVerifySnapshotJSONArraysAlwaysEmitBrackets(t *testing.T) {
	tests := []struct {
		name  string
		input invariants.VerifyInput
	}{
		{
			name: "clean pass with sync order",
			input: invariants.VerifyInput{
				SourceTotal: 4, SyncedCount: 3, SkippedCount: 1,
				TargetIDs: []string{}, SyncOrder: true,
			},
		},
		{
			name: "clean pass without sync order",
			input: invariants.VerifyInput{
				SourceTotal: 4, SyncedCount: 3, SkippedCount: 1,
				TargetIDs: []string{}, SyncOrder: false,
			},
		},
		{
			name: "empty source order with sync order on",
			input: invariants.VerifyInput{
				SourceTotal: 4, SyncedCount: 4,
				TargetIDs: []string{}, SyncOrder: true,
			},
		},
		{
			name: "count violation short-circuits",
			input: invariants.VerifyInput{
				SourceTotal: 4, SyncedCount: 3, SkippedCount: 2,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := invariants.NewVerifier(invariants.WithZeroTraceProbe(func() bool { return true }))
			snap := v.Verify(tt.input)
			data, err := json.Marshal(snap)
			if err != nil {
				t.Fatalf("marshal snapshot: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal snapshot: %v", err)
			}
			for _, key := range []string{"duplicate_target_ids", "disordered_indices"} {
				raw, ok := got[key]
				if !ok {
					t.Fatalf("snapshot JSON missing key %q: %s", key, data)
				}
				if raw == nil {
					t.Errorf("key %q marshalled to null — contract requires [] (TC-E2E-04): %s", key, data)
				}
				if _, isSlice := raw.([]any); !isSlice {
					t.Errorf("key %q is %T, want an array: %s", key, raw, data)
				}
			}
		})
	}
}
