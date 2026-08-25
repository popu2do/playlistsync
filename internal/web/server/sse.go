package server

import (
	"sync"
	"sync/atomic"
	"time"

	"playlistsync/internal/web/bridge"
)

// subscriberCapacity is the per-client buffered send channel capacity
// (spec 02 §3.2: 64 slots). A full buffer is dropped non-blockingly so the
// broadcast path never stalls for a slow client.
const subscriberCapacity = 64

// subscriber is one connected SSE client with an exclusive buffered channel.
// The channel is never closed by the hub: unsubscription removes the client
// from the fan-out map, and the reader (Task 2's SSE endpoint) selects on its
// own cancellation.
type subscriber struct {
	id uint64
	ch chan bridge.Event
}

// SSEHub fans broadcast events out to subscribed clients with a non-blocking
// per-client channel (spec 02 §3.2). It implements bridge.EventBroadcaster so
// the WebReviewBridge and the watchdog (SYSTEM_SHUTDOWN) can publish events
// through the same seam. The Last-Event-ID replay ring lives in
// bridge.SSEEventRingBuffer (plan-wc-01) and is layered on top by Task 2's
// /api/v1/events endpoint.
type SSEHub struct {
	mu          sync.RWMutex
	subscribers map[uint64]*subscriber
	nextID      atomic.Uint64
}

// NewSSEHub creates an empty hub.
func NewSSEHub() *SSEHub {
	return &SSEHub{subscribers: make(map[uint64]*subscriber)}
}

// Subscribe registers a client and returns its event channel plus an
// unsubscribe function. The channel is buffered (subscriberCapacity); events
// are dropped for a client whose buffer is full.
func (h *SSEHub) Subscribe() (<-chan bridge.Event, func()) {
	id := h.nextID.Add(1)
	sub := &subscriber{id: id, ch: make(chan bridge.Event, subscriberCapacity)}
	h.mu.Lock()
	h.subscribers[id] = sub
	h.mu.Unlock()
	return sub.ch, func() {
		h.mu.Lock()
		delete(h.subscribers, id)
		h.mu.Unlock()
	}
}

// Broadcast implements bridge.EventBroadcaster: it stamps one event with a
// monotonic id and the current Unix timestamp and non-blockingly delivers it
// to every subscriber. A subscriber with a full buffer is skipped (dropped),
// keeping the broadcast path zero-latency (spec 02 §3.2).
func (h *SSEHub) Broadcast(eventType string, data interface{}) {
	ev := bridge.Event{
		ID:        h.nextID.Add(1),
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now().Unix(),
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, sub := range h.subscribers {
		select {
		case sub.ch <- ev:
		default:
			// Slow client: drop this event non-blockingly (spec 02 §3.2).
		}
	}
}

// ClientCount returns the number of connected subscribers.
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}
