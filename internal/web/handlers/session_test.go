package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"playlistsync/internal/invariants"
	"playlistsync/internal/web/bridge"
)

// testMux builds a ServeMux with all endpoints registered against a recording
// broadcaster and an optional pre-seeded state.
func testMux(t *testing.T, cfg HandlerConfig) *http.ServeMux {
	t.Helper()
	if cfg.Broadcaster == nil {
		cfg.Broadcaster = NewRecordingBroadcaster(bridge.NewSSEEventRingBuffer(0))
	}
	if cfg.Ring == nil {
		cfg.Ring = bridge.NewSSEEventRingBuffer(0)
	}
	if cfg.State == nil {
		cfg.State = NewCockpitState()
	}
	if cfg.Verifier == nil {
		cfg.Verifier = invariants.NewVerifier()
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 50 * time.Millisecond
	}
	mux := http.NewServeMux()
	RegisterAll(mux, cfg.Verifier, cfg)
	return mux
}

func doReq(t *testing.T, mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

// decodeJSON unmarshals a recorded response body into v.
func decodeJSON(t *testing.T, w *httptest.ResponseRecorder, v interface{}) error {
	t.Helper()
	return json.Unmarshal(w.Body.Bytes(), v)
}

type invariantSummaryJSON struct {
	CountConserved  bool `json:"count_conserved"`
	UniquenessValid bool `json:"uniqueness_valid"`
	OrderMonotonic  bool `json:"order_lis_monotonic"`
	DiffComplete    bool `json:"diff_complete"`
	ZeroTraceClean  bool `json:"zero_trace_clean"`
}

func TestSessionStatus(t *testing.T) {
	state := NewCockpitState()
	state.StoreSnapshot(&invariants.InvariantSnapshot{
		IsCountConserved: true, HasDuplicateTargetIDs: false,
		IsDiffComplete: true, IsZeroTraceClean: true, AllPassed: true,
	})

	cfg := HandlerConfig{
		SessionID:   "sess_test",
		StartTime:   time.Date(2025, 2, 23, 10, 0, 0, 0, time.UTC),
		Port:        func() int { return 3080 },
		ClientCount: func() int { return 1 },
		State:       state,
	}
	mux := testMux(t, cfg)

	tests := []struct {
		name   string
		method string
		target string
		want   int
	}{
		{"status ok", "GET", "/api/v1/session", http.StatusOK},
		{"method not allowed", "DELETE", "/api/v1/session", http.StatusMethodNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(t, mux, tc.method, tc.target, "")
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
			if tc.want != http.StatusOK {
				return
			}
			var resp struct {
				SessionID     string               `json:"session_id"`
				Status        string               `json:"status"`
				Port          int                  `json:"port"`
				ClientCount   int                  `json:"client_count"`
				StartedAt     string               `json:"started_at"`
				LastHeartbeat string               `json:"last_heartbeat_at"`
				Invariants    invariantSummaryJSON `json:"invariants_summary"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.SessionID != "sess_test" {
				t.Errorf("session_id = %q", resp.SessionID)
			}
			if resp.Port != 3080 {
				t.Errorf("port = %d, want 3080", resp.Port)
			}
			if resp.ClientCount != 1 {
				t.Errorf("client_count = %d, want 1", resp.ClientCount)
			}
			if !resp.Invariants.CountConserved || !resp.Invariants.ZeroTraceClean {
				t.Errorf("invariants summary not surfaced: %+v", resp.Invariants)
			}
		})
	}
}

func TestSessionHeartbeatKicksWatchdog(t *testing.T) {
	kicks := 0
	mux := testMux(t, HandlerConfig{Kick: func() { kicks++ }})

	w := doReq(t, mux, "POST", "/api/v1/session/heartbeat", "{}")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	if kicks != 1 {
		t.Errorf("watchdog kicks = %d, want 1", kicks)
	}
	var resp struct {
		Status    string `json:"status"`
		Timestamp int64  `json:"timestamp"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "ALIVE" {
		t.Errorf("status = %q, want ALIVE", resp.Status)
	}
	if resp.Timestamp == 0 {
		t.Error("timestamp missing")
	}
}

func TestSessionHeartbeatTracksStateTimestamp(t *testing.T) {
	state := NewCockpitState()
	mux := testMux(t, HandlerConfig{State: state, Kick: func() {}})
	doReq(t, mux, "POST", "/api/v1/session/heartbeat", "{}")
	if state.lastHeartbeat().IsZero() {
		t.Error("heartbeat timestamp not recorded in cockpit state")
	}
}

func TestSessionShutdownBroadcastsAndShutsDown(t *testing.T) {
	rec := NewRecordingBroadcaster(bridge.NewSSEEventRingBuffer(0))
	shutdownCalled := make(chan struct{}, 1)
	cfg := HandlerConfig{
		Broadcaster: rec,
		Ring:        rec.Ring(),
		State:       NewCockpitState(),
		Shutdown: func(ctx context.Context) error {
			shutdownCalled <- struct{}{}
			return nil
		},
		Kick: func() {},
	}
	mux := testMux(t, cfg)

	w := doReq(t, mux, "POST", "/api/v1/session/shutdown", "{}")
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", w.Code, w.Body.String())
	}

	// SYSTEM_SHUTDOWN must be in the ring (broadcast through the recorder).
	found := false
	events, _ := rec.Ring().ReadSince(0)
	for _, ev := range events {
		if ev.Type == "SYSTEM_SHUTDOWN" {
			found = true
		}
	}
	if !found {
		t.Errorf("SYSTEM_SHUTDOWN not broadcast; ring=%+v", events)
	}

	select {
	case <-shutdownCalled:
		// graceful shutdown seam invoked.
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown seam not invoked")
	}
}
