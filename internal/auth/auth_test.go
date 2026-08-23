package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestMain(m *testing.M) {
	// Guard all tests from popping browser windows or spawning external interactive processes
	origLookup := browserLookupFn
	origLauncher := browserLauncherFn
	browserLookupFn = func() (string, error) {
		return "", fmt.Errorf("browser launch disabled during tests")
	}
	browserLauncherFn = func(exe string, args []string) (*exec.Cmd, error) {
		return nil, fmt.Errorf("browser execution disabled during tests")
	}

	code := m.Run()

	browserLookupFn = origLookup
	browserLauncherFn = origLauncher
	os.Exit(code)
}

func TestAuthStatus_String(t *testing.T) {
	tests := []struct {
		name     string
		status   *AuthStatus
		expected string
	}{
		{
			name: "Spotify Fresh Auth",
			status: &AuthStatus{
				Platform:      PlatformSpotify,
				Authenticated: true,
				Path:          "output/auth/spotify_credentials.json",
				Cached:        false,
			},
			expected: "[AUTH SUCCESS] Spotify: Authenticated | Path: output/auth/spotify_credentials.json",
		},
		{
			name: "Spotify Cached Auth",
			status: &AuthStatus{
				Platform:      PlatformSpotify,
				Authenticated: true,
				Path:          "output/auth/spotify_credentials.json",
				Cached:        true,
			},
			expected: "[AUTH SUCCESS] Spotify: Already authenticated, credentials valid | Path: output/auth/spotify_credentials.json",
		},
		{
			name: "YouTube Fresh Auth",
			status: &AuthStatus{
				Platform:      PlatformYouTube,
				Authenticated: true,
				Path:          "output/auth/ytmusic_credentials.json",
				Cached:        false,
			},
			expected: "[AUTH SUCCESS] YouTube Music: Authenticated | Path: output/auth/ytmusic_credentials.json",
		},
		{
			name: "Unauthenticated with Message",
			status: &AuthStatus{
				Platform:      PlatformSpotify,
				Authenticated: false,
				Message:       "token expired",
			},
			expected: "[AUTH FAILED] Spotify: token expired",
		},
		{
			name:     "Nil Status",
			status:   nil,
			expected: "[AUTH] Status: Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.status.String()
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestNormalizePlatform(t *testing.T) {
	cases := map[string]Platform{
		"spotify":       PlatformSpotify,
		"SPO":           PlatformSpotify,
		"sp":            PlatformSpotify,
		"youtube-music": PlatformYouTube,
		"ytmusic":       PlatformYouTube,
		"youtube_music": PlatformYouTube,
		"ytm":           PlatformYouTube,
		"yt":            PlatformYouTube,
		"all":           PlatformAll,
		"both":          PlatformAll,
		"custom":        Platform("custom"),
	}

	for input, expected := range cases {
		got := NormalizePlatform(input)
		if got != expected {
			t.Errorf("NormalizePlatform(%q) = %q; want %q", input, got, expected)
		}
	}
}

func TestValidateSpotifyCookie_MockServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "get_access_token") {
			cookie := r.Header.Get("Cookie")
			if strings.Contains(cookie, "valid_cookie") && !strings.Contains(cookie, "invalid_cookie") {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"accessToken": "mock_access_token_123",
					"isAnonymous": false,
					"clientId":    "client_abc",
					"username":    "test_user_spotify",
				})
				return
			} else if strings.Contains(cookie, "anon_cookie") {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"accessToken": "mock_anon_token",
					"isAnonymous": true,
					"clientId":    "client_anon",
				})
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if strings.Contains(r.URL.Path, "v1/me") {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"display_name": "Test Spotify User",
				"id":           "spotify_user_001",
			})
			return
		}

		http.NotFound(w, r)
	}))
	defer ts.Close()

	resetEndpoints := SetEndpointsForTesting(ts.URL+"/get_access_token", ts.URL+"/v1/me", "")
	defer resetEndpoints()

	t.Run("Valid cookie authentication", func(t *testing.T) {
		status, err := ValidateSpotifyCookie("sp_dc=valid_cookie_12345678901234567890", "")
		if err != nil {
			t.Fatalf("ValidateSpotifyCookie failed: %v", err)
		}
		if !status.Authenticated {
			t.Errorf("expected Authenticated=true, got false")
		}
		if status.User != "test_user_spotify" {
			t.Errorf("expected User 'test_user_spotify', got %q", status.User)
		}
	})

	t.Run("Anonymous session rejected", func(t *testing.T) {
		status, err := ValidateSpotifyCookie("sp_dc=anon_cookie_12345678901234567890", "")
		if err == nil {
			t.Fatal("expected error for anonymous cookie, got nil")
		}
		if status != nil {
			t.Errorf("expected status nil on error, got: %+v", status)
		}
	})

	t.Run("Unauthorized response", func(t *testing.T) {
		status, err := ValidateSpotifyCookie("sp_dc=invalid_cookie_12345678901234567890", "")
		if err == nil {
			t.Fatal("expected error for invalid cookie, got nil")
		}
		if status != nil {
			t.Errorf("expected status nil on error, got: %+v", status)
		}
	})
}

