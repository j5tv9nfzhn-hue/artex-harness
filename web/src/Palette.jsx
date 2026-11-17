/* Command palette — hand-written, because cmdk brings its own visual layer.
 *
 * cmdk is a behaviour library, but its default markup (`[cmdk-item]` selected
 * states, its own list padding) is a design decision we would then have to
 * override rule by rule for every token. The behaviour that actually matters
 * here is small and fully testable: arrow keys move the highlight, Enter picks,
 * Esc closes, typing filters. That is what this file implements.
 *
 * Accessibility: the listbox pattern (role=combobox/listbox/option,
 * aria-activedescendant), so keyboard and screen-reader users get the same
 * thing mouse users get.
 */
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { Search } from "lucide-react";

/* flatten builds the render list once: section headers are not selectable, so
 * the highlight index counts only real options. */
function flatten(sections) {
  const rows = [];
  let idx = 0;
  for (const s of sections) {
    if (!s.items.length) continue;
    rows.push({ kind: "group", label: s.label, key: "g-" + s.label });
    for (const it of s.items) {
      rows.push({ kind: "item", ...it, idx: idx++ });
    }
  }
  return rows;
}

export default function Palette({ sections, placeholder = "搜索…", onClose, footer }) {
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const listRef = useRef(null);
  const inputRef = useRef(null);
  const baseId = useId();

  // Filtering is per item: `keywords` lets a row be found by words that are not
  // in its label (e.g. "new chat" for 新建对话), which is the whole point of a
  // palette. Matching is case-insensitive substring, no fuzzy scoring: a wrong
  // "best match" reorders rows under the user's arrow keys.
  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    if (!needle) return sections;
    return sections.map((s) => ({
      ...s,
      items: s.items.filter((it) =>
        (it.label + " " + (it.keywords || "")).toLowerCase().includes(needle)),
    }));
  }, [sections, q]);

  const rows = useMemo(() => flatten(filtered), [filtered]);
  const options = useMemo(() => rows.filter((r) => r.kind === "item"), [rows]);

  useEffect(() => { setActive(0); }, [q]);
  useEffect(() => { inputRef.current?.focus(); }, []);

  // Keep the highlighted row visible when the list is longer than the box.
  useEffect(() => {
    const el = listRef.current?.querySelector('[data-active="true"]');
    el?.scrollIntoView({ block: "nearest" });
  }, [active]);

  const choose = (row) => {
    if (!row) return;
    onClose?.();
    row.onSelect?.();
  };

  const onKey = (e) => {
    if (e.key === "Escape") { e.preventDefault(); onClose?.(); return; }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((i) => (options.length ? (i + 1) % options.length : 0));
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((i) => (options.length ? (i - 1 + options.length) % options.length : 0));
      return;
    }
    if (e.key === "Enter") { e.preventDefault(); choose(options[active]); }
  };

  const activeId = options[active] ? `${baseId}-opt-${options[active].idx}` : undefined;

  return (
    <div className="cmdk-overlay" onClick={onClose} onKeyDown={onKey}>
      <div className="cmdk-box" role="dialog" aria-modal="true" aria-label="命令面板"
           onClick={(e) => e.stopPropagation()}>
        <div className="cmdk-head">
          <Search size={14} />
          <input
            ref={inputRef}
            className="cmdk-input"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={onKey}
            placeholder={placeholder}
            role="combobox"
            aria-expanded="true"
            aria-controls={`${baseId}-list`}
            aria-activedescendant={activeId}
            aria-autocomplete="list"
          />
        </div>
        <div className="cmdk-list" id={`${baseId}-list`} role="listbox" ref={listRef}>
          {!options.length ? (
            <div className="cmdk-empty">没有匹配项</div>
          ) : (
            rows.map((row) => {
              if (row.kind === "group") {
                return <div key={row.key} className="cmdk-group">{row.label}</div>;
              }
              const on = row.idx === active;
              return (
                <div key={row.key || `${baseId}-opt-${row.idx}`}
                     id={`${baseId}-opt-${row.idx}`}
                     role="option"
                     aria-selected={on}
                     data-active={on ? "true" : undefined}
                     className={"cmdk-item" + (on ? " is-active" : "")}
                     onMouseEnter={() => setActive(row.idx)}
                     onClick={() => choose(row)}>
                  {row.render ? row.render() : <span>{row.label}</span>}
                </div>
              );
            })
          )}
        </div>
        {footer ? <div className="cmdk-foot">{footer}</div> : null}
      </div>
    </div>
  );
}