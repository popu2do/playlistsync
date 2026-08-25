package invariants_test

import (
	"playlistsync/internal/invariants"
	"slices"
	"testing"
)

func TestAssertUniqueTargetIDs(t *testing.T) {
	tests := []struct {
		name       string
		targetIDs  []string
		wantDup    bool
		wantDupIDs []string
	}{
		{"unique list", []string{"t1", "t2", "t3", "t4"}, false, nil},
		{"empty list", []string{}, false, nil},
		{"single track", []string{"t1"}, false, nil},
		{"trailing duplicate", []string{"t1", "t2", "t3", "t1"}, true, []string{"t1"}},
		{"adjacent duplicate", []string{"t1", "t2", "t2", "t3"}, true, []string{"t2"}},
		{"all duplicates", []string{"a", "a", "a"}, true, []string{"a"}},
		{"multiple duplicate kinds", []string{"a", "b", "a", "b", "a"}, true, []string{"a", "b"}},
		{"empty ids ignored", []string{"", ""}, false, nil},
		{"mixed empty and real", []string{"t1", "", "t1"}, true, []string{"t1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDup, gotIDs := invariants.AssertUniqueTargetIDs(tt.targetIDs)
			if gotDup != tt.wantDup {
				t.Fatalf("AssertUniqueTargetIDs(%v) dup = %v, want %v", tt.targetIDs, gotDup, tt.wantDup)
			}
			if !slices.Equal(gotIDs, tt.wantDupIDs) {
				t.Fatalf("AssertUniqueTargetIDs(%v) ids = %v, want %v", tt.targetIDs, gotIDs, tt.wantDupIDs)
			}
		})
	}
}
