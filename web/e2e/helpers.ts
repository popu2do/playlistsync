import { spawn, type ChildProcess } from 'node:child_process';
import { existsSync, readFileSync, mkdtempSync, rmSync, openSync, closeSync, mkdirSync } from 'node:fs';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

/**
 * Shared E2E helpers: resolve the single binary, read the main server state
 * written by global-setup, and spawn dedicated cockpit servers (TC-E2E-05
 * watchdog uses this with a short idle timeout).
 */

const here = dirname(fileURLToPath(import.meta.url));
export const runtimeDir = join(here, '.runtime');
export const serverStatePath = join(runtimeDir, 'server.json');
mkdirSync(runtimeDir, { recursive: true });

export interface ServerState {
  /** Banner URL: http://127.0.0.1:<port>?token=<token> */
  url: string;
  port: number;
  token: string;
  pid: number;
  /** Hermetic temp workspace (seeded with fixtures; removed in teardown). */
  workspace: string;
}

export function getServerState(): ServerState {
  return JSON.parse(readFileSync(serverStatePath, 'utf8')) as ServerState;
}

export function serverStateExists(): boolean {
  return existsSync(serverStatePath);
}

/** Resolves the playlistsync single binary (env override or repo bin/). */
export function resolveBinaryPath(): string {
  const fromEnv = process.env.PLAYLISTSYNC_BIN;
  if (fromEnv) {
    if (!existsSync(fromEnv)) {
      throw new Error(`PLAYLISTSYNC_BIN=${fromEnv} does not exist`);
    }
    return fromEnv;
  }
  const repoRoot = resolve(here, '..', '..');
  const bin = join(repoRoot, 'bin', process.platform === 'win32' ? 'playlistsync.exe' : 'playlistsync');
  if (!existsSync(bin)) {
    throw new Error(
      `Single binary not found at ${bin}. Run \`make build\` (web SPA then Go binary) before \`make test-e2e\`.`,
    );
  }
  return bin;
}

const BANNER_RE = /playlistsync web: (http:\/\/127\.0\.0\.1:\d+\?token=[0-9a-f]{64})/;

export interface SpawnedServer {
  proc: ChildProcess;
  url: string;
  port: number;
  token: string;
  workspace: string;
  logPath: string;
  /** Resolves true if the process exits within timeoutMs. */
  waitForExit(timeoutMs: number): Promise<boolean>;
  /** Graceful API shutdown + hard-kill fallback + workspace cleanup. */
  cleanup(): Promise<void>;
}

/**
 * Spawns `playlistsync web --port 0` with a hermetic env (temp output/auth
 * dirs — no network credential lookups, no touching the user's `output/`).
 * stdout is redirected to a log FILE (not a pipe) so the parent can poll the
 * token banner without any pipe-capture coupling. Resolves once the banner
 * URL is observed.
 *
 * Workspaces live under the gitignored e2e/.runtime/ dir (Playwright's test
 * process re-points os.tmpdir() into its own test-results artifact dir —
 * roaming there leaks residue; .runtime is removed wholesale by
 * global-teardown, keeping the E2E zero-trace).
 */
export async function spawnCockpitServer(
  opts: { workspace?: string; idleTimeoutSeconds?: number } = {},
): Promise<SpawnedServer> {
  const binPath = resolveBinaryPath();
  const workspace = opts.workspace ?? mkdtempSync(join(runtimeDir, 'workspace-'));
  const outDir = join(workspace, 'out');
  const authDir = join(workspace, 'auth');
  mkdirSync(outDir, { recursive: true });
  mkdirSync(authDir, { recursive: true });

  const logPath = join(workspace, 'server.log');
  const logFd = openSync(logPath, 'w');

  const env: NodeJS.ProcessEnv = {
    ...process.env,
    PLAYLISTSYNC_OUTPUT_DIR: outDir,
    PLAYLISTSYNC_AUTH_DIR: authDir,
    // Explicit empty credential targets: auth status is unauthenticated and
    // fails FAST locally (no network validation round-trips).
    PLAYLISTSYNC_SPOTIFY_AUTH: join(authDir, 'spotify_credentials.json'),
    PLAYLISTSYNC_YTM_AUTH: join(authDir, 'ytmusic_credentials.json'),
    PLAYLISTSYNC_WEB_IDLE_TIMEOUT: opts.idleTimeoutSeconds ? `${opts.idleTimeoutSeconds}s` : '',
  };

  const proc = spawn(binPath, ['web', '--port', '0'], {
    env,
    stdio: ['ignore', logFd, logFd],
    windowsHide: true,
  });
  // Parent fd copy: the child inherited the open description; close ours.
  closeSync(logFd);

  let banner: string;
  try {
    banner = await waitForBanner(logPath, 30_000);
  } catch (err) {
    proc.kill();
    rmSync(workspace, { recursive: true, force: true });
    throw err;
  }

  const parsed = new URL(banner);
  const token = parsed.searchParams.get('token');
  if (!token) {
    proc.kill();
    rmSync(workspace, { recursive: true, force: true });
    throw new Error(`Cockpit banner missing token: ${banner}`);
  }

  return {
    proc,
    url: banner,
    port: Number(parsed.port),
    token,
    workspace,
    logPath,
    waitForExit: (timeoutMs) => waitForProcExit(proc, timeoutMs),
    cleanup: async () => {
      await shutdownViaAPI(banner);
      const exited = await waitForProcExit(proc, 10_000);
      if (!exited && proc.exitCode === null) {
        proc.kill();
      }
      rmSync(workspace, { recursive: true, force: true });
    },
  };
}

/** Polls the server log for the token banner line. */
function waitForBanner(logPath: string, timeoutMs: number): Promise<string> {
  return new Promise((resolvePromise, reject) => {
    const deadline = Date.now() + timeoutMs;
    const timer = setInterval(() => {
      if (!existsSync(logPath)) return;
      const content = readFileSync(logPath, 'utf8');
      const hit = content.match(BANNER_RE);
      if (hit) {
        clearInterval(timer);
        resolvePromise(hit[1]);
        return;
      }
      if (Date.now() > deadline) {
        clearInterval(timer);
        reject(new Error(`Timed out waiting for cockpit banner in ${logPath}:\n${content.slice(-2000)}`));
      }
    }, 100);
  });
}

function waitForProcExit(proc: ChildProcess, timeoutMs: number): Promise<boolean> {
  if (proc.exitCode !== null) return Promise.resolve(true);
  return new Promise((res) => {
    const timer = setTimeout(() => res(false), timeoutMs);
    proc.once('exit', () => {
      clearTimeout(timer);
      res(true);
    });
  });
}

/** Best-effort graceful shutdown through POST /api/v1/session/shutdown. */
async function shutdownViaAPI(bannerUrl: string): Promise<void> {
  try {
    const parsed = new URL(bannerUrl);
    const token = parsed.searchParams.get('token') ?? '';
    const host = parsed.host;
    const resp = await fetch(`http://${host}/api/v1/session/shutdown?token=${encodeURIComponent(token)}`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'application/json',
      },
      body: '{}',
    });
    await resp.arrayBuffer();
  } catch {
    // Server may already be gone; the caller falls back to a kill.
  }
}