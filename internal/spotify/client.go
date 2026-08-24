package spotify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"playlistsync/internal/auth"
	"playlistsync/internal/model"
	"strings"
	"time"
)

// Endpoint variables for Spotify Web API (can be overridden in tests)
var (
	EndpointMe          = "https://api.spotify.com/v1/me"
	EndpointMePlaylists = "https://api.spotify.com/v1/me/playlists"
	EndpointPlaylists   = "https://api.spotify.com/v1/playlists"
	EndpointSearch      = "https://api.spotify.com/v1/search"
)

// Client interacts with Spotify Web API
type Client struct {
	accessToken         string
	proxyURL            string
	httpClient          *http.Client
	enableEmbedFallback bool
}

// SpotifyClient defines the contract for live Spotify Web Player, Partner, and Web API operations.
type SpotifyClient interface {
	FindPlaylist(nameOrIDOrURL string) (*model.SpotifyPlaylist, error)
	GetPlaylist(playlistID string) (*model.SpotifyPlaylist, error)
	GetCurrentUser() (string, error)
	CreatePlaylist(name, description string) (string, error)
	AddTracksToPlaylist(playlistID string, trackURIs []string) error
	ReplacePlaylistTracks(playlistID string, trackURIs []string) error
	RemoveTracksFromPlaylist(playlistID string, trackURIs []string) error
	SearchTrack(query string) ([]model.SpotifyTrack, error)
}

var _ SpotifyClient = (*Client)(nil)

// Internal Spotify Web API JSON structures
type spArtistItem struct {
	Name string `json:"name"`
}

type spAlbumItem struct {
	Name string `json:"name"`
}

type spTrackItem struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Duration int            `json:"duration_ms"`
	Artists  []spArtistItem `json:"artists"`
	Album    spAlbumItem    `json:"album"`
}

type spPlaylistTrackWrapper struct {
	Track spTrackItem `json:"track"`
}

type spPlaylistTracksPage struct {
	Items []spPlaylistTrackWrapper `json:"items"`
	Next  string                   `json:"next"`
	Total int                      `json:"total"`
}

type spPlaylistDetail struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Tracks      spPlaylistTracksPage `json:"tracks"`
}

type spEmbedResponse struct {
	Props struct {
		PageProps struct {
			State struct {
				Data struct {
					Entity struct {
						Name      string `json:"name"`
						TrackList []struct {
							URI      string `json:"uri"`
							UID      string `json:"uid"`
							Title    string `json:"title"`
							Subtitle string `json:"subtitle"`
							Duration int    `json:"duration"`
						} `json:"trackList"`
					} `json:"entity"`
				} `json:"data"`
			} `json:"state"`
		} `json:"pageProps"`
	} `json:"props"`
}

type spSearchGQLResponse struct {
	Data struct {
		SearchV2 struct {
			Playlists struct {
				Items []struct {
					Data struct {
						ID   string `json:"id"`
						Name string `json:"name"`
						URI  string `json:"uri"`
					} `json:"data"`
				} `json:"items"`
			} `json:"playlists"`
		} `json:"searchV2"`
	} `json:"data"`
}

type spPlaylistGQLResponse struct {
	Data struct {
		PlaylistV2 struct {
			Name    string `json:"name"`
			Content struct {
				TotalCount int `json:"totalCount"`
				Items      []struct {
					ItemV2 struct {
						Data struct {
							Typename string `json:"__typename"`
							URI      string `json:"uri"`
							Name     string `json:"name"`
							Duration struct {
								TotalMilliseconds int `json:"totalMilliseconds"`
							} `json:"duration"`
							Artists struct {
								Items []struct {
									Profile struct {
										Name string `json:"name"`
									} `json:"profile"`
								} `json:"items"`
							} `json:"artists"`
							AlbumOfTrack struct {
								Name string `json:"name"`
							} `json:"albumOfTrack"`
						} `json:"data"`
					} `json:"itemV2"`
				} `json:"items"`
			} `json:"content"`
		} `json:"playlistV2"`
	} `json:"data"`
}

