import { useCallback, useEffect, useState } from "react";
import { Badge, Box, Button, Code, Flex, IconButton, ScrollArea, Separator, Text, Tooltip } from "./ui.jsx";
import { AlertTriangle, CheckCircle2, FileText, Fingerprint, History, Network, RefreshCw, ShieldCheck, Wrench, X, XCircle } from "lucide-react";
import { authedFetch, errorText } from "./lib/auth.js";
import { AuditChainPanel, GateFailure, PentestReportPanel, TrafficPanel, patchFindingConfirm } from "./Stage2.jsx";

/* 系统面：就绪自检 / 能力注册表 / 证据完整性。
 *
 * 这三个端点早就存在，控制台此前一条都没接。它们的价值恰恰在于
 * 「后端没能力时界面必须说清楚」——所以这里没有开关，只有事实与原因：
 *
 *   GET /api/harness/ready                  pg / llm / skills / browser_mcp 四项，各带 hint
 *   GET /api/harness/capabilities           surface/name/enabled/exec/concurrency_safe/deferred
 *   GET /api/harness/evidence-integrity?task=  有取证绑定 / 无绑定 的发现清单（仅报告）
 *
 * 阶段二（S6-P1）又加了三个面，渲染在 web/src/Stage2.jsx：
 *   审计链  GET /api/harness/audit-chain?task=   哈希链验证 + 条目
 *   流量    GET /api/traffic + POST /api/traffic/replay  F12 表 + 重放
 *   报告    GET /api/report/pentest?task=       专业报告 Markdown + 下载
 * 取证面的每条发现还多了「标记为已确认」：走验证门，422 时在行内列出
 * 失败原因并可人工越过（理由写入审计链）。
 */

async function api(path) {
  const r = await authedFetch(path, { credentials: "include" });
  if (!r.ok) throw new Error(await errorText(r));
  return r.json();
}

const TABS = [
  { key: "ready", label: "就绪", icon: ShieldCheck },
  { key: "caps", label: "能力", icon: Wrench },
  { key: "replay", label: "回放", icon: History },
  { key: "evidence", label: "取证", icon: AlertTriangle },
  { key: "audit", label: "审计链", icon: Fingerprint },
  { key: "traffic", label: "流量", icon: Network },
  { key: "report", label: "报告", icon: FileText },
];

/* 就绪项的展示顺序固定：数据库 → 模型 → 技能 → 浏览器。
 * 顺序有意义：前一项不 ok 时，后一项的值都是"在此基础上"。 */
const READY_ORDER = [
  ["pg", "PostgreSQL"],
  ["llm", "模型端点"],
  ["skills", "技能目录"],
  ["browser_mcp", "浏览器 MCP"],
];

