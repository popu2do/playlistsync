package auth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"playlistsync/internal/config"
	"strings"
	"time"
)

const (
	DefaultSpotifyAuthPath = "output/auth/spotify_credentials.json"
	DefaultYTMAuthPath     = "output/auth/ytmusic_credentials.json"
	DefaultCDPPort         = 9222
	DefaultLoginTimeout    = 3 * time.Minute
)

// Platform represents supported music streaming platforms
type Platform string

const (
	PlatformSpotify Platform = "spotify"
	PlatformYouTube Platform = "youtube-music"
	PlatformAll     Platform = "all"
)

// GetProfileDir returns the persistent browser profile directory for the specified platform
func GetProfileDir(platform Platform, authPath string) string {
	dir := filepath.Dir(authPath)
	if dir == "" || dir == "." {
		dir = config.GetAuthDir()
	}
	if NormalizePlatform(string(platform)) == PlatformSpotify {
		return filepath.Join(dir, ".chrome_spotify")
	}
	return filepath.Join(dir, ".chrome_ytmusic")
}

// AuthStatus represents the result of an authentication check or login attempt
type AuthStatus struct {
	Platform      Platform `json:"platform"`
	Authenticated bool     `json:"authenticated"`
	User          string   `json:"user,omitempty"`
	Path          string   `json:"path"`
	Cached        bool     `json:"cached"`
	Message       string   `json:"message,omitempty"`
}

// String returns a formatted, canonical status line
func (a *AuthStatus) String() string {
	if a == nil {
		return "[AUTH] Status: Unknown"
	}
	pName := PlatformDisplayName(a.Platform)
	if !a.Authenticated {
		if a.Message != "" {
			return fmt.Sprintf("[AUTH FAILED] %s: %s", pName, SanitizeSensitive(a.Message))
		}
		return fmt.Sprintf("[AUTH FAILED] %s: Unauthenticated", pName)
	}

	statusText := "Authenticated"
	if a.Cached {
		statusText = "Already authenticated, credentials valid"
	}
	return fmt.Sprintf("[AUTH SUCCESS] %s: %s | Path: %s", pName, statusText, a.Path)
}

// PlatformDisplayName returns the human-readable platform name
func PlatformDisplayName(p Platform) string {
	switch NormalizePlatform(string(p)) {
	case PlatformSpotify:
		return "Spotify"
	case PlatformYouTube:
		return "YouTube Music"
	case PlatformAll:
		return "All Platforms"
	default:
		return string(p)
	}
}

// NormalizePlatform maps common aliases to canonical Platform identifiers
func NormalizePlatform(input string) Platform {
	val := strings.ToLower(strings.TrimSpace(input))
	val = strings.ReplaceAll(val, "_", "-")

	switch val {
	case "youtube-music", "ytmusic", "youtube", "ytm", "yt", "ym":
		return PlatformYouTube
	case "spotify", "spo", "sp":
		return PlatformSpotify
	case "all", "both":
		return PlatformAll
	default:
		return Platform(val)
	}
}

// LoginOptions configures authentication behaviors
type LoginOptions struct {
	Force    bool
	ProxyURL string
	AuthPath string
	Timeout  time.Duration
}

// Option modifies LoginOptions
type Option func(*LoginOptions)

// WithForce bypasses fast-path cached validation
func WithForce(force bool) Option {
	return func(o *LoginOptions) {
		o.Force = force
	}
}

// WithProxy sets proxy URL
func WithProxy(proxyURL string) Option {
	return func(o *LoginOptions) {
		o.ProxyURL = proxyURL
	}
}

// WithAuthPath overrides the destination credential file path
func WithAuthPath(path string) Option {
	return func(o *LoginOptions) {
		o.AuthPath = path
	}
}

// WithTimeout sets custom login timeout
func WithTimeout(timeout time.Duration) Option {
	return func(o *LoginOptions) {
		o.Timeout = timeout
	}
}

