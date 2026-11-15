import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Badge, Box, Card, Code, Dialog, Flex, Heading, IconButton, ScrollArea, Separator,
  Switch, Text, TextArea, TextField as RTextField, Theme, Tooltip,
} from "@radix-ui/themes";
import { motion, AnimatePresence } from "motion/react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Command } from "cmdk";
import { Toaster, toast } from "sonner";
import rehypeHighlight from "rehype-highlight";
import "highlight.js/styles/github.css";
import { Copy, Pause, Play, Plus } from "lucide-react";
import Sidebar from "./Sidebar.jsx";
import Composer from "./Composer.jsx";
import SettingsView from "./SettingsView.jsx";
import { Area, AreaChart, ResponsiveContainer, Tooltip as ReTooltip } from "recharts";
import {
  Activity, Bot, BrainCircuit, CheckCircle2, CircleDot, Command as CommandIcon,
  Search, SquareTerminal, Wrench, XCircle,
} from "lucide-react";

/* 视觉层全部来自库：
 *   组件/排版/配色  → Radix Themes
 *   图标            → lucide-react
 *   动画            → motion
 *   Markdown        → react-markdown + remark-gfm
 *   命令面板        → cmdk
 *   图表            → recharts
 * 我们只负责布局与数据接线（AG-UI 事件流 + /api/tasks + /api/harness/budget）。 */

const TOKEN_KEY = "artex.harness.token";
const token = () => { try { return localStorage.getItem(TOKEN_KEY) || ""; } catch { return ""; } };
const authHeaders = () => (token() ? { Authorization: "Bearer " + token() } : {});

async function api(path) {
  const r = await fetch(path, { headers: authHeaders(), credentials: "include" });
  if (!r.ok) throw new Error((await r.text()).slice(0, 160) || r.statusText);
  return r.json();
}

