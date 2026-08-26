/**
 * Branded Types (nominal typing) — spec 06 §1 零类型断言强契约.
 *
 * These types exist to make impossible states unrepresentable: a raw string
 * is not a TrackID until it has passed a parse* guard at a trusted boundary.
 * Domain code MUST NOT use bare `as` casts; the only place an assertion is
 * sanctioned is inside these parse guards (the controlled parsing boundary).
 */
declare const BrandSymbol: unique symbol;

/** Brand marks a nominal subtype of T with the literal brand B. */
export type Brand<T, B extends string> = T & { readonly [BrandSymbol]: B };

/** Generic Spotify/Sync track id (non-empty string). */
export type TrackID = Brand<string, 'TrackID'>;
/** Platform-qualified Spotify track id. */
export type SpotifyTrackId = Brand<string, 'SpotifyTrackId'>;
/** Platform-qualified YouTube Music video id. */
export type YTMTrackId = Brand<string, 'YTMTrackId'>;
/** Playlist identifier (URL or ID shaped). */
export type PlaylistID = Brand<string, 'PlaylistID'>;
/** 256-bit session token as a 64-char hex string. */
export type SessionToken = Brand<string, 'SessionToken'>;
/** International Standard Recording Code (uppercase 12-char). */
export type ISRC = Brand<string, 'ISRC'>;
/** Track duration in milliseconds (positive integer). */
export type DurationMs = Brand<number, 'DurationMs'>;
/** Weighted confidence score in [0.0, 1.0]. */
export type ConfidenceScore = Brand<number, 'ConfidenceScore'>;

/** Guard message helper. */
function typeError(kind: string, raw: unknown): TypeError {
  return new TypeError(`[TypeGuard] Invalid ${kind}: ${JSON.stringify(raw)}`);
}

/** Parses a generic non-empty track id. Throws TypeError on invalid input. */
export function parseTrackID(raw: unknown): TrackID {
  if (typeof raw !== 'string' || raw.trim().length === 0) {
    throw typeError('TrackID', raw);
  }
  return raw as TrackID;
}

/** Parses a Spotify track id (non-empty string). */
export function parseSpotifyTrackId(raw: unknown): SpotifyTrackId {
  if (typeof raw !== 'string' || raw.trim().length === 0) {
    throw typeError('SpotifyTrackId', raw);
  }
  return raw as SpotifyTrackId;
}

/** Parses a YouTube Music video id (non-empty string). */
export function parseYTMTrackId(raw: unknown): YTMTrackId {
  if (typeof raw !== 'string' || raw.trim().length === 0) {
    throw typeError('YTMTrackId', raw);
  }
  return raw as YTMTrackId;
}

/** Parses a playlist id — a non-empty string (URL or bare id). */
export function parsePlaylistID(raw: unknown): PlaylistID {
  if (typeof raw !== 'string' || raw.trim().length === 0) {
    throw typeError('PlaylistID', raw);
  }
  return raw as PlaylistID;
}

/** Parses a 64-char hex session token (256-bit, spec 05 §2.2). */
export function parseSessionToken(raw: unknown): SessionToken {
  if (typeof raw !== 'string' || raw.length !== 64 || !/^[0-9a-fA-F]{64}$/.test(raw)) {
    throw typeError('SessionToken', raw);
  }
  return raw as SessionToken;
}

/** Parses an ISRC — uppercase 12-char code: 2 country letters + 3 alphanumeric
 * registrant chars + 2-digit year + 5-digit designation (ISO 3901). */
export function parseISRC(raw: unknown): ISRC {
  if (typeof raw !== 'string' || !/^[A-Z]{2}[A-Z0-9]{3}\d{7}$/.test(raw)) {
    throw typeError('ISRC', raw);
  }
  return raw as ISRC;
}

/** Parses a duration in milliseconds — a finite integer >= 0. */
export function parseDurationMs(raw: unknown): DurationMs {
  if (typeof raw !== 'number' || !Number.isFinite(raw) || raw < 0 || !Number.isInteger(raw)) {
    throw typeError('DurationMs', raw);
  }
  return raw as DurationMs;
}

/** Parses a confidence score in [0.0, 1.0] (spec 06 §1 / spec 03 §2.1). */
export function parseConfidenceScore(raw: unknown): ConfidenceScore {
  if (typeof raw !== 'number' || Number.isNaN(raw) || raw < 0.0 || raw > 1.0) {
    throw typeError('ConfidenceScore', raw);
  }
  return raw as ConfidenceScore;
}

/** Narrowing type guards (safe predicates, no assertions). */
export function isSessionToken(raw: string | null | undefined): raw is SessionToken {
  if (typeof raw !== 'string' || raw.length !== 64 || !/^[0-9a-fA-F]{64}$/.test(raw)) {
    return false;
  }
  return true;
}

export function isConfidenceScore(raw: number): raw is ConfidenceScore {
  return typeof raw === 'number' && !Number.isNaN(raw) && raw >= 0.0 && raw <= 1.0;
}

/** Sanctioned parser boundary: JSON protocol re-typing.
 *
 * Per the cockpit brief, ALL runtime `unknown -> T` coercion lives in this
 * file (the sole sanctioned `as` boundary). HTTP/SSE payloads cross the wire
 * as JSON; `parseJson` narrows them to the contract shape after the server
 * has already validated them. Zero `as` tokens exist anywhere else.
 */
export function parseJson<T>(raw: unknown): T {
  return raw as T;
}