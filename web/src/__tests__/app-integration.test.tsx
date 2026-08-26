/**
 * App dispatch integration tests (review MAJOR-1): drives App through the
 * full SSE -> FSM wiring with a mocked ApiClient/connectSSE, proving:
 *   - boot -> CONFIGURING (both platforms authenticated)
 *   - UI Start -> SCANNING_DIFF
 *   - INVARIANT_SNAPSHOT during SCANNING_DIFF -> INVARIANT_HEALTH_CHECK
 *   - INVARIANT_SNAPSHOT broadcast during APPLYING_MUTATIONS must NOT throw
 *   - INVARIANT_SNAPSHOT broadcast during COMPLETED_SUCCESS must NOT throw
 *   - illegal DIFF_PROGRESS dispatch dropped silently (no crash)
 *   - FATAL -> CRITICAL_ERROR banner
 *   - ErrorBoundary renders a recovery panel instead of a white screen
 */
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { act, fireEvent, render, screen, waitFor, cleanup } from '@testing-library/react';
import type { CockpitSSEEvent, SessionResponse } from '../types/contracts';

/** Deferred for controlling when applyMutations settles per test. */
function deferred<T>(): { promise: Promise<T>; resolve: (v: T) => void; reject: (e: unknown) => void } {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const TOKEN = 'a3f2c9d8e7b6a5f4c3d2e1f0a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2a1b0';
const SESSION: SessionResponse = {
  session_id: 'sess-test-1',
  status: 'READY',
  port: 3080,
  client_count: 1,
  started_at: '2025-01-01T00:00:00Z',
  last_heartbeat_at: '2025-01-01T00:00:10Z',
  invariants_summary: {
    count_conserved: true,
    uniqueness_valid: true,
    order_lis_monotonic: true,
    diff_complete: true,
    zero_trace_clean: true,
  },
};

function snapshotEvent(allPassed: boolean, ts: string): CockpitSSEEvent {
  return {
    type: 'INVARIANT_SNAPSHOT',
    id: 1,
    data: {
      source_total: 2,
      synced_count: 2,
      skipped_count: 0,
      failed_count: 0,
      is_count_conserved: allPassed,
      has_duplicate_target_ids: !allPassed,
      duplicate_target_ids: [],
      lis_disorder_ratio: 0,
      disordered_indices: [],
      is_diff_complete: allPassed,
      is_zero_trace_clean: allPassed,
      evaluated_at: ts,
      all_passed: allPassed,
    },
  };
}

// Hoisted mutable mock state: connectSSE onEvent capture + current apply promise.
// Optional fields (no `undefined as T` casts) keep the file zero-`as`.
const mockState = vi.hoisted(() => {
  const state: {
    onEvent?: (ev: CockpitSSEEvent) => void;
    applyDeferred?: { promise: Promise<{ applied: boolean }>; resolve: (v: { applied: boolean }) => void };
    /** When set, getDiff returns this pending diff to hold a resync open. */
    gapHold?: { promise: Promise<null>; resolve: (v: null) => void };
    applyCalls: number;
  } = { applyCalls: 0 };
  return state;
});

vi.mock('../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/client')>();
  return {
    ...actual,
    readTokenFromURL: () => TOKEN,
    stripTokenFromURL: vi.fn(),
    ApiClient: vi.fn().mockImplementation(() => ({
      getSession: vi.fn().mockResolvedValue(SESSION),
      getAuthStatus: vi.fn().mockResolvedValue({
        spotify: { authenticated: true, userDisplayName: 'tester' },
        youtubeMusic: { authenticated: true, accountName: 'yt-tester', authType: 'cookie' },
      }),
      listReports: vi.fn().mockResolvedValue([]),
      getDiff: vi.fn().mockImplementation(() => {
        const hold = mockState.gapHold;
        if (hold) return hold.promise;
        return Promise.resolve(null);
      }),
      startReconcile: vi.fn().mockResolvedValue({ job_id: 'rec_1', status: 'running', estimated_total_tracks: 2 }),
      applyMutations: vi.fn().mockImplementation(() => {
        mockState.applyCalls += 1;
        const d = mockState.applyDeferred;
        if (!d) return Promise.resolve({ applied: true });
        return d.promise;
      }),
      verifyInvariants: vi.fn().mockResolvedValue({
        source_total: 0, synced_count: 0, skipped_count: 0, failed_count: 0,
        is_count_conserved: true, has_duplicate_target_ids: false, duplicate_target_ids: [],
        lis_disorder_ratio: 0, disordered_indices: [], is_diff_complete: true,
        is_zero_trace_clean: true, evaluated_at: '', all_passed: true,
      }),
      heartbeat: vi.fn().mockResolvedValue({ status: 'ALIVE', timestamp: 0 }),
      shutdown: vi.fn(),
      inspectPlaylist: vi.fn(),
      submitDecision: vi.fn().mockResolvedValue({ success: true, resumed_at: 0 }),
      reportExportURL: (id: string, format: string): string => `/api/v1/reports/${id}/export?format=${format}&token=${TOKEN}`,
      withToken: (p: string): string => `${p}?token=${TOKEN}`,
    })),
    connectSSE: vi.fn().mockImplementation((opts: { onEvent: (ev: CockpitSSEEvent) => void; client: unknown }) => {
      mockState.onEvent = opts.onEvent;
      const close = vi.fn(() => {
        mockState.onEvent = undefined;
      });
      return { close, get closed() { return false; } };
    }),
  };
});

