import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { spawnCockpitServer, runtimeDir, serverStatePath, type ServerState } from './helpers';

/**
 * Global setup (plan-wc-04 Tier 3): spawns the REAL single binary with a
 * hermetic workspace under the gitignored e2e/.runtime/ dir, seeds
 * deterministic fixtures (all-green SyncResult for the invariant radar; a
 * report artifact for the archive + export), and records the token banner so
 * every spec can open the cockpit.
 */
export default async function globalSetup(): Promise<void> {
  const workspace = mkdtempSync(join(runtimeDir, 'workspace-'));
  const outDir = join(workspace, 'out');
  const authDir = join(workspace, 'auth');
  mkdirSync(outDir, { recursive: true });
  mkdirSync(authDir, { recursive: true });

  // Fixture 1 — all-green SyncResult (verify: Total 4 = Synced 3 + Skipped 1
  // + Failed 0; empty target universe keeps Uniqueness/Order/Diff vacuous-pass).
  writeFileSync(
    join(outDir, 'spotify_to_ytmusic_demo_result.json'),
    JSON.stringify(
      {
        direction: 'spotify_to_youtube_music',
        sourcePlatform: 'spotify',
        targetPlatform: 'youtube-music',
        title: 'E2E Demo',
        totalSourceTracks: 4,
        addedTracks: 3,
        skippedTracks: 1,
        syncOrder: true,
        skipped: [{ index: 4, title: 'C', artists: ['X'], reason: 'low confidence' }],
        addedAfterReview: [
          { index: 1, title: 'A', artists: ['X'], targetTrackId: '' },
          { index: 2, title: 'B', artists: ['Y'], targetTrackId: '' },
          { index: 3, title: 'C', artists: ['Z'], targetTrackId: '' },
        ],
        removedExtraTracks: [],
        lastSyncedAt: '2026-01-01T00:00:00Z',
      },
      null,
      2,
    ),
    'utf8',
  );

  // Fixture 2 — report artifact listed by GET /api/v1/reports and exported.
  writeFileSync(
    join(outDir, 'e2e_demo_report.json'),
    JSON.stringify(
      {
        direction: 'spotify_to_youtube_music',
        title: 'E2E Demo',
        totalSourceTracks: 4,
        addedTracks: 3,
        skippedTracks: 1,
        lastSyncedAt: '2026-01-01T00:00:00Z',
      },
      null,
      2,
    ),
    'utf8',
  );

  const srv = await spawnCockpitServer({ workspace });

  const state: ServerState = {
    url: srv.url,
    port: srv.port,
    token: srv.token,
    pid: srv.proc.pid ?? 0,
    workspace,
  };
  mkdirSync(runtimeDir, { recursive: true });
  writeFileSync(serverStatePath, JSON.stringify(state, null, 2), 'utf8');
  console.log(`[e2e] cockpit ${srv.url}`);
}