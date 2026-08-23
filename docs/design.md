# System Design Specification: playlistsync

This document details the core architectural subsystems, algorithmic pipelines, protocol adapters, and data consistency models of `playlistsync`.

---

## 1. System Architecture & Lifecycle

### 1.1 End-to-End Execution Sequence

```
User (CLI)        Syncer (Engine)     Auth Validator        Provider Clients      Diff Engine
    │                   │                   │                      │                   │
    ├─ sync <name> ────>│                   │                      │                   │
    │                   ├─ ValidateCreds ──>│                      │                   │
    │                   │<─ Valid (Cached) ─┤                      │                   │
    │                   │                                          │                   │
    │                   ├─ Load Source Playlist Metadata ─────────>│                   │
    │                   │                                          │                   │
    │                   ├─ FindOrCreate Target Playlist ──────────>│                   │
    │                   │<─ Target Playlist ID & Remote Tracks ────┤                   │
    │                   │                                          │                   │
    │                   ├─ ComputeDiff(source, target) ───────────────────────────────>│
    │                   │<─ DiffPlan (Matched, Missing, Extra) ────────────────────────┤
    │                   │                                          │                   │
    │                   ├─ For each Missing: SearchSong ──────────>│                   │
    │                   │<─ Candidate Tracks ──────────────────────┤                   │
    │                   │  (EvaluateConfidence >= 70)             │                   │
    │                   │                                          │                   │
    │                   ├─ AddPlaylistItems(Approved Video IDs) ──>│                   │
    │                   │<─ Success ───────────────────────────────┤                   │
    │                   │                                          │                   │
    │                   ├─ CleanExtra ? RemovePlaylistItems ──────>│                   │
    │                   │<─ Success ───────────────────────────────┤                   │
    │                   │                                          │                   │
    │                   ├─ Fetch Target Verification ─────────────>│                   │
    │                   ├─ Generate Artifacts & Validate Invariants                    │
    │<─ Summary Print ──┤                                                              │
```

---

## 2. Authentication Subsystem & CDP Session Capture

### 2.1 Browser Automation via Chrome DevTools Protocol

```
+-------------------------------------------------------------------------+
|                            internal/auth                                |
|                                                                         |
|  +-----------------+    Exec     +-----------------------------------+  |
|  |   browser.go    | ----------> | chrome.exe / msedge.exe           |  |
|  |                 |             | --remote-debugging-port=9222      |  |
|  +-----------------+             | --user-data-dir=output/auth/...   |  |
|                                  +-----------------+-----------------+  |
|                                                    |                    |
|                                  Direct Loopback   v                    |
|  +-----------------+   HTTP/WS   +-----------------------------------+  |
|  |     cdp.go      | <---------> | /json/version & /json/list        |  |
|  | (Raw WS Client) |             | ws://127.0.0.1:9222/devtools/page |  |
|  +--------+--------+             +-----------------------------------+  |
|           │                                                             |
|           v JSON-RPC: Network.getCookies / Runtime.evaluate             |
|  +-----------------+                                                    |
|  |   storage.go    | ----------> output/auth/<platform>_credentials.json|
|  +-----------------+                                                    |
+-------------------------------------------------------------------------+
```

1. **Browser Discovery Cascade**:
   - Primary: Standard 64-bit and 32-bit `Program Files` Google Chrome locations.
   - Secondary: Microsoft Edge executable locations.
   - Fallback: System `PATH` search for `chrome`, `chromium`, `msedge`.

2. **Isolated User Data Directories**:
   - Spotify Profile: `output/auth/.chrome_spotify`
   - YouTube Music Profile: `output/auth/.chrome_ytmusic`
   - Preserves session cookies without interfering with the host system's default browser profiles.

3. **Direct Loopback CDP Transport**:
   - Uses an explicit un-proxied HTTP client (`http.Transport{Proxy: nil}`) to communicate with `127.0.0.1:9222`.
   - Prevents system proxy software from intercepting local Chromium debugging connections.

