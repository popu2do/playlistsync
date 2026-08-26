/**
 * 44px fixed-height virtual window algorithm — spec 03 §3 verbatim.
 *
 * Pure function: given the item count, viewport height and scroll offset,
 * returns the render window (start/end indices) plus the translateY offset
 * and total placeholder height. Rendering only this window keeps the DOM
 * node count near-constant (<= 35 for a 800px viewport) regardless of a
 * 10,000+ track playlist, holding 60 FPS scrolling (spec 03 §3.3 Case 6).
 */
export interface VirtualWindowResult {
  /** Total placeholder height of the scroll content (px). */
  readonly totalHeight: number;
  /** First rendered row index (includes the top buffer). */
  readonly startIndex: number;
  /** One-past-last rendered row index (includes the bottom buffer). */
  readonly endIndex: number;
  /** translateY offset for the render container (px). */
  readonly offsetY: number;
  /** Number of rows rendered in this window. */
  readonly visibleCount: number;
}

export function computeVirtualWindow(
  itemCount: number,
  viewportHeight: number,
  scrollTop: number,
  rowHeight: number = 44,
  bufferCount: number = 5,
): VirtualWindowResult {
  if (itemCount <= 0 || viewportHeight <= 0 || rowHeight <= 0) {
    return {
      totalHeight: 0,
      startIndex: 0,
      endIndex: 0,
      offsetY: 0,
      visibleCount: 0,
    };
  }

  const totalHeight = itemCount * rowHeight;

  // Clamp scrollTop to the maximum valid scroll position (a real scroll
  // container never reports a larger value; this keeps the pure function
  // total even for hostile inputs, spec 03 §3.2 超限保护).
  const maxScrollTop = Math.max(0, totalHeight - viewportHeight);
  const safeScrollTop = Math.min(Math.max(0, scrollTop), maxScrollTop);

  const rawStartIndex = Math.floor(safeScrollTop / rowHeight);
  const visibleRowsInViewport = Math.ceil(viewportHeight / rowHeight);
  const rawEndIndex = rawStartIndex + visibleRowsInViewport;

  const startIndex = Math.max(0, rawStartIndex - bufferCount);
  const endIndex = Math.min(itemCount, rawEndIndex + bufferCount);

  const offsetY = startIndex * rowHeight;
  const visibleCount = Math.max(0, endIndex - startIndex);

  return { totalHeight, startIndex, endIndex, offsetY, visibleCount };
}