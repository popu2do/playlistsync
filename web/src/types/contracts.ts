/**
 * Frontend contracts — a 1:1 mirror of the plan-wc-02 Go handler JSON shapes
 * (internal/web/handlers + internal/web/bridge + internal/invariants).
 * The field names MUST match the Go `json:"..."` tags verbatim (plan Global
 * Constraint #6: 与后端契约对齐).
 */
import type {
  TrackID,
  PlaylistID,
  ConfidenceScore,
  ISRC,
  DurationMs,
} from './brands';

/* ------------------------------------------------------------------ */
/* Session & system (handlers/session.go)                             */
/* ------------------------------------------------------------------ */

export interface InvariantsSummaryDTO {
  readonly count_conserved: boolean;
  readonly uniqueness_valid: boolean;
  readonly order_lis_monotonic: boolean;
  readonly diff_complete: boolean;
  readonly zero_trace_clean: boolean;
}

export interface SessionResponse {
  readonly session_id: string;
  readonly status: string; // IDLE | RUNNING | READY
  readonly port: number;
  readonly client_count: number;
  readonly started_at: string; // RFC3339
  readonly last_heartbeat_at: string;
  readonly invariants_summary: InvariantsSummaryDTO;
}

/* ------------------------------------------------------------------ */
/* Auth (handlers/auth.go)                                            */
/* ------------------------------------------------------------------ */

export interface AuthStatusResponse {
  readonly spotify: {
    readonly authenticated: boolean;
    readonly userDisplayName?: string;
    readonly tokenExpiresAt?: string; // RFC3339; never populated today (INFO-18)
  };
  readonly youtubeMusic: {
    readonly authenticated: boolean;
    readonly accountName?: string;
    readonly authType: 'cookie' | 'oauth';
  };
}

export interface CDPStartRequest {
  readonly platform: 'spotify' | 'youtube_music';
  readonly headless?: boolean;
  readonly proxy?: string;
}

export interface CDPStartResponse {
  readonly status: string;
  readonly platform: string;
  readonly stream: string;
}

export interface SpotifyAuthorizeResponse {
  readonly authorize_url: string;
  readonly state: string;
  readonly redirect_uri: string;
}

/* ------------------------------------------------------------------ */
/* Tracks (model/spotify.go, model/ytmusic.go, model/sync.go)         */
/* ------------------------------------------------------------------ */

export interface SpotifyTrack {
  readonly index: number;
  readonly id: string;
  readonly title: string;
  readonly artists: readonly string[];
  readonly album: string;
  readonly duration: string;
  readonly spotifyUri?: string;
  readonly spotifyUrl?: string;
  readonly query?: string;
}

export interface YTMTrack {
  readonly videoId: string;
  readonly setVideoId?: string;
  readonly title: string;
  readonly artists: readonly string[];
  readonly duration?: string;
}

/** Retained (already-matched) track — model.AddedTrack in the diff partition. */
export interface AddedTrack {
  readonly index: number;
  readonly title: string;
  readonly artists: readonly string[];
  readonly targetTrackId?: string;
  readonly destinationTitle?: string;
}

export interface SkippedTrack {
  readonly index: number;
  readonly title: string;
  readonly artists: readonly string[];
  readonly reason: string;
}

export interface RemovedTrack {
  readonly targetTrackId: string;
  readonly title: string;
  readonly artists: readonly string[];
}

export interface SyncResult {
  readonly direction: string;
  readonly sourcePlatform: string;
  readonly targetPlatform: string;
  readonly playlistId: string;
  readonly playlistUrl?: string;
  readonly webUrl?: string;
  readonly title: string;
  readonly sourcePlaylistUrl?: string;
  readonly totalSourceTracks: number;
  readonly addedTracks: number;
  readonly skippedTracks: number;
  readonly syncOrder?: boolean;
  readonly orderConcordanceRate?: number;
  readonly reorderedCount?: number;
  readonly skipped?: readonly SkippedTrack[];
  readonly addedAfterReview?: readonly AddedTrack[];
  readonly removedExtraTracks?: readonly RemovedTrack[];
  readonly lastSyncedAt: string;
  readonly verification?: {
    readonly pageTitle?: string;
    readonly pageTrackCount?: number;
    readonly description?: string;
  };
}

/* ------------------------------------------------------------------ */
/* Reconcile (handlers/reconcile.go)                                  */
/* ------------------------------------------------------------------ */

export interface ReconcileRequest {
  readonly source_playlist_id: string;
  readonly target_playlist_id: string;
  readonly clean_extra: boolean;
  readonly sync_order: boolean;
  readonly concurrency: number;
}

export interface ReconcileStartResponse {
  readonly job_id: string;
  readonly status: string;
  readonly estimated_total_tracks: number;
}

export interface DiffCounts {
  readonly added: number;
  readonly removed: number;
  readonly retained: number;
  readonly skipped: number;
  readonly arbitration_required: number;
}

export interface DiffResponse {
  readonly job_id?: string;
  readonly status: string; // idle | running | complete | failed
  readonly error?: string;
  readonly added: readonly SpotifyTrack[];
  readonly removed: readonly YTMTrack[];
  readonly retained: readonly AddedTrack[];
  readonly skipped: readonly SkippedTrack[];
  readonly counts: DiffCounts;
}

