package engine

import (
	"fmt"
	"playlistsync/internal/model"
	"sort"
)

// MoveAction represents a track move operation in YouTube Music playlist
type MoveAction struct {
	SetVideoID                 string `json:"setVideoId"`
	MovedSetVideoIDPredecessor string `json:"movedSetVideoIdPredecessor,omitempty"`
}

// ComputeLISIndices returns the indices in s that form the Longest Increasing Subsequence (O(N log N)).
func ComputeLISIndices(s []int) []int {
	n := len(s)
	if n == 0 {
		return nil
	}

	tails := make([]int, 0, n)
	tailIndices := make([]int, 0, n)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = -1
	}

	for i, x := range s {
		idx := sort.Search(len(tails), func(j int) bool {
			return tails[j] >= x
		})

		if idx > 0 {
			parent[i] = tailIndices[idx-1]
		}

		if idx == len(tails) {
			tails = append(tails, x)
			tailIndices = append(tailIndices, i)
		} else {
			tails[idx] = x
			tailIndices[idx] = i
		}
	}

	lisLen := len(tails)
	lisIndices := make([]int, lisLen)
	curr := tailIndices[lisLen-1]
	for k := lisLen - 1; k >= 0; k-- {
		lisIndices[k] = curr
		curr = parent[curr]
	}

	return lisIndices
}

// ComputeReorderPlan calculates the minimum MoveAction list needed to transform
// the current YTM tracks into the desired videoID order based on LIS (In-place Reordering).
func ComputeReorderPlan(current []model.YTMTrack, desiredVids []string) ([]MoveAction, error) {
	if len(current) != len(desiredVids) {
		return nil, fmt.Errorf("length mismatch: current=%d, desired=%d", len(current), len(desiredVids))
	}
	n := len(current)
	if n <= 1 {
		return nil, nil
	}

	targetPositions := make(map[string][]int)
	for i, vid := range desiredVids {
		targetPositions[vid] = append(targetPositions[vid], i)
	}

	s := make([]int, n)
	posCounters := make(map[string]int)
	for i, tr := range current {
		poses, ok := targetPositions[tr.VideoID]
		if !ok || posCounters[tr.VideoID] >= len(poses) {
			return nil, fmt.Errorf("track videoId %s not found in desired list", tr.VideoID)
		}
		s[i] = poses[posCounters[tr.VideoID]]
		posCounters[tr.VideoID]++
	}

	lisIndices := ComputeLISIndices(s)
	isAnchorTargetIndex := make(map[int]bool)
	for _, idx := range lisIndices {
		targetPos := s[idx]
		isAnchorTargetIndex[targetPos] = true
	}

	setVideoIDByTargetPos := make(map[int]string)
	for i, tr := range current {
		targetPos := s[i]
		setVideoIDByTargetPos[targetPos] = tr.SetVideoID
	}

	var actions []MoveAction
	for targetPos := 0; targetPos < n; targetPos++ {
		if isAnchorTargetIndex[targetPos] {
			continue
		}

		currentSetID := setVideoIDByTargetPos[targetPos]
		var predSetID string
		if targetPos > 0 {
			predSetID = setVideoIDByTargetPos[targetPos-1]
		}

		actions = append(actions, MoveAction{
			SetVideoID:                 currentSetID,
			MovedSetVideoIDPredecessor: predSetID,
		})
	}

	return actions, nil
}

// BuildOrderedAddQueue produces a list of destination track IDs ordered strictly by source playlist Index.
func BuildOrderedAddQueue(sourceTracks []model.SpotifyTrack, mapping map[int]string, existingTargetIDs map[string]bool) []string {
	sortedTracks := append([]model.SpotifyTrack(nil), sourceTracks...)
	sort.Slice(sortedTracks, func(i, j int) bool {
		return sortedTracks[i].Index < sortedTracks[j].Index
	})

	seen := make(map[string]bool)
	for k, v := range existingTargetIDs {
		if v {
			seen[k] = true
		}
	}

	var toAdd []string
	for _, st := range sortedTracks {
		targetID, ok := mapping[st.Index]
		if !ok || targetID == "" {
			continue
		}
		if !seen[targetID] {
			toAdd = append(toAdd, targetID)
			seen[targetID] = true
		}
	}
	return toAdd
}

// CalculateOrderConcordanceRate computes the Kendall Tau concordance rate [0.0, 1.0] of actual order vs desired order.
func CalculateOrderConcordanceRate(desiredIDs, actualIDs []string) float64 {
	if len(desiredIDs) <= 1 || len(actualIDs) <= 1 {
		return 1.0
	}

	posMap := make(map[string]int)
	for i, id := range actualIDs {
		if _, exists := posMap[id]; !exists {
			posMap[id] = i
		}
	}

	var positions []int
	for _, id := range desiredIDs {
		if pos, ok := posMap[id]; ok {
			positions = append(positions, pos)
		}
	}

	m := len(positions)
	if m <= 1 {
		return 1.0
	}

	concordant := 0
	totalPairs := 0
	for i := 0; i < m-1; i++ {
		for j := i + 1; j < m; j++ {
			totalPairs++
			if positions[i] < positions[j] {
				concordant++
			}
		}
	}

	if totalPairs == 0 {
		return 1.0
	}
	return float64(concordant) / float64(totalPairs)
}