export default function App() {
  const [tasks, setTasks] = useState([]);
  const [taskId, setTaskId] = useState(null);
  const [rows, setRows] = useState([]);
  const [live, setLive] = useState("idle");
  const [err, setErr] = useState("");
  const [budget, setBudget] = useState(null);
  const [palette, setPalette] = useState(false);
  const [view, setView] = useState("chat"); // chat | settings
  const [version, setVersion] = useState("");
  const [health, setHealth] = useState(null);
  const [appearance, setAppearance] = useState(() => { try { return localStorage.getItem("artex.appearance") || "inherit"; } catch { return "inherit"; } });
  const changeAppearance = (v) => { setAppearance(v); try { localStorage.setItem("artex.appearance", v); } catch {} };
  const [profiles, setProfiles] = useState([]);
  const abort = useRef(null);
  const scroller = useRef(null);
  const stick = useRef(true);
  const [showJump, setShowJump] = useState(false);

  const loadTasks = useCallback(async () => {
    try {
      const d = await api("/api/tasks");
        const list = Array.isArray(d) ? d : (d.tasks || d.items || []);
        setTasks(list);
      if (list.length && !taskId) setTaskId(String(list[0].id));
    } catch (e) { setErr("加载任务失败：" + e.message); }
  }, [taskId]);

  useEffect(() => { loadTasks(); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!taskId) return;
    abort.current?.abort();
    const ac = new AbortController(); abort.current = ac;
    setRows([]); setLive("connecting"); setErr("");

    (async () => {
      try {
        const r = await fetch(`/api/agui/agent?task=${encodeURIComponent(taskId)}`,
          { headers: authHeaders(), credentials: "include", signal: ac.signal });
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
            if (ev.type === "TEXT_MESSAGE_CONTENT" && ev.delta) {
              window.dispatchEvent(new CustomEvent("agui:text", { detail: ev.delta }));
            }
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
    api("/api/health").then((d) => { setVersion(d.version || ""); setHealth(d); }).catch(() => {});
    api("/api/llm/profiles").then((d) => setProfiles(Array.isArray(d) ? d : (d.profiles || []))).catch(() => {});
  }, []);

  useEffect(() => {
    if (!taskId) return;
    api(`/api/harness/budget/${encodeURIComponent(taskId)}`).then(setBudget).catch(() => setBudget(null));
  }, [taskId, live]);

  // 粘底：内容一变（含流式追加）就跟到底；用户主动上滑则不打断，只提示"回到底部"。
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    if (stick.current) el.scrollTop = el.scrollHeight;
  });

  const onStreamScroll = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
    stick.current = atBottom;
    setShowJump(!atBottom);
  }, []);

  const jumpToBottom = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    stick.current = true;
    setShowJump(false);
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
  }, []);

  useEffect(() => {
    const onKey = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); setPalette((v) => !v); }
      if (e.key === "Escape") setPalette(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  async function control(action) {
    if (!taskId) return toast.error("先选择一个会话");
    try {
      const r = await fetch(`/api/tasks/${encodeURIComponent(taskId)}/control`, {
        method: "POST", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ action }),
      });
      if (!r.ok) throw new Error((await r.text()).slice(0, 140));
      toast.success(action === "pause" ? "已暂停" : "已恢复");
    } catch (e) { toast.error(e.message); }
  }

  const cur = useMemo(() => tasks.find((t) => String(t.id) === String(taskId)), [tasks, taskId]);
  const series = (budget && budget.series) || [];

  return (
    <Theme appearance={appearance} accentColor="tomato" grayColor="sand" radius="large">
      <Toaster position="bottom-center" richColors closeButton />
      <SettingsView open={view === "settings"} onOpenChange={(o) => setView(o ? "settings" : "chat")}
                    appearance={appearance} onAppearance={changeAppearance}
                    version={version} health={health} toast={toast} />
      <div className="app">
        <Sidebar tasks={tasks} taskId={taskId} onPick={setTaskId}
                 onPalette={() => setPalette(true)} profiles={profiles} version={version}
                 toast={toast} onFilter={() => setPalette(true)} onReload={loadTasks}
                 appearance={appearance} onAppearance={changeAppearance}
                 onOpenSettings={() => setView("settings")} />

        <div className="main">
          <Flex align="center" gap="3" px="4" py="3" style={{ borderBottom: "1px solid var(--gray-a5)" }}>
            <Activity size={15} />
            <Text size="2" weight="medium" truncate style={{ maxWidth: "42vw" }}>
              {cur ? (cur.goal || cur.description || "task " + cur.id) : "未选择任务"}
            </Text>
            <Box style={{ marginLeft: "auto" }}>
              <Flex gap="2" align="center">
                <NewTaskDialog onCreated={(t) => setTasks((prev) => [t, ...prev])} />
                <Tooltip content="暂停任务（含正在跑的轮次）">
                  <IconButton size="1" variant="ghost" aria-label="暂停" onClick={() => control("pause")}><Pause size={13} /></IconButton>
                </Tooltip>
                <Tooltip content="恢复任务">
                  <IconButton size="1" variant="ghost" aria-label="恢复" onClick={() => control("resume")}><Play size={13} /></IconButton>
                </Tooltip>
                {budget ? <BudgetSpark data={series} pct={budget.pct} /> : null}
                <StatusBadge status={cur && cur.status} />
                <Badge size="1" variant="outline" color="gray">AG-UI</Badge>
                <LiveBadge live={live} />
              </Flex>
            </Box>
          </Flex>

          <div className="stream" ref={scroller} onScroll={onStreamScroll}>
            {err ? (
              <Card size="1" style={{ borderColor: "var(--red-7)" }}>
                <Flex gap="2" align="center"><XCircle size={14} /><Text size="2" color="red">{err}</Text></Flex>
              </Card>
            ) : null}
            {!rows.length && !err ? <Text size="2" color="gray">选择任务后，这里按轮次显示 AG-UI 事件流。</Text> : null}
            <AnimatePresence initial={false}>
              {rows.map((r) => <Row key={r.key} row={r} />)}
            </AnimatePresence>
          </div>
          {showJump ? (
            <motion.button className="jump-bottom" onClick={jumpToBottom}
                           initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }}>
              回到底部 ↓
            </motion.button>
          ) : null}
          <Composer taskId={taskId} live={live} budget={budget} onSent={() => setLive("connecting")} />
        </div>

        <Flex className="statusbar" align="center" gap="3">
          <Flex align="center" gap="1"><LiveDot live={live} /><Text size="1" color="gray">{liveLabel(live)}</Text></Flex>
          <Text size="1" color="gray">·</Text>
          <Text size="1" color="gray">{taskId ? "#" + taskId : "无任务"}</Text>
          <Text size="1" color="gray">·</Text>
          <Text size="1" color="gray">{rows.length} 条事件</Text>
          {budget ? (<><Text size="1" color="gray">·</Text><Text size="1" color="gray">{series.length} 轮 · 压缩 {budget.compactions_observed?.total ?? budget.compactions ?? 0}</Text></>) : null}
          <Box style={{ marginLeft: "auto" }}><Text size="1" color="gray">Radix Themes · lucide · motion · cmdk · recharts</Text></Box>
        </Flex>

        {palette ? (
          <Palette tasks={tasks} onPick={(id) => { setTaskId(id); setPalette(false); }} onClose={() => setPalette(false)} />
        ) : null}
      </div>
    </Theme>
  );
}

