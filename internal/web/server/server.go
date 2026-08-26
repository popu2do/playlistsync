// Package server implements the embedded, loopback-only HTTP server for the
// web cockpit: security middleware stack (Host/Origin guard, constant-time
// bearer token, offline CSP), idle watchdog, SSE broadcast hub and graceful
// signal-driven shutdown. Interface contracts follow spec 02 §4.2 + §3.3 and
// spec 05 §2 (zero-trust loopback sandbox).
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"playlistsync/internal/web/bridge"
)

// Defaults per spec §3.3 / §4.2.
const (
	// DefaultPort is the preferred port; ListenWithRetry falls back within
	// DefaultPort..DefaultPort+9 when it is occupied (EADDRINUSE).
	DefaultPort = 3080
	// DefaultIdleTimeout is the watchdog idle timeout (15 minutes, spec §3.3).
	DefaultIdleTimeout = 15 * time.Minute
	// shutdownTimeout caps waiting for in-flight atomic writes and graceful
	// HTTP drain during shutdown (spec §3.3: force exit after 5s).
	shutdownTimeout = 5 * time.Second
)

// Config is the runtime configuration for the web cockpit server (spec §4.2).
// It is scoped to the server package: persistent/global CLI configuration
// lives in internal/config.
type Config struct {
	PreferredHost string        // strictly "127.0.0.1" or "localhost"; "" -> "127.0.0.1"
	PreferredPort int           // 0 -> kernel auto-assigns; >0 -> tried, then retried within port range
	SessionToken  string        // empty -> auto-generated 32-byte hex token
	IdleTimeout   time.Duration // watchdog idle timeout; <=0 -> 15 minutes

	// EventSink is an optional client-facing event broadcaster that receives
	// the SYSTEM_SHUTDOWN event whenever this server shuts down — via watchdog
	// timeout, OS signal, or the cockpit's shutdown endpoint. cmd/web wires
	// the handlers-side RecordingBroadcaster (EventStreamer) here so SSE
	// clients see the shutdown event on the real client bus, not just the
	// server-internal hub (plan QC qc3-M2).
	EventSink bridge.EventBroadcaster
}

// WebServer is the loopback-only HTTP server (spec §4.2 struct): config /
// listener / httpServer / actualPort / token / watchdog, plus an SSE broadcast
// hub (EventBroadcaster implementation) and shutdown lifecycle fields.
type WebServer struct {
	config     Config
	listener   net.Listener
	httpServer *http.Server
	actualPort int
	token      string
	watchdog   *WatchdogTimer

	hub *SSEHub // implements bridge.EventBroadcaster

	mux      *http.ServeMux // route table; handlers register before Start
	staticFS fs.FS          // SPA assets, rooted at dist/

	rootCtx context.Context
	cancel  context.CancelFunc
	sigCh   chan os.Signal
	doneCh  chan struct{}
	once    sync.Once
	// shutdownErr records the serve/shutdown result for Shutdown/Wait callers.
	shutdownErr error

	// writeMu serializes TrackWrite against shutdown; writeWG tracks in-flight
	// atomic file writes; shuttingDown intercepts new writes during shutdown.
	writeMu      sync.Mutex
	writeWG      sync.WaitGroup
	shuttingDown atomic.Bool
}