func TestValidateYTMCookie_MockServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHdr := r.Header.Get("Authorization")
		cookieHdr := r.Header.Get("Cookie")

		if !strings.HasPrefix(authHdr, "SAPISIDHASH ") && !strings.Contains(cookieHdr, "SAPISID=") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if strings.Contains(cookieHdr, "valid_sapisid") {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"header": map[string]interface{}{
					"musicHeaderRenderer": map[string]interface{}{
						"title": map[string]interface{}{
							"runs": []map[string]interface{}{
								{"text": "Test Channel"},
							},
						},
					},
				},
			})
			return
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"responseContext": map[string]interface{}{
				"mainAppWebResponseContext": map[string]interface{}{
					"loggedOut": true,
				},
			},
		})
	}))
	defer ts.Close()

	resetEndpoints := SetEndpointsForTesting("", "", ts.URL+"/browse")
	defer resetEndpoints()

	t.Run("Valid YTM cookie", func(t *testing.T) {
		status, err := ValidateYTMCookieString("SAPISID=valid_sapisid_token; __Secure-3PAPISID=sec_token", "")
		if err != nil {
			t.Fatalf("ValidateYTMCookieString failed: %v", err)
		}
		if !status.Authenticated {
			t.Errorf("expected Authenticated=true, got false")
		}
		if status.User != "Test Channel" {
			t.Errorf("expected user 'Test Channel', got %q", status.User)
		}
	})

	t.Run("Valid YTM cookie file", func(t *testing.T) {
		tempDir := t.TempDir()
		credPath := filepath.Join(tempDir, "ytm.json")
		if err := SaveRawCookieMap(credPath, "SAPISID=valid_sapisid_token; __Secure-3PAPISID=sec_token"); err != nil {
			t.Fatalf("SaveRawCookieMap failed: %v", err)
		}
		status, err := ValidateYTMCookie(credPath, "")
		if err != nil {
			t.Fatalf("ValidateYTMCookie failed: %v", err)
		}
		if !status.Authenticated || status.User != "Test Channel" {
			t.Errorf("expected Authenticated=true and user 'Test Channel', got %+v", status)
		}
	})

	t.Run("Visitor session rejected", func(t *testing.T) {
		_, err := ValidateYTMCookieString("SAPISID=logged_out_sapisid", "")
		if err == nil {
			t.Fatal("expected error for visitor session, got nil")
		}
	})

	t.Run("Missing SAPISID rejected", func(t *testing.T) {
		_, err := ValidateYTMCookieString("OTHER_COOKIE=123", "")
		if err == nil {
			t.Fatal("expected error when SAPISID is missing")
		}
	})
}

func TestValidateYTMCookie_VisitorFalsePositiveRegression(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"responseContext": map[string]interface{}{
				"visitorData": "visitor_token_12345",
				"serviceTrackingParams": []map[string]interface{}{
					{"service": "GFEEDBACK"},
				},
			},
			"header": map[string]interface{}{
				"musicHeaderRenderer": map[string]interface{}{
					"title": map[string]interface{}{
						"runs": []map[string]interface{}{
							{"text": "My Real Account"},
						},
					},
				},
			},
		})
	}))
	defer ts.Close()

	resetEndpoints := SetEndpointsForTesting("", "", ts.URL+"/browse")
	defer resetEndpoints()

	status, err := ValidateYTMCookieString("SAPISID=valid_sapisid_token; __Secure-3PAPISID=sec_token", "")
	if err != nil {
		t.Fatalf("unexpected failure on response containing visitorData: %v", err)
	}
	if !status.Authenticated || status.User != "My Real Account" {
		t.Errorf("unexpected status: %+v", status)
	}
}

func TestCheckAuthentication_And_FastPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if strings.Contains(r.URL.Path, "token") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"accessToken": "mock_token",
				"isAnonymous": false,
				"username":    "FastPathUser",
			})
			return
		}
		if strings.Contains(r.URL.Path, "browse") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"header": map[string]interface{}{
					"musicHeaderRenderer": map[string]interface{}{
						"title": map[string]interface{}{
							"runs": []map[string]interface{}{{"text": "FastPathYTM"}},
						},
					},
				},
			})
			return
		}
	}))
	defer ts.Close()

	resetEndpoints := SetEndpointsForTesting(ts.URL+"/token", "", ts.URL+"/browse")
	defer resetEndpoints()

	tempDir := t.TempDir()
	spPath := filepath.Join(tempDir, "spotify.json")
	ytmPath := filepath.Join(tempDir, "ytmusic.json")

	_ = SaveCookie(spPath, "sp_dc=fast_path_cookie_val")
	_ = SaveRawCookieMap(ytmPath, "SAPISID=fast_path_ytm_val")

	sStatus, err := CheckAuthentication(PlatformSpotify, spPath, "")
	if err != nil || !sStatus.Authenticated {
		t.Fatalf("CheckAuthentication Spotify failed: %v", err)
	}

	yStatus, err := CheckAuthentication(PlatformYouTube, ytmPath, "")
	if err != nil || !yStatus.Authenticated {
		t.Fatalf("CheckAuthentication YouTube failed: %v", err)
	}
}

func TestCDPLogin_ProcessExitDetection(t *testing.T) {
	tempDir := t.TempDir()
	savePath := filepath.Join(tempDir, "auth.json")

	reset := SetBrowserLauncherForTesting(
		func() (string, error) {
			return "mock_browser", nil
		},
		func(exe string, args []string) (*exec.Cmd, error) {
			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				cmd = exec.Command("where.exe", "non_existent_123")
			} else {
				cmd = exec.Command("false")
			}
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			return cmd, nil
		},
	)
	defer reset()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := StartCDPLoginWithContext(ctx, "https://open.spotify.com", savePath, "sp_dc", "")
	if err == nil {
		t.Fatal("expected error on immediate process exit, got nil")
	}
}

