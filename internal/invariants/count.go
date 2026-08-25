package invariants

// AssertCountConserved reports whether the source track total is conserved:
// SourceTotal == SyncedCount + SkippedCount + FailedCount.
// Any negative component count also fails, since a negative count is not a
// meaningful conservation partition (mirrors the spec count test matrix).
func AssertCountConserved(input VerifyInput) bool {
	if input.SyncedCount < 0 || input.SkippedCount < 0 || input.FailedCount < 0 {
		return false
	}
	return input.SourceTotal == input.SyncedCount+input.SkippedCount+input.FailedCount
}
