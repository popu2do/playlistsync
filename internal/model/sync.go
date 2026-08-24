package model

// SyncResult represents the final outcome of a synchronization run across platforms.
type SyncResult struct {
	Direction            string         `json:"direction"`
	SourcePlatform       string         `json:"sourcePlatform"`
	TargetPlatform       string         `json:"targetPlatform"`
	PlaylistID           string         `json:"playlistId"`
	PlaylistURL          string         `json:"playlistUrl"`
	WebURL               string         `json:"webUrl,omitempty"`
	Title                string         `json:"title"`
	SourcePlaylistURL    string         `json:"sourcePlaylistUrl,omitempty"`
	TotalSourceTracks    int            `json:"totalSourceTracks"`
	AddedTracks          int            `json:"addedTracks"`
	SkippedTracks        int            `json:"skippedTracks"`
	SyncOrder            bool           `json:"syncOrder,omitempty"`
	OrderConcordanceRate float64        `json:"orderConcordanceRate,omitempty"`
	ReorderedCount       int            `json:"reorderedCount,omitempty"`
	Skipped              []SkippedTrack `json:"skipped"`
	AddedAfterReview     []AddedTrack   `json:"addedAfterReview,omitempty"`
	RemovedExtraTracks   []RemovedTrack `json:"removedExtraTracks,omitempty"`
	LastSyncedAt         string         `json:"lastSyncedAt"`
	Verification         *Verification  `json:"verification,omitempty"`
}

// SkippedTrack captures tracks that could not be matched with high confidence.
type SkippedTrack struct {
	Index   int      `json:"index"`
	Title   string   `json:"title"`
	Artists []string `json:"artists"`
	Reason  string   `json:"reason"`
}

// AddedTrack records tracks confirmed and added to the destination playlist.
type AddedTrack struct {
	Index            int      `json:"index"`
	Title            string   `json:"title"`
	Artists          []string `json:"artists"`
	TargetTrackID    string   `json:"targetTrackId"`
	DestinationTitle string   `json:"destinationTitle,omitempty"`
}

// RemovedTrack records extraneous tracks pruned from the destination playlist.
type RemovedTrack struct {
	TargetTrackID string   `json:"targetTrackId"`
	Title         string   `json:"title"`
	Artists       []string `json:"artists"`
}

// Verification records post-synchronization playlist state and verification metrics.
type Verification struct {
	PageTitle      string `json:"pageTitle"`
	PageTrackCount int    `json:"pageTrackCount"`
	Description    string `json:"description,omitempty"`
}
