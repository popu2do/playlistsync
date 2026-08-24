package model

// YTMTrack represents a track in a YouTube Music playlist
type YTMTrack struct {
	VideoID    string   `json:"videoId"`
	SetVideoID string   `json:"setVideoId,omitempty"`
	Title      string   `json:"title"`
	Artists    []string `json:"artists"`
	Duration   string   `json:"duration,omitempty"`
}

// YTMPlaylist represents a YouTube Music playlist with complete track listings
type YTMPlaylist struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	TrackCount  int        `json:"trackCount"`
	Tracks      []YTMTrack `json:"tracks"`
}

// YTMPlaylistSummary represents a summary of a playlist in user library
type YTMPlaylistSummary struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// YTMReorderMove represents an in-place move action in a YouTube Music playlist
type YTMReorderMove struct {
	SetVideoID                 string `json:"setVideoId"`
	MovedSetVideoIDPredecessor string `json:"movedSetVideoIdPredecessor,omitempty"`
}

// YTMSearchResult represents a candidate returned from YouTube Music search
type YTMSearchResult struct {
	VideoID  string   `json:"videoId"`
	Title    string   `json:"title"`
	Artists  []string `json:"artists"`
	Duration string   `json:"duration,omitempty"`
	Score    int      `json:"score,omitempty"`
}
