package server

import (
	"testing"
	"time"

	"playlistsync/internal/web/bridge"
)

// TestSSEHubBroadcastDelivers verifies a subscriber receives a broadcast event
// with the correct type and payload.
func TestSSEHubBroadcastDelivers(t *testing.T) {
	hub := NewSSEHub()
	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	hub.Broadcast("SYSTEM_SHUTDOWN", map[string]interface{}{"reason": "idle_timeout"})

	select {
	case ev := <-ch:
		if ev.Type != "SYSTEM_SHUTDOWN" {
			t.Fatalf("event type = %q, want SYSTEM_SHUTDOWN", ev.Type)
		}
		if ev.ID == 0 {
			t.Fatal("event ID not stamped")
		}
		if ev.Timestamp == 0 {
			t.Fatal("event Timestamp not stamped")
		}
		data, ok := ev.Data.(map[string]interface{})
		if !ok || data["reason"] != "idle_timeout" {
			t.Fatalf("event data = %#v, want reason=idle_timeout", ev.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive the broadcast within 1s")
	}
}

// TestSSEHubUnsubscribeStopsDelivery verifies an unsubscribed client stops
// receiving events.
func TestSSEHubUnsubscribeStopsDelivery(t *testing.T) {
	hub := NewSSEHub()
	ch, unsubscribe := hub.Subscribe()
	unsubscribe()

	hub.Broadcast("EVENT", nil)

	select {
	case ev := <-ch:
		t.Fatalf("unsubscribed client received event %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// expected: no delivery
	}
	if hub.ClientCount() != 0 {
		t.Fatalf("ClientCount = %d, want 0 after unsubscribe", hub.ClientCount())
	}
}

// TestSSEHubSlowClientDoesNotBlock verifies a full subscriber buffer is
// dropped non-blockingly (spec §3.2): a broadcast with a never-reading client
// must return immediately and not panic.
func TestSSEHubSlowClientDoesNotBlock(t *testing.T) {
	hub := NewSSEHub()
	// Subscribe without reading: the 64-slot buffer fills up.
	_, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	start := time.Now()
	for i := 0; i < 200; i++ {
		hub.Broadcast("NOISE", i)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("200 broadcasts to a slow client took %v, want non-blocking (<500ms)", elapsed)
	}
}

// TestSSEHubClientCount verifies the subscriber count tracks Subscribe/Unsubscribe.
func TestSSEHubClientCount(t *testing.T) {
	hub := NewSSEHub()
	_, unsub1 := hub.Subscribe()
	_, unsub2 := hub.Subscribe()
	if got := hub.ClientCount(); got != 2 {
		t.Fatalf("ClientCount = %d, want 2", got)
	}
	unsub1()
	if got := hub.ClientCount(); got != 1 {
		t.Fatalf("ClientCount = %d, want 1 after one unsubscribe", got)
	}
	unsub2()
	if got := hub.ClientCount(); got != 0 {
		t.Fatalf("ClientCount = %d, want 0", got)
	}
}

// TestSSEHubSatisfiesEventBroadcaster locks the wire seam used by the bridge.
func TestSSEHubSatisfiesEventBroadcaster(t *testing.T) {
	var _ bridge.EventBroadcaster = NewSSEHub()
	var _ bridge.EventBroadcaster = new(SSEHub)
}