import App from '../App';
import { ErrorBoundary } from '../components/ErrorBoundary';

async function fireSSE(ev: CockpitSSEEvent): Promise<void> {
  const handler = mockState.onEvent;
  if (!handler) throw new Error('connectSSE.onEvent not captured yet');
  await act(async () => {
    handler(ev);
  });
}

function bannerStatus(): string | null {
  const el = document.querySelector('.status-banner');
  return el ? el.getAttribute('data-status') : null;
}

/** Common setup: boot to CONFIGURING, navigate to Reconcile, start scan. */
async function bootAndStartScan(source: string, target: string): Promise<void> {
  render(<App />);
  await waitFor(() => expect(bannerStatus()).toBe('CONFIGURING'));
  fireEvent.click(screen.getByRole('button', { name: /Reconcile/ }));
  fireEvent.change(screen.getByPlaceholderText('Spotify playlist URL or ID, or drop playlist JSON'), { target: { value: source } });
  fireEvent.change(screen.getByPlaceholderText('YouTube Music playlist ID, or drop playlist JSON'), { target: { value: target } });
  fireEvent.click(screen.getByRole('button', { name: /Start Reconcile/ }));
  await waitFor(() => expect(bannerStatus()).toBe('SCANNING_DIFF'));
}

beforeEach(() => {
  mockState.applyCalls = 0;
  mockState.applyDeferred = undefined;
  mockState.gapHold = undefined;
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('App SSE -> FSM dispatch wiring', () => {
  it('boots to CONFIGURING when both platforms are authenticated', async () => {
    render(<App />);
    await waitFor(() => expect(bannerStatus()).toBe('CONFIGURING'));
    expect(screen.getByText(/playlistsync cockpit/)).toBeInTheDocument();
  });

  it('INVARIANT_SNAPSHOT during SCANNING_DIFF advances to INVARIANT_HEALTH_CHECK', async () => {
    await bootAndStartScan('src-1', 'dst-1');
    await fireSSE(snapshotEvent(true, 't0'));
    expect(bannerStatus()).toBe('INVARIANT_HEALTH_CHECK');
    expect(screen.getByText(/Invariants PASSED/)).toBeInTheDocument();
  });

  it('DIFF_COMPLETE triggers verifyInvariants and unlocks Apply with truthful allPassed (QC BLOCKER-2)', async () => {
    await bootAndStartScan('src-dc', 'dst-dc');
    // Backend does NOT emit INVARIANT_SNAPSHOT after a diff scan; it emits
    // DIFF_COMPLETE. The App must call GET /verify/invariants itself and
    // dispatch SCAN_COMPLETE with the REAL verdict (mock returns all_passed:
    // true), so the Apply button unlocks instead of being locked forever.
    await fireSSE({ type: 'DIFF_COMPLETE', id: 5, data: { status: 'complete', added: 2, removed: 1, retained: 10, skipped: 0 } });
    await waitFor(() => expect(bannerStatus()).toBe('INVARIANT_HEALTH_CHECK'));
    expect(screen.getByTestId('apply-button')).toBeEnabled();
  });

  it('INVARIANT_SNAPSHOT broadcast during APPLYING_MUTATIONS does not throw (reconcile.go preflight)', async () => {
    await bootAndStartScan('src-2', 'dst-2');
    await fireSSE(snapshotEvent(true, 't0'));
    expect(bannerStatus()).toBe('INVARIANT_HEALTH_CHECK');

    // Second snapshot while already in INVARIANT_HEALTH_CHECK: dropped silently.
    await fireSSE(snapshotEvent(true, 't1'));
    expect(bannerStatus()).toBe('INVARIANT_HEALTH_CHECK');

    // Apply with a PENDING applyMutations -> APPLYING_MUTATIONS.
    const d = deferred<{ applied: boolean }>();
    mockState.applyDeferred = d;
    fireEvent.click(screen.getByTestId('apply-button'));
    await waitFor(() => expect(bannerStatus()).toBe('APPLYING_MUTATIONS'));

    // Backend broadcasts INVARIANT_SNAPSHOT during apply preflight -> no throw,
    // state stays APPLYING_MUTATIONS.
    await fireSSE(snapshotEvent(true, 't2'));
    expect(bannerStatus()).toBe('APPLYING_MUTATIONS');
    expect(screen.getByTestId('apply-button')).toBeDisabled();

    // Complete the apply -> COMPLETED_SUCCESS (cleanup for later assertions).
    d.resolve({ applied: true });
    await waitFor(() => expect(bannerStatus()).toBe('COMPLETED_SUCCESS'));
  });

  it('INVARIANT_SNAPSHOT broadcast during COMPLETED_SUCCESS does not throw', async () => {
    await bootAndStartScan('src-3', 'dst-3');
    await fireSSE(snapshotEvent(true, 't0'));
    expect(bannerStatus()).toBe('INVARIANT_HEALTH_CHECK');

    // Apply resolves immediately -> COMPLETED_SUCCESS.
    fireEvent.click(screen.getByTestId('apply-button'));
    await waitFor(() => expect(bannerStatus()).toBe('COMPLETED_SUCCESS'));

    // Snapshot broadcast at COMPLETED_SUCCESS -> must not throw, banner stays.
    await fireSSE(snapshotEvent(true, 't1'));
    expect(bannerStatus()).toBe('COMPLETED_SUCCESS');
  });

  it('illegal DIFF_PROGRESS from CONFIGURING is dropped without crashing', async () => {
    render(<App />);
    await waitFor(() => expect(bannerStatus()).toBe('CONFIGURING'));
    // DIFF_PROGRESS is legal only from SCANNING_DIFF — dispatch must be refused.
    await fireSSE({ type: 'DIFF_PROGRESS', id: 1, data: { worker_id: 'w', processed: 1, total: 2, current_track: 'T1' } });
    expect(bannerStatus()).toBe('CONFIGURING');
    // The shell must still be mounted (no white-screen crash).
    expect(document.querySelector('.app-shell')).toBeInTheDocument();
    expect(screen.getByText('CONFIGURING')).toBeInTheDocument();
  });

  it('FATAL event renders CRITICAL_ERROR banner', async () => {
    render(<App />);
    await waitFor(() => expect(bannerStatus()).toBe('CONFIGURING'));
    await fireSSE({ type: 'RECONCILE_FAILED', id: 3, data: { error: 'boom' } });
    await waitFor(() => expect(bannerStatus()).toBe('CRITICAL_ERROR'));
    expect(screen.getByText(/ERROR RECONCILE_FAILED/)).toBeInTheDocument();
  });

  it('New Sync / Reset returns CRITICAL_ERROR to CONFIGURING without reload (QC MAJOR-2)', async () => {
    render(<App />);
    await waitFor(() => expect(bannerStatus()).toBe('CONFIGURING'));
    await fireSSE({ type: 'RECONCILE_FAILED', id: 3, data: { error: 'boom' } });
    await waitFor(() => expect(bannerStatus()).toBe('CRITICAL_ERROR'));
    const resetBtn = screen.getByTestId('reset-button');
    expect(resetBtn).toBeInTheDocument();
    fireEvent.click(resetBtn);
    await waitFor(() => expect(bannerStatus()).toBe('CONFIGURING'));
    // The error banner is cleared by the reset.
    expect(screen.queryByText(/ERROR RECONCILE_FAILED/)).not.toBeInTheDocument();
    // And a fresh run can start again (Start Reconcile input visible).
    expect(screen.getByPlaceholderText('Spotify playlist URL or ID, or drop playlist JSON')).toBeInTheDocument();
  });

  it('keyboard shortcut a does NOT apply when invariants fail (qc1 M-1)', async () => {
    mockState.applyCalls = 0;
    await bootAndStartScan('src-ka', 'dst-ka');
    // Failing snapshot -> INVARIANT_HEALTH_CHECK with allPassed=false:
    // the Apply button is disabled and pressing `a` must NOT call
    // applyMutations (which would 409 server-side).
    await fireSSE(snapshotEvent(false, 't0'));
    expect(bannerStatus()).toBe('INVARIANT_HEALTH_CHECK');
    expect(screen.getByTestId('apply-button')).toBeDisabled();

    fireEvent.keyDown(window, { key: 'a' });
    await waitFor(() => expect(mockState.applyCalls).toBe(0));
    expect(bannerStatus()).toBe('INVARIANT_HEALTH_CHECK');
  });

  it('keyboard shortcut a applies when invariants pass (qc1 M-1 positive)', async () => {
    mockState.applyCalls = 0;
    await bootAndStartScan('src-kb', 'dst-kb');
    await fireSSE(snapshotEvent(true, 't0'));
    expect(bannerStatus()).toBe('INVARIANT_HEALTH_CHECK');
    expect(screen.getByTestId('apply-button')).toBeEnabled();

    // Hold applyMutations pending so we can observe the APPLYING_MUTATIONS
    // state before the immediate resolution races to COMPLETED_SUCCESS.
    const d = deferred<{ applied: boolean }>();
    mockState.applyDeferred = d;
    fireEvent.keyDown(window, { key: 'a' });
    await waitFor(() => expect(mockState.applyCalls).toBe(1));
    expect(bannerStatus()).toBe('APPLYING_MUTATIONS');
    d.resolve({ applied: true });
    await waitFor(() => expect(bannerStatus()).toBe('COMPLETED_SUCCESS'));
  });

  it('GAP_FALLBACK shows resync banner and refetches state (qc3 Warning-1)', async () => {
    render(<App />);
    await waitFor(() => expect(bannerStatus()).toBe('CONFIGURING'));

    // Hold the resync (getDiff) open so the transient notice is observable.
    const hold = deferred<null>();
    mockState.gapHold = hold;
    await fireSSE({ type: 'GAP_FALLBACK', id: 11, data: { message: 'history gap' } });
    // User notification during the resync:
    expect(screen.getByText(/Event stream gap detected/)).toBeInTheDocument();

    // Once the resync settles, the notice clears itself (only its own text).
    hold.resolve(null);
    await waitFor(() => expect(screen.queryByText(/Event stream gap detected/)).not.toBeInTheDocument());
    // Cockpit survived and is still usable (navigate to the reconcile view).
    fireEvent.click(screen.getByRole('button', { name: /Reconcile/ }));
    expect(screen.getByPlaceholderText('Spotify playlist URL or ID, or drop playlist JSON')).toBeInTheDocument();
    expect(bannerStatus()).toBe('CONFIGURING');
  });
});

describe('ErrorBoundary', () => {
  it('renders a recovery panel instead of a white screen when a child throws', () => {
    const Bomb = (): never => {
      throw new Error('render exploded');
    };
    const { container } = render(
      <ErrorBoundary>
        <Bomb />
      </ErrorBoundary>,
    );
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.getByText('render exploded')).toBeInTheDocument();
    expect(container.textContent).toContain('Cockpit crashed');
    expect(screen.getByRole('button', { name: 'Reload view' })).toBeInTheDocument();
  });
});