/* Toasts — the console's own, on the console's own tokens.
 *
 * Why not a library: sonner ships a whole design system (its own radii, shadow
 * ladder, palette, font stack) and every one of those has to be fought back to
 * match tokens.css. This keeps the call shape the call sites already use
 * (`toast.success` / `toast.error`) but renders nothing the tokens do not
 * define — the theme stays one file (tokens.css).
 *
 * Deliberate properties:
 *   · one stack, bottom-centre, oldest on top (reading order);
 *   · errors persist until dismissed, successes self-dismiss: a failure the
 *     operator never saw is the same as a swallowed error;
 *   · role="status" / role="alert" so a screen reader announces it.
 */
import { useEffect, useState } from "react";
import { CheckCircle2, Info, X, XCircle } from "lucide-react";

const DURATION = { success: 2600, info: 3200, error: 0 }; // 0 = sticky

let seq = 0;
const items = [];
const listeners = new Set();

function emit() {
  for (const fn of listeners) fn(items.slice());
}

export function dismiss(id) {
  const i = items.findIndex((x) => x.id === id);
  if (i < 0) return;
  items.splice(i, 1);
  emit();
}

function push(kind, message) {
  if (message == null || message === "") return;
  const item = { id: ++seq, kind, message: String(message) };
  items.push(item);
  emit();
  const ms = DURATION[kind] ?? 2600;
  if (ms > 0) setTimeout(() => dismiss(item.id), ms);
  return item.id;
}

export const toast = {
  success: (m) => push("success", m),
  error: (m) => push("error", m),
  info: (m) => push("info", m),
  dismiss,
};

const ICON = { success: CheckCircle2, error: XCircle, info: Info };

export function Toaster() {
  const [stack, setStack] = useState(() => items.slice());

  useEffect(() => {
    const fn = (next) => setStack(next);
    listeners.add(fn);
    return () => { listeners.delete(fn); };
  }, []);

  if (!stack.length) return null;

  return (
    <div className="toast-stack" aria-live="polite">
      {stack.map((t) => {
        const Icon = ICON[t.kind] || Info;
        return (
          <div key={t.id} className={"toast toast--" + t.kind}
               role={t.kind === "error" ? "alert" : "status"}>
            <Icon size={14} className="toast__icon" />
            <span className="toast__text">{t.message}</span>
            <button className="toast__close" aria-label="关闭提示" onClick={() => dismiss(t.id)}>
              <X size={12} />
            </button>
          </div>
        );
      })}
    </div>
  );
}