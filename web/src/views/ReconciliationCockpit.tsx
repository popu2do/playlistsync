/**
 * ReconciliationCockpit — 调解主控台 (spec 01 §2 / spec 04 §1.2): 源/目标
 * 歌单双向选择器 (URL / ID / JSON 拖拽), 差异三元看板 (Added 绿 / Removed
 * 红 / Retained 灰), Clean Extra 高危确认弹窗 (显式解锁, 不可绕过), Apply。
 */
import { useEffect, useMemo, useState, type DragEvent } from 'react';
import type { ApiClient } from '../api/client';
import type { DiffResponse } from '../types/contracts';
import { formatDurationString } from '../utils/format';
import { AlertTriangleIcon, ArrowRightLeftIcon } from '../components/icons';

export interface ReconciliationCockpitProps {
  readonly client: ApiClient;
  readonly diff: DiffResponse | null;
  readonly onStart: (source: string, target: string, cleanExtra: boolean, syncOrder: boolean, concurrency: number) => Promise<void>;
  readonly onApply: (forceOverride: boolean) => Promise<void>;
  readonly busy: 'scanning' | 'applying' | null;
  /** Monotonic counter bumped on every scan start (App); used to reset the
   * Clean-Extra unlock so each new scan requires the confirmation again. */
  readonly scanEpoch: number;
  readonly canApply: boolean;
}

