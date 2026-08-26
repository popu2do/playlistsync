# playlistsync Domain Concepts

Core terminology, invariants, and architectural concepts for playlistsync.

## System Invariants

1. **Count Conservation**: `Source Total = Synced + Skipped + Failed`. Every item in the source playlist must be accounted for without loss.
2. **Uniqueness Guarantee**: Target playlist must not contain duplicate track IDs.
3. **Monotonic Order (LIS)**: Preserve source track order monotonically; disorder is quantified by Longest Increasing Subsequence (LIS) disorder ratio.
4. **Diff Partition Completeness**: `Added`, `Removed`, and `Retained` sets form a mutually disjoint, complete partition of the playlist reconciliation universe.
5. **Zero-Cloud & Zero-Trace**: No telemetry or egress to unapproved servers, zero plaintext credential storage on disk, zero background zombie processes, and zero unmanaged file residue.

## Web Cockpit Subsystems

- **Web Cockpit**: A first-class independent subcommand (`playlistsync web`) running an embedded, loopback-only (`127.0.0.1`) web application.
- **Auth Vault**: Subsystem managing Spotify PKCE OAuth2 and YouTube Music CDP (Chrome DevTools Protocol) headless/interactive browser authentication with real-time SSE step streaming.
- **Reconciliation Cockpit**: 3-way partition view (Added/Removed/Retained) with interactive track reordering, dual-column diff, and high-risk `--clean-extra` confirmation guard.
- **Dual-Column Arbitration**: Interactive resolution card presented when track matching confidence is ambiguous or multi-candidate.
- **Invariant Guardian & Monitor**: Real-time 5-axis invariant verification engine calculating partition math and gating mutations before application.
- **Watchdog Timer**: Lock-free atomic idle timer that automatically triggers shutdown and process termination after 15 minutes of inactivity.