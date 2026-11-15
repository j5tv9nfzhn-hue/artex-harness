import { useCallback, useEffect, useMemo, useRef, useState } from "react";

/* 轻量控制台：左侧任务、中间 AG-UI 事件流、底部状态栏。
 * 数据只来自两个接口：/api/tasks 与 /api/agui/agent?task=N（AG-UI 事件流）。 */
const TOKEN_KEY = "artex.harness.token";
const token = () => { try { return localStorage.getItem(TOKEN_KEY) || ""; } catch { return ""; } };

async function api(path, opts = {}) {
  const headers = { ...(opts.headers || {}) };
  const t = token();
  if (t) headers.Authorization = "Bearer " + t;
  const r = await fetch(path, { ...opts, headers, credentials: "include" });
  if (!r.ok) throw new Error((await r.text()).slice(0, 200) || r.statusText);
  return r.json();
}

export default function App() {
  const [tasks, setTasks] = useState([]);
  const [taskId, setTaskId] = useState(null);
  const [rows, setRows] = useState([]);
  const [live, setLive] = useState("idle");
  const [err, setErr] = useState("");
  const abort = useRef(null);
  const scroller = useRef(null);

  const loadTasks = useCallback(async () => {
    try {
      const data = await api("/api/tasks");
      const list = Array.isArray(data) ? data : (data.tasks || data.items || []);
      setTasks(list);
      setErr("");
      if (!taskId && list.length) setTaskId(String(list[0].id));
    } catch (e) { setErr("加载任务失败：" + e.message); }
  }, [taskId]);

  useEffect(() => { loadTasks(); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // 订阅一条任务的 AG-UI 事件流
  useEffect(() => {
    if (!taskId) return;
    abort.current?.abort();
    const ac = new AbortController();
    abort.current = ac;
    setRows([]); setLive("connecting"); setErr("");

    (async () => {
      try {
        const r = await fetch(`/api/agui/agent?task=${encodeURIComponent(taskId)}`, {
          headers: token() ? { Authorization: "Bearer " + token() } : {},
          credentials: "include", signal: ac.signal,
        });
        if (!r.ok || !r.body) throw new Error("事件流不可用（" + r.status + "）");
        setLive("streaming");
        const reader = r.body.pipeThrough(new TextDecoderStream()).getReader();
        let buf = "";
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += value;
          let i;
          while ((i = buf.indexOf("\n\n")) >= 0) {
            const chunk = buf.slice(0, i); buf = buf.slice(i + 2);
            const data = chunk.split("\n").filter((l) => l.startsWith("data:")).map((l) => l.slice(5).trim()).join("");
            if (!data) continue;
            let ev; try { ev = JSON.parse(data); } catch { continue; }
            setRows((prev) => applyEvent(prev, ev));
          }
        }
        setLive("done");
      } catch (e) {
        if (e.name !== "AbortError") { setErr(e.message); setLive("error"); }
      }
    })();
    return () => ac.abort();
  }, [taskId]);

  useEffect(() => {
    const el = scroller.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [rows.length]);

  const cur = useMemo(() => tasks.find((t) => String(t.id) === String(taskId)), [tasks, taskId]);

  return (
    <div className="app">
      <aside className="rail">
        <div className="brand"><i />artex · harness</div>
        <div className="rail-title">任务 {tasks.length}</div>
        <ul className="tasks">
          {tasks.map((t) => (
            <li key={t.id} className="task" aria-current={String(t.id) === String(taskId)}
                onClick={() => setTaskId(String(t.id))}>
              <span className="goal">{t.goal || t.description || "task " + t.id}</span>
              <span className="meta">
                <span className={"pill " + statusClass(t.status)}>{t.status}</span>
                <span>#{t.id}</span>
                {t.harness_mode ? <span className="badge">H</span> : null}
              </span>
            </li>
          ))}
          {!tasks.length ? <li className="empty">暂无任务</li> : null}
        </ul>
      </aside>

      <main className="main">
        <header className="topbar">
          <strong>{cur ? (cur.goal || cur.description || "task " + cur.id) : "未选择任务"}</strong>
          <div className="pills">
            <span className={"pill " + statusClass(cur && cur.status)}>{cur ? cur.status : "—"}</span>
            <span className="pill">AG-UI</span>
            <span className={"pill " + (live === "streaming" ? "run" : live === "error" ? "bad" : "")}>{liveLabel(live)}</span>
          </div>
        </header>
        <div className="stream" ref={scroller}>
          {err ? <div className="empty" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>{err}</div> : null}
          {!rows.length && !err ? <div className="empty">选择任务后，这里按轮次显示 AG-UI 事件流。</div> : null}
          {rows.map((r) => <Row key={r.key} row={r} />)}
        </div>
      </main>

      <footer className="statusbar">
        <span><i className={"dot " + (live === "streaming" ? "on" : "")} /> {liveLabel(live)}</span>
        <span>·</span><span>{taskId ? "#" + taskId : "无任务"}</span>
        <span>·</span><span>{rows.length} 条事件</span>
        <span className="spacer">artex-harness · AG-UI</span>
      </footer>
    </div>
  );
}

function Row({ row }) {
  if (row.type === "turn") return <div className="turn"><span>{row.label}</span></div>;
  if (row.type === "tool") {
    return (
      <div className="tool">
        <div className="head"><span className="badge">tool</span><span className="name">{row.name}</span>{row.done ? null : <span className="badge">running</span>}</div>
        {row.args ? <pre className="args">{row.args}</pre> : null}
        {row.result !== undefined ? <pre className={"result" + (row.isError ? " err" : "")}>{row.result || "(无输出)"}</pre> : null}
      </div>
    );
  }
  return <p className={"msg " + (row.role === "thinking" ? "thinking" : "assistant")}>{row.text}</p>;
}

const statusClass = (s) => (s === "running" ? "run" : s === "done" || s === "achieved" ? "ok" : s === "failed" || s === "timeout" ? "bad" : "");
const liveLabel = (s) => ({ idle: "未连接", connecting: "连接中", streaming: "实时", done: "已结束", error: "连接失败" }[s] || s);

/* AG-UI 事件 → 视图行。投影是纯函数，便于以后加 Replay 时复用同一份逻辑。 */
function applyEvent(rows, ev) {
  const key = (n) => rows.length + "-" + n;
  switch (ev.type) {
    case "STEP_STARTED":
      return [...rows, { type: "turn", key: key("turn"), label: ev.stepName || "turn" }];
    case "TEXT_MESSAGE_START":
      return [...rows, { type: "msg", key: key("m"), role: "assistant", text: "", msgId: ev.messageId }];
    case "TEXT_MESSAGE_CONTENT": {
      const idx = findLast(rows, (r) => r.type === "msg" && r.msgId === ev.messageId);
      if (idx < 0) return [...rows, { type: "msg", key: key("m"), role: "assistant", text: ev.delta || "", msgId: ev.messageId }];
      const next = rows.slice();
      next[idx] = { ...next[idx], text: (next[idx].text || "") + (ev.delta || "") };
      return next;
    }
    case "TOOL_CALL_START":
      return [...rows, { type: "tool", key: key("t"), callId: ev.toolCallId, name: ev.toolCallName, args: "", done: false }];
    case "TOOL_CALL_ARGS": {
      const idx = findLast(rows, (r) => r.callId === ev.toolCallId);
      if (idx < 0) return rows;
      const next = rows.slice();
      next[idx] = { ...next[idx], args: (next[idx].args || "") + (ev.delta || "") };
      return next;
    }
    case "TOOL_CALL_END": {
      const idx = findLast(rows, (r) => r.callId === ev.toolCallId);
      if (idx < 0) return rows;
      const next = rows.slice();
      next[idx] = { ...next[idx], done: true };
      return next;
    }
    case "TOOL_CALL_RESULT": {
      const idx = findLast(rows, (r) => r.callId === ev.toolCallId);
      if (idx < 0) return rows;
      const next = rows.slice();
      next[idx] = { ...next[idx], done: true, result: ev.content ?? "", isError: ev.status === "error" };
      return next;
    }
    default:
      return rows;
  }
}

function findLast(arr, pred) {
  for (let i = arr.length - 1; i >= 0; i--) if (pred(arr[i])) return i;
  return -1;
}
