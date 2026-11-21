// Findings page (S6): the console's findings list + detail.
//
// The console previously had no findings page — the only place a finding
// could be confirmed was the System side panel's evidence tab (System.jsx),
// one row at a time. This page is the global list: filter by severity /
// status / task / vulnclass, server-side paging, and a detail view with the
// report, evidence, lineage, traffic bindings and retest history.
//
// Backend contracts (all retained after the S6 trim):
//   GET  /api/exploration/findings?page=&limit=&severity=&status=&vulnclass=&task_id=&q=&sort=
//        {items, total, page, page_size} (no page/limit -> bare array)
//   GET  /api/exploration/findings/stats   {total, pending, critical, high, medium, low, vulnclasses, tasks[]}
//   GET  /api/exploration/findings/{id}    detail DTO incl. report
//   GET  /api/exploration/findings/{id}/lineage   {nodes, edges}
//   GET  /api/exploration/findings/export?scope=filtered|selected&format=md-single|md-zip|csv|json
//   DELETE /api/exploration/findings/{id}
//   PATCH  /api/exploration/findings/{id}  {status:"confirmed"} -> 422 {error, reasons} on gate failure
//   POST /api/exploration/findings/{id}/retests   {notes} -> {retest, created}
//   GET  /api/exploration/findings/{id}/retests   {retests}
//   GET  /api/exploration/findings/retests/active {retests}
// The list items carry id == finding_id == the standalone findings-table id,
// which is the handle for PATCH / DELETE / retests. The confirm flow reuses
// patchFindingConfirm + GateFailure from Stage2.jsx — there is exactly one
// confirm implementation in the console.
import { useCallback, useEffect, useState } from "react";
import { Badge, Box, Button, Dialog, Flex, IconButton, ScrollArea, Separator, Text, TextField as RTextField } from "./ui.jsx";
import { ChevronLeft, Download, Play, RefreshCw, ShieldAlert, Trash2 } from "lucide-react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { toast } from "./toast.jsx";
import { authHeaders, authedFetch, errorText } from "./lib/auth.js";
import { GateFailure, patchFindingConfirm } from "./Stage2.jsx";

async function apiJSON(path, opts) {
  const r = await authedFetch(path, { credentials: "include", ...opts });
  if (!r.ok) throw new Error(await errorText(r));
  return r.json();
}

function fmtTime(ts) {
  if (!ts) return "—";
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? String(ts) : d.toLocaleString();
}

const SEVERITY = {
  critical: { label: "严重", color: "red" },
  high: { label: "高", color: "orange" },
  medium: { label: "中", color: "amber" },
  low: { label: "低", color: "gray" },
};

const STATUS = {
  pending: { label: "待确认", color: "gray" },
  in_progress: { label: "处理中", color: "blue" },
  confirmed: { label: "已确认", color: "green" },
  resolved: { label: "已解决", color: "green" },
  fixed: { label: "已修复", color: "green" },
  false_positive: { label: "误报", color: "gray" },
  ignored: { label: "已忽略", color: "gray" },
  duplicate: { label: "重复", color: "gray" },
  risk_accepted: { label: "接受风险", color: "amber" },
};

function sevOf(f) { return SEVERITY[f.severity] || { label: f.severity || "—", color: "gray" }; }
function statusOf(f) { return STATUS[f.status] || { label: f.status || "—", color: "gray" }; }
function displayName(f) { return f.name || f.vulnclass || `发现 #${f.finding_id || f.id}`; }
function findingKey(f) { return String(f.finding_id || f.id); }

function taskLabel(f) {
  if (f.task_description) return f.task_description;
  if (f.task_id) return "任务 #" + f.task_id;
  return "任务已删除";
}

// The export endpoint streams a file (Content-Disposition attachment).
async function downloadExport(qs, fallbackName) {
  const r = await authedFetch(`/api/exploration/findings/export?${qs}`, { credentials: "include" });
  if (!r.ok) throw new Error(await errorText(r));
  const blob = await r.blob();
  const m = (r.headers.get("Content-Disposition") || "").match(/filename="([^"]+)"/);
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = (m && m[1]) || fallbackName;
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 5000);
}

