package invariants

import "sort"

// AssertMonotonicOrder measures how far targetOrder deviates from the source
// sequence. Every targetOrder element that also exists in sourceOrder is
// mapped to its source position; each source track contributes its first
// target occurrence (later duplicates are a uniqueness concern, not an order
// one). The Longest Increasing Subsequence (LIS) of those positions is the
// longest run of tracks already in the correct relative order. The disorder
// metrics are:
//
//	ratio        = 1 - |LIS| / |sourceOrder|   (0 when sourceOrder is empty)
//	disordered   = targetOrder positions of mapped tracks NOT in the LIS
//
// Elements of targetOrder absent from sourceOrder (extra/retained tracks) do
// not perturb the result, while source tracks absent from targetOrder (e.g.
// skipped tracks) count toward the ratio per the formula above.
func AssertMonotonicOrder(sourceOrder, targetOrder []string) (ratio float64, disordered []int) {
	n := len(sourceOrder)
	if n == 0 {
		return 0, nil
	}

	sourcePos := make(map[string]int, n)
	for i, id := range sourceOrder {
		if _, seen := sourcePos[id]; !seen {
			sourcePos[id] = i
		}
	}

	var positions []int
	var targetIdx []int
	used := make(map[int]bool, n)
	for ti, id := range targetOrder {
		pi, ok := sourcePos[id]
		if !ok || used[pi] {
			continue
		}
		used[pi] = true
		positions = append(positions, pi)
		targetIdx = append(targetIdx, ti)
	}

	lisIdx := computeLISIndices(positions)
	inLIS := make(map[int]bool, len(lisIdx))
	for _, k := range lisIdx {
		inLIS[targetIdx[k]] = true
	}

	disorder := make([]int, 0, len(targetIdx)-len(lisIdx))
	for _, ti := range targetIdx {
		if !inLIS[ti] {
			disorder = append(disorder, ti)
		}
	}
	if len(disorder) == 0 {
		return 1 - float64(len(lisIdx))/float64(n), nil
	}
	return 1 - float64(len(lisIdx))/float64(n), disorder
}

// computeLISIndices returns the indices into seq that form a longest strictly
// increasing subsequence, computed in O(N log N) via patience sorting.
// It mirrors the algorithm used by internal/engine.ComputeLISIndices but is
// kept local so this package stays dependency-free (engine/web may consume
// invariants later; importing engine here would create a cycle risk).
func computeLISIndices(seq []int) []int {
	n := len(seq)
	if n == 0 {
		return nil
	}

	tails := make([]int, 0, n)
	tailIdx := make([]int, 0, n)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = -1
	}

	for i, x := range seq {
		idx := sort.Search(len(tails), func(j int) bool { return tails[j] >= x })
		if idx > 0 {
			parent[i] = tailIdx[idx-1]
		}
		if idx == len(tails) {
			tails = append(tails, x)
			tailIdx = append(tailIdx, i)
		} else {
			tails[idx] = x
			tailIdx[idx] = i
		}
	}

	lis := make([]int, len(tails))
	cur := tailIdx[len(tails)-1]
	for k := len(tails) - 1; k >= 0; k-- {
		lis[k] = cur
		cur = parent[cur]
	}
	return lis
}
