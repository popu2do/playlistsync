package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// GenerateSessionToken returns a 256-bit hex session token: 32 bytes of
// crypto/rand entropy hex-encoded (spec 02 §4.2 / spec 05 §2.2). It is only
// ever exposed through the terminal banner, never persisted.
func GenerateSessionToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// offlineCSP is the offline Content-Security-Policy header injected on every
// response (spec 02 §4.2, verbatim).
const offlineCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self' data:; object-src 'none'; base-uri 'self';"

// LoopbackAndOriginMiddleware enforces strict loopback-only Host headers (DNS
// rebinding defense) and cross-origin blocking, and injects the offline CSP
// header (spec 02 §4.2 verbatim, plus spec 05 §2.3 nosniff frame headers).
func (s *WebServer) LoopbackAndOriginMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 校验 Host 头 (DNS rebinding defense): loopback names only.
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "Forbidden: Invalid Host Header (Loopback Only)", http.StatusForbidden)
			return
		}

		// 校验 Origin 头 (若存在): must be the loopback server itself.
		origin := r.Header.Get("Origin")
		if origin != "" {
			expectedPrefix1 := fmt.Sprintf("http://127.0.0.1:%d", s.actualPort)
			expectedPrefix2 := fmt.Sprintf("http://localhost:%d", s.actualPort)
			if !strings.HasPrefix(origin, expectedPrefix1) && !strings.HasPrefix(origin, expectedPrefix2) {
				http.Error(w, "Forbidden: Cross-Origin Request Blocked", http.StatusForbidden)
				return
			}
		}

		// 注入离线脱机 CSP + no-sniff / frame defense (spec 02 §4.2 verbatim
		// CSP; spec 05 §2.3 adds X-Content-Type-Options / X-Frame-Options).
		w.Header().Set("Content-Security-Policy", offlineCSP)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// spotifyCallbackPath is exempted from the bearer-token gate (BLOCKER-1 fix):
// the Spotify OAuth redirect lands on this exact path and the browser never
// carries the session token. The endpoint is only reachable through the
// loopback server (remote attackers cannot reach 127.0.0.1), and CSRF on the
// callback is enforced by the PKCE `state` round-trip in the exchange handler
// (internal/web/handlers, RegisterAuthHandlers). It MUST match the path
// registered by handlers.RegisterAuthHandlers — the two are pinned together by
// tests.
const spotifyCallbackPath = "/api/v1/auth/spotify/callback"

// BearerAuthMiddleware authenticates every request with a constant-time token
// comparison, accepting the Authorization: Bearer header or the ?token= query
// parameter. On success it refreshes the idle watchdog (spec 02 §4.2 verbatim;
// spec 05 §2.2 constant-time requirement).
func (s *WebServer) BearerAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == spotifyCallbackPath {
			// OAuth redirects cannot carry the session token; bearer-exempt.
			// Loopback-only binding + Origin guard + PKCE state cover it.
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		token := ""
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		} else {
			token = r.URL.Query().Get("token")
		}

		if subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
			http.Error(w, "Unauthorized: Invalid or Missing Session Token", http.StatusUnauthorized)
			return
		}

		// 刷新看门狗活跃心跳: every authenticated request counts as activity.
		if s.watchdog != nil {
			s.watchdog.Kick()
		}
		next.ServeHTTP(w, r)
	})
}
