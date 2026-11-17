import { useCallback, useEffect, useState } from "react";
import { Badge, Box, Button, Code, Flex, IconButton, ScrollArea, Separator, Text, TextField, Tooltip } from "./ui.jsx";
import { Image as ImageIcon, Monitor, Plus, RefreshCw, RotateCw, Trash2, X } from "lucide-react";
import { toast } from "./toast.jsx";
import { authedFetch, errorText } from "./lib/auth.js";

/* 浏览器 / F12 面：后端早已就位（harness/browser.Manager + 4 个端点），
 * 控制台此前一条都没接线。这里把真实能力接上，并把没有能力的地方写明。
 *
 *   GET  /api/harness/browser?task=N          configured/enabled/npx/ready + devtools + recent + console_errors
 *   GET  /api/harness/browser/sessions?task=  Manager 的会话视图（servers 状态 / tabs / last_used）
 *   POST /api/harness/browser/restart         {task} → 重建耗时 + 新会话
 *   GET/POST /api/harness/browser/tabs        {task, action: open|close, url|id}
 *   GET  /api/harness/shot/{hash}             内容寻址截图（immutable 缓存）
 *
 * 诚实边界：这里的「F12」= chrome-devtools 的最近动作 + 已解析的 console 错误。
 * 后端没有产出结构化的 network 表，所以界面不画一个空表格装样子，而是直接说清楚。
 */

async function api(path, init) {
  const r = await authedFetch(path, { credentials: "include", ...(init || {}) });
  if (!r.ok) throw new Error(await errorText(r));
  return r.json();
}

const SERVERS = { browser: "Playwright", "chrome-devtools": "DevTools (F12)" };

const stateTone = (v) => {
  if (!v) return "gray";
  if (v === "live") return "green";
  if (String(v).startsWith("broken")) return "red";
  return "amber";
};

const TABS = [
  { key: "session", label: "会话", icon: Monitor },
  { key: "f12", label: "F12", icon: Code },
  { key: "shot", label: "截图", icon: ImageIcon },
];

