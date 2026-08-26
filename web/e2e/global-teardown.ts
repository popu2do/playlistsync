import { rmSync } from 'node:fs';
import { getServerState, serverStateExists, runtimeDir } from './helpers';

/**
 * Global teardown: graceful shutdown through the cockpit endpoint (the server
 * drains in-flight writes within its 5s budget and exits), a hard-kill
 * fallback for a stuck process, then removes the hermetic workspace and the
 * runtime state (zero-trace: no residue left behind).
 */
export default async function globalTeardown(): Promise<void> {
  if (!serverStateExists()) return;
  const state = getServerState();

  try {
    const parsed = new URL(state.url);
    const resp = await fetch(
      `http://${parsed.host}/api/v1/session/shutdown?token=${encodeURIComponent(state.token)}`,
      { method: 'POST', headers: { Authorization: `Bearer ${state.token}` }, body: '{}' },
    );
    await resp.arrayBuffer();
    // Give the 5s drain budget a moment to let the server exit cleanly.
    await new Promise((r) => setTimeout(r, 3500));
  } catch {
    // Server unreachable already.
  }

  try {
    process.kill(state.pid, 0); // probe
    process.kill(state.pid); // hard stop fallback (TerminateProcess on win32)
  } catch {
    // Already exited.
  }

  rmSync(state.workspace, { recursive: true, force: true });
  rmSync(runtimeDir, { recursive: true, force: true });
}