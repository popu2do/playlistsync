package model

// SpotifyTrack represents a single track in Spotify playlist
type SpotifyTrack struct {
	Index      int      `json:"index"`
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Artists    []string `json:"artists"`
	Album      string   `json:"album"`
	Duration   string   `json:"duration"`
	SpotifyURI string   `json:"spotifyUri"`
	SpotifyURL string   `json:"spotifyUrl"`
	Query      string   `json:"query"`
}

// SpotifyPlaylist represents the complete export of a Spotify playlist
type SpotifyPlaylist struct {
	Platform          string         `json:"platform"`
	PlaylistName      string         `json:"playlistName"`
	SourcePlaylistURL string         `json:"sourcePlaylistUrl"`
	ExpectedCount     int            `json:"expectedCount"`
	CollectedCount    int            `json:"collectedCount"`
	Tracks            []SpotifyTrack `json:"tracks"`
}
