package invariants

// AssertUniqueTargetIDs reports whether targetIDs contains any duplicated
// non-empty ID, and returns the de-duplicated list of offending IDs in
// first-seen order. Empty IDs are ignored: they represent placeholder or
// skipped entries, not real target tracks (mirrors the spec uniqueness
// semantics).
func AssertUniqueTargetIDs(targetIDs []string) (hasDuplicates bool, duplicateIDs []string) {
	seen := make(map[string]bool, len(targetIDs))
	dupSet := make(map[string]bool)
	// plan-wc-04 (TC-E2E-04): never return a nil slice — the web JSON
	// contract types duplicate_target_ids as an ARRAY and nil marshals to
	// `null`, crashing frontend consumers. Empty-but-non-nil keeps `[]`.
	duplicates := make([]string, 0)
	for _, id := range targetIDs {
		if id == "" {
			continue
		}
		if seen[id] && !dupSet[id] {
			dupSet[id] = true
			duplicates = append(duplicates, id)
		}
		seen[id] = true
	}
	return len(duplicates) > 0, duplicates
}