export default function Findings({ initialTaskId, onOpenTask, onClose }) {
  const [stats, setStats] = useState(null);
  const [statsErr, setStatsErr] = useState("");
  const [severity, setSeverity] = useState("");
  const [status, setStatus] = useState("");
  const [vulnclass, setVulnclass] = useState("");
  const [taskId, setTaskId] = useState(initialTaskId ? String(initialTaskId) : "");
  const [q, setQ] = useState("");
  const [sort, setSort] = useState("severity");
  const [page, setPage] = useState(1);
  const [data, setData] = useState(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [exportFmt, setExportFmt] = useState("md-single");
  const [exportBusy, setExportBusy] = useState(false);
  const [selectedId, setSelectedId] = useState(null);

  const loadStats = useCallback(async () => {
    try { setStats(await apiJSON("/api/exploration/findings/stats")); setStatsErr(""); }
    catch (e) { setStatsErr(e.message); }
  }, []);
  useEffect(() => { loadStats(); }, [loadStats]);

  const filterParams = useCallback(() => {
    const qs = new URLSearchParams();
    if (severity) qs.set("severity", severity);
    if (status) qs.set("status", status);
    if (vulnclass) qs.set("vulnclass", vulnclass);
    if (taskId) qs.set("task_id", taskId);
    if (q.trim()) qs.set("q", q.trim());
    if (sort) qs.set("sort", sort);
    return qs;
  }, [severity, status, vulnclass, taskId, q, sort]);

  const load = useCallback(async (p) => {
    setBusy(true); setErr("");
    try {
      const qs = filterParams();
      qs.set("page", String(p));
      qs.set("limit", "20");
      const d = await apiJSON(`/api/exploration/findings?${qs.toString()}`);
      setData(Array.isArray(d) ? { items: d, total: d.length, page: p, page_size: d.length } : d);
      setPage(p);
    } catch (e) { setErr(e.message); }
    finally { setBusy(false); }
  }, [filterParams]);

  useEffect(() => { setSelectedId(null); load(1); }, [load]);

  const applyFilter = (patch) => {
    if (patch.severity !== undefined) setSeverity(patch.severity);
    if (patch.status !== undefined) setStatus(patch.status);
    if (patch.vulnclass !== undefined) setVulnclass(patch.vulnclass);
    if (patch.taskId !== undefined) setTaskId(patch.taskId);
    // page resets via the load effect (filterParams changes -> load(1)).
  };

  const doExport = async () => {
    setExportBusy(true);
    try {
      const qs = filterParams();
      qs.set("scope", "filtered");
      qs.set("format", exportFmt);
      await downloadExport(qs.toString(), `findings.${exportFmt === "csv" ? "csv" : exportFmt === "json" ? "json" : exportFmt === "md-zip" ? "zip" : "md"}`);
      toast.success("已导出当前筛选的发现");
    } catch (e) { toast.error("导出失败：" + e.message); }
    finally { setExportBusy(false); }
  };

  if (selectedId) {
    return (
      <FindingDetail
        findingId={selectedId}
        onBack={() => { setSelectedId(null); load(page); loadStats(); }}
        onOpenTask={onOpenTask}
      />
    );
  }

  const items = data?.items || [];
  const total = data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / 20));

  const statCards = [
    { key: "total", label: "全部", value: stats?.total ?? "—", onClick: () => applyFilter({ severity: "", status: "" }), active: !severity && !status },
    { key: "pending", label: "待确认", value: stats?.pending ?? "—", onClick: () => applyFilter({ status: "pending", severity: "" }), active: status === "pending" },
    { key: "critical", label: "严重", value: stats?.critical ?? "—", onClick: () => applyFilter({ severity: "critical", status: "" }), active: severity === "critical" },
    { key: "high", label: "高", value: stats?.high ?? "—", onClick: () => applyFilter({ severity: "high", status: "" }), active: severity === "high" },
    { key: "medium", label: "中", value: stats?.medium ?? "—", onClick: () => applyFilter({ severity: "medium", status: "" }), active: severity === "medium" },
    { key: "low", label: "低", value: stats?.low ?? "—", onClick: () => applyFilter({ severity: "low", status: "" }), active: severity === "low" },
  ];

  return (
    <div style={{ flex: 1, minHeight: 0, overflow: "auto", padding: "16px 22px 40px" }}>
      <div style={{ maxWidth: 860, display: "flex", flexDirection: "column", gap: 12 }}>
        <Flex align="center" gap="2">
          <ShieldAlert size={15} />
          <Text size="3" weight="medium">发现</Text>
          <Badge size="1" variant="soft">{data ? `${total} 条` : "…"}</Badge>
          <Box style={{ marginLeft: "auto" }} />
          <select value={exportFmt} onChange={(e) => setExportFmt(e.target.value)} className="traffic-select" aria-label="导出格式">
            <option value="md-single">Markdown</option>
            <option value="csv">CSV</option>
            <option value="json">JSON</option>
            <option value="md-zip">Markdown 压缩包</option>
          </select>
          <Button size="1" variant="soft" onClick={doExport} disabled={exportBusy || !total}>
            <Download size={12} /> {exportBusy ? "导出中…" : "导出当前筛选"}
          </Button>
          <IconButton size="1" variant="ghost" aria-label="刷新发现列表" onClick={() => { load(page); loadStats(); }}>
            <RefreshCw size={12} className={busy ? "spin" : ""} />
          </IconButton>
        </Flex>

        {statsErr ? (
          <Text size="1" color="red">统计加载失败：{statsErr}</Text>
        ) : (
          <div style={{ display: "grid", gridTemplateColumns: "repeat(6, minmax(0,1fr))", gap: 4 }}>
            {statCards.map((s) => (
              <button key={s.key} type="button" onClick={s.onClick}
                      className={"findings-stat" + (s.active ? " active" : "")}
                      style={{
                        background: "var(--c-surface-2)",
                        border: "1px solid var(--c-border-1)",
                        borderRadius: 8, padding: "8px 10px", textAlign: "left", cursor: "pointer",
                      }}>
                <div style={{ fontSize: 11, color: "var(--c-ink-4)", letterSpacing: ".04em" }}>{s.label}</div>
                <div style={{ fontFamily: "var(--font-mono)", fontSize: 18, marginTop: 2, color: "var(--c-ink)" }}>{s.value}</div>
              </button>
            ))}
          </div>
        )}

        <Flex gap="2" wrap="wrap" align="center">
          <select value={severity} onChange={(e) => setSeverity(e.target.value)} className="traffic-select" aria-label="严重程度过滤">
            <option value="">全部严重程度</option>
            {Object.entries(SEVERITY).map(([k, v]) => <option key={k} value={k}>{v.label}</option>)}
          </select>
          <select value={status} onChange={(e) => setStatus(e.target.value)} className="traffic-select" aria-label="状态过滤">
            <option value="">全部状态</option>
            {Object.entries(STATUS).map(([k, v]) => <option key={k} value={k}>{v.label}</option>)}
          </select>
          <select value={taskId} onChange={(e) => setTaskId(e.target.value)} className="traffic-select" aria-label="任务过滤">
            <option value="">全部任务</option>
            {(stats?.tasks || []).map((t) => (
              <option key={t.id} value={String(t.id)}>
                {(t.description || t.name || `任务 #${t.id}`)}（{t.count}）
              </option>
            ))}
          </select>
          <select value={vulnclass} onChange={(e) => setVulnclass(e.target.value)} className="traffic-select" aria-label="漏洞类型过滤">
            <option value="">全部类型</option>
            {(stats?.vulnclasses || []).map((v) => <option key={v} value={v}>{v}</option>)}
          </select>
          <select value={sort} onChange={(e) => setSort(e.target.value)} className="traffic-select" aria-label="排序">
            <option value="severity">按严重程度</option>
            <option value="time">按时间</option>
          </select>
          <RTextField.Root size="1" placeholder="搜索名称 / 摘要…" value={q}
                      onChange={(e) => setQ(e.target.value)}
                      onKeyDown={(e) => { if (e.key === "Enter") load(1); }} style={{ width: 170 }} />
          <Button size="1" variant="soft" onClick={() => load(1)} disabled={busy}>筛选</Button>
          {(severity || status || taskId || vulnclass || q.trim()) ? (
            <Button size="1" variant="ghost" onClick={() => { setSeverity(""); setStatus(""); setTaskId(""); setVulnclass(""); setQ(""); }}>清除筛选</Button>
          ) : null}
        </Flex>

        {err ? (
          <Text size="1" color="red">{err} <Button size="1" variant="soft" onClick={() => load(page)}>重试</Button></Text>
        ) : null}

        {items.map((f) => {
          const sev = sevOf(f);
          const st = statusOf(f);
          const bindings = f.traffic_bindings || [];
          return (
            <div key={findingKey(f)} className="cap-row findings-row" role="button" tabIndex={0}
                 onClick={() => setSelectedId(findingKey(f))}
                 onKeyDown={(e) => { if (e.key === "Enter") setSelectedId(findingKey(f)); }}>
              <Flex align="center" gap="2" style={{ width: "100%" }}>
                <Badge size="1" variant="soft" color={sev.color}>{sev.label}</Badge>
                <Badge size="1" variant="soft" color={st.color}>{st.label}</Badge>
                <Text size="2" weight="medium" truncate>{displayName(f)}</Text>
                <Box style={{ marginLeft: "auto" }} />
                <Text size="1" color="gray" style={{ whiteSpace: "nowrap" }}>{fmtTime(f.ts)}</Text>
              </Flex>
              {f.summary ? (
                <div className="cap-detail" style={{ display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}>
                  {f.summary}
                </div>
              ) : null}
              <div className="cap-detail mono">
                {taskLabel(f)}
                {f.traffic_count ? ` · 流量 ${f.traffic_count} 条` : ""}
                {bindings.length ? ` · 证据绑定 ${bindings.length} 条` : ""}
                {(f.assets || []).length ? ` · 资产 ${f.assets.map((a) => a.label).join("、")}` : ""}
              </div>
            </div>
          );
        })}
        {data && !items.length && !err ? (
          <Text size="2" color="gray">
            没有匹配的发现。{taskId ? "这个任务还没有登记漏洞，" : ""}改宽筛选条件，或等一次运行登记新的发现。
          </Text>
        ) : null}
        {!data && !err ? <Text size="2" color="gray">读取中…</Text> : null}

        {data ? (
          <Flex gap="2" mt="2" align="center">
            <Button size="1" variant="soft" disabled={page <= 1 || busy} onClick={() => load(page - 1)}>上一页</Button>
            <Text size="1" color="gray">第 {page} / {pages} 页 · 共 {total} 条</Text>
            <Button size="1" variant="soft" disabled={busy || page >= pages} onClick={() => load(page + 1)}>下一页</Button>
          </Flex>
        ) : null}

        <Text size="1" color="gray" style={{ lineHeight: 1.7 }}>
          列表只读发现的登记状态：确认要过验证门（证据 + 复现 + 复测），点进详情里操作。
          统计卡片可点：点「严重」只看严重，点「待确认」只看待确认，点「全部」清掉严重程度与状态筛选。
        </Text>
      </div>
    </div>
  );
}

