package server

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestWatchdogFiresOnTimeout verifies the shutdown callback is invoked after
// the idle timeout elapses with no activity.
func TestWatchdogFiresOnTimeout(t *testing.T) {
	var fired atomic.Int64
	wd := newWatchdogTimer(80*time.Millisecond, 10*time.Millisecond, func() {
		fired.Add(1)
	})
	defer wd.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("watchdog onTimeout not invoked within 2s")
}

// TestWatchdogKickExtends verifies that Kick() refreshes the deadline: with a
// short timeout, a kick shortly before expiry prevents the callback.
func TestWatchdogKickExtends(t *testing.T) {
	var fired atomic.Int64
	timeout := 300 * time.Millisecond
	wd := newWatchdogTimer(timeout, 10*time.Millisecond, func() {
		fired.Add(1)
	})
	defer wd.Stop()

	// Refresh every 100ms, well within the 300ms timeout, for 1 second total:
	// the watchdog must never fire while activity continues.
	end := time.Now().Add(time.Second)
	for time.Now().Before(end) {
		wd.Kick()
		time.Sleep(100 * time.Millisecond)
	}
	if fired.Load() != 0 {
		t.Fatalf("watchdog fired %d times despite continuous kicks", fired.Load())
	}

	// Stop kicking: it must fire within a couple of timeout windows.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("watchdog did not fire after kicking stopped")
}

// TestWatchdogStopPreventsFire verifies Stop() suppresses the callback.
func TestWatchdogStopPreventsFire(t *testing.T) {
	var fired atomic.Int64
	wd := newWatchdogTimer(30*time.Millisecond, 10*time.Millisecond, func() {
		fired.Add(1)
	})
	wd.Stop()
	time.Sleep(300 * time.Millisecond)
	if fired.Load() != 0 {
		t.Fatalf("watchdog fired after Stop: %d", fired.Load())
	}
}

// TestWatchdogFiresOnce verifies the callback runs at most once.
func TestWatchdogFiresOnce(t *testing.T) {
	var fired atomic.Int64
	newWatchdogTimer(50*time.Millisecond, 10*time.Millisecond, func() {
		fired.Add(1)
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if fired.Load() != 1 {
		t.Fatalf("watchdog fired %d times, want exactly once", fired.Load())
	}
}
