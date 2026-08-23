# Architecture Decision Records (ADR)

This document captures the foundational architectural decisions, technical trade-offs, protocol reverse-engineering strategies, and consistency models of `playlistsync`.

---

## ADR Index

- [ADR-001: Go Architecture & Pragmatic Dependency Model](#adr-001-go-architecture--pragmatic-dependency-model)
- [ADR-002: CDP-Based Hybrid Visual/Headless Authentication Architecture](#adr-002-cdp-based-hybrid-visualheadless-authentication-architecture)
- [ADR-003: Innertube API Protocol & Dynamic SAPISIDHASH Authorization](#adr-003-innertube-api-protocol--dynamic-sapisidhash-authorization)
- [ADR-004: Multi-Factor Conservative Fuzzy Confidence Scoring & Normalization Pipeline](#adr-004-multi-factor-conservative-fuzzy-confidence-scoring--normalization-pipeline)
- [ADR-005: Differential Set Reconciliation Engine & Extraneous Track Pruning Policy](#adr-005-differential-set-reconciliation-engine--extraneous-track-pruning-policy)
- [ADR-006: Adaptive Proxy Cascade & Direct Loopback CDP Isolation](#adr-006-adaptive-proxy-cascade--direct-loopback-cdp-isolation)
- [ADR-007: Invariant Verification, Deterministic Artifact Persistence & Traceable Audit Design](#adr-007-invariant-verification-deterministic-artifact-persistence--traceable-audit-design)

---

## ADR-001: Go Architecture & Pragmatic Dependency Model

### Status
Accepted

### Context
Cross-platform playlist synchronization involves concurrent metadata resolution, network I/O, browser automation, and data diffing. Traditional implementations relying on Python or Electron/Node.js often suffer from heavy runtime dependency overhead, multi-megabyte distribution footprints, and brittle third-party wrapper dependencies that break when upstream platform APIs shift.

### Decision Drivers
- **Portability & Distribution**: Single self-contained binary distribution across Windows, macOS, and Linux without requiring runtime environments or external interpreters.
- **Performance & Footprint**: Sub-15ms cold start times, low memory overhead (<30MB), and high-throughput concurrent search pipelines without GIL limitations.
- **Engineering Pragmatism**: Implement core business logic and API protocols directly in standard Go, while selectively utilizing mature, lightweight libraries (such as `gorilla/websocket` and `spf13/cobra`) for robust protocol handling.

### Decision
We build `playlistsync` using Go (1.20+), utilizing standard library packages (`net/http`, `crypto/sha1`, `encoding/json`, `unicode`) for core engine workflows, while adopting established libraries for CLI interaction and WebSocket transport.

### Alternatives Considered
- *Python (ytmusicapi + spotipy)*: High prototyping speed, but requires Python environment setup, virtual environments, C-extensions, and has packaging bloat.
- *Rust*: High performance and memory safety, but incurs longer compilation times and requires extensive third-party crate dependencies for standard HTTP tasks.

### Consequences
- **Positive**: Single static binary distribution (~10MB), rapid execution speed, and transparent network protocol control.
- **Negative**: Platform API contracts and Innertube wire payloads must be directly maintained within the repository.

---

## ADR-002: CDP-Based Hybrid Visual/Headless Authentication Architecture

### Status
Accepted

### Context
YouTube Music does not provide personal library modification endpoints via public OAuth (the public YouTube Data API v3 incurs strict quota limits of 50 units per insert against a 10,000 daily limit, and lacks `WEB_REMIX` audio alignment). Spotify public OAuth requires setting up redirect servers for user-private playlists. Extracting session cookies manually via browser developer tools is error-prone for non-technical users.

### Decision Drivers
- **Zero Friction Authentication**: Allow users to log in through standard browser interfaces (including QR code scanning and 2FA).
- **Environment Isolation**: Prevent modification or corruption of the host system's primary browser profile.
- **Driver Independence**: Avoid external dependencies on Selenium or standalone `chromedriver` executables.

### Decision
We implement a Chrome DevTools Protocol (CDP) client over direct loopback WebSocket connections (`ws://127.0.0.1:9222`). The application launches an isolated Chromium instance (Google Chrome or Microsoft Edge) with dedicated user profiles (`output/auth/.chrome_<platform>`), monitors authentication state transitions, extracts required cookies (`SAPISID`, `sp_dc`), and securely persists credentials to `output/auth/`.

### Alternatives Considered
- *YouTube Data API v3 OAuth*: Insufficient quota (200 tracks per day maximum), missing audio catalog attributes.
- *Manual Cookie / cURL Copy-Paste*: Highly error-prone and poor user experience.
- *Selenium / WebDriver*: Requires downloading platform-specific driver binaries and Java/Python bindings.

### Consequences
- **Positive**: Automated credential capture with full support for interactive MFA/CAPTCHA; isolated profile persistence enables silent session re-use.
- **Negative**: Requires a Chromium-based browser (Chrome or Edge) installed on the host system.

---

## ADR-003: Innertube API Protocol & Dynamic SAPISIDHASH Authorization

### Status
Accepted

### Context
YouTube Music Web Client operations communicate directly with Google's Innertube endpoints (`https://music.youtube.com/youtubei/v1/...`). State-modifying requests that lack a matching dynamic `Authorization` header are rejected by the gateway with `401 Unauthorized`.

### Decision Drivers
- **Cryptographic Correctness**: Generate valid, non-expiring request signatures from session cookies.
- **Client Emulation Fidelity**: Emulate the official `WEB_REMIX` client context to prevent automated anti-bot throttling.

### Decision
We implement the `SAPISIDHASH` authorization generator in `internal/ytmusic/client.go`:
1. Obtain current Unix epoch timestamp in seconds ($T$).
2. Format raw string: `$T + " " + SAPISID + " " + "https://music.youtube.com"`.
3. Compute SHA-1 digest and format the header: `Authorization: SAPISIDHASH <T>_<Hex(SHA1)>`.
4. Attach standard Innertube `context` payloads (`WEB_REMIX`) to all POST requests.

### Alternatives Considered
- *Static Bearer Tokens*: Expire within 1 hour and fail without automated refresh mechanisms.
- *Unsigned Requests*: Rejected by Google's API gateway for library write operations.

### Consequences
- **Positive**: Uncapped playlist querying, searching, item appending, and pruning with session lifetime parity.
- **Negative**: Changes to Innertube endpoint signatures require adapter updates in `internal/ytmusic`.

---

## ADR-004: Multi-Factor Conservative Fuzzy Confidence Scoring & Normalization Pipeline

### Status
Accepted

### Context
Track metadata between Spotify and YouTube Music exhibits high variance: bracketed video tags (`[Official 4K MV]`), traditional vs. simplified Chinese characters, whitespace differences, and multiple artist delimiter formats. Naive string matching produces severe false positive rates (e.g., matching a 3-minute studio track to a 1-hour live compilation).

### Decision Drivers
- **Conservative Matching Policy**: Avoid false positive insertions into user playlists; prefer skipping borderline tracks for manual review over incorrect additions.
- **Robust Multi-Factor Evaluation**: Combine title similarity, artist overlap, and physical duration proximity into a deterministic scoring model.

### Decision
We implement a three-stage normalization and scoring pipeline in `internal/engine/diff.go`:
1. **Normalization Pipeline**: Full-width/half-width normalization, CJK traditional-to-simplified conversion via OpenCC dictionary tables, and regex-based noise bracket stripping.
2. **Multi-Factor Scoring Model**:
   - $W_{\text{title}} \in [0, 55]$: Evaluates exact, substring, and rune-level similarity.
   - $W_{\text{artist}} \in [-15, 30]$: Matches artist variants with penalties for explicit cross-artist mismatch.
   - $W_{\text{duration}} \in [-40, +15]$: Rewards duration proximity ($\le 3s \to +15$) and heavily penalizes duration discrepancies ($>45s \to -40$).
3. **Threshold Gate**: $\text{Score} \ge 70$ for automated addition; $50 \le \text{Score} < 70$ triggers interactive human review.

### Alternatives Considered
- *Unweighted Levenshtein Distance*: Readily misclassifies covers, live versions, and long compilation videos.
- *External Commercial Matching APIs*: Adds latency, cost, and external API dependencies.

### Consequences
- **Positive**: 100% precision on tested heterogeneous and multilingual track datasets without false-positive pollution.
- **Negative**: Highly obscure tracks with differing titles or durations may require manual review.

---

## ADR-005: Differential Set Reconciliation Engine & Extraneous Track Pruning Policy

### Status
Accepted

### Context
Users repeatedly execute synchronization workflows as playlists evolve. Naive full-recreation strategies delete and recreate playlists, invalidating sharing URLs, losing playlist follower counts, and consuming excessive API quotas.

### Decision Drivers
- **Idempotency**: Repeated runs must converge to the desired target state without creating duplicate entries.
- **Minimal Mutation Set**: Execute only necessary `AddPlaylistItems` and `RemovePlaylistItems` operations.
- **Controllable Pruning**: Allow users to configure whether target-only extraneous tracks are removed via `--clean-extra`.

### Decision
We implement differential set reconciliation in `internal/engine/diff.go` (`ComputeDiff`):
- Formulate a three-way diff partition: `Matched` (retained), `Missing` (to be added), and `Extraneous` (to be pruned).
- Execute batch additions for missing tracks and, when `--clean-extra=true` (default), perform targeted removals using `setVideoId` references.

### Alternatives Considered
- *Destructive Recreation*: Drop and recreate target playlist on each sync. Rejected due to URL invalidation and rate limits.
- *Blind Append*: Append all tracks on every run. Rejected due to duplicate track accumulation.

### Consequences
- **Positive**: Fast subsequent synchronization runs, preserved playlist IDs and URLs, and strictly idempotent convergence.
- **Negative**: Requires fetching and diffing remote target playlist items before executing mutations.

---

## ADR-006: Adaptive Proxy Cascade & Direct Loopback CDP Isolation

### Status
Accepted

### Context
Access to Spotify and YouTube Music services frequently requires proxy routing (e.g., Clash, v2ray). However, setting standard global environment proxies (`HTTP_PROXY`, `HTTPS_PROXY`) causes Go's default HTTP client to route local Chrome debugging requests (`127.0.0.1:9222`) through the proxy, leading to connection refusals (502 / Connection Refused).

### Decision Drivers
- **Local Loopback Isolation**: Guarantee that internal CDP communication always bypasses proxies.
- **Automated Proxy Discovery**: Automatically resolve system and proxy configuration for external API requests without requiring manual user flags.

### Decision
We implement a dedicated network topology in `internal/auth`:
1. **Isolated CDP Transport**: Instantiate a dedicated HTTP/WebSocket transport with `Proxy: nil` exclusively for loopback CDP traffic.
2. **Three-Tier Proxy Cascade**:
   - Priority 1: CLI flag `--proxy`.
   - Priority 2: Standard environment variables (`HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`).
   - Priority 3: Windows Registry (`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`).

### Alternatives Considered
- *Global Unset*: Requiring users to configure `NO_PROXY=127.0.0.1` manually. Rejected due to frequent user misconfiguration.

### Consequences
- **Positive**: Seamless execution across diverse network environments and proxy configurations.
- **Negative**: Windows Registry inspection introduces OS-specific branch logic in `internal/auth/proxy.go`.

---

## ADR-007: Invariant Verification, Deterministic Artifact Persistence & Traceable Audit Design

### Status
Accepted

### Context
Data migration tools must provide verifiable guarantees that no items were silently dropped, duplicated, or corrupted. Users and automated pipelines require persistent, structured audit trails.

### Decision Drivers
- **Mathematical Integrity Guarantee**: Enforce formal consistency invariants across source, result, and report datasets.
- **Atomic File Persistence**: Prevent partial file writes during power interruption or process termination.
- **Auditability**: Provide human-readable summaries (`inspect`) and automated integrity validation (`verify`).

### Decision
We enforce 4 formal data integrity invariants in `internal/report`:
- **Invariant 1 (Total Conservation)**: $N_{\text{source}} = N_{\text{added}} + N_{\text{skipped}}$.
- **Invariant 2 (Target Capacity Parity)**: $N_{\text{target\_final}} = N_{\text{added}}$.
- **Invariant 3 (Attributed Skipped Reasons)**: $\forall t \in \text{Skipped}, t.\text{Reason} \ne \text{""} \land t.\text{Index} \ge 1$.
- **Invariant 4 (Resource ID Validity)**: $\forall t \in \text{Added}, t.\text{TargetTrackID} \ne \text{""} \land t.\text{Title} \ne \text{""}$.

All persistent outputs (`_source.json`, `_result.json`, `_report.json`) are written via atomic temporary-file-and-rename operations under `output/`.

### Alternatives Considered
- *Log-Only Output*: Printing results only to stdout without standardized JSON artifacts. Rejected due to lack of CI and downstream tool integration.

### Consequences
- **Positive**: Complete auditability, fail-fast consistency verification, and zero risk of corrupted output files.
- **Negative**: Requires disk write access for the designated `output/` directory.
