import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { authedFetch } from "./lib/auth.js";

/* 用量热力图 —— 移植自 dsh-claude-style 的首页热力格（MIT，见
 * `packages/client/src/features/home/home-figures.ts` 与 `home-overview.css`）。
 *
 * 几何：一格一天，按周成列，周日在上；格子为正方形并均分面板宽度，gap 3px、
 * 圆角 3px。空格子就是零档 —— 骨架与数据因此长得像同一张网，数值到达时页面
 * 不跳动。
 *
 * 色阶：五档把「数据蓝」按 24/44/64/82/100% 混进空格子的底色调里（参考实现
 * 的 HOME_HEAT_RAMP）。混进空档而不是混进画布色，保证最浅的一档也至少和空
 * 格子一样实 —— 它读起来是"被染过色的空格子"，而不是一个洞。
 *
 * 数据：GET /api/tokens/daily?days=N → [{day, input_tokens, output_tokens,
 * cache_read_tokens}]（db/exploration.go TokenDailyAll，按 UTC 日历日聚合）。
 * 一天的用量取三项之和。
 *
 * 悬停：参考实现让一格提示跟随指针、落在格子上方或下方；这里用同一套规则
 * （放不下就翻到另一侧，宽度按内容，进入 150ms、离开 50ms）。
 */

const HEAT_GAP = 6;         // px between a cell and its tip
const CELL_LEVELS = 5;

/** Sunday-first weekday index, in UTC, for a "YYYY-MM-DD" day string. */
function weekdayOf(day) {
  return new Date(day + "T00:00:00Z").getUTCDay();
}

/** Days back from today (UTC), inclusive, as "YYYY-MM-DD". */
function dayKey(d) {
  return d.toISOString().slice(0, 10);
}

/** Bucket a day's total against the busiest day shown: one level per ramp step. */
function levelOf(total, busiest) {
  if (!total || !busiest) return 0;
  const share = total / busiest;
  if (share <= 0.2) return 1;
  if (share <= 0.4) return 2;
  if (share <= 0.6) return 3;
  if (share <= 0.8) return 4;
  return 5;
}

function fmtCompact(n) {
  if (n >= 1e9) return (n / 1e9).toFixed(1).replace(/\.0$/, "") + "B";
  if (n >= 1e6) return (n / 1e6).toFixed(1).replace(/\.0$/, "") + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(1).replace(/\.0$/, "") + "K";
  return String(n);
}

/** The last `weeks` weeks of days, Sunday first, ending today (UTC). */
function gridDays(weeks) {
  const today = new Date();
  const end = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate()));
  const start = new Date(end);
  start.setUTCDate(start.getUTCDate() - weekdayOf(dayKey(end)));
  start.setUTCDate(start.getUTCDate() - (weeks - 1) * 7);
  const out = [];
  for (let d = new Date(start); d <= end; d.setUTCDate(d.getUTCDate() + 1)) {
    out.push(dayKey(d));
  }
  return out;
}

export default function HeatGrid({ weeks = 26, days = 182, height }) {
  const [byDay, setByDay] = useState(null);
  const [err, setErr] = useState("");
  const gridRef = useRef(null);
  const hintRef = useRef(null);
  const [tip, setTip] = useState(null);

  const load = useCallback(async () => {
    try {
      const r = await authedFetch(`/api/tokens/daily?days=${days}`, { credentials: "include" });
      if (!r.ok) throw new Error((await r.text()).slice(0, 120) || `HTTP ${r.status}`);
      const rows = await r.json();
      const map = new Map();
      for (const row of Array.isArray(rows) ? rows : []) {
        const total = (row.input_tokens || 0) + (row.output_tokens || 0) + (row.cache_read_tokens || 0);
        map.set(row.day, total);
      }
      setByDay(map);
      setErr("");
    } catch (e) {
      setErr(e.message || String(e));
    }
  }, [days]);

  useEffect(() => { load(); }, [load]);

  const cells = useMemo(() => {
    const keys = gridDays(weeks);
    const totals = keys.map((k) => (byDay ? byDay.get(k) || 0 : 0));
    const busiest = Math.max(0, ...totals);
    return keys.map((day, i) => ({ day, total: totals[i], level: levelOf(totals[i], busiest) }));
  }, [byDay, weeks]);

  // The tip follows the pointer but only after a short rest; it leaves at once.
  // Its width, place and side are written before the showing attribute, so a
  // crossing stands over the next day on the first frame rather than travelling.
  const placeTip = useCallback((cell) => {
    const grid = gridRef.current;
    const hint = hintRef.current;
    if (!grid || !hint || !cell) return;
    const gb = grid.getBoundingClientRect();
    const cb = cell.getBoundingClientRect();
    const width = hint.offsetWidth;
    const heightPx = hint.offsetHeight;
    const left = Math.min(Math.max(cb.left - gb.left + cb.width / 2 - width / 2, 0), Math.max(0, gb.width - width));
    const below = gb.height - (cb.bottom - gb.top) - HEAT_GAP;
    const above = cb.top - gb.top - HEAT_GAP;
    const side = below > above && below >= heightPx + HEAT_GAP ? "below" : "above";
    const top = side === "below" ? cb.bottom - gb.top + HEAT_GAP : cb.top - gb.top - HEAT_GAP - heightPx;
    hint.style.left = `${left}px`;
    hint.style.top = `${top}px`;
    hint.dataset.side = side;
  }, []);

  const onMove = useCallback((e) => {
    const cell = e.target instanceof Element ? e.target.closest(".heat-cell") : null;
    if (!cell) { setTip(null); return; }
    const day = cell.dataset.tip || "";
    const total = Number(cell.dataset.total || 0);
    setTip({ day, total, cell });
  }, []);

  useEffect(() => { if (tip) placeTip(tip.cell); }, [tip, placeTip]);

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
      <div style={{ display: "flex", alignItems: "baseline", justifyContent: "space-between", gap: 12 }}>
        <span style={{ fontFamily: "var(--font-serif)", fontSize: 13 }}>用量</span>
        <span style={{ fontSize: 11, color: "var(--c-ink-4)", fontFamily: "var(--font-mono)" }}>
          {byDay ? `${fmtCompact([...byDay.values()].reduce((a, b) => a + b, 0))} tokens · 近 ${weeks} 周` : "…"}
        </span>
      </div>

      {err ? (
        <span style={{ fontSize: 11, color: "var(--c-status-bad)" }}>用量不可用：{err}</span>
      ) : null}

      <div className="heat" ref={gridRef} onMouseMove={onMove} onMouseLeave={() => setTip(null)}
           style={height ? { height } : undefined}>
        {cells.map((c) => (
          <div key={c.day} className="heat-cell" data-level={c.level || undefined}
               data-tip={c.day} data-total={c.total}
               aria-label={`${c.day}：${c.total} tokens`} />
        ))}
        <div className="heat-hint" ref={hintRef} data-shown={tip ? "true" : "false"} aria-hidden={!tip}>
          {tip ? `${tip.day} · ${tip.total ? fmtCompact(tip.total) + " tokens" : "无用量"}` : ""}
        </div>
      </div>

      <div className="heat-legend">
        <span>少</span>
        {Array.from({ length: CELL_LEVELS }, (_, i) => (
          <i key={i} data-level={i + 1}
             style={{ background: `color-mix(in srgb, var(--c-data-ink) ${[24, 44, 64, 82, 100][i]}%, color-mix(in srgb, var(--c-ink) 11%, transparent))` }} />
        ))}
        <span>多</span>
      </div>
    </div>
  );
}
