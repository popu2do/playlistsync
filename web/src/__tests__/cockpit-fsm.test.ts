/**
 * FSM test suite — spec 04 §2 全部合法/非法转移 (table-driven).
 */
import { describe, expect, it } from 'vitest';
import {
  ALL_STATUSES,
  IllegalTransitionError,
  INITIAL_STATE,
  cockpitReducer,
  transitionFSM,
  type FSMEvent,
  type WebSessionState,
} from '../fsm/cockpit-fsm';

function state(status: WebSessionState['status']): WebSessionState {
  switch (status) {
    case 'IDLE':
      return { status: 'IDLE' };
    case 'AUTH_REQUIRED':
      return { status: 'AUTH_REQUIRED', missing: ['spotify'] };
    case 'CONFIGURING':
      return { status: 'CONFIGURING', source: 'src', target: 'dst' };
    case 'SCANNING_DIFF':
      return { status: 'SCANNING_DIFF', progress: 10, currentTrack: 'Track' };
    case 'AWAITING_ARBITRATION':
      return { status: 'AWAITING_ARBITRATION', activeTrackId: 't1', candidatesCount: 3 };
    case 'INVARIANT_HEALTH_CHECK':
      return { status: 'INVARIANT_HEALTH_CHECK', allPassed: true, violations: [] };
    case 'APPLYING_MUTATIONS':
      return { status: 'APPLYING_MUTATIONS', writtenCount: 1, totalToWrite: 5 };
    case 'COMPLETED_SUCCESS':
      return { status: 'COMPLETED_SUCCESS', reportId: 'r1', summaryText: 'ok' };
    case 'CRITICAL_ERROR':
      return { status: 'CRITICAL_ERROR', errorCode: 'E1', errorMessage: 'boom' };
  }
}