func TestCookiePersistence(t *testing.T) {
	tempDir := t.TempDir()

	spPath := filepath.Join(tempDir, "sp.json")
	if err := SaveCookie(spPath, "sp_dc=AQB_secret_12345"); err != nil {
		t.Fatalf("SaveCookie failed: %v", err)
	}

	loadedSP, err := LoadCookie(spPath)
	if err != nil {
		t.Fatalf("LoadCookie failed: %v", err)
	}
	if loadedSP != "sp_dc=AQB_secret_12345" {
		t.Errorf("loaded cookie mismatch: %q", loadedSP)
	}

	ytmPath := filepath.Join(tempDir, "ytm.json")
	rawHeaders := "SAPISID=secret123; __Secure-3PAPISID=sec456; SID=sid789"
	if err := SaveRawCookieMap(ytmPath, rawHeaders); err != nil {
		t.Fatalf("SaveRawCookieMap failed: %v", err)
	}

	loadedYTM, err := LoadCookie(ytmPath)
	if err != nil {
		t.Fatalf("LoadCookie YTM failed: %v", err)
	}
	if !strings.Contains(loadedYTM, "SAPISID=secret123") {
		t.Errorf("loaded YTM cookie mismatch: %q", loadedYTM)
	}
}

func TestFindBrowserPath(t *testing.T) {
	path, err := FindBrowserPath()
	if err == nil {
		if path == "" {
			t.Errorf("expected non-empty browser path when err is nil")
		}
	}
}

func TestLoginPlatform_CDPLogin(t *testing.T) {
	reset := SetBrowserLauncherForTesting(
		func() (string, error) {
			return "mock_browser", nil
		},
		func(exe string, args []string) (*exec.Cmd, error) {
			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				cmd = exec.Command("where.exe", "non_existent_123")
			} else {
				cmd = exec.Command("false")
			}
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			return cmd, nil
		},
	)
	defer reset()

	tempDir := t.TempDir()
	authPath := filepath.Join(tempDir, "cdp_sp.json")

	_, err := LoginPlatform(PlatformSpotify, WithAuthPath(authPath), WithForce(true))
	if err == nil {
		t.Fatal("expected error on CDP login with exiting process, got nil")
	}
}

func TestSanitizeSensitive(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Empty string", "", ""},
		{"Normal string without secrets", "Normal text message without tokens", "Normal text message without tokens"},
		{"sp_dc cookie masking", "Error with cookie sp_dc=AQD88O7UylJ2RjsfOCOSOJ7jW6G3AZqc6H6arQpA in request", "Error with cookie sp_dc=[REDACTED] in request"},
		{"SAPISID cookie masking", "Cookie: SAPISID=vY7k4L-kM99xABCDEF12345; SID=123", "Cookie: SAPISID=[REDACTED]; SID=[REDACTED]"},
		{"Secure cookie masking", "Cookie: __Secure-3PAPISID=wX8y9Z0123456789; other=ok", "Cookie: __Secure-3PAPISID=[REDACTED]; other=ok"},
		{"SAPISIDHASH authorization header masking", "Authorization: SAPISIDHASH 1724345678_0123456789abcdef", "Authorization: SAPISIDHASH [REDACTED]"},
		{"Bearer token masking", "Authorization: Bearer BQD88O7UylJ2RjsfOCOSOJ7jW6G3AZqc6H6arQpA", "Authorization: Bearer [REDACTED]"},
		{"login_info cookie masking", "Cookie: login_info=secret_value_123", "Cookie: login_info=[REDACTED]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeSensitive(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeSensitive(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestAuthStatus_String_SensitiveSanitization(t *testing.T) {
	status := &AuthStatus{
		Platform:      PlatformSpotify,
		Authenticated: false,
		Message:       "failed to authenticate with sp_dc=AQD88O7UylJ2RjsfOCOSOJ7jW6G3AZqc6H6arQpA",
	}

	got := status.String()
	if strings.Contains(got, "AQD88O7UylJ2RjsfOCOSOJ7jW6G3AZqc6H6arQpA") {
		t.Errorf("status string leaked raw sp_dc token: %s", got)
	}
	if !strings.Contains(got, "sp_dc=[REDACTED]") {
		t.Errorf("expected [REDACTED] in status string, got: %s", got)
	}
}

func TestSaveCredentials_FilePermissions(t *testing.T) {
	tempDir := t.TempDir()
	spPath := filepath.Join(tempDir, "auth", "spotify_credentials.json")
	ytmPath := filepath.Join(tempDir, "auth", "ytmusic_credentials.json")

	if err := SaveCookie(spPath, "sp_dc=test_secret_123"); err != nil {
		t.Fatalf("SaveCookie failed: %v", err)
	}
	if err := SaveRawCookieMap(ytmPath, "SAPISID=test_ytm_123"); err != nil {
		t.Fatalf("SaveRawCookieMap failed: %v", err)
	}

	if runtime.GOOS != "windows" {
		spInfo, err := os.Stat(spPath)
		if err != nil {
			t.Fatalf("os.Stat(spPath) failed: %v", err)
		}
		if spInfo.Mode().Perm() != 0600 {
			t.Errorf("expected spPath permissions 0600, got %v", spInfo.Mode().Perm())
		}

		ytmInfo, err := os.Stat(ytmPath)
		if err != nil {
			t.Fatalf("os.Stat(ytmPath) failed: %v", err)
		}
		if ytmInfo.Mode().Perm() != 0600 {
			t.Errorf("expected ytmPath permissions 0600, got %v", ytmInfo.Mode().Perm())
		}
	}
}

func TestPlatformDisplayName(t *testing.T) {
	tests := []struct {
		platform Platform
		expected string
	}{
		{PlatformSpotify, "Spotify"},
		{PlatformYouTube, "YouTube Music"},
		{PlatformAll, "All Platforms"},
		{Platform("custom_service"), "custom_service"},
	}

	for _, tt := range tests {
		got := PlatformDisplayName(tt.platform)
		if got != tt.expected {
			t.Errorf("PlatformDisplayName(%q) = %q; want %q", tt.platform, got, tt.expected)
		}
	}
}

func TestParseCookie(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"   ", ""},
		{"sp_dc=AQB12345", "sp_dc=AQB12345"},
		{"AQB12345678901234567890", "sp_dc=AQB12345678901234567890"},
		{"short", "short"},
	}

	for _, tt := range tests {
		got := ParseCookie(tt.input)
		if got != tt.expected {
			t.Errorf("ParseCookie(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestLoadCookie(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("JSON with Cookie key", func(t *testing.T) {
		p := filepath.Join(tempDir, "cookie1.json")
		_ = os.WriteFile(p, []byte(`{"Cookie": "sp_dc=val1"}`), 0600)
		c, err := LoadCookie(p)
		if err != nil || c != "sp_dc=val1" {
			t.Errorf("got %q, err: %v", c, err)
		}
	})

	t.Run("JSON with sp_dc key and long token", func(t *testing.T) {
		p := filepath.Join(tempDir, "cookie2.json")
		_ = os.WriteFile(p, []byte(`{"sp_dc": "1234567890123456789012"}`), 0600)
		c, err := LoadCookie(p)
		if err != nil || c != "sp_dc=1234567890123456789012" {
			t.Errorf("got %q, err: %v", c, err)
		}
	})

	t.Run("Plaintext file", func(t *testing.T) {
		p := filepath.Join(tempDir, "plain.txt")
		_ = os.WriteFile(p, []byte("SAPISID=12345"), 0600)
		c, err := LoadCookie(p)
		if err != nil || c != "SAPISID=12345" {
			t.Errorf("got %q, err: %v", c, err)
		}
	})

	t.Run("Non-existent file", func(t *testing.T) {
		_, err := LoadCookie("non_existent_cookie_file.json")
		if err == nil {
			t.Fatal("expected error for non-existent file, got nil")
		}
	})
}

