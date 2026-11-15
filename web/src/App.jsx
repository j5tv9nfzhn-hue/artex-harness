import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Badge, Box, Card, Code, Flex, Heading, ScrollArea, Separator, Text, Theme,
} from "@radix-ui/themes";

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
    <Theme appearance="inherit" accentColor="tomato" grayColor="sand" radius="large" scaling="100%">
      <div className="app">
        <aside className="rail">
          <Box p="3"><Heading size="3" weight="medium">artex · harness</Heading></Box>
          <Box px="3" pb="2"><Text size="1" color="gray">任务 {tasks.length}</Text></Box>
          <ScrollArea style={{ flex: 1 }}>
            <Flex direction="column" gap="1" px="2" pb="3">
              {tasks.map((t) => (
                <Card key={t.id} className="task" aria-current={String(t.id) === String(taskId)}
                      size="1" variant={String(t.id) === String(taskId) ? "surface" : "ghost"}
                      onClick={() => setTaskId(String(t.id))} style={{ cursor: "pointer" }}>
                  <Flex direction="column" gap="1">
                    <Text size="2" style={{ lineHeight: 1.4 }}>{clip(t.goal || t.description || "task " + t.id, 90)}</Text>
                    <Flex gap="2" align="center">
                      <Badge size="1" color={badgeColor(t.status)} variant="soft">{t.status}</Badge>
                      <Text size="1" color="gray">#{t.id}</Text>
                      {t.harness_mode ? <Badge size="1" variant="outline" color="gray">H</Badge> : null}
                    </Flex>
                  </Flex>
                </Card>
              ))}
              {!tasks.length ? <Box p="3"><Text size="1" color="gray">暂无任务</Text></Box> : null}
            </Flex>
          </ScrollArea>
        </aside>

        <div className="main">
          <Flex align="center" gap="3" px="4" py="3" style={{ borderBottom: "1px solid var(--gray-a5)" }}>
            <Text size="2" weight="medium" truncate style={{ maxWidth: "46vw" }}>
              {cur ? (cur.goal || cur.description || "task " + cur.id) : "未选择任务"}
            </Text>
            <Box style={{ marginLeft: "auto" }}>
              <Flex gap="2">
                <Badge size="1" color={badgeColor(cur && cur.status)} variant="soft">{cur ? cur.status : "—"}</Badge>
                <Badge size="1" variant="outline" color="gray">AG-UI</Badge>
                <Badge size="1" color={live === "streaming" ? "green" : live === "error" ? "red" : "gray"} variant="soft">
                  {liveLabel(live)}
                </Badge>
              </Flex>
            </Box>
          </Flex>

          <div className="stream">
            {err ? <Card size="1" variant="surface" style={{ borderColor: "var(--red-7)" }}><Text size="2" color="red">{err}</Text></Card> : null}
            {!rows.length && !err ? <Text size="2" color="gray">选择任务后，这里按轮次显示 AG-UI 事件流。</Text> : null}
            <Flex direction="column" gap="2">
              {rows.map((r) => <Row key={r.key} row={r} />)}
            </Flex>
          </div>
        </div>

        <Flex className="statusbar" align="center" gap="3">
          <Text size="1" color="gray">{liveLabel(live)}</Text>
          <Text size="1" color="gray">·</Text>
          <Text size="1" color="gray">{taskId ? "#" + taskId : "无任务"}</Text>
          <Text size="1" color="gray">·</Text>
          <Text size="1" color="gray">{rows.length} 条事件</Text>
          <Box style={{ marginLeft: "auto" }}><Text size="1" color="gray">artex-harness · AG-UI</Text></Box>
        </Flex>
      </div>
    </Theme>
  );
}

const clip = (s, n) => (String(s).length > n ? String(s).slice(0, n) + "…" : String(s));
const badgeColor = (s) =>
  s === "running" ? "amber" : s === "done" || s === "achieved" ? "green" : s === "failed" || s === "timeout" ? "red" : "gray";

function Row({ row }) {
  if (row.type === "turn")
    return (
      <Flex align="center" gap="3" mt="3">
        <Text size="1" color="gray" style={{ letterSpacing: ".08em" }}>{row.label}</Text>
        <Box style={{ flex: 1 }}><Separator size="4" /></Box>
      </Flex>
    );

  if (row.type === "tool")
    return (
      <Card size="1" variant="surface">
        <Flex direction="column" gap="2">
          <Flex align="center" gap="2">
            <Badge size="1" variant="soft" color="tomato">tool</Badge>
            <Text size="1" weight="medium">{row.name}</Text>
            {row.done ? null : <Badge size="1" variant="soft" color="amber">running</Badge>}
          </Flex>
          {row.args ? <Code size="1" className="mono" style={{ display: "block" }}>{row.args}</Code> : null}
          {row.result !== undefined ? (
            <Box><Separator size="4" /><Code size="1" className="mono" color={row.isError ? "red" : undefined} style={{ display: "block", marginTop: 8, maxHeight: 220, overflow: "auto" }}>{row.result || "(无输出)"}</Code></Box>
          ) : null}
        </Flex>
      </Card>
    );

  return <Text size="2" className="msg" color={row.role === "thinking" ? "gray" : undefined} style={row.role === "thinking" ? { fontStyle: "italic" } : undefined}>{row.text}</Text>;
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
