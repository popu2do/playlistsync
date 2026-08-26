package bridge

import (
	"sync"
	"time"
)

// Event is one entry in the SSE ring buffer. Timestamp (Unix seconds) is
// stamped at write time and supports ordering/radar time-series rendering.
type Event struct {
	ID        uint64      `json:"id"`
	Type      string      `json:"type"`
	Data      interface{} `json:"data"`
	Timestamp int64       `json:"timestamp"`
}

// SSEEventRingBuffer is a fixed-capacity ring buffer for SSE events with
// globally monotonic uint64 ids. It supports Last-Event-ID replay and gap
// detection for slow consumers: when the requested range has been overwritten
// (current - lastID > capacity), ReadSince reports gap=true so the caller can
// push a GAP_FALLBACK event that triggers a full-state resync.
type SSEEventRingBuffer struct {
	mu        sync.RWMutex
	capacity  int
	head      int
	count     int
	events    []Event
	currentID uint64
}

// NewSSEEventRingBuffer creates a ring buffer with the given capacity. A
// non-positive capacity defaults to 1024 slots.
func NewSSEEventRingBuffer(capacity int) *SSEEventRingBuffer {
	if capacity <= 0 {
		capacity = 1024
	}
	return &SSEEventRingBuffer{
		capacity: capacity,
		events:   make([]Event, capacity),
	}
}

// Write stores event under the next monotonic id, stamps Timestamp with the
// current Unix second, and returns the stamped copy. Oldest events are
// overwritten when the buffer is full.
func (r *SSEEventRingBuffer) Write(event Event) Event {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.currentID++
	event.ID = r.currentID
	event.Timestamp = time.Now().Unix()

	idx := (r.head + r.count) % r.capacity
	if r.count == r.capacity {
		r.head = (r.head + 1) % r.capacity
	} else {
		r.count++
	}
	r.events[idx] = event
	return event
}

// LastID returns the highest event id written so far (0 when empty).
func (r *SSEEventRingBuffer) LastID() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.currentID
}

// ReadSince returns the events with id > lastID in write order, plus
// gap=true when current - lastID > capacity, i.e. the requested history has
// been overwritten by the ring and the caller must emit a GAP_FALLBACK event
// so the client performs a full-state resync. When gap is false the replay is
// guaranteed complete. An empty buffer or a lastID at/after the current id
// returns nil, false.
func (r *SSEEventRingBuffer) ReadSince(lastID uint64) ([]Event, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.count == 0 || lastID >= r.currentID {
		return nil, false
	}
	if r.currentID-lastID > uint64(r.capacity) {
		// Slow consumer missed events that have already been overwritten.
		return r.snapshotLocked(), true
	}

	var out []Event
	for i := 0; i < r.count; i++ {
		ev := r.events[(r.head+i)%r.capacity]
		if ev.ID > lastID {
			out = append(out, ev)
		}
	}
	return out, false
}

// snapshotLocked returns a copy of all retained events in write order. Caller
// must hold r.mu (read or write).
func (r *SSEEventRingBuffer) snapshotLocked() []Event {
	out := make([]Event, r.count)
	for i := 0; i < r.count; i++ {
		out[i] = r.events[(r.head+i)%r.capacity]
	}
	return out
}