func TestCheckAuthentication_ErrorsAndDefaults(t *testing.T) {
	_, err := CheckAuthentication(Platform("unsupported"), "", "")
	if err == nil {
		t.Fatal("expected error for unsupported platform, got nil")
	}

	_, err = CheckAuthentication(PlatformSpotify, "non_existent_path.json", "")
	if err == nil {
		t.Fatal("expected error for non-existent path, got nil")
	}

	_, err = CheckAuthentication(PlatformYouTube, "non_existent_path.json", "")
	if err == nil {
		t.Fatal("expected error for non-existent path, got nil")
	}
}

func TestEnsureAuthenticated_PlatformAll(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if strings.Contains(r.URL.Path, "get_access_token") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"accessToken": "mock_sp_token",
				"isAnonymous": false,
				"username":    "SpotifyUser",
			})
			return
		}
		if strings.Contains(r.URL.Path, "browse") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"header": map[string]interface{}{
					"musicHeaderRenderer": map[string]interface{}{
						"title": map[string]interface{}{
							"runs": []map[string]interface{}{{"text": "YTMUser"}},
						},
					},
				},
			})
			return
		}
	}))
	defer ts.Close()

	resetEndpoints := SetEndpointsForTesting(ts.URL+"/get_access_token", "", ts.URL+"/browse")
	defer resetEndpoints()

	tempDir := t.TempDir()
	spPath := filepath.Join(tempDir, "spotify.json")
	ytmPath := filepath.Join(tempDir, "ytmusic.json")
	_ = SaveCookie(spPath, "sp_dc=valid_sp_cookie")
	_ = SaveRawCookieMap(ytmPath, "SAPISID=valid_ytm_cookie")

	sStatus, err := EnsureAuthenticated(PlatformSpotify, spPath, "")
	if err != nil || !sStatus.Authenticated {
		t.Fatalf("Ensure Spotify failed: %v", err)
	}

	yStatus, err := EnsureAuthenticated(PlatformYouTube, ytmPath, "")
	if err != nil || !yStatus.Authenticated {
		t.Fatalf("Ensure YouTube failed: %v", err)
	}

	allStatus, err := EnsureAuthenticated(PlatformAll, "", "")
	_ = allStatus
}

