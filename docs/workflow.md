# Operational Workflow & User Guide: playlistsync

## 1. Authentication & Prerequisites

`playlistsync` uses Chrome DevTools Protocol (CDP) to automatically capture authenticated session credentials into isolated profiles.

### Authenticating Platforms

```bash
# Authenticate both Spotify and YouTube Music
playlistsync login all

# Authenticate YouTube Music only
playlistsync login youtube-music

# Authenticate Spotify only
playlistsync login spotify

# Force re-authentication bypassing cached session validation
playlistsync login all --force
```

When triggered, an isolated browser window will open. Complete the login or QR code verification in the window. The CLI detects session cookies, saves them securely to `output/auth/`, and closes the browser automatically.

---

## 2. Migration & Synchronization Scenarios

### Scenario A: Spotify to YouTube Music

Synchronize a Spotify playlist to YouTube Music:

```bash
# Using concise specifier syntax (recommended)
playlistsync sync spotify:"My Favorite Songs" ytm:

# Using direct Spotify playlist URL
playlistsync sync https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M ytm:

# Using standard flag syntax
playlistsync sync "My Favorite Songs" --from=spotify --to=youtube-music

# Sync into an existing YouTube Music destination playlist ID
playlistsync sync spotify:"My Favorite Songs" ytm:PLxxxxxxxxxxxxxxxxxxxx
```

### Scenario B: YouTube Music to Spotify

```bash
# Using concise specifier syntax
playlistsync sync ytm:"My YTM Playlist" spotify:

# Using standard flag syntax
playlistsync sync "My YTM Playlist" --from=youtube-music --to=spotify
```

### Scenario C: Incremental Synchronization

When new tracks are added to the source playlist or existing tracks are re-arranged:

```bash
# Differential reconciliation will identify new tracks and add them without duplicating existing tracks
playlistsync sync "My Favorite Songs"

# Preserve extraneous tracks in the destination playlist without pruning
playlistsync sync "My Favorite Songs" --clean-extra=false
```

### Scenario D: Non-Interactive / CI Batch Automation

For headless, automated pipelines or unattended execution:

```bash
# Run with automatic confirmation for all prompts and reviews
playlistsync sync "My Favorite Songs" --yes --non-interactive

# Specify custom worker concurrency and proxy
playlistsync sync "My Favorite Songs" -y -c 8 --proxy="http://127.0.0.1:7890"
```

---

## 3. Post-Sync Inspection & Invariant Verification

### 3.1 Inspect Migration Summary

Display a human-readable summary of matched, added, and skipped tracks:

```bash
playlistsync inspect "My Favorite Songs"
```

### 3.2 Verify Invariant Integrity

Run the 4 formal data invariant assertions on migration datasets:

```bash
playlistsync verify "My Favorite Songs"
```

### 3.3 Regenerate Audit Report

Regenerate canonical JSON report artifacts:

```bash
playlistsync report "My Favorite Songs"
```

---

## 4. File Path & Artifact Mappings

| File Path | Description | Access Mode |
| :--- | :--- | :--- |
| `output/auth/spotify_credentials.json` | Authenticated Spotify session token and cookie state | Confidential (0600) |
| `output/auth/ytmusic_credentials.json` | Authenticated YouTube Music session headers & cookies | Confidential (0600) |
| `output/auth/.chrome_spotify/` | Isolated Chromium profile directory for Spotify | Cache / Private |
| `output/auth/.chrome_ytmusic/` | Isolated Chromium profile directory for YouTube Music | Cache / Private |
| `output/spotify_<playlist>_source.json` | Snapshot of source Spotify playlist track metadata | Read / Snapshot |
| `output/ytmusic_<playlist>_source.json` | Snapshot of source YouTube Music playlist track metadata | Read / Snapshot |
| `output/spotify_to_ytmusic_<playlist>_result.json` | Detailed raw execution output for Spotify -> YTM sync | Result Log |
| `output/spotify_to_ytmusic_<playlist>_report.json` | Canonical audit report for Spotify -> YTM sync | Audit Artifact |
| `output/ytmusic_to_spotify_<playlist>_result.json` | Detailed raw execution output for YTM -> Spotify sync | Result Log |
| `output/ytmusic_to_spotify_<playlist>_report.json` | Canonical audit report for YTM -> Spotify sync | Audit Artifact |