// NewClient creates a new Spotify API client using a Bearer access token
func NewClient(accessToken, proxyURL string) *Client {
	if proxyURL == "" {
		proxyURL = auth.DetectSystemProxy()
	}

	transport := &http.Transport{
		Proxy:               auth.ProxyFunc(proxyURL),
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Client{
		accessToken:         accessToken,
		proxyURL:            proxyURL,
		enableEmbedFallback: true,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   20 * time.Second,
		},
	}
}

func (c *Client) get(endpoint string) ([]byte, error) {
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create spotify request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute spotify request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read spotify response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("spotify API returned HTTP %d: %s", resp.StatusCode, auth.SanitizeSensitive(string(bodyBytes)))
	}
	return bodyBytes, nil
}

func extractSpotifyPlaylistID(target string) string {
	id := strings.TrimSpace(target)
	if strings.Contains(id, "playlist/") {
		parts := strings.Split(id, "playlist/")
		if len(parts) > 1 {
			id = strings.Split(parts[1], "?")[0]
		}
	}
	id = strings.TrimPrefix(id, "spotify:playlist:")
	return strings.TrimSpace(id)
}

func matchPlaylistName(a, b string) bool {
	if strings.EqualFold(a, b) {
		return true
	}
	normA := strings.ReplaceAll(strings.ToLower(a), " ", "")
	normB := strings.ReplaceAll(strings.ToLower(b), " ", "")
	return normA != "" && normA == normB
}

// FetchPlaylistFromEmbed extracts playlist data from Spotify embed page without requiring OAuth scope
func FetchPlaylistFromEmbed(playlistIDOrURL, proxyURL string) (*model.SpotifyPlaylist, error) {
	id := extractSpotifyPlaylistID(playlistIDOrURL)
	if id == "" {
		return nil, fmt.Errorf("invalid spotify playlist identifier")
	}

	reqURL := fmt.Sprintf("https://open.spotify.com/embed/playlist/%s", id)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create spotify embed request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	transport := &http.Transport{
		Proxy:               auth.ProxyFunc(proxyURL),
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch spotify embed page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("spotify embed returned HTTP %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read spotify embed page body: %w", err)
	}

	html := string(bodyBytes)
	idx := strings.Index(html, "__NEXT_DATA__")
	if idx == -1 {
		return nil, fmt.Errorf("failed to find embed data in Spotify page")
	}

	sub := html[idx:]
	tagCloseRel := strings.Index(sub, ">")
	if tagCloseRel == -1 {
		return nil, fmt.Errorf("malformed embed html: missing opening script tag closing bracket")
	}
	start := idx + tagCloseRel + 1

	scriptCloseRel := strings.Index(html[start:], "</script>")
	if scriptCloseRel == -1 {
		return nil, fmt.Errorf("malformed embed html: missing </script> closing tag")
	}
	end := start + scriptCloseRel

	if start >= end || end > len(html) {
		return nil, fmt.Errorf("invalid script tag boundary in embed html (start=%d end=%d)", start, end)
	}
	jsonStr := html[start:end]

	var root spEmbedResponse
	if err := json.Unmarshal([]byte(jsonStr), &root); err != nil {
		return nil, fmt.Errorf("unmarshal embed playlist json: %w", err)
	}

	ent := root.Props.PageProps.State.Data.Entity
	if len(ent.TrackList) >= 100 {
		fmt.Printf("[Warning] Spotify embed only returns up to 100 tracks (got %d). If the playlist has more tracks, please configure a Spotify Access Token to enable full Web API pagination.\n", len(ent.TrackList))
	}
	pl := &model.SpotifyPlaylist{
		PlaylistName:      ent.Name,
		SourcePlaylistURL: fmt.Sprintf("https://open.spotify.com/playlist/%s", id),
		ExpectedCount:     len(ent.TrackList),
		CollectedCount:    len(ent.TrackList),
		Tracks:            make([]model.SpotifyTrack, 0, len(ent.TrackList)),
	}

	for i, t := range ent.TrackList {
		var artists []string
		if t.Subtitle != "" {
			for _, a := range strings.Split(t.Subtitle, ",") {
				aTrim := strings.TrimSpace(a)
				if aTrim != "" {
					artists = append(artists, aTrim)
				}
			}
		}
		query := t.Title
		if len(artists) > 0 {
			query = fmt.Sprintf("%s %s", t.Title, strings.Join(artists, " "))
		}

		spURL := ""
		if strings.HasPrefix(t.URI, "spotify:track:") {
			spURL = fmt.Sprintf("https://open.spotify.com/track/%s", strings.TrimPrefix(t.URI, "spotify:track:"))
		}

		durSec := t.Duration / 1000
		durStr := formatDuration(durSec)

		pl.Tracks = append(pl.Tracks, model.SpotifyTrack{
			Index:      i + 1,
			Title:      t.Title,
			Artists:    artists,
			Duration:   durStr,
			Query:      query,
			SpotifyURL: spURL,
		})
	}

	return pl, nil
}

