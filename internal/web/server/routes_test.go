package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

const testToken = "test-session-token-64-characters-abcdef0123456789"

// buildSPAHandlerStack builds the full middleware stack (Loopback/Origin ->
// Bearer -> SPA mux) over the given static FS, with actualPort set so Origin
// checks pass when the Origin header matches the loopback server.
func buildSPAHandlerStack(t *testing.T, staticFS fs.FS) http.Handler {
	t.Helper()
	srv, err := NewWebServer(Config{
		PreferredHost: "127.0.0.1",
		PreferredPort: 0,
		SessionToken:  testToken,
	}, staticFS)
	if err != nil {
		t.Fatalf("NewWebServer: %v", err)
	}
	srv.actualPort = 3080
	return srv.LoopbackAndOriginMiddleware(srv.BearerAuthMiddleware(srv.buildMux()))
}

// authorizedRequest builds a request with the bearer token and loopback Host.
func authorizedRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3080"+target, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	return req
}

func TestSPAFallbackServesIndex(t *testing.T) {
	staticFS := fstest.MapFS{
		"index.html":     {Data: []byte("<html>cockpit index</html>")},
		"assets/app.js":  {Data: []byte("console.log('app')")},
		"assets/app.css": {Data: []byte("body{}")},
	}
	handler := buildSPAHandlerStack(t, staticFS)

	tests := []struct {
		name     string
		target   string
		wantBody string
	}{
		{name: "root serves index", target: "/", wantBody: "<html>cockpit index</html>"},
		{name: "client route falls back to index", target: "/playlists/123", wantBody: "<html>cockpit index</html>"},
		{name: "deep client route falls back", target: "/reports/abc/export", wantBody: "<html>cockpit index</html>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := authorizedRequest(t, tt.target)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
			}
			if rec.Body.String() != tt.wantBody {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestSPAServesStaticFiles(t *testing.T) {
	staticFS := fstest.MapFS{
		"index.html":    {Data: []byte("<html>cockpit index</html>")},
		"assets/app.js": {Data: []byte("console.log('app')")},
	}
	handler := buildSPAHandlerStack(t, staticFS)

	tests := []struct {
		target   string
		wantBody string
	}{
		{target: "/assets/app.js", wantBody: "console.log('app')"},
		{target: "/index.html", wantBody: "<html>cockpit index</html>"},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			req := authorizedRequest(t, tt.target)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if rec.Body.String() != tt.wantBody {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

// TestSPAApiNamespaceNeverHitsStatic verifies /api/* paths are NOT answered by
// the SPA catch-all (they belong to the REST handlers, Task 2).
func TestSPAApiNamespaceNeverHitsStatic(t *testing.T) {
	staticFS := fstest.MapFS{
		"index.html": {Data: []byte("<html>cockpit index</html>")},
	}
	handler := buildSPAHandlerStack(t, staticFS)

	for _, target := range []string{"/api/v1/session", "/api/unknown", "/events"} {
		t.Run(target, func(t *testing.T) {
			req := authorizedRequest(t, target)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (must not hit static); body = %q", rec.Code, rec.Body.String())
			}
			if rec.Body.String() == "<html>cockpit index</html>" {
				t.Fatalf("SPA index.html leaked into %s", target)
			}
		})
	}
}

// TestSPARequiresAuth verifies the static handler sits behind bearer auth.
func TestSPARequiresAuth(t *testing.T) {
	staticFS := fstest.MapFS{
		"index.html": {Data: []byte("<html>cockpit index</html>")},
	}
	handler := buildSPAHandlerStack(t, staticFS)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3080/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without token", rec.Code)
	}
}
