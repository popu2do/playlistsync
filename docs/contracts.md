# Provider Interface Contracts & Protocols

## 1. Provider Interfaces

The architecture defines clean contracts for provider adapters and subsystems.

### 1.1 Spotify Provider Contracts

#### 1.1.1 File I/O Contract (`SpotifyReader`)
```go
type SpotifyReader interface {
    // ReadPlaylistJSON reads and parses a Spotify playlist JSON file.
    ReadPlaylistJSON(filePath string) (*model.SpotifyPlaylist, error)

    // WritePlaylistJSON serializes a Spotify playlist struct to formatted JSON.
    WritePlaylistJSON(filePath string, pl *model.SpotifyPlaylist) error

    // WritePlaylistCSV exports a Spotify playlist to UTF-8 BOM CSV.
    WritePlaylistCSV(filePath string, pl *model.SpotifyPlaylist) error

    // FindPlaylistByName searches for a playlist file matching the name in a directory.
    FindPlaylistByName(dataDir string, name string) (*model.SpotifyPlaylist, string, error)
}
```

#### 1.1.2 Live Online Client Contract (`SpotifyClient`)
```go
type SpotifyClient interface {
    // FindPlaylist resolves a playlist by exact name, Spotify ID, or share URL.
    FindPlaylist(nameOrIDOrURL string) (*model.SpotifyPlaylist, error)

    // FetchPlaylistFromGraphQL fetches un-truncated playlists (>100 tracks) via Spotify Partner GraphQL.
    FetchPlaylistFromGraphQL(playlistID string) (*model.SpotifyPlaylist, error)

    // GetPlaylist retrieves full playlist metadata.
    GetPlaylist(playlistID string) (*model.SpotifyPlaylist, error)

    // GetCurrentUser retrieves the authenticated Spotify user ID.
    GetCurrentUser() (string, error)

    // CreatePlaylist creates a new playlist on Spotify.
    CreatePlaylist(name, description string) (string, error)

    // AddTracksToPlaylist appends track URIs to the specified Spotify playlist.
    AddTracksToPlaylist(playlistID string, trackURIs []string) error

    // RemoveTracksFromPlaylist deletes track URIs from the specified Spotify playlist.
    RemoveTracksFromPlaylist(playlistID string, trackURIs []string) error

    // SearchTrack executes a track search query against Spotify catalog.
    SearchTrack(query string) ([]model.SpotifyTrack, error)
}
```

---

### 1.2 YouTube Music Provider Contract

```go
type YouTubeMusicClient interface {
    // GetPlaylist retrieves playlist metadata and full track items using pagination.
    GetPlaylist(playlistID string) (*model.YTMPlaylist, error)

    // AddPlaylistItems appends video IDs to the specified playlist.
    AddPlaylistItems(playlistID string, videoIDs []string) error

    // RemovePlaylistItems deletes specified tracks from the playlist using setVideoId.
    RemovePlaylistItems(playlistID string, items []model.YTMTrack) error

    // SearchSong executes a song search query against YouTube Music.
    SearchSong(query string) ([]model.YTMSearchResult, error)

    // CreatePlaylist creates a new playlist on YouTube Music.
    CreatePlaylist(title, description, privacy string) (string, error)

    // FindPlaylistByTitle searches user library playlists for a matching title.
    FindPlaylistByTitle(title string) (*model.YTMPlaylistSummary, error)

    // GetLibraryPlaylists fetches all playlist summaries from the user library.
    GetLibraryPlaylists() ([]model.YTMPlaylistSummary, error)
}
```

---

## 2. YouTube Music Innertube API Protocol Specification

### 2.1 Authentication & Request Signing

Authenticated Innertube requests (`WEB_REMIX` client) require dynamic `SAPISIDHASH` computation from session cookies.

#### Header Specification:
```http
POST /youtubei/v1/<endpoint>?prettyPrint=false HTTP/1.1
Host: music.youtube.com
Content-Type: application/json
User-Agent: <Browser User Agent>
Cookie: SAPISID=<sapisid_token>; __Secure-3PAPISID=...
Authorization: SAPISIDHASH <timestamp>_<sha1_hex>
X-Goog-AuthUser: 0
x-origin: https://music.youtube.com
```