function Row({ row }) {
  if (row.type === "turn")
    return (
      <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} transition={{ duration: 0.16 }}>
        <Flex align="center" gap="3" mt="3">
          <CircleDot size={12} color="var(--gray-8)" />
          <Text size="1" color="gray" style={{ letterSpacing: ".08em" }}>{row.label}</Text>
          <Box style={{ flex: 1 }}><Separator size="4" /></Box>
        </Flex>
      </motion.div>
    );

  if (row.type === "tool")
    return (
      <motion.div initial={{ opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.16, ease: [0.2, 0.7, 0.3, 1] }}
                  className="msg-wrap">
        <IconButton size="1" variant="ghost" color="gray" className="msg-copy" aria-label="复制这次工具调用"
                    onClick={() => copyText(`${row.name}\n${row.args}\n${row.result ?? ""}`)}>
          <Copy size={12} />
        </IconButton>
        <Card size="1">
          <Flex direction="column" gap="2">
            <Flex align="center" gap="2">
              <Wrench size={13} color="var(--accent-11)" />
              <Text size="1" weight="medium">{row.name}</Text>
              {row.done ? <CheckCircle2 size={12} color="var(--green-9)" /> : <Badge size="1" variant="soft" color="amber">running</Badge>}
            </Flex>
            {row.args ? (
              <Code size="1" variant="ghost" className="mono">{row.args}</Code>
            ) : null}
            {row.result !== undefined ? (
              <Box>
                <Separator size="4" />
                <Box mt="2">
                  <Code size="1" variant="ghost" color={row.isError ? "red" : undefined}
                        className="mono" style={{ display: "block", maxHeight: 220, overflow: "auto" }}>
                    {row.result || "(无输出)"}
                  </Code>
                </Box>
              </Box>
            ) : null}
          </Flex>
        </Card>
      </motion.div>
    );

  return (
    <motion.div initial={{ opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.16 }}
                className="msg-wrap">
      <IconButton size="1" variant="ghost" color="gray" className="msg-copy" aria-label="复制这条消息"
                  onClick={() => copyText(row.text || "")}>
        <Copy size={12} />
      </IconButton>
      <Box className={"md " + (row.role === "thinking" ? "thinking" : "")}>
        <Markdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeHighlight]} components={{ pre: CodeBlock }}>
          {row.text || ""}
        </Markdown>
      </Box>
    </motion.div>
  );
}

