package invariants_test

import (
	"playlistsync/internal/invariants"
	"testing"
)

func TestAssertDiffComplete(t *testing.T) {
	tests := []struct {
		name  string
		input invariants.VerifyInput
		want  bool
	}{
		{"complete partition",
			invariants.VerifyInput{
				TargetIDs:   []string{"a", "b", "c", "d", "e"},
				AddedIDs:    []string{"a", "b"},
				RemovedIDs:  []string{"c"},
				RetainedIDs: []string{"d", "e"},
			}, true},
		{"all added",
			invariants.VerifyInput{TargetIDs: []string{"a", "b", "c"}, AddedIDs: []string{"a", "b", "c"}}, true},
		{"all removed",
			invariants.VerifyInput{TargetIDs: []string{"a", "b"}, RemovedIDs: []string{"a", "b"}}, true},
		{"all retained",
			invariants.VerifyInput{TargetIDs: []string{"a", "b"}, RetainedIDs: []string{"a", "b"}}, true},
		{"empty universe", invariants.VerifyInput{}, true},
		{"added removed overlap",
			invariants.VerifyInput{TargetIDs: []string{"t1"}, AddedIDs: []string{"t1"}, RemovedIDs: []string{"t1"}}, false},
		{"added retained overlap",
			invariants.VerifyInput{TargetIDs: []string{"t1"}, AddedIDs: []string{"t1"}, RetainedIDs: []string{"t1"}}, false},
		{"removed retained overlap",
			invariants.VerifyInput{TargetIDs: []string{"t1"}, RemovedIDs: []string{"t1"}, RetainedIDs: []string{"t1"}}, false},
		{"uncovered target id",
			invariants.VerifyInput{TargetIDs: []string{"a", "b", "c"}, AddedIDs: []string{"a", "b"}}, false},
		{"partition leaks outside target",
			invariants.VerifyInput{TargetIDs: []string{"a"}, AddedIDs: []string{"a"}, RetainedIDs: []string{"z"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := invariants.AssertDiffComplete(tt.input); got != tt.want {
				t.Fatalf("AssertDiffComplete(%+v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
