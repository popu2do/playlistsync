package server

import (
	"io/fs"
	"net/http"
	"strings"
)

// buildMux wires the route table: the SPA catch-all is registered at "/" and
// Task 2 handlers register their /api/v1/* patterns on s.Mux() before Start.
// Go's ServeMux gives more specific patterns (e.g. /api/v1/session) precedence
// over the "/" catch-all.
func (s *WebServer) buildMux() *http.ServeMux {
	mux := s.mux
	mux.Handle("/", s.spaHandler())
	return mux
}

// spaHandler serves the embedded SPA with a client-side-route fallback:
// exact static files are served directly (via FileServer); unknown non-API
// paths fall back to index.html served directly (FileServer 301-redirects any
// path ending in /index.html, which would break client-side routing);
// /api/* and /events never reach the static handler (they belong to the
// REST/SSE namespaces handled upstream).
func (s *WebServer) spaHandler() http.Handler {
	files := http.FileServer(http.FS(s.staticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "api" || strings.HasPrefix(path, "api/") || path == "events" || strings.HasPrefix(path, "events/") {
			http.NotFound(w, r)
			return
		}

		// Direct hit: serve the file as-is (avoid /index.html so FileServer's
		// canonical redirect to "./" does not interfere).
		if path != "" && path != "index.html" {
			if _, err := fs.Stat(s.staticFS, path); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}

		// SPA fallback: serve index.html content directly (client-side route).
		data, err := fs.ReadFile(s.staticFS, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})
}
