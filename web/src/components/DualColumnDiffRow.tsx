/**
 * DualColumnDiffRow — the 44px fixed-height diff row (spec 03 §4.1) plus the
 * windowed virtual list that renders it (spec 03 §3): only the visible window
 * (+buffer) is mounted, so a 10,000-track diff keeps the DOM near-constant
 * and holds 60 FPS scrolling. The list container uses translate3d and
 * will-change: transform per spec 03 §6 (GPU compositing, zero reflow).
 */
import { memo, useCallback, useRef, useState } from 'react';
import { computeVirtualWindow } from '../utils/virtual-window';
import { formatMs } from '../utils/format';

export type DiffCategory = 'ADDED' | 'REMOVED' | 'RETAINED' | 'ARBITRATION';

export interface DiffRowModel {
  readonly index: number;
  readonly sourceTitle: string;
  readonly sourceArtist: string;
  readonly sourceDurationMs: number;
  readonly category: DiffCategory;
  readonly isSkipped: boolean;
  readonly selectedCandidateId?: string;
  readonly customTargetId?: string;
}

export interface DualColumnDiffRowProps {
  readonly row: DiffRowModel;
  readonly isFocused: boolean;
  readonly isExpanded: boolean;
  readonly onToggleExpand: (index: number) => void;
}

export const ROW_HEIGHT = 44;

/** One diff row: fixed 44px height, two columns + status glyph. */
export function DualColumnDiffRow({
  row,
  isFocused,
  isExpanded,
  onToggleExpand,
}: DualColumnDiffRowProps) {
  const categoryClass =
    row.category === 'ADDED'
      ? 'cat-added'
      : row.category === 'REMOVED'
        ? 'cat-removed'
        : row.category === 'ARBITRATION'
          ? 'cat-arbitration'
          : 'cat-retained';

  const statusLabel =
    row.category === 'ADDED'
      ? 'ADDED'
      : row.category === 'REMOVED'
        ? 'REMOVED'
        : row.category === 'ARBITRATION'
          ? 'ARBITRATION'
          : 'RETAINED';

  return (
    <div
      className={[
        'diff-row',
        categoryClass,
        isFocused ? 'is-focused' : '',
        isExpanded ? 'is-expanded' : '',
        row.isSkipped ? 'is-skipped' : '',
      ]
        .filter(Boolean)
        .join(' ')}
      style={{ height: ROW_HEIGHT }}
      role="row"
      aria-selected={isFocused}
      data-index={row.index}
      onClick={() => onToggleExpand(row.index)}
    >
      <div className="diff-row-index ps-tabular">{row.index + 1}</div>
      <div className="diff-col source-col">
        <span className="diff-title" title={row.sourceTitle}>
          {row.sourceTitle}
        </span>
        <span className="diff-artist">{row.sourceArtist}</span>
        <span className="diff-duration ps-tabular">{formatMs(row.sourceDurationMs)}</span>
      </div>
      <div className="diff-col target-col">
        {row.isSkipped ? (
          <span className="diff-skip-tag">SKIPPED</span>
        ) : row.selectedCandidateId ? (
          <span className="diff-target-id ps-tabular" title={row.selectedCandidateId}>
            {row.selectedCandidateId}
          </span>
        ) : (
          <span className="diff-target-empty">—</span>
        )}
      </div>
      <div className="diff-badge">
        <span className={`cat-badge ${categoryClass}`}>{statusLabel}</span>
        {row.category === 'ARBITRATION' && (
          <span className="cat-kbd" aria-label={`candidates: ${row.sourceArtist}`}>
            ⚖
          </span>
        )}
      </div>
    </div>
  );
}

export const MemoDualColumnDiffRow = memo(DualColumnDiffRow);

export interface VirtualDiffListProps {
  readonly rows: readonly DiffRowModel[];
  readonly focusedIndex: number;
  readonly expandedIndex: number | null;
  readonly onToggleExpand: (index: number) => void;
  readonly viewportHeight?: number;
}

/**
 * Windowed virtual list over 44px rows. Only rows in [startIndex, endIndex)
 * are rendered inside a translated container; an absolutely-positioned spacer
 * provides the full scroll height.
 */
export function VirtualDiffList({
  rows,
  focusedIndex,
  expandedIndex,
  onToggleExpand,
  viewportHeight = 400,
}: VirtualDiffListProps) {
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const [scrollTop, setScrollTop] = useState(0);

  const onScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    // Throttle via requestAnimationFrame: one state write per frame max.
    const top = el.scrollTop;
    if (top !== scrollTop) {
      window.requestAnimationFrame(() => setScrollTop(el.scrollTop));
    }
  }, [scrollTop]);

  const win = computeVirtualWindow(rows.length, viewportHeight, scrollTop, ROW_HEIGHT, 5);
  const visibleRows = rows.slice(win.startIndex, win.endIndex);

  if (rows.length === 0) {
    return <div className="virtual-list-empty">No diff rows yet.</div>;
  }

  return (
    <div
      className="virtual-list"
      role="rowgroup"
      style={{ height: viewportHeight, overflowY: 'auto', position: 'relative' }}
      ref={(el) => {
        scrollRef.current = el;
      }}
      onScroll={onScroll}
      data-testid="virtual-list"
    >
      <div
        className="virtual-list-spacer"
        style={{ height: win.totalHeight, position: 'relative' }}
      >
        <div
          className="virtual-list-window"
          style={{
            transform: `translate3d(0, ${win.offsetY}px, 0)`,
            willChange: 'transform',
            position: 'absolute',
            top: 0,
            left: 0,
            right: 0,
          }}
        >
          {visibleRows.map((row, i) => {
            const absIndex = win.startIndex + i;
            return (
              <MemoDualColumnDiffRow
                key={absIndex}
                row={row}
                isFocused={absIndex === focusedIndex}
                isExpanded={absIndex === expandedIndex}
                onToggleExpand={onToggleExpand}
              />
            );
          })}
        </div>
      </div>
    </div>
  );
}