// SearchPlaylistsGraphQL searches Spotify catalog playlists using GraphQL partner query
func (c *Client) SearchPlaylistsGraphQL(query string) ([]struct {
	ID   string
	Name string
}, error) {
	varsJSON := fmt.Sprintf(`{"query":"%s","offset":0,"limit":20}`, query)
	extJSON := `{"persistedQuery":{"version":1,"sha256Hash":"903df2a65d8121e27d73a2be03c01e88ebe6021bb6d4eb82a389e35d87e51d27"}}`

	qParams := url.Values{}
	qParams.Set("operationName", "findPlaylists")
	qParams.Set("variables", varsJSON)
	qParams.Set("extensions", extJSON)

	gqlURL := "https://api-partner.spotify.com/pathfinder/v1/query?" + qParams.Encode()
	req, err := http.NewRequest("GET", gqlURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create spotify search request: %w", err)
	}
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("App-Platform", "WebPlayer")
	req.Header.Set("Referer", "https://open.spotify.com/")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute spotify search request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read spotify search response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("spotify search returned HTTP %d: %s", resp.StatusCode, auth.SanitizeSensitive(string(bodyBytes)))
	}

	var raw spSearchGQLResponse
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal spotify search response: %w", err)
	}

	var results []struct {
		ID   string
		Name string
	}
	for _, item := range raw.Data.SearchV2.Playlists.Items {
		id := item.Data.ID
		if id == "" && item.Data.URI != "" {
			id = strings.TrimPrefix(item.Data.URI, "spotify:playlist:")
		}
		if id != "" {
			results = append(results, struct {
				ID   string
				Name string
			}{
				ID:   id,
				Name: item.Data.Name,
			})
		}
	}
	return results, nil
}

