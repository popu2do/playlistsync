package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"playlistsync/internal/auth"
	"playlistsync/internal/config"
	"playlistsync/internal/engine"
	"playlistsync/internal/fileutil"
	"playlistsync/internal/invariants"
	"playlistsync/internal/model"
	"playlistsync/internal/spotify"
	"playlistsync/internal/web/bridge"
	"playlistsync/internal/web/handlers"
	"playlistsync/internal/web/server"
	"playlistsync/internal/web/static"
	"playlistsync/internal/ytmusic"

	"github.com/spf13/cobra"
)

// webCmd is the independent cockpit subcommand. It NEVER calls syncCmd.RunE
// (plan Global Constraint #1): it spawns the loopback HTTP server, wires the
// handlers, prints the token banner and blocks until graceful shutdown.
func newWebCmd() *cobra.Command {
	var webPort int

	webCmd := &cobra.Command{
		Use:   "web",
		Short: "Start the local web cockpit server (loopback-only)",
		Long: `Start the local web cockpit server.

Binds the embedded HTTP+SSE cockpit to 127.0.0.1 on a random (or given) port,
prints the token-bearing access URL, and blocks until the server shuts down
(idle watchdog, Ctrl+C, or the cockpit's shutdown endpoint).

The session token is generated at startup with crypto/rand (256-bit) and is
only ever printed in the banner; it is never written to disk.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWebServer(webPort)
		},
	}
	webCmd.Flags().IntVarP(&webPort, "port", "p", 0, "HTTP port to bind (0 = random)")
	return webCmd
}

// runWebServer builds and starts the cockpit, prints the banner and blocks
// until shutdown. The returned error is the serve/shutdown error.
func runWebServer(port int) error {
	banner, shutdown, err := startCockpit(port)
	if err != nil {
		return err
	}
	fmt.Printf("playlistsync web: %s\n", banner)
	return shutdown()
}

// startCockpit wires the full web stack and starts the server. It returns the
// banner URL and a blocking wait function. Split out from runWebServer so
// tests can start the real stack and capture the banner without blocking.
func startCockpit(port int) (banner string, wait func() error, err error) {
	// Event bus: ring-backed recording broadcaster. The bridge and every
	// handler broadcast through it; the SSE endpoint subscribes to it.
	ring := bridge.NewSSEEventRingBuffer(0) // default 1024 slots (spec 02 §3.2)
	recorder := handlers.NewRecordingBroadcaster(ring)

	arb := bridge.NewWebReviewBridge(recorder, 5*time.Minute)

	srv, err := server.NewWebServer(server.Config{
		PreferredHost: "127.0.0.1",
		PreferredPort: port,
	}, static.DistFS)
	if err != nil {
		return "", nil, err
	}

	outDir := config.GetOutputDir()
	spAuthPath := config.GetSpotifyAuthPath()
	ytmAuthPath := config.GetYTMAuthPath()
	proxyURL := config.GlobalConfig.ProxyURL
	if proxyURL == "" {
		proxyURL = auth.DetectSystemProxy()
	}

	state := handlers.NewCockpitState()
	oauthStore := &encryptedTokenStore{authPath: spAuthPath}
	cfg := handlers.HandlerConfig{
		Broadcaster: recorder,
		Ring:        ring,
		RootCtx:     srv.RootDone(),
		SessionID:   "sess_" + hexToken(),
		StartTime:   time.Now(),
		Port:        srv.ActualPort,
		ClientCount: recorder.ClientCount,
		Kick:        srv.Kick,
		Shutdown:    srv.Shutdown,

		CheckAuth: func(platform auth.Platform, authPath, proxy string) (*auth.AuthStatus, error) {
			// MAJOR-5: the cockpit's PKCE OAuth store is consulted FIRST for
			// Spotify — the legacy cookie validator (auth.CheckAuthentication)
			// cannot read the encrypted OAuth token, so a successful PKCE
			// callback would otherwise never flip /auth/status. The legacy
			// cookie path stays as a fallback (CLI flow untouched).
			if platform == auth.PlatformSpotify {
				if st := spotifyAuthFromOAuth(oauthStore, authPath); st != nil {
					return st, nil
				}
			}
			return auth.CheckAuthentication(platform, authPath, proxy)
		},
		CDPLogin:        auth.StartCDPLoginWithContext,
		SpotifyAuthPath: spAuthPath,
		YTMAuthPath:     ytmAuthPath,
		ProxyURL:        proxyURL,
		PKCE:            newSpotifyPKCE(),
		TokenStore:      oauthStore,

		RunReconcile: func(ctx context.Context, p handlers.ReconcileParams, emit func(string, interface{})) (*handlers.DiffResult, error) {
			return runReconcile(ctx, p, emit, outDir, ytmAuthPath, proxyURL)
		},
		ResolveArbitration: arb.ResolveArbitration,
		ApplyDiff: func(ctx context.Context, diff *handlers.DiffResult, force bool) (*model.SyncResult, error) {
			return applyDiff(ctx, diff, outDir)
		},

		InspectSource: func(id string) (*model.SpotifyPlaylist, error) {
			return loadSourcePlaylist(id, outDir)
		},
		InspectTarget: func(id string) (*model.YTMPlaylist, error) {
			return loadTargetPlaylist(id, ytmAuthPath, proxyURL)
		},

		LoadResult: func(id string) (*model.SyncResult, error) {
			return loadResult(id, outDir)
		},
		Verifier:   invariants.NewVerifier(),
		ReportsDir: outDir,
		State:      state,
	}

	handlers.RegisterAll(srv.Mux(), cfg.Verifier, cfg)

	actualURL, err := srv.Start()
	if err != nil {
		return "", nil, err
	}
	// Banner: playlistsync web: http://127.0.0.1:<port>?token=<token>
	banner = fmt.Sprintf("http://127.0.0.1:%d?token=%s", srv.ActualPort(), srv.Token())
	_ = actualURL
	return banner, srv.Wait, nil
}

// hexToken returns a short random hex token for the session id (not the auth
// token — that is generated by the server itself).
func hexToken() string {
	b := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// runReconcile is the production reconcile runner: it loads the source Spotify
// playlist from the output directory, fetches the live target playlist through
// the YTM client, computes the diff with the engine and maps it to the web
// DiffResult. Progress is emitted through the SSE pipe.
func runReconcile(ctx context.Context, p handlers.ReconcileParams, emit func(string, interface{}), outDir, ytmAuthPath, proxy string) (*handlers.DiffResult, error) {
	if emit == nil {
		emit = func(string, interface{}) {}
	}

	sp, err := loadSourcePlaylist(p.SourcePlaylistID, outDir)
	if err != nil {
		return nil, err
	}
	emit("DIFF_PROGRESS", map[string]interface{}{"processed": 0, "total": len(sp.Tracks), "stage": "source_loaded"})

	target, err := loadTargetPlaylist(p.TargetPlaylistID, ytmAuthPath, proxy)
	if err != nil {
		return nil, err
	}
	emit("DIFF_PROGRESS", map[string]interface{}{"processed": len(sp.Tracks), "total": len(sp.Tracks), "stage": "target_loaded"})

	plan := engine.ComputeDiff(sp, target, nil)
	diff := &handlers.DiffResult{
		SourceTotal: len(sp.Tracks),
		Added:       plan.MissingInYTM,
		Removed:     plan.ExtraInYTM,
		Retained:    plan.Matched,
	}
	return diff, nil
}

// applyDiff writes a SyncResult artifact derived from the diff to the output
// directory (atomic zero-trace write). It is the cockpit's apply seam; a
// future plan can extend it to perform live YTM mutations.
func applyDiff(ctx context.Context, diff *handlers.DiffResult, outDir string) (*model.SyncResult, error) {
	if diff == nil {
		return nil, errors.New("nil diff")
	}
	added, _, retained, skipped := diff.Counts()
	res := &model.SyncResult{
		Direction:          "spotify_to_youtube_music",
		SourcePlatform:     "spotify",
		TargetPlatform:     "youtube-music",
		TotalSourceTracks:  diff.SourceTotal,
		AddedTracks:        added + retained,
		SkippedTracks:      skipped,
		RemovedExtraTracks: removedTrackModels(diff.Removed),
		AddedAfterReview:   addedTrackModels(diff.Added, diff.Retained),
		LastSyncedAt:       time.Now().UTC().Format(time.RFC3339),
		SyncOrder:          true,
	}

	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return nil, err
	}
	slug := sanitizeSlug("web_cockpit")
	resultPath := filepath.Join(outDir, fmt.Sprintf("spotify_to_ytmusic_%s_result.json", slug))
	if err := fileutil.WriteFileAtomic(resultPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("write result: %w", err)
	}
	return res, nil
}

func removedTrackModels(tracks []model.YTMTrack) []model.RemovedTrack {
	out := make([]model.RemovedTrack, 0, len(tracks))
	for _, t := range tracks {
		out = append(out, model.RemovedTrack{
			TargetTrackID: t.VideoID,
			Title:         t.Title,
			Artists:       t.Artists,
		})
	}
	return out
}

// addedTrackModels builds the AddedAfterReview summary. Added tracks get an
// EMPTY TargetTrackID (Major-3 fix): the cockpit apply persists an auditable
// artifact without performing a live YTM mutation, so pending adds have no
// real video id. Fabricating `pending_N` ids poisoned invariant 4 (diff
// completeness) because the placeholder was never in the target universe —
// handlers.buildVerifyInput now also defensively drops `pending_*` ids.
func addedTrackModels(added []model.SpotifyTrack, retained []model.AddedTrack) []model.AddedTrack {
	out := make([]model.AddedTrack, 0, len(added)+len(retained))
	for _, t := range added {
		out = append(out, model.AddedTrack{
			Index:         t.Index,
			Title:         t.Title,
			Artists:       t.Artists,
			TargetTrackID: "", // no real target id until the mutation is applied live
		})
	}
	out = append(out, retained...)
	return out
}

// loadSourcePlaylist resolves a source playlist artifact by id/slug.
func loadSourcePlaylist(id, outDir string) (*model.SpotifyPlaylist, error) {
	if id == "" {
		return nil, errors.New("empty source playlist id")
	}
	if pl, _, err := spotify.FindPlaylistByName(outDir, id); err == nil {
		return pl, nil
	}
	path := filepath.Join(outDir, fmt.Sprintf("spotify_%s_source.json", sanitizeSlug(id)))
	return spotify.ReadPlaylistJSON(path)
}

// loadTargetPlaylist fetches the live target playlist through the YTM client.
func loadTargetPlaylist(id, ytmAuthPath, proxy string) (*model.YTMPlaylist, error) {
	if id == "" {
		return nil, errors.New("empty target playlist id")
	}
	client, err := ytmusic.NewClient(ytmAuthPath, proxy)
	if err != nil {
		return nil, err
	}
	return client.GetPlaylist(id)
}

// loadResult reads a sync result artifact by id/slug.
func loadResult(id, outDir string) (*model.SyncResult, error) {
	if id == "" {
		return nil, errors.New("empty result id")
	}
	slug := sanitizeSlug(id)
	// Dynamic discovery mirrors resolveArtifactPaths.
	if entries, err := os.ReadDir(outDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fname := entry.Name()
			if strings.Contains(fname, "_to_") && strings.HasSuffix(strings.ToLower(fname), "_"+strings.ToLower(slug)+"_result.json") {
				return readResult(filepath.Join(outDir, fname))
			}
		}
	}
	path := filepath.Join(outDir, fmt.Sprintf("spotify_to_ytmusic_%s_result.json", slug))
	return readResult(path)
}

// readResult decodes a SyncResult JSON file.
func readResult(path string) (*model.SyncResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var res model.SyncResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("corrupted result %s: %w", path, err)
	}
	return &res, nil
}

// --- PKCE + encrypted token store (zero-trace: no plaintext at rest) --------

// spotifyPKCE is a real PKCE (RFC 7636) exchanger for the Spotify OAuth flow
// (BLOCKER-1 fix): it builds the authorize URL with a S256 code_challenge,
// remembers the code_verifier + redirect_uri per CSRF state, and performs the
// token exchange WITH the code_verifier and redirect_uri (required by
// Spotify). It requires PLAYLISTSYNC_SPOTIFY_CLIENT_ID; without one all
// operations fail with a clear configuration error (no credentials hardcoded —
// zero-trace).
// pkceStateTTL bounds how long an issued authorize state remains valid for the
// callback (Minor-14). Spotify redirects within seconds; 10 minutes is ample
// and bounds the anti-CSRF window.
const pkceStateTTL = 10 * time.Minute

type spotifyPKCE struct {
	clientID     string
	authorizeURL string // seam: https://accounts.spotify.com/authorize
	tokenURL     string // seam: https://accounts.spotify.com/api/token
	meURL        string // seam: https://api.spotify.com/v1/me
	http         *http.Client

	mu      sync.Mutex
	pending map[string]pkceEntry // state -> {verifier, redirectURI, issued}
}

// pkceEntry pairs the code_verifier with the redirect_uri it was issued for.
// issued is the issuance timestamp; entries older than pkceStateTTL are
// pruned on the next AuthorizeURL/ExchangeCode (Minor-14, lazy cleanup).
type pkceEntry struct {
	verifier    string
	redirectURI string
	issued      time.Time
}

func newSpotifyPKCE() handlers.PKCEExchanger {
	return &spotifyPKCE{
		clientID:     os.Getenv("PLAYLISTSYNC_SPOTIFY_CLIENT_ID"),
		authorizeURL: "https://accounts.spotify.com/authorize",
		tokenURL:     "https://accounts.spotify.com/api/token",
		meURL:        "https://api.spotify.com/v1/me",
		http:         &http.Client{Timeout: 30 * time.Second},
		pending:      make(map[string]pkceEntry),
	}
}

// pruneExpired removes state entries older than pkceStateTTL. Callers hold p.mu.
func (p *spotifyPKCE) pruneExpired(now time.Time) {
	for k, e := range p.pending {
		if now.Sub(e.issued) > pkceStateTTL {
			delete(p.pending, k)
		}
	}
}

// AuthorizeURL implements handlers.PKCEExchanger: it generates a fresh
// code_verifier (43-char, RFC 7636 §4.1) and a random CSRF state, stores
// {verifier, redirectURI, issued} keyed by state, and returns the Spotify
// authorize URL carrying the S256 code_challenge + redirect_uri + state.
func (p *spotifyPKCE) AuthorizeURL(redirectURI string) (string, string, error) {
	if p.clientID == "" {
		return "", "", errors.New("PLAYLISTSYNC_SPOTIFY_CLIENT_ID not set; PKCE authorize unavailable")
	}
	if redirectURI == "" {
		return "", "", errors.New("missing redirect uri")
	}
	verifier, err := newPKCEVerifier()
	if err != nil {
		return "", "", err
	}
	challenge := pkceS256Challenge(verifier)
	state, err := randomState()
	if err != nil {
		return "", "", err
	}

	p.mu.Lock()
	p.pruneExpired(time.Now())
	p.pending[state] = pkceEntry{verifier: verifier, redirectURI: redirectURI, issued: time.Now()}
	p.mu.Unlock()

	q := url.Values{}
	q.Set("client_id", p.clientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("scope", "playlist-modify-public playlist-modify-private user-read-email")
	u, err := url.Parse(p.authorizeURL)
	if err != nil {
		return "", "", err
	}
	u.RawQuery = q.Encode()
	return u.String(), state, nil
}

// ExchangeCode implements handlers.PKCEExchanger: the state MUST reference a
// live verifier issued by AuthorizeURL (CSRF protection); unknown, expired or
// already-consumed states return handlers.ErrInvalidOAuthState (the callback
// maps it to 400 — Minor-15). The exchange POSTs the code + code_verifier +
// redirect_uri (RFC 7636 §4.5) to the Spotify token endpoint, then
// best-effort captures the Spotify display name (/v1/me) so the status radar
// can show it without further network calls.
func (p *spotifyPKCE) ExchangeCode(ctx context.Context, code, state string) (*handlers.OAuthToken, error) {
	if p.clientID == "" {
		return nil, errors.New("PLAYLISTSYNC_SPOTIFY_CLIENT_ID not set; PKCE exchange unavailable")
	}
	if code == "" {
		return nil, errors.New("missing authorization code")
	}

	now := time.Now()
	p.mu.Lock()
	entry, ok := p.pending[state]
	delete(p.pending, state) // one-time use
	p.pruneExpired(now)
	p.mu.Unlock()
	if !ok || now.Sub(entry.issued) > pkceStateTTL {
		return nil, fmt.Errorf("%w: authorization flow was not initiated, expired, or already consumed", handlers.ErrInvalidOAuthState)
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", p.clientID)
	form.Set("redirect_uri", entry.redirectURI)
	form.Set("code_verifier", entry.verifier)

	req, err := http.NewRequestWithContext(ctx, "POST", p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("spotify token endpoint returned HTTP %d", resp.StatusCode)
	}
	var tok handlers.OAuthToken
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, err
	}
	tok.IssuedAt = now
	// Best-effort user capture: a /v1/me failure must never fail the exchange.
	if p.meURL != "" && tok.AccessToken != "" {
		if user := p.fetchSpotifyUser(ctx, tok.AccessToken); user != "" {
			tok.User = user
		}
	}
	return &tok, nil
}

// fetchSpotifyUser returns the display name (fallback: account id) for an
// access token via GET /v1/me. Failures (network, auth, decode) return "".
func (p *spotifyPKCE) fetchSpotifyUser(ctx context.Context, accessToken string) string {
	req, err := http.NewRequestWithContext(ctx, "GET", p.meURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := p.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var me struct {
		DisplayName string `json:"display_name"`
		ID          string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return ""
	}
	if me.DisplayName != "" {
		return me.DisplayName
	}
	return me.ID
}

// newPKCEVerifier returns a 43-char random code_verifier per RFC 7636 §4.1
// (charset A-Z a-z 0-9 - . _ ~; 43 is the minimum length).
func newPKCEVerifier() (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
	b := make([]byte, 64)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	out := make([]byte, 43)
	for i := range out {
		out[i] = charset[int(b[i])%len(charset)]
	}
	return string(out), nil
}

// pkceS256Challenge derives the S256 code_challenge (base64url, no padding)
// from a verifier per RFC 7636 §4.2.
func pkceS256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// randomState returns a 32-byte hex CSRF state.
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// encryptedTokenStore persists OAuth tokens AES-256-GCM-encrypted next to the
// auth dir. The key lives in a 0600 file derived from the process user's auth
// directory; token material is never written in plaintext (spec 05 §1.5).
type encryptedTokenStore struct {
	authPath string
}

func (s *encryptedTokenStore) SaveSpotifyToken(tok *handlers.OAuthToken, authPath string) error {
	if tok == nil {
		return errors.New("nil token")
	}
	if authPath == "" {
		authPath = s.authPath
	}
	key, err := loadOrCreateKey(filepath.Dir(authPath))
	if err != nil {
		return err
	}
	plain, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	// AES-256-GCM: random 12-byte nonce prepended to ciphertext.
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	sealed := gcm.Seal(nonce, nonce, plain, nil)
	return fileutil.WriteFileAtomic(authPath+".enc", sealed, 0o600)
}

// LoadSpotifyToken decrypts and returns the stored OAuth token (MAJOR-5): this
// is the reader counterpart to SaveSpotifyToken. Without it the PKCE store was
// write-only — the token was dead data and /auth/status could never flip to
// authenticated. A missing/undecryptable store returns an error (the caller
// falls back to the legacy cookie validator).
func (s *encryptedTokenStore) LoadSpotifyToken(authPath string) (*handlers.OAuthToken, error) {
	if authPath == "" {
		authPath = s.authPath
	}
	sealed, err := os.ReadFile(authPath + ".enc")
	if err != nil {
		return nil, fmt.Errorf("read encrypted spotify token: %w", err)
	}
	key, err := loadOrCreateKey(filepath.Dir(authPath))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < 12 {
		return nil, errors.New("encrypted token store truncated (nonce missing)")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, sealed[:12], sealed[12:], nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt spotify token: %w", err)
	}
	var tok handlers.OAuthToken
	if err := json.Unmarshal(plain, &tok); err != nil {
		return nil, fmt.Errorf("decode spotify token: %w", err)
	}
	return &tok, nil
}

// oauthTokenActive reports whether a stored OAuth token is still usable: no
// expiry information (older/partial records) counts as active; a known
// IssuedAt+ExpiresIn pair must not have elapsed.
func oauthTokenActive(tok *handlers.OAuthToken) bool {
	if tok == nil || tok.AccessToken == "" {
		return false
	}
	if tok.IssuedAt.IsZero() || tok.ExpiresIn <= 0 {
		return true
	}
	return time.Now().UTC().Before(tok.IssuedAt.Add(time.Duration(tok.ExpiresIn) * time.Second))
}

// spotifyAuthFromOAuth returns an authenticated AuthStatus when the encrypted
// OAuth store holds a usable token (the legacy cookie validator cannot read
// it — MAJOR-5). Returns nil when there is no usable OAuth token, so the
// caller can fall back to the legacy cookie check without weakening it.
func spotifyAuthFromOAuth(store *encryptedTokenStore, authPath string) *auth.AuthStatus {
	if store == nil {
		return nil
	}
	tok, err := store.LoadSpotifyToken(authPath)
	if err != nil || !oauthTokenActive(tok) {
		return nil
	}
	user := ""
	if tok.User != "" {
		user = tok.User
	}
	return &auth.AuthStatus{
		Platform:      auth.PlatformSpotify,
		Authenticated: true,
		User:          user,
		Path:          authPath,
		Cached:        true,
	}
}

// loadOrCreateKey loads the 32-byte encryption key from <dir>/.web_oauth.key,
// creating it with 0600 perms on first use.
func loadOrCreateKey(dir string) ([]byte, error) {
	keyPath := filepath.Join(dir, ".web_oauth.key")
	if data, err := os.ReadFile(keyPath); err == nil && len(data) == 32 {
		return data, nil
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := fileutil.WriteFileAtomic(keyPath, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}
