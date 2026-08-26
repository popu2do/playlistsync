/**
 * PlaylistInspector — 歌单透视仪 (spec 01 §2 / spec 04 §1.3): 元数据全景
 * (总数/时长/流派或艺术家分布) + 未匹配曲目下钻 (Skipped/Failed)。
 */
import { useMemo, useState } from 'react';
import type { ApiClient } from '../api/client';
import type { PlaylistInspectResponse } from '../types/contracts';
import { formatPercent } from '../utils/format';
import { SearchIcon } from '../components/icons';

export interface PlaylistInspectorProps {
  readonly client: ApiClient;
  readonly data: PlaylistInspectResponse | null;
  readonly onInspect: (id: string, platform: 'spotify' | 'ytmusic') => Promise<void>;
}

export function PlaylistInspector({ client, data, onInspect }: PlaylistInspectorProps) {
  void client;
  const [id, setId] = useState('');
  const [platform, setPlatform] = useState<'spotify' | 'ytmusic'>('spotify');
  const [query, setQuery] = useState('');
  const [busy, setBusy] = useState(false);

  const tracks = data?.tracks ?? [];
  const matched = tracks.filter((t) => t.match_state === 'retained' || t.match_state === 'added');
  const unmatched = tracks.filter((t) => t.match_state === 'skipped' || t.match_state === 'removed' || t.match_state === 'unknown');
  const matchRate = tracks.length > 0 ? matched.length / tracks.length : 0;

  const artistDist = useMemo(() => {
    const counts = new Map<string, number>();
    for (const t of tracks) {
      for (const a of t.artists) {
        counts.set(a, (counts.get(a) ?? 0) + 1);
      }
    }
    return [...counts.entries()].sort((a, b) => b[1] - a[1]).slice(0, 8);
  }, [tracks]);

  const filteredUnmatched = unmatched.filter(
    (t) => t.title.toLowerCase().includes(query.toLowerCase()) || t.artists.some((a) => a.toLowerCase().includes(query.toLowerCase())),
  );

  async function runInspect(): Promise<void> {
    setBusy(true);
    try {
      await onInspect(id.trim(), platform);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="view-panel inspector" data-testid="playlist-inspector" aria-label="Playlist inspector">
      <header className="panel-header">
        <h2>
          <SearchIcon width={14} height={14} /> Playlist Inspector
        </h2>
      </header>

      <div className="inspect-controls">
        <select
          value={platform}
          onChange={(e) => { const v = e.target.value; setPlatform(v === 'ytmusic' ? 'ytmusic' : 'spotify'); }}
          aria-label="Platform"
        >
          <option value="spotify">Spotify</option>
          <option value="ytmusic">YouTube Music</option>
        </select>
        <input type="text" value={id} onChange={(e) => setId(e.target.value)} placeholder="Playlist ID" />
        <button type="button" className="btn btn-primary" onClick={() => void runInspect()} disabled={busy || !id.trim()}>
          Inspect
        </button>
      </div>

      {data ? (
        <>
          <div className="inspector-meta">
            <div className="meta-cell">
              <span className="meta-value ps-tabular">{data.title}</span>
              <span className="meta-label">Title</span>
            </div>
            <div className="meta-cell">
              <span className="meta-value ps-tabular">{data.track_count}</span>
              <span className="meta-label">Tracks</span>
            </div>
            <div className="meta-cell">
              <span className="meta-value ps-tabular">{tracks.length > 0 ? formatPercent(matchRate) : '—'}</span>
              <span className="meta-label">Match rate</span>
            </div>
            <div className="meta-cell">
              <span className="meta-value ps-tabular">{unmatched.length}</span>
              <span className="meta-label">Unmatched</span>
            </div>
          </div>

          <div className="inspector-split">
            <div className="inspector-pane">
              <h4>Artist distribution</h4>
              <ul className="dist-list">
                {artistDist.map(([name, n]) => (
                  <li key={name}>
                    <span className="dist-name">{name}</span>
                    <span className="dist-bar-wrap">
                      <span
                        className="dist-bar"
                        style={{ width: `${(n / (artistDist[0]?.[1] ?? 1)) * 100}%` }}
                      />
                    </span>
                    <span className="dist-n ps-tabular">{n}</span>
                  </li>
                ))}
                {artistDist.length === 0 && <li className="dist-empty">No data</li>}
              </ul>
            </div>
            <div className="inspector-pane">
              <h4>Unmatched tracks drill-down</h4>
              <input
                type="text"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Filter unmatched…"
                className="filter-input"
              />
              <table className="data-table">
                <thead>
                  <tr>
                    <th>#</th>
                    <th>Title</th>
                    <th>Artists</th>
                    <th>State</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredUnmatched.slice(0, 50).map((t) => (
                    <tr key={t.index}>
                      <td className="ps-tabular">{t.index}</td>
                      <td>{t.title}</td>
                      <td>{t.artists.join(', ')}</td>
                      <td>
                        <span className={`match-state st-${t.match_state}`}>{t.match_state}</span>
                      </td>
                    </tr>
                  ))}
                  {filteredUnmatched.length === 0 && (
                    <tr>
                      <td colSpan={4} className="empty-cell">
                        No unmatched tracks — everything is matched.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </>
      ) : (
        <div className="inspector-empty">Enter a playlist ID to inspect its metadata and match states.</div>
      )}
    </section>
  );
}