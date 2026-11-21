// Stage 2 frontend (S6-P1): audit chain, F12 traffic table + replay,
// pentest report. Lives in the System side panel (System.jsx tabs), the
// only place the console already renders findings/traffic-adjacent data —
// the console has no separate findings or traffic page, so these panels
// reuse its tab + cap-row styling instead of inventing a new route.
//
// Backend contracts (server/harness_verify.go, server.go):
//   GET  /api/harness/audit-chain?task=   {valid, entries, head_hash, problem, chain[]}
//   GET  /api/traffic?host=&method=&status=&q=&page=&size=
//        {enabled, proxy, count, total, exchanges[{id,ts,host,method,url,status,content_type,resp_len}]}
//   GET  /api/traffic/exchange?id=        {req, resp}
//   POST /api/traffic/replay              {exchange_id, task_id} -> {reproduced, status, original_status, note, curl}
//   GET  /api/report/pentest?task=        text/markdown
import { useCallback, useEffect, useState } from "react";
import { Badge, Box, Button, Code, Flex, IconButton, ScrollArea, Text, TextField as RTextField } from "./ui.jsx";
import { CheckCircle2, Copy, Download, Play, RefreshCw, XCircle } from "lucide-react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { authHeaders, authedFetch, errorText } from "./lib/auth.js";

async function apiJSON(path, opts) {
  const r = await authedFetch(path, { credentials: "include", ...opts });
  if (!r.ok) {
    const err = new Error(await errorText(r));
    err.status = r.status;
    throw err;
  }
  return r.json();
}