describe('FSM legal transitions (spec 04 §2 table)', () => {
  const legal: readonly [WebSessionState['status'], FSMEvent, WebSessionState['status']][] = [
    // 1. IDLE ──(未授权)──► AUTH_REQUIRED
    ['IDLE', { type: 'AUTH_CHECKED', missing: ['spotify'] }, 'AUTH_REQUIRED'],
    ['IDLE', { type: 'AUTH_CHECKED', missing: ['ytmusic'] }, 'AUTH_REQUIRED'],
    // 2. IDLE / AUTH_REQUIRED ──(就绪)──► CONFIGURING
    ['IDLE', { type: 'AUTH_CHECKED', missing: [] }, 'CONFIGURING'],
    ['IDLE', { type: 'READY' }, 'CONFIGURING'],
    ['AUTH_REQUIRED', { type: 'AUTH_CHECKED', missing: [] }, 'CONFIGURING'],
    ['AUTH_REQUIRED', { type: 'READY' }, 'CONFIGURING'],
    // 3. CONFIGURING ──(开始调解)──► SCANNING_DIFF
    ['CONFIGURING', { type: 'START_SCAN' }, 'SCANNING_DIFF'],
    // 4. SCANNING_DIFF ──(发现歧义)──► AWAITING_ARBITRATION
    ['SCANNING_DIFF', { type: 'ARBITRATION_FOUND', activeTrackId: 't9', candidatesCount: 2 }, 'AWAITING_ARBITRATION'],
    // 5. AWAITING_ARBITRATION ──(选定/跳过)──► SCANNING_DIFF
    ['AWAITING_ARBITRATION', { type: 'ARBITRATION_RESOLVED', progress: 12, currentTrack: 'Next' }, 'SCANNING_DIFF'],
    // 6. SCANNING_DIFF ──(扫描完)──► INVARIANT_HEALTH_CHECK
    ['SCANNING_DIFF', { type: 'SCAN_COMPLETE', allPassed: true, violations: [] }, 'INVARIANT_HEALTH_CHECK'],
    // 7. INVARIANT_HEALTH_CHECK ──(通过+提交)──► APPLYING_MUTATIONS
    ['INVARIANT_HEALTH_CHECK', { type: 'INVARIANTS_PASSED', totalToWrite: 12 }, 'APPLYING_MUTATIONS'],
    // 8. APPLYING_MUTATIONS ──(完成)──► COMPLETED_SUCCESS
    ['APPLYING_MUTATIONS', { type: 'APPLY_COMPLETE', reportId: 'r9', summaryText: 'done' }, 'COMPLETED_SUCCESS'],
    // 9. 任何状态 ──(异常)──► CRITICAL_ERROR
    ['IDLE', { type: 'FATAL', code: 'X', message: 'y' }, 'CRITICAL_ERROR'],
    ['CONFIGURING', { type: 'FATAL', code: 'X', message: 'y' }, 'CRITICAL_ERROR'],
    ['APPLYING_MUTATIONS', { type: 'FATAL', code: 'X', message: 'y' }, 'CRITICAL_ERROR'],
    ['COMPLETED_SUCCESS', { type: 'FATAL', code: 'X', message: 'y' }, 'CRITICAL_ERROR'],
  ];

  for (const [from, event, to] of legal) {
    it(`${from} --${event.type}--> ${to}`, () => {
      const next = transitionFSM(state(from), event);
      expect(next.status).toBe(to);
    });
  }

  it('progressive happy-path journey', () => {
    let s: WebSessionState = INITIAL_STATE;
    s = transitionFSM(s, { type: 'AUTH_CHECKED', missing: ['spotify', 'ytmusic'] });
    expect(s.status).toBe('AUTH_REQUIRED');
    s = transitionFSM(s, { type: 'AUTH_CHECKED', missing: [] });
    expect(s.status).toBe('CONFIGURING');
    s = transitionFSM(s, { type: 'START_SCAN' });
    expect(s.status).toBe('SCANNING_DIFF');
    s = transitionFSM(s, { type: 'ARBITRATION_FOUND', activeTrackId: 't1', candidatesCount: 2 });
    expect(s.status).toBe('AWAITING_ARBITRATION');
    s = transitionFSM(s, { type: 'ARBITRATION_RESOLVED', progress: 30, currentTrack: 'go' });
    expect(s.status).toBe('SCANNING_DIFF');
    s = transitionFSM(s, { type: 'SCAN_COMPLETE', allPassed: true, violations: [] });
    expect(s.status).toBe('INVARIANT_HEALTH_CHECK');
    s = transitionFSM(s, { type: 'INVARIANTS_PASSED', totalToWrite: 4 });
    expect(s.status).toBe('APPLYING_MUTATIONS');
    s = transitionFSM(s, { type: 'APPLY_COMPLETE', reportId: 'R1', summaryText: 'ok' });
    expect(s.status).toBe('COMPLETED_SUCCESS');
  });

  it('DIFF_PROGRESS stays in SCANNING_DIFF with updated payload', () => {
    const next = transitionFSM(state('SCANNING_DIFF'), { type: 'DIFF_PROGRESS', progress: 42, currentTrack: 'T42' });
    expect(next).toEqual({ status: 'SCANNING_DIFF', progress: 42, currentTrack: 'T42' });
  });

  it('APPLY_PROGRESS stays in APPLYING_MUTATIONS with updated payload', () => {
    const next = transitionFSM(state('APPLYING_MUTATIONS'), { type: 'APPLY_PROGRESS', writtenCount: 3, totalToWrite: 5 });
    expect(next.status).toBe('APPLYING_MUTATIONS');
    if (next.status === 'APPLYING_MUTATIONS') {
      expect(next.writtenCount).toBe(3);
    }
  });

  it('SNAPSHOT_REFRESH re-evaluates allPassed in INVARIANT_HEALTH_CHECK (Apply unlock)', () => {
    const next = transitionFSM(state('INVARIANT_HEALTH_CHECK'), { type: 'SNAPSHOT_REFRESH', allPassed: true, violations: [] });
    expect(next.status).toBe('INVARIANT_HEALTH_CHECK');
    if (next.status === 'INVARIANT_HEALTH_CHECK') {
      expect(next.allPassed).toBe(true);
      expect(next.violations).toEqual([]);
    }
  });

  it('START_SCAN carries source/target through SCANNING_DIFF for post-scan verification', () => {
    const src = transitionFSM({ status: 'CONFIGURING', source: 'sp', target: 'yt' }, { type: 'START_SCAN', source: 'sp', target: 'yt' });
    expect(src.status).toBe('SCANNING_DIFF');
    if (src.status === 'SCANNING_DIFF') {
      expect(src.source).toBe('sp');
      expect(src.target).toBe('yt');
    }
  });
});