// --- Detail --------------------------------------------------------------
function FindingDetail({ findingId, onBack, onOpenTask }) {
  const [f, setF] = useState(null);
  const [lineage, setLineage] = useState(null);
  const [retests, setRetests] = useState([]);
  const [active, setActive] = useState([]);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [gateReasons, setGateReasons] = useState([]);
  const [confirmBusy, setConfirmBusy] = useState(false);
  const [confirmErr, setConfirmErr] = useState("");
  const [notes, setNotes] = useState("");
  const [retestBusy, setRetestBusy] = useState(false);
  const [retestErr, setRetestErr] = useState("");
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [openRetest, setOpenRetest] = useState(null);

  const loadDetail = useCallback(async () => {
    setBusy(true); setErr("");
    try {
      const d = await apiJSON(`/api/exploration/findings/${encodeURIComponent(findingId)}`);
      setF(d);
    } catch (e) { setErr(e.message); }
    finally { setBusy(false); }
  }, [findingId]);

  // Retests + active are fetched once per open (and once after starting a
  // retest) — no polling loop; a retest runs as its own conversation and its
  // verdict lands in the list the next time the detail is opened.
  const loadRetests = useCallback(async () => {
    try {
      const d = await apiJSON(`/api/exploration/findings/${encodeURIComponent(findingId)}/retests`);
      setRetests(d.retests || []);
      const a = await apiJSON("/api/exploration/findings/retests/active");
      setActive(a.retests || []);
    } catch { /* retest history is not the critical path */ }
  }, [findingId]);

  const loadLineage = useCallback(async () => {
    try {
      setLineage(await apiJSON(`/api/exploration/findings/${encodeURIComponent(findingId)}/lineage`));
    } catch { setLineage({ nodes: [], edges: [] }); }
  }, [findingId]);

  useEffect(() => {
    setF(null); setGateReasons([]); setConfirmErr(""); setRetestErr(""); setNotes("");
    setRetests([]); setActive([]); setLineage(null); setOpenRetest(null);
    loadDetail(); loadLineage(); loadRetests();
  }, [findingId, loadDetail, loadLineage, loadRetests]);

  const isActive = active.some((a) => String(a.finding_id) === String(findingId));

  const confirm = async (humanOverride) => {
    setConfirmBusy(true); setConfirmErr("");
    try {
      const res = await patchFindingConfirm(findingId, humanOverride);
      if (res.ok) {
        setGateReasons([]);
        toast.success("已标记为已确认");
        await loadDetail();
      } else if (res.reasons?.length) {
        setGateReasons(res.reasons);
      } else {
        setConfirmErr(res.message || "确认失败");
      }
    } catch (e) { setConfirmErr(e.message); }
    finally { setConfirmBusy(false); }
  };

  const startRetest = async () => {
    setRetestBusy(true); setRetestErr("");
    try {
      const r = await authedFetch(`/api/exploration/findings/${encodeURIComponent(findingId)}/retests`, {
        method: "POST", credentials: "include",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ notes: notes.trim() }),
      });
      if (!r.ok) throw new Error(await errorText(r));
      const d = await r.json();
      toast.success(d.created ? "复测已发起" : "已有进行中的复测");
      await loadRetests();
    } catch (e) { setRetestErr(e.message); }
    finally { setRetestBusy(false); }
  };

  const exportOne = async () => {
    try {
      const qs = new URLSearchParams({ scope: "selected", ids: findingId, format: "md-single" });
      await downloadExport(qs.toString(), `finding-${findingId}.md`);
      toast.success("已导出这条发现");
    } catch (e) { toast.error("导出失败：" + e.message); }
  };

  const doDelete = async () => {
    setDeleteBusy(true);
    try {
      const r = await authedFetch(`/api/exploration/findings/${encodeURIComponent(findingId)}`, {
        method: "DELETE", credentials: "include", headers: authHeaders(),
      });
      if (!r.ok) throw new Error(await errorText(r));
      toast.success("已删除这条发现");
      setDeleteOpen(false);
      onBack();
    } catch (e) { toast.error("删除失败：" + e.message); }
    finally { setDeleteBusy(false); }
  };

  if (!f && !err) {
    return (
      <div style={{ flex: 1, minHeight: 0, overflow: "auto", padding: "16px 22px 40px" }}>
        <Text size="2" color="gray">读取中…</Text>
      </div>
    );
  }
  if (!f) {
    return (
      <div style={{ flex: 1, minHeight: 0, overflow: "auto", padding: "16px 22px 40px" }}>
        <Button size="1" variant="soft" onClick={onBack}><ChevronLeft size={12} /> 返回列表</Button>
        <Text size="2" color="red" style={{ display: "block", marginTop: 12 }}>{err || "这条发现不存在或已被删除。"}</Text>
      </div>
    );
  }

  const sev = sevOf(f);
  const st = statusOf(f);
  const bindings = f.traffic_bindings || [];
  const nodes = lineage?.nodes || [];

  return (
    <div style={{ flex: 1, minHeight: 0, overflow: "auto", padding: "16px 22px 40px" }}>
      <div style={{ maxWidth: 860, display: "flex", flexDirection: "column", gap: 12 }}>
        <Flex align="center" gap="2">
          <Button size="1" variant="soft" onClick={onBack}><ChevronLeft size={12} /> 返回列表</Button>
          <Box style={{ marginLeft: "auto" }} />
          <Button size="1" variant="soft" onClick={exportOne}><Download size={12} /> 导出本条</Button>
          <Button size="1" variant="soft" color="red" onClick={() => setDeleteOpen(true)}>
            <Trash2 size={12} /> 删除
          </Button>
        </Flex>

        <Flex align="center" gap="2" wrap="wrap">
          <Badge size="1" variant="soft" color={sev.color}>{sev.label}</Badge>
          <Badge size="1" variant="soft" color={st.color}>{st.label}</Badge>
          {isActive ? <Badge size="1" variant="soft" color="blue">复测中</Badge> : null}
          <Text size="3" weight="medium" style={{ minWidth: 0 }}>{displayName(f)}</Text>
        </Flex>
        <Text size="1" color="gray" className="mono">
          {f.vulnclass ? `${f.vulnclass} · ` : ""}发现 #{findingKey(f)}
          {f.task_id ? (
            <> · <button type="button" className="linkbtn" onClick={() => onOpenTask?.(f.task_id)}>
              {taskLabel(f)}
            </button></>
          ) : null}
          {` · ${fmtTime(f.ts)}`}
        </Text>
        {err ? <Text size="1" color="red">{err}</Text> : null}

        {/* Confirm: the same gate + override as the System evidence tab. */}
        {f.status !== "confirmed" ? (
          <Box>
            <Flex gap="2" align="center">
              <Button size="2" variant="solid" onClick={() => confirm()} disabled={confirmBusy}>
                <ShieldAlert size={13} /> {confirmBusy ? "确认中…" : "标记为已确认"}
              </Button>
              <Text size="1" color="gray">确认要过验证门：证据绑定 + 复现 + 复测结论，缺一项会列在下面。</Text>
            </Flex>
            <GateFailure reasons={gateReasons} onOverride={(reason) => confirm(reason)} overrideBusy={confirmBusy} />
            {confirmErr ? <Text size="1" color="red" style={{ display: "block", marginTop: 4 }}>{confirmErr}</Text> : null}
          </Box>
        ) : (
          <Text size="1" color="green">已通过验证门确认（或人工越过并写入审计链）。</Text>
        )}

        {f.summary ? (
          <section>
            <SectionTitle>摘要</SectionTitle>
            <Text size="2" style={{ lineHeight: 1.7, whiteSpace: "pre-wrap" }}>{f.summary}</Text>
          </section>
        ) : null}

        {f.report ? (
          <section>
            <SectionTitle>报告</SectionTitle>
            {f.report_stale ? (
              <Text size="1" color="amber" style={{ display: "block", marginBottom: 6, lineHeight: 1.6 }}>
                报告已过期：证据在报告生成后有更新，结论以最新证据为准。
              </Text>
            ) : null}
            <div className="report-md">
              <Markdown remarkPlugins={[remarkGfm]}>{f.report}</Markdown>
            </div>
          </section>
        ) : null}

        {f.evidence ? (
          <section>
            <SectionTitle>证据</SectionTitle>
            <pre className="replay-md">{f.evidence}</pre>
          </section>
        ) : null}

        {(f.assets || []).length ? (
          <section>
            <SectionTitle>资产</SectionTitle>
            {(f.assets || []).map((a) => (
              <div key={a.id} className="cap-row">
                <Flex gap="2" align="center">
                  <Badge size="1" variant="soft">{a.type}</Badge>
                  <Text size="1" className="mono" style={{ wordBreak: "break-all" }}>{a.label}</Text>
                </Flex>
              </div>
            ))}
          </section>
        ) : null}

        <section>
          <SectionTitle>流量绑定（{bindings.length} 条）</SectionTitle>
          {bindings.length ? bindings.map((b) => (
            <div key={b.id} className="cap-row">
              <Flex gap="2" align="center">
                {b.role ? <Badge size="1" variant="soft">{b.role}</Badge> : null}
                <Text size="1" weight="medium" className="mono">{b.snapshot?.method || ""}</Text>
                {b.snapshot?.status ? (
                  <Badge size="1" variant="soft" color={b.snapshot.status >= 500 ? "red" : b.snapshot.status >= 400 ? "amber" : "green"}>
                    {b.snapshot.status}
                  </Badge>
                ) : null}
                <Text size="1" className="mono" truncate>{b.snapshot?.url || b.snapshot_id || "(无快照)"}</Text>
              </Flex>
              {b.note ? <div className="cap-detail">{b.note}</div> : null}
            </div>
          )) : (
            <Text size="1" color="gray">尚未绑定流量证据——绑定入口在 System 侧栏的流量面板，绑定后这里会列出来。</Text>
          )}
        </section>

        <section>
          <SectionTitle>血缘（{nodes.length} 个节点）</SectionTitle>
          {nodes.length ? (
            <div>
              {nodes.map((n, i) => (
                <div key={n.id} className="cap-row">
                  <Flex gap="2" align="center">
                    <Text size="1" color="gray" className="mono">#{i + 1}</Text>
                    <Badge size="1" variant="soft">{n.type}</Badge>
                    <Text size="1" className="mono" style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {lineageLabel(n)}
                    </Text>
                    <Text size="1" color="gray">{fmtTime(n.ts)}</Text>
                  </Flex>
                  <div className="cap-detail mono">{n.state}{n.origin ? ` · ${n.origin}` : ""}</div>
                </div>
              ))}
            </div>
          ) : (
            <Text size="1" color="gray">无血缘：源任务已删除，或这条发现没有关联探索节点。</Text>
          )}
        </section>

        <section>
          <SectionTitle>复测记录（{retests.length} 条）{isActive ? " · 有复测进行中" : ""}</SectionTitle>
          <Flex gap="2" mb="2" align="center">
            <RTextField.Root size="1" placeholder="复测补充说明（可选，最多 4000 字）" value={notes}
                        onChange={(e) => setNotes(e.target.value)} style={{ flex: 1 }} />
            <Button size="1" variant="soft" onClick={startRetest} disabled={retestBusy || isActive}>
              <Play size={12} /> {retestBusy ? "发起中…" : isActive ? "复测进行中" : "发起复测"}
            </Button>
          </Flex>
          {retestErr ? <Text size="1" color="red" style={{ display: "block", marginBottom: 6 }}>{retestErr}</Text> : null}
          {retests.length ? retests.map((r) => (
            <div key={r.id} className="cap-row" role="button" tabIndex={0}
                 onClick={() => setOpenRetest(openRetest === r.id ? null : r.id)}
                 onKeyDown={(e) => { if (e.key === "Enter") setOpenRetest(openRetest === r.id ? null : r.id); }}
                 style={{ cursor: "pointer" }}>
              <Flex gap="2" align="center">
                <Badge size="1" variant="soft" color={r.status === "pending" || r.status === "running" ? "blue" : "gray"}>
                  {r.status === "pending" ? "等待中" : r.status === "running" ? "复测中" : r.status === "done" ? "已完成" : r.status}
                </Badge>
                {r.verdict ? (
                  <Badge size="1" variant="soft" color={r.verdict === "reproduced" ? "red" : r.verdict === "fixed" ? "green" : "amber"}>
                    {r.verdict === "reproduced" ? "已复现" : r.verdict === "fixed" ? "已修复" : "未定论"}
                  </Badge>
                ) : null}
                <Text size="1" style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {r.summary || r.notes || "(无摘要)"}
                </Text>
                <Text size="1" color="gray">{fmtTime(r.created_at)}</Text>
              </Flex>
              {openRetest === r.id && r.evidence ? (
                <pre className="replay-md">{r.evidence}</pre>
              ) : null}
              {openRetest === r.id && r.error ? (
                <Text size="1" color="red" style={{ display: "block", marginTop: 4 }}>{r.error}</Text>
              ) : null}
            </div>
          )) : (
            <Text size="1" color="gray">还没有复测记录。发起后由复测 Agent 独立验证，结论为已修复时原漏洞会自动标为已修复。</Text>
          )}
        </section>

        <Dialog.Root open={deleteOpen} onOpenChange={setDeleteOpen}>
          <Dialog.Content maxWidth="440px">
            <Dialog.Title size="4">删除这条发现？</Dialog.Title>
            <Dialog.Description size="2" color="gray">
              将删除「{displayName(f)}」及其来源探索节点，删除后无法恢复。流量快照与审计记录不受影响。
            </Dialog.Description>
            <Flex gap="2" mt="4" justify="end">
              <Button size="2" variant="soft" onClick={() => setDeleteOpen(false)}>取消</Button>
              <Button size="2" variant="solid" color="red" disabled={deleteBusy} onClick={doDelete}>
                {deleteBusy ? "删除中…" : "确认删除"}
              </Button>
            </Flex>
          </Dialog.Content>
        </Dialog.Root>
      </div>
    </div>
  );
}

function SectionTitle({ children }) {
  return (
    <Text size="1" weight="medium" style={{ display: "block", marginBottom: 6, letterSpacing: ".04em" }}>
      {children}
    </Text>
  );
}

// Lineage nodes carry a raw JSON payload; pull the most identifying line.
function lineageLabel(n) {
  try {
    const p = JSON.parse(n.payload || "{}");
    return p.summary || p.goal || p.description || p.name || p.vulnclass
      || (p.method && p.url ? `${p.method} ${p.url}` : "") || String(n.payload || "").slice(0, 120) || n.type;
  } catch {
    return String(n.payload || "").slice(0, 120) || n.type;
  }
}