// CheckAuthentication verifies whether valid credentials currently exist on disk
func CheckAuthentication(platform Platform, authPath string, proxyURL string) (*AuthStatus, error) {
	norm := NormalizePlatform(string(platform))
	switch norm {
	case PlatformSpotify:
		if authPath == "" {
			authPath = DefaultSpotifyAuthPath
		}
		return ValidateSpotifyAuth(filepath.Clean(authPath), proxyURL)
	case PlatformYouTube:
		if authPath == "" {
			authPath = DefaultYTMAuthPath
		}
		return ValidateYTMCookie(filepath.Clean(authPath), proxyURL)
	default:
		return nil, fmt.Errorf("unsupported platform for authentication check: %s", platform)
	}
}

// EnsureAuthenticated ensures valid credentials exist; if invalid or missing, triggers login flow automatically
func EnsureAuthenticated(platform Platform, authPath string, proxyURL string) (*AuthStatus, error) {
	norm := NormalizePlatform(string(platform))
	if norm == PlatformAll {
		statusSpo, err := EnsureAuthenticated(PlatformSpotify, DefaultSpotifyAuthPath, proxyURL)
		if err != nil {
			return nil, err
		}
		statusYTM, err := EnsureAuthenticated(PlatformYouTube, DefaultYTMAuthPath, proxyURL)
		if err != nil {
			return nil, err
		}
		return &AuthStatus{
			Platform:      PlatformAll,
			Authenticated: statusSpo.Authenticated && statusYTM.Authenticated,
			User:          fmt.Sprintf("Spotify: %s | YouTube: %s", statusSpo.User, statusYTM.User),
			Cached:        statusSpo.Cached && statusYTM.Cached,
		}, nil
	}

	cleanPath := authPath
	if cleanPath != "" {
		cleanPath = filepath.Clean(cleanPath)
	}
	status, err := CheckAuthentication(norm, cleanPath, proxyURL)
	if err == nil && status != nil && status.Authenticated {
		status.Cached = true
		return status, nil
	}

	pName := PlatformDisplayName(norm)
	fmt.Printf("[AUTH] %s credentials not found or expired. Initiating login flow...\n", pName)

	return LoginPlatform(norm, WithAuthPath(cleanPath), WithProxy(proxyURL))
}