func TestParseYTMAuthResponse_DetailedDirect(t *testing.T) {
	ok, user, err := parseYTMAuthResponse([]byte("invalid json"))
	if ok || err == nil {
		t.Errorf("expected error for invalid json, got ok=%v, user=%q", ok, user)
	}

	errJSON := `{"error": {"code": 403, "message": "Forbidden"}}`
	ok, _, err = parseYTMAuthResponse([]byte(errJSON))
	if ok || err == nil || !strings.Contains(err.Error(), "code 403") {
		t.Errorf("expected code 403 error, got: %v", err)
	}

	loggedOutJSON := `{"responseContext": {"mainAppWebResponseContext": {"loggedOut": true}}}`
	ok, _, err = parseYTMAuthResponse([]byte(loggedOutJSON))
	if ok || err == nil || !strings.Contains(err.Error(), "visitor session") {
		t.Errorf("expected visitor session error, got: %v", err)
	}

	loggedInFalseJSON := `{"responseContext": {"LOGGED_IN": false}}`
	ok, _, err = parseYTMAuthResponse([]byte(loggedInFalseJSON))
	if ok || err == nil || !strings.Contains(err.Error(), "LOGGED_IN: false") {
		t.Errorf("expected LOGGED_IN false error, got: %v", err)
	}

	headerJSON := `{"header": {"musicHeaderRenderer": {"title": {"runs": [{"text": "Authenticated User"}]}}}}`
	ok, user, err = parseYTMAuthResponse([]byte(headerJSON))
	if !ok || err != nil || user != "Authenticated User" {
		t.Errorf("expected ok with user header, got ok=%v, user=%q, err=%v", ok, user, err)
	}

	contentsJSON := `{"contents": {"tab": 1}}`
	ok, _, err = parseYTMAuthResponse([]byte(contentsJSON))
	if ok || err == nil || !strings.Contains(err.Error(), "no authenticated user context found") {
		t.Errorf("expected unauthenticated error for anonymous contents without user header, got ok=%v, err=%v", ok, err)
	}

	emptyJSON := `{}`
	ok, _, err = parseYTMAuthResponse([]byte(emptyJSON))
	if ok || err == nil || !strings.Contains(err.Error(), "no authenticated user context found") {
		t.Errorf("expected no user context error, got ok=%v, err=%v", ok, err)
	}
}

func TestLoginPlatform_CachedFastPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"accessToken": "mock_token_fast",
			"isAnonymous": false,
			"username":    "CachedUser",
		})
	}))
	defer ts.Close()

	resetEndpoints := SetEndpointsForTesting(ts.URL+"/token", "", "")
	defer resetEndpoints()

	tempDir := t.TempDir()
	authPath := filepath.Join(tempDir, "cached_sp.json")
	_ = SaveCookie(authPath, "sp_dc=fast_path_secret_12345")

	status, err := LoginPlatform(PlatformSpotify, WithAuthPath(authPath), WithForce(false))
	if err != nil {
		t.Fatalf("LoginPlatform cached failed: %v", err)
	}
	if !status.Authenticated || !status.Cached {
		t.Errorf("expected Authenticated=true, Cached=true: %+v", status)
	}
}

func TestValidateSpotifyCookie_WebFallbackAndFailures(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	resetEndpoints := SetEndpointsForTesting(ts.URL+"/token", "", "")
	defer resetEndpoints()

	_, err := ValidateSpotifyCookie("sp_dc=failing_cookie_1234567890", "")
	if err == nil {
		t.Fatal("expected error when token endpoint fails")
	}
}

func TestLoginPlatform_OptionsAndEdgeCases(t *testing.T) {
	_, err := LoginPlatform(Platform("unsupported"))
	if err == nil {
		t.Fatal("expected error for unsupported platform in LoginPlatform")
	}

	opts := []Option{
		WithTimeout(10 * time.Second),
		WithProxy("http://proxy.test:8080"),
		WithForce(true),
		WithAuthPath("custom/path.json"),
	}
	cfg := LoginOptions{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.Timeout != 10*time.Second || cfg.ProxyURL != "http://proxy.test:8080" || !cfg.Force || cfg.AuthPath != "custom/path.json" {
		t.Errorf("options not set correctly: %+v", cfg)
	}
}

func TestCDP_WebSocket_Operations(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen tcp: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	var upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/devtools/page/test_cookies", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var msg map[string]interface{}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			_ = conn.WriteJSON(map[string]interface{}{
				"id": 1,
				"result": map[string]interface{}{
					"cookies": []map[string]string{
						{"name": "test_cookie", "value": "test_val"},
					},
				},
			})
		}
	})

	mux.HandleFunc("/devtools/page/test_eval", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var msg map[string]interface{}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			_ = conn.WriteJSON(map[string]interface{}{
				"id": 2,
				"result": map[string]interface{}{
					"result": map[string]interface{}{
						"type":  "string",
						"value": "eval_output",
					},
				},
			})
		}
	})

	mux.HandleFunc("/devtools/page/test_error", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var msg map[string]interface{}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			_ = conn.WriteJSON(map[string]interface{}{
				"id": msg["id"],
				"error": map[string]interface{}{
					"code":    -32000,
					"message": "CDP internal test error",
				},
			})
		}
	})

	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(ln) }()
	defer server.Close()

	t.Run("fetchCDPCookies success", func(t *testing.T) {
		cookies, err := fetchCDPCookies(port, "test_cookies")
		if err != nil {
			t.Fatalf("fetchCDPCookies failed: %v", err)
		}
		if len(cookies) != 1 || cookies[0].Name != "test_cookie" {
			t.Fatalf("unexpected cookies: %+v", cookies)
		}
	})

	t.Run("EvaluateCDPResult success", func(t *testing.T) {
		val, err := EvaluateCDPResult(port, "test_eval", "1+1")
		if err != nil {
			t.Fatalf("EvaluateCDPResult failed: %v", err)
		}
		if val != "eval_output" {
			t.Fatalf("expected 'eval_output', got %q", val)
		}
	})

	t.Run("CDP Error response handling", func(t *testing.T) {
		_, err := fetchCDPCookies(port, "test_error")
		if err == nil {
			t.Fatal("expected error from CDP error response")
		}
		_, err = EvaluateCDPResult(port, "test_error", "foo")
		if err == nil {
			t.Fatal("expected error from CDP error response")
		}
	})

	t.Run("Context cancellation handling", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately
		_, err := fetchCDPCookiesWithContext(ctx, port, "test_cookies")
		if err == nil {
			t.Fatal("expected error on cancelled context")
		}
		_, err = EvaluateCDPResultWithContext(ctx, port, "test_eval", "1+1")
		if err == nil {
			t.Fatal("expected error on cancelled context")
		}
	})

	t.Run("Connection failure handling", func(t *testing.T) {
		_, err := fetchCDPCookies(1, "non_existent")
		if err == nil {
			t.Fatal("expected error connecting to closed port")
		}
	})
}

