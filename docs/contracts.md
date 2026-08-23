# Provider Interface Contracts & Protocols

## 1. Go Interface Contracts

### 1.1 Spotify Contracts (`internal/spotify`)

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
    FindPlaylistByName(searchDir string, name string) (*model.SpotifyPlaylist, string, error)
}
```

#### 1.1.2 Live Online Client Contract (`SpotifyClient`)
```go
type SpotifyClient interface {
    // FindPlaylist resolves a playlist by exact name, Spotify ID, or share URL.
    FindPlaylist(nameOrIDOrURL string) (*model.SpotifyPlaylist, error)

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

### 1.2 YouTube Music Provider Contract (`internal/ytmusic`)

```go
type YouTubeMusicClient interface {
    // GetPlaylist retrieves playlist metadata and full track items using pagination continuations.
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

### 1.3 Reporting & Validation Contract (`internal/report`)

```go
type Reporter interface {
    // Summarize prints human-readable migration statistics to stdout.
    Summarize(resultPath string) error

    // Validate verifies invariant assertions across source, result, and report datasets.
    Validate(sourcePath, resultPath, reportPath string) error

    // GenerateReport persists the canonical report file atomically.
    GenerateReport(resultPath, reportPath string) error
}
```

---

## 2. YouTube Music Innertube Wire Protocol

### 2.1 Authentication & Request Signing

Authenticated Innertube endpoints require dynamic `SAPISIDHASH` computation from session cookies (`SAPISID` or `__Secure-3PAPISID`).

#### Wire Headers:
```http
POST /youtubei/v1/<endpoint>?prettyPrint=false HTTP/1.1
Host: music.youtube.com
Content-Type: application/json
User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36
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

### 2.2 Wire Endpoints

#### 2.2.1 Browse Playlist (`/youtubei/v1/browse`)

- **Initial Browse Payload**:
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
- **Continuation Pagination**:
  Query parameter `continuation=<continuationToken>` with context body.

#### 2.2.2 Edit Playlist (`/youtubei/v1/browse/edit_playlist`)

- **Add Tracks Action**:
  ```json
  {
    "context": {
      "client": {
        "clientName": "WEB_REMIX",
        "clientVersion": "1.20260822.01.00"
      }
    },
    "playlistId": "PL_TARGET_PLAYLIST_ID",
    "actions": [
      {
        "action": "ACTION_ADD_VIDEO",
        "addedVideoId": "VIDEO_ID_TO_ADD"
      }
    ]
  }
  ```

- **Remove Tracks Action**:
  ```json
  {
    "context": {
      "client": {
        "clientName": "WEB_REMIX",
        "clientVersion": "1.20260822.01.00"
      }
    },
    "playlistId": "PL_TARGET_PLAYLIST_ID",
    "actions": [
      {
        "action": "ACTION_REMOVE_VIDEO",
        "setVideoId": "SET_VIDEO_ID_UUID",
        "removedVideoId": "VIDEO_ID_TO_REMOVE"
      }
    ]
  }
  ```

#### 2.2.3 Search Songs (`/youtubei/v1/search`)

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
    "query": "Track Title Artist Name",
    "params": "EgWKAQIIAWoSEAUQAxAEEAkQChAOEBAQFRAR"
  }
  ```

---

## 3. Canonical Domain Model Schemas (`internal/model`)

### 3.1 Spotify Models (`internal/model/spotify.go`)

```go
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

type SpotifyPlaylist struct {
    Platform          string         `json:"platform"`
    PlaylistName      string         `json:"playlistName"`
    SourcePlaylistURL string         `json:"sourcePlaylistUrl"`
    ExpectedCount     int            `json:"expectedCount"`
    CollectedCount    int            `json:"collectedCount"`
    Tracks            []SpotifyTrack `json:"tracks"`
}
```

### 3.2 YouTube Music Models (`internal/model/ytmusic.go`)

```go
type YTMTrack struct {
    VideoID    string   `json:"videoId"`
    SetVideoID string   `json:"setVideoId,omitempty"`
    Title      string   `json:"title"`
    Artists    []string `json:"artists"`
    Duration   string   `json:"duration,omitempty"`
}

type YTMPlaylist struct {
    ID          string     `json:"id"`
    Title       string     `json:"title"`
    Description string     `json:"description,omitempty"`
    TrackCount  int        `json:"trackCount"`
    Tracks      []YTMTrack `json:"tracks"`
}

type YTMPlaylistSummary struct {
    ID    string `json:"id"`
    Title string `json:"title"`
}

type YTMSearchResult struct {
    VideoID  string   `json:"videoId"`
    Title    string   `json:"title"`
    Artists  []string `json:"artists"`
    Duration string   `json:"duration,omitempty"`
    Score    int      `json:"score,omitempty"`
}
```

### 3.3 Synchronization & Execution Models (`internal/model/sync.go`)

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

type Verification struct {
    PageTitle      string `json:"pageTitle"`
    PageTrackCount int    `json:"pageTrackCount"`
    Description    string `json:"description,omitempty"`
}
```
