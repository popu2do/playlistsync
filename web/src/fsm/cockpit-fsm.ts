/**
 * Web cockpit FSM — spec 04 §2 可辨识联合 (Discriminated Union), 9 states.
 *
 * The task brief mandates EXACTLY this state set and transition table:
 *
 *   1. IDLE ──(未授权侦测)──► AUTH_REQUIRED
 *   2. IDLE / AUTH_REQUIRED ──(就绪)──► CONFIGURING
 *   3. CONFIGURING ──(开始调解)──► SCANNING_DIFF
 *   4. SCANNING_DIFF ──(发现歧义候选)──► AWAITING_ARBITRATION
 *   5. AWAITING_ARBITRATION ──(用户选定/跳过)──► SCANNING_DIFF
 *   6. SCANNING_DIFF ──(全部扫描完毕)──► INVARIANT_HEALTH_CHECK
 *   7. INVARIANT_HEALTH_CHECK ──(验证通过+提交)──► APPLYING_MUTATIONS
 *   8. APPLYING_MUTATIONS ──(原子写入完成)──► COMPLETED_SUCCESS
 *   9. 任何状态 ──(不可逆异常)──► CRITICAL_ERROR
 *
 * Illegal transitions throw (a reducer must never silently keep a stale
 * state — an illegal transition is a programming error, not a no-op).
 */

/** Platform identifiers for the auth vault. */
export type PlatformId = 'spotify' | 'ytmusic';

/** WebSessionState — the discriminated union (spec 04 §2 verbatim names). */
export type WebSessionState =
  | { readonly status: 'IDLE' }
  | { readonly status: 'AUTH_REQUIRED'; readonly missing: readonly PlatformId[] }
  | { readonly status: 'CONFIGURING'; readonly source?: string; readonly target?: string }
  | {
      readonly status: 'SCANNING_DIFF';
      readonly progress: number;
      readonly currentTrack: string;
      /** Source/target carried through the scan so post-scan verification
       * (DIFF_COMPLETE → GET /verify/invariants) knows the target playlist. */
      readonly source?: string;
      readonly target?: string;
    }
  | {
      readonly status: 'AWAITING_ARBITRATION';
      readonly activeTrackId: string;
      readonly candidatesCount: number;
      readonly source?: string;
      readonly target?: string;
    }
  | {
      readonly status: 'INVARIANT_HEALTH_CHECK';
      readonly allPassed: boolean;
      readonly violations: readonly string[];
    }
  | { readonly status: 'APPLYING_MUTATIONS'; readonly writtenCount: number; readonly totalToWrite: number }
  | { readonly status: 'COMPLETED_SUCCESS'; readonly reportId: string; readonly summaryText: string }
  | { readonly status: 'CRITICAL_ERROR'; readonly errorCode: string; readonly errorMessage: string };

/** FSM actions (events) that drive state transitions. */
export type FSMEvent =
  | { readonly type: 'AUTH_CHECKED'; readonly missing: readonly PlatformId[] }
  | { readonly type: 'READY'; readonly source?: string; readonly target?: string }
  | { readonly type: 'START_SCAN'; readonly source?: string; readonly target?: string }
  | { readonly type: 'DIFF_PROGRESS'; readonly progress: number; readonly currentTrack: string }
  | { readonly type: 'ARBITRATION_FOUND'; readonly activeTrackId: string; readonly candidatesCount: number }
  | { readonly type: 'ARBITRATION_RESOLVED'; readonly progress: number; readonly currentTrack: string }
  | { readonly type: 'SCAN_COMPLETE'; readonly allPassed: boolean; readonly violations: readonly string[] }
  | {
      readonly type: 'SNAPSHOT_REFRESH';
      readonly allPassed: boolean;
      readonly violations: readonly string[];
    }
  | { readonly type: 'INVARIANTS_PASSED'; readonly totalToWrite: number }
  | { readonly type: 'APPLY_PROGRESS'; readonly writtenCount: number; readonly totalToWrite: number }
  | { readonly type: 'APPLY_COMPLETE'; readonly reportId: string; readonly summaryText: string }
  | { readonly type: 'FATAL'; readonly code: string; readonly message: string }
  | { readonly type: 'RESET' };

/** Result of a transition: the new state on success, a thrown IllegalTransitionError on failure. */
export class IllegalTransitionError extends Error {
  constructor(
    readonly from: WebSessionState['status'],
    readonly event: FSMEvent['type'],
  ) {
    super(`Illegal FSM transition: ${from} --(${event})--> ?`);
    this.name = 'IllegalTransitionError';
  }
}