func TestValidateSpotifyCookie_AllBranches(t *testing.T) {
	t.Run("Invalid short cookie without sp_dc", func(t *testing.T) {
		_, err := ValidateSpotifyCookie("short", "")
		if err == nil {
			t.Fatal("expected error for invalid cookie format")
		}
	})

	t.Run("User display_name fallback to user id", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "get_access_token") {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"accessToken": "mock_token_for_id",
					"isAnonymous": false,
					"username":    "",
				})
				return
			}
			if strings.Contains(r.URL.Path, "v1/me") {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"display_name": "",
					"id":           "user_id_999",
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		resetEndpoints := SetEndpointsForTesting(ts.URL+"/get_access_token", ts.URL+"/v1/me", "")
		defer resetEndpoints()

		status, err := ValidateSpotifyCookie("sp_dc=cookie_for_id", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status.User != "user_id_999" {
			t.Errorf("expected user 'user_id_999', got %q", status.User)
		}
	})
}

func TestValidateSpotifyAuth_FileErrors(t *testing.T) {
	tempDir := t.TempDir()

	emptyFile := filepath.Join(tempDir, "empty.json")
	_ = os.WriteFile(emptyFile, []byte(""), 0644)
	_, err := ValidateSpotifyAuth(emptyFile, "")
	if err == nil {
		t.Fatal("expected error for empty file")
	}

	_, err = ValidateSpotifyAuth("missing_file_xyz.json", "")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestMockCDP_FullFlows(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen tcp: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	var upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		targetJSON := fmt.Sprintf(`[
			{"id": "page_sp", "type": "page", "url": "https://open.spotify.com", "webSocketDebuggerUrl": "ws://127.0.0.1:%d/devtools/page/page_sp"},
			{"id": "page_ytm", "type": "page", "url": "https://music.youtube.com", "webSocketDebuggerUrl": "ws://127.0.0.1:%d/devtools/page/page_ytm"}
		]`, port, port)
		_, _ = w.Write([]byte(targetJSON))
	})

	mux.HandleFunc("/devtools/page/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var msg map[string]interface{}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			method, _ := msg["method"].(string)
			if method == "Runtime.evaluate" {
				_ = conn.WriteJSON(map[string]interface{}{
					"id": 2,
					"result": map[string]interface{}{
						"result": map[string]interface{}{
							"type":  "string",
							"value": "captured_sp_token",
						},
					},
				})
			} else {
				_ = conn.WriteJSON(map[string]interface{}{
					"id": 1,
					"result": map[string]interface{}{
						"cookies": []map[string]string{
							{"name": "sp_dc", "value": "captured_sp_secret"},
							{"name": "SAPISID", "value": "captured_sapisid"},
							{"name": "__Secure-3PAPISID", "value": "captured_secure_sapisid"},
						},
					},
				})
			}
		}
	})

	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(ln) }()
	defer server.Close()

	t.Run("fetchCDPCookies success", func(t *testing.T) {
		cookies, err := fetchCDPCookies(port, "page_sp")
		if err != nil {
			t.Fatalf("fetchCDPCookies failed: %v", err)
		}
		if len(cookies) < 2 {
			t.Fatalf("expected cookies, got %d", len(cookies))
		}
	})

	t.Run("QueryCDPCookie success", func(t *testing.T) {
		val, err := QueryCDPCookie(port, "sp_dc")
		if err != nil {
			t.Fatalf("QueryCDPCookie failed: %v", err)
		}
		if val != "captured_sp_secret" {
			t.Errorf("expected 'captured_sp_secret', got %q", val)
		}

		valMissing, err := QueryCDPCookie(port, "non_existent_cookie")
		if err != nil || valMissing != "" {
			t.Errorf("expected empty string for missing cookie, got %q, err %v", valMissing, err)
		}
	})

	t.Run("QueryAllCDPCookies success", func(t *testing.T) {
		cookieHeader, err := QueryAllCDPCookies(port)
		if err != nil {
			t.Fatalf("QueryAllCDPCookies failed: %v", err)
		}
		if !strings.Contains(cookieHeader, "SAPISID=captured_sapisid") {
			t.Errorf("expected SAPISID in cookieHeader, got %q", cookieHeader)
		}
	})

	t.Run("StartCDPLogin and StartCDPLoginWithContext with mock browser", func(t *testing.T) {
		tempDir := t.TempDir()
		origPort := cdpPort
		defer func() {
			cdpPort = origPort
		}()

		cdpPort = port

		ytmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"header": map[string]interface{}{
					"musicHeaderRenderer": map[string]interface{}{
						"title": map[string]interface{}{
							"runs": []map[string]interface{}{
								{"text": "Mock CDP YTM User"},
							},
						},
					},
				},
			})
		}))
		defer ytmServer.Close()

		spotifyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"display_name": "Mock Spotify User",
				"id":           "mock_sp_user",
				"accessToken":  "captured_sp_token",
				"isAnonymous":  false,
			})
		}))
		defer spotifyServer.Close()

		resetEndpoints := SetEndpointsForTesting(spotifyServer.URL+"/token", spotifyServer.URL+"/v1/me", ytmServer.URL+"/browse")
		defer resetEndpoints()

		resetLauncher := SetBrowserLauncherForTesting(
			func() (string, error) {
				return "mock_browser", nil
			},
			func(exe string, args []string) (*exec.Cmd, error) {
				var cmd *exec.Cmd
				if runtime.GOOS == "windows" {
					cmd = exec.Command("ping", "127.0.0.1", "-n", "30")
				} else {
					cmd = exec.Command("sleep", "30")
				}
				if err := cmd.Start(); err != nil {
					return nil, err
				}
				return cmd, nil
			},
		)
		defer resetLauncher()

		// Spotify CDP Login
		spSavePath := filepath.Join(tempDir, "spotify_captured.json")
		ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel1()
		err := StartCDPLoginWithContext(ctx1, "https://open.spotify.com", spSavePath, "sp_dc", "")
		if err != nil {
			t.Fatalf("StartCDPLoginWithContext Spotify failed: %v", err)
		}
		spCookie, _ := LoadCookie(spSavePath)
		if !strings.Contains(spCookie, "captured_sp_secret") {
			t.Errorf("Spotify cookie was not captured: %q", spCookie)
		}

		// YouTube Music CDP Login
		ytmSavePath := filepath.Join(tempDir, "ytm_captured.json")
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		err = StartCDPLoginWithContext(ctx2, "https://music.youtube.com", ytmSavePath, "SAPISID", "")
		if err != nil {
			t.Fatalf("StartCDPLoginWithContext YouTube failed: %v", err)
		}
		ytmCookie, _ := LoadCookie(ytmSavePath)
		if !strings.Contains(ytmCookie, "SAPISID=captured_sapisid") {
			t.Errorf("YTM cookie was not captured: %q", ytmCookie)
		}

		// StartCDPLogin wrapper
		spSavePath2 := filepath.Join(tempDir, "spotify_captured_2.json")
		if err := StartCDPLogin("https://open.spotify.com", spSavePath2, "sp_dc"); err != nil {
			t.Fatalf("StartCDPLogin wrapper failed: %v", err)
		}
	})
}

