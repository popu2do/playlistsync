package invariants_test

import (
	"math"
	"playlistsync/internal/invariants"
	"slices"
	"testing"
)

func TestAssertMonotonicOrder(t *testing.T) {
	tests := []struct {
		name         string
		sourceOrder  []string
		targetOrder  []string
		wantRatio    float64
		wantDisorder []int
	}{
		{"fully ordered", []string{"A", "B", "C", "D"}, []string{"A", "B", "C", "D"}, 0, []int{}},
		{"single track", []string{"A"}, []string{"A"}, 0, []int{}},
		{"fully reversed", []string{"A", "B", "C", "D"}, []string{"D", "C", "B", "A"}, 0.75, []int{0, 1, 2}},
		{"partial deviation", []string{"A", "B", "C", "D", "E"}, []string{"A", "C", "B", "D", "E"}, 0.2, []int{1}},
		{"empty source", nil, []string{"A", "B"}, 0, nil},
		{"empty target", []string{"A", "B", "C"}, nil, 1, []int{}},
		{"missing source track in target", []string{"A", "B", "C", "D", "E"}, []string{"B", "C", "D", "E"}, 0.2, []int{}},
		{"extra tracks ignored", []string{"A", "B", "C"}, []string{"X", "A", "B", "Y", "C"}, 0, []int{}},
		{"duplicate in target only counted once", []string{"A", "B"}, []string{"A", "B", "B"}, 0, []int{}},
	}
	const eps = 1e-9
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRatio, gotDisorder := invariants.AssertMonotonicOrder(tt.sourceOrder, tt.targetOrder)
			if math.Abs(gotRatio-tt.wantRatio) > eps {
				t.Fatalf("AssertMonotonicOrder(%v, %v) ratio = %v, want %v", tt.sourceOrder, tt.targetOrder, gotRatio, tt.wantRatio)
			}
			if !slices.Equal(gotDisorder, tt.wantDisorder) {
				t.Fatalf("AssertMonotonicOrder(%v, %v) disordered = %v, want %v", tt.sourceOrder, tt.targetOrder, gotDisorder, tt.wantDisorder)
			}
		})
	}
}
