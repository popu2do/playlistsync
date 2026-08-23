package ytmusic

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"playlistsync/internal/auth"
	"playlistsync/internal/model"
	"strconv"
	"strings"
	"time"
)

var (
	EndpointBrowse         = "https://music.youtube.com/youtubei/v1/browse?prettyPrint=false"
	EndpointEditPlaylist   = "https://music.youtube.com/youtubei/v1/browse/edit_playlist?prettyPrint=false"
	EndpointSearch         = "https://music.youtube.com/youtubei/v1/search?prettyPrint=false"
	EndpointCreatePlaylist = "https://music.youtube.com/youtubei/v1/playlist/create?prettyPrint=false"
)

const (
	ClientName    = "WEB_REMIX"
	ClientVersion = "1.20260822.01.00"
)

// Client handles authenticated requests to YouTube Music Innertube API
type Client struct {
	httpClient *http.Client
	headers    map[string]string
	sapisid    string
}

// YouTubeMusicClient defines the contract for interacting with YouTube Music Innertube API.
type YouTubeMusicClient interface {
	GetPlaylist(playlistID string) (*model.YTMPlaylist, error)
	AddPlaylistItems(playlistID string, videoIDs []string) error
	RemovePlaylistItems(playlistID string, items []model.YTMTrack) error
	SearchSong(query string) ([]model.YTMSearchResult, error)
	CreatePlaylist(title, description, privacy string) (string, error)
	FindPlaylistByTitle(title string) (*model.YTMPlaylistSummary, error)
	GetLibraryPlaylists() ([]model.YTMPlaylistSummary, error)
}

var _ YouTubeMusicClient = (*Client)(nil)

// NewClient initializes a YouTube Music client with auth headers and optional proxy
func NewClient(browserJSONPath string, proxyURL string) (*Client, error) {
	headers, err := auth.LoadAuthHeaders(browserJSONPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("[AUTH] YouTube Music credentials missing at %s. Run 'playlistsync login youtube-music' or authenticate via prompt", browserJSONPath)
		}
		return nil, fmt.Errorf("[AUTH] Failed to read YouTube Music credentials at %s: %w", browserJSONPath, err)
	}

	if proxyURL == "" {
		proxyURL = auth.DetectSystemProxy()
	}

	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil || parsed.Host == "" || parsed.Scheme == "" {
			return nil, fmt.Errorf("parse proxy url: invalid proxy URL %q", proxyURL)
		}
	}

	transport := &http.Transport{
		Proxy:               auth.ProxyFunc(proxyURL),
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	var sapisid string
	if cookie, ok := headers["Cookie"]; ok {
		sapisid = auth.ExtractSAPISID(cookie)
	}

	return &Client{
		httpClient: httpClient,
		headers:    headers,
		sapisid:    sapisid,
	}, nil
}

func (c *Client) buildAuthHeader() string {
	if c.sapisid == "" {
		return c.headers["Authorization"]
	}
	t := strconv.FormatInt(time.Now().Unix(), 10)
	hasher := sha1.New()
	hasher.Write([]byte(t + " " + c.sapisid + " https://music.youtube.com"))
	hashStr := hex.EncodeToString(hasher.Sum(nil))
	return fmt.Sprintf("SAPISIDHASH %s_%s", t, hashStr)
}

func (c *Client) post(endpoint string, payload interface{}) ([]byte, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request payload: %w", err)
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.headers["User-Agent"])
	req.Header.Set("Cookie", c.headers["Cookie"])
	req.Header.Set("Authorization", c.buildAuthHeader())
	req.Header.Set("X-Goog-AuthUser", "0")
	req.Header.Set("x-origin", "https://music.youtube.com")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network request failed: %w. Please check your internet connection or proxy settings", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return respBytes, fmt.Errorf("YouTube Music authentication error (HTTP %d): %s. Run 'playlistsync login youtube-music' to re-authenticate", resp.StatusCode, auth.SanitizeSensitive(string(respBytes)))
		}
		return respBytes, fmt.Errorf("YouTube Music API error (HTTP %d): %s", resp.StatusCode, auth.SanitizeSensitive(string(respBytes)))
	}

	return respBytes, nil
}

