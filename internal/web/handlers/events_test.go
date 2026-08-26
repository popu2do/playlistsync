package handlers

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"playlistsync/internal/web/bridge"
)

// sseReader reads the SSE response body line-by-line into a channel.
type sseReader struct {
	lines chan string
	done  chan struct{}
}

func newSSEReader(t *testing.T, resp *http.Response) *sseReader {
	t.Helper()
	r := &sseReader{lines: make(chan string, 64), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case r.lines <- sc.Text():
			default:
				// drop if the test stopped consuming (no block)
			}
		}
	}()
	return r
}

// waitForLine reads lines until pred matches or the deadline expires.
func (r *sseReader) waitForLine(t *testing.T, timeout time.Duration, pred func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case line := <-r.lines:
			if pred(line) {
				return line
			}
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("timeout waiting for SSE line")
	return ""
}

// eventsTestServer boots a real httptest server serving the mux built from cfg.
// It always creates a fresh recording broadcaster + ring and seeds them into
// the config, so every SSE test drives a *RecordingBroadcaster directly.
func eventsTestServer(t *testing.T, cfg HandlerConfig) (*httptest.Server, *RecordingBroadcaster, *bridge.SSEEventRingBuffer) {
	t.Helper()
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	ring := cfg.Ring
	if ring == nil {
		ring = bridge.NewSSEEventRingBuffer(0)
	}
	rec, _ := cfg.Broadcaster.(*RecordingBroadcaster)
	if rec == nil {
		rec = NewRecordingBroadcaster(ring)
	}
	cfg.Broadcaster = rec
	cfg.Ring = ring
	mux := testMux(t, cfg)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, rec, ring
}

func TestSSELiveEventDelivery(t *testing.T) {
	srv, rec, _ := eventsTestServer(t, HandlerConfig{})
	resp, err := http.Get(srv.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}

	reader := newSSEReader(t, resp)

	// Wait until the handler has subscribed (ClientCount reaches 1) before
	// broadcasting — otherwise the event is only persisted to the ring and
	// never delivered live to this connection.
	deadline := time.Now().Add(3 * time.Second)
	for rec.ClientCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if rec.ClientCount() != 1 {
		t.Fatalf("SSE subscriber not registered (count=%d)", rec.ClientCount())
	}

	// Broadcast must be delivered live to the connected subscriber. Frame
	// order is id -> event -> data (writeSSEEvent), so consume in that order.
	rec.Broadcast("TEST_EVENT", map[string]interface{}{"n": 1})
	reader.waitForLine(t, 3*time.Second, func(s string) bool {
		return strings.HasPrefix(s, "id: ")
	})
	line := reader.waitForLine(t, 3*time.Second, func(s string) bool {
		return strings.HasPrefix(s, "event: TEST_EVENT")
	})
	if line != "event: TEST_EVENT" {
		t.Errorf("unexpected line %q", line)
	}
	// The data line carries the payload.
	reader.waitForLine(t, time.Second, func(s string) bool {
		return strings.Contains(s, `"n":1`)
	})
}

func TestSSELastEventIDReplay(t *testing.T) {
	srv, rec, _ := eventsTestServer(t, HandlerConfig{})
	// Seed history: ids 1,2,3.
	rec.Broadcast("E1", map[string]interface{}{"i": 1})
	rec.Broadcast("E2", map[string]interface{}{"i": 2})
	rec.Broadcast("E3", map[string]interface{}{"i": 3})

	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/events", nil)
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	reader := newSSEReader(t, resp)
	// Replay starts at id 2 (events after lastID=1).
	line := reader.waitForLine(t, 3*time.Second, func(s string) bool {
		return strings.HasPrefix(s, "event: ")
	})
	if !strings.Contains(line, "E2") {
		t.Errorf("first replayed event = %q, want E2 (ids > 1)", line)
	}
}

