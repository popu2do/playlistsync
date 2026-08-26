/**
 * Branded Types guard tests — spec 06 §1 正/反例.
 */
import { describe, expect, it } from 'vitest';
import {
  parseConfidenceScore,
  parseDurationMs,
  parseISRC,
  parsePlaylistID,
  parseSessionToken,
  parseSpotifyTrackId,
  parseTrackID,
  parseYTMTrackId,
  isConfidenceScore,
  isSessionToken,
} from '../types/brands';

const VALID_HEX_64 = 'a3f2c9d8e7b6a5f4c3d2e1f0a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2a1b0';

describe('parseSessionToken', () => {
  it('accepts exactly 64 lowercase hex chars', () => {
    expect(parseSessionToken(VALID_HEX_64)).toBe(VALID_HEX_64);
  });
  it('accepts 64 uppercase hex chars', () => {
    const up = VALID_HEX_64.toUpperCase();
    expect(parseSessionToken(up)).toBe(up);
  });
  it('rejects 63 chars', () => {
    expect(() => parseSessionToken(VALID_HEX_64.slice(1))).toThrow(TypeError);
  });
  it('rejects 65 chars', () => {
    expect(() => parseSessionToken(VALID_HEX_64 + '0')).toThrow(TypeError);
  });
  it('rejects non-hex chars', () => {
    expect(() => parseSessionToken('g'.repeat(64))).toThrow(TypeError);
  });
  it('rejects non-strings (number, null, object, undefined)', () => {
    for (const bad of [123, null, undefined, {}, []]) {
      expect(() => parseSessionToken(bad)).toThrow(TypeError);
    }
  });
});

describe('parseTrackID / parseSpotifyTrackId / parseYTMTrackId', () => {
  it('accepts non-empty strings', () => {
    expect(parseTrackID('abc123')).toBe('abc123');
    expect(parseSpotifyTrackId('spotify:track:4uLU6hMCjMI75M1A2tKUQC')).toBe('spotify:track:4uLU6hMCjMI75M1A2tKUQC');
    expect(parseYTMTrackId('dQw4w9WgXcQ')).toBe('dQw4w9WgXcQ');
  });
  it('rejects empty/whitespace strings', () => {
    for (const bad of ['', '   ', '\t']) {
      expect(() => parseTrackID(bad)).toThrow(TypeError);
      expect(() => parseSpotifyTrackId(bad)).toThrow(TypeError);
      expect(() => parseYTMTrackId(bad)).toThrow(TypeError);
    }
  });
  it('rejects non-strings', () => {
    for (const bad of [42, false, null, undefined, { a: 1 }]) {
      expect(() => parseTrackID(bad)).toThrow(TypeError);
    }
  });
});

describe('parsePlaylistID', () => {
  it('accepts bare ids and URLs', () => {
    expect(parsePlaylistID('PLc3bQbG1KTdi8h3z6QYe')).toBe('PLc3bQbG1KTdi8h3z6QYe');
    expect(parsePlaylistID('https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M')).toContain('spotify');
  });
  it('rejects empty strings', () => {
    expect(() => parsePlaylistID('')).toThrow(TypeError);
  });
});

describe('parseConfidenceScore', () => {
  it('accepts 0.0, 0.5, 1.0 and boundary floats', () => {
    expect(parseConfidenceScore(0)).toBe(0);
    expect(parseConfidenceScore(0.5)).toBe(0.5);
    expect(parseConfidenceScore(1.0)).toBe(1.0);
    expect(parseConfidenceScore(0.8742)).toBe(0.8742);
  });
  it('rejects out-of-range, NaN, and non-numbers', () => {
    for (const bad of [-0.01, 1.01, Number.NaN, Number.POSITIVE_INFINITY, '0.5', null, undefined]) {
      expect(() => parseConfidenceScore(bad)).toThrow(TypeError);
    }
  });
  it('isConfidenceScore narrows correctly', () => {
    expect(isConfidenceScore(0.25)).toBe(true);
    expect(isConfidenceScore(1.5)).toBe(false);
    expect(isConfidenceScore(Number.NaN)).toBe(false);
  });
});

describe('parseDurationMs', () => {
  it('accepts non-negative integers', () => {
    expect(parseDurationMs(0)).toBe(0);
    expect(parseDurationMs(204000)).toBe(204000);
  });
  it('rejects negative, fractional, NaN, and non-numbers', () => {
    for (const bad of [-1, 1.5, Number.NaN, '204000']) {
      expect(() => parseDurationMs(bad)).toThrow(TypeError);
    }
  });
});

describe('parseISRC', () => {
  it('accepts the 12-char uppercase format', () => {
    expect(parseISRC('USRC17607839')).toBe('USRC17607839');
  });
  it('rejects malformed codes', () => {
    for (const bad of ['usrc17607839', 'USRC1760783', 'USRC176078390', 'US R17607839', 'US*C17607839']) {
      expect(() => parseISRC(bad)).toThrow(TypeError);
    }
  });
});

describe('isSessionToken predicate', () => {
  it('narrows valid hex and rejects invalid', () => {
    expect(isSessionToken(VALID_HEX_64)).toBe(true);
    expect(isSessionToken('short')).toBe(false);
    expect(isSessionToken(null)).toBe(false);
  });
});