import { rmSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { getServerState, serverStateExists, runtimeDir, type ServerState } from './helpers';

/**
 * Global teardown: graceful shutdown through the cockpit endpoint (the server
 * drains in-flight writes within its 5s budget and exits), then an ACTIVE
 * poll for the process to disappear (qc1 MAJOR-2 / qc2 MAJOR-1 — replaces a
 * fixed sleep), a force-terminate fallback (taskkill /F /T on Windows, which
 * tears down the whole process tree), and finally a retry-guarded removal of
 * the gitignored runtime dir (zero-trace: the workspace is nested inside it,
 * so a single rmSync covers everything).
 */
export default async function globalTeardown(): Promise<void> {
  if (!serverStateExists()) return;
  const state = getServerState();

  // 1. Graceful shutdown via the cockpit endpoint.
  try {
    const parsed = new URL(state.url);
    const resp = await fetch(
      `http://${parsed.host}/api/v1/session/shutdown?token=${encodeURIComponent(state.token)}`,
      { method: 'POST', headers: { Authorization: `Bearer ${state.token}` }, body: '{}' },
    );
    await resp.arrayBuffer();
  } catch {
    // Server unreachable already.
  }

  // 2. Actively poll for process exit (up to 7s), replacing the fixed sleep.
  const exited = await pollForExit(state, 7_000);
  if (!exited) {
    forceKill(state);
  }

  // 3. Single retry-guarded removal of the whole runtime dir (workspace is
  //    nested inside; separately deleting state.workspace would double-remove
  //    and can race the process shutdown on Windows).
  rmSync(runtimeDir, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
}

/** Polls process.kill(pid, 0) until the process is gone or the budget expires. */
function pollForExit(state: ServerState, budgetMs: number): Promise<boolean> {
  return new Promise((res) => {
    const deadline = Date.now() + budgetMs;
    const tick = (): void => {
      if (!isAlive(state.pid) || Date.now() >= deadline) {
        res(!isAlive(state.pid));
        return;
      }
      setTimeout(tick, 200);
    };
    tick();
  });
}

/** True when a process with the given pid still exists. */
function isAlive(pid: number): boolean {
  if (!pid) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

/**
 * Force-terminates the server. On Windows, taskkill /F /T also kills child
 * processes (the Go binary spawns no children, but a /T tree kill is the
 * zero-trace-safe guarantee); elsewhere SIGKILL.
 */
function forceKill(state: ServerState): void {
  try {
    if (process.platform === 'win32') {
      spawnSync('taskkill', ['/F', '/T', '/PID', String(state.pid)], { stdio: 'ignore' });
    } else {
      process.kill(state.pid, 'SIGKILL');
    }
  } catch {
    // Already gone.
  }
}