// NewWebServer validates Config and builds a WebServer ready for Start.
// staticFS is rooted at the SPA dist/ directory (see internal/web/static);
// it must not be nil.
func NewWebServer(cfg Config, staticFS fs.FS) (*WebServer, error) {
	if staticFS == nil {
		return nil, errors.New("server: staticFS must not be nil")
	}
	host := cfg.PreferredHost
	if host == "" {
		host = "127.0.0.1"
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return nil, fmt.Errorf("server: PreferredHost %q is not loopback (127.0.0.1/localhost only)", host)
	}
	if cfg.PreferredPort < 0 {
		return nil, errors.New("server: PreferredPort must be >= 0")
	}
	token := cfg.SessionToken
	if token == "" {
		var err error
		token, err = GenerateSessionToken()
		if err != nil {
			return nil, fmt.Errorf("server: generate session token: %w", err)
		}
	}
	idle := cfg.IdleTimeout
	if idle <= 0 {
		idle = DefaultIdleTimeout
	}
	// The root context is created here — at construction, not at Start — so
	// RootDone() is a live (non-nil) channel from the moment the WebServer
	// exists. Task-2 handlers capture cfg.RootCtx while wiring up (before
	// Start); a channel created only inside Start would be nil at capture time
	// and their `<-rootDone` select would never fire, stalling http.Server.
	// Shutdown is the only place the root context gets canceled.
	rootCtx, cancel := context.WithCancel(context.Background())
	return &WebServer{
		config: Config{
			PreferredHost: host,
			PreferredPort: cfg.PreferredPort,
			SessionToken:  token,
			IdleTimeout:   idle,
			EventSink:     cfg.EventSink,
		},
		token:    token,
		hub:      NewSSEHub(),
		mux:      http.NewServeMux(),
		doneCh:   make(chan struct{}),
		staticFS: staticFS,
		rootCtx:  rootCtx,
		cancel:   cancel,
	}, nil
}

// Token returns the 256-bit hex session token (banner only, never persisted).
func (s *WebServer) Token() string { return s.token }

// ActualPort returns the bound port (valid after Start).
func (s *WebServer) ActualPort() int { return s.actualPort }

// Mux exposes the route table so handlers (internal/web/handlers) register
// /api/v1/* endpoints before Start.
func (s *WebServer) Mux() *http.ServeMux { return s.mux }

// Done is closed once the server has fully shut down.
func (s *WebServer) Done() <-chan struct{} { return s.doneCh }

// RootDone returns a channel closed when the server's root context is canceled
// — the moment graceful shutdown begins (watchdog timeout, signal, or POST
// /session/shutdown), before in-flight HTTP connections are drained. SSE
// handlers (internal/web/handlers, plan-wc-02 Task 2) select on this channel so
// they release their connections promptly and http.Server.Shutdown does not
// burn its 5s budget waiting for them (Task-1 review INFO-3).
//
// The channel is created at NewWebServer (never nil), so handlers may capture
// it before Start() and select on it safely; it is closed (via cancel) only
// during Shutdown.
func (s *WebServer) RootDone() <-chan struct{} {
	return s.rootCtx.Done()
}

// Kick refreshes the idle watchdog's last-active timestamp. The bearer auth
// middleware already kicks on every authenticated request; this accessor lets
// non-route server consumers (e.g. the heartbeat handler) refresh it too.
func (s *WebServer) Kick() {
	if s.watchdog != nil {
		s.watchdog.Kick()
	}
}

// Broadcast implements bridge.EventBroadcaster, fanning one SSE event out to
// all connected clients through the hub.
func (s *WebServer) Broadcast(eventType string, data interface{}) {
	s.hub.Broadcast(eventType, data)
}

var _ bridge.EventBroadcaster = (*WebServer)(nil)

// TrackWrite registers one in-flight atomic file write; the returned function
// must be called exactly once when the write completes. ok=false means the
// server is already shutting down and new writes are intercepted (spec §3.3).
func (s *WebServer) TrackWrite() (done func(), ok bool) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.shuttingDown.Load() {
		return nil, false
	}
	s.writeWG.Add(1)
	return s.writeWG.Done, true
}