// FindPlaylist searches user's playlists by name, ID, or URL and retrieves all tracks
func (c *Client) FindPlaylist(nameOrIDOrURL string) (*model.SpotifyPlaylist, error) {
	target := strings.TrimSpace(nameOrIDOrURL)
	if target == "" {
		return nil, fmt.Errorf("playlist identifier cannot be empty")
	}

	cleanID := extractSpotifyPlaylistID(target)

	// 1. Direct URL check
	if strings.Contains(target, "playlist/") {
		return c.GetPlaylist(cleanID)
	}

	// 2. Direct Spotify ID check (15+ alphanumeric chars)
	if len(cleanID) >= 15 && isAlphanumeric(cleanID) {
		if pl, err := c.GetPlaylist(cleanID); err == nil && pl != nil {
			return pl, nil
		}
	}

	// 3. Search via GraphQL partner API (fast and zero rate limits)
	if c.accessToken != "" {
		if results, err := c.SearchPlaylistsGraphQL(target); err == nil && len(results) > 0 {
			// Phase 1: Exact match
			for _, item := range results {
				if matchPlaylistName(item.Name, target) {
					return c.GetPlaylist(item.ID)
				}
			}
			// Phase 2: Prefix match
			targetLower := strings.ToLower(target)
			for _, item := range results {
				if strings.HasPrefix(strings.ToLower(item.Name), targetLower) {
					return c.GetPlaylist(item.ID)
				}
			}
			// Phase 3: First result fallback
			return c.GetPlaylist(results[0].ID)
		}
	}

	// 4. Fallback: Search user library playlists via Web API
	endpoint := fmt.Sprintf("%s?limit=50", EndpointMePlaylists)
	for endpoint != "" {
		bodyBytes, err := c.get(endpoint)
		if err != nil {
			break
		}

		var page struct {
			Items []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"items"`
			Next string `json:"next"`
		}
		if err := json.Unmarshal(bodyBytes, &page); err != nil {
			break
		}

		for _, item := range page.Items {
			if matchPlaylistName(item.Name, target) {
				return c.GetPlaylist(item.ID)
			}
		}
		endpoint = page.Next
	}

	// 5. Fallback: Search Spotify catalog playlists via /v1/search?type=playlist&q=...
	searchURL := fmt.Sprintf("%s?type=playlist&limit=20&q=%s", EndpointSearch, url.QueryEscape(target))
	if searchBytes, err := c.get(searchURL); err == nil {
		var searchRes struct {
			Playlists struct {
				Items []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"items"`
			} `json:"playlists"`
		}
		if err := json.Unmarshal(searchBytes, &searchRes); err == nil {
			for _, item := range searchRes.Playlists.Items {
				if matchPlaylistName(item.Name, target) {
					return c.GetPlaylist(item.ID)
				}
			}
			targetLower := strings.ToLower(target)
			for _, item := range searchRes.Playlists.Items {
				if strings.HasPrefix(strings.ToLower(item.Name), targetLower) {
					return c.GetPlaylist(item.ID)
				}
			}
		}
	}

	// 6. Final fallback to Embed if target looks like a playlist URL or ID
	if strings.Contains(target, "playlist/") || (len(cleanID) == 22 && isAlphanumeric(cleanID)) {
		if pl, err := FetchPlaylistFromEmbed(cleanID, c.proxyURL); err == nil && pl != nil {
			return pl, nil
		}
	}

	return nil, fmt.Errorf("playlist '%s' not found in user's Spotify account", nameOrIDOrURL)
}