/* ------------------------------------------------------------------ */
/* Arbitration (web/bridge/arbitration.go)                            */
/* ------------------------------------------------------------------ */

export type ArbitrationAction = 'SELECT_CANDIDATE' | 'SKIP' | 'CUSTOM_ID';

export interface CandidateOption {
  readonly target_id: string;
  readonly title: string;
  readonly artist: string;
  readonly duration_ms: number;
  readonly confidence_score: number;
  readonly title_score: number;
  readonly artist_score: number;
  readonly duration_score: number;
  readonly isrc_matched: boolean;
}

export interface ArbitrationRequestEvent {
  readonly track_id: string;
  readonly source_title: string;
  readonly source_artist: string;
  readonly source_duration_ms: number;
  readonly candidates: readonly CandidateOption[];
  readonly created_at: string;
}

export interface ArbitrationDecisionRequest {
  readonly track_id: string;
  readonly action: ArbitrationAction;
  readonly selected_target_id?: string;
}

export interface ApplyResult {
  readonly applied: boolean;
  readonly output?: SyncResult;
  readonly invariant?: InvariantSnapshot;
}

/* ------------------------------------------------------------------ */
/* Inspect (handlers/inspect.go)                                      */
/* ------------------------------------------------------------------ */

export type MatchState = 'added' | 'removed' | 'retained' | 'skipped' | 'unknown';

export interface TrackInspectView {
  readonly index: number;
  readonly title: string;
  readonly artists: readonly string[];
  readonly duration?: string;
  readonly target_id?: string;
  readonly match_state: MatchState;
}

export interface PlaylistInspectResponse {
  readonly platform: 'spotify' | 'youtube-music';
  readonly playlist_id: string;
  readonly title: string;
  readonly track_count: number;
  readonly tracks: readonly TrackInspectView[];
}

/* ------------------------------------------------------------------ */
/* Verify (internal/invariants/verifier.go)                           */
/* ------------------------------------------------------------------ */

export interface InvariantSnapshot {
  readonly source_total: number;
  readonly synced_count: number;
  readonly skipped_count: number;
  readonly failed_count: number;
  readonly is_count_conserved: boolean;
  readonly has_duplicate_target_ids: boolean;
  readonly duplicate_target_ids: readonly string[];
  readonly lis_disorder_ratio: number;
  readonly disordered_indices: readonly number[];
  readonly is_diff_complete: boolean;
  readonly is_zero_trace_clean: boolean;
  readonly evaluated_at: string;
  readonly all_passed: boolean;
}

/* ------------------------------------------------------------------ */
/* Reports (handlers/reports.go)                                      */
/* ------------------------------------------------------------------ */

export interface ReportMeta {
  readonly id: string;
  readonly name: string;
  readonly size: number;
  readonly modified_at: string;
}

/* ------------------------------------------------------------------ */
/* SSE events (web/bridge/buffer.go + handlers/events.go)             */
/* ------------------------------------------------------------------ */

export interface SSEEventFrame {
  readonly id: number;
  readonly type: string;
  readonly data: unknown;
  readonly timestamp: number;
}

export interface DiffProgressEvent {
  readonly worker_id?: string;
  readonly processed?: number;
  readonly total?: number;
  readonly status?: string;
  readonly stage?: string;
  readonly current_track?: string;
}

export interface DiffCompleteEvent {
  readonly worker_id?: string;
  readonly status?: string;
  readonly added?: number;
  readonly removed?: number;
  readonly retained?: number;
  readonly skipped?: number;
}

export interface ReconcileFailedEvent {
  readonly worker_id?: string;
  readonly error?: string;
}

export interface LogStreamEvent {
  readonly level: string;
  readonly timestamp: string;
  readonly module: string;
  readonly text: string;
  readonly [k: string]: unknown;
}

/** Discriminated union of every SSE event the cockpit consumes.
 * No catch-all member: the backend writes exactly these event types
 * (spec 02 §3.1), and the union without a catch-all lets TS narrow
 * `ev.data` per branch — zero `as` needed at the dispatch site. */
export type CockpitSSEEvent =
  | { readonly type: 'DIFF_PROGRESS'; readonly data: DiffProgressEvent; readonly id: number }
  | { readonly type: 'DIFF_COMPLETE'; readonly data: DiffCompleteEvent; readonly id: number }
  | { readonly type: 'RECONCILE_FAILED'; readonly data: ReconcileFailedEvent; readonly id: number }
  | { readonly type: 'ARBITRATION_REQUIRED'; readonly data: ArbitrationRequestEvent; readonly id: number }
  | { readonly type: 'INVARIANT_SNAPSHOT'; readonly data: InvariantSnapshot; readonly id: number }
  | { readonly type: 'LOG_STREAM'; readonly data: LogStreamEvent; readonly id: number }
  | { readonly type: 'GAP_FALLBACK'; readonly data: { readonly message?: string }; readonly id: number }
  | { readonly type: 'SYSTEM_SHUTDOWN'; readonly data: { readonly reason?: string }; readonly id: number }
  | { readonly type: 'RATE_LIMITED'; readonly data: { readonly message?: string }; readonly id: number };

/* Re-exported branded ids for convenience in views. */
export type { TrackID, PlaylistID, ConfidenceScore, ISRC, DurationMs };