// LoginPlatform executes login via isolated browser session with automated CDP credential capture
func LoginPlatform(platform Platform, opts ...Option) (*AuthStatus, error) {
	norm := NormalizePlatform(string(platform))

	cfg := LoginOptions{
		Timeout: DefaultLoginTimeout,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	if norm == PlatformAll {
		statusSpo, err := LoginPlatform(PlatformSpotify, opts...)
		if err != nil {
			return nil, err
		}
		statusYTM, err := LoginPlatform(PlatformYouTube, opts...)
		if err != nil {
			return nil, err
		}
		return &AuthStatus{
			Platform:      PlatformAll,
			Authenticated: statusSpo.Authenticated && statusYTM.Authenticated,
			User:          fmt.Sprintf("Spotify: %s | YouTube: %s", statusSpo.User, statusYTM.User),
			Cached:        statusSpo.Cached && statusYTM.Cached,
		}, nil
	}

	savePath := cfg.AuthPath
	if savePath == "" {
		if norm == PlatformSpotify {
			savePath = DefaultSpotifyAuthPath
		} else {
			savePath = DefaultYTMAuthPath
		}
	}
	savePath = filepath.Clean(savePath)

	if !cfg.Force {
		status, err := CheckAuthentication(norm, savePath, cfg.ProxyURL)
		if err == nil && status != nil && status.Authenticated {
			status.Cached = true
			fmt.Println(status.String())
			return status, nil
		}
	}

	pName := PlatformDisplayName(norm)

	var targetURL, targetCookieName string
	switch norm {
	case PlatformSpotify:
		targetURL = "https://accounts.spotify.com/login?continue=https%3A%2F%2Fopen.spotify.com%2F"
		targetCookieName = "sp_dc"
	case PlatformYouTube:
		targetURL = "https://accounts.google.com/ServiceLogin?service=youtube&continue=https%3A%2F%2Fmusic.youtube.com%2F"
		targetCookieName = "SAPISID"
	default:
		err := fmt.Errorf("unsupported platform: %s", platform)
		fmt.Fprintf(os.Stderr, "[AUTH FAILED] %s\n", SanitizeSensitive(err.Error()))
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	if err := StartCDPLoginWithContext(ctx, targetURL, savePath, targetCookieName, cfg.ProxyURL); err != nil {
		fmt.Printf("[AUTH FAILED] %s: Authentication failed: %s\n", pName, SanitizeSensitive(err.Error()))
		return nil, err
	}

	status, err := CheckAuthentication(norm, savePath, cfg.ProxyURL)
	if err != nil || status == nil || !status.Authenticated {
		if err != nil {
			fmt.Printf("[AUTH FAILED] %s: verification failed: %s\n", pName, SanitizeSensitive(err.Error()))
			return nil, fmt.Errorf("verification of newly captured credentials failed: %w", err)
		}
		fmt.Printf("[AUTH FAILED] %s: verification of newly captured credentials failed\n", pName)
		return nil, fmt.Errorf("verification of newly captured credentials failed")
	}

	status.Cached = false
	fmt.Println(status.String())
	return status, nil
}

// StartCDPLoginWithContext runs CDP login with process exit watcher and context management
func StartCDPLoginWithContext(ctx context.Context, targetURL string, savePath string, targetCookieName string, proxyURL string) error {
	browserExe, err := browserLookupFn()
	if err != nil {
		return err
	}

	savePath = filepath.Clean(savePath)
	plat := PlatformSpotify
	if targetCookieName == "SAPISID" {
		plat = PlatformYouTube
	}

	profileDir := GetProfileDir(plat, savePath)
	if abs, err := filepath.Abs(profileDir); err == nil {
		profileDir = abs
	}
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return fmt.Errorf("create auth profile dir: %w", err)
	}

	port := getCDPPort()

	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", profileDir),
		"--remote-allow-origins=*",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-features=Translate,OptimizationHints,MediaRouter",
		"--disable-sync",
	}
	if proxyURL != "" {
		args = append(args, fmt.Sprintf("--proxy-server=%s", proxyURL))
	}
	args = append(args, targetURL)

	cmd, err := browserLauncherFn(browserExe, args)
	if err != nil {
		return fmt.Errorf("launch browser: %w", err)
	}

	procExit := make(chan error, 1)
	go func() {
		procExit <- cmd.Wait()
	}()

	defer closeBrowserGracefully(cmd, port)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	cdpEverConnected := false
	failedCDPPolls := 0
	startupDeadline := time.Now().Add(cdpStartupTimeout)
	procExited := false
	var procExitErr error

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("authentication timed out: %w", ctx.Err())

		case exitErr := <-procExit:
			procExited = true
			procExitErr = exitErr

		case <-ticker.C:
			resp, err := directCDPClient.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", port))
			if err == nil && resp != nil {
				_ = resp.Body.Close()
				cdpEverConnected = true
				failedCDPPolls = 0
			} else if cdpEverConnected {
				failedCDPPolls++
				if failedCDPPolls > 6 {
					return fmt.Errorf("browser closed by user before authentication completed")
				}
			} else if procExited && time.Now().After(startupDeadline) {
				if procExitErr != nil {
					return fmt.Errorf("browser closed by user or process exited unexpectedly: %w", procExitErr)
				}
				return fmt.Errorf("browser closed by user before authentication completed")
			}

			if targetCookieName == "SAPISID" {
				targetURL, pageID := getCDPTarget(port, "music.youtube.com")
				if pageID == "" || !strings.Contains(targetURL, "music.youtube.com") || strings.Contains(targetURL, "accounts.google.com") {
					continue
				}
				cookies, err := sendRawCDPGetAllCookies(port, pageID)
				if err != nil || !strings.Contains(cookies, "SAPISID=") {
					continue
				}
				if !strings.Contains(cookies, "__Secure-3PAPISID=") && !strings.Contains(cookies, "SID=") {
					continue
				}
				if err := SaveRawCookieMap(savePath, cookies); err != nil {
					return fmt.Errorf("save youtube credentials: %w", err)
				}
				return nil
			} else {
				cookieVal, err := QueryCDPCookie(port, targetCookieName)
				if err == nil && cookieVal != "" && len(cookieVal) > 10 {
					if err := SaveCookie(savePath, fmt.Sprintf("%s=%s", targetCookieName, cookieVal)); err != nil {
						return fmt.Errorf("save spotify credentials: %w", err)
					}
					return nil
				}
			}
		}
	}
}
