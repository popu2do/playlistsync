package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"playlistsync/internal/web/bridge"
)

// RecordingBroadcaster is the handlers-side event bus: it writes every
// broadcast into the SSE ring buffer (capturing history for Last-Event-ID
// replay, even with no client connected) and fans the same ring event out to
// connected subscribers through per-client buffered channels (spec 02 §3.2).
//
// It implements bridge.EventBroadcaster so the WebReviewBridge and every
// handler can broadcast through the same seam; cmd/web passes a
// *RecordingBroadcaster as HandlerConfig.Broadcaster and the SSE endpoint
// subscribes to it.
type RecordingBroadcaster struct {
	mu     sync.RWMutex
	ring   *bridge.SSEEventRingBuffer
	subs   map[uint64]chan bridge.Event
	nextID uint64
}

// NewRecordingBroadcaster creates a broadcaster over the given ring (nil ring
// creates a default-capacity one).
func NewRecordingBroadcaster(ring *bridge.SSEEventRingBuffer) *RecordingBroadcaster {
	if ring == nil {
		ring = bridge.NewSSEEventRingBuffer(0)
	}
	return &RecordingBroadcaster{
		ring: ring,
		subs: make(map[uint64]chan bridge.Event),
	}
}

// Broadcast implements bridge.EventBroadcaster: the event is persisted to the
// ring (stamping a monotonic id + timestamp) and delivered to every subscriber
// non-blockingly (slow clients are dropped, spec 02 §3.2).
func (b *RecordingBroadcaster) Broadcast(eventType string, data interface{}) {
	ev := b.ring.Write(bridge.Event{Type: eventType, Data: data})
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// Slow client: drop this event (spec 02 §3.2 non-blocking fan-out).
		}
	}
}

// Subscribe registers a client and returns its event channel plus an
// unsubscribe function. The channel is buffered (subscriberCapacity); a full
// buffer drops events rather than blocking the broadcaster (spec 02 §3.2).
func (b *RecordingBroadcaster) Subscribe() (<-chan bridge.Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := b.nextID
	ch := make(chan bridge.Event, 64)
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
	}
}

// ClientCount returns the number of connected subscribers.
func (b *RecordingBroadcaster) ClientCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// Ring exposes the replay store.
func (b *RecordingBroadcaster) Ring() *bridge.SSEEventRingBuffer {
	return b.ring
}

// compile-time assertion: *RecordingBroadcaster satisfies bridge.EventBroadcaster.
var _ bridge.EventBroadcaster = (*RecordingBroadcaster)(nil)

// eventsHandler serves GET /api/v1/events (spec 02 §3): an SSE stream that
// (1) replays ring history per Last-Event-ID (GAP_FALLBACK when history was
// overwritten), (2) fans out live broadcasts, and (3) emits a heartbeat
// comment every HeartbeatInterval (default 30s).
//
// Lifecycle obligations (Task-1 review INFO-3):
//   - defer unsubscribe() so http.Server.Shutdown can drain the connection
//     without leaking the subscriber;
//   - select on RootCtx.Done() (and the request context) so the handler
//     returns promptly when the server begins graceful shutdown — otherwise
//     Shutdown burns its full 5s budget waiting for the SSE goroutine.
type eventsHandler struct {
	cfg HandlerConfig
}

// RegisterEventsHandler mounts the SSE endpoint.
func RegisterEventsHandler(mux *http.ServeMux, cfg HandlerConfig) {
	mux.HandleFunc("GET /api/v1/events", (&eventsHandler{cfg: cfg}).ServeHTTP)
}

func (h *eventsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ring := h.cfg.Ring
	rootDone := h.cfg.RootCtx

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Last-Event-ID replay (spec 02 §3.2 / spec 05 §4.1).
	lastID := parseLastEventID(r.Header.Get("Last-Event-ID"))

	// Subscribe FIRST so the live bus starts buffering from this instant
	// (qc3-M3 TOCTOU fix): any broadcast that lands while we hold on to the
	// ring for replay is captured in the subscription channel and deduplicated
	// against the replayed range below — zero event loss across a reconnect.
	streamer := h.cfg.Broadcaster
	ch := (<-chan bridge.Event)(nil)
	if streamer != nil {
		sub, unsub := streamer.Subscribe()
		ch = sub
		// defer unsubscribe() is the leak-free guarantee: whether the loop
		// exits via client disconnect, server shutdown, or a write failure,
		// the subscriber is removed (Task-1 review INFO-3).
		defer unsub()
	}

	// Replay ring history (only events strictly after lastID).
	replayMax := lastID // highest id already sent to this client
	if ring != nil {
		events, gap := ring.ReadSince(lastID)
		if gap {
			// History overwritten: tell the client to resync via
			// GET /api/v1/reconcile/diff (spec 02 §3.2).
			writeSSEEvent(w, flusher, bridge.Event{
				Type: "GAP_FALLBACK",
				Data: map[string]interface{}{
					"message": "event history gap: resync via GET /api/v1/reconcile/diff",
				},
			})
		} else {
			for i := range events {
				writeSSEEvent(w, flusher, events[i])
			}
			if n := len(events); n > 0 {
				replayMax = events[n-1].ID
			}
		}
	}

	// No streamer (nil config): serve replay, then hold open until the client
	// disconnects.
	if ch == nil {
		<-r.Context().Done()
		return
	}

	heartbeat := h.cfg.HeartbeatInterval
	if heartbeat <= 0 {
		heartbeat = 30 * time.Second // spec 02 §3.4
	}
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()

	for {
		select {
		case ev := <-ch:
			// Drop events already delivered by the replay (or below the
			// client's acknowledged id): the ring is written before fan-out,
			// so anything at or below replayMax was either replayed just now
			// or predates the client's cursor. Streaming only ev.ID > replayMax
			// guarantees exactly-once delivery across the subscribe/replay
			// boundary (qc3-M3).
			if ev.ID <= replayMax {
				continue
			}
			writeSSEEvent(w, flusher, ev)
		case <-ticker.C:
			writeSSEComment(w, flusher)
		case <-r.Context().Done():
			return
		case <-rootDone:
			return
		}
	}
}

// parseLastEventID parses the Last-Event-ID header (0 = from the beginning).
func parseLastEventID(v string) uint64 {
	if v == "" {
		return 0
	}
	id, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// writeSSEEvent writes one SSE event frame:
//
//	id: <id>
//	event: <type>
//	data: <json>
//
// and flushes. The id mirrors the ring's id so a reconnect's Last-Event-ID
// continues the same sequence.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, ev bridge.Event) {
	data, err := json.Marshal(ev.Data)
	if err != nil {
		data = []byte("{}")
	}
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, data)
	flusher.Flush()
}

// writeSSEComment emits a heartbeat comment (": ping" + blank line) which SSE
// clients ignore but which keeps the connection and proxies alive (spec 02
// §3.4: 30s heartbeat).
func writeSSEComment(w http.ResponseWriter, flusher http.Flusher) {
	fmt.Fprint(w, ": ping\n\n")
	flusher.Flush()
}
