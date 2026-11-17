/* Sparkline / area chart — an inline SVG, no charting library.
 *
 * Why: recharts is a full rendering stack (its own animation engine, tooltip
 * portal, default palette and font). For the one thing this console plots — a
 * series of per-turn context sizes — it was ~200 KB of JS to draw a polyline,
 * and its tooltip had to be restyled anyway. The rules that matter are drawn
 * directly here: the accent colour, the fill gradient, and the drop line that
 * marks a compaction.
 *
 * Nothing is animated and nothing re-lays-out on resize (the SVG scales with
 * `preserveAspectRatio`), so a long task's budget panel stays cheap.
 */
import { useId, useMemo, useState } from "react";

/* Points → path. Equal x spacing: the series is "one sample per turn", and the
 * turn index is the x axis, not a timestamp. */
function geometry(values, w, h, pad) {
  const max = Math.max(...values, 1);
  const innerW = Math.max(w - pad * 2, 1);
  const innerH = Math.max(h - pad * 2, 1);
  const step = values.length > 1 ? innerW / (values.length - 1) : 0;
  return values.map((v, i) => ({
    x: pad + step * i,
    y: pad + innerH - (v / max) * innerH,
    v,
  }));
}

export default function Sparkline({
  values = [],
  height = 64,
  width = 320,
  label = "每轮输入 token",
  // 压缩点：某轮输入比上一轮明显回落（>=20%）。由后端 budget 接口给出，
  // 这里只负责画竖虚线，不做推断。
  drops = [],
}) {
  const uid = useId().replace(/:/g, "");
  const [hover, setHover] = useState(null);
  const pad = 4;

  const pts = useMemo(() => geometry(values, width, height, pad), [values, width, height]);
  const max = Math.max(...values, 1);

  if (!values.length) {
    return <div className="spark-empty">本轮尚无用量记录。</div>;
  }

  const line = pts.map((p, i) => `${i === 0 ? "M" : "L"}${p.x.toFixed(2)},${p.y.toFixed(2)}`).join(" ");
  const area = `${line} L${pts[pts.length - 1].x.toFixed(2)},${(height - pad).toFixed(2)} L${pts[0].x.toFixed(2)},${(height - pad).toFixed(2)} Z`;
  const dropSet = new Set(drops);

  return (
    <div className="spark">
      <svg viewBox={`0 0 ${width} ${height}`} width="100%" height={height}
           preserveAspectRatio="none" role="img"
           aria-label={`${label}：${values.length} 个点，峰值 ${max}`}
           onMouseLeave={() => setHover(null)}>
        <defs>
          <linearGradient id={`fill-${uid}`} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--c-accent)" stopOpacity="0.26" />
            <stop offset="100%" stopColor="var(--c-accent)" stopOpacity="0" />
          </linearGradient>
        </defs>
        <path d={area} fill={`url(#fill-${uid})`} />
        <path d={line} fill="none" stroke="var(--c-accent)" strokeWidth="1.5"
              strokeLinejoin="round" strokeLinecap="round" />
        {pts.map((p, i) => (
          dropSet.has(i) ? (
            <line key={"d" + i} x1={p.x} y1={pad} x2={p.x} y2={height - pad}
                  stroke="var(--c-ink-4)" strokeWidth="1" strokeDasharray="3 3" />
          ) : null
        ))}
        {pts.map((p, i) => (
          <circle key={"c" + i} cx={p.x} cy={p.y} r={hover?.i === i ? 3 : 1.6}
                  fill="var(--c-accent)" opacity={hover?.i === i ? 1 : 0.55} />
        ))}
        {/* Invisible hit bands: one per point, so hovering works on a 1px line. */}
        {pts.map((p, i) => {
          const half = pts.length > 1 ? (pts[1].x - pts[0].x) / 2 : width / 2;
          return (
            <rect key={"h" + i} x={Math.max(p.x - half, 0)} y={0}
                  width={Math.max(half * 2, 2)} height={height}
                  fill="transparent"
                  onMouseEnter={() => setHover({ i, ...p })} />
          );
        })}
      </svg>
      <div className="spark-readout">
        {hover ? `第 ${hover.i + 1} 轮 · ${hover.v.toLocaleString()} tokens` : `${values.length} 轮 · 峰值 ${max.toLocaleString()}`}
      </div>
    </div>
  );
}