import { authedFetch } from "./lib/auth.js";
import { useCallback, useEffect, useRef, useState } from "react";
import { Badge, Box, Button, Card, Code, Flex, IconButton, ScrollArea, Separator, Text, Tooltip } from "@radix-ui/themes";
import { motion } from "motion/react";
import { ArrowLeft, ChevronRight, Clock, Loader2, RefreshCw, Square, Users, X, XCircle } from "lucide-react";
import { toast } from "sonner";

/* 子代理侧栏（对照 DSH packages/client/ui-subagent）：
 *   section.title / section.empty   —— 分节标题与空态
 *   progress.*                      —— 进行中 / 已完成 / 失败 计数
 *   列表项                          —— 类型、状态、耗时、提示词
 *   点进去                          —— 该子代理自己的会话：思考 / 文本 / 工具调用与结果
 *   load.error / retry              —— 加载失败可重试，绝不静默
 *
 * 触发时机（读代码得出的，不是猜的）：
 *   · HARNESS 模式下没有 worker 池；派不派子代理由模型自己判断（Agent 工具）
 *   · 子代理定义里不含 Agent 工具 → 不会再派第三代（深度守卫见 agent/harness.go）
 *   · 每个子代理有自己的 10 分钟预算，父轮次被取消不会连带杀掉已在跑的子代理
 *
 * 数据：
 *   GET /api/harness/workers/{id}            → 去重后的委派列表（含 agent_id、耗时）
 *   GET /api/harness/subagent/{agent}?task=N → 该子代理的步骤（来自磁盘 sidechain）
 */

async function jget(path) {
  const r = await authedFetch(path);
  if (!r.ok) throw new Error(`HTTP ${r.status}：${(await r.text()).slice(0, 140)}`);
  return r.json();
}

function fmtDuration(ms) {
  if (!ms || ms < 0) return "—";
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}秒`;
  const m = Math.floor(s / 60), rs = s % 60;
  if (m < 60) return `${m}分${rs}秒`;
  return `${Math.floor(m / 60)}小时${m % 60}分`;
}

const hhmmss = (ts) => {
  if (!ts) return "";
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? "" : d.toTimeString().slice(0, 8);
};

const STATE = {
  running: { label: "运行中", color: "amber" },
  done: { label: "已完成", color: "green" },
  error: { label: "失败", color: "red" },
};

const AGENT_LABEL = { recon: "侦察", verify: "验证" };

export default function Subagents({ taskId, live, onClose }) {
  const [rows, setRows] = useState(null);       // null = 未加载
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [sel, setSel] = useState(null);         // 选中的委派
  const [detail, setDetail] = useState(null);   // {steps, note}
  const [detailErr, setDetailErr] = useState("");
  const [detailBusy, setDetailBusy] = useState(false);
  const alive = useRef(true);

  const load = useCallback(async (silent) => {
    if (!taskId) { setRows([]); return; }
    if (!silent) setBusy(true);
    try {
      const d = await jget(`/api/harness/workers/${encodeURIComponent(taskId)}`);
      if (!alive.current) return;
      setRows(Array.isArray(d.delegations) ? d.delegations : []);
      setErr("");
    } catch (e) {
      if (!alive.current) return;
      setErr(e.message || "无法加载子智能体");
      if (!silent) setRows(null);
    } finally {
      if (alive.current && !silent) setBusy(false);
    }
  }, [taskId]);

  useEffect(() => { alive.current = true; load(false); return () => { alive.current = false; }; }, [load]);
  useEffect(() => { setSel(null); setDetail(null); setDetailErr(""); }, [taskId]);

  // 有子代理在跑、或主事件流还在跑时轮询；都停下就不再打扰后端。
  const anyRunning = (rows || []).some((r) => r.state === "running");
  useEffect(() => {
    if (!taskId || sel) return; // 看明细时不刷新列表，避免选中项被顶掉
    if (!anyRunning && live !== "streaming" && live !== "connecting") return;
    const t = setInterval(() => load(true), anyRunning ? 2000 : 4000);
    return () => clearInterval(t);
  }, [taskId, anyRunning, live, load, sel]);

  const openDetail = useCallback(async (row) => {
    setSel(row); setDetail(null); setDetailErr(""); setDetailBusy(true);
    try {
      const d = await jget(`/api/harness/subagent/${encodeURIComponent(row.agent_id)}?task=${encodeURIComponent(taskId)}`);
      if (!alive.current) return;
      setDetail(d);
    } catch (e) {
      if (alive.current) setDetailErr(e.message || "无法加载子代理明细");
    } finally {
      if (alive.current) setDetailBusy(false);
    }
  }, [taskId]);

  const total = (rows || []).length;
  const running = (rows || []).filter((r) => r.state === "running").length;
  const done = (rows || []).filter((r) => r.state === "done").length;
  const failed = (rows || []).filter((r) => r.state === "error").length;

  // 后台委派可单独停止（Sprint 3）。停止只影响这一个委派；同步委派随父轮次走，
  // 不在这里提供停止入口。
  async function stopDelegation(did) {
    try {
      const r = await authedFetch(`/api/harness/delegations/${encodeURIComponent(taskId)}/${encodeURIComponent(did)}/stop`, { method: "POST" });
      if (!r.ok) throw new Error((await r.text()).slice(0, 140));
      toast.success(`已停止后台委派 #${did}`);
      load(true);
    } catch (e) { toast.error("停止失败：" + e.message); }
  }

  return (
    <div className="subpanel">
      <Flex align="center" gap="2" px="3" py="2" style={{ borderBottom: "1px solid var(--c-divider)" }}>
        {sel ? (
          <>
            <Tooltip content="返回子代理列表">
              <IconButton size="1" variant="ghost" aria-label="返回子代理列表"
                          onClick={() => { setSel(null); setDetail(null); setDetailErr(""); }}>
                <ArrowLeft size={13} />
              </IconButton>
            </Tooltip>
            <Text size="2" weight="medium">子代理明细</Text>
          </>
        ) : (
          <>
            <Users size={14} />
            <Text size="2" weight="medium">子智能体</Text>
          </>
        )}
        {!sel && total ? (
          <Flex gap="2" align="center" style={{ marginLeft: "auto" }}>
            {running ? <Badge size="1" variant="soft" color="amber">{running} 进行中</Badge> : null}
            {done ? <Badge size="1" variant="soft" color="green">{done} 已完成</Badge> : null}
            {failed ? <Badge size="1" variant="soft" color="red">{failed} 失败</Badge> : null}
          </Flex>
        ) : null}
        <Tooltip content="刷新">
          <IconButton size="1" variant="ghost" aria-label="刷新子智能体" style={{ marginLeft: sel || !total ? "auto" : 0 }}
                      onClick={() => {
                        if (sel?.agent_id) openDetail(sel);
                        else load(false);
                      }}>
            <RefreshCw size={12} className={busy || detailBusy ? "spin" : ""} />
          </IconButton>
        </Tooltip>
        <Tooltip content="关闭">
          <IconButton size="1" variant="ghost" aria-label="关闭子代理面板" onClick={onClose}><X size={13} /></IconButton>
        </Tooltip>
      </Flex>

      <ScrollArea style={{ flex: 1, minHeight: 0 }}>
        <Box p="3">
          {sel ? (
            <SubagentDetail row={sel} detail={detail} err={detailErr} busy={detailBusy} onRetry={() => openDetail(sel)} />
          ) : (
            <SubagentList
              rows={rows} err={err} busy={busy} onRetry={() => load(false)}
              onOpen={openDetail} onStop={stopDelegation} anyRunning={anyRunning}
            />
          )}
        </Box>
      </ScrollArea>
    </div>
  );
}

