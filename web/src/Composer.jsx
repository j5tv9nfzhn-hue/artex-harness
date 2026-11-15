import { useEffect, useRef, useState } from "react";
import { Badge, Box, Flex, IconButton, Select, Separator, Text, TextField, Tooltip } from "@radix-ui/themes";
import { ArrowUp, Gauge, Lock, SendHorizontal, Sparkles } from "lucide-react";

/* 输入区：控件全部来自 Radix Themes（Select / TextField / IconButton / Badge / Tooltip），
 * 只有后端真的提供的选项才出现——
 *   模型      GET /api/llm/profiles + PUT /api/tasks/{id}/llm
 *   思考等级  来自当前 profile 的 thinking_type / reasoning_effort（只读展示，改在模型设置里）
 *   速度      运行时按输出字符/耗时实时估算；空闲时显示预算接口的累计
 *   权限      后端未暴露任务级开关 —— 明确禁用并说明，不做假控件
 */

const authHeaders = () => {
  let t = ""; try { t = localStorage.getItem("artex.harness.token") || ""; } catch {}
  return t ? { Authorization: "Bearer " + t } : {};
};

export default function Composer({ taskId, live, budget, onSent }) {
  const [profiles, setProfiles] = useState([]);
  const [profileId, setProfileId] = useState("");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [tps, setTps] = useState(null);
  const startedAt = useRef(0);
  const chars = useRef(0);

  useEffect(() => {
    (async () => {
      try {
        const r = await fetch("/api/llm/profiles", { headers: authHeaders(), credentials: "include" });
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

  const current = profiles.find((p) => String(p.id) === String(profileId));
  const thinking = describeThinking(current);

  async function changeModel(id) {
    setProfileId(id);
    if (!taskId) return;
    try {
      const r = await fetch(`/api/tasks/${encodeURIComponent(taskId)}/llm`, {
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
    if (!msg || !taskId) return;
    setBusy(true); setErr("");
    try {
      const r = await fetch(`/api/tasks/${encodeURIComponent(taskId)}/say`, {
        method: "POST", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ message: msg }),
      });
      if (!r.ok) throw new Error((await r.text()).slice(0, 160));
      setText(""); onSent?.();
    } catch (e) { setErr(e.message); } finally { setBusy(false); }
  }

  return (
    <Box px="4" py="3" style={{ borderTop: "1px solid var(--gray-a5)" }}>
      <Flex direction="column" gap="2">
        <TextField.Root
          placeholder={taskId ? "对 agent 说话（同一会话继续，不改历史）" : "先选择一个会话"}
          value={text} disabled={!taskId}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) send(); }}
        >
          <TextField.Slot><Sparkles size={14} /></TextField.Slot>
          <TextField.Slot side="right">
            <Tooltip content="发送（⌘/Ctrl+Enter）">
              <IconButton size="1" variant="solid" disabled={!text.trim() || busy} onClick={send} aria-label="发送">
                {busy ? <SendHorizontal size={13} /> : <ArrowUp size={13} />}
              </IconButton>
            </Tooltip>
          </TextField.Slot>
        </TextField.Root>

        <Flex align="center" gap="3" wrap="wrap">
          <Flex align="center" gap="1">
            <Text size="1" color="gray">模型</Text>
            <Select.Root value={profileId} onValueChange={changeModel} size="1" disabled={!profiles.length}>
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

          <Tooltip content="思考等级来自模型配置（在「模型设置」里改）">
            <Flex align="center" gap="1">
              <Text size="1" color="gray">思考</Text>
              <Badge size="1" variant="soft" color={thinking.color}>{thinking.label}</Badge>
            </Flex>
          </Tooltip>

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