// patchFindingConfirm tries to mark a finding confirmed. On 422 it
// returns {ok:false, reasons[]} instead of throwing, so the caller can
// show the gate failures and offer a human override.
export async function patchFindingConfirm(findingId, humanOverride) {
  const body = { status: "confirmed" };
  if (humanOverride) body.human_override = humanOverride;
  const r = await authedFetch(`/api/exploration/findings/${findingId}`, {
    method: "PATCH", credentials: "include",
    headers: { ...authHeaders(), "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (r.ok) return { ok: true };
  let reasons = [];
  let message = "";
  try {
    const j = await r.json();
    if (Array.isArray(j.reasons)) reasons = j.reasons;
    message = j.error || "";
  } catch {
    message = await errorText(r);
  }
  if (r.status === 422 && reasons.length) return { ok: false, reasons, message };
  return { ok: false, reasons, message: message || `HTTP ${r.status}` };
}

const KIND_LABEL = {
  finding_reported: "漏洞登记",
  evidence_bound: "证据绑定",
  retest_verdict: "复测结论",
  finding_confirmed: "确认",
  finding_rejected: "驳回 / 误报",
  human_override: "人工越过",
  report_generated: "生成报告",
  traffic_replay: "流量重放",
};

function shortHash(h) {
  return typeof h === "string" && h.length > 12 ? h.slice(0, 12) : (h || "—");
}

function fmtTime(ts) {
  if (!ts) return "—";
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? String(ts) : d.toLocaleString();
}

function statusTone(status) {
  if (status >= 200 && status < 300) return "green";
  if (status >= 300 && status < 400) return "blue";
  if (status >= 400 && status < 500) return "amber";
  if (status >= 500) return "red";
  return "gray";
}

function statusClass(status) {
  if (status >= 200 && status < 300) return "2xx";
  if (status >= 300 && status < 400) return "3xx";
  if (status >= 400 && status < 500) return "4xx";
  if (status >= 500 && status < 600) return "5xx";
  return "other";
}

// --- Audit chain -------------------------------------------------------
export function AuditChainPanel({ taskId }) {
  const [data, setData] = useState(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    if (!taskId) return;
    setBusy(true); setErr("");
    try {
      setData(await apiJSON(`/api/harness/audit-chain?task=${encodeURIComponent(taskId)}`));
    } catch (e) { setErr(e.message); }
    finally { setBusy(false); }
  }, [taskId]);

  useEffect(() => { setData(null); load(); }, [load]);

  return (
    <ScrollArea style={{ flex: 1, minHeight: 0 }}>
      <Box px="3" py="2">
        {!taskId ? <Text size="1" color="gray">先选择一个运行（任务）——审计链是按任务记录的。</Text> : null}
        {err ? <Text size="1" color="red">{err} <Button size="1" variant="soft" onClick={load}>重试</Button></Text> : null}
        {data ? (
          <>
            <Flex align="center" gap="2" mb="2">
              <Text size="1" weight="medium">审计链</Text>
              <Badge size="1" variant="soft" color={data.valid ? "green" : "red"}>
                {data.valid ? "验证通过" : "验证失败"}
              </Badge>
              <Text size="1" color="gray">{data.entries} 条</Text>
              <Box style={{ marginLeft: "auto" }} />
              <IconButton size="1" variant="ghost" aria-label="刷新审计链" onClick={load}>
                <RefreshCw size={12} className={busy ? "spin" : ""} />
              </IconButton>
            </Flex>
            {data.head_hash ? (
              <Text size="1" color="gray" className="mono" style={{ display: "block", marginBottom: 8 }}>
                头哈希 {shortHash(data.head_hash)}…
              </Text>
            ) : null}
            {data.valid === false && data.problem ? (
              <Text size="1" color="red" style={{ display: "block", marginBottom: 8, lineHeight: 1.6 }}>
                {data.problem}——本任务的结论不可仅凭此链采信，请先核查审计文件。
              </Text>
            ) : null}
            {(data.chain || []).map((e) => (
              <div key={e.seq} className="cap-row">
                <Flex align="center" gap="2" style={{ width: "100%" }}>
                  <Badge size="1" variant="soft" color="gray">#{e.seq}</Badge>
                  <Text size="1" weight="medium">{KIND_LABEL[e.kind] || e.kind}</Text>
                  <Box style={{ marginLeft: "auto" }} />
                  <Text size="1" color="gray">{fmtTime(e.ts)}</Text>
                </Flex>
                <div className="cap-detail">{e.summary}</div>
                <div className="cap-detail mono">
                  {e.actor || "—"}{e.finding_id ? ` · finding #${e.finding_id}` : ""}
                  {e.evidence?.length ? ` · 证据 ${e.evidence.join(", ")}` : ""}
                </div>
                <div className="cap-detail mono">hash {shortHash(e.hash)}…</div>
              </div>
            ))}
            {!(data.chain || []).length ? (
              <Text size="1" color="gray">这个任务还没有审计记录：确认漏洞、生成报告、重放流量时会自动写入。</Text>
            ) : null}
            <Text size="1" color="gray" style={{ display: "block", marginTop: 8, lineHeight: 1.7 }}>
              每条记录都链着上一条的哈希：改一条、删一条都会让验证失败。链只能证明记录没被改过，
              不能证明记录本身完整——文件权限与备份仍需运维保障。
            </Text>
          </>
        ) : null}
        {!data && !err && taskId ? <Text size="1" color="gray">读取中…</Text> : null}
      </Box>
    </ScrollArea>
  );
}

// --- F12 traffic table + replay ----------------------------------------
export function TrafficPanel({ taskId }) {
  const [host, setHost] = useState("");
  const [method, setMethod] = useState("");
  const [status, setStatus] = useState("");
  const [q, setQ] = useState("");
  const [page, setPage] = useState(0);
  const [data, setData] = useState(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [selected, setSelected] = useState(null);
  const [detail, setDetail] = useState(null);
  const [replay, setReplay] = useState(null);
  const [replayBusy, setReplayBusy] = useState(false);
  const [replayErr, setReplayErr] = useState("");

  const load = useCallback(async (p) => {
    setBusy(true); setErr("");
    try {
      const qs = new URLSearchParams({ page: String(p), size: "50" });
      if (host.trim()) qs.set("host", host.trim());
      if (method) qs.set("method", method);
      if (status) qs.set("status", status);
      if (q.trim()) qs.set("q", q.trim());
      setData(await apiJSON(`/api/traffic?${qs.toString()}`));
      setPage(p);
    } catch (e) { setErr(e.message); }
    finally { setBusy(false); }
  }, [host, method, status, q]);

  useEffect(() => { setSelected(null); setDetail(null); setReplay(null); load(0); }, [load]);

  const openExchange = async (row) => {
    setSelected(row); setDetail(null); setReplay(null); setReplayErr("");
    try {
      setDetail(await apiJSON(`/api/traffic/exchange?id=${encodeURIComponent(row.id)}`));
    } catch (e) { setReplayErr(e.message); }
  };

  const doReplay = async () => {
    if (!selected) return;
    setReplayBusy(true); setReplayErr(""); setReplay(null);
    try {
      const r = await authedFetch("/api/traffic/replay", {
        method: "POST", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ exchange_id: selected.id, task_id: taskId ? Number(taskId) : 0 }),
      });
      if (!r.ok) throw new Error(await errorText(r));
      setReplay(await r.json());
    } catch (e) { setReplayErr(e.message); }
    finally { setReplayBusy(false); }
  };

  const copyCurl = async () => {
    if (!replay?.curl) return;
    try { await navigator.clipboard.writeText(replay.curl); } catch { /* clipboard unavailable */ }
  };

  return (
    <ScrollArea style={{ flex: 1, minHeight: 0 }}>
      <Box px="3" py="2">
        <Flex gap="2" mb="2" wrap="wrap" align="center">
          <RTextField size="1" placeholder="host 过滤" value={host} onChange={(e) => setHost(e.target.value)} style={{ width: 130 }} />
          <select value={method} onChange={(e) => setMethod(e.target.value)} className="traffic-select" aria-label="方法过滤">
            <option value="">全部方法</option>
            {["GET", "POST", "PUT", "PATCH", "DELETE"].map((m) => <option key={m} value={m}>{m}</option>)}
          </select>
          <select value={status} onChange={(e) => setStatus(e.target.value)} className="traffic-select" aria-label="状态过滤">
            <option value="">全部状态</option>
            {["2xx", "3xx", "4xx", "5xx"].map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
          <RTextField size="1" placeholder="搜索 URL / 内容" value={q} onChange={(e) => setQ(e.target.value)} style={{ width: 130 }} />
          <Button size="1" variant="soft" onClick={() => load(0)} disabled={busy}>筛选</Button>
          <Box style={{ marginLeft: "auto" }} />
          <Text size="1" color="gray">共 {data?.total ?? 0} 条</Text>
        </Flex>
        {data && data.enabled === false ? (
          <Text size="1" color="amber" style={{ display: "block", marginBottom: 8, lineHeight: 1.6 }}>
            流量记录未启用：这里只显示已抓到的历史流量。开启后代理地址 {data.proxy || "—"}。
          </Text>
        ) : null}
        {err ? <Text size="1" color="red">{err} <Button size="1" variant="soft" onClick={() => load(page)}>重试</Button></Text> : null}

        {(data?.exchanges || []).map((row) => (
          <div key={row.id} className={"cap-row traffic-row" + (selected?.id === row.id ? " selected" : "")}
               onClick={() => openExchange(row)} role="button" tabIndex={0}
               onKeyDown={(e) => { if (e.key === "Enter") openExchange(row); }}>
            <Flex align="center" gap="2" style={{ width: "100%" }}>
              <Text size="1" weight="medium" className="mono" style={{ minWidth: 44 }}>{row.method}</Text>
              <Badge size="1" variant="soft" color={statusTone(row.status)}>{row.status}</Badge>
              <Text size="1" className="mono" truncate>{row.url}</Text>
              <Box style={{ marginLeft: "auto" }} />
              <Text size="1" color="gray" style={{ whiteSpace: "nowrap" }}>{fmtBytes(row.resp_len)}</Text>
            </Flex>
            <div className="cap-detail mono">{row.host} · {fmtTime(row.ts)}{row.content_type ? ` · ${row.content_type}` : ""}</div>
          </div>
        ))}
        {data && !(data.exchanges || []).length ? (
          <Text size="1" color="gray">没有匹配的流量。改宽过滤条件，或确认流量记录已开启并有请求经过代理。</Text>
        ) : null}
        {!data && !err ? <Text size="1" color="gray">读取中…</Text> : null}

        {data ? (
          <Flex gap="2" mt="2" align="center">
            <Button size="1" variant="soft" disabled={page <= 0 || busy} onClick={() => load(page - 1)}>上一页</Button>
            <Text size="1" color="gray">第 {page + 1} 页</Text>
            <Button size="1" variant="soft" disabled={busy || (data.exchanges || []).length < 50} onClick={() => load(page + 1)}>下一页</Button>
          </Flex>
        ) : null}

        {selected ? (
          <>
            <Flex align="center" gap="2" mt="3" mb="2">
              <Text size="1" weight="medium">选中请求</Text>
              <Badge size="1" variant="soft" color={statusTone(selected.status)}>{selected.status} · {statusClass(selected.status)}</Badge>
              <Box style={{ marginLeft: "auto" }} />
              <Button size="1" variant="soft" onClick={doReplay} disabled={replayBusy}>
                <Play size={12} /> {replayBusy ? "重放中…" : "重放"}
              </Button>
            </Flex>
            {replayErr ? <Text size="1" color="red">{replayErr}</Text> : null}
            {replay ? (
              <Box mb="2">
                <Flex gap="2" mb="1" align="center">
                  <Badge size="1" variant="soft" color={replay.reproduced ? "green" : "amber"}>
                    {replay.reproduced ? "已复现" : "未复现"}
                  </Badge>
                  <Text size="1" color="gray">{replay.note}</Text>
                  <Box style={{ marginLeft: "auto" }} />
                  <IconButton size="1" variant="ghost" aria-label="复制 curl" onClick={copyCurl}>
                    <Copy size={12} />
                  </IconButton>
                </Flex>
                <pre className="replay-md">{replay.curl}</pre>
                <Text size="1" color="gray" style={{ display: "block", marginTop: 4, lineHeight: 1.6 }}>
                  重放只看状态是否复现：2xx 要求状态码相同，4xx/5xx 只要求同分桶。凭据头已脱敏，不会随重放发出。
                </Text>
              </Box>
            ) : null}
            {detail ? (
              <>
                <Text size="1" weight="medium" style={{ display: "block", margin: "6px 0 4px" }}>请求</Text>
                <pre className="replay-md">{detail.req || "(空)"}</pre>
                <Text size="1" weight="medium" style={{ display: "block", margin: "6px 0 4px" }}>响应</Text>
                <pre className="replay-md">{detail.resp || "(空)"}</pre>
              </>
            ) : null}
          </>
        ) : null}
      </Box>
    </ScrollArea>
  );
}

function fmtBytes(n) {
  if (!n && n !== 0) return "—";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

// --- Pentest report ------------------------------------------------------
export function PentestReportPanel({ taskId }) {
  const [md, setMd] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    if (!taskId) return;
    setBusy(true); setErr("");
    try {
      const r = await authedFetch(`/api/report/pentest?task=${encodeURIComponent(taskId)}`, { credentials: "include" });
      if (!r.ok) throw new Error(await errorText(r));
      setMd(await r.text());
    } catch (e) { setErr(e.message); }
    finally { setBusy(false); }
  }, [taskId]);

  useEffect(() => { setMd(""); load(); }, [load]);

  const download = () => {
    const blob = new Blob([md], { type: "text/markdown;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `pentest-${taskId || "task"}.md`;
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <ScrollArea style={{ flex: 1, minHeight: 0 }}>
      <Box px="3" py="2">
        {!taskId ? <Text size="1" color="gray">先选择一个运行（任务）——报告是按任务生成的。</Text> : null}
        {err ? <Text size="1" color="red">{err} <Button size="1" variant="soft" onClick={load}>重试</Button></Text> : null}
        {md ? (
          <>
            <Flex align="center" gap="2" mb="2">
              <Text size="1" weight="medium">渗透测试报告</Text>
              <Box style={{ marginLeft: "auto" }} />
              <IconButton size="1" variant="ghost" aria-label="刷新报告" onClick={load}>
                <RefreshCw size={12} className={busy ? "spin" : ""} />
              </IconButton>
              <Button size="1" variant="soft" onClick={download}>
                <Download size={12} /> 下载 Markdown
              </Button>
            </Flex>
            <div className="report-md">
              <Markdown remarkPlugins={[remarkGfm]}>{md}</Markdown>
            </div>
            <Text size="1" color="gray" style={{ display: "block", marginTop: 8, lineHeight: 1.7 }}>
              每次打开都会生成一份新报告并写入一条审计记录。报告只出 Markdown，PDF 需另接渲染器。
            </Text>
          </>
        ) : null}
        {!md && !err && taskId ? <Text size="1" color="gray">生成中…</Text> : null}
      </Box>
    </ScrollArea>
  );
}

export function GateFailure({ reasons, onOverride, overrideBusy }) {
  const [reason, setReason] = useState("");
  if (!reasons?.length) return null;
  return (
    <Box mt="1">
      <Text size="1" color="red" style={{ display: "block", lineHeight: 1.6 }}>
        验证门未通过，无法标记为已确认：
      </Text>
      {reasons.map((r, i) => (
        <Text key={i} size="1" color="red" style={{ display: "block", lineHeight: 1.6 }}>· {r}</Text>
      ))}
      <Flex gap="2" mt="2" align="center">
        <RTextField size="1" placeholder="人工越过理由（必填，会写入审计链）" value={reason}
                    onChange={(e) => setReason(e.target.value)} style={{ flex: 1 }} />
        <Button size="1" variant="soft" disabled={!reason.trim() || overrideBusy}
                onClick={() => onOverride(reason.trim())}>
          人工越过确认
        </Button>
      </Flex>
    </Box>
  );
}
