package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServer builds a WebServer suitable for middleware unit tests without
// starting the listener: fixed token and actualPort, so Host/Origin checks see
// a bound port.
func newTestServer(t *testing.T) *WebServer {
	t.Helper()
	return &WebServer{
		token:      "test-session-token-64-characters-abcdef0123456789",
		actualPort: 3080,
		mux:        http.NewServeMux(),
		doneCh:     make(chan struct{}),
		hub:        NewSSEHub(),
	}
}

// okHandler writes 200 with a marker body when reached.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestBearerAuthMiddleware(t *testing.T) {
	srv := newTestServer(t)
	handler := srv.BearerAuthMiddleware(okHandler())

	tests := []struct {
		name    string
		header  string
		query   string
		want    int
		allowed bool
	}{
		{name: "valid bearer header", header: "Bearer test-session-token-64-characters-abcdef0123456789", want: http.StatusOK, allowed: true},
		{name: "valid query token", query: "token=test-session-token-64-characters-abcdef0123456789", want: http.StatusOK, allowed: true},
		{name: "missing credentials", want: http.StatusUnauthorized},
		{name: "wrong bearer token", header: "Bearer wrong-token", want: http.StatusUnauthorized},
		{name: "wrong query token", query: "token=wrong-token", want: http.StatusUnauthorized},
		{name: "malformed auth header", header: "Basic abc123", want: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3080/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			if tt.query != "" {
				req.URL.RawQuery = tt.query
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestLoopbackAndOriginMiddlewareHost(t *testing.T) {
	srv := newTestServer(t)
	handler := srv.LoopbackAndOriginMiddleware(okHandler())

	tests := []struct {
		name string
		host string
		want int
	}{
		{name: "loopback ipv4 with port", host: "127.0.0.1:3080", want: http.StatusOK},
		{name: "localhost with port", host: "localhost:3080", want: http.StatusOK},
		{name: "loopback ipv6", host: "[::1]:3080", want: http.StatusOK},
		{name: "bare loopback ipv4", host: "127.0.0.1", want: http.StatusOK},
		{name: "bare localhost", host: "localhost", want: http.StatusOK},
		{name: "foreign host", host: "evil.example.com:3080", want: http.StatusForbidden},
		{name: "lan ip", host: "192.168.1.10:3080", want: http.StatusForbidden},
		{name: "bare evil host", host: "evil.example.com", want: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tt.host+"/", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("host %q: status = %d, want %d", tt.host, rec.Code, tt.want)
			}
		})
	}
}

func TestLoopbackAndOriginMiddlewareOrigin(t *testing.T) {
	srv := newTestServer(t)
	handler := srv.LoopbackAndOriginMiddleware(okHandler())

	tests := []struct {
		name   string
		origin string
		want   int
	}{
		{name: "same-loopback origin", origin: "http://127.0.0.1:3080", want: http.StatusOK},
		{name: "localhost origin", origin: "http://localhost:3080", want: http.StatusOK},
		{name: "no origin header", origin: "", want: http.StatusOK},
		{name: "cross-origin https", origin: "https://evil.example.com", want: http.StatusForbidden},
		{name: "cross-origin different port", origin: "http://127.0.0.1:9999", want: http.StatusForbidden},
		{name: "cross-origin scheme", origin: "https://127.0.0.1:3080", want: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3080/", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("origin %q: status = %d, want %d", tt.origin, rec.Code, tt.want)
			}
		})
	}
}

func TestLoopbackAndOriginMiddlewareSecurityHeaders(t *testing.T) {
	srv := newTestServer(t)
	handler := srv.LoopbackAndOriginMiddleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3080/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Security-Policy"); got != offlineCSP {
		t.Errorf("CSP = %q, want %q", got, offlineCSP)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
}

func TestBearerAuthCallbackExempt(t *testing.T) {
	// BLOCKER-1(a): the Spotify OAuth redirect targets the callback without a
	// session token, so that exact path must be exempt from the bearer gate.
	// All other paths still require the token.
	srv := newTestServer(t)
	handler := srv.BearerAuthMiddleware(okHandler())

	tests := []struct {
		name string
		path string
		want int
	}{
		{"callback exempt without token", spotifyCallbackPath, http.StatusOK},
		{"callback exempt with state query", spotifyCallbackPath + "?code=abc&state=xyz", http.StatusOK},
		{"other path still requires auth", "/api/v1/session", http.StatusUnauthorized},
		{"authorize still requires auth", "/api/v1/auth/spotify/authorize", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3080"+tt.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("path %q: status = %d, want %d", tt.path, rec.Code, tt.want)
			}
		})
	}
}