/** Validates and performs a state transition. Throws on illegal transitions. */
export function transitionFSM(state: WebSessionState, event: FSMEvent): WebSessionState {
  // Any state -> CRITICAL_ERROR is always legal (spec 04 §2 #9).
  if (event.type === 'FATAL') {
    return { status: 'CRITICAL_ERROR', errorCode: event.code, errorMessage: event.message };
  }

  switch (state.status) {
    case 'IDLE':
      switch (event.type) {
        case 'AUTH_CHECKED':
          return event.missing.length > 0
            ? { status: 'AUTH_REQUIRED', missing: [...event.missing] }
            : { status: 'CONFIGURING' };
        case 'READY':
          return { status: 'CONFIGURING', source: event.source, target: event.target };
        case 'RESET':
          return { status: 'IDLE' };
        default:
          throw new IllegalTransitionError(state.status, event.type);
      }

    case 'AUTH_REQUIRED':
      switch (event.type) {
        case 'AUTH_CHECKED':
          return event.missing.length > 0
            ? { status: 'AUTH_REQUIRED', missing: [...event.missing] }
            : { status: 'CONFIGURING' };
        case 'READY':
          return { status: 'CONFIGURING', source: event.source, target: event.target };
        default:
          throw new IllegalTransitionError(state.status, event.type);
      }

    case 'CONFIGURING':
      switch (event.type) {
        case 'START_SCAN':
          return {
            status: 'SCANNING_DIFF',
            progress: 0,
            currentTrack: 'Starting…',
            source: event.source,
            target: event.target,
          };
        default:
          throw new IllegalTransitionError(state.status, event.type);
      }

    case 'SCANNING_DIFF':
      switch (event.type) {
        case 'DIFF_PROGRESS':
          return {
            status: 'SCANNING_DIFF',
            progress: event.progress,
            currentTrack: event.currentTrack,
            source: state.source,
            target: state.target,
          };
        case 'ARBITRATION_FOUND':
          return {
            status: 'AWAITING_ARBITRATION',
            activeTrackId: event.activeTrackId,
            candidatesCount: event.candidatesCount,
            source: state.source,
            target: state.target,
          };
        case 'SCAN_COMPLETE':
          return {
            status: 'INVARIANT_HEALTH_CHECK',
            allPassed: event.allPassed,
            violations: [...event.violations],
          };
        default:
          throw new IllegalTransitionError(state.status, event.type);
      }

    case 'AWAITING_ARBITRATION':
      switch (event.type) {
        case 'ARBITRATION_RESOLVED':
          return {
            status: 'SCANNING_DIFF',
            progress: event.progress,
            currentTrack: event.currentTrack,
            source: state.source,
            target: state.target,
          };
        default:
          throw new IllegalTransitionError(state.status, event.type);
      }

    case 'INVARIANT_HEALTH_CHECK':
      switch (event.type) {
        case 'SNAPSHOT_REFRESH':
          return {
            status: 'INVARIANT_HEALTH_CHECK',
            allPassed: event.allPassed,
            violations: [...event.violations],
          };
        case 'INVARIANTS_PASSED':
          return { status: 'APPLYING_MUTATIONS', writtenCount: 0, totalToWrite: event.totalToWrite };
        default:
          throw new IllegalTransitionError(state.status, event.type);
      }

    case 'APPLYING_MUTATIONS':
      switch (event.type) {
        case 'APPLY_PROGRESS':
          return { status: 'APPLYING_MUTATIONS', writtenCount: event.writtenCount, totalToWrite: event.totalToWrite };
        case 'APPLY_COMPLETE':
          return { status: 'COMPLETED_SUCCESS', reportId: event.reportId, summaryText: event.summaryText };
        default:
          throw new IllegalTransitionError(state.status, event.type);
      }

    case 'COMPLETED_SUCCESS':
      switch (event.type) {
        case 'RESET':
          return { status: 'IDLE' };
        default:
          throw new IllegalTransitionError(state.status, event.type);
      }

    case 'CRITICAL_ERROR':
      switch (event.type) {
        case 'RESET':
          return { status: 'IDLE' };
        default:
          throw new IllegalTransitionError(state.status, event.type);
      }
  }
}

/** Reducer wrapper with the same contract (dispatch shape for useReducer). */
export function cockpitReducer(
  state: WebSessionState,
  action: FSMEvent,
): WebSessionState {
  return transitionFSM(state, action);
}

/** Pure legality predicate — non-throwing complement of transitionFSM.
 *
 * Dispatch callers (SSE listener, UI handlers) use this to refuse an event
 * that is illegal from the CURRENT state instead of crashing the reducer.
 * The transition table in transitionFSM remains the single source of truth.
 */
export function canTransition(state: WebSessionState, event: FSMEvent): boolean {
  try {
    transitionFSM(state, event);
    return true;
  } catch {
    return false;
  }
}

/** Initial cockpit state. */
export const INITIAL_STATE: WebSessionState = { status: 'IDLE' };

/** All statuses for validation tables. */
export const ALL_STATUSES: readonly WebSessionState['status'][] = [
  'IDLE',
  'AUTH_REQUIRED',
  'CONFIGURING',
  'SCANNING_DIFF',
  'AWAITING_ARBITRATION',
  'INVARIANT_HEALTH_CHECK',
  'APPLYING_MUTATIONS',
  'COMPLETED_SUCCESS',
  'CRITICAL_ERROR',
];

/** Simulated re-scan progress for display while the backend streams. */
export function clampProgress(value: number, max = 100): number {
  return Math.max(0, Math.min(max, value));
}