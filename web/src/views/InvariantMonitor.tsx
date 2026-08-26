/**
 * InvariantMonitor — 不变量体检仪 (spec 01 §2 / spec 04 §1.4): 5 大不变量
 * 实时体检看板 + LIS 序列偏离热力图 + ID 碰撞雷达。
 */
import { useState } from 'react';
import type { ApiClient } from '../api/client';
import type { InvariantSnapshot } from '../types/contracts';
import { formatTimestamp, formatPercent } from '../utils/format';
import { ShieldCheckIcon, XCircleIcon, AlertTriangleIcon } from '../components/icons';

export interface InvariantMonitorProps {
  readonly client: ApiClient;
  readonly snapshot: InvariantSnapshot | null;
  readonly verifyFor: string;
  readonly onVerify: (targetId: string) => Promise<void>;
}

const INVARIANT_ROWS: readonly { key: keyof Pick<InvariantSnapshot, 'is_count_conserved' | 'has_duplicate_target_ids' | 'is_diff_complete' | 'is_zero_trace_clean'>; label: string; okWhen: boolean }[] = [
  { key: 'is_count_conserved', label: 'Count: Total = Synced + Skipped + Failed', okWhen: true },
  { key: 'has_duplicate_target_ids', label: 'Uniqueness: no duplicate target Track IDs', okWhen: false },
  { key: 'is_diff_complete', label: 'Diff: Added ∪ Removed ∪ Retained partitions', okWhen: true },
  { key: 'is_zero_trace_clean', label: 'Zero-Trace: no residue / plaintext', okWhen: true },
];

export function InvariantMonitor({ client, snapshot, verifyFor, onVerify }: InvariantMonitorProps) {
  void client;
  const [targetId, setTargetId] = useState(verifyFor);
  const [busy, setBusy] = useState(false);

  const orderOk = snapshot === null || (snapshot.disordered_indices?.length ?? 0) === 0;

  async function runVerify(): Promise<void> {
    setBusy(true);
    try {
      await onVerify(targetId.trim());
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="view-panel invariant-monitor" data-testid="invariant-monitor" aria-label="Invariant monitor">
      <header className="panel-header">
        <h2>
          <ShieldCheckIcon width={14} height={14} /> Invariant Monitor
        </h2>
        <div className="verify-controls">
          <input type="text" value={targetId} onChange={(e) => setTargetId(e.target.value)} placeholder="Target playlist ID" />
          <button type="button" className="btn btn-primary btn-sm" onClick={() => void runVerify()} disabled={busy || !targetId.trim()}>
            {busy ? 'Verifying…' : 'Verify invariants'}
          </button>
        </div>
      </header>

      {snapshot ? (
        <>
          <div className="inv-dashboard">
            <div className="inv-formula ps-tabular">
              <span className="formula-n">{snapshot.source_total}</span> ={' '}
              <span className="formula-n ok">{snapshot.synced_count}</span> +{' '}
              <span className="formula-n" data-role="skipped">{snapshot.skipped_count}</span> +{' '}
              <span className="formula-n" data-role="failed">{snapshot.failed_count}</span>
              <span className={`formula-verdict ${snapshot.is_count_conserved ? 'ok' : 'fail'}`}>
                {snapshot.is_count_conserved ? 'CONSERVED' : 'VIOLATION'}
              </span>
            </div>

            <div className="inv-radar" data-testid="inv-radar" aria-label="Invariant radar">
              {INVARIANT_ROWS.map((row) => {
                const raw = snapshot[row.key];
                const ok = row.okWhen ? raw === true : raw === false;
                return (
                  <div key={row.key} className={`inv-radar-item ${ok ? 'ok' : 'fail'}`}>
                    {ok ? <ShieldCheckIcon width={13} height={13} /> : <XCircleIcon width={13} height={13} />}
                    {row.label}
                  </div>
                );
              })}
              <div className={`inv-radar-item ${orderOk ? 'ok' : 'fail'}`}>
                {orderOk ? <ShieldCheckIcon width={13} height={13} /> : <XCircleIcon width={13} height={13} />}
                Order: LIS monotonic ({formatPercent(1 - snapshot.lis_disorder_ratio)} aligned)
              </div>
            </div>

            {/* LIS disorder heatmap */}
            {snapshot.disordered_indices.length > 0 && (
              <div className="lis-heatmap" data-testid="lis-heatmap">
                <h4>LIS disorder heatmap — {snapshot.disordered_indices.length} displaced positions</h4>
                <div className="heatmap-row">
                  {snapshot.disordered_indices.slice(0, 120).map((idx, i) => (
                    <span key={`${idx}-${i}`} className="heat-cell" title={`index ${idx}`} />
                  ))}
                  {snapshot.disordered_indices.length > 120 && (
                    <span className="heat-more">+{snapshot.disordered_indices.length - 120}</span>
                  )}
                </div>
              </div>
            )}

            {/* ID collision radar */}
            <div className="collision-radar" data-testid="collision-radar">
              <h4>Target ID collision radar</h4>
              {snapshot.duplicate_target_ids.length === 0 ? (
                <span className="collision-clean">
                  <ShieldCheckIcon width={13} height={13} /> No duplicate target IDs
                </span>
              ) : (
                <ul className="collision-list">
                  {snapshot.duplicate_target_ids.slice(0, 20).map((id) => (
                    <li key={id} className="ps-tabular">
                      <AlertTriangleIcon width={11} height={11} /> {id}
                    </li>
                  ))}
                </ul>
              )}
            </div>

            <div className="inv-footer ps-tabular">
              evaluated {formatTimestamp(snapshot.evaluated_at)} · all_passed:{' '}
              <strong className={snapshot.all_passed ? 'ok' : 'fail'}>{String(snapshot.all_passed)}</strong>
            </div>
          </div>
        </>
      ) : (
        <div className="inspector-empty">
          No invariant snapshot yet — run a reconcile + verify to populate the radar, or verify a target ID directly.
        </div>
      )}
    </section>
  );
}