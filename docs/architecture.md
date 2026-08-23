# Architecture & Component Design: playlistsync

## 1. System Overview

`playlistsync` is a modular, high-reliability Go application designed to synchronize playlists bidirectionally between streaming platforms (Spotify and YouTube Music) with differential reconciliation, conservative multi-factor candidate matching, and automated invariant verification.

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

---

## 2. Layer Architecture & Responsibilities

### 2.1 Presentation Layer (`cmd/playlistsync`)
- **CLI Command Dispatching**: Implements `sync`, `login`, `inspect`, `verify`, and `report` subcommands.
- **Option & Flag Parsing**: Handles parameters such as `--from`, `--to`, `--proxy`, `--clean-extra`, `--yes`, `--concurrency`, and `--output-dir`.
- **Interactive Prompts**: Manages terminal-based candidate review interactions and maps system outcomes to standard exit codes.

### 2.2 Domain Layer (`internal/model`)
- **Canonical Domain Models**: Defines platform-agnostic models (`SpotifyPlaylist`, `SpotifyTrack`, `YTMPlaylist`, `YTMTrack`, `SyncResult`, `SkippedTrack`, `AddedTrack`, `RemovedTrack`, `Verification`).
- **Zero-Dependency Architecture**: Pure Go types with JSON serialization tags, devoid of external library or protocol dependencies.

### 2.3 Engine Layer (`internal/engine`)
- **`diff.go`**: Computes differential plans comparing source and target track collections. Implements Unicode-aware text normalization, CJK simplified/traditional conversion, and multi-factor fuzzy confidence scoring.
- **`review.go`**: Encapsulates interactive terminal decision hooks for human review of moderate-confidence candidates.
- **`syncer.go`**: High-level workflow orchestrator managing target playlist discovery/creation, concurrent candidate search, batch insertions, and extraneous track pruning.

### 2.4 Reporting & Audit Layer (`internal/report`)
- **Summary Visualizer (`Summarize`)**: Generates human-readable console metrics and track breakdown summaries.
- **Invariant Verifier (`Validate`)**: Enforces 4 formal data integrity invariants across source, result, and report datasets.
- **Artifact Generator (`GenerateReport`)**: Persists canonical JSON audit reports using atomic file operations.

### 2.5 Provider Adapters (`internal/spotify`, `internal/ytmusic`)
- **`internal/spotify`**: Manages Spotify playlist file I/O (JSON/CSV) and Web Player GraphQL API integration.
- **`internal/ytmusic`**: Handles authenticated Innertube API communication with dynamic `SAPISIDHASH` header generation, search query execution, batch additions/removals, and continuation token recursion.

### 2.6 Authentication & Infrastructure Layer (`internal/auth`, `internal/config`)
- **`internal/auth`**: Manages Chromium browser lifecycle via CDP over direct loopback WebSocket, extracts session cookies, generates dynamic TOTP tokens, and maintains secured credential storage.
- **`internal/config`**: Resolves global application configuration, environment variable cascades, and standardized file paths.

---

## 3. Dependency Graph & Data Flow

```
[User Command] ──> cmd/playlistsync
                         │
                         ▼
                  internal/engine (Syncer, Diff, Review)
                   │             │               │
      ┌────────────┘             │               └────────────┐
      ▼                          ▼                            ▼
internal/spotify        internal/ytmusic              internal/report
 (Reader, Client)        (Innertube Client)          (Reporter, Validator)
      │                          │                            │
      └────────────┬─────────────┘                            │
                   ▼                                          │
             internal/auth                                    │
        (CDP, Session, Proxy)                                 │
                   │                                          │
                   └──────────────────┬───────────────────────┘
                                      ▼
                                internal/model
```
