/**
 * Component render/snapshot tests (plan Task-2 completion criteria):
 * DualColumnDiffRow / ArbitrationCard / InvariantGuardianBar.
 */
import { describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';
import { DualColumnDiffRow, VirtualDiffList } from '../components/DualColumnDiffRow';
import { ArbitrationCard } from '../components/ArbitrationCard';
import { InvariantGuardianBar } from '../components/InvariantGuardianBar';
import type { DiffRowModel } from '../components/DualColumnDiffRow';
import type { ArbitrationRequestEvent } from '../types/contracts';

const baseRow: DiffRowModel = {
  index: 0,
  sourceTitle: 'Bohemian Rhapsody',
  sourceArtist: 'Queen',
  sourceDurationMs: 354000,
  category: 'ADDED',
  isSkipped: false,
};

describe('DualColumnDiffRow', () => {
  it('renders a 44px row with source title, artist and category badge', () => {
    const { container } = render(
      <DualColumnDiffRow row={baseRow} isFocused={false} isExpanded={false} onToggleExpand={() => undefined} />,
    );
    expect(container.querySelector('.diff-row')).toBeInTheDocument();
    expect(container.querySelector('.diff-row')).toHaveStyle({ height: '44px' });
    expect(screen.getByText('Bohemian Rhapsody')).toBeInTheDocument();
    expect(screen.getByText('Queen')).toBeInTheDocument();
    expect(screen.getByText('ADDED')).toBeInTheDocument();
  });

  it('applies the focused / skipped modifiers', () => {
    const { container } = render(
      <DualColumnDiffRow
        row={{ ...baseRow, isSkipped: true, category: 'RETAINED' }}
        isFocused
        isExpanded={false}
        onToggleExpand={() => undefined}
      />,
    );
    const rowEl = container.querySelector('.diff-row');
    expect(rowEl).toHaveClass('is-focused');
    expect(rowEl).toHaveClass('is-skipped');
    expect(screen.getByText('RETAINED')).toBeInTheDocument();
    expect(screen.getByText('SKIPPED')).toBeInTheDocument();
  });

  it('VirtualDiffList renders only the visible window (10,000 rows -> <= 35 DOM rows)', () => {
    const rows: DiffRowModel[] = Array.from({ length: 10000 }, (_, i) => ({
      index: i,
      sourceTitle: `Track ${i}`,
      sourceArtist: `Artist ${i % 50}`,
      sourceDurationMs: 180000,
      category: i % 3 === 0 ? 'ADDED' : i % 3 === 1 ? 'REMOVED' : 'RETAINED',
      isSkipped: false,
    }));
    render(
      <VirtualDiffList rows={rows} focusedIndex={0} expandedIndex={null} onToggleExpand={() => undefined} viewportHeight={800} />,
    );
    const list = screen.getByTestId('virtual-list');
    const renderedRows = list.querySelectorAll('.diff-row');
    expect(renderedRows.length).toBeLessThanOrEqual(35);
    expect(renderedRows.length).toBeGreaterThan(0);
    // Window @ scrollTop 0 with a 800px viewport: rows 0..24
    // (ceil(800/44)=19 visible + 2*5 buffer) per spec 03 §3.3 Case 3.
    expect(renderedRows.length).toBe(24);
    expect(screen.getByText('Track 0')).toBeInTheDocument();
  });

  it('VirtualDiffList spacer equals 10000 * 44 px', () => {
    const rows: DiffRowModel[] = Array.from({ length: 10000 }, (_, i) => ({
      ...baseRow,
      index: i,
      sourceTitle: `T${i}`,
      category: 'RETAINED',
    }));
    render(<VirtualDiffList rows={rows} focusedIndex={0} expandedIndex={null} onToggleExpand={() => undefined} viewportHeight={800} />);
    const spacer = screen.getByTestId('virtual-list').querySelector('.virtual-list-spacer');
    expect(spacer).toHaveStyle({ height: '440000px' });
  });
});

describe('ArbitrationCard', () => {
  const request: ArbitrationRequestEvent = {
    track_id: 'spotify:track:ABC',
    source_title: 'Nights',
    source_artist: 'Frank Ocean',
    source_duration_ms: 307000,
    candidates: [
      { target_id: 'v1', title: 'Nights', artist: 'Frank Ocean', duration_ms: 307000, confidence_score: 0.97, title_score: 1, artist_score: 1, duration_score: 0.9, isrc_matched: true },
      { target_id: 'v2', title: 'Nights (Remix)', artist: 'Unknown', duration_ms: 240000, confidence_score: 0.62, title_score: 0.7, artist_score: 0.4, duration_score: 0.3, isrc_matched: false },
    ],
    created_at: new Date().toISOString(),
  };

  it('renders candidate list with confidence radar and numbered keys', () => {
    const onSelect = vi.fn();
    const onSkip = vi.fn();
    const onCustom = vi.fn();
    render(<ArbitrationCard request={request} onSelectCandidate={onSelect} onSkip={onSkip} onCustomId={onCustom} />);
    expect(screen.getByTestId('arbitration-card')).toBeInTheDocument();
    expect(screen.getByTestId('candidate-1')).toBeInTheDocument();
    expect(screen.getByTestId('candidate-2')).toBeInTheDocument();
    const list = screen.getAllByRole('img');
    expect(list.length).toBeGreaterThanOrEqual(2);
  });

  it('select candidate button invokes onSelectCandidate with candidate', () => {
    const onSelect = vi.fn();
    render(<ArbitrationCard request={request} onSelectCandidate={onSelect} onSkip={() => undefined} onCustomId={() => undefined} />);
    const selectButtons = screen.getAllByText('Select');
    expect(selectButtons[0]).toBeDefined();
    if (selectButtons[0]) fireEvent.click(selectButtons[0]);
    expect(onSelect).toHaveBeenCalledWith('spotify:track:ABC', request.candidates[0], 0.97);
  });

  it('skip button invokes onSkip', () => {
    const onSkip = vi.fn();
    render(<ArbitrationCard request={request} onSelectCandidate={() => undefined} onSkip={onSkip} onCustomId={() => undefined} />);
    fireEvent.click(screen.getByText(/Skip/));
    expect(onSkip).toHaveBeenCalledWith('spotify:track:ABC');
  });

  it('custom ID box accepts input and submits', () => {
    const onCustom = vi.fn();
    render(<ArbitrationCard request={request} onSelectCandidate={() => undefined} onSkip={() => undefined} onCustomId={onCustom} />);
    fireEvent.click(screen.getByText(/Custom ID/));
    const input = screen.getByTestId('custom-id-input');
    fireEvent.change(input, { target: { value: 'dQw4w9WgXcQ' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onCustom).toHaveBeenCalledWith('spotify:track:ABC', 'dQw4w9WgXcQ');
  });

  it('renders nothing when request is null', () => {
    const { container } = render(
      <ArbitrationCard request={null} onSelectCandidate={() => undefined} onSkip={() => undefined} onCustomId={() => undefined} />,
    );
    expect(container.firstChild).toBeNull();
  });
});

describe('InvariantGuardianBar', () => {
  const allPass = {
    countConserved: true,
    uniquenessValid: true,
    orderMonotonic: true,
    diffComplete: true,
    zeroTraceClean: true,
  };

  it('renders five invariant status items', () => {
    render(<InvariantGuardianBar invariants={allPass} onApply={() => undefined} isApplying={false} isApplyEnabled />);
    const bar = screen.getByTestId('invariant-guardian-bar');
    expect(within(bar).getByText('COUNT')).toBeInTheDocument();
    expect(within(bar).getByText('UNIQUE')).toBeInTheDocument();
    expect(within(bar).getByText('ORDER')).toBeInTheDocument();
    expect(within(bar).getByText('DIFF')).toBeInTheDocument();
    expect(within(bar).getByText('ZERO-TRACE')).toBeInTheDocument();
  });

  it('enables Apply only when all invariants pass', () => {
    render(<InvariantGuardianBar invariants={allPass} onApply={() => undefined} isApplying={false} isApplyEnabled />);
    const btn = screen.getByTestId('apply-button');
    expect(btn).not.toBeDisabled();
  });

  it('disables Apply and shows a violation note when any invariant fails', () => {
    render(
      <InvariantGuardianBar
        invariants={{ ...allPass, uniquenessValid: false }}
        onApply={() => undefined}
        isApplying={false}
        isApplyEnabled={false}
      />,
    );
    const btn = screen.getByTestId('apply-button');
    expect(btn).toBeDisabled();
    expect(screen.getByText(/Invariant violation/)).toBeInTheDocument();
  });

  it('renders unknown state when invariants are null (no snapshot yet)', () => {
    const { container } = render(
      <InvariantGuardianBar invariants={null} onApply={() => undefined} isApplying={false} isApplyEnabled={false} />,
    );
    expect(container.querySelector('.inv-unknown')).not.toBeNull();
    expect(screen.getByTestId('apply-button')).toBeDisabled();
  });
});