4. **Credential Validation & Probing**:
   - `ValidateSpotifyCredentials`: Probes Spotify Web Player `/path/to/profile` or client token endpoint.
   - `ValidateYTMCredentials`: Probes Innertube `/youtubei/v1/browse` to confirm `SAPISID` validity.
   - Unauthenticated states automatically trigger interactive CDP browser capture.

---

## 3. YouTube Music Innertube Wire Adapter

### 3.1 Dynamic `SAPISIDHASH` Authentication

Google Innertube APIs require dynamic SHA-1 authorization headers for all state-changing endpoints:

$$\text{Payload} = \text{UnixEpochSeconds}() + \text{" "} + \text{SAPISID} + \text{" "} + \text{"https://music.youtube.com"}$$

$$\text{Authorization} = \text{"SAPISIDHASH "} + \text{UnixEpochSeconds}() + \text{"_"} + \text{HexEncode}(\text{SHA1}(\text{Payload}))$$

### 3.2 Recursive Continuation Token Parsing

Playlists exceeding 100 tracks are paginated via `continuationEndpoint` tokens:
1. Parse `musicShelfRenderer.contents` from initial response.
2. Extract continuation token from `musicShelfRenderer.continuations[0].nextContinuationData.continuation`.
3. Recursively request `/youtubei/v1/browse?continuation=<token>` until no further continuation token is returned.

---

## 4. Text Normalization & Fuzzy Confidence Scoring

### 4.1 Text Normalization Pipeline

```
Raw Track Title: "【Official MV 4K】晴天（周杰倫 / Jay Chou）[1080p]〜"
      │
      ▼ 1. Full-width & Punctuation Normalization (normalizeUnicode)
      │    -> "[Official MV 4K]晴天(周杰倫 / Jay Chou)[1080p]~"
      │
      ▼ 2. Traditional to Simplified Chinese Conversion (convertT2S)
      │    -> "[Official MV 4K]晴天(周杰伦 / Jay Chou)[1080p]~"
      │
      ▼ 3. Bracket Noise & Video Tag Stripping (stripNoiseBrackets)
      │    -> "晴天"
      │
      ▼ 4. Whitespace & Casing Normalization (normalizeText)
      ▼
Normalized Title: "晴天"
```

### 4.2 Multi-Factor Weighted Scoring Model

Given a source track $S$ and candidate track $C$, the overall match confidence is evaluated as:

$$\text{Score}(S, C) = \min(100, \max(0, W_{\text{title}} + W_{\text{artist}} + W_{\text{duration}}))$$

```
+-------------------------------------------------------------------------+
|                  Multi-Factor Confidence Scoring Model                  |
+------------------------------------+------------------------------------+
| Factor                             | Score Range / Weight               |
+------------------------------------+------------------------------------+
| Title Match (W_title)              | 0 to 55 points                     |
| - Exact / Clean Title Equality     | +55 points                         |
| - Substring Containment            | +45 points                         |
| - Rune / Token Similarity (>=0.75) | +40 to +50 points                  |
| - Title Mismatch                   | 0 points                           |
+------------------------------------+------------------------------------+
| Artist Overlap (W_artist)          | -15 to +30 points                  |
| - Exact Artist Variant Match       | +30 points                         |
| - Fuzzy Substring / Similarity     | +25 points                         |
| - Artist Contained in Title        | +25 points                         |
| - Source Artist Not Provided       | +20 points                         |
| - Cross-Script / Empty Candidate   | 0 points (Neutral)                 |
| - Explicit Artist Mismatch         | -15 points (Penalty)               |
+------------------------------------+------------------------------------+
| Duration Proximity (W_duration)    | -40 to +15 points                  |
| - |Δt| <= 3s                       | +15 points                         |
| - 3s < |Δt| <= 8s                  | +10 points                         |
| - 8s < |Δt| <= 15s                 | +5 points                          |
| - 15s < |Δt| <= 25s                | 0 points (Neutral)                 |
| - 25s < |Δt| <= 45s                | -25 points (Penalty)               |
| - |Δt| > 45s                       | -40 points (Severe Penalty)        |
+------------------------------------+------------------------------------+
```

