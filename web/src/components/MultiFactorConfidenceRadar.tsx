/**
 * Multi-factor confidence radar (spec 01 §2 / spec 03 §4.2): a 4-axis radar
 * over Title / Artist / Duration / (ISRC implicit) scores. Pure SVG — no
 * chart library, zero-CDN, CSP-safe.
 */
export interface ConfidenceBreakdown {
  readonly titleScore: number;
  readonly artistScore: number;
  readonly durationScore: number;
  readonly totalScore: number;
  readonly isrcMatched: boolean;
}

export interface MultiFactorConfidenceRadarProps {
  readonly breakdown: ConfidenceBreakdown;
  readonly size?: number;
}

interface Axis {
  readonly label: string;
  readonly value: number; // 0..1
}

export function ConfidenceRadar({ breakdown, size = 120 }: MultiFactorConfidenceRadarProps) {
  const axes: readonly Axis[] = [
    { label: 'Title', value: breakdown.titleScore },
    { label: 'Artist', value: breakdown.artistScore },
    { label: 'Duration', value: breakdown.durationScore },
    { label: 'ISRC', value: breakdown.isrcMatched ? 1 : 0 },
  ];

  const cx = size / 2;
  const cy = size / 2;
  const radius = size / 2 - 18;
  const n = axes.length;

  const point = (i: number, value: number): [number, number] => {
    const angle = (Math.PI * 2 * i) / n - Math.PI / 2;
    const r = radius * Math.max(0, Math.min(1, value));
    return [cx + r * Math.cos(angle), cy + r * Math.sin(angle)];
  };

  const polygon = (value: number): string =>
    axes.map((_, i) => point(i, value).join(',')).join(' ');

  const dataPolygon = axes.map((a, i) => point(i, a.value).join(',')).join(' ');

  const gridLevels = [0.25, 0.5, 0.75, 1];

  return (
    <svg
      className="conf-radar"
      width={size}
      height={size}
      viewBox={`0 0 ${size} ${size}`}
      role="img"
      aria-label={`confidence radar total ${(breakdown.totalScore * 100).toFixed(0)}%`}
    >
      {gridLevels.map((lv) => (
        <polygon key={lv} points={polygon(lv)} className="radar-grid" />
      ))}
      {axes.map((a, i) => {
        const [x, y] = point(i, 1);
        return (
          <g key={a.label}>
            <line x1={cx} y1={cy} x2={x} y2={y} className="radar-axis" />
          </g>
        );
      })}
      <polygon points={dataPolygon} className="radar-data" />
      {axes.map((a, i) => {
        const [x, y] = point(i, a.value);
        return <circle key={a.label} cx={x} cy={y} r={2.5} className="radar-dot" />;
      })}
      {axes.map((a, i) => {
        const [x, y] = point(i, 1.18);
        return (
          <text key={a.label} x={x} y={y} textAnchor="middle" dominantBaseline="middle" className="radar-label">
            {a.label}
          </text>
        );
      })}
      <text x={cx} y={cy} textAnchor="middle" dominantBaseline="middle" className="radar-total ps-tabular">
        {(breakdown.totalScore * 100).toFixed(0)}
      </text>
    </svg>
  );
}