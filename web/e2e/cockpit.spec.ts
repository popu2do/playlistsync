import { test, expect, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { getServerState, serverStateExists, spawnCockpitServer, type ServerState } from './helpers';

/**
 * Tier 3 E2E — plan-wc-04 TC-E2E-01..06.
 *
 * Runs against the REAL `playlistsync web` single binary (spawned in
 * global-setup with a hermetic temp workspace). A fresh cockpit has NO
 * credentials, so the FSM boot lands on AUTH_REQUIRED (truthful, zero-trace
 * state) — TC-E2E-01 documents this; the CONFIGURING acceptance is
 * unreachable hermetically because auth checks require live credentials.
 */

test.describe.configure({ mode: 'serial' });

let state: ServerState;

test.beforeAll(() => {
  if (!serverStateExists()) {
    throw new Error('server state missing — run `pnpm exec playwright test` (global-setup must start first)');
  }
  state = getServerState();
});

async function openCockpit(page: Page): Promise<void> {
  await page.goto(state.url);
  await expect(page.locator('.app-shell')).toBeVisible();
}

/* ------------------------------------------------------------------ */
/* TC-E2E-01 — cockpit boot + session token verification               */
/* ------------------------------------------------------------------ */
test('TC-E2E-01: cockpit boots, token is verified, UI loads in the fresh-cockpit FSM state', async ({ page }) => {
  // Negative first (fresh context, no sessionStorage leak): a banner-less URL
  // MUST refuse to boot the cockpit — proves the session token gate (spec 05
  // §2.2). The token is mirrored to sessionStorage only after a banner visit,
  // so this check must run before the positive path in the same context.
  const bare = new URL(state.url);
  bare.search = '';
  bare.hash = '';
  await page.goto(bare.toString());
  await expect(page.locator('.app-shell').getByText('Missing session token')).toBeVisible();

  // Positive: open the banner URL (carries ?token=...) -> shell boots.
  await page.goto(state.url);
  await expect(page.locator('.app-shell')).toBeVisible();

  // The session token was ACCEPTED: GET /api/v1/session returned the real
  // session id + bound port (otherwise the nav shows 'session …' / 'port ?').
  const sessionNav = page.locator('.nav-session');
  await expect(sessionNav.locator('.token-hint')).toContainText(/port \d+/);
  await expect(sessionNav).not.toContainText('session …');

  // FSM booted past IDLE. Fresh hermetic cockpit has no credentials -> the
  // honest zero-trace state is AUTH_REQUIRED with both platforms missing.
  // (The plan's "CONFIGURING" acceptance needs live credentials, which would
  // break hermeticity — see the task report.)
  const banner = page.locator('.status-banner');
  await expect(banner).toHaveAttribute('data-status', 'AUTH_REQUIRED');
  await expect(banner).toContainText('Auth required');

  // All five subsystem nav entries rendered.
  for (const label of ['Auth Vault', 'Reconcile', 'Inspector', 'Invariants', 'Reports']) {
    await expect(page.locator('.nav-item', { hasText: label })).toBeVisible();
  }
});

/* ------------------------------------------------------------------ */
/* TC-E2E-02 — AuthVault renders Spotify & YTM cards, unauthenticated  */
/* ------------------------------------------------------------------ */
test('TC-E2E-02: AuthVault shows Spotify & YTM cards in unauthenticated state', async ({ page }) => {
  await openCockpit(page);
  const vault = page.locator('[data-testid="auth-vault"]');
  await expect(vault).toBeVisible();

  const spotify = vault.locator('.vault-card.vault-spotify');
  const ytm = vault.locator('.vault-card.vault-ytm');
  await expect(spotify).toBeVisible();
  await expect(ytm).toBeVisible();

  // Unauthenticated markers on both cards (no credentials -> "no credentials").
  await expect(spotify.locator('.vault-missing')).toHaveText('no credentials');
  await expect(ytm.locator('.vault-missing')).toHaveText('no credentials');

  // Login entry points rendered.
  await expect(spotify.locator('button', { hasText: 'Authorize (OAuth PKCE)' })).toBeVisible();
  await expect(ytm.locator('button', { hasText: 'CDP Login (browser)' })).toBeVisible();

  // Proxy configuration input rendered.
  await expect(vault.locator('#vault-proxy')).toBeVisible();
});

/* ------------------------------------------------------------------ */
/* TC-E2E-03 — Reconcile inputs + Clean Extra confirm modal gating     */
/* ------------------------------------------------------------------ */
test('TC-E2E-03: reconcile inputs fillable; Clean Extra modal blocks until unlock', async ({ page }) => {
  await openCockpit(page);
  await page.locator('.nav-item', { hasText: 'Reconcile' }).click();
  const cockpit = page.locator('[data-testid="reconcile-cockpit"]');
  await expect(cockpit).toBeVisible();

  // Source + target drop-zone inputs are present and fillable.
  const dropInputs = cockpit.locator('.drop-zone input[type="text"]');
  await expect(dropInputs).toHaveCount(2);
  await dropInputs.nth(0).fill('https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M');
  await dropInputs.nth(1).fill('PLdemoE2ETargetPlaylist012');
  await expect(dropInputs.nth(0)).toHaveValue(/37i9dQZF1DXcBWIGoYBM5M/);

  // Start becomes enabled once both sides are filled.
  const startBtn = cockpit.locator('button', { hasText: 'Start Reconcile' });
  await expect(startBtn).toBeEnabled();

  // Toggle the destructive Clean Extra option.
  await cockpit.locator('.checkbox input[type="checkbox"]').nth(0).check();
  await expect(cockpit.locator('.checkbox').first()).toContainText('Clean extra tracks in target');

  // Start WITHOUT unlocking -> the danger modal appears and BLOCKS:
  // the confirm action is disabled until the user explicitly unlocks.
  await startBtn.click();
  const modal = page.locator('.modal-danger[role="dialog"]');
  await expect(modal).toBeVisible();
  await expect(modal).toContainText('Clean Extra — Destructive Operation');

  const confirm = modal.locator('[data-testid="confirm-clean"]');
  await expect(confirm).toBeDisabled();
  await expect(confirm).toHaveText(/Unlock to proceed/);

  // No scan started behind the modal (FSM untouched).
  await expect(page.locator('.status-banner')).toHaveAttribute('data-status', 'AUTH_REQUIRED');
  await expect(modal).toBeVisible();

  // Unlock enables the destructive confirm; Cancel dismisses without start.
  await modal.locator('[data-testid="clean-unlock"]').check();
  await expect(confirm).toBeEnabled();
  await expect(confirm).toHaveText(/Proceed with clean/);

  await modal.locator('button', { hasText: 'Cancel' }).click();
  await expect(modal).toBeHidden();
});

/* ------------------------------------------------------------------ */
/* TC-E2E-04 — InvariantMonitor: 5 invariants + radar rendered         */
/* ------------------------------------------------------------------ */
test('TC-E2E-04: InvariantMonitor renders the 5-invariant radar & formula', async ({ page }) => {
  await openCockpit(page);
  await page.locator('.nav-item', { hasText: 'Invariants' }).click();
  const monitor = page.locator('[data-testid="invariant-monitor"]');
  await expect(monitor).toBeVisible();

  // Empty state before any verification.
  await expect(monitor.locator('.inspector-empty')).toContainText('No invariant snapshot yet');

  // Verify the seeded all-green result -> radar renders with 5 rows.
  await monitor.locator('.verify-controls input').fill('demo');
  await monitor.locator('button', { hasText: 'Verify invariants' }).click();

  const radar = monitor.locator('[data-testid="inv-radar"]');
  await expect(radar).toBeVisible();
  await expect(radar.locator('.inv-radar-item')).toHaveCount(5);
  await expect(radar).toContainText('Count: Total = Synced + Skipped + Failed');
  await expect(radar).toContainText('Uniqueness: no duplicate target Track IDs');
  await expect(radar).toContainText('Diff: Added ∪ Removed ∪ Retained partitions');
  await expect(radar).toContainText('Zero-Trace: no residue / plaintext');
  await expect(radar).toContainText('Order: LIS monotonic');
  await expect(monitor.locator('.formula-verdict')).toHaveText('CONSERVED');

  // The persistent guardian bar also shows the 5 invariant tokens.
  const bar = page.locator('[data-testid="invariant-guardian-bar"]');
  await expect(bar).toBeVisible();
  await expect(bar.locator('.inv-item')).toHaveCount(5);
});

/* ------------------------------------------------------------------ */
/* TC-E2E-05 — Watchdog idle timeout -> SYSTEM_SHUTDOWN + process exit */
/* ------------------------------------------------------------------ */
test('TC-E2E-05: watchdog timeout broadcasts SYSTEM_SHUTDOWN and exits the process', async ({ page }) => {
  // Dedicated server with a short idle timeout via PLAYLISTSYNC_WEB_IDLE_TIMEOUT
  // (plan GC #2). 10s is well under the frontend's 30s heartbeat so the
  // watchdog fires deterministically between beats.
  const srv = await spawnCockpitServer({ idleTimeoutSeconds: 10 });
  try {
    await page.goto(srv.url);
    await expect(page.locator('.app-shell')).toBeVisible();

    // The SSE stream delivers SYSTEM_SHUTDOWN: the terminal dock auto-opens
    // and prints the shutdown line (App.tsx SYSTEM_SHUTDOWN handler).
    const dock = page.locator('[data-testid="terminal-dock"]');
    await expect(dock).toHaveClass(/is-open/, { timeout: 45_000 });
    await expect(dock.locator('.terminal-line')).toContainText('SYSTEM_SHUTDOWN', { timeout: 45_000 });
    await expect(dock.locator('.terminal-line')).toContainText('shutting down');

    // The process exits by itself (graceful watchdog shutdown).
    const exited = await srv.waitForExit(20_000);
    expect(exited).toBe(true);
  } finally {
    await srv.cleanup();
  }
});

/* ------------------------------------------------------------------ */
/* TC-E2E-06 — ReportArchive: report list + JSON export download       */
/* ------------------------------------------------------------------ */
test('TC-E2E-06: ReportArchive lists reports and downloads a JSON export', async ({ page }) => {
  await openCockpit(page);
  await page.locator('.nav-item', { hasText: 'Reports' }).click();
  const archive = page.locator('[data-testid="report-archive"]');
  await expect(archive).toBeVisible();

  // Seeded report artifact is listed.
  const node = archive.locator('.report-node', { hasText: 'e2e_demo' });
  await expect(node).toBeVisible();
  await expect(node.locator('.report-id')).toHaveText('e2e_demo');

  // JSON export triggers a file download.
  const downloadPromise = page.waitForEvent('download');
  await node.locator('a', { hasText: 'JSON' }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe('e2e_demo_report.json');

  // The downloaded payload is the seeded JSON (valid round-trip).
  const path = await download.path();
  if (!path) throw new Error('download produced no local file');
  const downloaded = JSON.parse(readFileSync(path, 'utf8')) as { title?: string };
  expect(downloaded.title).toBe('E2E Demo');
});