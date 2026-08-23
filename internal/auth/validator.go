package auth

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	spotifyTokenEndpoint = "https://open.spotify.com/api/token"
	spotifyMeEndpoint    = "https://api.spotify.com/v1/me"
	ytmBrowseEndpoint    = "https://music.youtube.com/youtubei/v1/browse?prettyPrint=false"
)

func newAuthHTTPClient(proxyURL string) (*http.Client, func()) {
	transport := &http.Transport{
		Proxy: ProxyFunc(proxyURL),
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
	}
	return client, transport.CloseIdleConnections
}

// SetEndpointsForTesting overrides authentication validation endpoints and returns a cleanup func
func SetEndpointsForTesting(spToken, spMe, ytmBrowse string) func() {
	origToken := spotifyTokenEndpoint
	origMe := spotifyMeEndpoint
	origYTM := ytmBrowseEndpoint
	if spToken != "" {
		spotifyTokenEndpoint = spToken
	}
	if spMe != "" {
		spotifyMeEndpoint = spMe
	}
	if ytmBrowse != "" {
		ytmBrowseEndpoint = ytmBrowse
	}
	return func() {
		spotifyTokenEndpoint = origToken
		spotifyMeEndpoint = origMe
		ytmBrowseEndpoint = origYTM
	}
}

// GetSpotifyAccessToken loads Spotify credentials from path and retrieves a valid web player access token
func GetSpotifyAccessToken(path string, proxyURL string) (string, error) {
	cleanPath := filepath.Clean(path)
	cookie, err := LoadCookie(cleanPath)
	if err != nil {
		return "", err
	}
	if cookie == "" {
		return "", fmt.Errorf("[AUTH] Spotify credentials file is empty at %s. Run 'playlistsync login spotify' to authenticate", cleanPath)
	}
	return GetSpotifyAccessTokenFromCookie(cookie, proxyURL)
}

