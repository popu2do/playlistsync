/**
 * InvariantGuardianBar — 5 大不变量实时状态条 (spec 01 §2, spec 03 §4.2):
 * SSE 驱动 (INVARIANT_SNAPSHOT events), 任一不变量失败 -> 禁用 Apply。
 * 常驻页面底部 36px (spec 03 §4.1: 36px Fixed Bottom)。
 */
import { ShieldCheckIcon, XCircleIcon } from './icons';

export interface InvariantFlags {
  readonly countConserved: boolean;
  readonly uniquenessValid: boolean;
  readonly orderMonotonic: boolean;
  readonly diffComplete: boolean;
  readonly zeroTraceClean: boolean;
}

export interface InvariantGuardianBarProps {
  readonly invariants: InvariantFlags | null;
  readonly onApply: () => void;
  readonly isApplying: boolean;
  readonly isApplyEnabled: boolean;
}

const INVARIANT_DEFS: readonly { key: keyof InvariantFlags; label: string }[] = [
  { key: 'countConserved', label: 'COUNT' },
  { key: 'uniquenessValid', label: 'UNIQUE' },
  { key: 'orderMonotonic', label: 'ORDER' },
  { key: 'diffComplete', label: 'DIFF' },
  { key: 'zeroTraceClean', label: 'ZERO-TRACE' },
];

export function InvariantGuardianBar({
  invariants,
  onApply,
  isApplying,
  isApplyEnabled,
}: InvariantGuardianBarProps) {
  const flags = invariants;
  const allPass = flags !== null && INVARIANT_DEFS.every((d) => flags[d.key] === true);
  const anyKnown = flags !== null;

  return (
    <footer className="guardian-bar" data-testid="invariant-guardian-bar" aria-label="Invariant guardian">
      <div className="guardian-invariants">
        {INVARIANT_DEFS.map((def) => {
          const ok = flags ? flags[def.key] === true : null;
          const cls = ok === null ? 'inv-unknown' : ok ? 'inv-ok' : 'inv-fail';
          const Icon = ok === false ? XCircleIcon : ShieldCheckIcon;
          return (
            <span key={def.key} className={`inv-item ${cls}`} data-invariant={def.key} title={def.label}>
              <Icon width={13} height={13} />
              {def.label}
            </span>
          );
        })}
      </div>
      <div className="guardian-apply">
        {anyKnown && !allPass && (
          <span className="guardian-block-note">Invariant violation — Apply disabled</span>
        )}
        <button
          type="button"
          className="btn btn-primary"
          disabled={isApplying || !isApplyEnabled || !allPass}
          onClick={onApply}
          data-testid="apply-button"
          aria-disabled={!allPass}
        >
          {isApplying ? 'Applying…' : 'Apply'}
        </button>
      </div>
    </footer>
  );
}