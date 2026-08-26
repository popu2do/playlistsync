/**
 * App — cockpit shell: token bootstrap (spec 05 §2.2), FSM driver (spec 04
 * §2), SSE wiring (spec 02 §3), heartbeat, keyboard navigation, five subsystem
 * views + guardian bar + terminal dock.
 */
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { ApiClient, connectSSE, readTokenFromURL, stripTokenFromURL, type SSEConnection } from './api/client';
import { cockpitReducer, canTransition, INITIAL_STATE, type FSMEvent } from './fsm/cockpit-fsm';
import { useKeyboardShortcuts } from './hooks/useKeyboardShortcuts';
import { useHeartbeat } from './hooks/useHeartbeat';
import type {
  ArbitrationRequestEvent,
  AuthStatusResponse,
  DiffResponse,
  InvariantSnapshot,
  ReportMeta,
  PlaylistInspectResponse,
  CandidateOption,
  SessionResponse,
} from './types/contracts';
import type { TerminalLine } from './components/TerminalDock';
import { logToLine, TerminalDock } from './components/TerminalDock';
import { InvariantGuardianBar, type InvariantFlags } from './components/InvariantGuardianBar';
import { AuthVault } from './views/AuthVault';
import { ReconciliationCockpit } from './views/ReconciliationCockpit';
import { PlaylistInspector } from './views/PlaylistInspector';
import { InvariantMonitor } from './views/InvariantMonitor';
import { ReportArchive } from './views/ReportArchive';
import { VirtualDiffList, type DiffRowModel } from './components/DualColumnDiffRow';
import { ArbitrationCard } from './components/ArbitrationCard';
import { BrandMark } from './components/icons';
import './styles/theme.css';

type ViewId = 'auth' | 'reconcile' | 'inspect' | 'verify' | 'reports';

/** Compact invariant flags derived from the SSE snapshot / session summary. */
function toInvariantFlags(snap: InvariantSnapshot | null, session: SessionResponse | null): InvariantFlags | null {
  if (snap) {
    return {
      countConserved: snap.is_count_conserved,
      uniquenessValid: !snap.has_duplicate_target_ids,
      orderMonotonic: (snap.disordered_indices?.length ?? 0) === 0,
      diffComplete: snap.is_diff_complete,
      zeroTraceClean: snap.is_zero_trace_clean,
    };
  }
  if (session) {
    const inv = session.invariants_summary;
    return {
      countConserved: inv.count_conserved,
      uniquenessValid: inv.uniqueness_valid,
      orderMonotonic: inv.order_lis_monotonic,
      diffComplete: inv.diff_complete,
      zeroTraceClean: inv.zero_trace_clean,
    };
  }
  return null;
}

function buildDiffRows(diff: DiffResponse | null): readonly DiffRowModel[] {
  if (!diff) return [];
  const rows: DiffRowModel[] = [];
  for (const t of diff.added) {
    rows.push({
      index: t.index,
      sourceTitle: t.title,
      sourceArtist: t.artists.join(', '),
      sourceDurationMs: parseDurationMs(t.duration),
      category: 'ADDED',
      isSkipped: false,
    });
  }
  for (const t of diff.retained) {
    rows.push({
      index: t.index,
      sourceTitle: t.title,
      sourceArtist: t.artists.join(', '),
      sourceDurationMs: 0,
      category: 'RETAINED',
      isSkipped: false,
      selectedCandidateId: t.targetTrackId,
    });
  }
  for (const t of diff.skipped) {
    rows.push({
      index: t.index,
      sourceTitle: t.title,
      sourceArtist: t.artists.join(', '),
      sourceDurationMs: 0,
      category: 'RETAINED',
      isSkipped: true,
    });
  }
  // Removed tracks carry no original index in the diff contract
  // (diff.removed = YTMTrack[]); enumerate them sequentially so the row shows
  // 1..n instead of a static 0, and show the YTM videoId as the target ref.
  diff.removed.forEach((t, i) => {
    rows.push({
      index: i,
      sourceTitle: t.title,
      sourceArtist: t.artists.join(', '),
      sourceDurationMs: 0,
      category: 'REMOVED',
      isSkipped: false,
      selectedCandidateId: t.videoId,
    });
  });
  return rows;
}

