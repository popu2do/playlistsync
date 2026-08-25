package server

import (
	"sync"
	"sync/atomic"
	"time"
)

// WatchdogTimer is a lock-free idle watchdog (spec 05 §4.2): Kick() refreshes
// an atomic last-active timestamp; when the idle time exceeds the timeout the
// onTimeout callback fires exactly once. The server wires onTimeout to inject
// root-context cancellation, broadcast SYSTEM_SHUTDOWN and shut down
// gracefully (spec 02 §3.3).
type WatchdogTimer struct {
	timeout    time.Duration
	tick       time.Duration // poll interval; 1s in production (spec 05 §4.2)
	lastActive atomic.Int64  // UnixNano timestamp of last activity
	onTimeout  func()
	stopCh     chan struct{}
	stopOnce   sync.Once
}

// NewWatchdogTimer creates a started watchdog. A non-positive timeout defaults
// to defaultIdleTimeout (15 minutes, spec §3.3). onTimeout runs on the first
// idle expiry and must not block for longer than the shutdown budget.
func NewWatchdogTimer(timeout time.Duration, onTimeout func()) *WatchdogTimer {
	return newWatchdogTimer(timeout, time.Second, onTimeout)
}

// newWatchdogTimer allows tests to shrink the poll interval; production always
// uses the spec's 1-second tick.
func newWatchdogTimer(timeout, tick time.Duration, onTimeout func()) *WatchdogTimer {
	if timeout <= 0 {
		timeout = DefaultIdleTimeout
	}
	if tick <= 0 {
		tick = time.Second
	}
	w := &WatchdogTimer{
		timeout:   timeout,
		tick:      tick,
		onTimeout: onTimeout,
		stopCh:    make(chan struct{}),
	}
	w.Kick()
	go w.run()
	return w
}

// Kick refreshes the last-active timestamp (lock-free: a single atomic store).
func (w *WatchdogTimer) Kick() {
	w.lastActive.Store(time.Now().UnixNano())
}

// Stop halts the watchdog without firing onTimeout. Idempotent.
func (w *WatchdogTimer) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
}

// run polls at w.tick and fires onTimeout once when the idle time exceeds the
// timeout. The run loop exits after firing or on Stop.
func (w *WatchdogTimer) run() {
	ticker := time.NewTicker(w.tick)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			last := time.Unix(0, w.lastActive.Load())
			if time.Since(last) > w.timeout {
				if w.onTimeout != nil {
					w.onTimeout()
				}
				return
			}
		case <-w.stopCh:
			return
		}
	}
}
