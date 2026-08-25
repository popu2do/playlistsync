package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"playlistsync/internal/auth"
	"playlistsync/internal/model"
	"playlistsync/internal/web/handlers"
)

// TestWebCmdHelpNoAlias verifies the web command exists and does not present
// itself as an alias of another command (plan GC #1: independent subcommand).
func TestWebCmdHelpNoAlias(t *testing.T) {
	cmd := newWebCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("web --help failed: %v", err)
	}
	help := out.String()
	if !strings.Contains(help, "web") {
		t.Errorf("help does not mention web:\n%s", help)
	}
	if strings.Contains(help, "alias") {
		t.Errorf("web help mentions alias (should be an independent command):\n%s", help)
	}
	// Loopback-only binding must be stated (plan GC #7: never binds beyond
	// 127.0.0.1); the help text names the literal loopback address.
	if !strings.Contains(help, "127.0.0.1") {
		t.Errorf("help should state loopback-only binding:\n%s", help)
	}
}

// TestWebCmdFlagPort parses the --port flag.
func TestWebCmdFlagPort(t *testing.T) {
	cmd := newWebCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--port", "9009", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("web --port --help failed: %v", err)
	}
	flag := cmd.Flags().Lookup("port")
	if flag == nil {
		t.Fatal("missing --port flag")
	}
	if flag.DefValue != "0" {
		t.Errorf("--port default = %q, want 0 (random)", flag.DefValue)
	}
}

