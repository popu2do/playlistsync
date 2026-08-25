// Package bridge implements the in-process bidirectional WebReviewBridge that
// connects the domain engine to the web cockpit: an engine worker goroutine
// suspends on RequestArbitration, an HTTP handler wakes it with a user
// decision via ResolveArbitration, and an SSE ring buffer (SSEEventRingBuffer)
// fans events out to clients with Last-Event-ID replay.
//
// The package is process-internal by design: the engine hands a struct pointer
// to a buffered channel, so no subprocess, no serialization and no polling is
// involved. The bridge depends only on the EventBroadcaster interface, which
// the SSE hub (internal/web/server, plan-wc-02) implements.
package bridge

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrArbitrationTimeout is returned when a pending arbitration is not
	// resolved before the deadline.
	ErrArbitrationTimeout = errors.New("arbitration decision timeout")

	// ErrArbitrationCanceled is returned when the arbitration context is
	// canceled by the caller.
	ErrArbitrationCanceled = errors.New("arbitration canceled by context")

	// ErrBridgeClosed is returned when an arbitration is requested on, or
	// released by, a closed bridge.
	ErrBridgeClosed = errors.New("web review bridge is closed")
)

// ArbitrationAction is the user-facing decision action type.
type ArbitrationAction string

const (
	// ActionSelectCandidate confirms a proposed candidate as the match.
	ActionSelectCandidate ArbitrationAction = "SELECT_CANDIDATE"

	// ActionSkip skips the track (leave it unsynced).
	ActionSkip ArbitrationAction = "SKIP"

	// ActionCustomID accepts a user-supplied target track ID.
	ActionCustomID ArbitrationAction = "CUSTOM_ID"
)

// ArbitrationRequest is the suspension payload a domain engine worker sends
// to the bridge when a track needs human arbitration.
type ArbitrationRequest struct {
	TrackID          string            `json:"track_id"`
	SourceTitle      string            `json:"source_title"`
	SourceArtist     string            `json:"source_artist"`
	SourceDurationMs int               `json:"source_duration_ms"`
	Candidates       []CandidateOption `json:"candidates"`
	CreatedAt        time.Time         `json:"created_at"`
}

// CandidateOption is a candidate track presented for arbitration.
type CandidateOption struct {
	TargetID        string  `json:"target_id"`
	Title           string  `json:"title"`
	Artist          string  `json:"artist"`
	DurationMs      int     `json:"duration_ms"`
	ConfidenceScore float64 `json:"confidence_score"`
	TitleScore      float64 `json:"title_score"`
	ArtistScore     float64 `json:"artist_score"`
	DurationScore   float64 `json:"duration_score"`
	ISRCMatched     bool    `json:"isrc_matched"`
}

// ArbitrationDecision is the user's decision submitted by the web handler.
type ArbitrationDecision struct {
	TrackID          string            `json:"track_id"`
	Action           ArbitrationAction `json:"action"`
	SelectedTargetID string            `json:"selected_target_id,omitempty"`
	DecidedAt        time.Time         `json:"decided_at"`
}

// pendingItem wraps a suspended arbitration with its wake-up channel.
type pendingItem struct {
	req      *ArbitrationRequest
	respChan chan *ArbitrationDecision
}

// EventBroadcaster delivers SSE events to connected clients. The concrete SSE
// hub (internal/web/server, plan-wc-02) implements this interface; the bridge
// depends on the interface only. Implementations must be safe for concurrent
// use.
type EventBroadcaster interface {
	// Broadcast publishes one SSE event of the given type with the given
	// payload.
	Broadcast(eventType string, data interface{})
}

// WebReviewBridge is the in-memory bidirectional bridge between engine worker
// goroutines and the web cockpit. Pending arbitrations are stored in a
// sync.RWMutex-protected map keyed by track ID.
type WebReviewBridge struct {
	mu             sync.RWMutex
	pending        map[string]*pendingItem
	eventHub       EventBroadcaster
	isClosed       bool
	defaultTimeout time.Duration
}

// NewWebReviewBridge creates a bridge. A non-positive timeout defaults to
// 5 minutes per pending arbitration.
func NewWebReviewBridge(hub EventBroadcaster, timeout time.Duration) *WebReviewBridge {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &WebReviewBridge{
		pending:        make(map[string]*pendingItem),
		eventHub:       hub,
		defaultTimeout: timeout,
	}
}