func TestSSEHeartbeat(t *testing.T) {
	srv, _, _ := eventsTestServer(t, HandlerConfig{HeartbeatInterval: 40 * time.Millisecond})
	resp, err := http.Get(srv.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := newSSEReader(t, resp)
	// A heartbeat comment ": ping" must arrive within ~500ms.
	reader.waitForLine(t, time.Second, func(s string) bool {
		return strings.HasPrefix(s, ": ping")
	})
}

func TestSSEGapFallback(t *testing.T) {
	// Tiny ring (cap 3): writing 5 events overwrites ids 1,2; a client that
	// last saw id 1 must be told to resync (gap detected).
	ring := bridge.NewSSEEventRingBuffer(3)
	rec := NewRecordingBroadcaster(ring)
	srv, _, _ := eventsTestServer(t, HandlerConfig{Broadcaster: rec, Ring: ring})

	for i := 1; i <= 5; i++ {
		rec.Broadcast(fmt.Sprintf("EV%d", i), map[string]interface{}{"i": i})
	}

	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/events", nil)
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	reader := newSSEReader(t, resp)
	line := reader.waitForLine(t, 3*time.Second, func(s string) bool {
		return strings.Contains(s, "GAP_FALLBACK")
	})
	if !strings.Contains(line, "event: GAP_FALLBACK") {
		t.Errorf("unexpected gap line %q", line)
	}
}

func TestSSEDisconnectDropsSubscriber(t *testing.T) {
	srv, rec, _ := eventsTestServer(t, HandlerConfig{})
	resp, err := http.Get(srv.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	reader := newSSEReader(t, resp)

	// Wait until the subscriber is registered.
	deadline := time.Now().Add(3 * time.Second)
	for rec.ClientCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if rec.ClientCount() != 1 {
		t.Fatalf("subscriber count = %d, want 1", rec.ClientCount())
	}

	// Client disconnects; the handler must unsub (defer unsub()) promptly.
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for rec.ClientCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	_ = reader
	if rec.ClientCount() != 0 {
		t.Errorf("subscriber count after disconnect = %d, want 0 (defer unsub leak)", rec.ClientCount())
	}
}

func TestSSERootCtxCancellationReleases(t *testing.T) {
	// INFO-3: the SSE handler must return when the server root context is
	// cancelled, so http.Server.Shutdown can drain without burning its 5s
	// budget. We drive RootCtx directly and assert the stream closes.
	rootCtx := make(chan struct{})
	srv, _, _ := eventsTestServer(t, HandlerConfig{RootCtx: rootCtx, HeartbeatInterval: 30 * time.Millisecond})
	resp, err := http.Get(srv.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Ensure the connection is established and streaming (idle heartbeat).
	reader := newSSEReader(t, resp)
	reader.waitForLine(t, 2*time.Second, func(s string) bool {
		return strings.HasPrefix(s, ": ping") || strings.HasPrefix(s, "event: ")
	})

	// Cancel the root context; the handler must return and close the stream.
	close(rootCtx)
	done := make(chan struct{})
	go func() {
		<-reader.done
		close(done)
	}()
	select {
	case <-done:
		// handler exited; stream EOF.
	case <-time.After(3 * time.Second):
		t.Fatal("SSE handler did not release on root context cancellation")
	}
}

// TestSSEReconnectNoLossNoDuplicate is the qc3-M3 regression: a reconnect must
// see every event after its Last-Event-ID exactly once. The handler subscribes
// to the live bus BEFORE replaying ring history, so an event broadcast inside
// that window lands both in the ring (replay) and in the subscription channel;
// the dedup rule (ev.ID <= replayMax) must ensure it is delivered exactly once,
// never lost, never duplicated.
func TestSSEReconnectNoLossNoDuplicate(t *testing.T) {
	srv, rec, _ := eventsTestServer(t, HandlerConfig{})
	// Seed history: ids 1,2,3.
	rec.Broadcast("E1", map[string]interface{}{"i": 1})
	rec.Broadcast("E2", map[string]interface{}{"i": 2})
	rec.Broadcast("E3", map[string]interface{}{"i": 3})

	// Reconnect with Last-Event-ID=1 and, racing the handler's subscribe, fire
	// E4. Whether E4 lands in the ring replay or only the live channel, the
	// client must see it exactly once — and E2/E3 exactly once each.
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/events", nil)
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := newSSEReader(t, resp)

	// Give the handler a moment to subscribe AND run replay (the TOCTOU
	// window we are closing), then broadcast the racing event.
	deadline := time.Now().Add(3 * time.Second)
	for rec.ClientCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if rec.ClientCount() != 1 {
		t.Fatal("SSE subscriber not registered for reconnect test")
	}
	rec.Broadcast("E4", map[string]interface{}{"i": 4})
	rec.Broadcast("E5", map[string]interface{}{"i": 5})

	// Consume every SSE line until E5's data arrives (then read on a bit more
	// to catch stragglers), counting occurrences of each event type.
	counts := map[string]int{}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case line := <-reader.lines:
			if strings.HasPrefix(line, "event: ") {
				counts[strings.TrimPrefix(line, "event: ")]++
			}
		case <-time.After(50 * time.Millisecond):
		}
		if counts["E5"] >= 1 && counts["E4"] >= 1 && len(reader.lines) == 0 {
			break
		}
	}

	for _, typ := range []string{"E2", "E3", "E4", "E5"} {
		if counts[typ] != 1 {
			t.Errorf("event %s delivered %d times, want exactly 1 (zero loss / zero duplicate)", typ, counts[typ])
		}
	}
}