func validateSpotifyAccessToken(token string, proxyURL string) (*AuthStatus, error) {
	client, cleanup := newAuthHTTPClient(proxyURL)
	defer cleanup()

	meReq, err := http.NewRequest("GET", spotifyMeEndpoint, nil)
	if err != nil {
		return nil, err
	}
	meReq.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(meReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("spotify token returned HTTP %d", resp.StatusCode)
	}

	var meData struct {
		DisplayName string `json:"display_name"`
		ID          string `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&meData)
	user := meData.DisplayName
	if user == "" {
		user = meData.ID
	}
	return &AuthStatus{
		Platform:      PlatformSpotify,
		Authenticated: true,
		User:          user,
	}, nil
}

// GetSpotifyAccessTokenFromCookie requests an access token from Spotify Web Player using raw cookie
func GetSpotifyAccessTokenFromCookie(cookie string, proxyURL string) (string, error) {
	token, _, err := getSpotifyAccessTokenDetails(cookie, proxyURL)
	return token, err
}

// ExtractSAPISID extracts SAPISID from a raw Cookie header string.
// Priority: SAPISID > __Secure-3PAPISID > __Secure-1PAPISID, stripping quotes and whitespace.
func ExtractSAPISID(cookieHeader string) string {
	cookieHeader = strings.TrimSpace(cookieHeader)
	if cookieHeader == "" {
		return ""
	}
	cookies := make(map[string]string)
	for _, part := range strings.Split(cookieHeader, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if k, v, ok := strings.Cut(part, "="); ok {
			cookies[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), "\"")
		}
	}

	for _, key := range []string{"SAPISID", "__Secure-3PAPISID", "__Secure-1PAPISID"} {
		if val, ok := cookies[key]; ok && val != "" {
			return val
		}
	}
	return ""
}

// LoadAuthHeaders loads auth credentials from a file path supporting JSON dictionary
// (case-insensitive keys) and plaintext cookie formats.
func LoadAuthHeaders(path string) (map[string]string, error) {
	cleanPath := filepath.Clean(path)
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil, fmt.Errorf("credentials file is empty at %s", cleanPath)
	}

	headers := make(map[string]string)
	res := make(map[string]string)

	if strings.HasPrefix(content, "{") || strings.HasSuffix(strings.ToLower(cleanPath), ".json") {
		var rawMap map[string]string
		if err := json.Unmarshal(data, &rawMap); err != nil {
			return nil, fmt.Errorf("credentials file corrupted at %s: %w", cleanPath, err)
		}
		for k, v := range rawMap {
			headers[strings.ToLower(k)] = v
			res[k] = v
		}
	} else {
		headers["cookie"] = content
		res["Cookie"] = content
	}

	if cookie, ok := headers["cookie"]; ok && cookie != "" {
		res["Cookie"] = cookie
	}
	if ua, ok := headers["user-agent"]; ok && ua != "" {
		res["User-Agent"] = ua
	} else if res["User-Agent"] == "" {
		res["User-Agent"] = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
	}
	if auth, ok := headers["authorization"]; ok && auth != "" {
		res["Authorization"] = auth
	}
	return res, nil
}

func getSpotifyAccessTokenDetails(cookie string, proxyURL string) (string, string, error) {
	cookieStr := ParseCookie(cookie)
	if !strings.Contains(cookieStr, "sp_dc=") || len(cookieStr) < 10 {
		return "", "", fmt.Errorf("[AUTH] Invalid Spotify cookie format. Run 'playlistsync login spotify' to re-authenticate")
	}

	client, cleanup := newAuthHTTPClient(proxyURL)
	defer cleanup()

	targetURL := spotifyTokenEndpoint
	if strings.Contains(targetURL, "/api/token") && !strings.Contains(targetURL, "?") {
		totp, ver := GenerateSpotifyTOTP(time.Now().UnixMilli())
		targetURL = fmt.Sprintf("%s?reason=init&productType=web_player&totp=%s&totpServer=unavailable&totpVer=%s", targetURL, totp, ver)
	}

	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("create spotify token request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="133", "Not(A:Brand";v="99", "Google Chrome";v="133"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Referer", "https://open.spotify.com/")
	req.Header.Set("Origin", "https://open.spotify.com")
	req.Header.Set("App-Platform", "WebPlayer")
	req.Header.Set("Cookie", cookieStr)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("execute spotify token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", "", fmt.Errorf("[AUTH] Spotify session unauthorized (HTTP %d). Run 'playlistsync login spotify' to re-authenticate", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("[AUTH] Spotify session is not authenticated or expired (HTTP %d). Run 'playlistsync login spotify' to re-authenticate", resp.StatusCode)
	}

	var res struct {
		AccessToken string `json:"accessToken"`
		IsAnonymous bool   `json:"isAnonymous"`
		Username    string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", "", fmt.Errorf("decode spotify token response: %w", err)
	}
	if res.IsAnonymous {
		return "", "", fmt.Errorf("spotify session is anonymous/unauthenticated")
	}
	if res.AccessToken == "" {
		return "", "", fmt.Errorf("[AUTH] Spotify access token empty in response")
	}
	return res.AccessToken, res.Username, nil
}

// ValidateSpotifyAuth loads Spotify credentials from file path and validates them
func ValidateSpotifyAuth(path string, proxyURL string) (*AuthStatus, error) {
	cleanPath := filepath.Clean(path)
	cookie, err := LoadCookie(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("read spotify credentials: %w", err)
	}
	if cookie == "" {
		return nil, fmt.Errorf("empty spotify cookie at %s", cleanPath)
	}

	status, err := ValidateSpotifyCookie(cookie, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("validate spotify credentials: %w", err)
	}
	status.Path = cleanPath
	return status, nil
}

// ValidateSpotifyCookie tests if a cookie can acquire a valid Web Player token or active session
func ValidateSpotifyCookie(cookie string, proxyURL string) (*AuthStatus, error) {
	token, username, err := getSpotifyAccessTokenDetails(cookie, proxyURL)
	if err != nil {
		return nil, err
	}

	user := username
	if user == "" && spotifyMeEndpoint != "" {
		if status, err := validateSpotifyAccessToken(token, proxyURL); err == nil && status != nil && status.User != "" {
			user = status.User
		}
	}

	return &AuthStatus{
		Platform:      PlatformSpotify,
		Authenticated: true,
		User:          user,
		Path:          DefaultSpotifyAuthPath,
		Cached:        false,
	}, nil
}

type ytmAuthPayload struct {
	ResponseContext struct {
		ServiceTrackingParams []struct {
			Service string `json:"service"`
			Params  []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"params"`
		} `json:"serviceTrackingParams"`
		MainAppWebResponseContext struct {
			LoggedOut bool `json:"loggedOut"`
		} `json:"mainAppWebResponseContext"`
		LoggedIn *bool `json:"LOGGED_IN"`
	} `json:"responseContext"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
	Header struct {
		MusicHeaderRenderer struct {
			Title struct {
				Runs []struct {
					Text string `json:"text"`
				} `json:"runs"`
			} `json:"title"`
		} `json:"musicHeaderRenderer"`
	} `json:"header"`
}

func parseYTMAuthResponse(body []byte) (bool, string, error) {
	var resp ytmAuthPayload
	if err := json.Unmarshal(body, &resp); err != nil {
		return false, "", fmt.Errorf("unmarshal youtube response: %w", err)
	}

	if resp.Error != nil {
		return false, "", fmt.Errorf("youtube music API error (code %d): %s", resp.Error.Code, resp.Error.Message)
	}

	if resp.ResponseContext.MainAppWebResponseContext.LoggedOut {
		return false, "", fmt.Errorf("youtube music session is logged out (visitor session)")
	}

	if resp.ResponseContext.LoggedIn != nil && !*resp.ResponseContext.LoggedIn {
		return false, "", fmt.Errorf("youtube music session is unauthenticated (LOGGED_IN: false)")
	}

	var hasLoggedInParam, hasLoggedOutParam bool
	for _, st := range resp.ResponseContext.ServiceTrackingParams {
		for _, p := range st.Params {
			k := strings.ToLower(p.Key)
			if k == "logged_in" || k == "is_logged_in" {
				if p.Value == "1" || strings.EqualFold(p.Value, "true") {
					hasLoggedInParam = true
				} else if p.Value == "0" || strings.EqualFold(p.Value, "false") {
					hasLoggedOutParam = true
				}
			}
		}
	}

	if hasLoggedOutParam && !hasLoggedInParam {
		return false, "", fmt.Errorf("youtube music session is unauthenticated (visitor session detected)")
	}

	var user string
	if len(resp.Header.MusicHeaderRenderer.Title.Runs) > 0 {
		user = resp.Header.MusicHeaderRenderer.Title.Runs[0].Text
	}

	if hasLoggedInParam || (resp.ResponseContext.LoggedIn != nil && *resp.ResponseContext.LoggedIn) {
		return true, user, nil
	}

	if !hasLoggedOutParam && len(resp.Header.MusicHeaderRenderer.Title.Runs) > 0 {
		return true, user, nil
	}

	return false, "", fmt.Errorf("youtube music session is unauthenticated: no authenticated user context found")
}

// ValidateYTMAuth loads and validates YouTube Music credentials from file path
func ValidateYTMAuth(path string, proxyURL string) (*AuthStatus, error) {
	cleanPath := filepath.Clean(path)
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("[AUTH] YouTube Music credentials missing at %s. Run 'playlistsync login youtube-music' or authenticate via prompt", cleanPath)
		}
		return nil, fmt.Errorf("[AUTH] YouTube Music credentials unreadable at %s: %w. Run 'playlistsync login youtube-music' to regenerate", cleanPath, err)
	}

	var headers map[string]string
	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil, fmt.Errorf("[AUTH] YouTube Music credentials file is empty at %s. Run 'playlistsync login youtube-music' to authenticate", cleanPath)
	}
	if strings.HasPrefix(content, "{") {
		if err := json.Unmarshal(data, &headers); err != nil {
			return nil, fmt.Errorf("[AUTH] YouTube Music credentials file is corrupted at %s (%w). Run 'playlistsync login youtube-music' to re-authenticate", cleanPath, err)
		}
	} else {
		headers = map[string]string{
			"Cookie":     content,
			"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		}
	}

	cookie := headers["Cookie"]
	userAgent := headers["User-Agent"]
	status, err := validateYTMCookieStringWithUA(cookie, userAgent, proxyURL)
	if err != nil {
		return nil, err
	}
	status.Path = cleanPath
	return status, nil
}

// ValidateYTMCookie is an alias for ValidateYTMAuth for backward compatibility
func ValidateYTMCookie(path string, proxyURL string) (*AuthStatus, error) {
	return ValidateYTMAuth(path, proxyURL)
}

// ValidateYTMCookieString validates raw cookie headers against YouTube Music Innertube API
func ValidateYTMCookieString(cookie string, proxyURL string) (*AuthStatus, error) {
	return validateYTMCookieStringWithUA(cookie, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36", proxyURL)
}

func validateYTMCookieStringWithUA(cookie string, userAgent string, proxyURL string) (*AuthStatus, error) {
	if strings.TrimSpace(cookie) == "" {
		return nil, fmt.Errorf("[AUTH] YouTube Music cookie not found. Run 'playlistsync login youtube-music' to authenticate")
	}

	sapisid := ExtractSAPISID(cookie)
	if sapisid == "" {
		return nil, fmt.Errorf("[AUTH] Missing SAPISID / __Secure-3PAPISID in YouTube Music credentials. Run 'playlistsync login youtube-music' to re-authenticate")
	}

	client, cleanup := newAuthHTTPClient(proxyURL)
	defer cleanup()

	t := strconv.FormatInt(time.Now().Unix(), 10)
	hasher := sha1.New()
	hasher.Write([]byte(t + " " + sapisid + " https://music.youtube.com"))
	hashStr := hex.EncodeToString(hasher.Sum(nil))
	authHeader := fmt.Sprintf("SAPISIDHASH %s_%s", t, hashStr)

	payload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    "WEB_REMIX",
				"clientVersion": "1.20260822.01.00",
				"hl":            "zh-CN",
				"gl":            "US",
			},
		},
		"browseId": "FEmusic_liked_playlists",
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal validate payload: %w", err)
	}

	req, err := http.NewRequest("POST", ytmBrowseEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create ytm validate request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("X-Goog-AuthUser", "0")
	req.Header.Set("x-origin", "https://music.youtube.com")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute ytm validate request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read ytm validate response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("youtube music API responded with HTTP %d: %s", resp.StatusCode, SanitizeSensitive(string(respBytes)))
	}

	authenticated, user, err := parseYTMAuthResponse(respBytes)
	if err != nil {
		return nil, err
	}
	if !authenticated {
		return nil, fmt.Errorf("youtube music session is unauthenticated")
	}

	return &AuthStatus{
		Platform:      PlatformYouTube,
		Authenticated: true,
		User:          user,
		Cached:        false,
	}, nil
}