describe('FSM illegal transitions throw', () => {
  // Build the illegal matrix as ALL_STATUSES minus the states where each event
  // IS legal (spec 04 §2 table). FATAL is legal from every state.
  function illegalFrom(
    event: FSMEvent,
    legalStates: readonly WebSessionState['status'][],
  ): [WebSessionState['status'], FSMEvent][] {
    const legal = new Set(legalStates);
    return ALL_STATUSES.filter((st) => !legal.has(st)).map(
      (st): [WebSessionState['status'], FSMEvent] => [st, event],
    );
  }

  const illegal: readonly [WebSessionState['status'], FSMEvent][] = [
    ...illegalFrom({ type: 'START_SCAN' }, ['CONFIGURING']),
    ...illegalFrom({ type: 'APPLY_COMPLETE', reportId: 'r', summaryText: 's' }, ['APPLYING_MUTATIONS']),
    ...illegalFrom({ type: 'SCAN_COMPLETE', allPassed: true, violations: [] }, ['SCANNING_DIFF']),
    ...illegalFrom({ type: 'ARBITRATION_FOUND', activeTrackId: 'a', candidatesCount: 1 }, ['SCANNING_DIFF']),
    ...illegalFrom({ type: 'DIFF_PROGRESS', progress: 1, currentTrack: 't' }, ['SCANNING_DIFF']),
    ...illegalFrom({ type: 'ARBITRATION_RESOLVED', progress: 1, currentTrack: 't' }, ['AWAITING_ARBITRATION']),
    ...illegalFrom({ type: 'INVARIANTS_PASSED', totalToWrite: 1 }, ['INVARIANT_HEALTH_CHECK']),
    ...illegalFrom({ type: 'SNAPSHOT_REFRESH', allPassed: true, violations: [] }, ['INVARIANT_HEALTH_CHECK']),
    ...illegalFrom({ type: 'APPLY_PROGRESS', writtenCount: 1, totalToWrite: 2 }, ['APPLYING_MUTATIONS']),
    ...illegalFrom({ type: 'AUTH_CHECKED', missing: [] }, ['IDLE', 'AUTH_REQUIRED']),
    ...illegalFrom({ type: 'READY' }, ['IDLE', 'AUTH_REQUIRED']),
    ...illegalFrom({ type: 'RESET' }, ['IDLE', 'COMPLETED_SUCCESS', 'CRITICAL_ERROR']),
  ];

  for (const [from, event] of illegal) {
    it(`${from} --${event.type}--> throws`, () => {
      expect(() => transitionFSM(state(from), event)).toThrow(IllegalTransitionError);
    });
  }

  it('throws IllegalTransitionError with from/event details', () => {
    try {
      transitionFSM(state('IDLE'), { type: 'APPLY_COMPLETE', reportId: 'r', summaryText: 's' });
      expect.unreachable('should have thrown');
    } catch (e) {
      expect(e).toBeInstanceOf(IllegalTransitionError);
      if (e instanceof IllegalTransitionError) {
        expect(e.from).toBe('IDLE');
        expect(e.event).toBe('APPLY_COMPLETE');
      }
    }
  });

  it('CRITICAL_ERROR is a terminal state (only RESET leaves it)', () => {
    expect(() => transitionFSM(state('CRITICAL_ERROR'), { type: 'READY' })).toThrow(IllegalTransitionError);
    const next = transitionFSM(state('CRITICAL_ERROR'), { type: 'RESET' });
    expect(next.status).toBe('IDLE');
  });

  it('COMPLETED_SUCCESS is terminal except RESET', () => {
    expect(() => transitionFSM(state('COMPLETED_SUCCESS'), { type: 'START_SCAN' })).toThrow(IllegalTransitionError);
    const next = transitionFSM(state('COMPLETED_SUCCESS'), { type: 'RESET' });
    expect(next.status).toBe('IDLE');
  });
});

describe('cockpitReducer (useReducer shape)', () => {
  it('dispatches the same transitions', () => {
    const next = cockpitReducer(INITIAL_STATE, { type: 'AUTH_CHECKED', missing: ['ytmusic'] });
    expect(next.status).toBe('AUTH_REQUIRED');
  });
});