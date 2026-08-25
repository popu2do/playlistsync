// Package handlers implements the full REST + SSE endpoint set for the web
// cockpit (spec 02 §2 + §3, spec 07 §3). It is the thin HTTP layer between the
// loopback server (internal/web/server) and the domain packages (auth, engine,
// model, report, invariants).
//
// Every handler depends only on interfaces — bridge.EventBroadcaster and
// invariants.InvariantVerifier — plus small function seams declared in
// HandlerConfig. No concrete bridge type is imported anywhere in this package
// (plan-wc-02 Global Constraint #4; resolved per Task-1 review INFO-5: the
// handler seams are EventBroadcaster + InvariantVerifier).
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"playlistsync/internal/auth"
	"playlistsync/internal/invariants"
	"playlistsync/internal/model"
	"playlistsync/internal/web/bridge"
)

// writeJSON marshals v as the response body with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// writeErrorJSON writes a JSON error envelope: {"error": "message"}.
func writeErrorJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": sanitize(msg)})
}

// sanitize redacts sensitive credential material from error messages before
// they leave the process (zero-trace: no secrets in HTTP responses/logs).
func sanitize(s string) string { return auth.SanitizeSensitive(s) }

// readJSON decodes a JSON request body into v, rejecting unknown keys so a
// typo cannot silently produce zero-value parameters.
func readJSON(r *http.Request, v interface{}) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// HandlerConfig carries every seam the handlers need. cmd/playlistsync wires
// the real implementations; tests inject fakes. All fields are optional except
// where noted; nil/zero seams degrade to sensible HTTP errors.
type HandlerConfig struct {
	// Broadcaster is the event bus every handler broadcasts through. cmd/web
	// supplies a *RecordingBroadcaster (ring + fan-out); tests may use any
	// EventBroadcaster. Required for handlers that emit events (session
	// shutdown, auth cdp, reconcile).
	Broadcaster bridge.EventBroadcaster

	// Ring is the SSE replay store. Required by the events handler.
	Ring *bridge.SSEEventRingBuffer

	// RootCtx is the server root-cancellation channel (WebServer.RootDone).
	// The SSE handler selects on it so connections release promptly during
	// graceful shutdown. Nil means "never cancel" (safe for tests).
	RootCtx <-chan struct{}

	// HeartbeatInterval is the SSE keep-alive interval; <=0 defaults to 30s
	// (spec 02 §3.4). Tests shrink this to milliseconds.
	HeartbeatInterval time.Duration

	// Session metadata (GET /session).
	SessionID   string
	StartTime   time.Time
	Port        func() int // actual bound port; nil -> 0
	ClientCount func() int // connected SSE clients; nil -> 0

	// Kick refreshes the idle watchdog (heartbeat handler). The middleware
	// also kicks on every authenticated request; this is for direct use.
	Kick func()

	// Shutdown triggers graceful server shutdown (POST /session/shutdown).
	Shutdown func(ctx context.Context) error

	// Auth seams (auth.go) ---------------------------------------------------
	CheckAuth       func(platform auth.Platform, authPath, proxy string) (*auth.AuthStatus, error)
	CDPLogin        func(ctx context.Context, targetURL, savePath, targetCookieName, proxyURL string) error
	SpotifyAuthPath string
	YTMAuthPath     string
	ProxyURL        string
	PKCE            PKCEExchanger
	TokenStore      TokenStore

	// Reconcile seams (reconcile.go) -----------------------------------------
	RunReconcile       func(ctx context.Context, params ReconcileParams, emit func(eventType string, data interface{})) (*DiffResult, error)
	ResolveArbitration func(decision *bridge.ArbitrationDecision) bool
	ApplyDiff          func(ctx context.Context, diff *DiffResult, force bool) (*model.SyncResult, error)

	// Inspect seams (inspect.go) --------------------------------------------
	InspectSource func(id string) (*model.SpotifyPlaylist, error)
	InspectTarget func(id string) (*model.YTMPlaylist, error)

	// Verify seams (verify.go) ----------------------------------------------
	LoadResult func(id string) (*model.SyncResult, error)
	Verifier   invariants.InvariantVerifier

	// Reports seam (reports.go) ---------------------------------------------
	ReportsDir string

	// State is the shared cockpit state (last reconcile diff + last invariant
	// snapshot). cmd/web creates one with NewCockpitState().
	State *CockpitState
}

// CockpitState is the small shared session store: the latest reconcile diff
// (for /reconcile/diff, /inspect/playlist match states and the apply
// preflight) and the latest invariant snapshot (for /session's
// invariants_summary). Guarded by a single mutex; handlers never block other
// handlers for longer than a lock acquisition.
type CockpitState struct {
	mu           sync.RWMutex
	jobID        string
	status       string // "idle" | "running" | "complete" | "failed"
	diff         *DiffResult
	lastError    string
	lastSnapshot *invariants.InvariantSnapshot
	heartbeatAt  time.Time
}

// NewCockpitState returns an idle cockpit state.
func NewCockpitState() *CockpitState {
	return &CockpitState{status: "idle"}
}

// Heartbeat records the latest frontend heartbeat timestamp.
func (s *CockpitState) Heartbeat(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heartbeatAt = t
}

// lastHeartbeat returns the latest heartbeat timestamp (zero if none).
func (s *CockpitState) lastHeartbeat() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.heartbeatAt
}

// BeginJob marks a reconcile job as running.
func (s *CockpitState) BeginJob(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobID = jobID
	s.status = "running"
	s.diff = nil
	s.lastError = ""
}

// CompleteJob stores a finished reconcile diff.
func (s *CockpitState) CompleteJob(jobID string, diff *DiffResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobID = jobID
	s.status = "complete"
	s.diff = diff
	s.lastError = ""
}

// FailJob records a reconcile failure.
func (s *CockpitState) FailJob(jobID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobID = jobID
	s.status = "failed"
	s.diff = nil
	s.lastError = sanitize(err.Error())
}

// Snapshot returns a copy of the current state. The returned DiffResult must
// not be mutated by the caller.
func (s *CockpitState) Snapshot() (jobID, status string, diff *DiffResult, lastError string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.jobID, s.status, s.diff, s.lastError
}

// StoreSnapshot remembers the latest invariant verification result.
func (s *CockpitState) StoreSnapshot(snap *invariants.InvariantSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSnapshot = snap
}

// InvariantsSummary returns the last snapshot's five booleans, or false
// defaults when no verification has run yet (spec 07 §3.1 shape).
func (s *CockpitState) InvariantsSummary() (count, unique, order, diff, trace bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastSnapshot == nil {
		return false, false, false, false, false
	}
	snap := s.lastSnapshot
	return snap.IsCountConserved, !snap.HasDuplicateTargetIDs,
		len(snap.DisorderedIndices) == 0, snap.IsDiffComplete, snap.IsZeroTraceClean
}