export default function Browser({ taskId, onClose }) {
  const [tab, setTab] = useState("session");
  const [status, setStatus] = useState(null);
  const [sess, setSess] = useState(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const [shot, setShot] = useState(null);
  const [url, setUrl] = useState("");

  const load = useCallback(async () => {
    if (!taskId) { setStatus(null); setSess(null); return; }
    const qs = `?task=${encodeURIComponent(taskId)}`;
    try {
      const [st, se] = await Promise.all([
        api(`/api/harness/browser${qs}`),
        api(`/api/harness/browser/sessions${qs}`).catch(() => null),
      ]);
      setStatus(st);
      setSess(se?.session || null);
      setErr("");
      const h = pickThumb(st?.recent);
      if (h) setShot(h);
    } catch (e) { setErr(e.message); }
  }, [taskId]);

  useEffect(() => { load(); }, [load]);

  const restart = useCallback(async () => {
    setBusy("restart");
    try {
      const d = await api("/api/harness/browser/restart", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ task: taskId }),
      });
      if (d.error) toast.error("重建未完全成功：" + d.error);
      else toast.success(`会话已重建（${d.elapsed_ms}ms）`);
      setSess(d.session || null);
      await load();
    } catch (e) { toast.error("重建失败：" + e.message); }
    finally { setBusy(""); }
  }, [taskId, load]);

  const openTab = useCallback(async () => {
    const target = url.trim();
    if (!target) return toast.error("先填一个地址");
    setBusy("tab");
    try {
      const d = await api("/api/harness/browser/tabs", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ task: taskId, action: "open", url: target }),
      });
      toast.success("已开标签 " + (d.tab?.id || ""));
      setUrl("");
      await load();
    } catch (e) { toast.error("开标签失败：" + e.message); }
    finally { setBusy(""); }
  }, [taskId, url, load]);

  const closeTab = useCallback(async (id) => {
    try {
      await api("/api/harness/browser/tabs", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ task: taskId, action: "close", id }),
      });
      await load();
    } catch (e) { toast.error("关标签失败：" + e.message); }
  }, [taskId, load]);

  const head = (
    <Flex align="center" gap="1" px="2" py="1" style={{ borderBottom: "1px solid var(--c-divider)" }}>
      {TABS.map((t) => {
        const Icon = t.icon;
        return (
          <button key={t.key} className={"bench-tab" + (tab === t.key ? " active" : "")}
                  onClick={() => setTab(t.key)}>
            <Icon size={12} /> {t.label}
          </button>
        );
      })}
      <Box style={{ marginLeft: "auto" }} />
      {taskId ? (
        <Tooltip content="刷新">
          <IconButton size="1" variant="ghost" aria-label="刷新浏览器状态" onClick={load}>
            <RefreshCw size={12} className={busy ? "spin" : ""} />
          </IconButton>
        </Tooltip>
      ) : null}
      <Tooltip content="关闭">
        <IconButton size="1" variant="ghost" aria-label="关闭浏览器面板" onClick={onClose}><X size={13} /></IconButton>
      </Tooltip>
    </Flex>
  );

  if (!taskId) {
    return (
      <div className="subpanel">
        {head}
        <Box px="3" py="3"><Text size="1" color="gray">先选择一个运行（任务）——浏览器会话是按任务持有的。</Text></Box>
      </div>
    );
  }

  const servers = sess?.servers || {};
  const devtools = status?.devtools || {};
  const recent = status?.recent || [];
  const cerrs = status?.console_errors || [];
  const tabs = sess?.tabs || [];

  return (
    <div className="subpanel">
      {head}

      {err ? (
        <Box px="3" py="2">
          <Text size="1" color="red">{err} <Button size="1" variant="soft" onClick={load}>重试</Button></Text>
        </Box>
      ) : null}

      {/* 就绪横幅：每个原因一句话，不合并成一个"不可用" */}
      {status && !status.configured ? (
        <Box px="3" py="2"><Text size="1" color="amber">{status.hint}</Text></Box>
      ) : null}
      {status?.configured && status.hint ? (
        <Box px="3" py="2"><Text size="1" color={status.ready ? "gray" : "amber"}>{status.hint}</Text></Box>
      ) : null}
      {status?.npx && !status.npx.found ? (
        <Box px="3" py="1"><Text size="1" color="red">找不到 npx：Playwright / DevTools MCP 都需要 Node。</Text></Box>
      ) : null}

      {tab === "session" ? (
        <ScrollArea style={{ flex: 1, minHeight: 0 }}>
          <Box px="3" py="2">
            <Flex align="center" gap="2" mb="2">
              <Text size="1" weight="medium">MCP 服务器</Text>
              <Box style={{ marginLeft: "auto" }} />
              <Tooltip content="重建会话：把两个 MCP server 重新拉起（断线/崩溃后用它）">
                <Button size="1" variant="soft" disabled={busy === "restart"} onClick={restart}>
                  <RotateCw size={12} className={busy === "restart" ? "spin" : ""} /> 重建会话
                </Button>
              </Tooltip>
            </Flex>
            {Object.keys(SERVERS).map((k) => {
              const v = servers[k];
              return (
                <Flex key={k} align="center" gap="2" py="1">
                  <Monitor size={12} style={{ color: "var(--c-ink-4)" }} />
                  <Text size="1">{SERVERS[k]}</Text>
                  <Box style={{ marginLeft: "auto" }} />
                  <Badge size="1" variant="soft" color={stateTone(v)}>{v || "未启动"}</Badge>
                </Flex>
              );
            })}
            <Text size="1" color="gray" style={{ display: "block", marginTop: 6, lineHeight: 1.6 }}>
              会话按任务持有：Playwright 与 DevTools 共用一个会话（F12 连的是同一个上下文）。
              一个服务器崩掉不会带走另一个，下一次工具调用会重建它。
            </Text>

            <Separator size="4" style={{ margin: "12px 0" }} />

            <Flex align="center" gap="2" mb="2">
              <Text size="1" weight="medium">标签</Text>
              <Text size="1" color="gray">{tabs.length}</Text>
              <Box style={{ marginLeft: "auto" }} />
              <Text size="1" color="gray">
                {sess?.last_used_unix ? "最近使用 " + ago(sess.last_used_unix) : "尚未使用"}
              </Text>
            </Flex>
            <Flex gap="2" mb="2">
              <Box style={{ flex: 1, minWidth: 0 }}>
                <TextField.Root size="1" value={url} placeholder="http://127.0.0.1:8080/"
                                onChange={(e) => setUrl(e.target.value)}
                                onKeyDown={(e) => { if (e.key === "Enter") openTab(); }} />
              </Box>
              <Button size="1" variant="soft" disabled={busy === "tab"} onClick={openTab}>
                <Plus size={12} /> 开标签
              </Button>
            </Flex>
            {tabs.length ? tabs.map((t) => (
              <Flex key={t.id} align="center" gap="2" py="1">
                <Badge size="1" variant="soft" color="gray">{t.id}</Badge>
                <Text size="1" truncate title={t.url || ""}>{t.title || t.url || "(空白标签)"}</Text>
                <Box style={{ marginLeft: "auto" }} />
                <IconButton size="1" variant="ghost" aria-label={"关闭标签 " + t.id} onClick={() => closeTab(t.id)}>
                  <Trash2 size={12} />
                </IconButton>
              </Flex>
            )) : (
              <Text size="1" color="gray">还没有标签。标签是控制台侧的视图模型，开一个不会打断 agent。</Text>
            )}
          </Box>
        </ScrollArea>
      ) : null}

      {tab === "f12" ? (
        <ScrollArea style={{ flex: 1, minHeight: 0 }}>
          <Box px="3" py="2">
            <Flex align="center" gap="2" mb="2">
              <Text size="1" weight="medium">DevTools（F12）</Text>
              <Badge size="1" variant="soft" color={stateTone(servers["chrome-devtools"])}>
                {servers["chrome-devtools"] || (devtools.ready ? "ready" : "未连通")}
              </Badge>
            </Flex>
            {status?.f12_hint ? (
              <Text size="1" color="amber" style={{ display: "block", marginBottom: 8, lineHeight: 1.6 }}>
                {status.f12_hint}
              </Text>
            ) : null}

            <Text size="1" weight="medium" style={{ display: "block", marginBottom: 4 }}>
              Console 错误（{cerrs.length}）
            </Text>
            {cerrs.length ? cerrs.map((c) => (
              <div key={c.id} className="console-row">
                <Badge size="1" variant="soft" color={c.level === "error" ? "red" : "amber"}>{c.level || "log"}</Badge>
                <span className="console-text">{c.text}</span>
              </div>
            )) : (
              <Text size="1" color="gray">
                还没有抓到 console 错误。agent 用 list_console_messages 读到的错误会出现在这里。
              </Text>
            )}
            {status?.console_errors_unparsed ? (
              <Text size="1" color="gray" style={{ display: "block", marginTop: 6 }}>
                另有 {status.console_errors_unparsed} 条工具输出无法结构化解析——原文见下面的最近动作。
              </Text>
            ) : null}

            <Separator size="4" style={{ margin: "12px 0" }} />

            <Text size="1" weight="medium" style={{ display: "block", marginBottom: 4 }}>
              最近浏览器动作（{recent.length}）
            </Text>
            {recent.length ? recent.map((r) => (
              <Flex key={r.id} align="center" gap="2" py="1">
                <Badge size="1" variant="soft" color={r.tool.includes("chrome-devtools") ? "violet" : "blue"}>
                  {r.tool.replace(/^mcp__/, "").replace("__", " · ")}
                </Badge>
                <Text size="1" color="gray" truncate title={r.summary}>{r.summary}</Text>
              </Flex>
            )) : (
              <Text size="1" color="gray">agent 还没用过浏览器工具。</Text>
            )}
            <Text size="1" color="gray" style={{ display: "block", marginTop: 8, lineHeight: 1.6 }}>
              完整 network 面板不在这个版本里：后端只把 chrome-devtools 的输出按 console 级别解析，
              没有产出结构化的请求/响应表。逐条看请用上面对应动作的原文（事件流里同样可见）。
            </Text>
          </Box>
        </ScrollArea>
      ) : null}

      {tab === "shot" ? (
        <ScrollArea style={{ flex: 1, minHeight: 0 }}>
          <Box px="3" py="2">
            {shot ? (
              <>
                <Flex align="center" gap="2" mb="2">
                  <Text size="1" weight="medium">最近截图</Text>
                  <Text size="1" color="gray" className="mono" truncate>{shot.slice(0, 12)}…</Text>
                  <Box style={{ marginLeft: "auto" }} />
                  <a className="linkbtn" href={`/api/harness/shot/${shot}`} target="_blank" rel="noreferrer">看原图</a>
                </Flex>
                <img className="shot" src={`/api/harness/shot/${shot}`} alt="浏览器最近一次截图" />
                <Text size="1" color="gray" style={{ display: "block", marginTop: 8, lineHeight: 1.6 }}>
                  截图是内容寻址的（同图只存一份，24h TTL，上限 2000 张）。
                  agent 调 browser_take_screenshot 后结果里带 <code>[thumb] &lt;hash&gt;</code>，这里就渲染它。
                </Text>
              </>
            ) : (
              <Text size="1" color="gray">
                还没有截图。让 agent 调一次 browser_take_screenshot，或先用上面的工具跑一轮浏览器动作。
              </Text>
            )}
          </Box>
        </ScrollArea>
      ) : null}
    </div>
  );
}

/* pickThumb 读取截图管线写进工具结果摘要里的内容哈希（"[thumb] <sha256>"）。
 * 这是 agent 侧与这个面板之间唯一的契约。 */
function pickThumb(recent) {
  if (!Array.isArray(recent)) return null;
  for (const r of recent) {
    const m = /\[thumb\]\s*([0-9a-f]{8,64})/i.exec(r.summary || "");
    if (m) return m[1];
  }
  return null;
}

function ago(unix) {
  const s = Math.max(0, Math.floor(Date.now() / 1000 - unix));
  if (s < 60) return s + "s 前";
  if (s < 3600) return Math.floor(s / 60) + "m 前";
  return Math.floor(s / 3600) + "h 前";
}