package bridge

import (
	"context"
	"errors"
	"time"
)

// RequestArbitration suspends the calling engine worker goroutine for the
// given track: it registers a pending item, broadcasts an ARBITRATION_REQUIRED
// SSE event, then blocks until the user resolves the arbitration, the
// deadline expires, the context is canceled, or the bridge is closed.
//
// It returns the user's decision, or one of ErrArbitrationTimeout,
// ErrArbitrationCanceled and ErrBridgeClosed. Any goroutine released by
// Close() observes ErrBridgeClosed (a decision is never a nil pointer unless
// the channel was closed without a resolution).
func (b *WebReviewBridge) RequestArbitration(ctx context.Context, req *ArbitrationRequest) (*ArbitrationDecision, error) {
	b.mu.Lock()
	if b.isClosed {
		b.mu.Unlock()
		return nil, ErrBridgeClosed
	}

	respChan := make(chan *ArbitrationDecision, 1)
	b.pending[req.TrackID] = &pendingItem{
		req:      req,
		respChan: respChan,
	}
	b.mu.Unlock()

	// Broadcast the SSE arbitration beacon.
	b.eventHub.Broadcast("ARBITRATION_REQUIRED", req)

	// Bound the wait by the caller's context plus the bridge default timeout.
	evalCtx, cancel := context.WithTimeout(ctx, b.defaultTimeout)
	defer cancel()

	select {
	case decision, ok := <-respChan:
		if !ok {
			// Channel closed by Close(): the bridge shut down while this
			// arbitration was still pending.
			return nil, ErrBridgeClosed
		}
		return decision, nil
	case <-evalCtx.Done():
		b.mu.Lock()
		delete(b.pending, req.TrackID)
		b.mu.Unlock()
		if errors.Is(evalCtx.Err(), context.DeadlineExceeded) {
			return nil, ErrArbitrationTimeout
		}
		return nil, ErrArbitrationCanceled
	}
}

// ResolveArbitration wakes the goroutine suspended for decision.TrackID,
// stamps decision.DecidedAt with the current time, delivers it and closes the
// wake-up channel. It reports false when no matching pending arbitration
// exists (already resolved, timed out or never requested).
func (b *WebReviewBridge) ResolveArbitration(decision *ArbitrationDecision) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	item, exists := b.pending[decision.TrackID]
	if !exists {
		return false
	}

	delete(b.pending, decision.TrackID)
	decision.DecidedAt = time.Now()
	item.respChan <- decision
	close(item.respChan)
	return true
}

// Close marks the bridge closed and releases every pending arbitration by
// closing its wake-up channel; each suspended goroutine observes
// ErrBridgeClosed. Subsequent RequestArbitration calls fail with
// ErrBridgeClosed and ResolveArbitration calls return false.
func (b *WebReviewBridge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.isClosed = true
	for id, item := range b.pending {
		close(item.respChan)
		delete(b.pending, id)
	}
}
