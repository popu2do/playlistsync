package bridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"playlistsync/internal/web/bridge"
	"sync"
	"testing"
	"time"
)

// fakeHub records broadcasts and can be waited on for a target count; it is
// safe for concurrent use, matching the EventBroadcaster contract.
type fakeHub struct {
	mu     sync.Mutex
	events []broadcastRecord
}

type broadcastRecord struct {
	eventType string
	data      interface{}
}

func (h *fakeHub) Broadcast(eventType string, data interface{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, broadcastRecord{eventType: eventType, data: data})
}

func (h *fakeHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.events)
}

func (h *fakeHub) waitForCount(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if h.count() >= n {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func testRequest(id string) *bridge.ArbitrationRequest {
	return &bridge.ArbitrationRequest{
		TrackID:          id,
		SourceTitle:      "Song " + id,
		SourceArtist:     "Some Artist",
		SourceDurationMs: 200000,
		Candidates: []bridge.CandidateOption{
			{TargetID: "yt:" + id, Title: "Song " + id, ConfidenceScore: 0.92},
		},
		CreatedAt: time.Now().UTC(),
	}
}

func TestRequestArbitrationResolve(t *testing.T) {
	hub := &fakeHub{}
	b := bridge.NewWebReviewBridge(hub, time.Minute)

	type result struct {
		decision *bridge.ArbitrationDecision
		err      error
	}
	resCh := make(chan result, 1)
	req := testRequest("track-1")
	go func() {
		d, err := b.RequestArbitration(context.Background(), req)
		resCh <- result{d, err}
	}()

	if !hub.waitForCount(1, 2*time.Second) {
		t.Fatal("ARBITRATION_REQUIRED broadcast not observed")
	}

	decision := &bridge.ArbitrationDecision{
		TrackID:          "track-1",
		Action:           bridge.ActionSelectCandidate,
		SelectedTargetID: "yt:track-1",
	}
	if ok := b.ResolveArbitration(decision); !ok {
		t.Fatal("ResolveArbitration returned false for a pending track")
	}

	res := <-resCh
	if res.err != nil {
		t.Fatalf("RequestArbitration error: %v", res.err)
	}
	if res.decision == nil {
		t.Fatal("RequestArbitration returned a nil decision")
	}
	if res.decision.TrackID != "track-1" || res.decision.SelectedTargetID != "yt:track-1" {
		t.Fatalf("unexpected decision: %+v", res.decision)
	}
	if res.decision.DecidedAt.IsZero() {
		t.Fatal("DecidedAt was not stamped by ResolveArbitration")
	}

	hub.mu.Lock()
	defer hub.mu.Unlock()
	if len(hub.events) != 1 || hub.events[0].eventType != "ARBITRATION_REQUIRED" {
		t.Fatalf("unexpected broadcasts: %+v", hub.events)
	}
	if got, ok := hub.events[0].data.(*bridge.ArbitrationRequest); !ok || got != req {
		t.Fatalf("broadcast payload is not the arbitration request: %#v", hub.events[0].data)
	}
}

// TestRequestArbitrationErrorMatrix table-drives the failure outcomes of
// RequestArbitration (timeout, context cancellation, closed bridge) across
// both the entry and mid-flight paths, and verifies each failed request
// leaves no orphaned pending entry behind.
func TestRequestArbitrationErrorMatrix(t *testing.T) {
	tests := []struct {
		name               string
		bridgeTimeout      time.Duration
		ctxTimeout         time.Duration // 0: no context deadline
		cancelBefore       bool          // cancel ctx before requesting
		cancelAfterRequest bool          // cancel ctx after ARBITRATION_REQUIRED is broadcast
		closeBefore        bool          // Close() the bridge before requesting
		wantErr            error
	}{
		{
			name:          "bridge default timeout expires",
			bridgeTimeout: 30 * time.Millisecond,
			wantErr:       bridge.ErrArbitrationTimeout,
		},
		{
			name:          "context deadline fires before bridge timeout",
			bridgeTimeout: time.Hour,
			ctxTimeout:    30 * time.Millisecond,
			wantErr:       bridge.ErrArbitrationTimeout,
		},
		{
			name:          "context canceled before request",
			bridgeTimeout: time.Hour,
			cancelBefore:  true,
			wantErr:       bridge.ErrArbitrationCanceled,
		},
		{
			name:               "context canceled while parked",
			bridgeTimeout:      time.Hour,
			cancelAfterRequest: true,
			wantErr:            bridge.ErrArbitrationCanceled,
		},
		{
			name:          "bridge closed before request",
			bridgeTimeout: time.Hour,
			closeBefore:   true,
			wantErr:       bridge.ErrBridgeClosed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub := &fakeHub{}
			b := bridge.NewWebReviewBridge(hub, tt.bridgeTimeout)
			if tt.closeBefore {
				b.Close()
			}

			ctx := context.Background()
			var cancel context.CancelFunc = func() {}
			switch {
			case tt.ctxTimeout > 0:
				ctx, cancel = context.WithTimeout(ctx, tt.ctxTimeout)
			case tt.cancelBefore:
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case tt.cancelAfterRequest:
				ctx, cancel = context.WithCancel(ctx)
			}
			defer cancel()

			const trackID = "track-error"
			if tt.cancelAfterRequest {
				errCh := make(chan error, 1)
				go func() {
					_, err := b.RequestArbitration(ctx, testRequest(trackID))
					errCh <- err
				}()
				if !hub.waitForCount(1, 2*time.Second) {
					t.Fatal("ARBITRATION_REQUIRED broadcast not observed")
				}
				cancel()
				if err := <-errCh; !errors.Is(err, tt.wantErr) {
					t.Fatalf("RequestArbitration err = %v, want %v", err, tt.wantErr)
				}
			} else {
				if _, err := b.RequestArbitration(ctx, testRequest(trackID)); !errors.Is(err, tt.wantErr) {
					t.Fatalf("RequestArbitration err = %v, want %v", err, tt.wantErr)
				}
			}

			// A failed request must not leave an orphaned pending entry:
			// resolving it afterwards must fail.
			decision := &bridge.ArbitrationDecision{TrackID: trackID, Action: bridge.ActionSkip}
			if ok := b.ResolveArbitration(decision); ok {
				t.Fatal("ResolveArbitration unexpectedly succeeded after a failed request")
			}
		})
	}
}

func TestCloseReleasesPendingArbitration(t *testing.T) {
	hub := &fakeHub{}
	b := bridge.NewWebReviewBridge(hub, time.Minute)

	resCh := make(chan error, 1)
	go func() {
		_, err := b.RequestArbitration(context.Background(), testRequest("track-pending"))
		resCh <- err
	}()
	if !hub.waitForCount(1, 2*time.Second) {
		t.Fatal("ARBITRATION_REQUIRED broadcast not observed")
	}

	b.Close()
	err := <-resCh
	if !errors.Is(err, bridge.ErrBridgeClosed) {
		t.Fatalf("pending arbitration after Close: err = %v, want ErrBridgeClosed", err)
	}
}

func TestResolveArbitrationUnknownTrack(t *testing.T) {
	hub := &fakeHub{}
	b := bridge.NewWebReviewBridge(hub, time.Minute)
	if ok := b.ResolveArbitration(&bridge.ArbitrationDecision{TrackID: "never-requested"}); ok {
		t.Fatal("ResolveArbitration returned true for an unknown track")
	}
}

func TestConcurrentArbitration10Goroutines(t *testing.T) {
	hub := &fakeHub{}
	b := bridge.NewWebReviewBridge(hub, time.Minute)
	const n = 10

	type result struct {
		decision *bridge.ArbitrationDecision
		err      error
	}
	results := make([]chan result, n)
	ctx := context.Background()
	for i := 0; i < n; i++ {
		id := "track-" + string(rune('A'+i))
		results[i] = make(chan result, 1)
		go func(id string, ch chan result) {
			d, err := b.RequestArbitration(ctx, testRequest(id))
			ch <- result{d, err}
		}(id, results[i])
	}

	// All ten requests must be registered (and broadcast) before resolving.
	if !hub.waitForCount(n, 5*time.Second) {
		t.Fatalf("expected %d broadcasts, got %d", n, hub.count())
	}

	// Resolve in reverse order to exercise map deletion + wake-up ordering.
	for i := n - 1; i >= 0; i-- {
		id := "track-" + string(rune('A'+i))
		d := &bridge.ArbitrationDecision{TrackID: id, Action: bridge.ActionSelectCandidate, SelectedTargetID: "yt:" + id}
		if !b.ResolveArbitration(d) {
			t.Fatalf("resolve %s returned false", id)
		}
	}

	for i, ch := range results {
		id := "track-" + string(rune('A'+i))
		res := <-ch
		if res.err != nil {
			t.Fatalf("%s: RequestArbitration error: %v", id, res.err)
		}
		if res.decision == nil || res.decision.TrackID != id || res.decision.SelectedTargetID != "yt:"+id {
			t.Fatalf("%s: unexpected decision %+v", id, res.decision)
		}
	}

	// Nothing remains pending: a duplicate resolve must fail.
	if ok := b.ResolveArbitration(&bridge.ArbitrationDecision{TrackID: "track-A"}); ok {
		t.Fatal("duplicate resolve unexpectedly succeeded")
	}
}

// TestArbitrationJSONContract locks the JSON wire format against spec §4.1:
// every field must marshal to the documented key, and selected_target_id must
// be omitted when empty (omitempty).
func TestArbitrationJSONContract(t *testing.T) {
	req := &bridge.ArbitrationRequest{
		TrackID:          "spotify:track:abc",
		SourceTitle:      "Song",
		SourceArtist:     "Artist",
		SourceDurationMs: 200000,
		Candidates: []bridge.CandidateOption{
			{
				TargetID:        "yt:abc",
				Title:           "Song",
				Artist:          "Artist",
				DurationMs:      200000,
				ConfidenceScore: 0.95,
				TitleScore:      0.9,
				ArtistScore:     0.8,
				DurationScore:   1,
				ISRCMatched:     true,
			},
		},
		CreatedAt: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
	}

	reqJSON, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal ArbitrationRequest: %v", err)
	}
	var reqMap map[string]any
	if err := json.Unmarshal(reqJSON, &reqMap); err != nil {
		t.Fatalf("unmarshal ArbitrationRequest: %v", err)
	}
	for _, key := range []string{
		"track_id", "source_title", "source_artist", "source_duration_ms",
		"candidates", "created_at",
	} {
		if _, ok := reqMap[key]; !ok {
			t.Errorf("ArbitrationRequest JSON missing key %q: %s", key, reqJSON)
		}
	}

	cands, ok := reqMap["candidates"].([]any)
	if !ok || len(cands) != 1 {
		t.Fatalf("candidates = %#v, want one candidate", reqMap["candidates"])
	}
	cand, ok := cands[0].(map[string]any)
	if !ok {
		t.Fatalf("candidate payload = %#v", cands[0])
	}
	for _, key := range []string{
		"target_id", "title", "artist", "duration_ms", "confidence_score",
		"title_score", "artist_score", "duration_score", "isrc_matched",
	} {
		if _, ok := cand[key]; !ok {
			t.Errorf("CandidateOption JSON missing key %q: %s", key, reqJSON)
		}
	}

	// selected_target_id carries omitempty: absent when empty, present when set.
	dec := &bridge.ArbitrationDecision{
		TrackID:   "spotify:track:abc",
		Action:    bridge.ActionSkip,
		DecidedAt: time.Date(2025, 1, 2, 3, 4, 6, 0, time.UTC),
	}
	decJSON, err := json.Marshal(dec)
	if err != nil {
		t.Fatalf("marshal ArbitrationDecision: %v", err)
	}
	var decMap map[string]any
	if err := json.Unmarshal(decJSON, &decMap); err != nil {
		t.Fatalf("unmarshal ArbitrationDecision: %v", err)
	}
	for _, key := range []string{"track_id", "action", "decided_at"} {
		if _, ok := decMap[key]; !ok {
			t.Errorf("ArbitrationDecision JSON missing key %q: %s", key, decJSON)
		}
	}
	if _, ok := decMap["selected_target_id"]; ok {
		t.Errorf("selected_target_id present while empty (omitempty): %s", decJSON)
	}

	dec.SelectedTargetID = "yt:abc"
	decJSON, err = json.Marshal(dec)
	if err != nil {
		t.Fatalf("marshal ArbitrationDecision: %v", err)
	}
	decMap = nil
	if err := json.Unmarshal(decJSON, &decMap); err != nil {
		t.Fatalf("unmarshal ArbitrationDecision: %v", err)
	}
	if _, ok := decMap["selected_target_id"]; !ok {
		t.Errorf("selected_target_id missing when set: %s", decJSON)
	}
}
