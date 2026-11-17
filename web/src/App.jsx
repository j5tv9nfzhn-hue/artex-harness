import { authedFetch, authHeaders, tokenCandidates } from "./lib/auth.js";
import LoginGate, { clearToken } from "./LoginGate.jsx";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Badge, Box, Button, Card, Code, Dialog, Flex, Heading, IconButton, ScrollArea, Separator,
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
import Subagents from "./Subagents.jsx";
import Trajectory from "./Trajectory.jsx";
import Checkpoints from "./Checkpoints.jsx";
import WorkspaceTabs from "./WorkspaceTabs.jsx";
import Workbench from "./Workbench.jsx";
import { useVirtualizer } from "@tanstack/react-virtual";
import { Area, AreaChart, ResponsiveContainer, Tooltip as ReTooltip } from "recharts";
import {
  Activity, Bot, BrainCircuit, CheckCircle2, ChevronRight, CircleDot, Command as CommandIcon,
  Search, ShieldAlert, SquareTerminal, Users, Wrench, XCircle,
} from "lucide-react";

/* 视觉层全部来自库：
 *   组件/排版/配色  → Radix Themes
 *   图标            → lucide-react
 *   动画            → motion
 *   Markdown        → react-markdown + remark-gfm
 *   命令面板        → cmdk
 *   图表            → recharts
 * 我们只负责布局与数据接线（AG-UI 事件流 + /api/tasks + /api/harness/budget）。 */

// Token handling lives in lib/auth.js so every panel agrees on which keys exist
// and on the 401-retry order. Reading a single key here used to leave whole
// screens unauthenticated when the session had logged in through the other one.
const token = () => tokenCandidates()[0] || "";

// 令牌失效时回到登录门：一处 401 就够，不要让每个面板各自报错。
// 用 window 事件解耦：api() 是模块级函数，拿不到 Console 的 state。
const UNAUTHORIZED_EVENT = "artex:unauthorized";
function notifyUnauthorized() {
  try { window.dispatchEvent(new Event(UNAUTHORIZED_EVENT)); } catch { /* ignore */ }
}

async function api(path) {
  const r = await authedFetch(path, { headers: authHeaders(), credentials: "include" });
  if (r.status === 401) notifyUnauthorized();
  if (!r.ok) throw new Error((await r.text()).slice(0, 160) || r.statusText);
  return r.json();
}

export default function App() {
  // 登录门：没有令牌就渲染 LoginGate，拿到令牌后才挂载整个工作台。
  // 之前控制台根本没有登录入口，首次部署打开 /app/ 只会看到满屏 401。
  const [authed, setAuthed] = useState(() => tokenCandidates().length > 0);
  if (!authed) return <Theme appearance="inherit" accentColor="tomato" grayColor="sand" radius="large"><LoginGate onAuthed={() => setAuthed(true)} /></Theme>;
  return <Console onLogout={() => { clearToken(); setAuthed(false); }} />;
}

