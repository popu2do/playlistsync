package engine_test

import (
	"fmt"
	"math/rand"
	"playlistsync/internal/engine"
	"playlistsync/internal/model"
	"testing"
)

// SimulateMoves applies a series of MoveAction operations on a slice of YTMTrack
func SimulateMoves(initial []model.YTMTrack, moves []engine.MoveAction) []model.YTMTrack {
	state := make([]model.YTMTrack, len(initial))
	copy(state, initial)

	for _, move := range moves {
		fromIdx := -1
		for i, t := range state {
			if t.SetVideoID == move.SetVideoID {
				fromIdx = i
				break
			}
		}
		if fromIdx == -1 {
			continue
		}
		item := state[fromIdx]
		state = append(state[:fromIdx], state[fromIdx+1:]...)

		if move.MovedSetVideoIDPredecessor == "" {
			state = append([]model.YTMTrack{item}, state...)
		} else {
			predIdx := -1
			for i, t := range state {
				if t.SetVideoID == move.MovedSetVideoIDPredecessor {
					predIdx = i
					break
				}
			}
			if predIdx == -1 {
				state = append(state, item)
			} else {
				state = append(state[:predIdx+1], append([]model.YTMTrack{item}, state[predIdx+1:]...)...)
			}
		}
	}
	return state
}

// TestLISAlgorithm verifies the LIS index computation correctness
func TestLISAlgorithm(t *testing.T) {
	s := []int{2, 0, 4, 1, 3}
	lis := engine.ComputeLISIndices(s)
	if len(lis) != 3 {
		t.Fatalf("expected LIS length 3, got %d", len(lis))
	}
	if lis[0] != 1 || lis[1] != 3 || lis[2] != 4 {
		t.Fatalf("unexpected LIS indices: %v", lis)
	}
}

// TestReorderPlanSimulation verifies that generated move actions correctly sort the array
func TestReorderPlanSimulation(t *testing.T) {
	current := []model.YTMTrack{
		{VideoID: "vid_C", SetVideoID: "set_C", Title: "C"},
		{VideoID: "vid_A", SetVideoID: "set_A", Title: "A"},
		{VideoID: "vid_E", SetVideoID: "set_E", Title: "E"},
		{VideoID: "vid_B", SetVideoID: "set_B", Title: "B"},
		{VideoID: "vid_D", SetVideoID: "set_D", Title: "D"},
	}
	desired := []string{"vid_A", "vid_B", "vid_C", "vid_D", "vid_E"}

	moves, err := engine.ComputeReorderPlan(current, desired)
	if err != nil {
		t.Fatalf("ComputeReorderPlan failed: %v", err)
	}

	if len(moves) != 2 {
		t.Errorf("expected 2 moves, got %d: %+v", len(moves), moves)
	}

	finalState := SimulateMoves(current, moves)
	for i, tr := range finalState {
		if tr.VideoID != desired[i] {
			t.Errorf("position %d mismatch: got %s, expected %s", i, tr.VideoID, desired[i])
		}
	}
}

// TestRandomPermutationsConvergence tests 100 random permutations for 100% convergence
func TestRandomPermutationsConvergence(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	const n = 30
	const iterations = 100

	baseVids := make([]string, n)
	for i := 0; i < n; i++ {
		baseVids[i] = fmt.Sprintf("vid_%03d", i)
	}

	for it := 0; it < iterations; it++ {
		perm := r.Perm(n)
		current := make([]model.YTMTrack, n)
		for i, p := range perm {
			current[i] = model.YTMTrack{
				VideoID:    baseVids[p],
				SetVideoID: fmt.Sprintf("set_%03d", p),
				Title:      fmt.Sprintf("Track %d", p),
			}
		}

		moves, err := engine.ComputeReorderPlan(current, baseVids)
		if err != nil {
			t.Fatalf("iter %d: compute failed: %v", it, err)
		}

		finalState := SimulateMoves(current, moves)
		if len(finalState) != n {
			t.Fatalf("iter %d: length changed", it)
		}
		for i := 0; i < n; i++ {
			if finalState[i].VideoID != baseVids[i] {
				t.Fatalf("iter %d pos %d mismatch: got %s expected %s", it, i, finalState[i].VideoID, baseVids[i])
			}
		}
	}
}

// TestOrderedAddQueueSubsequence verifies that missing/skipped items don't disrupt relative order
func TestOrderedAddQueueSubsequence(t *testing.T) {
	source := make([]model.SpotifyTrack, 10)
	for i := 0; i < 10; i++ {
		source[i] = model.SpotifyTrack{
			Index: i + 1,
			Title: fmt.Sprintf("Song %d", i+1),
		}
	}

	mapping := map[int]string{
		1:  "vid_1",
		2:  "vid_2",
		4:  "vid_4",
		5:  "vid_5",
		6:  "vid_6",
		9:  "vid_9",
		10: "vid_10",
	}

	queue := engine.BuildOrderedAddQueue(source, mapping, nil)

	expected := []string{"vid_1", "vid_2", "vid_4", "vid_5", "vid_6", "vid_9", "vid_10"}
	if len(queue) != len(expected) {
		t.Fatalf("expected %d items, got %d", len(expected), len(queue))
	}
	for i := range expected {
		if queue[i] != expected[i] {
			t.Errorf("pos %d: got %s, expected %s", i, queue[i], expected[i])
		}
	}
}

// TestReviewMapRandomizationOrderFix verifies that unsorted review outcome map is properly stabilized
func TestReviewMapRandomizationOrderFix(t *testing.T) {
	source := []model.SpotifyTrack{
		{Index: 1, Title: "T1"},
		{Index: 2, Title: "T2 (Needs Review)"},
		{Index: 3, Title: "T3"},
		{Index: 4, Title: "T4 (Needs Review)"},
		{Index: 5, Title: "T5"},
	}

	mapping := map[int]string{
		1: "vid_1",
		3: "vid_3",
		5: "vid_5",
	}

	reviewOutcomeAccepted := map[int]string{
		4: "vid_4",
		2: "vid_2",
	}
	for idx, vid := range reviewOutcomeAccepted {
		mapping[idx] = vid
	}

	queue := engine.BuildOrderedAddQueue(source, mapping, nil)

	expected := []string{"vid_1", "vid_2", "vid_3", "vid_4", "vid_5"}
	for i := range expected {
		if queue[i] != expected[i] {
			t.Errorf("pos %d: got %s, expected %s (review ordering broken!)", i, queue[i], expected[i])
		}
	}
}

// BenchmarkReorder1000Tracks benchmarks LCS/LIS reorder computation on 1000 items
func BenchmarkReorder1000Tracks(b *testing.B) {
	const n = 1000
	r := rand.New(rand.NewSource(12345))
	baseVids := make([]string, n)
	for i := 0; i < n; i++ {
		baseVids[i] = fmt.Sprintf("vid_%04d", i)
	}

	perm := r.Perm(n)
	current := make([]model.YTMTrack, n)
	for i, p := range perm {
		current[i] = model.YTMTrack{
			VideoID:    baseVids[p],
			SetVideoID: fmt.Sprintf("set_%04d", p),
			Title:      fmt.Sprintf("Track %d", p),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.ComputeReorderPlan(current, baseVids)
	}
}
