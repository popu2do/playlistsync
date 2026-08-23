# Architecture & Component Design: playlistsync (Go)

## 1. System Overview

The **Universal Playlist Synchronization CLI** (`playlistsync`) is a modular, high-reliability Go application designed to synchronize playlists across Spotify and YouTube Music with differential reconciliation, conservative candidate matching, and auditability.

```
+-------------------------------------------------------------------------+
|                            cmd/playlistsync                             |
|          (CLI Parsing, Flag Routing, Signal & Context Management)       |
+------------------------------------+------------------------------------+
                                     |
                                     v
+-------------------------------------------------------------------------+
|                            internal/engine                              |
|           (Diffing Engine, Matching Heuristics, Sync Workflow)          |
+------------------+-----------------+-------------------+----------------+
                   |                 |                   |
                   v                 v                   v
+--------------------+ +--------------------+ +--------------------+
|  internal/spotify  | |  internal/ytmusic  | |  internal/report   |
| (Web API/GraphQL)  | |  (Innertube API)   | | (Audit/Validation) |
+--------------------+ +--------------------+ +--------------------+
                   |                 |                   |
                   +--------+--------+-------------------+
                            |
                            v
+-------------------------------------------------------------------------+
|                            internal/auth                                |
|        (CDP Automated Login, Token Probes, System Proxy & Storage)      |
+------------------------------------+------------------------------------+
                                     |
                                     v
+-------------------------------------------------------------------------+
|                            internal/model                               |
|        (Pure Domain Entities, Invariant Rules, Data Contracts)          |
+-------------------------------------------------------------------------+
```

## 2. Layer Definitions & Responsibilities

### 2.1 Presentation Layer (`cmd/playlistsync`)
- Handles CLI commands: `sync`, `login`, `inspect`, `verify`, `report`.
- Parses flags: `--from`, `--to`, `--proxy`, `--clean-extra`, `--yes`.
- Manages terminal review prompt interaction and process exit codes.

### 2.2 Domain Layer (`internal/model`)
- Declares platform-agnostic data models: `SpotifyPlaylist`, `SpotifyTrack`, `YTMPlaylist`, `YTMTrack`, `SyncResult`, `DiffPlan`.
- Pure Go structs with standard JSON serialization tags; zero external dependencies.

### 2.3 Engine Layer (`internal/engine`)
- **`diff.go`**: Pure diff engine comparing source and destination track sets. Implements conservative Unicode-aware title/artist matching heuristics.
- **`review.go`**: Interactive human review prompt hooks and decision state machines.
- **`syncer.go`**: High-level workflow orchestrator managing target playlist creation, search queries, incremental batch additions, and extraneous track pruning.

### 2.4 Reporting & Audit Layer (`internal/report`)
- Formats human-readable console summaries (`inspect`).
- Runs invariant integrity checks on source, sync result, and audit reports (`verify`).
- Generates canonical report JSON files (`report`).

### 2.5 Provider Adapters (`internal/spotify`, `internal/ytmusic`)
- **`internal/spotify`**: Encapsulates Spotify playlist file I/O (JSON/CSV) as well as live Web Player GraphQL API integration.
- **`internal/ytmusic`**: Encapsulates authenticated Innertube API communication with SAPISID authorization header generation and response parsing.

### 2.6 Authentication & Infrastructure Layer (`internal/auth`, `internal/config`)
- **`internal/auth`**: Isolated browser lifecycle management via CDP, token/cookie probes, TOTP token signature generation, and secure file permission enforcement (0600).
- **`internal/config`**: Global application configuration, environment variable precedence, and standardized file path resolutions.