// TestStartCockpitBanner starts the real stack on a random port and verifies
// the banner is a loopback URL carrying the session token, then performs an
// authenticated round-trip through the live server and shuts it down.
func TestStartCockpitBanner(t *testing.T) {
	banner, wait, err := startCockpit(0)
	if err != nil {
		t.Fatalf("startCockpit: %v", err)
	}
	u, err := url.Parse(banner)
	if err != nil {
		t.Fatalf("parse banner %q: %v", banner, err)
	}
	if u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
		t.Errorf("banner must be loopback http, got %q", banner)
	}
	token := u.Query().Get("token")
	if token == "" {
		t.Fatalf("banner missing token: %q", banner)
	}

	// Authenticated round-trip: GET /api/v1/session with the banner token.
	base := "http://" + u.Host
	req, _ := http.NewRequest("GET", base+"/api/v1/session?token="+token, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/session status = %d", resp.StatusCode)
	}
	var sess struct {
		SessionID string `json:"session_id"`
		Status    string `json:"status"`
		Port      int    `json:"port"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if sess.SessionID == "" || sess.Status == "" {
		t.Errorf("session response incomplete: %+v", sess)
	}
	if sess.Port == 0 {
		t.Errorf("session port = 0, want the bound loopback port")
	}

	// Trigger graceful shutdown through the cockpit endpoint and wait.
	shutReq, _ := http.NewRequest("POST", base+"/api/v1/session/shutdown?token="+token, strings.NewReader("{}"))
	shutReq.Header.Set("Authorization", "Bearer "+token)
	shutResp, err := client.Do(shutReq)
	if err != nil {
		t.Fatalf("POST /api/v1/session/shutdown: %v", err)
	}
	shutResp.Body.Close()
	if shutResp.StatusCode != http.StatusAccepted {
		t.Fatalf("shutdown status = %d, want 202", shutResp.StatusCode)
	}

	done := make(chan error, 1)
	go func() { done <- wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server wait returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down gracefully")
	}
}

// TestSpotifyPKCEFullFlow is the BLOCKER-1 regression: the PKCE exchanger must
// (a) build an authorize URL with an S256 code_challenge + redirect_uri +
// state, and (b) exchange the code WITH the code_verifier + redirect_uri, as
// Spotify requires. A mocked Spotify backend (httptest) captures the token
// exchange form; a state that was never issued must be rejected (CSRF).
func TestSpotifyPKCEFullFlow(t *testing.T) {
	var captured url.Values
	var captureMu = &sync.Mutex{}
	var meCalled = &sync.Mutex{}
	meCallCount := 0
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/me" {
			meCalled.Lock()
			meCallCount++
			meCalled.Unlock()
			if got := r.Header.Get("Authorization"); got != "Bearer tok-123" {
				t.Errorf("me call Authorization = %q, want Bearer tok-123", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"display_name":"PKCE User","id":"u123"}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		captureMu.Lock()
		captured = vals
		captureMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-123","token_type":"Bearer","expires_in":3600,"scope":"playlist-modify-public"}`))
	}))
	defer tokenSrv.Close()
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("response_type") != "code" {
			t.Errorf("authorize: response_type = %q", q.Get("response_type"))
		}
		if q.Get("code_challenge_method") != "S256" {
			t.Errorf("authorize: code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
		}
		if q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("redirect_uri") == "" {
			t.Errorf("authorize: missing required param (challenge=%q state=%q redirect=%q)",
				q.Get("code_challenge"), q.Get("state"), q.Get("redirect_uri"))
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer authSrv.Close()

	p := &spotifyPKCE{
		clientID:     "test-client",
		authorizeURL: authSrv.URL,
		tokenURL:     tokenSrv.URL,
		meURL:        tokenSrv.URL + "/me",
		http:         authSrv.Client(), // inject handler via server
		pending:      make(map[string]pkceEntry),
	}
	redirectURI := "http://127.0.0.1:4242/api/v1/auth/spotify/callback"

	authURL, state, err := p.AuthorizeURL(redirectURI)
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	if !strings.HasPrefix(authURL, authSrv.URL) {
		t.Errorf("authorize url = %q, want %q prefix", authURL, authSrv.URL)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	q := u.Query()
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Errorf("authorize url missing S256 challenge: %s", authURL)
	}
	if q.Get("state") != state {
		t.Errorf("authorize url state %q != returned state %q", q.Get("state"), state)
	}
	if q.Get("redirect_uri") != redirectURI {
		t.Errorf("authorize url redirect_uri = %q, want %q", q.Get("redirect_uri"), redirectURI)
	}
	if q.Get("client_id") != "test-client" {
		t.Errorf("authorize url client_id = %q", q.Get("client_id"))
	}

	// Exchange: with the correct state the POST must carry code_verifier +
	// redirect_uri (the BLOCKER-1(c) requirement), and the /v1/me best-effort
	// user capture must populate the display name (MAJOR-5 user radar).
	tok, err := p.ExchangeCode(context.Background(), "authz-code-1", state)
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok.AccessToken != "tok-123" {
		t.Errorf("access token = %q, want tok-123", tok.AccessToken)
	}
	if tok.User != "PKCE User" {
		t.Errorf("token user = %q, want PKCE User (from /v1/me)", tok.User)
	}
	if tok.IssuedAt.IsZero() {
		t.Error("token issued_at not set")
	}
	meCalled.Lock()
	if meCallCount == 0 {
		t.Error("/v1/me was never called")
	}
	meCalled.Unlock()
	captureMu.Lock()
	if captured.Get("code_verifier") == "" {
		t.Error("token POST missing code_verifier (Spotify PKCE requires it)")
	}
	if captured.Get("redirect_uri") != redirectURI {
		t.Errorf("token POST redirect_uri = %q, want %q", captured.Get("redirect_uri"), redirectURI)
	}
	if captured.Get("code") != "authz-code-1" {
		t.Errorf("token POST code = %q", captured.Get("code"))
	}
	if captured.Get("grant_type") != "authorization_code" {
		t.Errorf("token POST grant_type = %q", captured.Get("grant_type"))
	}
	captureMu.Unlock()

	// The S256 challenge must match the verifier (RFC 7636 §4.2).
	if got := pkceS256Challenge(verifierFromCapture(t, &captured, captureMu)); got != q.Get("code_challenge") {
		t.Errorf("S256 challenge mismatch: captured verifier produces %q, authorize used %q", got, q.Get("code_challenge"))
	}

	// CSRF: a state that was never issued must be rejected before any HTTP call
	// with the sentinel error (Minor-15 -> callback maps it to 400).
	_, err = p.ExchangeCode(context.Background(), "code", "bogus-state")
	if !errors.Is(err, handlers.ErrInvalidOAuthState) {
		t.Errorf("unknown state error = %v, want ErrInvalidOAuthState", err)
	}
	// Replay of an already-consumed state must also fail with the sentinel.
	_, err = p.ExchangeCode(context.Background(), "code", state)
	if !errors.Is(err, handlers.ErrInvalidOAuthState) {
		t.Errorf("replay error = %v, want ErrInvalidOAuthState", err)
	}
}

// verifierFromCapture reads the last captured form's code_verifier (helper to
// keep the capture lock usage explicit).
func verifierFromCapture(t *testing.T, captured *url.Values, mu *sync.Mutex) string {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	return captured.Get("code_verifier")
}

// TestSpotifyPKCEMissingClientID verifies every operation fails cleanly when
// PLAYLISTSYNC_SPOTIFY_CLIENT_ID is absent (zero-trace: no hardcoded creds).
func TestSpotifyPKCEMissingClientID(t *testing.T) {
	p := &spotifyPKCE{
		clientID: "",
		http:     &http.Client{},
		pending:  make(map[string]pkceEntry),
	}
	if _, _, err := p.AuthorizeURL("http://127.0.0.1:1/callback"); err == nil {
		t.Error("AuthorizeURL with empty client id should error")
	}
	if _, err := p.ExchangeCode(context.Background(), "code", "state"); err == nil {
		t.Error("ExchangeCode with empty client id should error")
	}
}

// TestApplyDiffNoPendingIds is the MAJOR-3 regression (cmd side): the cockpit
// apply artifact must NOT carry `pending_N` placeholder target ids — they
// poisoned the invariant diff-completeness check. Added tracks are recorded
// with an empty target id (the live mutation has not happened yet).
func TestApplyDiffNoPendingIds(t *testing.T) {
	diff := &handlers.DiffResult{
		SourceTotal: 4,
		Added: []model.SpotifyTrack{
			{Index: 1, ID: "sp1", Title: "A", Artists: []string{"X"}},
			{Index: 2, ID: "sp2", Title: "B"},
		},
		Removed: []model.YTMTrack{{VideoID: "yt_extra", Title: "Old"}},
		Retained: []model.AddedTrack{
			{Index: 3, Title: "C", TargetTrackID: "yt_keep"},
		},
		Skipped: []model.SkippedTrack{{Index: 4, Title: "D"}},
	}
	outDir := t.TempDir()
	res, err := applyDiff(context.Background(), diff, outDir)
	if err != nil {
		t.Fatalf("applyDiff: %v", err)
	}
	if res == nil {
		t.Fatal("applyDiff returned nil result")
	}
	for _, a := range res.AddedAfterReview {
		if strings.HasPrefix(a.TargetTrackID, "pending_") {
			t.Errorf("artifact still contains placeholder id %q (MAJOR-3)", a.TargetTrackID)
		}
	}
	// The artifact must also be on disk and re-readable (loadResult round-trip).
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read outDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no result artifact written")
	}
	if _, err := loadResult("web_cockpit", outDir); err != nil {
		t.Fatalf("loadResult(web_cockpit): %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "spotify_to_ytmusic_web_cockpit_result.json")); err != nil {
		t.Fatalf("expected artifact path missing: %v", err)
	}
}

// TestAuthorizeEndpointUnauthorized verifies the OAuth authorize URL is only
// reachable behind the bearer token (unlike the callback, which the Spotify
// redirect must reach without a token).
func TestAuthorizeEndpointUnauthorized(t *testing.T) {
	banner, wait, err := startCockpit(0)
	if err != nil {
		t.Fatalf("startCockpit: %v", err)
	}
	u, _ := url.Parse(banner)
	base := "http://" + u.Host

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(base + "/api/v1/auth/spotify/authorize")
	if err != nil {
		t.Fatalf("GET authorize without token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("authorize without token status = %d, want 401", resp.StatusCode)
	}

	// Callback must be reachable WITHOUT the token (BLOCKER-1(a)): the Spotify
	// redirect never carries it. A missing code yields 400, not 401.
	resp2, err := client.Get(base + "/api/v1/auth/spotify/callback")
	if err != nil {
		t.Fatalf("GET callback without token: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("callback without token status = %d, want 400 (not 401)", resp2.StatusCode)
	}

	// Shutdown must be authenticated (unlike the callback).
	shutReq, _ := http.NewRequest("POST", base+"/api/v1/session/shutdown?token="+u.Query().Get("token"), nil)
	resp3, err := client.Do(shutReq)
	if err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	resp3.Body.Close()
	done := make(chan error, 1)
	go func() { done <- wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down")
	}
}

// TestStartCockpitShutdownPromptWithSSEClient is the BLOCKER-2 end-to-end
// regression: with an SSE client attached, graceful shutdown must complete
// promptly (the SSE handler must release on RootCtx cancel), not stall into
// the 5s http.Server.Shutdown deadline and exit non-zero.
func TestStartCockpitShutdownPromptWithSSEClient(t *testing.T) {
	banner, wait, err := startCockpit(0)
	if err != nil {
		t.Fatalf("startCockpit: %v", err)
	}
	u, _ := url.Parse(banner)
	token := u.Query().Get("token")
	base := "http://" + u.Host

	// Attach an SSE client (heartbeat + RootCtx release paths active).
	sseReq, _ := http.NewRequest("GET", base+"/api/v1/events?token="+token, nil)
	sseClient := &http.Client{Timeout: 0}
	sseResp, err := sseClient.Do(sseReq)
	if err != nil {
		t.Fatalf("GET /api/v1/events: %v", err)
	}
	defer sseResp.Body.Close()
	if sseResp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d", sseResp.StatusCode)
	}
	// Let the handler subscribe.
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	shutReq, _ := http.NewRequest("POST", base+"/api/v1/session/shutdown?token="+token, strings.NewReader("{}"))
	client := &http.Client{Timeout: 5 * time.Second}
	shutResp, err := client.Do(shutReq)
	if err != nil {
		t.Fatalf("POST shutdown: %v", err)
	}
	shutResp.Body.Close()

	done := make(chan error, 1)
	go func() { done <- wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait returned error: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("server did not shut down within 4s with an SSE client attached (root ctx never cancelled?)")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("shutdown took %v with an SSE client — the drain would have hit the 5s deadline", elapsed)
	}
}

// TestEncryptedTokenStoreRoundTrip is the MAJOR-5 reader regression: the PKCE
// store must be readable (LoadSpotifyToken), not write-only — otherwise the
// exchanged token is dead data and /auth/status can never flip. Save then Load
// must recover the identical token (including the /v1/me user name).
func TestEncryptedTokenStoreRoundTrip(t *testing.T) {
	outDir := t.TempDir()
	authPath := filepath.Join(outDir, "auth", "spotify_credentials.json")
	store := &encryptedTokenStore{authPath: authPath}

	orig := &handlers.OAuthToken{
		AccessToken:  "tok-secret-1",
		RefreshToken: "refresh-secret-1",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		Scope:        "playlist-modify-public",
		User:         "PKCE User",
		IssuedAt:     time.Now().UTC(),
	}
	if err := store.SaveSpotifyToken(orig, authPath); err != nil {
		t.Fatalf("SaveSpotifyToken: %v", err)
	}
	// Plaintext must NOT be readable at rest (zero-trace).
	if content, err := os.ReadFile(authPath + ".enc"); err != nil {
		t.Fatalf("read .enc: %v", err)
	} else if strings.Contains(string(content), "tok-secret-1") {
		t.Fatal(".enc contains plaintext access token (zero-trace violation)")
	}

	loaded, err := store.LoadSpotifyToken(authPath)
	if err != nil {
		t.Fatalf("LoadSpotifyToken: %v", err)
	}
	if loaded.AccessToken != orig.AccessToken || loaded.RefreshToken != orig.RefreshToken {
		t.Errorf("round-trip token mismatch: %+v", loaded)
	}
	if loaded.User != "PKCE User" {
		t.Errorf("round-trip user = %q, want PKCE User", loaded.User)
	}
	if !loaded.IssuedAt.Equal(orig.IssuedAt) {
		t.Errorf("round-trip issued_at mismatch: %v vs %v", loaded.IssuedAt, orig.IssuedAt)
	}

	// Missing store -> error (fallback path), not panic.
	if _, err := store.LoadSpotifyToken(filepath.Join(outDir, "nope", "x.json")); err == nil {
		t.Error("LoadSpotifyToken on missing store should error")
	}
}

// TestOAuthTokenActive covers the expiry logic behind the status flip.
func TestOAuthTokenActive(t *testing.T) {
	now := time.Now().UTC()
	active := &handlers.OAuthToken{AccessToken: "tok", IssuedAt: now.Add(-time.Minute), ExpiresIn: 3600}
	if !oauthTokenActive(active) {
		t.Error("fresh token should be active")
	}
	expired := &handlers.OAuthToken{AccessToken: "tok", IssuedAt: now.Add(-2 * time.Hour), ExpiresIn: 3600}
	if oauthTokenActive(expired) {
		t.Error("expired token should be inactive")
	}
	noExpiry := &handlers.OAuthToken{AccessToken: "tok"} // no IssuedAt/ExpiresIn
	if !oauthTokenActive(noExpiry) {
		t.Error("token without expiry info should be treated as active")
	}
	if oauthTokenActive(nil) || oauthTokenActive(&handlers.OAuthToken{}) {
		t.Error("empty/nil token must not be active")
	}
}

// TestSpotifyAuthFromOAuth is the MAJOR-5 seam regression: a usable encrypted
// OAuth token must surface as an authenticated AuthStatus (with the stored
// user name), while a missing/empty store yields nil so the caller falls back
// to the legacy cookie validator unchanged.
func TestSpotifyAuthFromOAuth(t *testing.T) {
	outDir := t.TempDir()
	authPath := filepath.Join(outDir, "auth", "spotify_credentials.json")
	store := &encryptedTokenStore{authPath: authPath}

	// No store yet -> nil (legacy fallback).
	if st := spotifyAuthFromOAuth(store, authPath); st != nil {
		t.Fatalf("expected nil for missing store, got %+v", st)
	}
	if st := spotifyAuthFromOAuth(nil, authPath); st != nil {
		t.Fatalf("expected nil for nil store, got %+v", st)
	}

	// Store an active token -> authenticated with user name.
	now := time.Now().UTC()
	if err := store.SaveSpotifyToken(&handlers.OAuthToken{
		AccessToken: "active-tok", ExpiresIn: 3600, User: "Alice", IssuedAt: now,
	}, authPath); err != nil {
		t.Fatalf("save: %v", err)
	}
	st := spotifyAuthFromOAuth(store, authPath)
	if st == nil {
		t.Fatal("expected authenticated status for stored active token")
	}
	if !st.Authenticated || st.Platform != auth.PlatformSpotify {
		t.Errorf("status = %+v, want authenticated spotify", st)
	}
	if st.User != "Alice" {
		t.Errorf("status user = %q, want Alice", st.User)
	}

	// An expired token -> nil (falls through to legacy, which also fails).
	if err := store.SaveSpotifyToken(&handlers.OAuthToken{
		AccessToken: "stale-tok", ExpiresIn: 3600, IssuedAt: now.Add(-2 * time.Hour),
	}, authPath); err != nil {
		t.Fatalf("save expired: %v", err)
	}
	if st := spotifyAuthFromOAuth(store, authPath); st != nil {
		t.Errorf("expired token must not authenticate, got %+v", st)
	}
}

// TestPKCEStateExpiry is the Minor-14 regression: an authorize state older
// than the TTL must be rejected on exchange (and lazily pruned).
func TestPKCEStateExpiry(t *testing.T) {
	p := &spotifyPKCE{
		clientID: "test-client",
		pending:  make(map[string]pkceEntry),
	}
	state := "stale-state"
	p.mu.Lock()
	p.pending[state] = pkceEntry{verifier: "v", redirectURI: "http://127.0.0.1:9/cb", issued: time.Now().Add(-pkceStateTTL - time.Minute)}
	p.mu.Unlock()

	_, err := p.ExchangeCode(context.Background(), "code", state)
	if !errors.Is(err, handlers.ErrInvalidOAuthState) {
		t.Errorf("expired state error = %v, want ErrInvalidOAuthState", err)
	}

	// The stale entry must be pruned from the map.
	p.mu.Lock()
	_, stillPresent := p.pending[state]
	p.mu.Unlock()
	if stillPresent {
		t.Error("expired state entry was not pruned")
	}
}

// TestAuthorizeAndCallbackEndToEnd flips /auth/status through the live
// CheckAuth seam shape used by startCockpit: a token persisted via the
// encrypted store (as a PKCE callback does) makes the seam report
// authenticated=true, while an empty store reports false (legacy fallback).
func TestAuthorizeAndCallbackEndToEnd(t *testing.T) {
	outDir := t.TempDir()
	authPath := filepath.Join(outDir, "auth", "spotify_credentials.json")
	store := &encryptedTokenStore{authPath: authPath}

	legacy := func(platform auth.Platform, path, proxy string) (*auth.AuthStatus, error) {
		// Simulate the legacy cookie validator: unauthenticated (no cookies).
		return &auth.AuthStatus{Platform: platform, Authenticated: false, Path: path}, nil
	}
	checkAuth := func(platform auth.Platform, authPath, proxy string) (*auth.AuthStatus, error) {
		if platform == auth.PlatformSpotify {
			if st := spotifyAuthFromOAuth(store, authPath); st != nil {
				return st, nil
			}
		}
		return legacy(platform, authPath, proxy)
	}

	// Before any callback: not authenticated.
	st, err := checkAuth(auth.PlatformSpotify, authPath, "")
	if err != nil || st.Authenticated {
		t.Fatalf("pre-callback status = %+v err=%v, want unauthenticated", st, err)
	}

	// Simulate a successful PKCE callback persisting the token through the store.
	if err := store.SaveSpotifyToken(&handlers.OAuthToken{
		AccessToken: "callback-tok", ExpiresIn: 3600, User: "Spotify User", IssuedAt: time.Now().UTC(),
	}, authPath); err != nil {
		t.Fatalf("save token: %v", err)
	}

	// After the callback: authenticated with display name.
	st, err = checkAuth(auth.PlatformSpotify, authPath, "")
	if err != nil {
		t.Fatalf("post-callback checkAuth: %v", err)
	}
	if !st.Authenticated {
		t.Fatal("status did not flip to authenticated after PKCE token persisted (MAJOR-5)")
	}
	if st.User != "Spotify User" {
		t.Errorf("post-callback user = %q, want Spotify User", st.User)
	}
}