function Palette({ tasks, onPick, onClose }) {
  return (
    <div className="cmdk-overlay" onClick={onClose}>
      <div className="cmdk-box" onClick={(e) => e.stopPropagation()}>
        <Command label="命令面板">
          <Flex align="center" gap="2" px="3" style={{ borderBottom: "1px solid var(--gray-a5)" }}>
            <Search size={14} />
            <Command.Input placeholder="搜索任务…（↑↓ 选择，Enter 打开，Esc 关闭）" autoFocus />
          </Flex>
          <Command.List>
            <Command.Empty><Text size="2" color="gray">没有匹配项</Text></Command.Empty>
            <Command.Group heading="任务">
              {tasks.map((t) => (
                <Command.Item key={t.id} value={`${t.id} ${t.goal || ""} ${t.description || ""}`}
                              onSelect={() => onPick(String(t.id))}>
                  <Flex align="center" gap="2" style={{ width: "100%" }}>
                    <SquareTerminal size={13} />
                    <Text size="2" truncate>{clip(t.goal || t.description || "task " + t.id, 70)}</Text>
                    <Box style={{ marginLeft: "auto" }}><Text size="1" color="gray">#{t.id} · {t.status}</Text></Box>
                  </Flex>
                </Command.Item>
              ))}
            </Command.Group>
          </Command.List>
          <Flex px="3" py="2" gap="3" style={{ borderTop: "1px solid var(--gray-a5)" }}>
            <Text size="1" color="gray">⌘K 打开</Text><Text size="1" color="gray">Esc 关闭</Text>
          </Flex>
        </Command>
      </div>
    </div>
  );
}

function BudgetSpark({ data, pct }) {
  if (!data || data.length < 2) return null;
  const points = data.map((v, i) => ({ i, v }));
  return (
    <Tooltip content={`上下文占用 ${Math.round(pct || 0)}% · ${data.length} 轮`}>
      <Box style={{ width: 84, height: 22 }}>
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={points} margin={{ top: 2, right: 0, bottom: 0, left: 0 }}>
            <Area type="monotone" dataKey="v" stroke="var(--accent-9)" fill="var(--accent-4)" strokeWidth={1.5} isAnimationActive={false} />
            <ReTooltip content={() => null} />
          </AreaChart>
        </ResponsiveContainer>
      </Box>
    </Tooltip>
  );
}

function copyText(t) {
  navigator.clipboard?.writeText(t).then(() => toast.success("已复制"), () => toast.error("复制失败"));
}

function CodeBlock({ children }) {
  const ref = useRef(null);
  return (
    <div style={{ position: "relative" }}>
      <IconButton size="1" variant="ghost" color="gray" aria-label="复制代码"
                  style={{ position: "absolute", top: 6, right: 6 }}
                  onClick={() => {
                    const t = ref.current?.innerText || "";
                    navigator.clipboard?.writeText(t).then(() => toast.success("已复制"), () => toast.error("复制失败"));
                  }}>
        <Copy size={12} />
      </IconButton>
      <pre ref={ref}>{children}</pre>
    </div>
  );
}

function NewTaskDialog({ onCreated }) {
  const [open, setOpen] = useState(false);
  const [goal, setGoal] = useState("");
  const [desc, setDesc] = useState("");
  const [harness, setHarness] = useState(true);
  const [busy, setBusy] = useState(false);

  async function create() {
    if (!goal.trim()) return toast.error("目标不能为空");
    setBusy(true);
    try {
      const r = await fetch("/api/tasks", {
        method: "POST", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ goal: goal.trim(), description: desc.trim(), harness_mode: harness }),
      });
      if (!r.ok) throw new Error((await r.text()).slice(0, 160));
      const t = await r.json();
      toast.success("任务已创建 #" + t.id);
      setOpen(false); setGoal(""); setDesc("");
      onCreated?.(t);
    } catch (e) { toast.error("创建失败：" + e.message); } finally { setBusy(false); }
  }

  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger>
        <IconButton size="1" variant="soft" aria-label="新建任务"><Plus size={13} /></IconButton>
      </Dialog.Trigger>
      <Dialog.Content maxWidth="520px">
        <Dialog.Title size="4">新建任务</Dialog.Title>
        <Dialog.Description size="1" color="gray">目标决定 agent 做什么；创建后会自动生成一个短标题。</Dialog.Description>
        <Flex direction="column" gap="3" mt="3">
          <Box>
            <Text size="1" color="gray" as="div" mb="1">目标</Text>
            <TextArea placeholder="例如：只读侦察 127.0.0.1:8899 的 /api/health 与 /console/，报告状态码"
                      value={goal} onChange={(e) => setGoal(e.target.value)} rows={3} />
          </Box>
          <Box>
            <Text size="1" color="gray" as="div" mb="1">描述（可选）</Text>
            <RTextField.Root placeholder="补充背景、范围或约束" value={desc} onChange={(e) => setDesc(e.target.value)} />
          </Box>
          <Flex align="center" gap="2">
            <Switch checked={harness} onCheckedChange={setHarness} />
            <Text size="1" color="gray">HARNESS 会话模式（单 agent 对话式）</Text>
          </Flex>
        </Flex>
        <Flex gap="3" mt="4" justify="end">
          <Dialog.Close><button className="ghost-btn">取消</button></Dialog.Close>
          <IconButton variant="solid" disabled={busy} onClick={create}>{busy ? "创建中…" : "创建"}</IconButton>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}