func TestValidateCredentials_Facade(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"accessToken": "mock_token_facade",
			"isAnonymous": false,
			"username":    "FacadeUser",
		})
	}))
	defer ts.Close()

	resetEndpoints := SetEndpointsForTesting(ts.URL, "", "")
	defer resetEndpoints()

	tempDir := t.TempDir()
	p := filepath.Join(tempDir, "sp.json")
	_ = SaveSpotifyCredentials(p, "sp_dc=valid_facade_sp_dc_1234567890")

	status, err := ValidateCredentials(PlatformSpotify, p, "")
	if err != nil || !status.Authenticated {
		t.Fatalf("ValidateCredentials failed: %v", err)
	}
	if status.User != "FacadeUser" {
		t.Errorf("expected FacadeUser, got %s", status.User)
	}
}

func TestCredentialHelpers_SpotifyAndYTM(t *testing.T) {
	tempDir := t.TempDir()

	spPath := filepath.Join(tempDir, "sp_helper.json")
	if err := SaveSpotifyCredentials(spPath, "sp_dc=sp_token_1234567890"); err != nil {
		t.Fatalf("SaveSpotifyCredentials failed: %v", err)
	}
	loadedSP, err := LoadSpotifyCredentials(spPath)
	if err != nil || loadedSP != "sp_dc=sp_token_1234567890" {
		t.Fatalf("LoadSpotifyCredentials failed: %v, got %q", err, loadedSP)
	}

	ytmPath := filepath.Join(tempDir, "ytm_helper.json")
	if err := SaveYTMCredentials(ytmPath, "SAPISID=ytm_token_123"); err != nil {
		t.Fatalf("SaveYTMCredentials failed: %v", err)
	}
	loadedYTM, err := LoadYTMCredentials(ytmPath)
	if err != nil || !strings.Contains(loadedYTM, "SAPISID=ytm_token_123") {
		t.Fatalf("LoadYTMCredentials failed: %v, got %q", err, loadedYTM)
	}
}

func TestGetSpotifyAccessToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie := r.Header.Get("Cookie")
		if strings.Contains(cookie, "valid_access_sp_dc") {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"accessToken": "acquired_access_token_xyz_123",
				"isAnonymous": false,
			})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	resetEndpoints := SetEndpointsForTesting(ts.URL, "", "")
	defer resetEndpoints()

	tempDir := t.TempDir()
	p := filepath.Join(tempDir, "sp_access.json")
	_ = SaveCookie(p, "sp_dc=valid_access_sp_dc_1234567890")

	token, err := GetSpotifyAccessToken(p, "")
	if err != nil {
		t.Fatalf("GetSpotifyAccessToken failed: %v", err)
	}
	if token != "acquired_access_token_xyz_123" {
		t.Errorf("expected acquired_access_token_xyz_123, got %s", token)
	}

	// Test invalid cookie
	_, err = GetSpotifyAccessTokenFromCookie("invalid_short", "")
	if err == nil {
		t.Fatal("expected error for invalid cookie, got nil")
	}
}

