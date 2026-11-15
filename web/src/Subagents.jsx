import { authedFetch } from "./lib/auth.js";
import { useCallback, useEffect, useRef, useState } from "react";
import { Badge, Box, Button, Card, Code, Flex, IconButton, ScrollArea, Separator, Text, Tooltip } from "@radix-ui/themes";
import { motion } from "motion/react";
import { ChevronDown, ChevronRight, Clock, Loader2, RefreshCw, Users, X, XCircle } from "lucide-react";

/* 子代理侧栏（对照 DSH packages/client/ui-subagent）：
 *   section.title / section.empty   —— 分节标题与空态
 *   progress.*                      —— 进行中 / 已完成 / 失败 计数
 *   每项                            —— 类型、状态、耗时、提示词、结果
 *   load.error / retry              —— 加载失败可重试，绝不静默
 *
 * 触发时机（这是从代码读出来的，不是猜的）：
 *   · HARNESS 模式下没有 worker 池；派不派子代理由模型自己判断（Agent 工具）
 *   · 子代理定义里不含 Agent 工具 → 不会再派第三代（深度守卫见 agent/harness.go）
 *   · 每个子代理有自己的 10 分钟预算，父轮次被取消不会连带杀掉已在跑的子代理
 * 数据来自 GET /api/harness/workers/{id}，后端已把重复记录（原始行 + 可读行）去重。
 */

const authHeaders = () => {
  let t = "";
  try { t = localStorage.getItem("artex_token") || localStorage.getItem("artex.harness.token") || ""; } catch {}
  return t ? { Authorization: "Bearer " + t } : {};
};

async function jget(path) {
  const r = await authedFetch(path, { headers: authHeaders(), credentials: "include" });
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

const STATE = {
  running: { label: "运行中", color: "amber" },
  done: { label: "已完成", color: "green" },
  error: { label: "失败", color: "red" },
};

const AGENT_LABEL = { recon: "侦察", verify: "验证" };

export default function Subagents({ taskId, live, onClose }) {
  const [rows, setRows] = useState(null);   // null = 未加载
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState({});     // activity_id → 展开结果
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

  // 有子代理在跑、或主事件流还在跑时轮询；都停下就不再打扰后端。
  const anyRunning = (rows || []).some((r) => r.state === "running");
  useEffect(() => {
    if (!taskId) return;
    if (!anyRunning && live !== "streaming" && live !== "connecting") return;
    const t = setInterval(() => load(true), anyRunning ? 2000 : 4000);
    return () => clearInterval(t);
  }, [taskId, anyRunning, live, load]);

  const total = (rows || []).length;
  const running = (rows || []).filter((r) => r.state === "running").length;
  const done = (rows || []).filter((r) => r.state === "done").length;
  const failed = (rows || []).filter((r) => r.state === "error").length;

  return (
    <div className="subpanel">
      <Flex align="center" gap="2" px="3" py="2" style={{ borderBottom: "1px solid var(--gray-a5)" }}>
        <Users size={14} />
        <Text size="2" weight="medium">子智能体</Text>
        {total ? (
          <Flex gap="2" align="center" style={{ marginLeft: "auto" }}>
            {running ? <Badge size="1" variant="soft" color="amber">{running} 进行中</Badge> : null}
            {done ? <Badge size="1" variant="soft" color="green">{done} 已完成</Badge> : null}
            {failed ? <Badge size="1" variant="soft" color="red">{failed} 失败</Badge> : null}
          </Flex>
        ) : null}
        <Tooltip content="刷新">
          <IconButton size="1" variant="ghost" aria-label="刷新子智能体" style={{ marginLeft: total ? 0 : "auto" }}
                      onClick={() => load(false)}>
            <RefreshCw size={12} className={busy ? "spin" : ""} />
          </IconButton>
        </Tooltip>
        <Tooltip content="关闭">
          <IconButton size="1" variant="ghost" aria-label="关闭子代理面板" onClick={onClose}><X size={13} /></IconButton>
        </Tooltip>
      </Flex>

      <ScrollArea style={{ flex: 1, minHeight: 0 }}>
        <Box p="3">
          {err ? (
            <Card size="1" style={{ borderColor: "var(--red-7)", marginBottom: 10 }}>
              <Flex direction="column" gap="2">
                <Flex gap="2" align="center"><XCircle size={13} /><Text size="1" color="red">无法加载子智能体</Text></Flex>
                <Text size="1" color="gray">{err}</Text>
                <Button size="1" variant="soft" color="red" onClick={() => load(false)}>重试</Button>
              </Flex>
            </Card>
          ) : null}

          {rows === null && !err ? (
            <Flex align="center" gap="2" py="3">
              <Loader2 size={13} className="spin" />
              <Text size="1" color="gray">正在加载子智能体…</Text>
            </Flex>
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
              const isOpen = !!open[r.activity_id];
              return (
                <motion.div key={r.activity_id} initial={{ opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.16 }}>
                  <Card size="1">
                    <Flex direction="column" gap="2">
                      <Flex align="center" gap="2" wrap="wrap">
                        <Badge size="1" variant="soft" color="iris">{AGENT_LABEL[r.agent_type] || r.agent_type || "子代理"}</Badge>
                        <Badge size="1" variant="soft" color={st.color}>
                          {r.state === "running" ? <Loader2 size={10} className="spin" style={{ marginRight: 4 }} /> : null}
                          {st.label}
                        </Badge>
                        <Flex align="center" gap="1" style={{ marginLeft: "auto" }}>
                          <Clock size={11} color="var(--gray-8)" />
                          <Text size="1" color="gray">{fmtDuration(r.duration_ms)}</Text>
                        </Flex>
                      </Flex>

                      {r.prompt ? (
                        <Text size="1" color="gray" style={{ lineHeight: 1.55, display: "-webkit-box", WebkitLineClamp: isOpen ? "unset" : 3, WebkitBoxOrient: "vertical", overflow: "hidden" }}>
                          {r.prompt}
                        </Text>
                      ) : null}

                      {r.result || isOpen ? (
                        <Button size="1" variant="ghost" onClick={() => setOpen((o) => ({ ...o, [r.activity_id]: !o[r.activity_id] }))}>
                          {isOpen ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
                          {isOpen ? "收起来源与输出" : "查看输出"}
                        </Button>
                      ) : null}

                      {isOpen ? (
                        <Code size="1" variant="ghost" className="mono"
                              style={{ display: "block", maxHeight: 260, overflow: "auto", whiteSpace: "pre-wrap" }}>
                          {r.result || "(无输出)"}
                        </Code>
                      ) : null}
                    </Flex>
                  </Card>
                </motion.div>
              );
            })}
          </Flex>
        </Box>
      </ScrollArea>
    </div>
  );
}
