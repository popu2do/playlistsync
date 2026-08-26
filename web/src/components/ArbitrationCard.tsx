/**
 * ArbitrationCard — 候选音轨列表 + 置信度雷达 + 全键盘仲裁 (spec 01 §2,
 * spec 03 §4.2): `1`~`9` 秒选候选, `s` 跳过, `c` 自定义 Target ID,
 * Enter 确认自定义输入。键盘由 useKeyboardShortcuts 全局处理；本组件
 * 仅提供受控输入框与渲染。
 */
import { useEffect, useRef, useState } from 'react';
import { ConfidenceRadar } from './MultiFactorConfidenceRadar';
import { formatMs } from '../utils/format';
import type { ArbitrationRequestEvent, CandidateOption } from '../types/contracts';
import { parseConfidenceScore } from '../types/brands';

export interface ArbitrationCardProps {
  readonly request: ArbitrationRequestEvent | null;
  readonly onSelectCandidate: (trackId: string, candidate: CandidateOption, rawScore: number) => void;
  readonly onSkip: (trackId: string) => void;
  readonly onCustomId: (trackId: string, customId: string) => void;
}

/** Renders the candidate list for the active arbitration request. */
export function ArbitrationCard({
  request,
  onSelectCandidate,
  onSkip,
  onCustomId,
}: ArbitrationCardProps) {
  const [customOpen, setCustomOpen] = useState(false);
  const [customValue, setCustomValue] = useState('');
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (customOpen) inputRef.current?.focus();
  }, [customOpen]);

  useEffect(() => {
    // Reset local custom state when the active request changes.
    setCustomOpen(false);
    setCustomValue('');
  }, [request?.track_id]);

  if (!request) return null;

  const submitCustom = (): void => {
    const id = customValue.trim();
    if (id.length === 0) return;
    onCustomId(request.track_id, id);
    setCustomOpen(false);
    setCustomValue('');
  };

  return (
    <section className="arbitration-card" data-testid="arbitration-card" aria-label="Arbitration">
      <header className="arbitration-header">
        <div>
          <span className="sep-label">ARBITRATION</span>
          <h3 className="arb-title">{request.source_title}</h3>
          <span className="arb-artist">{request.source_artist}</span>
          <span className="arb-duration ps-tabular">{formatMs(request.source_duration_ms)}</span>
        </div>
        <div className="arb-keyhints" aria-hidden="true">
          <kbd>1-9</kbd> select · <kbd>s</kbd> skip · <kbd>c</kbd> custom
        </div>
      </header>

      <ol className="candidate-list">
        {request.candidates.map((cand, idx) => {
          const score = parseConfidenceScore(cand.confidence_score);
          return (
            <li
              key={cand.target_id}
              className="candidate-row"
              data-testid={`candidate-${idx + 1}`}
            >
              <span className="candidate-kbd ps-tabular">{idx + 1}</span>
              <div className="candidate-main">
                <span className="candidate-title" title={cand.title}>
                  {cand.title}
                </span>
                <span className="candidate-artist">{cand.artist}</span>
                <span className="candidate-duration ps-tabular">{formatMs(cand.duration_ms)}</span>
              </div>
              <ConfidenceRadar
                breakdown={{
                  titleScore: clamp01(cand.title_score),
                  artistScore: clamp01(cand.artist_score),
                  durationScore: clamp01(cand.duration_score),
                  totalScore: score,
                  isrcMatched: cand.isrc_matched,
                }}
                size={96}
              />
              <button
                type="button"
                className="btn btn-primary btn-sm"
                onClick={() => onSelectCandidate(request.track_id, cand, cand.confidence_score)}
              >
                Select
              </button>
            </li>
          );
        })}
      </ol>

      <footer className="arbitration-actions">
        <button type="button" className="btn btn-ghost" onClick={() => onSkip(request.track_id)}>
          Skip (<kbd>s</kbd>)
        </button>
        <button
          type="button"
          className="btn btn-ghost"
          onClick={() => setCustomOpen((v) => !v)}
          aria-expanded={customOpen}
        >
          Custom ID (<kbd>c</kbd>)
        </button>
        {customOpen && (
          <span className="custom-id-box">
            <input
              ref={inputRef}
              type="text"
              value={customValue}
              onChange={(e) => setCustomValue(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') submitCustom();
                e.stopPropagation();
              }}
              placeholder="YouTube Music video ID"
              aria-label="Custom target ID"
              data-testid="custom-id-input"
            />
            <button type="button" className="btn btn-primary btn-sm" onClick={submitCustom}>
              OK
            </button>
          </span>
        )}
      </footer>
    </section>
  );
}

function clamp01(v: number): number {
  if (!Number.isFinite(v)) return 0;
  return Math.max(0, Math.min(1, v));
}