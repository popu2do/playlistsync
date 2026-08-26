package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"playlistsync/internal/auth"
)

func TestAuthStatusReflectsState(t *testing.T) {
	cfg := HandlerConfig{
		CheckAuth: func(platform auth.Platform, authPath, proxy string) (*auth.AuthStatus, error) {
			switch platform {
			case auth.PlatformSpotify:
				return &auth.AuthStatus{Platform: auth.PlatformSpotify, Authenticated: true, User: "alice"}, nil
			default:
				return &auth.AuthStatus{Platform: auth.PlatformYouTube, Authenticated: false}, nil
			}
		},
	}
	mux := testMux(t, cfg)

	tests := []struct {
		name   string
		method string
		target string
		want   int
	}{
		{"status ok", "GET", "/api/v1/auth/status", http.StatusOK},
		{"method not allowed", "POST", "/api/v1/auth/status", http.StatusMethodNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(t, mux, tc.method, tc.target, "")
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusOK {
				var dto AuthStatusDTO
				if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if !dto.Spotify.Authenticated || dto.Spotify.UserDisplayName != "alice" {
					t.Errorf("spotify state wrong: %+v", dto.Spotify)
				}
				if dto.YouTubeMusic.Authenticated {
					t.Errorf("youtube should be unauthenticated: %+v", dto.YouTubeMusic)
				}
				if dto.YouTubeMusic.AuthType != "cookie" {
					t.Errorf("ytm authType = %q, want cookie", dto.YouTubeMusic.AuthType)
				}
			}
		})
	}
}

func TestAuthStatusCheckAuthError(t *testing.T) {
	cfg := HandlerConfig{CheckAuth: func(auth.Platform, string, string) (*auth.AuthStatus, error) {
		return nil, errors.New("validation endpoint unreachable")
	}}
	mux := testMux(t, cfg)
	w := doReq(t, mux, "GET", "/api/v1/auth/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (auth errors degrade to unauthenticated)", w.Code)
	}
	var dto AuthStatusDTO
	_ = json.Unmarshal(w.Body.Bytes(), &dto)
	if dto.Spotify.Authenticated || dto.YouTubeMusic.Authenticated {
		t.Error("auth errors must degrade to unauthenticated state")
	}
}

// fakeCDPLogin is a hermetic CDP seam that records invocations. The calls run
// in separate goroutines (the handler launches them async), so the recording
// fields are guarded and the test asserts the SET of cookies used, not a
// "last call" race.
type fakeCDPLogin struct {
	mu        mutexForTest
	calls     int
	cookies   map[string]bool
	urls      map[string]bool
	lastProxy string
	err       error
}

func (f *fakeCDPLogin) fn() func(ctx context.Context, targetURL, savePath, cookieName, proxy string) error {
	return func(ctx context.Context, targetURL, savePath, cookieName, proxy string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		if f.cookies == nil {
			f.cookies = map[string]bool{}
		}
		f.cookies[cookieName] = true
		if f.urls == nil {
			f.urls = map[string]bool{}
		}
		f.urls[targetURL] = true
		f.lastProxy = proxy
		return f.err
	}
}

// mutexForTest is a tiny guard so the async CDP fakes stay race-free.
type mutexForTest struct {
	mu sync.Mutex
}

func (m *mutexForTest) Lock()   { m.mu.Lock() }
func (m *mutexForTest) Unlock() { m.mu.Unlock() }

// snapshot returns a consistent view of the recorded calls.
func (f *fakeCDPLogin) snapshot() (calls int, cookies, urls map[string]bool, lastProxy string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cookies = map[string]bool{}
	for k := range f.cookies {
		cookies[k] = true
	}
	urls = map[string]bool{}
	for k := range f.urls {
		urls[k] = true
	}
	return f.calls, cookies, urls, f.lastProxy
}