func TestPathTraversal_Cleaning(t *testing.T) {
	tempDir := t.TempDir()
	nestedDir := filepath.Join(tempDir, "a", "b")
	_ = os.MkdirAll(nestedDir, 0700)

	traversalPath := filepath.Join(nestedDir, "..", "..", "credentials_clean.json")
	if err := SaveCookie(traversalPath, "sp_dc=traversal_cookie_1234567890"); err != nil {
		t.Fatalf("SaveCookie with relative dots failed: %v", err)
	}

	expectedClean := filepath.Clean(traversalPath)
	if _, err := os.Stat(expectedClean); err != nil {
		t.Fatalf("expected file to exist at clean path %s: %v", expectedClean, err)
	}

	loaded, err := LoadCookie(traversalPath)
	if err != nil || loaded != "sp_dc=traversal_cookie_1234567890" {
		t.Fatalf("LoadCookie failed on traversal path: %v", err)
	}
}

func TestSanitizeSensitive_AdvancedTokens(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "JSON access token",
			input:    `{"accessToken": "BQC_secret_token_12345", "token_type": "Bearer"}`,
			expected: `{"accessToken": "[REDACTED]", "token_type": "Bearer"}`,
		},
		{
			name:     "JSON refresh token",
			input:    `{"refreshToken": "AQD_refresh_secret", "scope": "playlist-modify"}`,
			expected: `{"refreshToken": "[REDACTED]", "scope": "playlist-modify"}`,
		},
		{
			name:     "Basic Auth Header",
			input:    `Authorization: Basic dXNlcjpwYXNz`,
			expected: `Authorization: Basic [REDACTED]`,
		},
		{
			name:     "Cookie with CSRF and Auth token",
			input:    `Cookie: csrf_token=sec_csrf_999; auth_token=sec_auth_888; session=ok`,
			expected: `Cookie: csrf_token=[REDACTED]; auth_token=[REDACTED]; session=ok`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeSensitive(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeSensitive(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGetProfileDir(t *testing.T) {
	spDir := GetProfileDir(PlatformSpotify, "output/auth/spotify_credentials.json")
	if !strings.Contains(spDir, ".chrome_spotify") {
		t.Errorf("expected .chrome_spotify in path, got: %s", spDir)
	}

	ytmDir := GetProfileDir(PlatformYouTube, "output/auth/ytmusic_credentials.json")
	if !strings.Contains(ytmDir, ".chrome_ytmusic") {
		t.Errorf("expected .chrome_ytmusic in path, got: %s", ytmDir)
	}
}

func TestRefreshSpotifyTokenHeadless_Mock(t *testing.T) {
	tempDir := t.TempDir()
	authPath := filepath.Join(tempDir, "spotify_credentials.json")
	profileDir := filepath.Join(tempDir, ".chrome_spotify")
	_ = os.MkdirAll(profileDir, 0700)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen tcp: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	origPort := cdpPort
	cdpPort = port
	defer func() {
		cdpPort = origPort
	}()

	var upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		targetJSON := fmt.Sprintf(`[{"id": "page_sp", "type": "page", "url": "https://open.spotify.com/", "webSocketDebuggerUrl": "ws://127.0.0.1:%d/devtools/page/page_sp"}]`, port)
		_, _ = w.Write([]byte(targetJSON))
	})

	mux.HandleFunc("/devtools/page/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var msg map[string]interface{}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			method, _ := msg["method"].(string)
			if method == "Runtime.evaluate" {
				_ = conn.WriteJSON(map[string]interface{}{
					"id": 2,
					"result": map[string]interface{}{
						"result": map[string]interface{}{
							"type":  "string",
							"value": "{\"accessToken\":\"headless_refreshed_token\",\"isAnonymous\":false}",
						},
					},
				})
			} else {
				_ = conn.WriteJSON(map[string]interface{}{
					"id": 1,
					"result": map[string]interface{}{
						"cookies": []map[string]string{
							{"name": "sp_dc", "value": "headless_cookie"},
						},
					},
				})
			}
		}
	})

	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(ln) }()
	defer server.Close()

	spServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"display_name": "Headless User",
			"id":           "headless_user_1",
			"accessToken":  "headless_refreshed_token",
			"isAnonymous":  false,
		})
	}))
	defer spServer.Close()

	_ = SaveCookie(authPath, "sp_dc=headless_cookie_val_123456789")

	resetEndpoints := SetEndpointsForTesting(spServer.URL+"/token", spServer.URL+"/v1/me", "")
	defer resetEndpoints()

	resetLauncher := SetBrowserLauncherForTesting(
		func() (string, error) {
			return "mock_browser", nil
		},
		func(exe string, args []string) (*exec.Cmd, error) {
			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				cmd = exec.Command("ping", "127.0.0.1", "-n", "30")
			} else {
				cmd = exec.Command("sleep", "30")
			}
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			return cmd, nil
		},
	)
	defer resetLauncher()

	token, err := RefreshSpotifyTokenHeadless("", authPath)
	if err != nil {
		t.Fatalf("RefreshSpotifyTokenHeadless failed: %v", err)
	}
	if token != "headless_refreshed_token" {
		t.Errorf("expected 'headless_refreshed_token', got %q", token)
	}
}