// FetchPlaylistFromGraphQL retrieves the complete playlist (all tracks) using Spotify's Partner GraphQL API
func (c *Client) FetchPlaylistFromGraphQL(playlistID string) (*model.SpotifyPlaylist, error) {
	cleanID := extractSpotifyPlaylistID(playlistID)

	var allTracks []model.SpotifyTrack
	offset := 0
	limit := 200
	totalCount := 0
	playlistName := ""

	for {
		varsJSON := fmt.Sprintf(`{"uri":"spotify:playlist:%s","offset":%d,"limit":%d}`, cleanID, offset, limit)
		extJSON := `{"persistedQuery":{"version":1,"sha256Hash":"908a5597b4d0af0489a9ad6a2d41bc3b416ff47c0884016d92bbd6822d0eb6d8"}}`

		qParams := url.Values{}
		qParams.Set("operationName", "queryPlaylist")
		qParams.Set("variables", varsJSON)
		qParams.Set("extensions", extJSON)

		gqlURL := "https://api-partner.spotify.com/pathfinder/v1/query?" + qParams.Encode()
		req, err := http.NewRequest("GET", gqlURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create spotify graphql request: %w", err)
		}
		if c.accessToken != "" {
			req.Header.Set("Authorization", "Bearer "+c.accessToken)
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("App-Platform", "WebPlayer")
		req.Header.Set("Referer", "https://open.spotify.com/")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("execute spotify graphql request: %w", err)
		}
		bodyBytes, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read spotify graphql response: %w", readErr)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("spotify graphql returned HTTP %d: %s", resp.StatusCode, auth.SanitizeSensitive(string(bodyBytes)))
		}

		var raw spPlaylistGQLResponse
		if err := json.Unmarshal(bodyBytes, &raw); err != nil {
			return nil, fmt.Errorf("unmarshal spotify graphql response: %w", err)
		}

		plV2 := raw.Data.PlaylistV2
		if plV2.Name != "" && playlistName == "" {
			playlistName = plV2.Name
		}
		if totalCount == 0 {
			totalCount = plV2.Content.TotalCount
		}

		items := plV2.Content.Items
		if len(items) == 0 {
			break
		}

		for _, it := range items {
			d := it.ItemV2.Data
			if d.Name == "" && d.URI == "" {
				continue
			}
			trackID := strings.TrimPrefix(d.URI, "spotify:track:")
			var artists []string
			for _, a := range d.Artists.Items {
				if a.Profile.Name != "" {
					artists = append(artists, a.Profile.Name)
				}
			}
			durSec := d.Duration.TotalMilliseconds / 1000
			durStr := formatDuration(durSec)
			query := d.Name
			if len(artists) > 0 {
				query = fmt.Sprintf("%s %s", d.Name, artists[0])
			}

			allTracks = append(allTracks, model.SpotifyTrack{
				Index:      len(allTracks) + 1,
				ID:         trackID,
				Title:      d.Name,
				Artists:    artists,
				Album:      d.AlbumOfTrack.Name,
				Duration:   durStr,
				Query:      query,
				SpotifyURI: d.URI,
				SpotifyURL: fmt.Sprintf("https://open.spotify.com/track/%s", trackID),
			})
		}

		offset += len(items)
		if offset >= totalCount || len(items) < limit {
			break
		}
	}

	if totalCount == 0 {
		totalCount = len(allTracks)
	}

	return &model.SpotifyPlaylist{
		PlaylistName:      playlistName,
		SourcePlaylistURL: fmt.Sprintf("https://open.spotify.com/playlist/%s", cleanID),
		ExpectedCount:     totalCount,
		CollectedCount:    len(allTracks),
		Tracks:            allTracks,
	}, nil
}

// GetPlaylist fetches a Spotify playlist by its ID and paginates through all tracks
func (c *Client) GetPlaylist(playlistID string) (*model.SpotifyPlaylist, error) {
	cleanID := extractSpotifyPlaylistID(playlistID)

	// 1. Try GraphQL Partner API first (supports all tracks with zero rate limit)
	if c.accessToken != "" {
		if pl, err := c.FetchPlaylistFromGraphQL(cleanID); err == nil && pl != nil && len(pl.Tracks) > 0 {
			return pl, nil
		}
	}

	// 2. Try official Web API
	endpoint := fmt.Sprintf("%s/%s", EndpointPlaylists, cleanID)
	bodyBytes, err := c.get(endpoint)
	if err != nil {
		if c.enableEmbedFallback && (strings.Contains(playlistID, "playlist/") || (len(cleanID) == 22 && isAlphanumeric(cleanID))) {
			if pl, embedErr := FetchPlaylistFromEmbed(cleanID, c.proxyURL); embedErr == nil && pl != nil {
				return pl, nil
			}
		}
		return nil, fmt.Errorf("fetch spotify playlist details: %w", err)
	}

	var plRaw spPlaylistDetail
	if err := json.Unmarshal(bodyBytes, &plRaw); err != nil {
		return nil, fmt.Errorf("unmarshal spotify playlist: %w", err)
	}

	pl := &model.SpotifyPlaylist{
		PlaylistName:      plRaw.Name,
		SourcePlaylistURL: fmt.Sprintf("https://open.spotify.com/playlist/%s", plRaw.ID),
	}

	appendTracks := func(items []spPlaylistTrackWrapper) {
		for _, it := range items {
			t := it.Track
			if t.ID == "" && t.Name == "" {
				continue
			}
			artists := make([]string, 0, len(t.Artists))
			for _, a := range t.Artists {
				if a.Name != "" {
					artists = append(artists, a.Name)
				}
			}
			durationStr := formatDuration(t.Duration / 1000)
			query := t.Name
			if len(artists) > 0 {
				query = fmt.Sprintf("%s %s", t.Name, artists[0])
			}
			pl.Tracks = append(pl.Tracks, model.SpotifyTrack{
				Index:      len(pl.Tracks) + 1,
				ID:         t.ID,
				Title:      t.Name,
				Artists:    artists,
				Album:      t.Album.Name,
				Duration:   durationStr,
				Query:      query,
				SpotifyURI: fmt.Sprintf("spotify:track:%s", t.ID),
				SpotifyURL: fmt.Sprintf("https://open.spotify.com/track/%s", t.ID),
			})
		}
	}

	appendTracks(plRaw.Tracks.Items)

	nextURL := plRaw.Tracks.Next
	for nextURL != "" {
		nextBytes, err := c.get(nextURL)
		if err != nil {
			return nil, fmt.Errorf("fetch next page of spotify tracks (%s): %w", nextURL, err)
		}
		var page spPlaylistTracksPage
		if err := json.Unmarshal(nextBytes, &page); err != nil {
			return nil, fmt.Errorf("unmarshal spotify tracks page: %w", err)
		}
		appendTracks(page.Items)
		nextURL = page.Next
	}

	expected := plRaw.Tracks.Total
	if expected == 0 {
		expected = len(pl.Tracks)
	}
	pl.ExpectedCount = expected
	pl.CollectedCount = len(pl.Tracks)

	return pl, nil
}