function SubagentList({ rows, err, busy, onRetry, onOpen, onStop }) {
  const total = (rows || []).length;
  return (
    <>
      {err ? (
        <Card size="1" style={{ borderColor: "var(--red-7)", marginBottom: 10 }}>
          <Flex direction="column" gap="2">
            <Flex gap="2" align="center"><XCircle size={13} /><Text size="1" color="red">无法加载子智能体</Text></Flex>
            <Text size="1" color="gray">{err}</Text>
            <Button size="1" variant="soft" color="red" onClick={onRetry}>重试</Button>
          </Flex>
        </Card>
      ) : null}

      {rows === null && !err ? (
        <Flex align="center" gap="2" py="3"><Loader2 size={13} className="spin" /><Text size="1" color="gray">正在加载子智能体…</Text></Flex>
      ) : null}

      {rows !== null && !total ? (
        <Flex direction="column" gap="2">
          <Text size="1" weight="medium">暂无子智能体</Text>
          <Text size="1" color="gray" style={{ lineHeight: 1.6 }}>
            HARNESS 没有 worker 池：派不派由模型自己判断。它判定某块工作能独立完成时，
            才会调用 Agent 派一个侦察或验证子代理。
          </Text>
          <Separator size="4" />
          <Text size="1" color="gray" style={{ lineHeight: 1.6 }}>
            限制：子代理不能再派子代理；每个子代理有独立的 10 分钟预算，父轮次被取消不会杀掉已在跑的子代理。
          </Text>
        </Flex>
      ) : null}

      <Flex direction="column" gap="2">
        {(rows || []).map((r) => {
          const st = STATE[r.state] || STATE.running;
          const clickable = !!r.agent_id;
          return (
            <motion.div key={r.activity_id} initial={{ opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.16 }}>
              <Card size="1" style={{ cursor: clickable ? "pointer" : "default" }}
                    onClick={clickable ? () => onOpen(r) : undefined}>
                <Flex direction="column" gap="2">
                  <Flex align="center" gap="2" wrap="wrap">
                    <Badge size="1" variant="soft" color="iris">{AGENT_LABEL[r.agent_type] || r.agent_type || "子代理"}</Badge>
                    {r.background ? <Badge size="1" variant="outline" color="purple">后台</Badge> : null}
                    <Badge size="1" variant="soft" color={st.color}>
                      {r.state === "running" ? <Loader2 size={10} className="spin" style={{ marginRight: 4 }} /> : null}
                      {st.label}
                    </Badge>
                    <Flex align="center" gap="1" style={{ marginLeft: "auto" }}>
                      <Clock size={11} color="var(--c-status-running)" />
                      <Text size="1" color="gray">{fmtDuration(r.duration_ms)}</Text>
                      {clickable ? <ChevronRight size={13} color="var(--gray-9)" /> : null}
                    </Flex>
                  </Flex>
                  {r.delegation_id && r.state === "running" ? (
                    <Button size="1" variant="soft" color="red"
                            onClick={(e) => { e.stopPropagation(); onStop?.(r.delegation_id); }}>
                      <Square size={11} /> 停止后台委派
                    </Button>
                  ) : null}
                  {r.prompt ? (
                    <Text size="1" color="gray" style={{ lineHeight: 1.55, display: "-webkit-box", WebkitLineClamp: 3, WebkitBoxOrient: "vertical", overflow: "hidden" }}>
                      {r.prompt}
                    </Text>
                  ) : null}
                  {!clickable ? (
                    <Text size="1" color="gray" style={{ opacity: 0.75 }}>没有落盘记录（子代理未真正运行，或运行在旧版本上）</Text>
                  ) : null}
                </Flex>
              </Card>
            </motion.div>
          );
        })}
      </Flex>
    </>
  );
}