export function ReconciliationCockpit({
  client,
  diff,
  onStart,
  onApply,
  busy,
  scanEpoch,
  canApply,
}: ReconciliationCockpitProps) {
  void client;
  const [source, setSource] = useState('');
  const [target, setTarget] = useState('');
  const [cleanExtra, setCleanExtra] = useState(false);
  const [syncOrder, setSyncOrder] = useState(true);
  const [concurrency, setConcurrency] = useState(4);
  const [dragSource, setDragSource] = useState(false);
  const [confirmCleanOpen, setConfirmCleanOpen] = useState(false);
  const [cleanUnlocked, setCleanUnlocked] = useState(false);

  // Every new scan (epoch bump), new diff job, or terminal/failed diff state
  // resets the destructive unlock: the user must re-confirm before any
  // subsequent Clean-Extra run (QC MAJOR-1).
  useEffect(() => {
    setCleanUnlocked(false);
    setConfirmCleanOpen(false);
  }, [scanEpoch, diff?.job_id, diff?.status]);

  const counts = diff?.counts ?? { added: 0, removed: 0, retained: 0, skipped: 0, arbitration_required: 0 };
  const total = counts.added + counts.removed + counts.retained + counts.skipped;

  const onDrop = (e: DragEvent<HTMLDivElement>, which: 'source' | 'target'): void => {
    e.preventDefault();
    setDragSource(false);
    const files = e.dataTransfer.files;
    if (files.length === 0) return;
    const file = files[0];
    if (!file) return;
    void file.text().then((raw) => {
      try {
        // Structural narrowing — no `as` casts (brief constraint).
        const parsed: unknown = JSON.parse(raw);
        const first = Array.isArray(parsed) ? parsed[0] : null;
        const value = isRecordWithId(first) ? first.id : parseIdFromText(raw);
        if (which === 'source') setSource(value);
        else setTarget(value);
      } catch {
        // Not valid JSON: fall back to the plain text as an ID hint.
        const value = raw.trim().slice(0, 200);
        if (which === 'source') setSource(value);
        else setTarget(value);
      }
    });
  };

  const submitStart = (): void => {
    if (cleanExtra && !cleanUnlocked) {
      setConfirmCleanOpen(true);
      return;
    }
    void onStart(source.trim(), target.trim(), cleanExtra, syncOrder, concurrency);
  };

  const confirmClean = (): void => {
    setConfirmCleanOpen(false);
    setCleanUnlocked(true);
    void onStart(source.trim(), target.trim(), true, syncOrder, concurrency);
  };

  const summaries: readonly { key: string; label: string; n: number; cls: string }[] = useMemo(
    () => [
      { key: 'added', label: 'Added', n: counts.added, cls: 'sum-added' },
      { key: 'removed', label: 'Removed', n: counts.removed, cls: 'sum-removed' },
      { key: 'retained', label: 'Retained', n: counts.retained, cls: 'sum-retained' },
      { key: 'skipped', label: 'Skipped', n: counts.skipped, cls: 'sum-skipped' },
    ],
    [counts],
  );

  return (
    <section className="view-panel reconcile-cockpit" data-testid="reconcile-cockpit" aria-label="Reconciliation cockpit">
      <header className="panel-header">
        <h2>
          <ArrowRightLeftIcon width={14} height={14} /> Reconciliation Cockpit
        </h2>
        <span className={`reconcile-status ps-tabular`} data-status={diff?.status ?? 'idle'}>
          {diff?.status ?? 'idle'}
        </span>
      </header>

      {/* Playlist selectors */}
      <div className="selector-grid">
        <DropZone
          label="Source (Spotify)"
          value={source}
          onChange={setSource}
          placeholder="Spotify playlist URL or ID, or drop playlist JSON"
          active={dragSource}
          onDragOver={() => setDragSource(true)}
          onDragLeave={() => setDragSource(false)}
          onDrop={(e) => onDrop(e, 'source')}
        />
        <DropZone
          label="Target (YouTube Music)"
          value={target}
          onChange={setTarget}
          placeholder="YouTube Music playlist ID, or drop playlist JSON"
          active={false}
          onDragOver={() => undefined}
          onDragLeave={() => undefined}
          onDrop={(e) => onDrop(e, 'target')}
        />
      </div>

      {/* Options */}
      <div className="reconcile-options">
        <label className="checkbox">
          <input type="checkbox" checked={cleanExtra} onChange={(e) => setCleanExtra(e.target.checked)} />
          <span>
            Clean extra tracks in target <strong className="danger-text">(destructive)</strong>
          </span>
        </label>
        <label className="checkbox">
          <input type="checkbox" checked={syncOrder} onChange={(e) => setSyncOrder(e.target.checked)} />
          <span>Sync order (monotonic)</span>
        </label>
        <label className="concurrency-row">
          <span>Concurrency</span>
          <input
            type="number"
            min={1}
            max={16}
            value={concurrency}
            onChange={(e) => setConcurrency(Math.max(1, Math.min(16, Number(e.target.value) || 4)))}
            className="ps-tabular"
          />
        </label>
        <button type="button" className="btn btn-primary" onClick={submitStart} disabled={busy !== null || !source.trim() || !target.trim()}>
          {busy === 'scanning' ? 'Scanning…' : 'Start Reconcile'}
        </button>
      </div>

      {/* Diff triad */}
      <div className="diff-triad" data-testid="diff-triad">
        {summaries.map((s) => (
          <div key={s.key} className={`triad-cell ${s.cls}`}>
            <span className="triad-num ps-tabular">{s.n}</span>
            <span className="triad-label">{s.label}</span>
          </div>
        ))}
        <div className="triad-cell sum-total">
          <span className="triad-num ps-tabular">{total}</span>
          <span className="triad-label">Total</span>
        </div>
      </div>

      {diff && diff.added.length > 0 && (
        <details className="diff-preview">
          <summary>
            Added tracks preview ({diff.added.length})
          </summary>
          <ul className="diff-preview-list">
            {diff.added.slice(0, 20).map((t, i) => (
              <li key={i}>
                <span className="ps-tabular">{t.index + 1}</span> {t.title} — {t.artists.join(', ')} ({formatDurationString(t.duration)})
              </li>
            ))}
            {diff.added.length > 20 && <li className="diff-more">… and {diff.added.length - 20} more</li>}
          </ul>
        </details>
      )}

      {diff && (
        <div className="apply-row">
          <button type="button" className="btn btn-primary" onClick={() => void onApply(false)} disabled={busy !== null || !canApply}>
            {busy === 'applying' ? 'Applying…' : 'Apply Diff'}
          </button>
        </div>
      )}

      {/* Clean Extra high-risk confirmation modal (explicit unlock, cannot bypass) */}
      {confirmCleanOpen && (
        <div className="modal-backdrop" role="presentation" onClick={() => setConfirmCleanOpen(false)}>
          <div className="modal modal-danger" role="dialog" aria-modal="true" aria-labelledby="clean-extra-title" onClick={(e) => e.stopPropagation()}>
            <h3 id="clean-extra-title">
              <AlertTriangleIcon width={16} height={16} /> Clean Extra — Destructive Operation
            </h3>
            <p>
              This will <strong className="danger-text">remove {counts.removed} tracks</strong> from the target
              playlist that are absent from the source. This action cannot be undone.
            </p>
            <label className="unlock-row">
              <input
                type="checkbox"
                checked={cleanUnlocked}
                onChange={(e) => setCleanUnlocked(e.target.checked)}
                data-testid="clean-unlock"
              />
              I understand this is destructive and cannot be undone.
            </label>
            <div className="modal-actions">
              <button type="button" className="btn btn-ghost" onClick={() => setConfirmCleanOpen(false)}>
                Cancel
              </button>
              <button
                type="button"
                className="btn btn-danger"
                disabled={!cleanUnlocked}
                onClick={confirmClean}
                data-testid="confirm-clean"
              >
                {cleanUnlocked ? 'Proceed with clean' : 'Unlock to proceed'}
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}

interface DropZoneProps {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder: string;
  active: boolean;
  onDragOver: () => void;
  onDragLeave: () => void;
  onDrop: (e: DragEvent<HTMLDivElement>) => void;
}

function DropZone({ label, value, onChange, placeholder, active, onDragOver, onDragLeave, onDrop }: DropZoneProps) {
  return (
    <div className={`drop-zone ${active ? 'drag-active' : ''}`} onDragOver={(e) => { e.preventDefault(); onDragOver(); }} onDragLeave={onDragLeave} onDrop={onDrop}>
      <label>{label}</label>
      <input type="text" value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} />
    </div>
  );
}

function parseIdFromText(raw: string): string {
  const m = /(?:playlist\/|PL)[A-Za-z0-9_-]{10,}/.exec(raw);
  return m ? m[0] : raw.trim().slice(0, 200);
}

/** Structural type guard for playlist JSON: `{ id: string }`-ish record.
 * No `as` casts — Reflect.get + narrowing only (brief constraint). */
function isRecordWithId(value: unknown): value is { id: string } {
  if (typeof value !== 'object' || value === null) return false;
  const maybeId: unknown = Reflect.get(value, 'id');
  return typeof maybeId === 'string' && maybeId.length > 0;
}