const StatusBadge = ({ status }) => (
  <Badge size="1" variant="soft" color={status === "running" ? "amber" : status === "done" || status === "achieved" ? "green" : status === "failed" || status === "timeout" ? "red" : "gray"}>
    {status || "—"}
  </Badge>
);

const LiveBadge = ({ live }) => (
  <Badge size="1" variant="soft" color={live === "streaming" ? "green" : live === "error" ? "red" : "gray"}>
    {liveLabel(live)}
  </Badge>
);

const LiveDot = ({ live }) => (
  <motion.span
    animate={live === "streaming" ? { opacity: [1, 0.35, 1] } : { opacity: 1 }}
    transition={{ duration: 2, repeat: live === "streaming" ? Infinity : 0 }}
    style={{ width: 6, height: 6, borderRadius: 99, display: "inline-block",
             background: live === "streaming" ? "var(--green-9)" : live === "error" ? "var(--red-9)" : "var(--gray-8)" }} />
);

const liveLabel = (s) => ({ idle: "未连接", connecting: "连接中", streaming: "实时", done: "已结束", error: "连接失败" }[s] || s);
const clip = (s, n) => (String(s).length > n ? String(s).slice(0, n) + "…" : String(s));

function applyEvent(rows, ev) {
  const key = (n) => rows.length + "-" + n;
  switch (ev.type) {
    case "STEP_STARTED": return [...rows, { type: "turn", key: key("t"), label: ev.stepName || "turn" }];
    case "TEXT_MESSAGE_START": return [...rows, { type: "msg", key: key("m"), role: "assistant", text: "", msgId: ev.messageId }];
    case "TEXT_MESSAGE_CONTENT": {
      const i = findLast(rows, (r) => r.type === "msg" && r.msgId === ev.messageId);
      if (i < 0) return [...rows, { type: "msg", key: key("m"), role: "assistant", text: ev.delta || "", msgId: ev.messageId }];
      const next = rows.slice(); next[i] = { ...next[i], text: (next[i].text || "") + (ev.delta || "") }; return next;
    }
    case "TOOL_CALL_START": return [...rows, { type: "tool", key: key("x"), callId: ev.toolCallId, name: ev.toolCallName, args: "", done: false }];
    case "TOOL_CALL_ARGS": {
      const i = findLast(rows, (r) => r.callId === ev.toolCallId); if (i < 0) return rows;
      const next = rows.slice(); next[i] = { ...next[i], args: (next[i].args || "") + (ev.delta || "") }; return next;
    }
    case "TOOL_CALL_END": {
      const i = findLast(rows, (r) => r.callId === ev.toolCallId); if (i < 0) return rows;
      const next = rows.slice(); next[i] = { ...next[i], done: true }; return next;
    }
    case "TOOL_CALL_RESULT": {
      const i = findLast(rows, (r) => r.callId === ev.toolCallId); if (i < 0) return rows;
      const next = rows.slice(); next[i] = { ...next[i], done: true, result: ev.content ?? "", isError: ev.status === "error" }; return next;
    }
    default: return rows;
  }
}

function findLast(arr, pred) { for (let i = arr.length - 1; i >= 0; i--) if (pred(arr[i])) return i; return -1; }