function SubagentDetail({ row, detail, err, busy, onRetry }) {
  const st = STATE[row.state] || STATE.running;
  const steps = (detail && detail.steps) || [];
  return (
    <Flex direction="column" gap="3">
      <Flex align="center" gap="2" wrap="wrap">
        <Badge size="1" variant="soft" color="iris">{AGENT_LABEL[row.agent_type] || row.agent_type || "子代理"}</Badge>
        <Badge size="1" variant="soft" color={st.color}>{st.label}</Badge>
        <Flex align="center" gap="1" style={{ marginLeft: "auto" }}>
          <Clock size={11} color="var(--c-status-running)" />
          <Text size="1" color="gray">{fmtDuration(row.duration_ms)}</Text>
        </Flex>
      </Flex>

      {row.prompt ? (
        <Box p="2" style={{ background: "var(--c-hover-plate)", borderRadius: 8 }}>
          <Text size="1" color="gray" as="div" mb="1">派发提示词</Text>
          <Text size="1" style={{ lineHeight: 1.6, whiteSpace: "pre-wrap" }}>{row.prompt}</Text>
        </Box>
      ) : null}

      {err ? (
        <Card size="1" style={{ borderColor: "var(--red-7)" }}>
          <Flex direction="column" gap="2">
            <Flex gap="2" align="center"><XCircle size={13} /><Text size="1" color="red">无法加载明细</Text></Flex>
            <Text size="1" color="gray">{err}</Text>
            <Button size="1" variant="soft" color="red" onClick={onRetry}>重试</Button>
          </Flex>
        </Card>
      ) : null}

      {busy ? (
        <Flex align="center" gap="2"><Loader2 size={13} className="spin" /><Text size="1" color="gray">正在读取子代理会话…</Text></Flex>
      ) : null}

      {!busy && !err && detail && !steps.length ? (
        <Text size="1" color="gray">{detail.note || "这个子代理没有可显示的内容。"}</Text>
      ) : null}

      {steps.map((s, i) => (
        <Flex key={i} direction="column" gap="1">
          <Flex align="center" gap="2">
            <Text size="1" color="gray" style={{ letterSpacing: ".06em" }}>
              {s.role === "user" ? "提示" : s.thinking ? "思考" : s.tool ? "工具" : "回复"}
            </Text>
            {s.ts ? <Text size="1" color="gray" style={{ opacity: 0.7 }}>{hhmmss(s.ts)}</Text> : null}
          </Flex>
          {s.text ? (
            <Text size="1" style={{ lineHeight: 1.65, whiteSpace: "pre-wrap" }}>{s.text}</Text>
          ) : null}
          {s.thinking ? (
            <Text size="1" color="gray" style={{ lineHeight: 1.6, whiteSpace: "pre-wrap", fontStyle: "italic" }}>{s.thinking}</Text>
          ) : null}
          {s.tool ? (
            <Card size="1">
              <Flex direction="column" gap="2">
                <Text size="1" weight="medium">{s.tool}</Text>
                {s.args ? <Code size="1" variant="ghost" className="mono" style={{ display: "block", maxHeight: 160, overflow: "auto" }}>{s.args}</Code> : null}
                {s.result ? (
                  <>
                    <Separator size="4" />
                    <Code size="1" variant="ghost" color={s.is_error ? "red" : undefined} className="mono"
                          style={{ display: "block", maxHeight: 220, overflow: "auto", whiteSpace: "pre-wrap" }}>
                      {s.result}
                    </Code>
                  </>
                ) : null}
              </Flex>
            </Card>
          ) : null}
          {!s.text && !s.thinking && !s.tool && s.result ? (
            <Code size="1" variant="ghost" className="mono" style={{ display: "block", maxHeight: 200, overflow: "auto" }}>{s.result}</Code>
          ) : null}
        </Flex>
      ))}
    </Flex>
  );
}
