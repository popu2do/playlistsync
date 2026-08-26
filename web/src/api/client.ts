/**
 * ApiClient — typed fetch wrapper for the 16 plan-wc-02 REST endpoints
 * plus the SSE event stream. All request/response shapes mirror the Go
 * handler JSON contracts (web/src/types/contracts.ts).
 *
 * Auth model (spec 05 §2.2): the token is captured from the ?token= query of
 * the banner URL at boot, kept in memory only, stripped from the URL via
 * history.replaceState, and sent as `Authorization: Bearer` (with the
 * `X-Requested-With: playlistsync-web` header) on every REST call. The SSE
 * EventSource cannot set headers, so it uses /api/v1/events?token=<token>
 * (the backend accepts query tokens on any path — spec 02 §4.2).
 */
import type {
  SessionResponse,
  AuthStatusResponse,
  CDPStartRequest,
  CDPStartResponse,
  SpotifyAuthorizeResponse,
  ReconcileRequest,
  ReconcileStartResponse,
  DiffResponse,
  ArbitrationDecisionRequest,
  ApplyResult,
  PlaylistInspectResponse,
  InvariantSnapshot,
  ReportMeta,
  CockpitSSEEvent,
} from '../types/contracts';
import { parseJson, parseSessionToken, type SessionToken } from '../types/brands';

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly path: string,
  ) {
    super(`API ${status} on ${path}: ${message}`);
    this.name = 'ApiError';
  }
}

/** Extracts the session token from the current URL ?token= (or null). */
export function readTokenFromURL(): SessionToken | null {
  const raw = new URLSearchParams(window.location.search).get('token');
  if (raw === null) return null;
  try {
    return parseSessionToken(raw);
  } catch {
    return null;
  }
}

/** Strips the ?token= param from the URL in place (spec 05 §2.2). */
export function stripTokenFromURL(): void {
  const url = new URL(window.location.href);
  if (!url.searchParams.has('token')) return;
  url.searchParams.delete('token');
  const clean = url.pathname + url.search + url.hash;
  window.history.replaceState(window.history.state, document.title, clean || '/');
}

export interface ApiClientOptions {
  baseURL: string;
  token: SessionToken;
}

export class ApiClient {
  readonly baseURL: string;
  readonly token: SessionToken;

  constructor(options: ApiClientOptions) {
    this.baseURL = options.baseURL.replace(/\/+$/, '');
    this.token = options.token;
  }

