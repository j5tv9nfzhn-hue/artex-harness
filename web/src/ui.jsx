import {
  cloneElement, createContext, forwardRef, useCallback, useContext, useEffect, useId, useLayoutEffect,
  useMemo, useRef, useState,
} from "react";
import { createPortal } from "react-dom";

/* ─────────────────────────────────────────────────────────────────────────
 * UI primitives — hand-written, no component library.
 *
 * The console used to be assembled from Radix Themes, which is exactly what
 * made it read as "a pile of libraries": Radix paints its own palette on a
 * wrapper, brings its own radii, its own 1px borders and its own control
 * geometry, and every one of those has to be fought back rule by rule.
 *
 * These primitives keep the API the console already calls (Text/Flex/Box/
 * Button/…) so the call sites stay readable, but each one renders plain
 * semantic HTML and takes all of its look from tokens.css. Nothing here
 * decides a colour, a radius or a spacing value.
 * ───────────────────────────────────────────────────────────────────────── */

/* ── spacing scale: Radix's 1–9 props, mapped to the 4px rhythm ─────────── */
const SPACE = { "1": 4, "2": 8, "3": 12, "4": 16, "5": 20, "6": 24, "7": 28, "8": 32, "9": 40 };
const space = (v) => (typeof v === "number" ? v : SPACE[v] ?? undefined);

/** Build the inline style a spacing-prop shorthand asks for. */
function spacing(props) {
  const out = {};
  const map = {
    p: ["padding"], px: ["paddingLeft", "paddingRight"],
    py: ["paddingTop", "paddingBottom"], pt: ["paddingTop"],
    pb: ["paddingBottom"], pl: ["paddingLeft"], pr: ["paddingRight"],
    m: ["margin"], mx: ["marginLeft", "marginRight"],
    my: ["marginTop", "marginBottom"], mt: ["marginTop"],
    mb: ["marginBottom"], ml: ["marginLeft"], mr: ["marginRight"],
  };
  for (const [key, targets] of Object.entries(map)) {
    if (props[key] === undefined) continue;
    const v = space(props[key]);
    if (v === undefined) continue;
    for (const t of targets) out[t] = v;
  }
  return out;
}

/** Split our prop vocabulary from the props an element accepts. */
function split(props, extra = []) {
  const {
    style, className, children, as: As, ...rest
  } = props;
  const own = {};
  const passthrough = {};
  for (const [k, v] of Object.entries(rest)) {
    if (extra.includes(k) || k in SPACE_PROPS) own[k] = v;
    else passthrough[k] = v;
  }
  return { style, className, children, As, own, passthrough };
}
const SPACE_PROPS = {
  p: 1, px: 1, py: 1, pt: 1, pb: 1, pl: 1, pr: 1,
  m: 1, mx: 1, my: 1, mt: 1, mb: 1, ml: 1, mr: 1,
};

/* ── Box ─────────────────────────────────────────────────────────────────── */
export const Box = forwardRef(function Box(props, ref) {
  const { style, className, children, As, own, passthrough } = split(props);
  const El = As || "div";
  return (
    <El ref={ref} className={className} style={{ ...spacing(own), ...style }} {...passthrough}>
      {children}
    </El>
  );
});

/* ── Flex ────────────────────────────────────────────────────────────────── */
export const Flex = forwardRef(function Flex(props, ref) {
  const { style, className, children, As, own, passthrough } = split(props, ["direction", "align", "justify", "gap", "wrap", "height", "minHeight", "maxWidth", "width"]);
  const El = As || "div";
  const css = {
    display: "flex",
    flexDirection: own.direction || "row",
    alignItems: own.align === "start" ? "flex-start" : own.align === "end" ? "flex-end" : own.align,
    justifyContent: own.justify === "start" ? "flex-start" : own.justify === "end" ? "flex-end" : own.justify === "between" ? "space-between" : own.justify,
    gap: space(own.gap),
    flexWrap: own.wrap === "wrap" ? "wrap" : undefined,
    height: own.height === "100%" ? "100%" : own.height,
    minHeight: own.minHeight === "0" ? 0 : own.minHeight,
    maxWidth: own.maxWidth,
    width: own.width,
    ...spacing(own),
    ...style,
  };
  for (const k of Object.keys(css)) if (css[k] === undefined) delete css[k];
  return (
    <El ref={ref} className={className} style={css} {...passthrough}>
      {children}
    </El>
  );
});