func (c *Client) post(endpoint string, payload interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal request payload: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequest("POST", endpoint, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create spotify request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute spotify request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read spotify response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return bodyBytes, fmt.Errorf("spotify API returned HTTP %d: %s", resp.StatusCode, auth.SanitizeSensitive(string(bodyBytes)))
	}
	return bodyBytes, nil
}

func (c *Client) put(endpoint string, payload interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal request payload: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequest("PUT", endpoint, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create spotify request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute spotify request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read spotify response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return bodyBytes, fmt.Errorf("spotify API returned HTTP %d: %s", resp.StatusCode, auth.SanitizeSensitive(string(bodyBytes)))
	}
	return bodyBytes, nil
}

func (c *Client) delete(endpoint string, payload interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal request payload: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequest("DELETE", endpoint, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create spotify request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute spotify request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read spotify response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return bodyBytes, fmt.Errorf("spotify API returned HTTP %d: %s", resp.StatusCode, auth.SanitizeSensitive(string(bodyBytes)))
	}
	return bodyBytes, nil
}

// GetCurrentUser returns the authenticated user's ID
func (c *Client) GetCurrentUser() (string, error) {
	bodyBytes, err := c.get(EndpointMe)
	if err != nil {
		return "", err
	}
	var me struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(bodyBytes, &me); err != nil {
		return "", fmt.Errorf("unmarshal me: %w", err)
	}
	return me.ID, nil
}

// CreatePlaylist creates a new playlist for the current user
func (c *Client) CreatePlaylist(name, description string) (string, error) {
	payload := map[string]interface{}{
		"name":        name,
		"description": description,
		"public":      false,
	}
	bodyBytes, err := c.post(EndpointMePlaylists, payload)
	if err != nil {
		return "", err
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return "", fmt.Errorf("unmarshal create playlist: %w", err)
	}
	return res.ID, nil
}

// AddTracksToPlaylist appends Spotify track URIs to the specified playlist in chunks of 100
func (c *Client) AddTracksToPlaylist(playlistID string, trackURIs []string) error {
	if len(trackURIs) == 0 {
		return nil
	}
	chunkSize := 100
	for i := 0; i < len(trackURIs); i += chunkSize {
		end := i + chunkSize
		if end > len(trackURIs) {
			end = len(trackURIs)
		}
		chunk := trackURIs[i:end]
		payload := map[string]interface{}{
			"uris": chunk,
		}
		endpoint := fmt.Sprintf("%s/%s/tracks", EndpointPlaylists, playlistID)
		if _, err := c.post(endpoint, payload); err != nil {
			return fmt.Errorf("add tracks to playlist batch %d-%d: %w", i, end, err)
		}
	}
	return nil
}