#### Hash Calculation:
```
T = UnixEpochSeconds()
Payload = T + " " + SAPISID + " " + "https://music.youtube.com"
Hash = HexEncode(SHA1(Payload))
AuthHeader = "SAPISIDHASH " + T + "_" + Hash
```

---

### 2.2 Endpoint Contracts

#### 2.2.1 Browse Playlist Endpoint (`/youtubei/v1/browse`)

- **Payload**:
  ```json
  {
    "context": {
      "client": {
        "clientName": "WEB_REMIX",
        "clientVersion": "1.20260822.01.00",
        "hl": "zh-CN",
        "gl": "US"
      }
    },
    "browseId": "VLPL_TARGET_PLAYLIST_ID"
  }
  ```

#### 2.2.2 Edit Playlist Endpoint (`/youtubei/v1/browse/edit_playlist`)

- **Add Videos Action**:
  ```json
  {
    "context": { "client": { "clientName": "WEB_REMIX", "clientVersion": "1.20260822.01.00" } },
    "playlistId": "PL_TARGET_PLAYLIST_ID",
    "actions": [
      {
        "action": "ACTION_ADD_VIDEO",
        "addedVideoId": "video_id_to_add"
      }
    ]
  }
  ```

- **Remove Videos Action**:
  ```json
  {
    "context": { "client": { "clientName": "WEB_REMIX", "clientVersion": "1.20260822.01.00" } },
    "playlistId": "PL_TARGET_PLAYLIST_ID",
    "actions": [
      {
        "action": "ACTION_REMOVE_VIDEO",
        "setVideoId": "set_video_id",
        "removedVideoId": "video_id_to_remove"
      }
    ]
  }
  ```

#### 2.2.3 Search Endpoint (`/youtubei/v1/search`)

- **Payload**:
  ```json
  {
    "context": {
      "client": {
        "clientName": "WEB_REMIX",
        "clientVersion": "1.20260822.01.00",
        "hl": "zh-CN",
        "gl": "US"
      }
    },
    "query": "Song Title Artist Name",
    "params": "Eg-KAQwIABAAGAAgACgB"
  }
  ```

---

## 3. Reporting & Validation Contract

```go
type Reporter interface {
    // Summarize prints migration statistics to stdout.
    Summarize(spotifyPath, resultPath string) error

    // Validate verifies invariant assertions across source, result, and report datasets.
    Validate(spotifyPath, resultPath, reportPath string) error

    // GenerateReport persists the canonical report file.
    GenerateReport(resultPath, reportPath string) error
}
```

---

## 4. Canonical Domain Entity Contracts (`internal/model`)

### 4.1 Synchronization Result (`SyncResult`)
```go
type SyncResult struct {
    Direction          string         `json:"direction"`
    SourcePlatform     string         `json:"sourcePlatform"`
    TargetPlatform     string         `json:"targetPlatform"`
    PlaylistID         string         `json:"playlistId"`
    PlaylistURL        string         `json:"playlistUrl"`
    WebURL             string         `json:"webUrl,omitempty"`
    Title              string         `json:"title"`
    SourcePlaylistURL  string         `json:"sourcePlaylistUrl,omitempty"`
    TotalSourceTracks  int            `json:"totalSourceTracks"`
    AddedTracks        int            `json:"addedTracks"`
    SkippedTracks      int            `json:"skippedTracks"`
    Skipped            []SkippedTrack `json:"skipped"`
    AddedAfterReview   []AddedTrack   `json:"addedAfterReview,omitempty"`
    RemovedExtraTracks []RemovedTrack `json:"removedExtraTracks,omitempty"`
    LastSyncedAt       string         `json:"lastSyncedAt"`
    Verification       *Verification  `json:"verification,omitempty"`
}
```

### 4.2 Track Entities
```go
type SkippedTrack struct {
    Index   int      `json:"index"`
    Title   string   `json:"title"`
    Artists []string `json:"artists"`
    Reason  string   `json:"reason"`
}

type AddedTrack struct {
    Index            int      `json:"index"`
    Title            string   `json:"title"`
    Artists          []string `json:"artists"`
    TargetTrackID    string   `json:"targetTrackId"`
    DestinationTitle string   `json:"destinationTitle,omitempty"`
}

type RemovedTrack struct {
    TargetTrackID string   `json:"targetTrackId"`
    Title         string   `json:"title"`
    Artists       []string `json:"artists"`
}
```

