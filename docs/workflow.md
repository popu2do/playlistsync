# Operational Workflow & Procedures: playlistsync

## 1. Standard Synchronization Workflow

### Step 1: Authenticate Credentials or Place Source Data
Ensure credentials are authenticated via CDP login or placed in the `output/auth/` directory:
- `output/auth/spotify_credentials.json` (Spotify session credentials / cookie)
- `output/auth/ytmusic_credentials.json` (YouTube Music session credentials / headers)
- `output/spotify_<playlist>_source.json` (Cached source playlist track data, or automatically fetched via Web API / Embed)

Alternatively, run `playlistsync login all` or `playlistsync login <platform>` to automatically capture session credentials.

### Step 2: Run Synchronization
Execute playlist synchronization using canonical platform names or aliases:
```bash
# Spotify to YouTube Music (Default)
playlistsync sync <playlist_name> --from=spotify --to=youtube-music

# YouTube Music to Spotify
playlistsync sync <playlist_name> --from=youtube-music --to=spotify
```

### Step 3: Inspect & Validate Results
Inspect synchronization metrics and verify invariant constraints:
```bash
# Inspect summary
playlistsync inspect <playlist_name>

# Validate dataset integrity
playlistsync verify <playlist_name>

# Regenerate report artifact
playlistsync report <playlist_name>
```

---

## 2. File Location Reference
- `output/auth/spotify_credentials.json`: Authenticated Spotify session credentials.
- `output/auth/ytmusic_credentials.json`: Authenticated YouTube Music session headers.
- `output/spotify_<playlist>_source.json`: Spotify source playlist track snapshot (backward-compatible with `<playlist>_playlist.json`).
- `output/ytmusic_<playlist>_source.json`: YouTube Music source playlist track snapshot.
- `output/spotify_to_ytmusic_<playlist>_result.json`: Raw execution outcome for Spotify -> YouTube Music sync.
- `output/spotify_to_ytmusic_<playlist>_report.json`: Canonical migration audit report for Spotify -> YouTube Music sync.
- `output/ytmusic_to_spotify_<playlist>_result.json`: Raw execution outcome for YouTube Music -> Spotify sync.
- `output/ytmusic_to_spotify_<playlist>_report.json`: Canonical migration audit report for YouTube Music -> Spotify sync.
