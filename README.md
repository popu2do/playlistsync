# playlistsync - Universal Playlist Synchronization CLI (Go)

`playlistsync` is a production-grade Go CLI application designed to synchronize music playlists between streaming platforms (Spotify and YouTube Music) with conservative fuzzy matching, automated pruning of extraneous tracks, and auditable differential reporting.

## Architecture

The codebase is structured in Go following a clean layered architecture, strictly decoupling domain data models, provider adapters, synchronization engine, and CLI presentation:

```
.
├── go.mod
├── internal/
│   ├── model/           # Canonical domain entities and data definitions
│   │   ├── spotify.go   # SpotifyPlaylist, SpotifyTrack
│   │   ├── ytmusic.go   # YTMPlaylist, YTMTrack, YTMSearchResult, YTMEditAction
│   │   └── sync.go      # SyncResult, SkippedTrack, AddedTrack, RemovedTrack
│   │
│   ├── spotify/         # Spotify data access layer
│   │   └── reader.go    # JSON and CSV reader/writer implementations
│   │
│   ├── ytmusic/         # YouTube Music Innertube API adapter
│   │   ├── client.go    # HTTP client with SAPISID authentication
│   │   └── parser.go    # Innertube payload and continuation parser
│   │
│   ├── engine/          # Core synchronization engine
│   │   ├── diff.go      # Diffing and confidence matching algorithms
│   │   └── syncer.go    # End-to-end sync workflow orchestration
│   │
│   └── report/          # Audit and verification layer
│       └── reporter.go  # Summary, invariant validation, and report generator
│
├── cmd/
│   └── playlistsync/
│       └── main.go      # CLI entrypoint (binary: playlistsync)
│
├── docs/                # Technical documentation and specifications
│   ├── architecture.md
│   ├── contracts.md
│   ├── decisions.md
│   ├── design.md
│   ├── specification.md
│   └── workflow.md
│
└── output/              # 100% ignored by Git (all runtime data, credentials & reports)
    ├── auth/            # Platform credentials & browser profiles
    ├── spotify_<name>_source.json                # Source playlist snapshot
    ├── spotify_to_ytmusic_<name>_result.json     # Execution result
    └── spotify_to_ytmusic_<name>_report.json     # Audit report
```

## CLI Usage

### Build

```powershell
go build -buildvcs=false -o bin/playlistsync.exe ./cmd/playlistsync
```

### Commands

#### 1. Synchronize Playlist

Synchronize a playlist between platforms:

```powershell
# Spotify to YouTube Music (Default)
.\bin\playlistsync.exe sync <playlist_name> --from=spotify --to=youtube-music

# YouTube Music to Spotify
.\bin\playlistsync.exe sync <playlist_name> --from=youtube-music --to=spotify
```

Options:
- `--from=<spotify|youtube-music>`: Source platform (`spotify` or `youtube-music`; aliases `spo`, `sp`, `youtube`, `ytmusic`, `ytm`, `yt` supported).
- `--to=<youtube-music|spotify>`: Destination platform (`youtube-music` or `spotify`).
- `--clean-extra`: When true (default), prunes unmapped tracks in destination.
- `--proxy`: HTTP/HTTPS proxy URL (falls back to system proxy and `HTTP_PROXY`).
- `--yes` / `-y`: Automatically confirm prompts for non-interactive execution.
- `--concurrency` / `-c`: Concurrent candidate search worker count (default: 6).
- `--output-dir`: Custom working and artifact directory (default: `output/`).
- `--json`: Direct path to source playlist JSON file.
- `--playlist-id` / `--id`: Explicit target playlist ID.

#### 2. Authenticate Credentials (login)

Authenticate Spotify and YouTube Music accounts via automated Chrome/Edge CDP login:

```powershell
# Authenticate all platforms (Spotify & YouTube Music)
.\bin\playlistsync.exe login all

# Authenticate specific platform
.\bin\playlistsync.exe login spotify
.\bin\playlistsync.exe login youtube-music

# Force re-authentication bypassing cached validation
.\bin\playlistsync.exe login spotify --force
```

#### 3. Inspect Status

Display human-readable sync summary and metadata for a playlist:

```powershell
.\bin\playlistsync.exe inspect <playlist_name>
```

#### 4. Verify Invariants

Verify data consistency and integrity assertions:

```powershell
.\bin\playlistsync.exe verify <playlist_name>
```

#### 5. Report Generation

Regenerate canonical report artifact:

```powershell
.\bin\playlistsync.exe report <playlist_name>
```

## Testing

Run unit tests across all packages:

```powershell
go test ./...
```
