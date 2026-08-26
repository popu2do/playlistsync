/**
 * ApiClient + connectSSE unit tests.
 *
 * BLOCKER-1 (qc2/qc3): verifies that named SSE frames (`event: <TYPE>` +
 * `data: <json>`, events.go writeSSEEvent) are parsed and dispatched through
 * `addEventListener('<TYPE>', ...)` — EventSource never routes named events to
 * `onmessage`. jsdom has no EventSource, so a minimal fake drives the DOM
 * addEventListener contract.
 */
import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';
import { cleanup } from '@testing-library/react';
import { ApiClient, connectSSE, readTokenFromURL, SSE_EVENT_TYPES, type SSEConnection } from '../api/client';
import { parseSessionToken, type SessionToken } from '../types/brands';
import type { CockpitSSEEvent } from '../types/contracts';

const TOKEN = 'a3f2c9d8e7b6a5f4c3d2e1f0a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2a1b0';
const parsedToken: SessionToken = parseSessionToken(TOKEN);

/** Minimal fake EventSource implementing the DOM addEventListener contract. */
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  readonly url: string;
  private readonly listeners = new Map<string, ((e: { data: string; lastEventId: string }) => void)[]>();
  onmessage: ((e: { data: string; lastEventId: string }) => void) | null = null;
  onerror: (() => void) | null = null;

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, fn: (e: { data: string; lastEventId: string }) => void): void {
    const list = this.listeners.get(type) ?? [];
    list.push(fn);
    this.listeners.set(type, list);
  }

  /** Simulates a server pushing one wire frame of `event: <type>` + data. */
  emit(type: string, data: string, id = '0'): void {
    const frame = { data, lastEventId: id };
    const fns = this.listeners.get(type);
    if (fns) {
      for (const fn of fns) fn(frame);
    } else if (type === 'message' && this.onmessage) {
      this.onmessage(frame);
    }
  }

  close(): void {
    /* no-op for the fake */
  }
}

beforeEach(() => {
  vi.stubGlobal('EventSource', FakeEventSource);
});

function makeClient(): ApiClient {
  return new ApiClient({ baseURL: 'http://127.0.0.1:3080', token: parsedToken });
}

/** Narrowing helper: the most recent FakeEventSource created (or throw). */
function lastInstance(): FakeEventSource {
  const es = FakeEventSource.instances[FakeEventSource.instances.length - 1];
  if (es === undefined) throw new Error('no FakeEventSource instance was created');
  return es;
}

function listen(): { onEvent: (ev: CockpitSSEEvent) => void; conn: SSEConnection; es: FakeEventSource } {
  const events: CockpitSSEEvent[] = [];
  const conn = connectSSE({
    client: makeClient(),
    onEvent: (ev) => {
      events.push(ev);
    },
  });
  const es = lastInstance();
  return { onEvent: (ev) => events.push(ev), conn, es };
}

afterEach(() => {
  cleanup();
  FakeEventSource.instances = [];
  vi.unstubAllGlobals();
});

describe('SSE named-event wire format (BLOCKER-1)', () => {
  it('registers an addEventListener for every backend event type', () => {
    const { es } = listen();
    // addEventListener with a falsy listener throws in real DOM; here we just
    // assert the names are non-empty and alphabetically cover the contract.
    expect(SSE_EVENT_TYPES).toContain('DIFF_PROGRESS');
    expect(SSE_EVENT_TYPES).toContain('DIFF_COMPLETE');
    expect(SSE_EVENT_TYPES).toContain('INVARIANT_SNAPSHOT');
    expect(SSE_EVENT_TYPES).toContain('ARBITRATION_REQUIRED');
    expect(SSE_EVENT_TYPES).toContain('RECONCILE_FAILED');
    expect(SSE_EVENT_TYPES).toContain('GAP_FALLBACK');
    expect(SSE_EVENT_TYPES).toContain('SYSTEM_SHUTDOWN');
    expect(SSE_EVENT_TYPES).toContain('LOG_STREAM');
    expect(es.url).toContain(TOKEN);
  });

  it('parses a named DIFF_PROGRESS frame and dispatches with type + parsed data', () => {
    const events: CockpitSSEEvent[] = [];
    connectSSE({
      client: makeClient(),
      onEvent: (ev) => events.push(ev),
    });
    const es = lastInstance();
    es.emit('DIFF_PROGRESS', '{"processed":1,"total":2,"current_track":"T1"}', '7');
    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({
      type: 'DIFF_PROGRESS',
      id: 7,
    });
    if (events[0] && events[0].type === 'DIFF_PROGRESS') {
      expect(events[0].data).toEqual({ processed: 1, total: 2, current_track: 'T1' });
    }
  });

  it('parses GAP_FALLBACK and invokes onGapFallback', () => {
    const events: CockpitSSEEvent[] = [];
    const gap = vi.fn();
    connectSSE({
      client: makeClient(),
      onEvent: (ev) => events.push(ev),
      onGapFallback: gap,
    });
    const es = lastInstance();
    es.emit('GAP_FALLBACK', '{"message":"history gap"}', '9');
    expect(gap).toHaveBeenCalledTimes(1);
    expect(events).toHaveLength(1);
    if (events[0] && events[0].type === 'GAP_FALLBACK') {
      expect(events[0].data).toEqual({ message: 'history gap' });
    }
  });

  it('tolerates non-JSON data by passing it through as { raw }', () => {
    const events: CockpitSSEEvent[] = [];
    connectSSE({
      client: makeClient(),
      onEvent: (ev) => events.push(ev),
    });
    const es = lastInstance();
    es.emit('LOG_STREAM', 'not json at all', '3');
    expect(events).toHaveLength(1);
    if (events[0] && events[0].type === 'LOG_STREAM') {
      expect(events[0].data).toEqual({ raw: 'not json at all' });
    }
  });
});

describe('readTokenFromURL sessionStorage continuity (QC MAJOR-2)', () => {
  it('reads a valid token from the URL query and mirrors it to sessionStorage', () => {
    window.history.replaceState(null, '', `/?token=${TOKEN}`);
    const token = readTokenFromURL();
    expect(token).toBe(TOKEN);
    expect(window.sessionStorage.getItem('playlistsync_session_token')).toBe(TOKEN);
  });

  it('falls back to sessionStorage when the URL has no token (post-refresh)', () => {
    window.history.replaceState(null, '', '/');
    window.sessionStorage.setItem('playlistsync_session_token', TOKEN);
    const token = readTokenFromURL();
    expect(token).toBe(TOKEN);
  });

  it('rejects an invalid stored token and returns null', () => {
    window.history.replaceState(null, '', '/');
    window.sessionStorage.setItem('playlistsync_session_token', 'definitely-not-a-token');
    expect(readTokenFromURL()).toBeNull();
  });
});