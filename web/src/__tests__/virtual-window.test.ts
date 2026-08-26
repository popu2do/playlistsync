/**
 * computeVirtualWindow TDD verification suite — spec 03 §3.3 verbatim cases.
 */
import { describe, expect, it } from 'vitest';
import { computeVirtualWindow } from '../utils/virtual-window';

describe('computeVirtualWindow TDD Verification Suite', () => {
  it('Case 1: 空歌单边界 (itemCount = 0)', () => {
    const res = computeVirtualWindow(0, 800, 0, 44, 5);
    expect(res).toEqual({ totalHeight: 0, startIndex: 0, endIndex: 0, offsetY: 0, visibleCount: 0 });
  });

  it('Case 2: 极少数据 (itemCount = 3, 远小于视窗容纳数)', () => {
    const res = computeVirtualWindow(3, 800, 0, 44, 5);
    expect(res.totalHeight).toBe(132);
    expect(res.startIndex).toBe(0);
    expect(res.endIndex).toBe(3);
    expect(res.offsetY).toBe(0);
    expect(res.visibleCount).toBe(3);
  });

  it('Case 3: 视窗顶部首屏 (scrollTop = 0, itemCount = 1000)', () => {
    const res = computeVirtualWindow(1000, 800, 0, 44, 5);
    expect(res.startIndex).toBe(0);
    expect(res.endIndex).toBe(24);
    expect(res.offsetY).toBe(0);
    expect(res.visibleCount).toBe(24);
    expect(res.totalHeight).toBe(44000);
  });

  it('Case 4: 视窗中部滚动 (scrollTop = 2200, row 50)', () => {
    const res = computeVirtualWindow(1000, 800, 2200, 44, 5);
    expect(res.startIndex).toBe(45);
    expect(res.endIndex).toBe(74);
    expect(res.offsetY).toBe(45 * 44);
    expect(res.visibleCount).toBe(29);
  });

  it('Case 5: 视窗触底与超限保护 (scrollTop 达到极限)', () => {
    const itemCount = 100;
    const totalHeight = 4400;
    // scrollTop beyond the content: clamps to maxScroll = totalHeight - viewport
    // = 3600 -> row 81 -> start 76, end 100, visible 24. The window must never
    // render zero rows nor exceed the item range (spec 03 §3.2 超限保护).
    const res = computeVirtualWindow(itemCount, 800, 99999, 44, 5);
    expect(res.totalHeight).toBe(totalHeight);
    expect(res.endIndex).toBe(itemCount);
    expect(res.startIndex).toBe(76);
    expect(res.offsetY).toBe(76 * 44);
    expect(res.visibleCount).toBe(24);
  });

  it('Case 6: 10,000 首曲目 60FPS 契约 — 常驻 DOM 节点远小于 45', () => {
    const res = computeVirtualWindow(10000, 800, 44000, 44, 5);
    // scrollTop 44000 -> row 1000
    expect(res.totalHeight).toBe(440000);
    expect(res.visibleCount).toBeLessThanOrEqual(35);
    expect(res.visibleCount).toBeGreaterThanOrEqual(19);
  });

  it('negative rowHeight guard', () => {
    const res = computeVirtualWindow(10, 800, 0, -44, 5);
    expect(res.totalHeight).toBe(0);
    expect(res.visibleCount).toBe(0);
  });
});