func clientContext(hl, gl string) map[string]interface{} {
	clientMap := map[string]interface{}{
		"clientName":    ClientName,
		"clientVersion": ClientVersion,
	}
	if hl != "" {
		clientMap["hl"] = hl
	}
	if gl != "" {
		clientMap["gl"] = gl
	}
	return map[string]interface{}{
		"context": map[string]interface{}{
			"client": clientMap,
		},
	}
}

// GetPlaylist fetches a YouTube Music playlist by ID
func (c *Client) GetPlaylist(playlistID string) (*model.YTMPlaylist, error) {
	browseID := playlistID
	if !strings.HasPrefix(browseID, "VL") {
		browseID = "VL" + playlistID
	}

	payload := clientContext("zh-CN", "US")
	payload["browseId"] = browseID

	respBytes, err := c.post(EndpointBrowse, payload)
	if err != nil {
		return nil, err
	}

	pl, continuation, err := parsePlaylistResponse(respBytes)
	if err != nil {
		return nil, err
	}
	pl.ID = playlistID

	maxPages := 100
	page := 0
	seenTokens := make(map[string]bool)
	if continuation != "" {
		seenTokens[continuation] = true
	}
	for continuation != "" && page < maxPages {
		page++
		contPayload := clientContext("zh-CN", "US")
		contPayload["continuation"] = continuation

		// Innertube continuation browse: continuation parameter must be passed in query string and/or body
		var endpointURL string
		if strings.Contains(EndpointBrowse, "?") {
			endpointURL = fmt.Sprintf("%s&continuation=%s&ctoken=%s", EndpointBrowse, continuation, continuation)
		} else {
			endpointURL = fmt.Sprintf("%s?continuation=%s&ctoken=%s", EndpointBrowse, continuation, continuation)
		}
		contResp, err := c.post(endpointURL, contPayload)
		if err != nil {
			return nil, fmt.Errorf("fetch playlist page %d: %w", page, err)
		}
		nextPl, nextToken, err := parsePlaylistResponse(contResp)
		if err != nil {
			return nil, fmt.Errorf("parse playlist page %d: %w", page, err)
		}
		pl.Tracks = append(pl.Tracks, nextPl.Tracks...)
		if nextToken == "" || nextToken == continuation || seenTokens[nextToken] {
			break
		}
		seenTokens[nextToken] = true
		continuation = nextToken
	}

	pl.TrackCount = len(pl.Tracks)
	return pl, nil
}

// GetLibraryPlaylists fetches playlists from the user's library
func (c *Client) GetLibraryPlaylists() ([]model.YTMPlaylistSummary, error) {
	payload := clientContext("zh-CN", "US")
	payload["browseId"] = "FEmusic_liked_playlists"

	respBytes, err := c.post(EndpointBrowse, payload)
	if err != nil {
		return nil, err
	}

	return parseLibraryPlaylists(respBytes), nil
}

// FindPlaylistByTitle searches user library playlists for a matching title
func (c *Client) FindPlaylistByTitle(title string) (*model.YTMPlaylistSummary, error) {
	playlists, err := c.GetLibraryPlaylists()
	if err != nil {
		return nil, err
	}

	target := strings.TrimSpace(title)
	if target == "" {
		return nil, nil
	}

	targetLower := strings.ToLower(target)
	targetWithoutImport := strings.TrimSpace(strings.TrimSuffix(targetLower, "(spotify import)"))

	// Pass 1: Exact case-insensitive title match
	for _, p := range playlists {
		pTitle := strings.TrimSpace(p.Title)
		if strings.EqualFold(pTitle, target) {
			return &p, nil
		}
	}

	// Pass 2: Suffix variation matches (e.g. "Title (Spotify import)" vs "Title")
	for _, p := range playlists {
		pTitle := strings.TrimSpace(p.Title)
		pTitleLower := strings.ToLower(pTitle)
		pTitleWithoutImport := strings.TrimSpace(strings.TrimSuffix(pTitleLower, "(spotify import)"))

		if strings.EqualFold(pTitle, target+" (Spotify import)") ||
			strings.EqualFold(pTitle, target+" (spotify import)") ||
			(targetWithoutImport != "" && pTitleWithoutImport == targetWithoutImport) {
			return &p, nil
		}
	}

	// Pass 3: Exact word-boundary prefix match only
	for _, p := range playlists {
		pTitleLower := strings.ToLower(strings.TrimSpace(p.Title))
		if strings.HasPrefix(pTitleLower, targetLower) {
			suffix := pTitleLower[len(targetLower):]
			if suffix == "" || strings.HasPrefix(suffix, " -") || strings.HasPrefix(suffix, " :") || strings.HasPrefix(suffix, " (") {
				return &p, nil
			}
		}
	}

	return nil, nil
}