  /** Builds a URL with the token appended (SSE / download endpoints). */
  withToken(path: string): string {
    const sep = path.includes('?') ? '&' : '?';
    return `${this.baseURL}${path}${sep}token=${encodeURIComponent(this.token)}`;
  }

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.token}`,
      'X-Requested-With': 'playlistsync-web',
    };
    const init: RequestInit = { method, headers };
    if (body !== undefined) {
      headers['Content-Type'] = 'application/json';
      init.body = JSON.stringify(body);
    }
    const resp = await fetch(`${this.baseURL}${path}`, init);
    if (!resp.ok) {
      let message = resp.statusText;
      try {
        const data = parseJson<{ error?: string }>(await resp.json());
        if (typeof data.error === 'string' && data.error.length > 0) message = data.error;
      } catch {
        // Non-JSON error body (e.g. 401 plain text) — keep statusText.
      }
      throw new ApiError(resp.status, message, path);
    }
    if (resp.status === 204) return parseJson<T>(undefined);
    return parseJson<T>(await resp.json());
  }

  /* ---- Session & system ---- */

  getSession(): Promise<SessionResponse> {
    return this.request('GET', '/api/v1/session');
  }

  heartbeat(): Promise<{ status: string; timestamp: number }> {
    return this.request('POST', '/api/v1/session/heartbeat');
  }

  shutdown(): Promise<{ status: string }> {
    return this.request('POST', '/api/v1/session/shutdown');
  }

  /* ---- Auth vault ---- */

  getAuthStatus(): Promise<AuthStatusResponse> {
    return this.request('GET', '/api/v1/auth/status');
  }

  startCDPLogin(req: CDPStartRequest): Promise<CDPStartResponse> {
    return this.request('POST', '/api/v1/auth/cdp/start', req);
  }

  getSpotifyAuthorizeURL(): Promise<SpotifyAuthorizeResponse> {
    return this.request('GET', '/api/v1/auth/spotify/authorize');
  }

  /* ---- Reconcile & arbitration ---- */

  startReconcile(req: ReconcileRequest): Promise<ReconcileStartResponse> {
    return this.request('POST', '/api/v1/reconcile/start', req);
  }

  getDiff(): Promise<DiffResponse> {
    return this.request('GET', '/api/v1/reconcile/diff');
  }

  submitDecision(d: ArbitrationDecisionRequest): Promise<{ success: boolean; resumed_at: string }> {
    return this.request('POST', '/api/v1/arbitrate/decision', d);
  }

  applyMutations(forceOverride = false): Promise<ApplyResult> {
    return this.request('POST', '/api/v1/reconcile/apply', {
      force_override_invariants: forceOverride,
    });
  }

  /* ---- Inspect ---- */

  inspectPlaylist(id: string, platform: 'spotify' | 'youtube-music' | 'ytmusic' | ''): Promise<PlaylistInspectResponse> {
    const q = new URLSearchParams({ id, platform });
    return this.request('GET', `/api/v1/inspect/playlist?${q.toString()}`);
  }

  /* ---- Verify ---- */

  verifyInvariants(targetId: string): Promise<InvariantSnapshot> {
    const q = new URLSearchParams({ target_id: targetId });
    return this.request('GET', `/api/v1/verify/invariants?${q.toString()}`);
  }

  /* ---- Reports ---- */

  listReports(): Promise<ReportMeta[]> {
    return this.request('GET', '/api/v1/reports');
  }

  /** Download URL for a report export (json | markdown). */
  reportExportURL(id: string, format: 'json' | 'markdown'): string {
    return this.withToken(`/api/v1/reports/${encodeURIComponent(id)}/export?format=${format}`);
  }
}

/* ------------------------------------------------------------------ */
/* SSE                                                                 */
/* ------------------------------------------------------------------ */

/** EventSource adapter with Last-Event-ID replay + GAP_FALLBACK resync. */
export interface SSEConnection {
  close(): void;
  readonly closed: boolean;
}

export interface ConnectSSEOptions {
  client: ApiClient;
  onEvent: (event: CockpitSSEEvent) => void;
  /** Called when the backend signals a history gap (state must be re-fetched). */
  onGapFallback?: () => void;
}

/**
 * Opens the SSE stream at /api/v1/events?token=... and dispatches parsed
 * events to onEvent. The native EventSource retries automatically and sends
 * the `Last-Event-ID` header on reconnect, which the ring buffer uses for
 * lossless replay (spec 02 §3.2). Returns a handle with close().
 */
export function connectSSE({ client, onEvent, onGapFallback }: ConnectSSEOptions): SSEConnection {
  let closed = false;
  const es = new EventSource(client.withToken('/api/v1/events'));

  es.onmessage = (msg: MessageEvent<string>) => {
    let data: unknown;
    try {
      data = parseJson<unknown>(JSON.parse(msg.data));
    } catch {
      data = { raw: msg.data };
    }
    // `lastEventId` on the message carries the server id: line.
    const id = Number(msg.lastEventId) || 0;
    const event: CockpitSSEEvent = parseJson<CockpitSSEEvent>({ type: msg.type || 'message', data, id });
    if (event.type === 'GAP_FALLBACK' && onGapFallback) {
      onGapFallback();
    }
    onEvent(event);
  };

  es.onerror = () => {
    // Native EventSource auto-reconnects with Last-Event-ID; nothing to do
    // except surface repeated failures. We do not close here.
  };

  return {
    close() {
      closed = true;
      es.close();
    },
    get closed() {
      return closed;
    },
  };
}