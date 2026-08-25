package bridge_test

import (
	"playlistsync/internal/web/bridge"
	"sync"
	"testing"
)

func TestRingBufferWriteReadSince(t *testing.T) {
	rb := bridge.NewSSEEventRingBuffer(0) // defaults to 1024
	for i := 1; i <= 5; i++ {
		stamped := rb.Write(bridge.Event{Type: "LOG_STREAM", Data: i})
		if stamped.ID != uint64(i) {
			t.Fatalf("write %d stamped id = %d, want %d", i, stamped.ID, i)
		}
	}
	if got := rb.LastID(); got != 5 {
		t.Fatalf("LastID = %d, want 5", got)
	}

	// Fresh client: full replay, no gap.
	events, gap := rb.ReadSince(0)
	if gap {
		t.Fatal("ReadSince(0) reported a gap on a small history")
	}
	if len(events) != 5 || events[0].ID != 1 || events[4].ID != 5 {
		t.Fatalf("ReadSince(0) = %+v", events)
	}

	// Incremental replay.
	events, gap = rb.ReadSince(3)
	if gap || len(events) != 2 || events[0].ID != 4 || events[1].ID != 5 {
		t.Fatalf("ReadSince(3) = %+v, gap = %v", events, gap)
	}

	// Requesting the current id returns nothing.
	events, gap = rb.ReadSince(5)
	if gap || len(events) != 0 {
		t.Fatalf("ReadSince(5) = %+v, gap = %v", events, gap)
	}

	// A client ahead of the buffer returns nothing.
	events, gap = rb.ReadSince(99)
	if gap || len(events) != 0 {
		t.Fatalf("ReadSince(99) = %+v, gap = %v", events, gap)
	}
}

func TestRingBufferOverwriteGap(t *testing.T) {
	rb := bridge.NewSSEEventRingBuffer(4) // small capacity to force overwrites
	for i := 1; i <= 7; i++ {
		rb.Write(bridge.Event{Type: "DIFF_PROGRESS", Data: i})
	}
	if got := rb.LastID(); got != 7 {
		t.Fatalf("LastID = %d, want 7", got)
	}

	// Fresh client after overwrite: gap + best-effort retained tail (ids 4..7).
	events, gap := rb.ReadSince(0)
	if !gap {
		t.Fatal("fresh client after overwrite: expected gap")
	}
	if len(events) != 4 || events[0].ID != 4 || events[3].ID != 7 {
		t.Fatalf("gap replay = %+v", events)
	}

	// lastID=2: delta 5 > capacity 4 -> gap.
	if events, gap := rb.ReadSince(2); !gap || len(events) != 4 {
		t.Fatalf("ReadSince(2) = %+v, gap = %v, want 4 events + gap", events, gap)
	}

	// lastID=3: delta 4 == capacity -> no gap, complete replay of 4..7.
	events, gap = rb.ReadSince(3)
	if gap {
		t.Fatal("ReadSince(3) should not report a gap when delta == capacity")
	}
	if len(events) != 4 || events[0].ID != 4 || events[3].ID != 7 {
		t.Fatalf("ReadSince(3) = %+v", events)
	}

	// lastID=6: only event 7.
	if events, gap := rb.ReadSince(6); gap || len(events) != 1 || events[0].ID != 7 {
		t.Fatalf("ReadSince(6) = %+v, gap = %v", events, gap)
	}

	// lastID=7: nothing new.
	if events, gap := rb.ReadSince(7); gap || len(events) != 0 {
		t.Fatalf("ReadSince(7) = %+v, gap = %v", events, gap)
	}
}

func TestRingBufferDefaultCapacityBoundary(t *testing.T) {
	rb := bridge.NewSSEEventRingBuffer(0) // default 1024
	const total = 1025
	for i := 1; i <= total; i++ {
		rb.Write(bridge.Event{Type: "LOG_STREAM", Data: i})
	}
	// Oldest retained id is 2 (ids 2..1025).

	// Fresh client: delta 1025 > 1024 -> gap with 1024 retained events.
	events, gap := rb.ReadSince(0)
	if !gap {
		t.Fatal("expected gap for a fresh client after 1025 writes")
	}
	if len(events) != 1024 || events[0].ID != 2 || events[1023].ID != 1025 {
		t.Fatalf("boundary replay: len=%d first=%d last=%d", len(events), events[0].ID, events[1023].ID)
	}

	// lastID=1: delta 1024 == capacity -> no gap, exactly 1024 events (2..1025).
	events, gap = rb.ReadSince(1)
	if gap {
		t.Fatal("unexpected gap when delta == capacity")
	}
	if len(events) != 1024 || events[0].ID != 2 || events[1023].ID != 1025 {
		t.Fatalf("delta==capacity replay: len=%d first=%d last=%d", len(events), events[0].ID, events[1023].ID)
	}
}

func TestRingBufferConcurrentWrites(t *testing.T) {
	rb := bridge.NewSSEEventRingBuffer(1024)
	const workers = 8
	const perWorker = 64 // 512 total, below capacity so no overwrite
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				rb.Write(bridge.Event{Type: "LOG_STREAM", Data: i})
			}
		}()
	}
	wg.Wait()

	total := workers * perWorker
	if got := rb.LastID(); got != uint64(total) {
		t.Fatalf("LastID = %d, want %d", got, total)
	}

	// Ids must be exactly 1..total with no duplicates or gaps.
	events, gap := rb.ReadSince(0)
	if gap {
		t.Fatal("unexpected gap on a non-overwritten buffer")
	}
	if len(events) != total {
		t.Fatalf("len = %d, want %d", len(events), total)
	}
	seen := make(map[uint64]bool, total)
	for _, ev := range events {
		if ev.ID < 1 || ev.ID > uint64(total) {
			t.Fatalf("out-of-range id %d", ev.ID)
		}
		if seen[ev.ID] {
			t.Fatalf("duplicate id %d", ev.ID)
		}
		seen[ev.ID] = true
	}
}

// TestRingBufferConcurrentReadWrite races writers against readers: ReadSince
// must always return a consistent, monotonically ordered view (RWMutex) while
// writes are in flight, and every assigned id must remain unique.
func TestRingBufferConcurrentReadWrite(t *testing.T) {
	rb := bridge.NewSSEEventRingBuffer(1024)
	const writers = 6
	const perWriter = 100 // 600 total, below capacity so nothing overwrites

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				rb.Write(bridge.Event{Type: "DIFF_PROGRESS", Data: w*perWriter + i})
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				events, gap := rb.ReadSince(0)
				if gap {
					t.Error("ReadSince reported a gap on a non-overwritten buffer")
					return
				}
				for j := 1; j < len(events); j++ {
					if events[j].ID <= events[j-1].ID {
						t.Errorf("ReadSince returned non-monotonic ids: %d then %d", events[j-1].ID, events[j].ID)
						return
					}
				}
			}
		}()
	}
	wg.Wait()

	total := writers * perWriter
	if got := rb.LastID(); got != uint64(total) {
		t.Fatalf("LastID = %d, want %d", got, total)
	}
	events, gap := rb.ReadSince(0)
	if gap || len(events) != total {
		t.Fatalf("final ReadSince: len=%d gap=%v, want %d events and no gap", len(events), gap, total)
	}
	for i, ev := range events {
		if ev.ID != uint64(i+1) {
			t.Fatalf("event %d has id %d, want %d", i, ev.ID, i+1)
		}
	}
}
