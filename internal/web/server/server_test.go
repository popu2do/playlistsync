package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func testStaticFS() *fstest.MapFS {
	return &fstest.MapFS{
		"index.html":    {Data: []byte("<html>cockpit index</html>")},
		"assets/app.js": {Data: []byte("console.log('app')")},
	}
}

func TestNewWebServerValidation(t *testing.T) {
	staticFS := testStaticFS()

	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{name: "defaults to no token provided", cfg: Config{PreferredHost: "127.0.0.1"}},
		{name: "localhost host ok", cfg: Config{PreferredHost: "localhost"}},
		{name: "empty host ok (defaults to 127.0.0.1)", cfg: Config{}},
		{name: "invalid host rejected", cfg: Config{PreferredHost: "0.0.0.0"}, wantErr: "PreferredHost"},
		{name: "hostname rejected", cfg: Config{PreferredHost: "example.com"}, wantErr: "PreferredHost"},
		{name: "negative port rejected", cfg: Config{PreferredPort: -1}, wantErr: "PreferredPort"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := NewWebServer(tt.cfg, staticFS)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NewWebServer err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewWebServer: %v", err)
			}
			if srv.Token() == "" {
				t.Fatal("token not generated")
			}
			if srv.config.IdleTimeout != DefaultIdleTimeout {
				t.Fatalf("IdleTimeout = %v, want default %v", srv.config.IdleTimeout, DefaultIdleTimeout)
			}
		})
	}
}

func TestNewWebServerNilStaticFS(t *testing.T) {
	if _, err := NewWebServer(Config{}, nil); err == nil {
		t.Fatal("NewWebServer with nil staticFS should error")
	}
}

func TestNewWebServerCustomTokenKept(t *testing.T) {
	srv, err := NewWebServer(Config{PreferredHost: "127.0.0.1", SessionToken: "custom-token"}, testStaticFS())
	if err != nil {
		t.Fatalf("NewWebServer: %v", err)
	}
	if srv.Token() != "custom-token" {
		t.Fatalf("Token = %q, want custom-token", srv.Token())
	}
}

func TestGenerateSessionToken(t *testing.T) {
	tokens := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok, err := GenerateSessionToken()
		if err != nil {
			t.Fatalf("GenerateSessionToken: %v", err)
		}
		if len(tok) != 64 {
			t.Fatalf("token length = %d, want 64 hex chars (32 bytes)", len(tok))
		}
		if tokens[tok] {
			t.Fatalf("duplicate token: %s", tok)
		}
		tokens[tok] = true
	}
}

// TestStartReturnsBannerURL verifies Start returns the authorized URL with the
// token and the server actually serves the SPA through the full stack.
func TestStartReturnsBannerURL(t *testing.T) {
	srv, err := NewWebServer(Config{PreferredHost: "127.0.0.1"}, testStaticFS())
	if err != nil {
		t.Fatalf("NewWebServer: %v", err)
	}

	url, err := srv.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("url = %q, want http://127.0.0.1:PORT", url)
	}
	if !strings.Contains(url, "token="+srv.Token()) {
		t.Fatalf("url = %q, want token=%s", url, srv.Token())
	}
	if srv.ActualPort() <= 0 {
		t.Fatalf("ActualPort = %d, want > 0", srv.ActualPort())
	}

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body = %s", resp.StatusCode, body)
	}
	if string(body) != "<html>cockpit index</html>" {
		t.Fatalf("body = %q, want cockpit index", body)
	}
}

// TestShutdownIdempotentAndTrackWrite verifies Shutdown intercepts new writes
// and returns cleanly; TrackWrite is refused after shutdown.
func TestShutdownIdempotentAndTrackWrite(t *testing.T) {
	srv, err := NewWebServer(Config{PreferredHost: "127.0.0.1"}, testStaticFS())
	if err != nil {
		t.Fatalf("NewWebServer: %v", err)
	}

	done1, ok := srv.TrackWrite()
	if !ok {
		t.Fatal("TrackWrite refused before shutdown")
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		done1() // release the in-flight write so shutdown can complete
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// Second Shutdown is a no-op (same result).
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}

	if _, ok := srv.TrackWrite(); ok {
		t.Fatal("TrackWrite accepted after shutdown")
	}
	select {
	case <-srv.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done not closed after Shutdown")
	}
}

// TestShutdownReleasesWaitingWrites verifies Shutdown waits for an in-flight
// tracked write to complete (within the ctx budget).
func TestShutdownReleasesWaitingWrites(t *testing.T) {
	srv, err := NewWebServer(Config{PreferredHost: "127.0.0.1"}, testStaticFS())
	if err != nil {
		t.Fatalf("NewWebServer: %v", err)
	}
	done1, ok := srv.TrackWrite()
	if !ok {
		t.Fatal("TrackWrite refused")
	}
	start := time.Now()
	go func() {
		time.Sleep(150 * time.Millisecond)
		done1()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("Shutdown returned too fast (%v) without waiting for the write", elapsed)
	}
}

// TestWatchdogIdleShutdown verifies the end-to-end watchdog: with a short
// IdleTimeout the server broadcasts SYSTEM_SHUTDOWN and shuts down on its own.
func TestWatchdogIdleShutdown(t *testing.T) {
	staticFS := testStaticFS()
	srv, err := NewWebServer(Config{PreferredHost: "127.0.0.1", IdleTimeout: 120 * time.Millisecond}, staticFS)
	if err != nil {
		t.Fatalf("NewWebServer: %v", err)
	}
	// A subscriber records the SYSTEM_SHUTDOWN broadcast.
	ch, unsubscribe := srv.hub.Subscribe()
	defer unsubscribe()

	if _, err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-ch:
		// SYSTEM_SHUTDOWN received — good enough (hub stamps the event).
	case <-time.After(3 * time.Second):
		t.Fatal("no SYSTEM_SHUTDOWN broadcast after watchdog idle timeout")
	}

	select {
	case <-srv.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down after watchdog timeout")
	}
	if err := srv.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

// TestRootDoneLiveFromConstruction is the BLOCKER-2 regression: handlers
// capture cfg.RootCtx while wiring up — BEFORE Start(). If the root context
// were only created inside Start(), RootDone() would return a nil channel at
// capture time and SSE handlers' `<-rootDone` select would never fire, making
// http.Server.Shutdown stall its full 5s budget (context.DeadlineExceeded).
// The root context must exist from NewWebServer and be closed by Shutdown.
func TestRootDoneLiveFromConstruction(t *testing.T) {
	staticFS := testStaticFS()
	srv, err := NewWebServer(Config{PreferredHost: "127.0.0.1"}, staticFS)
	if err != nil {
		t.Fatalf("NewWebServer: %v", err)
	}

	// Before Start (the exact capture point Task-2 handlers use) the channel
	// must be non-nil.
	ch := srv.RootDone()
	if ch == nil {
		t.Fatal("RootDone() returned nil before Start — SSE handlers would never wake on shutdown")
	}
	// It must NOT be closed already.
	select {
	case <-ch:
		t.Fatal("RootDone() closed before Start")
	default:
	}

	if _, err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Shutdown must close it promptly (well under the 5s budget).
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case <-ch:
	default:
		t.Fatal("RootDone() not closed after Shutdown")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Shutdown took %v — SSE client would have stalled the drain until the 5s deadline", elapsed)
	}
}