// ReplacePlaylistTracks replaces the entire playlist content with the specified URIs in exact order.
func (c *Client) ReplacePlaylistTracks(playlistID string, trackURIs []string) error {
	endpoint := fmt.Sprintf("%s/%s/tracks", EndpointPlaylists, playlistID)
	if len(trackURIs) == 0 {
		payload := map[string]interface{}{
			"uris": []string{},
		}
		if _, err := c.put(endpoint, payload); err != nil {
			return fmt.Errorf("clear spotify playlist: %w", err)
		}
		return nil
	}

	firstChunkSize := 100
	if len(trackURIs) < firstChunkSize {
		firstChunkSize = len(trackURIs)
	}
	firstChunk := trackURIs[:firstChunkSize]
	payload := map[string]interface{}{
		"uris": firstChunk,
	}
	if _, err := c.put(endpoint, payload); err != nil {
		return fmt.Errorf("replace spotify tracks batch 0-%d: %w", firstChunkSize, err)
	}

	if len(trackURIs) > firstChunkSize {
		remaining := trackURIs[firstChunkSize:]
		if err := c.AddTracksToPlaylist(playlistID, remaining); err != nil {
			return fmt.Errorf("append remaining spotify tracks: %w", err)
		}
	}
	return nil
}

// RemoveTracksFromPlaylist removes Spotify track URIs from the specified playlist in chunks of 100
func (c *Client) RemoveTracksFromPlaylist(playlistID string, trackURIs []string) error {
	if len(trackURIs) == 0 {
		return nil
	}
	chunkSize := 100
	for i := 0; i < len(trackURIs); i += chunkSize {
		end := i + chunkSize
		if end > len(trackURIs) {
			end = len(trackURIs)
		}
		chunk := trackURIs[i:end]
		trackItems := make([]map[string]string, len(chunk))
		for j, uri := range chunk {
			trackItems[j] = map[string]string{"uri": uri}
		}
		payload := map[string]interface{}{
			"tracks": trackItems,
		}
		endpoint := fmt.Sprintf("%s/%s/tracks", EndpointPlaylists, playlistID)
		if _, err := c.delete(endpoint, payload); err != nil {
			return fmt.Errorf("remove tracks from playlist batch %d-%d: %w", i, end, err)
		}
	}
	return nil
}

// SearchTrack searches the Spotify catalog for tracks matching query
func (c *Client) SearchTrack(query string) ([]model.SpotifyTrack, error) {
	searchURL := fmt.Sprintf("%s?type=track&limit=10&q=%s", EndpointSearch, url.QueryEscape(query))
	bodyBytes, err := c.get(searchURL)
	if err != nil {
		return nil, err
	}
	var searchRes struct {
		Tracks struct {
			Items []spTrackItem `json:"items"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal(bodyBytes, &searchRes); err != nil {
		return nil, fmt.Errorf("unmarshal search tracks: %w", err)
	}

	results := make([]model.SpotifyTrack, 0, len(searchRes.Tracks.Items))
	for i, item := range searchRes.Tracks.Items {
		var artists []string
		for _, a := range item.Artists {
			if a.Name != "" {
				artists = append(artists, a.Name)
			}
		}
		results = append(results, model.SpotifyTrack{
			Index:      i + 1,
			ID:         item.ID,
			Title:      item.Name,
			Artists:    artists,
			Album:      item.Album.Name,
			Duration:   formatDuration(item.Duration / 1000),
			SpotifyURI: fmt.Sprintf("spotify:track:%s", item.ID),
			SpotifyURL: fmt.Sprintf("https://open.spotify.com/track/%s", item.ID),
		})
	}
	return results, nil
}

func formatDuration(seconds int) string {
	m := seconds / 60
	s := seconds % 60
	return fmt.Sprintf("%d:%02d", m, s)
}

func isAlphanumeric(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}
