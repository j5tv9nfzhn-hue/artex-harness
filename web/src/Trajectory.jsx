import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge, Box, Button, Card, Code, Flex, IconButton, ScrollArea, Select, Separator, Text, TextField } from "./ui.jsx";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ArrowLeft, ChevronRight, Clock, Coins, Copy, Search, Wrench, X } from "lucide-react";
import { toast } from "sonner";
import { authedFetch } from "./lib/auth.js";

/* 轨迹视图（Sprint 2）：把 /api/harness/trajectory 的按轮次行模型渲染成流水表。
 * 与对话流分开：对话回答"说了什么"，轨迹回答"按什么顺序发生了什么、花了多少"。
 * 数据来自 replay 历史的只读投影，与 harness-gate 同源，不会和判定口径分叉。 */

async function api(path) {
  const r = await authedFetch(path, { credentials: "include" });
  if (!r.ok) throw new Error((await r.text()).slice(0, 160) || r.statusText);
  return r.json();
}

const kindColor = (k) => ({
  system: "violet", user: "blue", context: "gray", assistant: "green", tool: "orange",
}[k] || "gray");
const kindLabel = (k) => ({
  system: "系统", user: "用户", context: "上下文", assistant: "助手", tool: "工具",
}[k] || k);

export default function Trajectory({ taskId, onClose, onLocate }) {
  const [data, setData] = useState(null);
  const [err, setErr] = useState("");
  const [agent, setAgent] = useState("");
  const [q, setQ] = useState("");
  const [sel, setSel] = useState(null);       // selected row → detail panel
  const [hideTools, setHideTools] = useState(false);
  const [folded, setFolded] = useState({});
  const [loading, setLoading] = useState(false);
  const scroller = { current: null };
  const scrollerRef = (el) => { scroller.current = el; };

  const load = useCallback(async () => {
    if (!taskId) { setData(null); return; }
    setLoading(true); setErr("");
    try {
      const u = `/api/harness/trajectory?task=${encodeURIComponent(taskId)}${agent ? "&agent=" + encodeURIComponent(agent) : ""}`;
      const d = await api(u);
      setData(d);
    } catch (e) { setErr(e.message); setData(null); }
    finally { setLoading(false); }
  }, [taskId, agent]);
  useEffect(() => { load(); }, [load]);

  const turns = data?.view?.turns || [];
  const flat = useMemo(() => {
    const out = [];
    for (const t of turns) {
      out.push({ kind: "head", key: "h" + t.revision, turn: t });
      if (folded[t.revision]) continue;
      for (const r of t.rows) {
        if (hideTools && r.kind === "tool") continue;
        if (q && !(r.preview || "").toLowerCase().includes(q.toLowerCase()) && !(r.label || "").toLowerCase().includes(q.toLowerCase())) continue;
        out.push({ kind: "row", key: r.id, row: r, turn: t });
      }
    }
    return out;
  }, [turns, folded, hideTools, q]);

  const virt = useVirtualizer({
    count: flat.length,
    getScrollElement: () => scroller.current,
    estimateSize: (i) => flat[i].kind === "head" ? 34 : 30,
    overscan: 16,
  });

  const minimap = data?.view?.minimap || [];
  const subagents = data?.subagents || [];

  return (
    <div className="traj">
      <Flex align="center" gap="2" px="3" py="2" className="traj-head">
        <Button size="1" variant="ghost" onClick={onClose}><ArrowLeft size={13} /> 返回对话</Button>
        <Text size="2" weight="medium">轨迹</Text>
        <Badge size="1" variant="soft">{turns.length} 轮 · {data?.view?.total_tokens ?? 0} tokens</Badge>
        {subagents.length ? (
          <Select.Root value={agent || "all"} onValueChange={(v) => setAgent(v === "all" ? "" : v)}>
            <Select.Trigger placeholder="子代理" />
            <Select.Content>
              <Select.Item value="all">全部（主会话）</Select.Item>
              {subagents.map((a) => <Select.Item key={a} value={a}>{a}</Select.Item>)}
            </Select.Content>
          </Select.Root>
        ) : null}
        <Box style={{ marginLeft: "auto" }} />
        <TextField.Root size="1" placeholder="过滤行…" value={q} onChange={(e) => setQ(e.target.value)} style={{ width: 150 }}>
          <TextField.Slot><Search size={12} /></TextField.Slot>
        </TextField.Root>
        <Button size="1" variant={hideTools ? "solid" : "soft"} onClick={() => setHideTools((v) => !v)}>隐藏工具</Button>
        <IconButton size="1" variant="ghost" aria-label="刷新" onClick={load}>↻</IconButton>
      </Flex>

      {/* 迷你泳道图：点任意位置跳到对应轮次 */}
      <Flex align="end" gap="1" px="3" py="1" className="traj-minimap">
        {minimap.map((l) => (
          <button key={l.revision} className="lane" style={{ height: 8 + l.height * 0.32 }}
            title={`第 ${l.revision} 轮 · in ${l.input} / out ${l.output} · ${l.tools} 工具`}
            onClick={() => {
              const i = flat.findIndex((x) => x.kind === "head" && x.turn.revision === l.revision);
              if (i >= 0) virt.scrollToIndex(i, { align: "start" });
            }} />
        ))}
        <Text size="1" color="gray" ml="2">按轮次输入/输出 token 缩放</Text>
      </Flex>

      {err ? <Box px="3" py="2"><Text size="2" color="red">加载失败：{err} <Button size="1" variant="soft" onClick={load}>重试</Button></Text></Box> : null}
      {!err && !data && !loading ? <Box px="3" py="4"><Text size="2" color="gray">选择一个 HARNESS 运行后查看轨迹。</Text></Box> : null}
      {loading ? <Box px="3" py="2"><Text size="2" color="gray">加载中…</Text></Box> : null}

      <div className="traj-body">
        <div className="traj-list" ref={scrollerRef}>
          <div style={{ height: virt.getTotalSize(), position: "relative" }}>
            {virt.getVirtualItems().map((vi) => {
              const item = flat[vi.index];
              return (
                <div key={item.key} data-index={vi.index} ref={virt.measureElement}
                     style={{ position: "absolute", top: 0, left: 0, width: "100%", transform: `translateY(${vi.start}px)` }}>
                  {item.kind === "head" ? (
                    <Flex align="center" gap="2" className="traj-turn">
                      <IconButton size="1" variant="ghost" aria-label="折叠轮次"
                        onClick={() => setFolded((f) => ({ ...f, [item.turn.revision]: !f[item.turn.revision] }))}>
                        <ChevronRight size={12} style={{ transform: folded[item.turn.revision] ? "none" : "rotate(90deg)" }} />
                      </IconButton>
                      <Text size="1" weight="medium">第 {item.turn.revision} 轮</Text>
                      <Badge size="1" variant="soft" color="gray">{item.turn.kind || "turn"}</Badge>
                      <Text size="1" color="gray" truncate>{item.turn.summary}</Text>
                      <Box style={{ marginLeft: "auto" }} />
                      <Text size="1" color="gray" title="该轮首尾活动的时间差"><Clock size={10} /> {item.turn.duration_ms}ms</Text>
                      <Text size="1" color="gray" title="输入/输出 token"><Coins size={10} /> {item.turn.input_tokens}/{item.turn.output_tokens}</Text>
                      {item.turn.has_error ? <Badge size="1" color="red">有失败</Badge> : null}
                    </Flex>
                  ) : (
                    <button className={"traj-row" + (sel?.id === item.row.id ? " active" : "")}
                            onClick={() => setSel(item.row)}>
                      <span className="traj-kind" style={{ background: `var(--${kindColor(item.row.kind)}-a4)` }}>{kindLabel(item.row.kind)}</span>
                      <span className="traj-label">{item.row.label}</span>
                      <span className="traj-preview">{item.row.preview}</span>
                      {item.row.is_error ? <Badge size="1" color="red">err</Badge> : null}
                      {item.row.subagent ? <Badge size="1" variant="soft">{item.row.subagent}</Badge> : null}
                    </button>
                  )}
                </div>
              );
            })}
          </div>
        </div>

        {sel ? (
          <div className="traj-detail">
            <Flex align="center" gap="2" px="3" py="2">
              <Text size="2" weight="medium" truncate>{sel.label}</Text>
              <Badge size="1" color={kindColor(sel.kind)}>{kindLabel(sel.kind)}</Badge>
              <Box style={{ marginLeft: "auto" }} />
              <IconButton size="1" variant="ghost" aria-label="复制正文"
                onClick={() => navigator.clipboard?.writeText(sel.detail || sel.preview || "").then(() => toast.success("已复制"), () => toast.error("复制失败"))}>
                <Copy size={12} />
              </IconButton>
              <IconButton size="1" variant="ghost" aria-label="关闭详情" onClick={() => setSel(null)}><X size={12} /></IconButton>
            </Flex>
            <Separator size="4" />
            <ScrollArea style={{ height: "100%" }}>
              <Box px="3" py="2">
                <Text size="1" color="gray" as="div" mb="1">概述</Text>
                <Text size="2" as="div" mb="3" style={{ whiteSpace: "pre-wrap" }}>{sel.preview || "—"}</Text>
                <Text size="1" color="gray" as="div" mb="1">参数 / 结果（完整正文）</Text>
                <Code size="1" variant="ghost" className="mono" style={{ display: "block", whiteSpace: "pre-wrap", maxHeight: "46vh", overflow: "auto" }}>
                  {sel.detail || "(无正文)"}
                </Code>
                {sel.tool ? <Text size="1" color="gray" as="div" mt="2">工具：{sel.tool}{sel.tool_use_id ? " · " + sel.tool_use_id : ""}</Text> : null}
                {sel.input_tokens || sel.output_tokens ? <Text size="1" color="gray" as="div">tokens：in {sel.input_tokens} / out {sel.output_tokens}</Text> : null}
                {sel.subagent ? <Box mt="3"><Button size="1" variant="soft" onClick={() => setAgent(sel.subagent)}>查看子代理轨迹 →</Button></Box> : null}
                {onLocate ? <Box mt="3"><Button size="1" variant="soft" onClick={() => onLocate(sel)}>在对话中定位</Button></Box> : null}
              </Box>
            </ScrollArea>
          </div>
        ) : null}
      </div>

      {data?.system_versions?.length > 1 ? (
        <Flex px="3" py="1" gap="2" align="center" className="traj-foot">
          <Text size="1" color="gray">系统提示词 {data.system_versions.length} 个版本（轨迹里以「已更新」标注）</Text>
        </Flex>
      ) : null}
    </div>
  );
}
