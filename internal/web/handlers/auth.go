package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"playlistsync/internal/auth"
)

// PKCEExchanger swaps a Spotify authorization code for OAuth tokens (spec 02
// §2.2 + plan GC #7: loopback callback -> code exchange -> encrypted token
// store). Handlers depend on this interface; cmd/web wires a real exchange,
// tests inject a mock.
type PKCEExchanger interface {
	// AuthorizeURL builds the Spotify OAuth authorize URL for the given
	// redirect URI (http://127.0.0.1:<port>/api/v1/auth/spotify/callback).
	// It generates and remembers the PKCE code_verifier + a CSRF state keyed
	// internally, so the subsequent ExchangeCode(code, state) can pair the
	// verifier and redirect_uri. Returns the URL and the state value.
	AuthorizeURL(redirectURI string) (authorizeURL, state string, err error)

	// ExchangeCode performs the PKCE code exchange. state must match a verifier
	// issued by AuthorizeURL (CSRF protection) or the exchange fails.
	ExchangeCode(ctx context.Context, code, state string) (*OAuthToken, error)
}

// ErrInvalidOAuthState is the sentinel error PKCE exchangers return when the
// callback's `state` does not match any pending authorization (never issued,
// expired, or already consumed). The callback handler maps it to 400 (client
// error) instead of 500 — Minor-15.
var ErrInvalidOAuthState = errors.New("invalid or expired OAuth state")

