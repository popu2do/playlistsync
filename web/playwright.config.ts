import { defineConfig } from '@playwright/test';

/**
 * Tier 3 Playwright E2E — plan-wc-04 TDD packaging & verification.
 *
 * The suite boots the REAL `playlistsync web` single binary (embed.FS SPA) in
 * global-setup with a hermetic temp workspace (zero-trace: no touching the
 * user's `output/`), captures the token banner and runs the 6 cockpit TCs
 * against it. TC-E2E-05 spawns its own dedicated server with a short
 * PLAYLISTSYNC_WEB_IDLE_TIMEOUT (see web/e2e/helpers.ts).
 */
export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/global-setup.ts',
  globalTeardown: './e2e/global-teardown.ts',
  // Stateful shared server: run sequentially for deterministic coverage.
  fullyParallel: false,
  workers: 1,
  timeout: 120_000,
  expect: { timeout: 15_000 },
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['line']] : [['list']],
  use: {
    acceptDownloads: true,
    headless: true,
    // Local Windows dev box: reuse the installed Edge (no browser download).
    // Linux CI installs Playwright's chromium explicitly and can pin
    // E2E_CHANNEL=chromium. `msedge` matches Playwright's supported "stable"
    // Edge channel and requires no `playwright install` step locally.
    channel: process.env.E2E_CHANNEL || (process.platform === 'win32' ? 'msedge' : undefined),
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
});