- **Confidence Threshold**: $\text{Score}(S, C) \ge 70$ is required for automatic candidate acceptance.
- **Interactive Review Window**: Candidates scoring between $50 \le \text{Score} < 70$ trigger interactive human confirmation during interactive CLI sessions.

---

## 5. Differential Reconciliation Engine (`ComputeDiff`)

The differential reconciliation engine compares the source playlist against the destination playlist to calculate minimal required mutations:

```
Inputs: SourcePlaylist (S), TargetPlaylist (T), KnownMappings (M)

1. Index TargetPlaylist tracks by TargetTrackID:
   TargetTrackMap = { t.VideoID -> t for t in T.Tracks }

2. For each track s in S.Tracks:
   a. If s.Index exists in M and M[s.Index] in TargetTrackMap:
      Mark as Matched (Retained)
   b. Else: Search T.Tracks for fuzzy match:
      If CalculateScore(s, t) >= 70:
         Mark as Matched (Retained) and record mapping
      Else:
         Mark as Missing (To be searched and added)

3. For each track t in T.Tracks:
   If t.VideoID not in Matched set:
      Mark as Extraneous (To be pruned if clean-extra=true)

Output: DiffPlan { Matched, Missing, Extraneous }
```

---

## 6. Data Integrity & Invariant Verification

Four formal invariant assertions are validated across migration datasets by `internal/report`:

| Invariant | Mathematical Formulation | Operational Semantic |
| :--- | :--- | :--- |
| **Invariant 1: Total Conservation** | $N_{\text{source}} = N_{\text{added}} + N_{\text{skipped}}$ | Every source track must be definitively accounted for as either added or skipped. |
| **Invariant 2: Target Capacity Parity** | $N_{\text{target\_final}} = N_{\text{added}}$ | The final target playlist track count must equal the total number of approved tracks. |
| **Invariant 3: Attributed Skipped Reasons** | $\forall t \in \text{Skipped}: t.\text{Reason} \ne \text{""} \land t.\text{Index} \ge 1$ | Every skipped track record must contain a non-empty causal explanation and valid 1-based index. |
| **Invariant 4: Resource ID Validity** | $\forall t \in \text{Added}: t.\text{TargetTrackID} \ne \text{""} \land t.\text{Title} \ne \text{""}$ | Every added track record must contain a valid, non-empty target platform resource ID and title. |

---

## 7. Artifact Persistence & Working Directory Layout

All operational artifacts and credentials are isolated under `output/`:

```
output/
├── auth/
│   ├── spotify_credentials.json           # Authenticated Spotify session credentials
│   ├── ytmusic_credentials.json           # Authenticated YouTube Music session cookies
│   ├── .chrome_spotify/                   # Isolated Chromium profile for Spotify
│   └── .chrome_ytmusic/                   # Isolated Chromium profile for YouTube Music
├── spotify_<playlist>_source.json         # Source Spotify playlist metadata snapshot
├── ytmusic_<playlist>_source.json         # Source YouTube Music playlist metadata snapshot
├── spotify_to_ytmusic_<playlist>_result.json  # Raw synchronization execution result
├── spotify_to_ytmusic_<playlist>_report.json  # Canonical migration audit report
├── ytmusic_to_spotify_<playlist>_result.json  # Reverse synchronization execution result
└── ytmusic_to_spotify_<playlist>_report.json  # Reverse migration audit report
```

- **Atomic Writes**: All JSON artifacts are written via temporary files and atomic rename operations to prevent partial or corrupted file writes.
- **Security Isolation**: `output/` is ignored by version control to prevent credential and personal playlist metadata leakage.