// OAuthToken is the sanitized result of a PKCE exchange. Token material is
// never logged and is only persisted by the TokenStore seam (encrypted at
// rest, zero-trace per spec 05 §1.5). User is a best-effort display name
// (Spotify /v1/me) captured at exchange time, stored with the token so the
// status check can surface it without extra network calls.
type OAuthToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	ExpiresIn    int       `json:"expires_in,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	User         string    `json:"user,omitempty"`
	IssuedAt     time.Time `json:"issued_at"`
}

// TokenStore persists OAuth tokens without ever writing plaintext credentials
// (spec 05 §2.4 + §1.5 zero-trace). The concrete implementation in cmd/web
// encrypts at rest; tests inject a fake.
type TokenStore interface {
	// SaveSpotifyToken persists a Spotify OAuth token securely.
	SaveSpotifyToken(tok *OAuthToken, authPath string) error
}

// AuthStatusDTO is the strictly-redacted auth state response (spec 05 §2.4).
type AuthStatusDTO struct {
	Spotify      SpotifyStatusDTO `json:"spotify"`
	YouTubeMusic YTStatusDTO      `json:"youtubeMusic"`
}

type SpotifyStatusDTO struct {
	Authenticated   bool   `json:"authenticated"`
	UserDisplayName string `json:"userDisplayName,omitempty"`
	TokenExpiresAt  string `json:"tokenExpiresAt,omitempty"` // RFC3339 when known
}

type YTStatusDTO struct {
	Authenticated bool   `json:"authenticated"`
	AccountName   string `json:"accountName,omitempty"`
	AuthType      string `json:"authType"` // "cookie" | "oauth"
}

// RegisterAuthHandlers mounts the auth endpoints (spec 02 §2.2):
//
//	GET  /api/v1/auth/status             sanitized Spotify/YTM auth state
//	POST /api/v1/auth/cdp/start          launch CDP login; logs stream via SSE
//	GET  /api/v1/auth/spotify/authorize  build the Spotify OAuth (PKCE) URL
//	GET  /api/v1/auth/spotify/callback   PKCE callback (loopback capture endpoint)
func RegisterAuthHandlers(mux *http.ServeMux, cfg HandlerConfig) {
	mux.HandleFunc("GET /api/v1/auth/status", authStatus(cfg))
	mux.HandleFunc("POST /api/v1/auth/cdp/start", authCDPStart(cfg))
	mux.HandleFunc("GET /api/v1/auth/spotify/authorize", authSpotifyAuthorize(cfg))
	mux.HandleFunc("GET /api/v1/auth/spotify/callback", authSpotifyCallback(cfg))
}

// spotifyRedirectURI computes the loopback redirect URI for the PKCE flow.
// It is always http://127.0.0.1:<bound-port>/api/v1/auth/spotify/callback —
// the exact path the server exempts from the bearer gate (server package).
func spotifyRedirectURI(cfg HandlerConfig) string {
	port := 0
	if cfg.Port != nil {
		port = cfg.Port()
	}
	return fmt.Sprintf("http://127.0.0.1:%d/api/v1/auth/spotify/callback", port)
}

// authSpotifyAuthorize (GET /auth/spotify/authorize) starts the PKCE flow:
// it asks the PKCEExchanger to build the Spotify OAuth URL (which internally
// pairs a fresh code_verifier + CSRF state), and returns the URL + state so
// the frontend can open it in a browser. The callback (exempt from the bearer
// gate) later completes the exchange with the state round-trip.
func authSpotifyAuthorize(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.PKCE == nil {
			writeErrorJSON(w, http.StatusServiceUnavailable, "PKCE exchanger not configured")
			return
		}
		redirectURI := spotifyRedirectURI(cfg)
		authorizeURL, state, err := cfg.PKCE.AuthorizeURL(redirectURI)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "authorize URL generation failed: "+sanitize(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"authorize_url": authorizeURL,
			"state":         state,
			"redirect_uri":  redirectURI,
		})
	}
}

func authStatus(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dto := AuthStatusDTO{
			Spotify:      SpotifyStatusDTO{},
			YouTubeMusic: YTStatusDTO{AuthType: "cookie"},
		}

		if cfg.CheckAuth != nil {
			if st, err := cfg.CheckAuth(auth.PlatformSpotify, cfg.SpotifyAuthPath, cfg.ProxyURL); err == nil && st != nil && st.Authenticated {
				dto.Spotify.Authenticated = true
				dto.Spotify.UserDisplayName = st.User
			}
			if st, err := cfg.CheckAuth(auth.PlatformYouTube, cfg.YTMAuthPath, cfg.ProxyURL); err == nil && st != nil && st.Authenticated {
				dto.YouTubeMusic.Authenticated = true
				dto.YouTubeMusic.AccountName = st.User
			}
		}
		writeJSON(w, http.StatusOK, dto)
	}
}

// cdpStartRequest is the POST /auth/cdp/start body (spec 02 §2.2).
type cdpStartRequest struct {
	Platform string `json:"platform"`           // "spotify" | "youtube_music"
	Headless bool   `json:"headless,omitempty"` // accepted, may be ignored by driver
	ProxyURL string `json:"proxy,omitempty"`
}

func authCDPStart(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body cdpStartRequest
		if err := readJSON(r, &body); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		platform := auth.NormalizePlatform(body.Platform)
		if platform != auth.PlatformSpotify && platform != auth.PlatformYouTube {
			writeErrorJSON(w, http.StatusBadRequest, "unsupported platform: "+body.Platform)
			return
		}
		if cfg.CDPLogin == nil {
			writeErrorJSON(w, http.StatusServiceUnavailable, "CDP login driver not configured")
			return
		}

		proxy := body.ProxyURL
		if proxy == "" {
			proxy = cfg.ProxyURL
		}

		var targetURL, cookieName, savePath string
		switch platform {
		case auth.PlatformSpotify:
			targetURL = "https://accounts.spotify.com/login?continue=https%3A%2F%2Fopen.spotify.com%2F"
			cookieName = "sp_dc"
			savePath = cfg.SpotifyAuthPath
		default:
			targetURL = "https://accounts.google.com/ServiceLogin?service=youtube&continue=https%3A%2F%2Fmusic.youtube.com%2F"
			cookieName = "SAPISID"
			savePath = cfg.YTMAuthPath
		}

		// Launch the CDP login in the background; progress is streamed to SSE
		// clients via LOG_STREAM events (spec 02 §2.2). The request returns
		// 202 immediately.
		//
		// Major-4 fix: the login context is tied to the server RootCtx so a
		// graceful shutdown cancels the CDP session, which aborts the browser
		// loop and triggers closeBrowserGracefully — no zombie browser (spec
		// 05 §4, invariant 5 zero-trace). The watcher goroutine selects on
		// RootCtx.Done() and the login context; it exits without leaking once
		// the login completes (ctx.Done fires via defer cancel).
		go func() {
			broadcastLog(cfg, "auth", "starting CDP login", map[string]interface{}{
				"platform": string(platform), "headless": body.Headless,
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cfg.RootCtx != nil {
				go func() {
					select {
					case <-cfg.RootCtx:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			if err := cfg.CDPLogin(ctx, targetURL, savePath, cookieName, proxy); err != nil {
				broadcastLog(cfg, "auth", "CDP login failed", map[string]interface{}{
					"platform": string(platform), "error": sanitize(err.Error()),
				})
				return
			}
			broadcastLog(cfg, "auth", "CDP login complete", map[string]interface{}{
				"platform": string(platform),
			})
		}()

		writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"status":   "STARTED",
			"platform": string(platform),
			"stream":   "LOG_STREAM",
		})
	}
}

// broadcastLog emits one LOG_STREAM SSE event (spec 02 §3.1 event #4).
func broadcastLog(cfg HandlerConfig, module, text string, attrs map[string]interface{}) {
	if cfg.Broadcaster == nil {
		return
	}
	payload := map[string]interface{}{
		"level":     "info",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"module":    module,
		"text":      text,
	}
	for k, v := range attrs {
		payload[k] = v
	}
	cfg.Broadcaster.Broadcast("LOG_STREAM", payload)
}

// The PKCE callback is a local loopback capture endpoint (plan GC #7): Spotify
// redirects the browser here after the user approves; the handler exchanges
// the code for tokens and persists them encrypted. Non-loopback exposure is
// impossible because the server only binds 127.0.0.1.
func authSpotifyCallback(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errParam := q.Get("error"); errParam != "" {
			desc := q.Get("error_description")
			if desc == "" {
				desc = errParam
			}
			writeErrorJSON(w, http.StatusBadRequest, "authorization failed: "+sanitize(desc))
			return
		}
		code := q.Get("code")
		if code == "" {
			writeErrorJSON(w, http.StatusBadRequest, "missing authorization code")
			return
		}
		if cfg.PKCE == nil {
			writeErrorJSON(w, http.StatusServiceUnavailable, "PKCE exchange not configured")
			return
		}
		tok, err := cfg.PKCE.ExchangeCode(r.Context(), code, q.Get("state"))
		if err != nil {
			// Minor-15: an unknown/expired/replayed state is a client-side
			// CSRF failure -> 400, not a server-side exchange failure -> 500.
			if errors.Is(err, ErrInvalidOAuthState) {
				writeErrorJSON(w, http.StatusBadRequest, "token exchange failed: "+sanitize(err.Error()))
				return
			}
			writeErrorJSON(w, http.StatusInternalServerError, "token exchange failed: "+sanitize(err.Error()))
			return
		}
		if cfg.TokenStore != nil {
			if err := cfg.TokenStore.SaveSpotifyToken(tok, cfg.SpotifyAuthPath); err != nil {
				writeErrorJSON(w, http.StatusInternalServerError, "token storage failed: "+sanitize(err.Error()))
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":   "authenticated",
			"platform": "spotify",
		})
	}
}
