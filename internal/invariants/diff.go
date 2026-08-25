package invariants

// AssertDiffComplete reports whether AddedIDs, RemovedIDs and RetainedIDs
// form a mutually exclusive (pairwise disjoint) and complete partition of the
// target track universe TargetIDs:
//
//	Added ∩ Removed = ∅,  Added ∩ Retained = ∅,  Removed ∩ Retained = ∅
//	Added ∪ Removed ∪ Retained == TargetIDs (set equality, no leaks)
//
// Every target ID must belong to exactly one partition, and no partition ID
// may live outside the target universe.
func AssertDiffComplete(input VerifyInput) bool {
	added := stringSet(input.AddedIDs)
	removed := stringSet(input.RemovedIDs)
	retained := stringSet(input.RetainedIDs)

	// 1. Pairwise disjointness.
	for id := range added {
		if removed[id] || retained[id] {
			return false
		}
	}
	for id := range removed {
		if retained[id] {
			return false
		}
	}

	// 2. Complete coverage: every target ID is partitioned.
	target := stringSet(input.TargetIDs)
	for id := range target {
		if !added[id] && !removed[id] && !retained[id] {
			return false
		}
	}

	// 3. No partition leakage: every partitioned ID is a target ID.
	for id := range added {
		if !target[id] {
			return false
		}
	}
	for id := range removed {
		if !target[id] {
			return false
		}
	}
	for id := range retained {
		if !target[id] {
			return false
		}
	}

	return true
}

// stringSet builds a set from ids.
func stringSet(ids []string) map[string]bool {
	s := make(map[string]bool, len(ids))
	for _, id := range ids {
		s[id] = true
	}
	return s
}