export default function System({ taskId, onClose }) {
  const [tab, setTab] = useState("ready");
  const [ready, setReady] = useState(null);
  const [caps, setCaps] = useState(null);
  const [ev, setEv] = useState(null);
  const [err, setErr] = useState("");
  const [evErr, setEvErr] = useState("");
  const [busy, setBusy] = useState(false);
  // 回放面：/api/harness/replay 是只读投影（与 harness-gate 同源），
  // 控制台此前没有渲染入口。这里给出指纹 + 拒绝事件 + markdown 原文。
  const [replay, setReplay] = useState(null);
  const [replayErr, setReplayErr] = useState("");
  const [replayBusy, setReplayBusy] = useState(false);
  const [showMd, setShowMd] = useState(false);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const [r, c] = await Promise.all([
        api("/api/harness/ready"),
        api("/api/harness/capabilities").catch(() => null),
      ]);
      setReady(r);
      setCaps(c);
      setErr("");
    } catch (e) { setErr(e.message); }
    finally { setBusy(false); }
  }, []);

  useEffect(() => { load(); }, [load]);

  const loadEv = useCallback(async () => {
    if (!taskId) return;
    setEvErr("");
    try {
      setEv(await api(`/api/harness/evidence-integrity?task=${encodeURIComponent(taskId)}`));
    } catch (e) { setEvErr(e.message); }
  }, [taskId]);

  useEffect(() => {
    if (tab !== "evidence" || !taskId) return;
    loadEv();
  }, [tab, loadEv]);

  // Verification gate (stage 2): confirming a finding runs the gate on the
  // server. A 422 lists the failing checks under the row and offers a human
  // override — the override reason is written to the audit chain, never
  // silently treated as a verified confirmation.
  const [gateReasons, setGateReasons] = useState({});
  const [confirmBusy, setConfirmBusy] = useState(null);
  const confirmFinding = async (findingId, humanOverride) => {
    setConfirmBusy(findingId);
    try {
      const res = await patchFindingConfirm(findingId, humanOverride);
      if (res.ok) {
        setGateReasons((g) => ({ ...g, [findingId]: [] }));
        await loadEv();
      } else if (res.reasons?.length) {
        setGateReasons((g) => ({ ...g, [findingId]: res.reasons }));
      } else {
        setEvErr(res.message || "确认失败");
      }
    } catch (e) { setEvErr(e.message); }
    finally { setConfirmBusy(null); }
  };

  const loadReplay = useCallback(async () => {
    if (!taskId) return;
    setReplayBusy(true); setReplayErr("");
    try {
      const d = await api(`/api/harness/replay?task=${encodeURIComponent(taskId)}`);
      setReplay(d);
    } catch (e) { setReplayErr(e.message); }
    finally { setReplayBusy(false); }
  }, [taskId]);

  useEffect(() => {
    if (tab !== "replay") return;
    setReplay(null);
    loadReplay();
  }, [tab, loadReplay]);

  const head = (
    <Flex align="center" gap="1" px="2" py="1" style={{ borderBottom: "1px solid var(--c-divider)" }}>
      <div style={{ display: "flex", gap: 4, overflowX: "auto", minWidth: 0, flex: 1, scrollbarWidth: "none" }}>
      {TABS.map((t) => {
        const Icon = t.icon;
        return (
          <button key={t.key} className={"bench-tab" + (tab === t.key ? " active" : "")}
                  onClick={() => setTab(t.key)}>
            <Icon size={12} /> {t.label}
          </button>
        );
      })}
      </div>
      <Box style={{ marginLeft: "auto" }} />
      <Tooltip content="刷新">
        <IconButton size="1" variant="ghost" aria-label="刷新系统状态" onClick={load}>
          <RefreshCw size={12} className={busy ? "spin" : ""} />
        </IconButton>
      </Tooltip>
      <Tooltip content="关闭">
        <IconButton size="1" variant="ghost" aria-label="关闭系统面板" onClick={onClose}><X size={13} /></IconButton>
      </Tooltip>
    </Flex>
  );

  return (
    <div className="subpanel">
      {head}

      {err ? (
        <Box px="3" py="2"><Text size="1" color="red">{err} <Button size="1" variant="soft" onClick={load}>重试</Button></Text></Box>
      ) : null}

      {tab === "ready" ? (
        <ScrollArea style={{ flex: 1, minHeight: 0 }}>
          <Box px="3" py="2">
            <Flex align="center" gap="2" mb="2">
              <Text size="1" weight="medium">引擎就绪</Text>
              {ready ? (
                <Badge size="1" variant="soft" color={ready.ready ? "green" : "red"}>
                  {ready.ready ? "ready" : "idle"}
                </Badge>
              ) : null}
            </Flex>
            {ready?.hint ? (
              <Text size="1" color={ready.ready ? "gray" : "amber"}
                    style={{ display: "block", marginBottom: 8, lineHeight: 1.6 }}>{ready.hint}</Text>
            ) : null}

            {READY_ORDER.map(([key, label]) => {
              const b = ready?.[key];
              if (!b) return null;
              const Icon = b.ok ? CheckCircle2 : XCircle;
              return (
                <div key={key} className="ready-row">
                  <Icon size={13} className="ready-icon"
                        style={{ color: b.ok ? "var(--c-status-ok)" : "var(--c-status-bad)" }} />
                  <div style={{ minWidth: 0 }}>
                    <div className="ready-label">
                      {label}
                      {typeof b.count === "number" && b.count > 0 ? <span className="ready-count">{b.count}</span> : null}
                    </div>
                    {b.detail ? <div className="ready-detail mono">{b.detail}</div> : null}
                    {b.hint ? <div className="ready-hint">{b.hint}</div> : null}
                  </div>
                </div>
              );
            })}

            <Separator size="4" style={{ margin: "10px 0" }} />
            <Text size="1" color="gray" style={{ display: "block", lineHeight: 1.7 }}>
              这四项各自独立失败：缺模型是"引擎 idle"，缺技能只是少一层领域知识，
              缺浏览器只影响浏览器工具。控制台不会把四种原因说成同一句"卡住了"。
            </Text>
            {ready?.workspace ? (
              <Text size="1" color="gray" className="mono" style={{ display: "block", marginTop: 6 }}>
                工作区根：{ready.workspace}
              </Text>
            ) : null}
          </Box>
        </ScrollArea>
      ) : null}

      {tab === "caps" ? (
        <ScrollArea style={{ flex: 1, minHeight: 0 }}>
          <Box px="3" py="2">
            {caps?.concurrency ? (
              <Flex gap="3" mb="2">
                <Text size="1" color="gray">可并行 {caps.concurrency.parallel}</Text>
                <Text size="1" color="gray">独占 {caps.concurrency.exclusive}</Text>
                <Box style={{ marginLeft: "auto" }} />
                <Text size="1" color="gray">共 {(caps.capabilities || []).length} 项</Text>
              </Flex>
            ) : null}
            {(caps?.capabilities || []).map((c) => (
              <div key={c.surface + "/" + c.name} className="cap-row">
                <Flex align="center" gap="2" style={{ width: "100%" }}>
                  <Badge size="1" variant="soft" color={surfaceTone(c.surface)}>{c.surface}</Badge>
                  <Text size="1" weight="medium" truncate>{c.name}</Text>
                  <Box style={{ marginLeft: "auto" }} />
                  {c.exec ? <Badge size="1" variant="soft" color="amber">可执行</Badge> : null}
                  {c.concurrency_safe ? <Badge size="1" variant="soft" color="blue">并行安全</Badge> : null}
                  {c.deferred ? <Badge size="1" variant="soft" color="gray">延迟解锁</Badge> : null}
                  <Badge size="1" variant="soft" color={c.enabled ? "green" : "gray"}>
                    {c.enabled ? "启用" : "关闭"}
                  </Badge>
                </Flex>
                {c.detail ? <div className="cap-detail">{c.detail}</div> : null}
                {c.agents?.length ? <div className="cap-detail mono">agents: {c.agents.join(", ")}</div> : null}
              </div>
            ))}
            {!caps?.capabilities?.length ? (
              <Text size="1" color="gray">没有能力可展示（数据库不可用时会这样）。</Text>
            ) : null}
            <Text size="1" color="gray" style={{ display: "block", marginTop: 8, lineHeight: 1.7 }}>
              「可执行」「并行安全」是事实描述，不是风险评分：前者表示它能在这台机器上跑代码或发请求，
              后者表示流式执行器可以与其它安全能力并行。判据留在操作者手里。
            </Text>
          </Box>
        </ScrollArea>
      ) : null}

      {tab === "replay" ? (
        <ScrollArea style={{ flex: 1, minHeight: 0 }}>
          <Box px="3" py="2">
            {!taskId ? (
              <Text size="1" color="gray">先选择一个运行（任务）——回放是按任务重建的历史。</Text>
            ) : null}
            {replayErr ? (
              <Text size="1" color="red">{replayErr} <Button size="1" variant="soft" onClick={loadReplay}>重试</Button></Text>
            ) : null}
            {replay?.run ? (
              <>
                <Flex align="center" gap="2" mb="2">
                  <Text size="1" weight="medium">回放指纹</Text>
                  <Code size="1" variant="ghost" className="mono">{replay.hash || "—"}</Code>
                  <Box style={{ marginLeft: "auto" }} />
                  <IconButton size="1" variant="ghost" aria-label="重新载入回放" onClick={loadReplay}>
                    <RefreshCw size={12} className={replayBusy ? "spin" : ""} />
                  </IconButton>
                </Flex>
                <Flex gap="3" mb="2">
                  <Text size="1" color="gray">轮次 {replay.fingerprint?.turns ?? 0}</Text>
                  <Text size="1" color="gray">条目 {replay.fingerprint?.items ?? 0}</Text>
                  <Text size="1" color={replay.blocks?.length ? "amber" : "gray"}>
                    拒绝事件 {replay.blocks?.length ?? 0}
                  </Text>
                </Flex>

                {replay.run.truncated ? (
                  <Text size="1" color="amber" style={{ display: "block", marginBottom: 6, lineHeight: 1.6 }}>
                    回放被截断（历史超出读取上限）：不能据此判定一致性，也不能当作完整行为证据。
                  </Text>
                ) : null}

                <Text size="1" weight="medium" style={{ display: "block", margin: "10px 0 4px" }}>工具分布</Text>
                {Object.keys(replay.fingerprint?.tools || {}).length ? (
                  Object.entries(replay.fingerprint.tools)
                    .sort((a, b) => b[1] - a[1])
                    .map(([k, v]) => (
                      <Flex key={k} align="center" gap="2" py="1">
                        <Text size="1" className="mono" truncate>{k}</Text>
                        <Box style={{ marginLeft: "auto" }} />
                        <Text size="1" color="gray">×{v}</Text>
                      </Flex>
                    ))
                ) : (
                  <Text size="1" color="gray">这次运行没有调用工具。</Text>
                )}

                {replay.blocks?.length ? (
                  <>
                    <Text size="1" weight="medium" style={{ display: "block", margin: "10px 0 4px" }}>
                      拒绝 / 拦截事件
                    </Text>
                    {replay.blocks.map((b, i) => (
                      <div key={i} className="cap-row">
                        <Flex align="center" gap="2" style={{ width: "100%" }}>
                          <Badge size="1" variant="soft" color="red">{b.kind || "block"}</Badge>
                          <Text size="1" className="mono" truncate>{b.tool}</Text>
                          <Box style={{ marginLeft: "auto" }} />
                          <Text size="1" color="gray">rev {b.revision}</Text>
                        </Flex>
                        <div className="cap-detail">{b.message}</div>
                      </div>
                    ))}
                  </>
                ) : null}

                <Separator size="4" style={{ margin: "10px 0" }} />
                <Flex align="center" gap="2">
                  <Text size="1" weight="medium">Markdown 原文</Text>
                  <Box style={{ marginLeft: "auto" }} />
                  <Button size="1" variant="soft" onClick={() => setShowMd((v) => !v)}>
                    {showMd ? "收起" : "展开"}
                  </Button>
                </Flex>
                {showMd ? (
                  <pre className="replay-md">{replay.markdown || "(空)"}</pre>
                ) : null}
              </>
            ) : null}
            {!replay && !replayErr && taskId ? (
              <Text size="1" color="gray">读取中…</Text>
            ) : null}
            <Text size="1" color="gray" style={{ display: "block", marginTop: 8, lineHeight: 1.7 }}>
              回放是只读投影：它不触碰探索状态，所以对正在跑的任务做回放也不会扰动它。
              非终态任务也可以回放；上面那一行「拒绝事件」与 harness-gate 用的是同一份数据。
            </Text>
          </Box>
        </ScrollArea>
      ) : null}

      {tab === "evidence" ? (
        <ScrollArea style={{ flex: 1, minHeight: 0 }}>
          <Box px="3" py="2">
            {!taskId ? (
              <Text size="1" color="gray">先选择一个运行（任务）——证据绑定是按任务统计的。</Text>
            ) : null}
            {evErr ? <Text size="1" color="red">{evErr}</Text> : null}
            {ev ? (
              <>
                <Flex gap="3" mb="2">
                  <Text size="1" color="gray">共 {ev.total}</Text>
                  <Text size="1" color="green">有绑定 {ev.with_evidence}</Text>
                  <Text size="1" color={ev.without_evidence ? "amber" : "gray"}>无绑定 {ev.without_evidence}</Text>
                </Flex>
                {(ev.rows || []).map((r) => (
                  <div key={r.finding_id} className="cap-row">
                    <Flex align="center" gap="2" style={{ width: "100%" }}>
                      <Badge size="1" variant="soft" color={r.bindings > 0 ? "green" : "amber"}>
                        {r.bindings > 0 ? r.bindings + " 绑" : "无绑定"}
                      </Badge>
                      <Text size="1" weight="medium" truncate>{r.name || "finding #" + r.finding_id}</Text>
                      <Box style={{ marginLeft: "auto" }} />
                      {r.severity ? <Text size="1" color="gray">{r.severity}</Text> : null}
                      {r.status ? <Text size="1" color="gray">{r.status}</Text> : null}
                    </Flex>
                    <div className="cap-detail mono">
                      snapshots={r.snapshots} · report={r.has_report ? "有" : "无"}
                    </div>
                    {r.status === "confirmed" ? (
                      <div className="cap-detail">已确认（通过验证门，或人工越过并已写入审计链）。</div>
                    ) : (
                      <Flex gap="2" mt="1" align="center">
                        <Button size="1" variant="soft" disabled={confirmBusy === r.finding_id}
                                onClick={() => confirmFinding(r.finding_id, "")}>
                          {confirmBusy === r.finding_id ? "确认中…" : "标记为已确认"}
                        </Button>
                        <Text size="1" color="gray">确认会过验证门：需证据绑定、复现步骤与复测复现。</Text>
                      </Flex>
                    )}
                    <GateFailure reasons={gateReasons[r.finding_id]}
                                 overrideBusy={confirmBusy === r.finding_id}
                                 onOverride={(reason) => confirmFinding(r.finding_id, reason)} />
                  </div>
                ))}
                {!(ev.rows || []).length ? (
                  <Text size="1" color="gray">这个任务还没有登记发现。</Text>
                ) : null}
                {ev.note ? (
                  <Text size="1" color="gray" style={{ display: "block", marginTop: 8, lineHeight: 1.7 }}>{ev.note}</Text>
                ) : null}
              </>
            ) : null}
          </Box>
        </ScrollArea>
      ) : null}

      {tab === "audit" ? <AuditChainPanel taskId={taskId} /> : null}
      {tab === "traffic" ? <TrafficPanel taskId={taskId} /> : null}
      {tab === "report" ? <PentestReportPanel taskId={taskId} /> : null}
    </div>
  );
}

function surfaceTone(s) {
  return ({ tool: "blue", mcp: "violet", skill: "green" })[s] || "gray";
}