/**
 * ReportArchive — 审计报告库 (spec 01 §2 / spec 04 §1.5): 历史报告树 +
 * JSON / Markdown 导出下载。
 */
import { useState } from 'react';
import type { ApiClient } from '../api/client';
import type { ReportMeta } from '../types/contracts';
import { formatBytes, formatTimestamp } from '../utils/format';
import { ChevronDownIcon, RefreshCwIcon } from '../components/icons';

export interface ReportArchiveProps {
  readonly client: ApiClient;
  readonly reports: readonly ReportMeta[];
  readonly onReload: () => Promise<void>;
}

export function ReportArchive({ client, reports, onReload }: ReportArchiveProps) {
  const [busy, setBusy] = useState(false);

  async function reload(): Promise<void> {
    setBusy(true);
    try {
      await onReload();
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="view-panel report-archive" data-testid="report-archive" aria-label="Report archive">
      <header className="panel-header">
        <h2>Report Archive</h2>
        <button type="button" className="btn btn-ghost btn-sm" onClick={() => void reload()} disabled={busy}>
          <RefreshCwIcon width={12} height={12} /> Reload
        </button>
      </header>

      {reports.length === 0 ? (
        <div className="inspector-empty">No audit reports yet — apply a diff to generate one.</div>
      ) : (
        <ul className="report-tree">
          {reports.map((r) => (
            <li key={r.id} className="report-node">
              <div className="report-row">
                <ChevronDownIcon width={12} height={12} className="report-chevron" />
                <span className="report-id ps-tabular">{r.id}</span>
                <span className="report-meta ps-tabular">
                  {formatTimestamp(r.modified_at)} · {formatBytes(r.size)}
                </span>
                <span className="report-actions">
                  <a
                    className="btn btn-ghost btn-sm"
                    href={client.reportExportURL(r.id, 'json')}
                    download={`${r.id}_report.json`}
                  >
                    JSON
                  </a>
                  <a
                    className="btn btn-ghost btn-sm"
                    href={client.reportExportURL(r.id, 'markdown')}
                    download={`${r.id}_report.md`}
                  >
                    Markdown
                  </a>
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}