/* ── Text ────────────────────────────────────────────────────────────────── */
/* size 1–3 maps to the measured scale; colour names map to the ink ladder. */
export const Text = forwardRef(function Text(props, ref) {
  const { style, className, children, As, own, passthrough } = split(props, ["size", "color", "weight", "truncate", "align"]);
  const El = As || "span";
  const size = { "1": "var(--fs-caption)", "2": "var(--fs-ui)", "3": "var(--fs-body)", "4": "var(--fs-prose)" }[own.size];
  const color = {
    gray: "var(--c-ink-3)", red: "var(--c-status-bad)", green: "var(--c-status-ok)",
    amber: "var(--c-status-awaiting)", accent: "var(--c-accent)",
  }[own.color] || (own.color && own.color.startsWith("var(") ? own.color : undefined);
  const css = {
    fontSize: size,
    color,
    fontWeight: own.weight === "medium" ? 500 : own.weight === "bold" ? 600 : own.weight,
    ...(own.truncate ? { overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" } : null),
    textAlign: own.align,
    ...spacing(own),
    ...style,
  };
  for (const k of Object.keys(css)) if (css[k] === undefined) delete css[k];
  return (
    <El ref={ref} className={className} style={css} {...passthrough}>
      {children}
    </El>
  );
});

/* ── Heading ─────────────────────────────────────────────────────────────── */
export const Heading = forwardRef(function Heading(props, ref) {
  const { style, className, children, As, own, passthrough } = split(props, ["size", "weight"]);
  const El = As || "h2";
  return (
    <El ref={ref} className={className}
        style={{
          fontFamily: "var(--font-serif)",
          fontSize: own.size === "3" ? "var(--fs-title)" : own.size === "5" ? "var(--fs-display)" : "var(--fs-body)",
          fontWeight: own.weight === "medium" ? 500 : 500,
          letterSpacing: "-0.01em",
          lineHeight: 1.25,
          margin: 0,
          ...spacing(own), ...style,
        }}
        {...passthrough}>{children}</El>
  );
});

/* ── Button / IconButton ─────────────────────────────────────────────────── */
export const Button = forwardRef(function Button(props, ref) {
  const {
    style, className = "", children, variant = "solid", color, size = "2", disabled, ...rest
  } = props;
  const cls = ["ui-btn", `ui-btn--${variant}`, `ui-btn--${size}`, color ? `ui-btn--${color}` : "", className]
    .filter(Boolean).join(" ");
  return (
    <button ref={ref} type={rest.type || "button"} className={cls} disabled={disabled}
            style={style} {...rest}>{children}</button>
  );
});

export const IconButton = forwardRef(function IconButton(props, ref) {
  const {
    style, className = "", children, variant = "ghost", color, size = "2", disabled, "aria-label": aria, ...rest
  } = props;
  const cls = ["ui-icon-btn", `ui-icon-btn--${variant}`, `ui-icon-btn--${size}`, color ? `ui-icon-btn--${color}` : "", className]
    .filter(Boolean).join(" ");
  return (
    <button ref={ref} type="button" className={cls} aria-label={aria} disabled={disabled}
            style={style} {...rest}>{children}</button>
  );
});

/* ── Badge ───────────────────────────────────────────────────────────────── */
export const Badge = forwardRef(function Badge(props, ref) {
  const { style, className = "", children, variant = "soft", color, size = "1", ...rest } = props;
  const cls = ["ui-badge", `ui-badge--${variant}`, color ? `ui-badge--${color}` : "", className]
    .filter(Boolean).join(" ");
  return <span ref={ref} className={cls} style={style} {...rest}>{children}</span>;
});

/* ── Card ────────────────────────────────────────────────────────────────── */
export const Card = forwardRef(function Card(props, ref) {
  const { style, className = "", children, size, ...rest } = props;
  return <div ref={ref} className={"ui-card " + className} style={style} {...rest}>{children}</div>;
});

/* ── Code ────────────────────────────────────────────────────────────────── */
export const Code = forwardRef(function Code(props, ref) {
  const { style, className = "", children, size, variant, ...rest } = props;
  return <code ref={ref} className={"ui-code " + className} style={style} {...rest}>{children}</code>;
});

/* ── Separator ───────────────────────────────────────────────────────────── */
export const Separator = forwardRef(function Separator(props, ref) {
  const { orientation = "horizontal", size, style, className = "", ...rest } = props;
  return (
    <span ref={ref} role="separator" aria-orientation={orientation}
          className={`ui-sep ui-sep--${orientation} ` + className}
          style={style} {...rest} />
  );
});

/* ── ScrollArea ──────────────────────────────────────────────────────────── */
export const ScrollArea = forwardRef(function ScrollArea(props, ref) {
  const { style, className = "", children, type, scrollbars, ...rest } = props;
  return <div ref={ref} className={"ui-scroll " + className} style={style} {...rest}>{children}</div>;
});

/* ── TextField ───────────────────────────────────────────────────────────── */
const TextFieldRoot = forwardRef(function TextFieldRoot(props, ref) {
  const { style, className = "", children, size, ...rest } = props;
  return (
    <label className={"ui-field " + className} style={style}>
      {children ? <span className="ui-field__slot">{children}</span> : null}
      <input ref={ref} className="ui-field__input" {...rest} />
    </label>
  );
});
const TextFieldSlot = ({ children }) => <>{children}</>;
export const TextField = { Root: TextFieldRoot, Slot: TextFieldSlot };

/* ── TextArea ────────────────────────────────────────────────────────────── */
export const TextArea = forwardRef(function TextArea(props, ref) {
  const { style, className = "", ...rest } = props;
  return <textarea ref={ref} className={"ui-textarea " + className} style={style} {...rest} />;
});

/* ── Checkbox ────────────────────────────────────────────────────────────── */
export const Checkbox = forwardRef(function Checkbox({ checked, onCheckedChange, style, className = "", ...rest }, ref) {
  return (
    <input ref={ref} type="checkbox" className={"ui-check " + className} checked={!!checked}
           onChange={(e) => onCheckedChange?.(e.target.checked)} style={style} {...rest} />
  );
});

/* ── Switch ──────────────────────────────────────────────────────────────── */
export const Switch = forwardRef(function Switch({ checked, onCheckedChange, disabled, style, className = "", ...rest }, ref) {
  return (
    <button ref={ref} type="button" role="switch" aria-checked={!!checked} disabled={disabled}
            className={"ui-switch " + className} onClick={() => onCheckedChange?.(!checked)} style={style} {...rest}>
      <span className="ui-switch__knob" />
    </button>
  );
});

/* ── Avatar ──────────────────────────────────────────────────────────────── */
export const Avatar = forwardRef(function Avatar({ fallback, size, radius, color, style, className = "" }, ref) {
  return <span ref={ref} className={"ui-avatar " + className} style={style}>{fallback}</span>;
});

/* ── Table ───────────────────────────────────────────────────────────────── */
const T = (tag, cls) => forwardRef(function Cell(props, ref) {
  const { style, className = "", children, ...rest } = props;
  return <tag ref={ref} className={cls + " " + className} style={style} {...rest}>{children}</tag>;
});
export const Table = {
  Root: forwardRef(function Root({ children, className = "", style, ...rest }, ref) {
    return <table ref={ref} className={"ui-table " + className} style={style} {...rest}>{children}</table>;
  }),
  Header: T("thead", "ui-table__head"),
  Body: T("tbody", "ui-table__body"),
  Row: T("tr", "ui-table__row"),
  ColumnHeaderCell: T("th", "ui-table__th"),
  Cell: T("td", "ui-table__td"),
};

/* ── Tooltip ─────────────────────────────────────────────────────────────────
 * A CSS-only hint: the trigger carries the text in `data-hint`, and the plate
 * is drawn by ::after. No portal, no measurement, no listener — the reference
 * draws its own hints the same way where it can. */
export const Tooltip = ({ content, children, side = "top" }) => {
  if (!content) return children;
  const child = Array.isArray(children) ? children[0] : children;
  if (!child || typeof child !== "object") return child;
  return (
    <span className="ui-hint" data-hint={content} data-side={side}>
      {child}
    </span>
  );
};

/* ── Popover surface shared by the menu/select/dialog primitives ─────────── */
function useDismiss(open, onClose, ref) {
  useEffect(() => {
    if (!open) return;
    const onDown = (e) => { if (ref.current && !ref.current.contains(e.target)) onClose?.(); };
    const onKey = (e) => { if (e.key === "Escape") onClose?.(); };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open, onClose, ref]);
}

/* ── DropdownMenu / ContextMenu ────────────────────────────────────────────
 * 两者共用一套菜单面与关闭逻辑（点外部关闭、Esc 关闭），触发方式不同：
 *   DropdownMenu：左键点击触发面。切换并进触发子元素的 onClick，而不是
 *     包一层 span 指望冒泡——子元素里常见的 stopPropagation（行内 "…"
 *     按钮防导航）会把冒泡拦掉，菜单就永远打不开。
 *   ContextMenu：右键触发（onContextMenu，preventDefault 后在光标处打开）；
 *     左键行为完全不受影响。
 * 此前两者共用同一个左键触发：右键什么都不发生，左键点行反而弹菜单——
 * 那不是右键菜单。3.28 修掉。菜单面按需做视口内收敛（光标贴边时不溢出）。 */
function MenuSurface({ open, onClose, anchor, pos, align = "start", side = "bottom", children }) {
  const ref = useRef(null);
  const [shift, setShift] = useState({ x: 0, y: 0 });
  useDismiss(open, onClose, ref);
  useLayoutEffect(() => {
    if (!open || !ref.current) { setShift({ x: 0, y: 0 }); return; }
    const r = ref.current.getBoundingClientRect();
    const x = Math.min(0, window.innerWidth - 4 - r.right) + Math.max(0, 4 - r.left);
    const y = Math.min(0, window.innerHeight - 4 - r.bottom) + Math.max(0, 4 - r.top);
    if (x || y) setShift({ x, y });
  }, [open, pos]);
  if (!open) return null;
  const base = pos
    ? { left: Math.min(pos.x, window.innerWidth - 200), top: Math.min(pos.y, window.innerHeight - 240) }
    : anchor
      ? side === "right"
        ? { left: anchor.right + 6, top: anchor.top }
        : align === "end"
          ? { left: anchor.right, top: anchor.bottom + 4, transform: "translateX(-100%)" }
          : { left: anchor.left, top: anchor.bottom + 4 }
      : { left: 0, top: 0 };
  const style = { ...base, marginLeft: shift.x, marginTop: shift.y };
  return createPortal(
    <div ref={ref} className="ui-menu" role="menu" style={style}>{children}</div>,
    document.body,
  );
}

const MenuCtx = createContext(null);

function MenuRoot({ children, open: openProp, onOpenChange }) {
  const [openState, setOpenState] = useState(false);
  const [pos, setPosState] = useState(null);
  const open = openProp ?? openState;
  const anchorRef = useRef(null);
  const setOpen = (v, p) => {
    setOpenState(v);
    if (p !== undefined) setPosState(p);
    else if (!v) setPosState(null);
    onOpenChange?.(v);
  };
  return <MenuCtx.Provider value={{ open, setOpen, anchorRef, pos }}>{children}</MenuCtx.Provider>;
}

function MenuTrigger({ children }) {
  const ctx = useContext(MenuCtx);
  const child = Array.isArray(children) ? children[0] : children;
  if (!child || typeof child !== "object") return child;
  const onClick = (e) => {
    child.props.onClick?.(e);
    if (e.defaultPrevented) return;
    ctx.anchorRef.current = e.currentTarget;
    ctx.setOpen(!ctx.open);
  };
  return cloneElement(child, { onClick });
}

/* 右键触发：只接 onContextMenu；左键点行照常打开会话。 */
function ContextTrigger({ children }) {
  const ctx = useContext(MenuCtx);
  const child = Array.isArray(children) ? children[0] : children;
  if (!child || typeof child !== "object") return child;
  const onContextMenu = (e) => {
    child.props.onContextMenu?.(e);
    e.preventDefault();
    ctx.setOpen(true, { x: e.clientX, y: e.clientY });
  };
  return cloneElement(child, { onContextMenu });
}

function MenuContent({ children, align, side, onClose, ...rest }) {
  const ctx = useContext(MenuCtx);
  const anchor = ctx.anchorRef.current?.getBoundingClientRect?.() ?? null;
  return (
    <MenuSurface open={ctx.open} onClose={() => { ctx.setOpen(false); onClose?.(); }} anchor={anchor} pos={ctx.pos} align={align} side={side}>
      {children}
    </MenuSurface>
  );
}

function MenuItem({ children, onSelect, color, ...rest }) {
  const ctx = useContext(MenuCtx);
  return (
    <button type="button" role="menuitem" className={"ui-menu__item" + (color === "red" ? " ui-menu__item--danger" : "")}
            onClick={(e) => { e.stopPropagation(); ctx.setOpen(false); onSelect?.(e); }}
            {...rest}>{children}</button>
  );
}
const MenuLabel = ({ children }) => <div className="ui-menu__label">{children}</div>;
const MenuSeparator = () => <div className="ui-sep ui-sep--horizontal" style={{ margin: "4px 0" }} />;

export const DropdownMenu = {
  Root: MenuRoot, Trigger: MenuTrigger, Content: MenuContent,
  Item: MenuItem, Label: MenuLabel, Separator: MenuSeparator,
};
export const ContextMenu = {
  Root: MenuRoot,
  Trigger: ContextTrigger,
  Content: MenuContent,
  Item: MenuItem,
  Separator: MenuSeparator,
};

/* ── Select (a native <select> styled to the ladder) ─────────────────────────
 * The reference uses a popover list; a native select keeps keyboard and
 * platform behaviour correct and is visually identical at rest, which is what
 * the console needs (model picker, effort picker, sort order). */
const SelectCtx = createContext(null);

function SelectRoot({ children, value, onValueChange, disabled, placeholder }) {
  return (
    <SelectCtx.Provider value={{ value, onValueChange, disabled, placeholder }}>
      <span className="ui-select">{children}</span>
    </SelectCtx.Provider>
  );
}
function SelectTrigger({ children, placeholder, ...rest }) {
  const ctx = useContext(SelectCtx);
  return (
    <span className="ui-select__face" data-disabled={ctx.disabled ? "true" : undefined}>
      {children || placeholder || "选择"}
      <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
        <path d="M2 4l3 3 3-3" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
      </svg>
    </span>
  );
}
function SelectContent({ children }) {
  const ctx = useContext(SelectCtx);
  const options = [];
  const walk = (nodes) => {
    for (const n of Array.isArray(nodes) ? nodes : [nodes]) {
      if (!n) continue;
      if (n.type === SelectItem) options.push(n);
      else if (n.props?.children) walk(n.props.children);
    }
  };
  walk(children);
  return (
    <select className="ui-select__native" value={ctx.value ?? ""} disabled={ctx.disabled}
            onChange={(e) => ctx.onValueChange?.(e.target.value)} aria-label="选择">
      {options.map((o, i) => (
        <option key={i} value={o.props.value}>{o.props.children}</option>
      ))}
    </select>
  );
}
const SelectItem = ({ children }) => null;
export const Select = { Root: SelectRoot, Trigger: SelectTrigger, Content: SelectContent, Item: SelectItem };

/* ── Dialog ──────────────────────────────────────────────────────────────── */
const DialogCtx = createContext(null);

function DialogRoot({ children, open: openProp, onOpenChange }) {
  const [openState, setOpenState] = useState(false);
  const open = openProp ?? openState;
  const setOpen = (v) => { setOpenState(v); onOpenChange?.(v); };
  useEffect(() => {
    if (!open) return;
    const onKey = (e) => { if (e.key === "Escape") setOpen(false); };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open]);
  return <DialogCtx.Provider value={{ open, setOpen }}>{children}</DialogCtx.Provider>;
}

function DialogTrigger({ children }) {
  const ctx = useContext(DialogCtx);
  const child = Array.isArray(children) ? children[0] : children;
  if (!child || typeof child !== "object") return child;
  return <span style={{ display: "contents" }} onClick={() => ctx.setOpen(true)}>{child}</span>;
}

function DialogContent({ children, maxWidth, className, ...rest }) {
  const ctx = useContext(DialogCtx);
  if (!ctx.open) return null;
  return createPortal(
    <div className="ui-dialog__backdrop" onMouseDown={(e) => { if (e.target === e.currentTarget) ctx.setOpen(false); }}>
      {/* className merges with .ui-dialog instead of replacing it: spreading
          {...rest} after className="ui-dialog" used to let a caller's
          className (e.g. SettingsView's "settings-dialog") silently drop
          every .ui-dialog rule — background included — so the dialog rendered
          transparent over the backdrop. */}
      <div className={"ui-dialog" + (className ? " " + className : "")} role="dialog" aria-modal="true" style={maxWidth ? { maxWidth } : undefined} {...rest}>
        {children}
      </div>
    </div>,
    document.body,
  );
}

const DialogTitle = ({ children, size }) => <h2 className="ui-dialog__title">{children}</h2>;
const DialogDescription = ({ children, size, color }) => <p className="ui-dialog__desc">{children}</p>;
function DialogClose({ children }) {
  const ctx = useContext(DialogCtx);
  const child = Array.isArray(children) ? children[0] : children;
  if (!child || typeof child !== "object") return child;
  return <span style={{ display: "contents" }} onClick={() => ctx.setOpen(false)}>{child}</span>;
}

export const Dialog = {
  Root: DialogRoot, Trigger: DialogTrigger, Content: DialogContent,
  Title: DialogTitle, Description: DialogDescription, Close: DialogClose,
};

/* ── Theme ───────────────────────────────────────────────────────────────────
 * Kept as a no-op shell so call sites do not change: the token ladder lives on
 * :root and switches on `data-theme`, so there is no wrapper palette to set. */
export const Theme = ({ children, ...rest }) => <>{children}</>;

export const useAnchorRect = () => {
  const ref = useRef(null);
  const [rect, setRect] = useState(null);
  const measure = useCallback(() => setRect(ref.current?.getBoundingClientRect() ?? null), []);
  useLayoutEffect(measure, [measure]);
  return [ref, rect, measure];
};

export { useMemo, useId };

/* ── BrandMark ───────────────────────────────────────────────────────────────
 * artex-harness's own mark — a bold geometric "H" (for harness), drawn for
 * this product and shipped as `web/public/harness-mark.png`.
 *
 * It replaces the upstream ARTEX infinity glyph (`artex-mark.png`, deleted):
 * that mark is ARTEX's identity, and it kept showing up in the sidebar and
 * the top band next to our own wordmark. The reference project's starburst
 * was never used for the same reason — that mark is Anthropic's brand
 * identity, and drawing it in our own product would be a trademark use we
 * have no licence for. The visual language we took from it (palette,
 * geometry, type) is design, not identity; the mark is identity.
 *
 * The PNG is used as a CSS mask so the mark takes whatever ink the surrounding
 * text does, in either appearance. */
export function BrandMark({ size = 18 }) {
  return (
    <span
      aria-hidden="true"
      style={{
        display: "inline-block",
        width: size,
        height: size,
        flex: "none",
        background: "currentColor",
        WebkitMaskImage: "url(/app/harness-mark.png)",
        maskImage: "url(/app/harness-mark.png)",
        WebkitMaskSize: "contain",
        maskSize: "contain",
        WebkitMaskRepeat: "no-repeat",
        maskRepeat: "no-repeat",
        WebkitMaskPosition: "center",
        maskPosition: "center",
      }}
    />
  );
}
