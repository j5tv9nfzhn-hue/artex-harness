import { authedFetch, authHeaders } from "./lib/auth.js";
import { useEffect, useRef, useState } from "react";
import { Badge, Box, Flex, IconButton, Select, Separator, Text, TextArea, Tooltip } from "@radix-ui/themes";
import { ArrowUp, Gauge, Lock, SendHorizontal, Sparkles } from "lucide-react";

/* 输入区：控件全部来自 Radix Themes（Select / TextField / IconButton / Badge / Tooltip），
 * 只有后端真的提供的选项才出现——
 *   模型      GET /api/llm/profiles + PUT /api/tasks/{id}/llm
 *   思考等级  来自当前 profile 的 thinking_type / reasoning_effort（只读展示，改在模型设置里）
 *   速度      运行时按输出字符/耗时实时估算；空闲时显示预算接口的累计
 *   权限      后端未暴露任务级开关 —— 明确禁用并说明，不做假控件
 */

export default function Composer({ taskId, convId, live, budget, task, onSent, onTuned }) {
  const [profiles, setProfiles] = useState([]);
  const [profileId, setProfileId] = useState("");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [tps, setTps] = useState(null);
  const taRef = useRef(null);
  const [files, setFiles] = useState([]);
  const [mention, setMention] = useState(null); // {q, start} | null
  const [effort, setEffort] = useState("");     // 任务级思考深度；"" = 跟随模型
  const [efforts, setEfforts] = useState(["low", "medium", "high"]); // 该模型认的档位
  const draftKey = taskId ? `artex.draft.${taskId}` : "";
  const startedAt = useRef(0);
  const chars = useRef(0);

  // 任务切换时同步当前覆盖值（后端是唯一真相，别在本地臆测）。
  useEffect(() => { setEffort(task?.reasoning_effort || ""); }, [task?.reasoning_effort, taskId]);

  useEffect(() => {
    (async () => {
      try {
        const r = await authedFetch("/api/llm/profiles", { headers: authHeaders(), credentials: "include" });
        if (!r.ok) return;
        const d = await r.json();
        const list = Array.isArray(d) ? d : (d.profiles || []);
        setProfiles(list);
        const active = list.find((p) => p.is_default) || list[0];
        if (active) setProfileId(String(active.id));
      } catch { /* 选择器为空时只是不显示 */ }
    })();
  }, []);

  // 运行中按「已产出字符 / 已用时间」估算输出速度（字符≈token 的近似，标注为估算）
  useEffect(() => {
    if (live === "streaming") {
      if (!startedAt.current) { startedAt.current = Date.now(); chars.current = 0; }
      const id = setInterval(() => {
        const secs = (Date.now() - startedAt.current) / 1000;
        if (secs > 0.5) setTps(chars.current / secs);
      }, 700);
      return () => clearInterval(id);
    }
    startedAt.current = 0; chars.current = 0;
    if (live === "done") return; // 保留最后一次读数
    setTps(null);
  }, [live]);

  // 从事件流的文本增量里累计字符数（CustomEvent 由 App 转发）
  useEffect(() => {
    const onDelta = (e) => { chars.current += (e.detail || "").length; };
    window.addEventListener("agui:text", onDelta);
    return () => window.removeEventListener("agui:text", onDelta);
  }, []);

  // 草稿：按会话保存，切换会话不丢未发送内容
  useEffect(() => {
    if (!draftKey) return;
    let v = ""; try { v = localStorage.getItem(draftKey) || ""; } catch {}
    setText(v);
  }, [draftKey]);

  useEffect(() => {
    if (!draftKey) return;
    const id = setTimeout(() => { try { localStorage.setItem(draftKey, text); } catch {} }, 250);
    return () => clearTimeout(id);
  }, [text, draftKey]);

  // 工作区文件：给 @ 引用用。两个可能的路径都探一下，取能用的那个。
  useEffect(() => {
    if (!taskId) return;
    (async () => {
      for (const path of [`/api/harness/files/${encodeURIComponent(taskId)}`, `/api/tasks/${encodeURIComponent(taskId)}/files`]) {
        try {
          const r = await authedFetch(path, { headers: authHeaders(), credentials: "include" });
          if (!r.ok) continue;
          const d = await r.json();
          const nodes = d.nodes || d.files || (Array.isArray(d) ? d : []);
          const list = nodes.filter((n) => n && (n.path || n.name) && !n.dir && !n.is_dir)
                            .map((n) => n.path || n.name).slice(0, 800);
          if (list.length) { setFiles(list); return; }
        } catch { /* 试下一个 */ }
      }
    })();
  }, [taskId]);

  useEffect(() => {
    const el = taRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = Math.min(el.scrollHeight, 200) + "px";
  }, [text]);

  const current = profiles.find((p) => String(p.id) === String(profileId));
  const thinking = describeThinking(current);

  // 这个模型认哪些档位：问公开目录；目录不认识就退回通用三档，不硬塞 xhigh
  // 这种某些提供商直接 400 的值。
  useEffect(() => {
    const model = current?.model;
    if (!model) return;
    let dead = false;
    (async () => {
      try {
        const r = await authedFetch(`/api/llm/model-catalog?q=${encodeURIComponent(model)}`);
        if (!r.ok) return;
        const d = await r.json();
        const hit = (d.entries || []).find((e) => model.toLowerCase().startsWith(e.match));
        if (!dead && hit?.reasoning_efforts?.length) setEfforts(hit.reasoning_efforts);
      } catch { /* 目录不可用就保持默认档位 */ }
    })();
    return () => { dead = true; };
  }, [current?.model]);

  // 思考深度是**任务级覆盖**：写库后立刻对下一次调用生效，不动模型配置本身。
  async function changeEffort(v) {
    const prev = effort;
    setEffort(v); setErr("");
    if (!taskId) return;
    try {
      const r = await authedFetch(`/api/tasks/${encodeURIComponent(taskId)}/llm`, {
        method: "PUT", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ reasoning_effort: v }),
      });
      if (!r.ok) throw new Error((await r.text()).slice(0, 140));
      onTuned?.();
    } catch (e) {
      setEffort(prev);
      setErr("切换思考深度失败：" + e.message);
    }
  }

  async function changeModel(id) {
    setProfileId(id);
    if (!taskId) return;
    try {
      const r = await authedFetch(`/api/tasks/${encodeURIComponent(taskId)}/llm`, {
        method: "PUT", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ llm_profile_ids: [Number(id)] }),
      });
      if (!r.ok) setErr("切换模型失败：" + (await r.text()).slice(0, 120));
      else setErr("");
    } catch (e) { setErr(e.message); }
  }

  async function send() {
    const msg = text.trim();
    if (!msg) return;
    const url = convId
      ? `/api/conversations/${encodeURIComponent(convId)}/messages`
      : taskId ? `/api/tasks/${encodeURIComponent(taskId)}/say` : "";
    if (!url) return;
    setBusy(true); setErr("");
    try {
      const r = await authedFetch(url, {
        method: "POST", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ message: msg }),
      });
      if (!r.ok) throw new Error((await r.text()).slice(0, 160));
      setText(""); onSent?.();
    } catch (e) { setErr(e.message); } finally { setBusy(false); }
  }

  return (
    <Box px="4" py="3" style={{ borderTop: "1px solid var(--c-divider)" }}>
      <Flex direction="column" gap="2">
        {/* 多行输入：随内容自增高，到 200px 后内部滚动；Enter 发送、Shift+Enter 换行。
            单行 TextField 会把多行消息挤成一条，看不到自己写了什么。 */}
        <Box style={{ position: "relative" }}>
          <TextArea
            ref={taRef}
            placeholder={(taskId || convId) ? "对 agent 说话（Enter 发送 · Shift+Enter 换行）" : "先新建一个对话，或在左侧选择会话"}
            value={text} disabled={!taskId && !convId} rows={1}
            onChange={(e) => {
              const v = e.target.value;
              setText(v);
              const m = /@([\w./-]*)$/.exec(v);
              setMention(m ? { q: m[1].toLowerCase(), start: v.length - m[0].length } : null);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); send(); }
            }}
            style={{ minHeight: 46, maxHeight: 200, resize: "none", paddingRight: 44, lineHeight: 1.55 }}
          />
          <Tooltip content="发送（Enter）">
            <IconButton size="1" variant="solid" disabled={!text.trim() || busy} onClick={send} aria-label="发送"
                        style={{ position: "absolute", right: 8, bottom: 8 }}>
              {busy ? <SendHorizontal size={13} /> : <ArrowUp size={13} />}
            </IconButton>
          </Tooltip>
        </Box>

        {mention && files.length ? (
          <Box style={{ position: "relative" }}>
            <Box className="mention-list">
              {files.filter((f) => !mention.q || f.toLowerCase().includes(mention.q)).slice(0, 8).map((f) => (
                <div key={f} className="mention-item" onClick={() => {
                  const next = text.slice(0, mention.start) + "@" + f + " ";
                  setText(next); setMention(null); taRef.current?.focus();
                }}>{f}</div>
              ))}
            </Box>
          </Box>
        ) : null}

        <Flex align="center" gap="3" wrap="wrap">
          <Flex align="center" gap="1">
            <Text size="1" color="gray">模型</Text>
            <Select.Root value={profileId} onValueChange={changeModel} size="1" disabled={!profiles.length || !!convId}>
              <Select.Trigger variant="ghost" placeholder={profiles.length ? "选择模型" : "未配置"} />
              <Select.Content position="popper">
                {profiles.map((p) => (
                  <Select.Item key={p.id} value={String(p.id)}>
                    {p.name || p.model} <Text size="1" color="gray">· {p.model}</Text>
                  </Select.Item>
                ))}
              </Select.Content>
            </Select.Root>
          </Flex>

          <Separator orientation="vertical" size="1" />

          <Tooltip content="只作用于这个会话：任务级覆盖，不改模型配置。留空/跟随模型 = 用模型自己的设置">
            <Flex align="center" gap="1">
              <Text size="1" color="gray">思考</Text>
              <Select.Root value={effort || "inherit"} onValueChange={(v) => changeEffort(v === "inherit" ? "" : v)}
                           size="1" disabled={!taskId}>
                <Select.Trigger variant="ghost" />
                <Select.Content position="popper">
                  <Select.Item value="inherit">
                    跟随模型{current?.reasoning_effort ? `（${current.reasoning_effort}）` : ""}
                  </Select.Item>
                  {efforts.map((e) => (
                    <Select.Item key={e} value={e}>{EFFORT_LABEL[e] || e}</Select.Item>
                  ))}
                </Select.Content>
              </Select.Root>
            </Flex>
          </Tooltip>

          <Separator orientation="vertical" size="1" />

          {/* 上下文占用：最近一轮输入 token / 模型窗口。两者都来自 /api/harness/budget，
              超过 90% 变红——压缩阈值就在这个数字附近。 */}
          {budget && budget.window ? (
            <Tooltip content={`最近一轮输入 ${budget.last_input?.toLocaleString?.() ?? budget.last_input} tokens · 窗口 ${budget.window.toLocaleString()} tokens · 压缩阈值按窗口比例`}>
              <Flex align="center" gap="2">
                <Text size="1" color="gray">上下文</Text>
                <Text size="1" weight="medium" color={pctColor(budget.pct)}>{Math.round(budget.pct || 0)}%</Text>
                <Box style={{ width: 46, height: 4, borderRadius: 99, background: "var(--c-hover-plate)", overflow: "hidden" }}>
                  <Box style={{ width: Math.min(100, Math.round(budget.pct || 0)) + "%", height: "100%",
                                background: pctColor(budget.pct) === "red" ? "var(--c-status-bad)"
                                          : pctColor(budget.pct) === "amber" ? "var(--c-status-awaiting)" : "var(--c-accent)" }} />
                </Box>
                <Text size="1" color="gray">{fmtTokens(budget.last_input)} / {fmtTokens(budget.window)}</Text>
              </Flex>
            </Tooltip>
          ) : null}

          <Separator orientation="vertical" size="1" />

          <Tooltip content="估算：本轮流式输出字符数 / 已用时长">
            <Flex align="center" gap="1">
              <Gauge size={12} color="var(--gray-9)" />
              <Text size="1" color="gray">
                {tps != null ? `${tps.toFixed(1)} 字符/秒` : budget ? `累计 ${budget.totals?.output_tokens ?? 0} 输出` : "—"}
              </Text>
            </Flex>
          </Tooltip>

          <Separator orientation="vertical" size="1" />

          <Tooltip content="后端未提供任务级权限开关；当前由平台管控规则与审批门决定">
            <Flex align="center" gap="1" style={{ opacity: 0.6 }}>
              <Lock size={12} color="var(--gray-9)" />
              <Text size="1" color="gray">权限 · 由平台管控</Text>
            </Flex>
          </Tooltip>

          {err ? <Text size="1" color="red">{err}</Text> : null}
        </Flex>
      </Flex>
    </Box>
  );
}

function describeThinking(p) {
  const t = (p?.thinking_type || "").toLowerCase();
  const e = (p?.reasoning_effort || "").toLowerCase();
  if (e) return { label: e, color: e === "high" ? "amber" : "gray" };
  if (t) return { label: t, color: t === "enabled" || t === "on" ? "amber" : "gray" };
  return { label: "默认", color: "gray" };
}

const EFFORT_LABEL = {
  minimal: "极低", low: "低", medium: "中", high: "高", xhigh: "极高", max: "最大",
};

// token 数显示：1234 → 1.2K，1_200_000 → 1.2M。
const fmtTokens = (n) => {
  const v = Number(n) || 0;
  if (v >= 1000000) return (v / 1000000).toFixed(1) + "M";
  if (v >= 1000) return (v / 1000).toFixed(1) + "K";
  return String(v);
};

// 上下文占用配色：90% 以上是压缩阈值附近，必须一眼看到。
const pctColor = (p) => (Number(p) >= 90 ? "red" : Number(p) >= 70 ? "amber" : "gray");
