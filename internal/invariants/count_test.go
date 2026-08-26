package invariants_test

import (
	"playlistsync/internal/invariants"
	"testing"
)

func TestAssertCountConserved(t *testing.T) {
	tests := []struct {
		name  string
		input invariants.VerifyInput
		want  bool
	}{
		{"exact conservation", invariants.VerifyInput{SourceTotal: 100, SyncedCount: 80, SkippedCount: 15, FailedCount: 5}, true},
		{"all synced", invariants.VerifyInput{SourceTotal: 50, SyncedCount: 50}, true},
		{"all skipped", invariants.VerifyInput{SourceTotal: 20, SkippedCount: 20}, true},
		{"all failed", invariants.VerifyInput{SourceTotal: 10, FailedCount: 10}, true},
		{"all zero", invariants.VerifyInput{}, true},
		{"missing one", invariants.VerifyInput{SourceTotal: 100, SyncedCount: 79, SkippedCount: 15, FailedCount: 5}, false},
		{"one extra", invariants.VerifyInput{SourceTotal: 100, SyncedCount: 80, SkippedCount: 16, FailedCount: 5}, false},
		{"negative skipped", invariants.VerifyInput{SourceTotal: 100, SyncedCount: 105, SkippedCount: -5}, false},
		{"negative synced", invariants.VerifyInput{SourceTotal: 3, SyncedCount: -1, SkippedCount: 4}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := invariants.AssertCountConserved(tt.input); got != tt.want {
				t.Fatalf("AssertCountConserved(%+v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