func TestAuthCDPStartStreamsLogEvents(t *testing.T) {
	rec := NewRecordingBroadcaster(nil)
	fake := &fakeCDPLogin{}
	cfg := HandlerConfig{
		Broadcaster:     rec,
		Ring:            rec.Ring(),
		SpotifyAuthPath: "output/auth/spotify_credentials.json",
		YTMAuthPath:     "output/auth/ytmusic_credentials.json",
		ProxyURL:        "http://127.0.0.1:7890",
		CDPLogin:        fake.fn(),
	}
	mux := testMux(t, cfg)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"youtube_music", `{"platform":"youtube_music","headless":true}`, http.StatusAccepted},
		{"spotify", `{"platform":"spotify"}`, http.StatusAccepted},
		{"bad platform", `{"platform":"netflix"}`, http.StatusBadRequest},
		{"bad body", `{nope`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(t, mux, "POST", "/api/v1/auth/cdp/start", tc.body)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}

	// The youtube_music and spotify calls complete asynchronously; wait for
	// the fake to be invoked and verify LOG_STREAM events were recorded.
	calls, cookies, urls, lastProxy := fake.snapshot()
	deadline := time.Now().Add(2 * time.Second)
	for calls < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		calls, cookies, urls, lastProxy = fake.snapshot()
	}
	if calls != 2 {
		t.Fatalf("CDPLogin invocations = %d, want 2", calls)
	}
	if !cookies["SAPISID"] {
		t.Errorf("youtube capture cookie SAPISID not used; cookies = %v", cookies)
	}
	if !cookies["sp_dc"] {
		t.Errorf("spotify capture cookie sp_dc not used; cookies = %v", cookies)
	}
	if len(urls) != 2 {
		t.Errorf("expected two distinct login target URLs, got %v", urls)
	}
	if lastProxy != "http://127.0.0.1:7890" {
		t.Errorf("proxy not propagated: %q", lastProxy)
	}

	var logTypes int
	events, _ := rec.Ring().ReadSince(0)
	for _, ev := range events {
		if ev.Type == "LOG_STREAM" {
			logTypes++
		}
	}
	if logTypes == 0 {
		t.Error("no LOG_STREAM events streamed to the SSES pipe")
	}
}

// fakePKCE implements PKCEExchanger with a configurable result.
type fakePKCE struct {
	tok             *OAuthToken
	err             error
	authorizeErr    error
	lastCode        string
	lastState       string
	lastRedirectURI string
	authURL         string // canned authorize URL returned on success
}

func (f *fakePKCE) AuthorizeURL(redirectURI string) (string, string, error) {
	f.lastRedirectURI = redirectURI
	if f.authorizeErr != nil {
		return "", "", f.authorizeErr
	}
	if f.authURL != "" {
		return f.authURL, "authz_state", nil
	}
	return "https://accounts.spotify.com/authorize?client_id=fake", "authz_state", nil
}

func (f *fakePKCE) ExchangeCode(ctx context.Context, code, state string) (*OAuthToken, error) {
	f.lastCode = code
	f.lastState = state
	return f.tok, f.err
}

type fakeTokenStore struct {
	saved *OAuthToken
	path  string
	err   error
}

func (f *fakeTokenStore) SaveSpotifyToken(tok *OAuthToken, authPath string) error {
	f.saved = tok
	f.path = authPath
	return f.err
}