// CreatePlaylist creates a new playlist on YouTube Music.
// Privacy can be "PRIVATE", "PUBLIC", or "UNLISTED" (defaults to "PRIVATE").
func (c *Client) CreatePlaylist(title, description, privacy string) (string, error) {
	normPrivacy := strings.ToUpper(strings.TrimSpace(privacy))
	switch normPrivacy {
	case "PUBLIC", "UNLISTED", "PRIVATE":
		privacy = normPrivacy
	case "PRIVACY_STATUS_PUBLIC":
		privacy = "PUBLIC"
	case "PRIVACY_STATUS_UNLISTED":
		privacy = "UNLISTED"
	case "PRIVACY_STATUS_PRIVATE":
		privacy = "PRIVATE"
	default:
		privacy = "PRIVATE"
	}

	payload := clientContext("", "")
	payload["title"] = title
	payload["description"] = description
	payload["privacyStatus"] = privacy

	respBytes, err := c.post(EndpointCreatePlaylist, payload)
	if err != nil {
		return "", err
	}

	var res struct {
		PlaylistID string `json:"playlistId"`
	}
	if err := json.Unmarshal(respBytes, &res); err != nil {
		return "", fmt.Errorf("unmarshal create playlist response: %w", err)
	}

	if res.PlaylistID == "" {
		return "", fmt.Errorf("no playlistId returned in create response")
	}

	return res.PlaylistID, nil
}

// RemovePlaylistItems removes specified tracks from a playlist in batches of at most 20
func (c *Client) RemovePlaylistItems(playlistID string, items []model.YTMTrack) error {
	if len(items) == 0 {
		return nil
	}

	actions := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		if item.SetVideoID == "" {
			continue
		}
		actions = append(actions, map[string]interface{}{
			"action":         "ACTION_REMOVE_VIDEO",
			"setVideoId":     item.SetVideoID,
			"removedVideoId": item.VideoID,
		})
	}

	if len(actions) == 0 {
		return nil
	}

	batchSize := 20
	for i := 0; i < len(actions); i += batchSize {
		end := i + batchSize
		if end > len(actions) {
			end = len(actions)
		}
		batch := actions[i:end]

		payload := clientContext("", "")
		payload["playlistId"] = playlistID
		payload["actions"] = batch

		if _, err := c.post(EndpointEditPlaylist, payload); err != nil {
			return err
		}

		if end < len(actions) {
			time.Sleep(300 * time.Millisecond)
		}
	}
	return nil
}

// AddPlaylistItems adds specified videoIDs to a playlist in batches of at most 20
func (c *Client) AddPlaylistItems(playlistID string, videoIDs []string) error {
	if len(videoIDs) == 0 {
		return nil
	}

	batchSize := 20
	for i := 0; i < len(videoIDs); i += batchSize {
		end := i + batchSize
		if end > len(videoIDs) {
			end = len(videoIDs)
		}
		chunk := videoIDs[i:end]

		actions := make([]map[string]interface{}, 0, len(chunk))
		for _, vid := range chunk {
			actions = append(actions, map[string]interface{}{
				"action":       "ACTION_ADD_VIDEO",
				"addedVideoId": vid,
			})
		}

		payload := clientContext("", "")
		payload["playlistId"] = playlistID
		payload["actions"] = actions

		if _, err := c.post(EndpointEditPlaylist, payload); err != nil {
			return fmt.Errorf("add items batch %d-%d: %w", i, end, err)
		}
		time.Sleep(300 * time.Millisecond)
	}

	return nil
}

// SearchSong searches for songs on YouTube Music
func (c *Client) SearchSong(query string) ([]model.YTMSearchResult, error) {
	payload := clientContext("zh-CN", "US")
	payload["query"] = query
	payload["params"] = "Eg-KAQwIABAAGAAgACgB"

	respBytes, err := c.post(EndpointSearch, payload)
	if err != nil {
		return nil, err
	}

	return parseSearchResults(respBytes), nil
}