function Console({ onLogout }) {
  const [tasks, setTasks] = useState([]);
  const [taskId, setTaskId] = useState(null);
  const [rows, setRows] = useState([]);
  const [live, setLive] = useState("idle");
  const [err, setErr] = useState("");
  const [budget, setBudget] = useState(null);
  const [palette, setPalette] = useState(false);
  const [view, setView] = useState("chat"); // chat | trajectory | checkpoints | settings
  const [tabs, setTabs] = useState([]);   // 打开过的会话/任务标签
  const [searchOpen, setSearchOpen] = useState(false);
  const [searchQ, setSearchQ] = useState("");
  const [searchHits, setSearchHits] = useState([]);
  const [pendingCount, setPendingCount] = useState(0);
  const [version, setVersion] = useState("");
  const [health, setHealth] = useState(null);
  const [appearance, setAppearance] = useState(() => { try { return localStorage.getItem("artex.appearance") || "inherit"; } catch { return "inherit"; } });
  const changeAppearance = (v) => { setAppearance(v); try { localStorage.setItem("artex.appearance", v); } catch {} };
  const [profiles, setProfiles] = useState([]);
  const abort = useRef(null);
  const scroller = useRef(null);
  const stick = useRef(true);
  // 长任务的事件上千条，全量渲染会拖死主线程；虚拟化只渲染视口内的行。
  // 连续的同类工具调用先合并成一行（如 "Bash × 6"），展开才渲染每一次。
  const displayRows = useMemo(() => groupRows(rows), [rows]);
  const rowVirtualizer = useVirtualizer({
    count: displayRows.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => 96,
    overscan: 10,
  });
  const retryNow = useCallback(() => setRetryNonce((n) => n + 1), []);
  const [showJump, setShowJump] = useState(false);
  const sinceRef = useRef(0);          // AG-UI 续传游标（= 最后一条事件的活动 id）
  const [retryNonce, setRetryNonce] = useState(0);
  const [subOpen, setSubOpen] = useState(false);
  const [benchOpen, setBenchOpen] = useState(false);
  const [newOpen, setNewOpen] = useState(false);   // 新建对话：侧栏/标题栏/命令面板共用一个
  const [stale, setStale] = useState(false);       // 页面跑的是旧包 → 提示刷新
  const [convs, setConvs] = useState([]);          // 对话（无目标，首条消息才开跑）
  const [convId, setConvId] = useState(null);
  const [convTitle, setConvTitle] = useState("");

  // 任何面板拿到 401（令牌过期、jwt.key 被换过）都回到登录门，
  // 而不是让每个面板各显示一遍"事件流不可用"。
  useEffect(() => {
    const onUnauthorized = () => onLogout?.();
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
  }, [onLogout]);

  const loadTasks = useCallback(async () => {
    try {
      const d = await api("/api/tasks");
        const list = Array.isArray(d) ? d : (d.tasks || d.items || []);
        setTasks(list);      if (list.length && !taskId) setTaskId(String(list[0].id));
      // 把当前任务加入标签（只加不删，切换标签不中断运行）
      setTabs((prev) => {
        const next = [...prev];
        for (const t of list.filter((x) => x.harness_mode).slice(0, 12)) {
          const id = String(t.id);
          if (!next.some((x) => x.type === "task" && String(x.id) === id)) {
            next.push({ type: "task", id, title: t.goal || t.description || "运行 #" + id, status: t.status });
          } else {
            const i = next.findIndex((x) => x.type === "task" && String(x.id) === id);
            next[i] = { ...next[i], status: t.status };
          }
        }
        return next;
      });
    } catch (e) { setErr("加载任务失败：" + e.message); }
  }, [taskId]);

  useEffect(() => { loadTasks(); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // 换任务时游标归零；同一个任务内重连沿用 since，只补缺口。
  useEffect(() => { sinceRef.current = 0; }, [taskId]);

  useEffect(() => {
    if (!taskId) return;
    abort.current?.abort();
    const ac = new AbortController(); abort.current = ac;
    let cursor = sinceRef.current;
    let attempt = 0;
    let timer = null;
    let stopped = false;
    if (cursor === 0) setRows([]);
    setLive(cursor ? "reconnecting" : "connecting");
    setErr("");

    const schedule = (msg) => {
      if (stopped) return;
      attempt += 1;
      const delay = Math.min(15000, 600 * Math.pow(1.8, Math.min(attempt, 6)));
      setLive("reconnecting");
      setErr(`${msg}｜第 ${attempt} 次重连，${(delay / 1000).toFixed(1)}s 后`);
      timer = setTimeout(connect, delay);
    };

    const connect = async () => {
      if (stopped) return;
      try {
        const r = await authedFetch(
          `/api/agui/agent?task=${encodeURIComponent(taskId)}&since=${cursor}&follow=1`,
          { headers: authHeaders(), credentials: "include", signal: ac.signal });
        if (!r.ok || !r.body) {
          const detail = (await r.text().catch(() => "")).slice(0, 160);
          throw new Error(`事件流不可用（HTTP ${r.status}）${detail ? "：" + detail : ""}`);
        }
        setLive("streaming"); setErr(""); attempt = 0;
        const reader = r.body.pipeThrough(new TextDecoderStream()).getReader();
        let buf = "";
        let sawFinish = false;
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += value;
          let i;
          while ((i = buf.indexOf("\n\n")) >= 0) {
            const chunk = buf.slice(0, i); buf = buf.slice(i + 2);
            const data = chunk.split("\n").filter((l) => l.startsWith("data:")).map((l) => l.slice(5).trim()).join("");
            if (!data) continue; // 注释帧（心跳）
            let ev; try { ev = JSON.parse(data); } catch { continue; }
            if (typeof ev.seq === "number" && ev.seq > cursor) { cursor = ev.seq; sinceRef.current = cursor; }
            if (ev.type === "RUN_FINISHED") sawFinish = true;
            if (ev.type === "RUN_ERROR") { schedule("服务端读取活动失败：" + (ev.message || "未知")); return; }
            if (ev.type === "TEXT_MESSAGE_CONTENT" && ev.delta) {
              window.dispatchEvent(new CustomEvent("agui:text", { detail: ev.delta }));
            }
            setRows((prev) => applyEvent(prev, ev));
          }
        }
        if (stopped) return;
        if (sawFinish) { setLive("done"); return; }
        schedule("连接被服务端或网络中断");
      } catch (e) {
        if (stopped || e.name === "AbortError") return;
        schedule(e.message || "连接失败");
      }
    };

    connect();
    return () => { stopped = true; clearTimeout(timer); ac.abort(); };
  }, [taskId, retryNonce]);

  useEffect(() => {
    api("/api/health").then((d) => { setVersion(d.version || ""); setHealth(d); }).catch(() => {});
    api("/api/llm/profiles").then((d) => setProfiles(Array.isArray(d) ? d : (d.profiles || []))).catch(() => {});
  }, []);

  // 待处理审批的全局徽标：长任务切到别的标签也不丢"有人在等我"。
  useEffect(() => {
    let dead = false;
    const tick = async () => {
      try {
        const r = await authedFetch("/api/intercept/pending", { headers: authHeaders(), credentials: "include" });
        if (r.ok && !dead) {
          const d = await r.json();
          const list = Array.isArray(d) ? d : (d.items || d.pending || []);
          setPendingCount(list.length);
        }
      } catch { /* 徽标不是关键路径 */ }
    };
    tick();
    const id = setInterval(tick, 15000);
    return () => { dead = true; clearInterval(id); };
  }, []);

  // 跨会话全文搜索：命中 summary 或完整 detail（后端按活动正文检索）。
  const runSearch = useCallback(async () => {
    const q = searchQ.trim();
    if (!q || !taskId) { setSearchHits([]); return; }
    try {
      const d = await api(`/api/harness/search?task=${encodeURIComponent(taskId)}&q=${encodeURIComponent(q)}&limit=50`);
      setSearchHits(d.results || []);
    } catch (e) { toast.error("搜索失败：" + e.message); }
  }, [searchQ, taskId]);

  useEffect(() => {
    if (!taskId) { setBudget(null); return; }
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
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "n") { e.preventDefault(); newConversation(); }
      if (e.key === "Escape") setPalette(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // 已经打开的标签页会一直跑旧包（入口 HTML 是 no-store，但已加载的 JS 不会自己换）。
  // 每次重建后行为都会变，用户却看不到，于是把"已经修好"当成"没修"。这里比对入口
  // HTML 引用的资源名，不一致就提示刷新。
  useEffect(() => {
    const mine = Array.from(document.querySelectorAll('script[type="module"]')).map((s) => s.src).join(",");
    let dead = false;
    const check = async () => {
      try {
        const r = await authedFetch("/app/index.html", { cache: "no-store" });
        if (!r.ok) return;
        const html = await r.text();
        const m = html.match(/assets\/index-[A-Za-z0-9_-]+\.js/);
        if (!dead && m && !mine.includes(m[0])) setStale(true);
      } catch { /* 离线就下次再说 */ }
    };
    const id = setInterval(check, 60000);
    check();
    return () => { dead = true; clearInterval(id); };
  }, []);

  async function control(action) {
    if (!taskId) return toast.error("先选择一个会话");    try {
      const r = await authedFetch(`/api/tasks/${encodeURIComponent(taskId)}/control`, {
        method: "POST", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ action }),
      });
      if (!r.ok) throw new Error((await r.text()).slice(0, 140));
      toast.success(action === "pause" ? "已暂停" : "已恢复");
    } catch (e) { toast.error(e.message); }
  }

  // ── 对话（conversation）───────────────────────────────────────────────
  // 后端本来就有这个实体：无 goal，发第一条消息才开始跑。它与「任务」（goal 驱动、
  // 有目标判定/子代理/预算）是两种东西，不能共用一个创建流程。
  useEffect(() => {
    authedFetch("/api/conversations")
      .then((r) => (r.ok ? r.json() : null))
      .then((d) => setConvs(d?.conversations || (Array.isArray(d) ? d : [])))
      .catch(() => {});
  }, []);

  const openTab = useCallback((tab) => {
    setTabs((prev) => prev.some((x) => x.type === tab.type && String(x.id) === String(tab.id))
      ? prev.map((x) => x.type === tab.type && String(x.id) === String(tab.id) ? { ...x, ...tab } : x)
      : [...prev, tab]);
  }, []);

  const pickTab = useCallback((tab) => {
    if (tab.type === "conv") { setConvId(String(tab.id)); setTaskId(null); setConvTitle(tab.title || "对话"); }
    else { setTaskId(String(tab.id)); setConvId(null); }
    setView("chat");
  }, []);

  const closeTab = useCallback((tab) => {
    setTabs((prev) => prev.filter((x) => !(x.type === tab.type && String(x.id) === String(tab.id))));
  }, []);

  const newConversation = useCallback(async () => {
    try {
      const r = await authedFetch("/api/conversations", {
        method: "POST", credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ agent_key: "harness" }),
      });
      if (!r.ok) throw new Error((await r.text()).slice(0, 160));
      const c = await r.json();
      setConvs((prev) => [c, ...prev.filter((x) => String(x.id) !== String(c.id))]);
      setConvId(String(c.id)); setConvTitle(c.title || "新对话"); setTaskId(null); setView("chat");
      openTab({ type: "conv", id: String(c.id), title: c.title || "新对话", status: "done" });
    } catch (e) { toast.error("新建对话失败：" + e.message); }
  }, [openTab]);

  const pickConversation = useCallback((id) => {
    setConvId(String(id)); setTaskId(null);
    const c = convs.find((x) => String(x.id) === String(id));
    setConvTitle(c?.title || "对话"); setView("chat");
    openTab({ type: "conv", id: String(id), title: c?.title || "对话", status: "done" });
  }, [convs, openTab]);

  // 对话用轮询（后端提供 ?since= 增量），不走 AG-UI SSE：它没有 task 活动流。
  useEffect(() => {
    if (!convId) return;
    let dead = false, cursor = 0, timer = null;
    setRows([]); setLive("connecting"); setErr("");
    const tick = async () => {
      try {
        const r = await authedFetch(`/api/conversations/${encodeURIComponent(convId)}/messages?since=${cursor}`);
        if (!r.ok) throw new Error(`事件流不可用（HTTP ${r.status}）${(await r.text()).slice(0, 100)}`);
        const d = await r.json();
        if (dead) return;
        if (Array.isArray(d.items) && d.items.length) {
          cursor = d.cursor ?? cursor;
          setRows((prev) => d.items.reduce(applyActivity, prev));
        }
        setLive(d.running ? "streaming" : "done");
        setErr("");
      } catch (e) {
        if (!dead) { setErr(e.message || "连接失败"); setLive("error"); }
      }
      if (!dead) timer = setTimeout(tick, 1200);
    };
    tick();
    return () => { dead = true; clearTimeout(timer); };
  }, [convId]);

  // HARNESS 的产品面只有两类对象：自由对话（conversation）与 HARNESS 运行
  // （task 且 harness_mode=true）。旧 ARTeX 控制台的 planner/worker 任务单列一组，
  // 不能混进主列表——否则整个界面看起来就是一个任务控制台。
  const harnessRuns = useMemo(() => tasks.filter((t) => t.harness_mode), [tasks]);
  const legacyRuns = useMemo(() => tasks.filter((t) => !t.harness_mode), [tasks]);
  const cur = useMemo(() => tasks.find((t) => String(t.id) === String(taskId)), [tasks, taskId]);
  const series = (budget && budget.series) || [];

  return (
    <Theme appearance={appearance} accentColor="tomato" grayColor="sand" radius="large">
      <Toaster position="bottom-center" richColors closeButton />
      <SettingsView open={view === "settings"} onOpenChange={(o) => setView(o ? "settings" : "chat")}
                    appearance={appearance} onAppearance={changeAppearance}
                    version={version} health={health} toast={toast} />
      <NewTaskDialog open={newOpen} onOpenChange={setNewOpen}
                     onCreated={(t) => { setTasks((prev) => [t, ...prev]); setTaskId(String(t.id)); }} />
      <div className="app">
        <Sidebar tasks={harnessRuns} legacy={legacyRuns} taskId={taskId} onPick={setTaskId}
                 convs={convs} convId={convId} onPickConv={pickConversation}
                 onNew={newConversation}
                 onPalette={() => setPalette(true)} profiles={profiles} version={version}
                 toast={toast} onFilter={() => setPalette(true)} onReload={loadTasks}
                 appearance={appearance} onAppearance={changeAppearance}
                 onOpenSettings={() => setView("settings")} onLogout={onLogout} />

        <div className="main">
          <WorkspaceTabs open={tabs} active={convId ? { type: "conv", id: convId } : taskId ? { type: "task", id: taskId } : null}
                         onPick={pickTab} onClose={closeTab} onNew={newConversation}
                         pendingByTask={pendingCount ? { [String(taskId)]: pendingCount } : {}} />
          <Flex align="center" gap="3" px="4" py="2" style={{ borderBottom: "1px solid var(--gray-a5)" }}>
            <Activity size={15} />
            <Text size="2" weight="medium" truncate style={{ maxWidth: "34vw" }}>
              {convId ? (convTitle || "对话")
                : cur ? (cur.goal || cur.description || "task " + cur.id) : "未选择对话或任务"}
            </Text>
            <Box style={{ marginLeft: "auto" }}>
              <Flex gap="2" align="center">
                <SegmentedView view={view} onChange={setView} disabled={convId || !taskId} />
                <Tooltip content="搜索本会话正文（summary + 完整 detail）">
                  <Button size="1" variant={searchOpen ? "solid" : "soft"} disabled={!taskId || !!convId}
                          onClick={() => setSearchOpen((v) => !v)}><Search size={12} /> 搜索</Button>
                </Tooltip>
                <Tooltip content="新建对话（⌘N）——不设目标，发第一条消息才开始">
                  <Button size="1" variant="soft" onClick={newConversation}><Plus size={13} /> 新建对话</Button>
                </Tooltip>
                {!convId ? (<>
                <Tooltip content="暂停任务（含正在跑的轮次）">
                  <IconButton size="1" variant="ghost" aria-label="暂停" onClick={() => control("pause")}><Pause size={13} /></IconButton>
                </Tooltip>
                <Tooltip content="恢复任务">
                  <IconButton size="1" variant="ghost" aria-label="恢复" onClick={() => control("resume")}><Play size={13} /></IconButton>
                </Tooltip>
                {budget ? <ContextRing pct={budget.pct} window={budget.window} last={budget.last_input} /> : null}
                {budget ? <BudgetSpark data={series} pct={budget.pct} /> : null}
                {pendingCount > 0 ? (
                  <Tooltip content={`${pendingCount} 个审批等待处理`}>
                    <Badge size="1" color="amber"><ShieldAlert size={11} /> {pendingCount}</Badge>
                  </Tooltip>
                ) : null}
                <Tooltip content="子代理：模型自己派出的 Agent 及其结果">
                  <Button size="1" variant={subOpen ? "solid" : "soft"} onClick={() => { setBenchOpen(false); setSubOpen((v) => !v); }}>
                    <Users size={12} /> 子代理
                  </Button>
                </Tooltip>
                <Tooltip content="工作台：文件树、DIFF 审阅、终端">
                  <Button size="1" variant={benchOpen ? "solid" : "soft"} onClick={() => { setSubOpen(false); setBenchOpen((v) => !v); }}>
                    <Wrench size={12} /> 工作台
                  </Button>
                </Tooltip>
                <StatusBadge status={cur && cur.status} />
                </>) : null}
                {!convId ? <Badge size="1" variant="outline" color="gray">AG-UI</Badge>
                          : <Badge size="1" variant="outline" color="gray">对话</Badge>}
                <LiveBadge live={live} />
              </Flex>
            </Box>
          </Flex>

          {searchOpen && !convId ? (
            <Flex align="center" gap="2" px="4" py="2" style={{ borderBottom: "1px solid var(--gray-a5)" }}>
              <Search size={13} />
              <RTextField.Root placeholder="搜索本会话的活动正文…" value={searchQ}
                onChange={(e) => setSearchQ(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") runSearch(); }} style={{ flex: 1 }} />
              <Button size="1" variant="soft" onClick={runSearch}>搜索</Button>
              <Button size="1" variant="ghost" onClick={() => { setSearchOpen(false); setSearchHits([]); setSearchQ(""); }}>关闭</Button>
              {searchHits.length ? <Badge size="1" variant="soft">{searchHits.length} 命中</Badge> : null}
            </Flex>
          ) : null}
          {searchOpen && searchHits.length ? (
            <Box px="4" py="2" style={{ maxHeight: 180, overflow: "auto", borderBottom: "1px solid var(--gray-a5)" }}>
              {searchHits.map((h) => (
                <Flex key={h.id} gap="2" py="1" align="start" style={{ borderBottom: "1px solid var(--gray-a3)" }}>
                  <Badge size="1" variant="soft">{h.kind}</Badge>
                  <Text size="1" className="mono" style={{ flex: 1, whiteSpace: "pre-wrap" }}>
                    {h.summary || h.detail?.slice(0, 160) || "(无正文)"}
                  </Text>
                  <Text size="1" color="gray">#{h.id}</Text>
                </Flex>
              ))}
            </Box>
          ) : null}

          {view === "trajectory" && taskId && !convId ? (
            <Trajectory taskId={taskId} onClose={() => setView("chat")} onLocate={() => setView("chat")} />
          ) : view === "checkpoints" && taskId && !convId ? (
            <Box style={{ flex: 1, minHeight: 0 }}><Checkpoints taskId={taskId} onRestored={() => setRetryNonce((n) => n + 1)} /></Box>
          ) : (
          <>
          <div className="stream" ref={scroller} onScroll={onStreamScroll}>
            {err ? (
              <Card size="1" style={{ borderColor: "var(--red-7)" }}>
                <Flex gap="3" align="center" wrap="wrap">
                  <XCircle size={14} />
                  <Text size="2" color="red" style={{ flex: 1, minWidth: 220 }}>{err}</Text>
                  <Button size="1" variant="soft" color="red" onClick={retryNow}>立即重连</Button>
                </Flex>
              </Card>
            ) : null}
            {!rows.length && !err ? (
              <Text size="2" color="gray">
                {convId ? "发第一条消息开始这段对话。" : "选择任务后，这里按轮次显示 AG-UI 事件流。"}
              </Text>
            ) : null}
            <div style={{ height: rowVirtualizer.getTotalSize(), position: "relative", width: "100%" }}>
              {rowVirtualizer.getVirtualItems().map((vi) => {
                const r = displayRows[vi.index];
                if (!r) return null;
                return (
                  <div key={r.key} data-index={vi.index} ref={rowVirtualizer.measureElement}
                       style={{ position: "absolute", top: 0, left: 0, width: "100%", transform: `translateY(${vi.start}px)` }}>
                    <Row row={r} />
                  </div>
                );
              })}
            </div>
          </div>
          {showJump ? (
            <motion.button className="jump-bottom" onClick={jumpToBottom}
                           initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }}>
              回到底部 ↓
            </motion.button>
          ) : null}
          <Composer taskId={taskId} convId={convId} live={live} budget={budget} task={cur}
                    onSent={() => setRetryNonce((n) => n + 1)} onTuned={loadTasks} />
          </>
          )}
        </div>

        {subOpen ? <Subagents taskId={taskId} live={live} onClose={() => setSubOpen(false)} /> : null}
        {benchOpen ? <Workbench taskId={taskId} onClose={() => setBenchOpen(false)} /> : null}

        <Flex className="statusbar" align="center" gap="3">
          <Flex align="center" gap="1"><LiveDot live={live} /><Text size="1" color="gray">{liveLabel(live)}</Text></Flex>
          <Text size="1" color="gray">·</Text>
          <Text size="1" color="gray">{convId ? "对话 #" + convId : taskId ? "#" + taskId : "无选择"}</Text>
          <Text size="1" color="gray">·</Text>
          <Text size="1" color="gray">{rows.length} 条事件</Text>
          {budget ? (<><Text size="1" color="gray">·</Text><Text size="1" color="gray">{series.length} 轮 · 压缩 {budget.compactions_observed?.total ?? budget.compactions ?? 0}</Text></>) : null}
          <Box style={{ marginLeft: "auto" }}><Text size="1" color="gray">Radix Themes · lucide · motion · cmdk · recharts</Text></Box>
        </Flex>

        {stale ? (
          <motion.button className="stale-pill" initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }}
                         onClick={() => location.reload()}>
            界面已更新 · 点此刷新
          </motion.button>
        ) : null}

        {palette ? (
          <Palette tasks={harnessRuns} onPick={(id) => { setTaskId(id); setConvId(null); setPalette(false); }}
                   onNew={newConversation} onNewTask={() => setNewOpen(true)} onClose={() => setPalette(false)} />
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

  if (row.type === "approval") return <ApprovalCard row={row} />;

  if (row.type === "toolgroup") return <ToolGroup row={row} />;

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

  if (row.role === "user")
    return (
      <div className="user-row">
        <Card size="1" style={{ background: "var(--accent-a3)", maxWidth: "78%" }}>
          <Text size="2" style={{ whiteSpace: "pre-wrap", lineHeight: 1.6 }}>{row.text}</Text>
        </Card>
      </div>
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

function Palette({ tasks, onPick, onNew, onNewTask, onClose }) {
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
            <Command.Group heading="操作">
              <Command.Item value="新建对话 新建会话 聊天 new chat" onSelect={() => { onClose(); onNew?.(); }}>
                <Flex align="center" gap="2" style={{ width: "100%" }}>
                  <Plus size={13} />
                  <Text size="2">新建对话</Text>
                  <Box style={{ marginLeft: "auto" }}><Text size="1" color="gray">⌘N · 不设目标</Text></Box>
                </Flex>
              </Command.Item>
              <Command.Item value="新建运行 长任务 渗透 目标 goal" onSelect={() => { onClose(); onNewTask?.(); }}>
                <Flex align="center" gap="2" style={{ width: "100%" }}>
                  <SquareTerminal size={13} />
                  <Text size="2">新建运行（带目标）</Text>
                  <Box style={{ marginLeft: "auto" }}><Text size="1" color="gray">长任务 / 渗透</Text></Box>
                </Flex>
              </Command.Item>
            </Command.Group>
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

function SegmentedView({ view, onChange, disabled }) {
  const items = [
    { key: "chat", label: "对话" },
    { key: "trajectory", label: "轨迹" },
    { key: "checkpoints", label: "检查点" },
  ];
  return (
    <Flex gap="1" className="view-seg" role="tablist" aria-label="工作区视图">
      {items.map((it) => (
        <button key={it.key} role="tab" aria-selected={view === it.key}
                className={"view-seg-btn" + (view === it.key ? " active" : "")}
                disabled={disabled && it.key !== "chat"}
                onClick={() => onChange(it.key)}>{it.label}</button>
      ))}
    </Flex>
  );
}

// ContextRing：上下文占用环。颜色分级与后端 budget.pct 一致（≥70 琥珀、≥90 红）。
function ContextRing({ pct, window, last }) {
  const v = Math.max(0, Math.min(100, Math.round(pct || 0)));
  const color = v >= 90 ? "var(--red-9)" : v >= 70 ? "var(--amber-9)" : "var(--green-9)";
  const deg = v * 3.6;
  return (
    <Tooltip content={`上下文占用 ${v}% · 窗口 ${window || "?"} · 最近输入 ${last || 0} tokens`}>
      <Box className="ctx-ring" style={{ background: `conic-gradient(${color} ${deg}deg, var(--gray-a4) 0)` }}
           title={`${v}%`}>
        <span>{v}</span>
      </Box>
    </Tooltip>
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

/* 就地审批卡片（Sprint 4）：intercept_request 以前只写进 DB，长任务会卡在审批上
 * 而界面上看不到——人必须先打开侧栏才发现。现在它出现在事件流里，允许/拒绝都
 * 直接调 /api/intercept/pending/{id}/decide，后端仍是唯一否决点。 */
function ApprovalCard({ row }) {
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(row.decided ? "已处理" : "");
  const [showInput, setShowInput] = useState(false);
  // The decide endpoint takes "allowed" | "denied". Sending "allow"/"deny" (the
  // wording used by the buttons and by the model-facing vocabulary) returns 400,
  // which made the card render but every click fail — the run stayed blocked.
  async function decide(choice) {
    const decision = choice === "allow" ? "allowed" : "denied";
    setBusy(true);
    try {
      const r = await authedFetch(`/api/intercept/pending/${encodeURIComponent(row.pendingId)}/decide`, {
        method: "POST", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ decision }),
      });
      if (!r.ok) throw new Error((await r.text()).slice(0, 160));
      setDone(choice === "allow" ? "已允许" : "已拒绝");
      toast.success(choice === "allow" ? "已允许执行" : "已拒绝执行");
    } catch (e) { toast.error("审批失败：" + e.message); }
    finally { setBusy(false); }
  }
  let pretty = row.input;
  try { pretty = JSON.stringify(JSON.parse(row.input), null, 2); } catch { /* keep raw */ }
  return (
    <motion.div initial={{ opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.16 }}>
      <Card size="1" style={{ borderColor: "var(--amber-7)", background: "var(--amber-a2)" }}>
        <Flex direction="column" gap="2">
          <Flex align="center" gap="2">
            <ShieldAlert size={14} color="var(--amber-9)" />
            <Text size="2" weight="medium">需要审批：{row.tool || "工具"}</Text>
            <Badge size="1" color="amber">#{row.pendingId}</Badge>
            <Box style={{ marginLeft: "auto" }} />
            {done ? <Badge size="1" variant="soft" color={done === "已允许" ? "green" : "gray"}>{done}</Badge> : null}
          </Flex>
          <Code size="1" variant="ghost" className="mono"
                style={{ display: "block", maxHeight: showInput ? 260 : 72, overflow: "auto", whiteSpace: "pre-wrap" }}>
            {pretty || "(无参数)"}
          </Code>
          <Flex gap="2" align="center">
            <Button size="1" variant="soft" onClick={() => setShowInput((v) => !v)}>
              {showInput ? "收起完整输入" : "显示完整输入"}
            </Button>
            <Box style={{ marginLeft: "auto" }} />
            <Button size="1" variant="soft" color="red" disabled={busy || !!done} onClick={() => decide("deny")}>拒绝</Button>
            <Button size="1" variant="solid" color="green" disabled={busy || !!done} onClick={() => decide("allow")}>允许</Button>
          </Flex>
        </Flex>
      </Card>
    </motion.div>
  );
}

/* 连续同类工具调用合并（Sprint 4）：一行 "Bash × 6"，点开才逐条渲染。
 * 长任务里几十次 read/grep 会把事件流刷成一堵墙，合并后扫一眼就知道做了什么。 */
function ToolGroup({ row }) {
  const [open, setOpen] = useState(false);
  const n = row.items.length;
  const failed = row.items.some((x) => x.isError);
  return (
    <motion.div initial={{ opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.16 }}>
      <Card size="1">
        <button className="toolgroup-head" onClick={() => setOpen((v) => !v)}>
          <ChevronRight size={12} style={{ transform: open ? "rotate(90deg)" : "none", transition: "transform .12s" }} />
          <Wrench size={13} color="var(--accent-11)" />
          <Text size="1" weight="medium">{row.name || "工具"} × {n}</Text>
          {failed ? <Badge size="1" color="red">有失败</Badge> : null}
          <Box style={{ marginLeft: "auto" }} />
          <Text size="1" color="gray">{open ? "收起" : "展开"}</Text>
        </button>
        {open ? (
          <Flex direction="column" gap="2" mt="2">
            {row.items.map((it) => (
              <Box key={it.key}>
                <Code size="1" variant="ghost" className="mono" style={{ display: "block", whiteSpace: "pre-wrap", maxHeight: 140, overflow: "auto" }}>
                  {it.args || "(无参数)"}
                </Code>
                {it.result !== undefined ? (
                  <Code size="1" variant="ghost" color={it.isError ? "red" : undefined} className="mono"
                        style={{ display: "block", marginTop: 4, whiteSpace: "pre-wrap", maxHeight: 180, overflow: "auto" }}>
                    {it.result || "(无输出)"}
                  </Code>
                ) : null}
              </Box>
            ))}
          </Flex>
        ) : null}
      </Card>
    </motion.div>
  );
}

function groupRows(rows) {
  const out = [];
  for (const r of rows) {
    if (r.type !== "tool") { out.push(r); continue; }
    const last = out[out.length - 1];
    if (last && last.type === "toolgroup" && last.name === r.name) {
      out[out.length - 1] = { ...last, items: [...last.items, r] };
      continue;
    }
    if (last && last.type === "tool" && last.name === r.name) {
      out[out.length - 1] = { type: "toolgroup", key: "g:" + last.key, name: r.name, items: [last, r] };
      continue;
    }
    out.push(r);
  }
  return out;
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

function NewTaskDialog({ open: openProp, onOpenChange, onCreated }) {
  // 受控 + 非受控两用：侧栏、标题栏、命令面板都指向同一个对话框，状态必须由 App 持有。
  const [openState, setOpenState] = useState(false);
  const open = openProp ?? openState;
  const setOpen = onOpenChange ?? setOpenState;
  const [goal, setGoal] = useState("");
  const [desc, setDesc] = useState("");
  const [harness, setHarness] = useState(true);
  const [busy, setBusy] = useState(false);

  async function create() {
    if (!goal.trim()) return toast.error("目标不能为空");
    setBusy(true);
    try {
      const r = await authedFetch("/api/tasks", {
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
  <Badge size="1" variant="soft" color={live === "streaming" ? "green" : live === "error" ? "red" : live === "reconnecting" ? "amber" : "gray"}>
    {liveLabel(live)}
  </Badge>
);

const LiveDot = ({ live }) => (
  <motion.span
    animate={live === "streaming" ? { opacity: [1, 0.35, 1] } : live === "reconnecting" ? { opacity: [1, 0.2, 1] } : { opacity: 1 }}
    transition={{ duration: live === "reconnecting" ? 0.9 : 2, repeat: live === "streaming" || live === "reconnecting" ? Infinity : 0 }}
    style={{ width: 6, height: 6, borderRadius: 99, display: "inline-block",
             background: live === "streaming" ? "var(--green-9)"
                       : live === "reconnecting" ? "var(--amber-9)"
                       : live === "error" ? "var(--red-9)" : "var(--gray-8)" }} />
);

const liveLabel = (s) => ({
  idle: "未连接", connecting: "连接中", streaming: "实时",
  reconnecting: "重连中", done: "已结束", error: "连接失败",
}[s] || s);
const clip = (s, n) => (String(s).length > n ? String(s).slice(0, n) + "…" : String(s));

function applyEvent(rows, ev) {
  const key = (n) => rows.length + "-" + n;
  switch (ev.type) {
    case "STEP_STARTED": return [...rows, { type: "turn", key: key("t"), label: ev.stepName || "turn" }];
    case "TEXT_MESSAGE_START": return [...rows, { type: "msg", key: key("m"), role: ev.role || "assistant", text: "", msgId: ev.messageId }];
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
    case "INTERCEPT_REQUEST": {
      let p = {};
      try { p = JSON.parse(ev.content || "{}"); } catch { /* 保留原文 */ }
      return [...rows, {
        type: "approval", key: key("ap"), pendingId: p.pending_id,
        tool: p.tool || "", input: typeof p.input === "string" ? p.input : JSON.stringify(p.input ?? {}, null, 2),
        decided: false,
      }];
    }
    default: return rows;
  }
}

function findLast(arr, pred) { for (let i = arr.length - 1; i >= 0; i--) if (pred(arr[i])) return i; return -1; }

// 把一条 activity DTO 折成一行。对话（conversation）用轮询拿 ?since= 增量，字段与
// AG-UI 那条路的来源是同一张 activity 表，所以行模型可以共用。
function applyActivity(rows, a) {
  const key = "a" + a.seq;
  const callId = a.tool_use_id || key;
  // detail 才是正文；summary 只是 firstLine(…,200/400) 的索引行。
  const body = a.detail && String(a.detail).trim() ? a.detail : (a.summary || "");
  switch (a.kind) {
    case "text": return [...rows, { type: "msg", key, role: "assistant", text: body }];
    // 对话里助手的最终回复是 kind=result（不是 text）；漏了它，界面上就只剩用户自己说的话。
    case "result": return [...rows, { type: "msg", key, role: "assistant", text: body }];
    // 对话里用户自己的话也是一条 activity（kind=user），不渲染就等于自己的输入消失了。
    case "user": return [...rows, { type: "msg", key, role: "user", text: body }];
    case "thinking": return [...rows, { type: "msg", key, role: "thinking", text: body }];
    case "tool_use": return [...rows, { type: "tool", key, callId, name: a.tool || "", args: body, done: false }];
    case "tool_result": {
      const i = findLast(rows, (r) => r.type === "tool" && r.callId === callId);
      if (i < 0) {
        return [...rows, { type: "tool", key, callId, name: "", args: "", done: true, result: body, isError: !!a.is_error }];
      }
      const next = rows.slice();
      next[i] = { ...next[i], done: true, result: body, isError: !!a.is_error };
      return next;
    }
    case "intercept_request": {
      let p = {};
      try { p = JSON.parse(body || "{}"); } catch { /* 保留原文 */ }
      return [...rows, {
        type: "approval", key, pendingId: p.pending_id,
        tool: p.tool || "", input: typeof p.input === "string" ? p.input : JSON.stringify(p.input ?? {}, null, 2),
        decided: false,
      }];
    }
    default: return rows;
  }
}