func TestAuthSpotifyCallbackTokenExchange(t *testing.T) {
	tok := &OAuthToken{AccessToken: "secret-access-token-123", TokenType: "Bearer", IssuedAt: time.Now().UTC()}
	store := &fakeTokenStore{}
	pkce := &fakePKCE{tok: tok}
	cfg := HandlerConfig{
		PKCE:            pkce,
		TokenStore:      store,
		SpotifyAuthPath: "output/auth/spotify_credentials.json",
	}
	mux := testMux(t, cfg)

	tests := []struct {
		name   string
		target string
		want   int
	}{
		{"success", "/api/v1/auth/spotify/callback?code=abc123&state=xyz", http.StatusOK},
		{"missing code", "/api/v1/auth/spotify/callback?state=xyz", http.StatusBadRequest},
		{"authorization error", "/api/v1/auth/spotify/callback?error=access_denied&error_description=user%20denied", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(t, mux, "GET", tc.target, "")
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}

	if store.saved == nil {
		t.Fatal("token not persisted to store")
	}
	if store.saved.AccessToken != "secret-access-token-123" {
		t.Errorf("stored token mismatch: %+v", store.saved)
	}
	if pkce.lastCode != "abc123" || pkce.lastState != "xyz" {
		t.Errorf("PKCE seam got code=%q state=%q", pkce.lastCode, pkce.lastState)
	}
	if store.path != "output/auth/spotify_credentials.json" {
		t.Errorf("token store path = %q", store.path)
	}
}

func TestAuthSpotifyCallbackExchangeError(t *testing.T) {
	pkce := &fakePKCE{err: errors.New("invalid_grant: code expired access_token=abc123secret token_expiry=2025")}
	cfg := HandlerConfig{PKCE: pkce, TokenStore: &fakeTokenStore{}}
	mux := testMux(t, cfg)

	w := doReq(t, mux, "GET", "/api/v1/auth/spotify/callback?code=dead", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	// The error message must be sanitized (zero-trace): the embedded secret
	// pattern must not appear in the response.
	if body := w.Body.String(); strings.Contains(body, "access_token=abc123secret") {
		t.Errorf("response leaks credential material: %s", body)
	}
}

func TestAuthSpotifyCallbackStoreError(t *testing.T) {
	store := &fakeTokenStore{err: errors.New("disk full")}
	cfg := HandlerConfig{PKCE: &fakePKCE{tok: &OAuthToken{AccessToken: "tok"}}, TokenStore: store}
	mux := testMux(t, cfg)
	w := doReq(t, mux, "GET", "/api/v1/auth/spotify/callback?code=abc", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestAuthSpotifyCallbackInvalidState(t *testing.T) {
	// Minor-15: an unknown/expired/replayed OAuth state is a client-side CSRF
	// failure -> 400 (not 500). The production exchanger returns
	// ErrInvalidOAuthState for those cases.
	pkce := &fakePKCE{err: ErrInvalidOAuthState}
	cfg := HandlerConfig{PKCE: pkce}
	mux := testMux(t, cfg)

	w := doReq(t, mux, "GET", "/api/v1/auth/spotify/callback?code=abc&state=bogus", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
	}
	if pkce.lastState != "bogus" {
		t.Errorf("PKCE seam got state %q, want bogus", pkce.lastState)
	}
}

// TestAuthStatusFlipsAfterPKCECallback is the MAJOR-5 regression: a successful
// PKCE callback (mock exchange + store) MUST flip GET /auth/status to
// authenticated — end-to-end through the CheckAuth seam, which here consults
// the same store the callback wrote through.
func TestAuthStatusFlipsAfterPKCECallback(t *testing.T) {
	store := &fakeTokenStore{}
	pkce := &fakePKCE{tok: &OAuthToken{AccessToken: "flip-token", User: "PKCE User"}}
	cfg := HandlerConfig{
		PKCE:            pkce,
		TokenStore:      store,
		SpotifyAuthPath: "output/auth/spotify_credentials.json",
		CheckAuth: func(platform auth.Platform, authPath, proxy string) (*auth.AuthStatus, error) {
			if platform == auth.PlatformSpotify {
				// Mirror the production seam (web.go): the OAuth store the
				// callback wrote through is the source of truth.
				if store.saved != nil && store.saved.AccessToken != "" {
					return &auth.AuthStatus{
						Platform:      auth.PlatformSpotify,
						Authenticated: true,
						User:          store.saved.User,
						Path:          authPath,
					}, nil
				}
			}
			return &auth.AuthStatus{Platform: platform, Authenticated: false, Path: authPath}, nil
		},
	}
	mux := testMux(t, cfg)

	// Before the callback: unauthenticated.
	var pre AuthStatusDTO
	w := doReq(t, mux, "GET", "/api/v1/auth/status", "")
	if err := decodeJSON(t, w, &pre); err != nil {
		t.Fatalf("decode pre: %v", err)
	}
	if pre.Spotify.Authenticated {
		t.Fatal("spotify already authenticated before callback")
	}

	// Successful callback: exchange + store.
	w = doReq(t, mux, "GET", "/api/v1/auth/spotify/callback?code=flip&state=s1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("callback status = %d (body %s)", w.Code, w.Body.String())
	}
	if store.saved == nil {
		t.Fatal("callback did not persist the token")
	}

	// After the callback: authenticated with the stored display name.
	var post AuthStatusDTO
	w = doReq(t, mux, "GET", "/api/v1/auth/status", "")
	if err := decodeJSON(t, w, &post); err != nil {
		t.Fatalf("decode post: %v", err)
	}
	if !post.Spotify.Authenticated {
		t.Fatal("MAJOR-5: /auth/status did not flip to authenticated after PKCE callback")
	}
	if post.Spotify.UserDisplayName != "PKCE User" {
		t.Errorf("userDisplayName = %q, want PKCE User", post.Spotify.UserDisplayName)
	}
}

func TestAuthSpotifyAuthorizeBuildsURL(t *testing.T) {
	// BLOCKER-1(b): GET /auth/spotify/authorize must produce the Spotify OAuth
	// URL (via the PKCE seam) with the loopback redirect URI and CSRF state.
	pkce := &fakePKCE{authURL: "https://accounts.spotify.com/authorize?client_id=fake"}
	cfg := HandlerConfig{
		PKCE: pkce,
		Port: func() int { return 3080 },
	}
	mux := testMux(t, cfg)

	tests := []struct {
		name   string
		method string
		target string
		want   int
	}{
		{"authorize ok", "GET", "/api/v1/auth/spotify/authorize", http.StatusOK},
		{"method not allowed", "POST", "/api/v1/auth/spotify/authorize", http.StatusMethodNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(t, mux, tc.method, tc.target, "")
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}

	var resp struct {
		AuthorizeURL string `json:"authorize_url"`
		State        string `json:"state"`
		RedirectURI  string `json:"redirect_uri"`
	}
	w := doReq(t, mux, "GET", "/api/v1/auth/spotify/authorize", "")
	if err := decodeJSON(t, w, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(resp.AuthorizeURL, "https://accounts.spotify.com/authorize") {
		t.Errorf("authorize_url = %q", resp.AuthorizeURL)
	}
	if resp.State != "authz_state" {
		t.Errorf("state = %q, want authz_state from seam", resp.State)
	}
	if resp.RedirectURI != "http://127.0.0.1:3080/api/v1/auth/spotify/callback" {
		t.Errorf("redirect_uri = %q, want loopback callback with bound port", resp.RedirectURI)
	}
	if pkce.lastRedirectURI != "http://127.0.0.1:3080/api/v1/auth/spotify/callback" {
		t.Errorf("PKCE seam got redirect_uri = %q", pkce.lastRedirectURI)
	}
}

func TestAuthSpotifyAuthorizePKCEUnavailable(t *testing.T) {
	cfg := HandlerConfig{} // PKCE nil -> 503
	mux := testMux(t, cfg)
	w := doReq(t, mux, "GET", "/api/v1/auth/spotify/authorize", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", w.Code, w.Body.String())
	}
}

func TestAuthCDPStartRootCtxCancellation(t *testing.T) {
	// MAJOR-4 regression: the CDP login context must be cancelled when the
	// server RootCtx closes (graceful shutdown), so the browser is not left as
	// a zombie process (spec 05 §4, invariant 5).
	rootCtx := make(chan struct{})
	loginCtxCancelled := make(chan struct{}, 1)
	cdp := func(ctx context.Context, targetURL, savePath, cookieName, proxy string) error {
		// The real driver selects on ctx.Done() and cleans up the browser;
		// here we just wait for cancellation.
		<-ctx.Done()
		loginCtxCancelled <- struct{}{}
		return ctx.Err()
	}
	cfg := HandlerConfig{
		RootCtx:  rootCtx,
		CDPLogin: cdp,
	}
	mux := testMux(t, cfg)

	w := doReq(t, mux, "POST", "/api/v1/auth/cdp/start", `{"platform":"youtube_music"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", w.Code, w.Body.String())
	}

	// Closing the root context (shutdown) must cancel the login ctx.
	close(rootCtx)
	select {
	case <-loginCtxCancelled:
		// CDP login context cancelled: browser will be torn down by the driver.
	case <-time.After(3 * time.Second):
		t.Fatal("CDP login context not cancelled when RootCtx closed")
	}
}