// waitForWrites intercepts new writes and waits (bounded by ctx) for in-flight
// atomic writes to complete, mirroring spec §3.3's 5s shut-down cap.
func (s *WebServer) waitForWrites(ctx context.Context) {
	s.writeMu.Lock()
	s.shuttingDown.Store(true)
	s.writeMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.writeWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Start binds the loopback listener (with port retry), wires the middleware
// stack, starts the idle watchdog and signal watcher, serves in a background
// goroutine and returns the authorized banner URL
// http://127.0.0.1:<port>/?token=<token>.
func (s *WebServer) Start() (actualURL string, err error) {
	if s.httpServer != nil || s.listener != nil {
		return "", errors.New("server: Start called twice")
	}
	ln, port, err := ListenWithRetry(s.config.PreferredPort)
	if err != nil {
		return "", err
	}
	s.listener = ln
	s.actualPort = port

	// Idle watchdog: on timeout, inject root-context cancellation, broadcast
	// SYSTEM_SHUTDOWN and shut down gracefully (spec §3.3 / spec 05 §4.2).
	s.watchdog = NewWatchdogTimer(s.config.IdleTimeout, func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = s.Shutdown(ctx)
	})

	// Middleware stack: Loopback/Origin guard -> Bearer auth -> routes.
	handler := s.LoopbackAndOriginMiddleware(s.BearerAuthMiddleware(s.buildMux()))

	s.httpServer = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Signal capture: SIGINT/SIGTERM -> SYSTEM_SHUTDOWN broadcast + graceful
	// shutdown waiting (bounded by shutdownTimeout) for atomic writes.
	s.sigCh = make(chan os.Signal, 1)
	signal.Notify(s.sigCh, os.Interrupt, syscall.SIGTERM)
	go s.watchSignals()

	go func() {
		err := s.httpServer.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Surface an unexpected serve failure through Shutdown/Wait.
			s.once.Do(func() {
				s.shutdownErr = err
				close(s.doneCh)
			})
		}
	}()

	return fmt.Sprintf("http://%s:%d/?token=%s", s.config.PreferredHost, port, s.token), nil
}

// watchSignals waits for SIGINT/SIGTERM, then shuts down gracefully. It exits
// when the root context is cancelled (e.g. watchdog-initiated shutdown).
func (s *WebServer) watchSignals() {
	defer signal.Stop(s.sigCh)
	select {
	case <-s.sigCh:
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = s.Shutdown(ctx)
	case <-s.rootCtx.Done():
	}
}

// Shutdown gracefully stops the server: broadcasts SYSTEM_SHUTDOWN (on the
// internal hub AND Config.EventSink — the client event bus when wired),
// cancels the root context, stops the watchdog, intercepts new writes and
// waits (bounded by ctx) for in-flight atomic writes, then drains the HTTP
// server. Safe for concurrent use; only the first call performs the shutdown.
func (s *WebServer) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.once.Do(func() {
		if s.hub != nil {
			s.hub.Broadcast("SYSTEM_SHUTDOWN", map[string]interface{}{"reason": "shutdown"})
		}
		// qc3-M2: surface the shutdown event on the client-facing event bus
		// (Config.EventSink, the handlers RecordingBroadcaster in cmd/web) so
		// every SSE client observes SYSTEM_SHUTDOWN regardless of what
		// triggered the shutdown (watchdog, signal, or POST /session/shutdown
		// — the latter no longer broadcasts in the handler; the server is the
		// single source).
		if s.config.EventSink != nil {
			s.config.EventSink.Broadcast("SYSTEM_SHUTDOWN", map[string]interface{}{"reason": "shutdown"})
		}
		// qc2 MAJOR-1: give connected SSE clients a short window to flush the
		// SYSTEM_SHUTDOWN frame from their TCP buffers before the listener /
		// HTTP drain tears the connection down. Without this, a client that
		// just received the broadcast could miss it when Shutdown closes the
		// socket immediately (the E2E watchdog test depends on the browser
		// observing the event).
		time.Sleep(150 * time.Millisecond)
		if s.cancel != nil {
			s.cancel()
		}
		if s.watchdog != nil {
			s.watchdog.Stop()
		}
		s.waitForWrites(ctx)
		if s.httpServer != nil {
			s.shutdownErr = s.httpServer.Shutdown(ctx)
		}
		if s.listener != nil {
			_ = s.listener.Close()
		}
		close(s.doneCh)
	})
	return s.shutdownErr
}

// Wait blocks until the server has shut down and returns the serve/shutdown
// error (nil on a clean shutdown).
func (s *WebServer) Wait() error {
	<-s.doneCh
	return s.shutdownErr
}