function parseDurationMs(duration: string | undefined): number {
  if (!duration) return 0;
  const m = /^(\d+):(\d{1,2})$/.exec(duration);
  if (m) return (Number(m[1]) * 60 + Number(m[2])) * 1000;
  const iso = /^PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:\.\d+)?)S)?$/.exec(duration);
  if (iso) {
    return ((Number(iso[1] ?? 0) * 3600 + Number(iso[2] ?? 0) * 60 + Number(iso[3] ?? 0)) ) * 1000;
  }
  return 0;
}

export default function App() {
  const [state, dispatch] = useReducer(cockpitReducer, INITIAL_STATE);
  const [view, setView] = useState<ViewId>('auth');
  const [session, setSession] = useState<SessionResponse | null>(null);
  const [authStatus, setAuthStatus] = useState<AuthStatusResponse | null>(null);
  const [diff, setDiff] = useState<DiffResponse | null>(null);
  const [invariantSnapshot, setInvariantSnapshot] = useState<InvariantSnapshot | null>(null);
  const [reports, setReports] = useState<ReportMeta[]>([]);
  const [inspectData, setInspectData] = useState<PlaylistInspectResponse | null>(null);

  const [terminalOpen, setTerminalOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const [focusIndex, setFocusIndex] = useState(0);
  const [expandedIndex, setExpandedIndex] = useState<number | null>(null);
  const [arbitration, setArbitration] = useState<ArbitrationRequestEvent | null>(null);
  const [lines, setLines] = useState<TerminalLine[]>([]);
  const [busy, setBusy] = useState<'scanning' | 'applying' | null>(null);
  const [errorBanner, setErrorBanner] = useState<string | null>(null);

  const lineIdRef = useRef(0);
  const sseRef = useRef<SSEConnection | null>(null);

  const client = useMemo<ApiClient | null>(() => {
    const token = readTokenFromURL();
    if (!token) return null;
    stripTokenFromURL();
    return new ApiClient({ baseURL: window.location.origin, token });
  }, []);

  const rows = useMemo(() => buildDiffRows(diff), [diff]);
  const invariants = useMemo(
    () => toInvariantFlags(invariantSnapshot, session),
    [invariantSnapshot, session],
  );

  // Mirror of the reducer state for use inside stable callbacks (SSE listener,
  // UI handlers) without re-creating them on every state change. The SSE
  // listener must refuse illegal dispatches against the CURRENT state rather
  // than crash on IllegalTransitionError (review BLOCKER-1).
  const stateRef = useRef(state);
  useEffect(() => {
    stateRef.current = state;
  }, [state]);

  /** Dispatches only if the event is legal from the current FSM state. */
  const safeDispatch = useCallback((action: FSMEvent): boolean => {
    if (canTransition(stateRef.current, action)) {
      dispatch(action);
      return true;
    }
    return false;
  }, []);

  /* ---- Boot: fetch session + auth + reports, open SSE ---- */
  useEffect(() => {
    if (!client) return undefined;

    const boot = async (): Promise<void> => {
      try {
        const s = await client.getSession();
        setSession(s);
      } catch (e) {
        setErrorBanner(e instanceof Error ? e.message : String(e));
        safeDispatch({ type: 'FATAL', code: 'BOOT_SESSION', message: 'Failed to load session' });
      }
      try {
        const a = await client.getAuthStatus();
        setAuthStatus(a);
        void syncMissing(a);
      } catch {
        // keep AUTH_REQUIRED via missing detection below
      }
      try {
        setReports(await client.listReports());
      } catch {
        // reports view shows empty
      }
    };

    const sse = connectSSE({
      client,
      onEvent: (ev) => {
        switch (ev.type) {
          case 'DIFF_PROGRESS': {
            const d = ev.data;
            safeDispatch({ type: 'DIFF_PROGRESS', progress: progressPct(d.processed, d.total), currentTrack: d.current_track ?? `processed ${d.processed ?? 0}` });
            break;
          }
          case 'DIFF_COMPLETE':
            safeDispatch({ type: 'SCAN_COMPLETE', allPassed: false, violations: [] });
            setBusy(null);
            void refreshDiff();
            break;
          case 'RECONCILE_FAILED': {
            safeDispatch({ type: 'FATAL', code: 'RECONCILE_FAILED', message: ev.data.error ?? 'reconcile failed' });
            setBusy(null);
            break;
          }
          case 'ARBITRATION_REQUIRED': {
            const d = ev.data;
            setArbitration(d);
            safeDispatch({ type: 'ARBITRATION_FOUND', activeTrackId: d.track_id, candidatesCount: d.candidates.length });
            break;
          }
          case 'INVARIANT_SNAPSHOT': {
            const snap = ev.data;
            // The snapshot always updates local state; the FSM only advances
            // SCANNING_DIFF -> INVARIANT_HEALTH_CHECK when the scan actually
            // completed. A snapshot broadcast during APPLYING_MUTATIONS or
            // COMPLETED_SUCCESS (reconcile/apply preflight, reconcile.go) must
            // NOT force an illegal transition — drop the dispatch instead
            // (review BLOCKER-1).
            setInvariantSnapshot(snap);
            if (stateRef.current.status === 'SCANNING_DIFF') {
              safeDispatch({ type: 'SCAN_COMPLETE', allPassed: snap.all_passed, violations: snap.all_passed ? [] : invariantViolations(snap) });
            }
            break;
          }
          case 'LOG_STREAM': {
            const log = ev.data;
            setLines((prev) => [...prev.slice(-399), logToLine(++lineIdRef.current, log)]);
            break;
          }
          case 'GAP_FALLBACK':
            void refreshAll();
            break;
          case 'SYSTEM_SHUTDOWN':
            setTerminalOpen(true);
            setLines((prev) => [...prev.slice(-399), { id: ++lineIdRef.current, ts: '', module: 'system', level: 'warn', text: 'SYSTEM_SHUTDOWN — server is shutting down' }]);
            break;
          case 'RATE_LIMITED':
            setLines((prev) => [...prev.slice(-399), { id: ++lineIdRef.current, ts: '', module: 'rate-limit', level: 'warn', text: ev.data.message ?? 'rate limited' }]);
            break;
          default:
            break;
        }
      },
    });
    sseRef.current = sse;
    void boot();

    return () => {
      sse.close();
      sseRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client]);

  useHeartbeat(client, client !== null);

  const refreshDiff = useCallback(async (): Promise<void> => {
    if (!client) return;
    try {
      setDiff(await client.getDiff());
    } catch (e) {
      setErrorBanner(e instanceof Error ? e.message : String(e));
    }
  }, [client]);

  const refreshAll = useCallback(async (): Promise<void> => {
    if (!client) return;
    await Promise.allSettled([
      client.getSession().then(setSession),
      client.getAuthStatus().then(setAuthStatus),
      client.getDiff().then(setDiff),
      client.listReports().then(setReports),
    ]);
  }, [client]);

  /* ---- FSM helpers ---- */

  const syncMissing = async (a: AuthStatusResponse): Promise<void> => {
    const missing: ('spotify' | 'ytmusic')[] = [];
    if (!a.spotify.authenticated) missing.push('spotify');
    if (!a.youtubeMusic.authenticated) missing.push('ytmusic');
    safeDispatch({ type: 'AUTH_CHECKED', missing });
  };

  const handleStart = async (source: string, target: string, cleanExtra: boolean, syncOrder: boolean, concurrency: number): Promise<void> => {
    if (!client) return;
    const started = safeDispatch({ type: 'START_SCAN', source, target });
    if (!started) {
      setErrorBanner('Cannot start: a reconcile or apply is already in progress.');
      return;
    }
    setBusy('scanning');
    try {
      await client.startReconcile({ source_playlist_id: source, target_playlist_id: target, clean_extra: cleanExtra, sync_order: syncOrder, concurrency });
    } catch (e) {
      setBusy(null);
      safeDispatch({ type: 'FATAL', code: 'START_RECONCILE', message: e instanceof Error ? e.message : String(e) });
    }
  };

  const handleApply = async (forceOverride: boolean): Promise<void> => {
    if (!client) return;
    const totalToWrite = (diff?.added.length ?? 0) + (diff?.removed.length ?? 0);
    const started = safeDispatch({ type: 'INVARIANTS_PASSED', totalToWrite });
    if (!started) {
      setErrorBanner('Apply unavailable in the current state (invariants must pass first).');
      return;
    }
    setBusy('applying');
    try {
      const res = await client.applyMutations(forceOverride);
      if (res.applied) {
        safeDispatch({ type: 'APPLY_COMPLETE', reportId: res.output?.playlistId ?? 'report', summaryText: res.output?.title ?? 'Done' });
        void refreshAll();
      } else {
        setErrorBanner('Apply blocked: invariant preflight failed.');
        safeDispatch({ type: 'FATAL', code: 'APPLY_BLOCKED', message: 'Invariant preflight failed' });
      }
    } catch (e) {
      setBusy(null);
      const msg = e instanceof Error ? e.message : String(e);
      if (msg.includes('409')) {
        setErrorBanner('Invariant conflict — review the guardian bar.');
        safeDispatch({ type: 'FATAL', code: 'INVARIANT_CONFLICT', message: msg });
      } else {
        safeDispatch({ type: 'FATAL', code: 'APPLY', message: msg });
      }
    }
  };

  const handleVerify = async (targetId: string): Promise<void> => {
    if (!client) return;
    const snap = await client.verifyInvariants(targetId);
    setInvariantSnapshot(snap);
    // A manual verify may be requested outside SCANNING_DIFF (e.g. from
    // CONFIGURING / COMPLETED_SUCCESS); only advance the FSM when legal, and
    // never crash the reducer on an illegal SCAN_COMPLETE.
    safeDispatch({ type: 'SCAN_COMPLETE', allPassed: snap.all_passed, violations: snap.all_passed ? [] : invariantViolations(snap) });
  };

  const handleInspect = async (id: string, platform: 'spotify' | 'ytmusic'): Promise<void> => {
    if (!client) return;
    setInspectData(await client.inspectPlaylist(id, platform));
  };

  const selectCandidate = (trackId: string, candidate: CandidateOption): void => {
    void (async () => {
      if (!client) return;
      await client.submitDecision({ track_id: trackId, action: 'SELECT_CANDIDATE', selected_target_id: candidate.target_id });
      setArbitration(null);
      safeDispatch({ type: 'ARBITRATION_RESOLVED', progress: 0, currentTrack: 'Continuing…' });
    })();
  };

  const skipTrack = (trackId: string): void => {
    void (async () => {
      if (!client) return;
      await client.submitDecision({ track_id: trackId, action: 'SKIP' });
      setArbitration(null);
      safeDispatch({ type: 'ARBITRATION_RESOLVED', progress: 0, currentTrack: 'Skipped, continuing…' });
    })();
  };

  const customId = (trackId: string, customTargetId: string): void => {
    void (async () => {
      if (!client) return;
      await client.submitDecision({ track_id: trackId, action: 'CUSTOM_ID', selected_target_id: customTargetId });
      setArbitration(null);
      safeDispatch({ type: 'ARBITRATION_RESOLVED', progress: 0, currentTrack: 'Custom target set, continuing…' });
    })();
  };

  /* ---- Keyboard handlers ---- */
  const kb = useMemo(
    () => ({
      moveFocus: (delta: number) => {
        setFocusIndex((prev) => {
          const next = prev + delta;
          return next < 0 ? 0 : next >= rows.length ? rows.length - 1 : next;
        });
      },
      selectCandidate: (idx: number) => {
        const cand = arbitration && idx >= 1 && idx <= arbitration.candidates.length ? arbitration.candidates[idx - 1] : undefined;
        if (arbitration && cand) {
          selectCandidate(arbitration.track_id, cand);
        }
      },
      skip: () => {
        if (arbitration) skipTrack(arbitration.track_id);
      },
      openCustom: () => {
        if (arbitration) setCustomRequested((v) => v + 1);
      },
      apply: () => {
        // INVARIANTS_PASSED is legal only from INVARIANT_HEALTH_CHECK
        // (spec 04 §2 #7); the CONFIGURING branch would throw.
        if (state.status === 'INVARIANT_HEALTH_CHECK') {
          void handleApply(false);
        }
      },
      openArbitration: () => {
        const row = rows[focusIndex];
        if (row && row.category === 'ARBITRATION' ) {
          // No arbitration data on a static row; expand to show detail.
          setExpandedIndex((prev) => (prev === focusIndex ? null : focusIndex));
        }
      },
      onEscape: () => {
        setHelpOpen(false);
        setExpandedIndex(null);
      },
      toggleTerminal: () => setTerminalOpen((v) => !v),
      toggleHelp: () => setHelpOpen((v) => !v),
    }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [arbitration, rows, focusIndex, state.status],
  );

  useKeyboardShortcuts(kb);

  /* ---- Custom-ID keyboard trigger: opens the input via a signal ---- */
  const [customRequested, setCustomRequested] = useState(0);
  // Retained for the reconciliation flow: when arbitration is present we focus
  // its custom input. We expose the signal to ArbitrationCard through a ref
  // keyed render. (Simple approach: click the button via DOM.)

  useEffect(() => {
    if (customRequested > 0 && arbitration) {
      const btn = document.querySelector<HTMLButtonElement>('.arbitration-actions .btn-ghost:nth-of-type(2)');
      btn?.click();
    }
  }, [customRequested, arbitration]);

  /* ---- status banner ---- */
  const statusMeta = useMemo(() => {
    switch (state.status) {
      case 'SCANNING_DIFF':
        return `Scanning… ${state.progress}% — ${state.currentTrack}`;
      case 'AWAITING_ARBITRATION':
        return `Awaiting arbitration — ${state.candidatesCount} candidates`;
      case 'INVARIANT_HEALTH_CHECK':
        return state.allPassed ? 'Invariants PASSED — ready to apply' : `Invariants FAILED: ${state.violations.join('; ')}`;
      case 'APPLYING_MUTATIONS':
        return `Applying mutations… ${state.writtenCount}/${state.totalToWrite}`;
      case 'COMPLETED_SUCCESS':
        return `Completed — ${state.summaryText} (${state.reportId})`;
      case 'CRITICAL_ERROR':
        return `ERROR ${state.errorCode}: ${state.errorMessage}`;
      case 'AUTH_REQUIRED':
        return `Auth required: ${state.missing.join(', ')}`;
      case 'CONFIGURING':
        return 'Ready — configure a reconcile';
      default:
        return state.status;
    }
  }, [state]);

  const navItems: readonly { id: ViewId; label: string }[] = [
    { id: 'auth', label: 'Auth Vault' },
    { id: 'reconcile', label: 'Reconcile' },
    { id: 'inspect', label: 'Inspector' },
    { id: 'verify', label: 'Invariants' },
    { id: 'reports', label: 'Reports' },
  ];

  if (!client) {
    return (
      <div className="app-shell">
        <div className="view-container" style={{ maxWidth: 640, margin: '0 auto' }}>
          <div className="view-panel">
            <h2>Missing session token</h2>
            <p>
              This cockpit is only reachable through the <code>playlistsync web</code> banner URL
              which carries <code>?token=…</code>. Open the terminal URL to continue.
            </p>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="app-shell">
      <nav className="top-nav" aria-label="Cockpit navigation">
        <div className="nav-brand">
          <BrandMark size={20} /> playlistsync cockpit
        </div>
        {navItems.map((n) => (
          <button key={n.id} type="button" className="nav-item" aria-current={view === n.id ? 'page' : undefined} onClick={() => setView(n.id)}>
            <span className="nav-dot" data-state={n.id === 'reconcile' && diff ? 'alive' : undefined} />
            {n.label}
          </button>
        ))}
        <div className="nav-session ps-tabular">
          <span>session {session?.session_id ?? '…'}</span>
          <span className="token-hint">port {session?.port ?? '?'} · clients {session?.client_count ?? '-'}</span>
        </div>
      </nav>

      <div className="main-area">
        <div className="view-container">
          <div className="status-banner" data-status={state.status}>
            <span className="dot" aria-hidden="true" />
            <code>{state.status}</code>
            <span>{statusMeta}</span>
          </div>
          {errorBanner && (
            <div className="vault-error" role="alert">
              {errorBanner}
            </div>
          )}

          {view === 'auth' && (
            <AuthVault client={client} status={authStatus} onRefresh={() => void refreshAll()} />
          )}
          {view === 'reconcile' && (
            <>
              <ReconciliationCockpit
                client={client}
                diff={diff}
                onStart={handleStart}
                onApply={(force) => handleApply(force)}
                busy={busy}
                canApply={state.status === 'INVARIANT_HEALTH_CHECK' && state.allPassed}
              />
              {arbitration && (
                <ArbitrationCard
                  request={arbitration}
                  onSelectCandidate={(trackId, cand) => selectCandidate(trackId, cand)}
                  onSkip={skipTrack}
                  onCustomId={customId}
                />
              )}
              {rows.length > 0 && (
                <div className="view-panel">
                  <div className="panel-header">
                    <h3>Diff rows — {rows.length}</h3>
                    <span className="reconcile-status ps-tabular">windowed 44px rows</span>
                  </div>
                  <VirtualDiffList
                    rows={rows}
                    focusedIndex={focusIndex}
                    expandedIndex={expandedIndex}
                    onToggleExpand={(i) => setExpandedIndex((prev) => (prev === i ? null : i))}
                    viewportHeight={Math.min(560, Math.max(240, window.innerHeight - 380))}
                  />
                </div>
              )}
            </>
          )}
          {view === 'inspect' && (
            <PlaylistInspector client={client} data={inspectData} onInspect={handleInspect} />
          )}
          {view === 'verify' && (
            <InvariantMonitor client={client} snapshot={invariantSnapshot} verifyFor={diff?.added[0]?.id ?? ''} onVerify={handleVerify} />
          )}
          {view === 'reports' && (
            <ReportArchive client={client} reports={reports} onReload={async () => setReports(await client.listReports())} />
          )}

          <TerminalDock isOpen={terminalOpen} lines={lines} onToggle={() => setTerminalOpen((v) => !v)} onClear={() => setLines([])} />
        </div>

        <InvariantGuardianBar
          invariants={invariants}
          onApply={() => void handleApply(false)}
          isApplying={busy === 'applying'}
          isApplyEnabled={state.status === 'INVARIANT_HEALTH_CHECK' && state.allPassed}
        />
      </div>

      {helpOpen && (
        <div className="help-overlay" onClick={() => setHelpOpen(false)}>
          <div className="help-card" role="dialog" aria-modal="true" aria-label="Keyboard shortcuts" onClick={(e) => e.stopPropagation()}>
            <h3>Keyboard shortcuts</h3>
            <div className="help-grid">
              <div><kbd>j</kbd><span>move focus down</span></div>
              <div><kbd>k</kbd><span>move focus up</span></div>
              <div><kbd>1-9</kbd><span>select candidate</span></div>
              <div><kbd>s</kbd><span>skip track</span></div>
              <div><kbd>c</kbd><span>custom target ID</span></div>
              <div><kbd>a</kbd><span>apply diff</span></div>
              <div><kbd>Enter</kbd><span>open arbitration / confirm</span></div>
              <div><kbd>t</kbd><span>toggle terminal</span></div>
              <div><kbd>?</kbd><span>this cheat sheet</span></div>
              <div><kbd>Esc</kbd><span>close dialogs</span></div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function progressPct(processed: number | undefined, total: number | undefined): number {
  if (!total || total <= 0) return 0;
  return Math.min(100, Math.round((processed ?? 0) / total * 100));
}

function invariantViolations(snap: InvariantSnapshot): string[] {
  const v: string[] = [];
  if (!snap.is_count_conserved) v.push('count');
  if (snap.has_duplicate_target_ids) v.push('uniqueness');
  if (snap.disordered_indices && snap.disordered_indices.length > 0) v.push('order');
  if (!snap.is_diff_complete) v.push('diff');
  if (!snap.is_zero_trace_clean) v.push('zero-trace');
  return v;
}