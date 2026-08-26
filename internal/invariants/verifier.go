// Package invariants implements the five system invariant assertions that
// guard every playlist synchronization mutation:
//
//  1. Count conservation:   SourceTotal = Synced + Skipped + Failed
//  2. Target uniqueness:    no duplicated target track IDs
//  3. Order monotonicity:   source track sequence preserved monotonically
//     unless shuffle (sync order off) is requested
//  4. Diff completeness:    Added / Removed / Retained partition the mutation
//     lifecycle
//  5. Zero-trace:           no unmanaged filesystem residue (pid files, temp
//     leftovers), no accidental persistence of plaintext credentials
//
// All assertions are pure and read-only (zero side effects), so they can run
// safely as a pre-flight gate before any apply/reconcile step. The aggregate
// verifier (InvariantVerifier) runs them in the fixed order above and
// short-circuits on the first violation so the earliest breach is never
// masked by later results. After the first failing invariant, later
// invariants are not evaluated and their flags remain at zero values
// (false/0); consumers such as the web radar must rely on AllPassed plus the
// first failing flag and must not treat zero-valued flags as violations.
package invariants

import "time"

// InvariantSnapshot is the JSON-serializable verification result. It is
// consumed directly by the web cockpit radar view.
type InvariantSnapshot struct {
	SourceTotal           int       `json:"source_total"`
	SyncedCount           int       `json:"synced_count"`
	SkippedCount          int       `json:"skipped_count"`
	FailedCount           int       `json:"failed_count"`
	IsCountConserved      bool      `json:"is_count_conserved"`
	HasDuplicateTargetIDs bool      `json:"has_duplicate_target_ids"`
	DuplicateIDs          []string  `json:"duplicate_target_ids"`
	LISDisorderRatio      float64   `json:"lis_disorder_ratio"`
	DisorderedIndices     []int     `json:"disordered_indices"`
	IsDiffComplete        bool      `json:"is_diff_complete"`
	IsZeroTraceClean      bool      `json:"is_zero_trace_clean"`
	EvaluatedAt           time.Time `json:"evaluated_at"`
	AllPassed             bool      `json:"all_passed"`
}

// VerifyInput carries everything the invariant assertions need. It is
// constructed from a model.SyncResult plus supplementary data that SyncResult
// does not carry (the full target track ID set and the source track ID
// sequence), because SyncResult only records counts and per-track summaries.
type VerifyInput struct {
	SourceTotal  int
	SyncedCount  int
	SkippedCount int
	FailedCount  int
	SourceOrder  []string // source track IDs in source order (order-sensitive)
	TargetIDs    []string // every track ID currently in the target playlist
	AddedIDs     []string // target IDs added by this mutation
	RemovedIDs   []string // target IDs removed by this mutation
	RetainedIDs  []string // target IDs retained by this mutation
	SyncOrder    bool
}

// InvariantVerifier aggregates the five atomic assertions into a single
// verification snapshot. Handlers depend on this interface rather than on a
// concrete implementation.
type InvariantVerifier interface {
	// Verify runs the five assertions in fixed order (Count, Uniqueness,
	// Order, Diff, Zero-Trace) and short-circuits on the first failure. Order
	// is only evaluated when input.SyncOrder is true; otherwise the ratio is
	// zero and order never fails. Order fails only when actual disorder is
	// present (non-empty DisorderedIndices); LISDisorderRatio additionally
	// reflects source tracks absent from the target and is a reporting metric.
	Verify(input VerifyInput) InvariantSnapshot
}

// VerifierOption configures a Verifier.
type VerifierOption func(*Verifier)

// WithZeroTraceProbe overrides the zero-trace probe used during verification.
// It exists for deterministic tests and for callers that need to probe
// custom residue paths instead of the defaults.
func WithZeroTraceProbe(probe func() bool) VerifierOption {
	return func(v *Verifier) { v.zeroTraceProbe = probe }
}

// Verifier is the default InvariantVerifier implementation.
type Verifier struct {
	zeroTraceProbe func() bool
}

// NewVerifier returns a Verifier ready for use. The default zero-trace probe
// scans the configured output directory for pid files and the residue
// directory for leftover temp entries.
func NewVerifier(opts ...VerifierOption) *Verifier {
	v := &Verifier{zeroTraceProbe: AssertZeroTraceClean}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// Verify implements InvariantVerifier with fail-fast short-circuit semantics:
// Count -> Uniqueness -> Order -> Diff -> Zero-Trace. Fields for invariants
// after the first failure are left at their zero values.
func (v *Verifier) Verify(input VerifyInput) InvariantSnapshot {
	snap := InvariantSnapshot{
		SourceTotal:  input.SourceTotal,
		SyncedCount:  input.SyncedCount,
		SkippedCount: input.SkippedCount,
		FailedCount:  input.FailedCount,
		EvaluatedAt:  time.Now().UTC(),
		// plan-wc-04 (TC-E2E-04): the JSON contract (web contracts.ts) types
		// duplicate_target_ids / disordered_indices as ARRAYS. Go marshals
		// nil slices to `null`, which crashes frontend consumers reading
		// `.length` (InvariantMonitor radar crash found by the E2E suite).
		// Seed non-nil so early-return paths always emit `[]`.
		DuplicateIDs:      []string{},
		DisorderedIndices: []int{},
	}

	// 1. Count conservation.
	snap.IsCountConserved = AssertCountConserved(input)
	if !snap.IsCountConserved {
		return snap
	}

	// 2. Target uniqueness.
	snap.HasDuplicateTargetIDs, snap.DuplicateIDs = AssertUniqueTargetIDs(input.TargetIDs)
	if snap.HasDuplicateTargetIDs {
		return snap
	}

	// 3. Order monotonicity (only when the caller requested order sync).
	//    The gate keys on actual disorder, not the ratio: LISDisorderRatio
	//    also counts source tracks absent from the target (skips) and is a
	//    reporting metric only.
	if input.SyncOrder {
		snap.LISDisorderRatio, snap.DisorderedIndices = AssertMonotonicOrder(input.SourceOrder, input.TargetIDs)
		if len(snap.DisorderedIndices) > 0 {
			return snap
		}
	}

	// JSON-contract normalization (TC-E2E-04): the assertion helpers return
	// nil slices for the clean case (empty source order, no duplicates), which
	// marshal to `null` and break the documented array contract. Normalize at
	// this single choke point so the emitted snapshot always carries `[]`.
	if snap.DisorderedIndices == nil {
		snap.DisorderedIndices = []int{}
	}
	if snap.DuplicateIDs == nil {
		snap.DuplicateIDs = []string{}
	}

	// 4. Diff completeness.
	snap.IsDiffComplete = AssertDiffComplete(input)
	if !snap.IsDiffComplete {
		return snap
	}

	// 5. Zero-trace residue.
	probe := v.zeroTraceProbe
	if probe == nil {
		probe = AssertZeroTraceClean
	}
	snap.IsZeroTraceClean = probe()
	if !snap.IsZeroTraceClean {
		return snap
	}

	snap.AllPassed = true
	return snap
}

// Compile-time assertion: *Verifier satisfies InvariantVerifier.
var _ InvariantVerifier = (*Verifier)(nil)
