/* artex-harness console — zero-build, same-origin, no external deps.
 *
 * Layout (docs/07 §P1): three columns.
 *   left  : tasks · project tree · conversation composer
 *   middle: the run as an event stream grouped by revision (plus Plan/Replay/Eval/平台)
 *   right : status · files · DIFF · budget · browser · intervention
 *
 * Data: SSE /api/harness/stream for events; /api/harness/* for the panels.
 */
"use strict";

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
const esc = (s) =>
  String(s ?? "").replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const S = {
  tasks: [], task: null, es: null,
  seen: new Set(), turns: new Map(),
  intents: new Map(), selected: null, planOrder: [], planDirty: false,
  dockTab: "status", files: [], diffFiles: [], diffPath: null, budget: null,
  delegations: [], browser: null, ready: null, evalFailing: false,
};

/* ── token ─────────────────────────────────────────────────────────────────
 * The backend accepts the JWT as `Authorization: Bearer` OR as the `artex_token`
 * cookie. We set both: the header for fetch, the cookie for EventSource (which
 * cannot set headers) — same approach as the upstream client.
 */
const TOKEN_KEY = "artex.harness.token";
const getToken = () => { try { return localStorage.getItem(TOKEN_KEY) || ""; } catch { return ""; } };
function setToken(tok) {
  try { tok ? localStorage.setItem(TOKEN_KEY, tok) : localStorage.removeItem(TOKEN_KEY); } catch {}
  document.cookie = "artex_token=" + encodeURIComponent(tok || "") + "; path=/; SameSite=Strict";
}

async function api(path, opts = {}) {
  const headers = Object.assign({}, opts.headers);
  if (opts.body) headers["Content-Type"] = "application/json";
  const tok = getToken();
  if (tok) headers["Authorization"] = "Bearer " + tok;
  const res = await fetch(path, { credentials: "same-origin", ...opts, headers });
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = text; }
  if (!res.ok) {
    if (res.status === 401 && !String(path).startsWith("/api/auth/")) { setToken(""); showGate(); }
    const err = new Error((data && (data.error || data.message)) || res.statusText || `HTTP ${res.status}`);
    err.status = res.status;
    throw err;
  }
  return data;
}

let toastTimer = null;
function toast(msg, bad = false) {
  const el = $("#toast");
  el.textContent = msg;
  el.className = "toast" + (bad ? " bad" : "");
  el.hidden = false;
  // 入场/离场各走一次过渡：直接 hidden 会让提示"啪"地出现，像是渲染错误。
  requestAnimationFrame(() => el.classList.add("show"));
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    el.classList.remove("show");
    setTimeout(() => { el.hidden = true; }, 180);
  }, 3600);
}

/* ── auth ─────────────────────────────────────────────────────────────── */
function showGate() { $("#gate").hidden = false; $("#app").hidden = true; $("#gate-pass").focus(); }

async function boot() {
  let st = null;
  try { st = await api("/api/auth/status"); } catch {}
  if (!st || !st.initialized) { $("#gate-hint").textContent = "首次使用：设置管理密码（用户名固定为 ARTEX）"; return showGate(); }
  if (!getToken()) return showGate();
  try { await api("/api/tasks"); enterApp(); } catch { setToken(""); showGate(); }
}

$("#gate-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const pass = $("#gate-pass").value, errEl = $("#gate-err");
  errEl.hidden = true;
  try {
    const st = await api("/api/auth/status");
    if (!st.initialized) await api("/api/auth/init", { method: "POST", body: JSON.stringify({ password: pass }) });
    const r = await api("/api/auth/login", { method: "POST", body: JSON.stringify({ username: "ARTEX", password: pass }) });
    if (r && r.token) setToken(r.token);
    $("#gate").hidden = true;
    enterApp();
  } catch (e) { errEl.textContent = e.message; errEl.hidden = false; }
});

/* ── shell ────────────────────────────────────────────────────────────── */
function enterApp() {
  $("#gate").hidden = true;
  $("#app").hidden = false;
  loadTasks();
  loadReady();
  setInterval(() => { if (!document.hidden) { loadTasks(true); loadReady(); } }, 20000);
}

async function loadReady() {
  try {
    S.ready = await api("/api/harness/ready");
    const llm = S.ready.llm || {};
    const pill = $("#pill-llm");
    pill.textContent = llm.ok ? "llm ✓" : "llm ✗";
    pill.className = "pill mono " + (llm.ok ? "done" : "bad");
    pill.title = (llm.detail || "") + (llm.hint ? "\n" + llm.hint : "");
    if (!llm.ok) toast("引擎 idle：" + (llm.hint || "未配置模型"), true);
  } catch {}
}

async function loadTasks(quiet = false) {
  try {
    const data = await api("/api/tasks");
    const list = Array.isArray(data) ? data : (data.tasks || data.items || []);
    S.tasks = list;
    renderTasks();
    if (!S.task && list.length) selectTask(list[0].id);
  } catch (e) { if (!quiet) toast("加载任务失败：" + e.message, true); }
}

function statusClass(s) {
  if (s === "running") return "run";
  if (s === "done" || s === "achieved") return "done";
  if (s === "failed" || s === "timeout") return "bad";
  return "";
}

function renderTasks() {
  $("#task-list").innerHTML = S.tasks.map((t) => {
    const active = S.task && String(S.task.id) === String(t.id);
    const mode = t.harness_mode ? "H" : "A";
    return `<li class="task-item${active ? " active" : ""}" data-id="${esc(t.id)}" title="${esc(t.goal || "")}">
      <span class="t-goal">${esc(t.goal || t.description || "task " + t.id)}</span>
      <span class="t-meta">
        <span class="pill ${statusClass(t.status)}" style="padding:0 5px">${esc(t.status)}</span>
        <span class="tag">${mode}</span>
        <span>#${esc(t.id)}</span>
        ${t.llm_ready === false ? '<span class="tag exec">no-llm</span>' : ""}
      </span></li>`;
  }).join("");
  $("#rail-stat").textContent = `${S.tasks.length}`;
}

$("#task-list").addEventListener("click", (ev) => {
  const li = ev.target.closest(".task-item");
  if (li) selectTask(li.dataset.id);
});

function selectTask(id) {
  const t = S.tasks.find((x) => String(x.id) === String(id));
  if (!t) return;
  if (S.task && String(S.task.id) === String(id)) return;
  S.task = t;
  S.seen.clear(); S.turns.clear(); S.intents.clear();
  S.selected = null; S.planOrder = []; S.planDirty = false;
  S.files = []; S.diffFiles = []; S.diffPath = null; S.budget = null; S.delegations = []; S.browser = null;
  $("#stream").innerHTML = "";
  $("#task-goal").textContent = t.goal || t.description || "task " + t.id;
  $("#pill-status").textContent = t.status || "—";
  $("#pill-status").className = "pill " + statusClass(t.status);
  renderTasks();
  connectStream(t.id);
  refreshThreadMeta();
  refreshDock();
  loadIntents();
  if (t.harness_mode === false) toast("该任务是历史 ARTEX 管线（planner/worker），HARNESS 面板部分不可用", true);
}

async function refreshThreadMeta() {
  if (!S.task) return;
  try {
    const th = await api(`/api/harness/thread?task=${encodeURIComponent(S.task.id)}`);
    $("#pill-rev").textContent = `rev ${th.latest ?? 0}`;
    $("#pill-status").textContent = th.status || "—";
    $("#pill-status").className = "pill " + statusClass(th.status);
  } catch {}
}

/* ── stream ───────────────────────────────────────────────────────────── */
function connectStream(taskId) {
  if (S.es) { S.es.close(); S.es = null; }
  const live = $("#pill-live");
  live.textContent = "● 连接中";
  live.className = "pill mono";
  const es = new EventSource(`/api/harness/stream?task=${encodeURIComponent(taskId)}`);
  S.es = es;
  es.onopen = () => { live.textContent = "● 实时"; live.className = "pill mono live"; };
  es.onerror = () => { live.textContent = "● 断开（自动重连）"; live.className = "pill mono dead"; };
  const onEnv = (ev) => { try { renderEnvelope(JSON.parse(ev.data)); } catch {} };
  es.addEventListener("harness", onEnv);
  es.onmessage = onEnv;
}

function renderEnvelope(env) {
  const it = env.item || {};
  const aid = it.activity_id || 0;
  if (aid && S.seen.has(aid)) return;
  if (aid) S.seen.add(aid);

  const key = env.rev > 0 ? env.rev : "legacy";
  let turn = S.turns.get(key);
  if (!turn) {
    const kind = (env.turn && env.turn.kind) || "";
    const el = document.createElement("div");
    el.className = "turn " + (kind === "system" ? "human" : kind === "planner" ? "planner" : "");
    el.innerHTML = `<div class="turn-head">
        <span class="rev">rev ${esc(env.rev || "—")}</span>
        <span class="kind">${esc(kind || "event")}</span>
        <span class="sum">${esc((env.turn && env.turn.summary) || (env.turn && env.turn.ref) || "")}</span>
      </div><div class="turn-items"></div>`;
    $("#stream").appendChild(el);
    turn = { el, items: el.querySelector(".turn-items") };
    S.turns.set(key, turn);
  }
  turn.items.appendChild(renderItem(it));
  if (pinned) $("#stream").scrollTop = $("#stream").scrollHeight;
  if (env.rev > 0) $("#pill-rev").textContent = `rev ${env.rev}`;
  if ((it.tool === "Agent" || String(it.tool || "").startsWith("mcp__browser__")) && S.dockTab === "status") {
    /* keep panels live without a full reload storm */
  }
}

let pinned = true;
$("#stream").addEventListener("scroll", () => {
  const s = $("#stream");
  pinned = s.scrollHeight - s.scrollTop - s.clientHeight < 80;
});

function renderItem(it) {
  const kind = it.kind || "text";
  const row = document.createElement("div");
  row.className = "item";
  let body = "";
  if (kind === "tool_use") {
    body = `<span class="tool">${esc(it.tool || "tool")}</span> <span class="txt">${esc(it.summary || "")}</span>`;
  } else if (kind === "tool_result") {
    body = `<span class="txt detail">${esc(it.summary || it.detail || "(no output)")}</span>`;
  } else if (kind === "thinking") {
    body = `<span class="txt detail">${esc(it.summary || it.detail || "")}</span>`;
  } else if (kind === "intercept") {
    body = `<span style="color:var(--warn)">${esc(it.summary || "审批请求")}</span>`;
  } else {
    body = `<span class="txt">${esc(it.summary || it.detail || "")}</span>`;
  }
  // 截图标记 → <img>（内容寻址，见 docs/09 §3）
  const thumb = /\[thumb\]\s*([0-9a-f]{64})/.exec(body);
  if (thumb) {
    body = body.replace(thumb[0], "");
    body += ` <a href="/api/harness/shot/${thumb[1]}" target="_blank" rel="noopener">
      <img class="thumb" src="/api/harness/shot/${thumb[1]}" alt="screenshot" loading="lazy" /></a>`;
  }
  if (it.node_id) body += ` <span class="intent-ref" data-intent="${esc(it.node_id)}">#${esc(it.node_id)}</span>`;
  row.innerHTML = `<div class="item-badge ${esc(kind)}">${esc(String(kind).replace("_", " "))}</div>
    <div class="item-body${it.is_error ? " err" : ""}">${body}</div>`;
  const ref = row.querySelector(".intent-ref");
  if (ref) ref.addEventListener("click", () => selectIntent(ref.dataset.intent));
  return row;
}

/* ── left: project tree + composer ────────────────────────────────────── */
async function loadFiles() {
  if (!S.task) return;
  try {
    const r = await api(`/api/harness/files/${encodeURIComponent(S.task.id)}`);
    S.files = r.files || [];
    $("#ws-root").textContent = (r.root || "").replace(/^.*\/tasks\//, "tasks/");
    renderTree();
  } catch (e) { $("#file-tree").innerHTML = `<p class="eval-fail">${esc(e.message)}</p>`; }
}

function renderTree() {
  if (!S.files.length) { $("#file-tree").innerHTML = `<p class="dim xs">工作区还是空的（agent 还没有写文件）。</p>`; return; }
  $("#file-tree").innerHTML = S.files.map((f) => {
    const depth = (f.path.match(/\//g) || []).length;
    const cls = f.dir ? "ft-dir" : "ft-file";
    const chg = f.change ? ` <span class="tag exec">${esc(f.change)}</span>` : "";
    return `<div class="${cls}" style="padding-left:${6 + depth * 11}px"
      ${f.dir ? "" : `data-path="${esc(f.path)}"`}>${esc(f.path.split("/").pop())}${chg}</div>`;
  }).join("");
}

$("#file-tree").addEventListener("click", (ev) => {
  const el = ev.target.closest("[data-path]");
  if (!el) return;
  S.diffPath = el.dataset.path;
  S.dockTab = "diff";
  renderDockTabs();
  refreshDock();
});

async function sendSay() {
  if (!S.task) return toast("先选一个任务", true);
  const box = $("#say");
  const msg = box.value.trim();
  if (!msg) return toast("消息不能为空", true);
  try {
    await api(`/api/tasks/${encodeURIComponent(S.task.id)}/say`, { method: "POST", body: JSON.stringify({ message: msg }) });
    box.value = "";
    toast("已发送：agent 会在同一会话里继续");
    refreshThreadMeta();
  } catch (e) { toast("发送失败：" + e.message, true); }
}
$("#btn-say").addEventListener("click", sendSay);
$("#say").addEventListener("keydown", (ev) => {
  if (ev.key === "Enter" && (ev.metaKey || ev.ctrlKey)) { ev.preventDefault(); sendSay(); }
});
$("#btn-say-stop").addEventListener("click", () => taskControl("pause"));
$("#btn-say-resume").addEventListener("click", () => taskControl("resume"));

async function taskControl(action) {
  if (!S.task) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(S.task.id)}/control`, { method: "POST", body: JSON.stringify({ action }) });
    toast(action === "pause" ? "任务已暂停" : "任务已恢复");
    loadTasks(true); refreshThreadMeta();
  } catch (e) { toast("操作失败：" + e.message, true); }
}
$("#btn-pause").addEventListener("click", () => taskControl("pause"));
$("#btn-resume").addEventListener("click", () => taskControl("resume"));
$("#btn-replan").addEventListener("click", async () => {
  if (!S.task) return;
  try { await api(`/api/tasks/${encodeURIComponent(S.task.id)}/replan`, { method: "POST" }); toast("已请求重规划"); }
  catch (e) { toast("触发失败：" + e.message, true); }
});
$("#btn-refresh").addEventListener("click", () => loadTasks());

/* ── right dock ───────────────────────────────────────────────────────── */
const DOCK = ["status", "files", "diff", "budget", "browser", "act"];
function renderDockTabs() {
  $$("#dock-tabs .dt").forEach((b) => b.classList.toggle("active", b.dataset.dt === S.dockTab));
}
$("#dock-tabs").addEventListener("click", (ev) => {
  const b = ev.target.closest(".dt");
  if (!b) return;
  S.dockTab = b.dataset.dt;
  renderDockTabs();
  refreshDock();
});

function refreshDock() {
  const box = $("#dock-body");
  if (!S.task) { box.innerHTML = `<p class="dim xs">先选一个任务。</p>`; return; }
  switch (S.dockTab) {
    case "status": return renderStatusPanel();
    case "files": return renderFilesPanel();
    case "diff": return renderDiffPanel();
    case "budget": return renderBudgetPanel();
    case "browser": return renderBrowserPanel();
    case "act": return renderActPanel();
  }
}

async function renderStatusPanel() {
  const box = $("#dock-body");
  box.innerHTML = `<p class="dim xs">加载中…</p>`;
  try {
    const w = await api(`/api/harness/workers/${encodeURIComponent(S.task.id)}`);
    S.delegations = w.delegations || [];
    const rows = S.delegations.map((d) => `
      <div class="dcard">
        <div class="dcard-head">
          <span class="tag">${esc(d.agent_type || "subagent")}</span>
          <span class="${d.state === "running" ? "eval-pass" : d.state === "error" ? "eval-fail" : "dim"}">${esc(d.state)}</span>
        </div>
        <div class="dcard-body">${esc(d.prompt || "")}</div>
        ${d.result ? `<div class="dcard-res">${esc(d.result)}</div>` : ""}
      </div>`).join("");
    box.innerHTML = `
      <div class="dstat">
        <span>模式 <b>${w.harness ? "HARNESS" : "历史管线"}</b></span>
        <span>委派 <b>${S.delegations.length}</b></span>
      </div>
      ${rows || `<p class="dim xs">还没有派出子代理。HARNESS 里由 agent 自己决定是否委派。</p>`}
      <p class="dim xs" style="margin-top:8px">${esc(w.note || "")}</p>`;
  } catch (e) { box.innerHTML = `<p class="eval-fail">${esc(e.message)}</p>`; }
}

async function renderFilesPanel() {
  const box = $("#dock-body");
  box.innerHTML = `<p class="dim xs">加载中…</p>`;
  await loadFiles();
  const files = S.files.filter((f) => !f.dir);
  box.innerHTML = `
    <div class="dstat"><span>文件 <b>${files.length}</b></span>
      <span>变更 <b>${files.filter((f) => f.change).length}</b></span></div>
    ${files.map((f) => `
      <div class="file-row" data-path="${esc(f.path)}">
        <span class="fp">${esc(f.path)}</span>
        <span class="mono dim">${f.size ?? 0}B</span>
        ${f.change ? `<span class="tag exec">${esc(f.change)}</span>` : ""}
      </div>`).join("") || `<p class="dim xs">工作区还是空的。</p>`}`;
  $$("#dock-body .file-row").forEach((el) =>
    el.addEventListener("click", () => { S.diffPath = el.dataset.path; S.dockTab = "diff"; renderDockTabs(); refreshDock(); }));
}

async function renderDiffPanel() {
  const box = $("#dock-body");
  box.innerHTML = `<p class="dim xs">加载中…</p>`;
  try {
    const q = S.diffPath ? `?path=${encodeURIComponent(S.diffPath)}` : "";
    const r = await api(`/api/harness/diff/${encodeURIComponent(S.task.id)}${q}`);
    S.diffFiles = r.files || [];
    const list = S.diffFiles.map((f) => `
      <div class="file-row" data-path="${esc(f.path)}">
        <span class="fp">${esc(f.path)}</span>
        <span class="mono"><span class="eval-pass">+${f.additions || 0}</span>
          <span class="eval-fail">−${f.deletions || 0}</span></span>
        <span class="tag">${esc(f.status)}</span>
      </div>`).join("");
    const body = S.diffFiles.find((f) => f.path === S.diffPath);
    box.innerHTML = `
      <div class="dstat"><span>变更 <b>${r.changed || 0}</b></span>
        <span class="${r.baseline ? "dim" : "eval-fail"}">${r.baseline ? "基线已建立" : "无基线"}</span></div>
      ${list || `<p class="dim xs">没有变更。</p>`}
      ${body && body.body ? `<pre class="diff-body">${esc(body.body)}</pre>` : ""}
      <p class="dim xs">${esc(r.note || "")}</p>`;
    $$("#dock-body .file-row").forEach((el) =>
      el.addEventListener("click", () => { S.diffPath = el.dataset.path; refreshDock(); }));
  } catch (e) { box.innerHTML = `<p class="eval-fail">${esc(e.message)}</p>`; }
}

async function renderBudgetPanel() {
  const box = $("#dock-body");
  box.innerHTML = `<p class="dim xs">加载中…</p>`;
  try {
    const b = await api(`/api/harness/budget/${encodeURIComponent(S.task.id)}`);
    S.budget = b;
    const pct = Math.round(b.pct || 0);
    const cls = pct >= 80 ? "eval-fail" : pct >= 50 ? "" : "eval-pass";
    const t = b.totals || {};
    box.innerHTML = `
      <div class="dstat"><span>窗口 <b>${b.window || "?"}</b></span>
        <span>最近输入 <b>${b.last_input || 0}</b></span></div>
      <div class="bar"><div class="bar-fill ${cls}" style="width:${Math.min(pct, 100)}%"></div></div>
      <div class="dim xs">上下文占用约 ${pct}%</div>
      <div class="dstat" style="margin-top:10px">
        <span>输入 <b>${t.InputTokens ?? t.input_tokens ?? 0}</b></span>
        <span>输出 <b>${t.OutputTokens ?? t.output_tokens ?? 0}</b></span>
      </div>
      <div class="dstat"><span>缓存读 <b>${t.CacheReadTokens ?? t.cache_read_tokens ?? 0}</b></span>
        <span>缓存写 <b>${t.CacheWriteTokens ?? t.cache_write_tokens ?? 0}</b></span></div>
      <p class="dim xs">${esc(b.note || "")}</p>`;
  } catch (e) { box.innerHTML = `<p class="eval-fail">${esc(e.message)}</p>`; }
}

async function renderBrowserPanel() {
  const box = $("#dock-body");
  box.innerHTML = `<p class="dim xs">加载中…</p>`;
  try {
    const b = await api(`/api/harness/browser?task=${encodeURIComponent(S.task.id)}`);
    S.browser = b;
    let sess = null;
    try { sess = (await api(`/api/harness/browser/sessions?task=${encodeURIComponent(S.task.id)}`)).session; } catch {}
    const sessHTML = sess ? `
      <div class="dcard">
        <div class="dcard-head"><span class="tag">会话 · 生命周期</span>
          <span class="${sess.alive ? "eval-pass" : "dim"}">${sess.alive ? "存活" : "未启动"}</span></div>
        <div class="dcard-res">${Object.entries(sess.servers || {}).map(([k, v]) => `${k}: ${v}`).join("\n")}</div>
        <div class="dcard-res">标签 ${(sess.tabs || []).length} 个：${(sess.tabs || []).map(t => t.id + " " + (t.url || "")).join(" · ") || "—"}</div>
        <div style="display:flex;gap:6px;margin-top:6px">
          <button class="sm" id="btn-br-tab">新标签</button>
          <button class="sm" id="btn-br-restart">重建会话</button>
        </div>
      </div>` : "";
    const recent = (b.recent || []).map((r) => `
      <div class="dcard">
        <div class="dcard-head"><span class="tag">${esc(r.tool.replace("mcp__browser__", ""))}</span>
          <span class="dim">${esc(r.kind)}</span></div>
        <div class="dcard-res">${esc(r.summary || "")}</div>
      </div>`).join("");
    let obs = null;
    try { obs = await api(`/api/harness/budget/${encodeURIComponent(S.task.id)}`); } catch {}
    const series = (obs && obs.series) || [];
    const spark = series.length > 1 ? sparkline(series, 260, 34) : "";
    const obsComp = obs && obs.compactions_observed ? obs.compactions_observed : null;
    const compTotal = obsComp ? obsComp.total : ((obs && obs.compactions) || 0);
    const compWhy = obsComp
      ? `已记录 ${compTotal} 次（micro ${obsComp.micro} · auto ${obsComp.auto}）`
      : `按回落推断 ${compTotal} 次`;
    const obsHTML = obs ? `
      <div class="dcard">
        <div class="dcard-head"><span class="tag">上下文观测</span>
          <span class="${compTotal > 0 ? "eval-pass" : "dim"}">压缩 ${compTotal} 次</span></div>
        ${spark}
        <div class="dcard-res">每轮输入 token ${series.join(" → ") || "—"}
预算 ${Math.round((obs.run_budget_seconds || 0) / 60)} 分钟 · 当前占用 ${Math.round(obs.pct || 0)}% · ${escapeHtmlLite(compWhy)}</div>
      </div>` : "";
    box.innerHTML = sessHTML + obsHTML + `
      <div class="dstat">
        <span>已配置 <b>${b.configured ? "是" : "否"}</b></span>
        <span>已启用 <b>${b.enabled ? "是" : "否"}</b></span>
        <span>工具 <b>${b.tools_cached ?? 0}</b></span>
      </div>
      <div class="dstat"><span>npx <b>${b.npx && b.npx.found ? "可用" : "缺失"}</b></span>
        <span>${esc(b.transport || "")}</span></div>
      <p class="${b.ready ? "dim xs" : "eval-fail xs"}">${esc(b.hint || "")}</p>
      <div class="dcard">
        <div class="dcard-head"><span class="tag">F12 · chrome-devtools</span>
          <span class="${b.devtools && b.devtools.ready ? "eval-pass" : "dim"}">${b.devtools && b.devtools.ready ? "就绪" : "未连接"}</span></div>
        <div class="dcard-res">${esc((b.devtools && (b.devtools.hint || (b.devtools.enabled ? "已启用 · 工具缓存 " + b.devtools.tools_cached : "未启用"))) || "未配置")}</div>
        ${b.f12_hint ? `<div class="dcard-res">${esc(b.f12_hint)}</div>` : ""}
      </div>
      ${(() => {
        const errs = b.console_errors || [];
        const unparsed = b.console_errors_unparsed || 0;
        const f12 = (b.recent || []).filter((r) => r.tool.startsWith("mcp__chrome-devtools__"));
        const head = `<div class="dcard-head"><span class="tag">F12 · console 报错</span>
          <span class="${errs.length ? "eval-fail" : "eval-pass"}">${errs.length ? errs.length + " 条" : "无"}</span></div>`;
        const tail = f12.length
          ? `<details style="margin-top:6px"><summary class="dim xs">最近 ${f12.length} 次 F12 动作</summary>
             <div class="dcard-res">${f12.map((r) => `${esc(r.tool.replace("mcp__chrome-devtools__",""))} · ${esc(r.kind)}\n${esc((r.summary || "").slice(0, 300))}`).join("\n\n")}</div></details>`
          : "";
        if (!errs.length) {
          const why = unparsed
            ? `<div class="dcard-res eval-fail">${unparsed} 条 console 读取结果无法解析——不当作「无报错」</div>`
            : `<div class="dcard-res dim">agent 还没读过 console（list_console_messages），或页面没有 warn/error。</div>`;
          return `<div class="dcard">${head}${why}${tail}</div>`;
        }
        const rows = errs.map((e) => `<div class="dcard-res"><span class="${e.level === "error" ? "eval-fail" : "dim"}">${esc(e.level)}</span>
          <span class="dim xs">${esc(e.at || "")} · ${esc(e.tool || "")}</span><br>${esc(e.text)}</div>`).join("");
        return `<div class="dcard">${head}${rows}${unparsed ? `<div class="dim xs">另有 ${unparsed} 条无法解析</div>` : ""}${tail}</div>`;
      })()}
      ${recent || `<p class="dim xs">本任务还没有浏览器动作。</p>`}
      <p class="dim xs">说明：截图以缩略图形式挂在事件流每条浏览器动作旁（点击看大图）；存储按哈希去重、24h TTL、上限 2000 张。</p>`;
    const tabBtn = $("#btn-br-tab");
    if (tabBtn) tabBtn.addEventListener("click", async () => {
      const url = prompt("新标签要打开的地址（可留空）", "http://127.0.0.1:8899/console/");
      if (url === null) return;
      try {
        await api("/api/harness/browser/tabs", { method: "POST", body: JSON.stringify({ task: S.task.id, action: "open", url }) });
        toast("已开新标签"); refreshDock();
      } catch (e) { toast("开标签失败：" + e.message, true); }
    });
    const restartBtn = $("#btn-br-restart");
    if (restartBtn) restartBtn.addEventListener("click", async () => {
      try {
        const r = await api("/api/harness/browser/restart", { method: "POST", body: JSON.stringify({ task: S.task.id }) });
        toast(`会话已重建（${r.elapsed_ms}ms）` + (r.error ? "：" + r.error : ""));
        refreshDock();
      } catch (e) { toast("重建失败：" + e.message, true); }
    });
  } catch (e) { box.innerHTML = `<p class="eval-fail">${esc(e.message)}</p>`; }
}

async function renderActPanel() {
  const box = $("#dock-body");
  if (!S.selected) {
    box.innerHTML = `<p class="dim xs">在事件流或 Plan 里点一条意图/节点，即可在这里纠偏 / 停止 / 编辑。</p>`;
    return;
  }
  const it = S.intents.get(String(S.selected)) || {};
  const p = (() => { try { return typeof it.payload === "string" ? JSON.parse(it.payload) : (it.payload || {}); } catch { return {}; } })();
  const summary = p.summary || "(无摘要)";
  box.innerHTML = `
    <div class="dock-intent">
      <div class="i-head"><span>节点 #${esc(S.selected)}</span><span>${esc(it.state || "—")}</span></div>
      <div>${esc(summary)}</div>
    </div>
    <label for="steer-msg">运行中纠偏（不中断当前轮）</label>
    <textarea id="steer-msg" rows="3" placeholder="例如：不要继续爆破 order_id，先证明『只校验登录态』"></textarea>
    <button id="btn-steer" class="primary sm">发送纠偏</button>
    <div class="dock-row" style="margin-top:4px">
      <button id="btn-kill" class="danger sm">停止该 work</button>
      <button id="btn-edit" class="sm">编辑意图</button>
    </div>
    <div id="edit-box" hidden style="gap:6px">
      <label for="edit-summary">摘要</label>
      <textarea id="edit-summary" rows="3"></textarea>
      <label for="edit-prio">优先级</label>
      <input id="edit-prio" type="number" step="1" style="width:100%" />
      <button id="btn-edit-save" class="primary sm">保存</button>
    </div>`;
  $("#edit-summary").value = summary;
  $("#edit-prio").value = it.priority ?? 0;
  $("#btn-steer").addEventListener("click", () => doSteer(S.selected));
  $("#btn-kill").addEventListener("click", () => doKill(S.selected));
  $("#btn-edit").addEventListener("click", () => {
    const b = $("#edit-box");
    b.hidden = !b.hidden;
    b.style.display = b.hidden ? "" : "grid";
  });
  $("#btn-edit-save").addEventListener("click", () => doEdit(S.selected));
}

async function selectIntent(id) {
  S.selected = String(id);
  if (!S.intents.has(S.selected)) await loadIntents(true);
  S.dockTab = "act";
  renderDockTabs();
  refreshDock();
  renderPlan();
}

async function doSteer(intentId) {
  const msg = ($("#steer-msg").value || "").trim();
  if (!msg) return toast("纠偏内容不能为空", true);
  try {
    await api(`/api/tasks/${encodeURIComponent(S.task.id)}/intents/${intentId}/steer`, { method: "POST", body: JSON.stringify({ message: msg }) });
    toast("纠偏已投递（下个工具调用前生效）");
  } catch (e) { toast("纠偏失败：" + e.message, true); }
}

async function doKill(intentId) {
  if (!confirm(`停止 #${intentId}？`)) return;
  try { await api(`/api/tasks/${encodeURIComponent(S.task.id)}/intents/${intentId}/kill`, { method: "POST" }); toast("已请求停止"); loadIntents(true); }
  catch (e) { toast("停止失败：" + e.message, true); }
}

async function doEdit(intentId) {
  const summary = ($("#edit-summary").value || "").trim();
  const prio = parseInt($("#edit-prio").value, 10);
  const patch = {};
  if (summary) patch.summary = summary;
  if (!Number.isNaN(prio)) patch.priority = prio;
  if (!Object.keys(patch).length) return toast("没有要修改的字段", true);
  try {
    await api(`/api/tasks/${encodeURIComponent(S.task.id)}/intents/${intentId}`, { method: "PATCH", body: JSON.stringify(patch) });
    toast("已修改"); loadIntents(true);
  } catch (e) { toast("修改失败：" + e.message, true); }
}

/* ── Plan ─────────────────────────────────────────────────────────────── */
async function loadIntents(quiet = false) {
  if (!S.task) return;
  try {
    const data = await api(`/api/exploration/intents?task=${encodeURIComponent(S.task.id)}&limit=500`);
    const list = Array.isArray(data) ? data : (data.items || []);
    S.intents.clear();
    for (const n of list) S.intents.set(String(n.id), n);
    if (!S.planDirty) rebuildPlanOrder();
    renderPlan();
  } catch (e) { if (!quiet) toast("加载意图失败：" + e.message, true); }
}

const isOpen = (n) => (n.type === "intent" || n.kind === "intent") && n.state === "open";

function rebuildPlanOrder() {
  const open = Array.from(S.intents.values()).filter(isOpen);
  open.sort((a, b) => (b.priority ?? 0) - (a.priority ?? 0) || Number(a.id) - Number(b.id));
  S.planOrder = open.map((n) => String(n.id));
}

function renderPlan() {
  const rows = S.planOrder.map((id, idx) => {
    const n = S.intents.get(id) || {};
    let p = {};
    try { p = typeof n.payload === "string" ? JSON.parse(n.payload) : (n.payload || {}); } catch {}
    return `<tr class="${S.selected === id ? "sel" : ""}" data-id="${esc(id)}">
      <td class="mono">${idx + 1}</td><td class="mono"></td>
      <td><span class="plan-sum" contenteditable="true" data-id="${esc(id)}">${esc(p.summary || "")}</span></td>
      <td><input class="plan-prio" type="number" value="${esc(n.priority ?? 0)}" data-id="${esc(id)}" /></td>
      <td>${esc(n.state || "")}</td>
      <td><div class="plan-ops">
        <button class="icon sm" data-up="${esc(id)}">↑</button>
        <button class="icon sm" data-down="${esc(id)}">↓</button>
        <button class="icon sm" data-steer="${esc(id)}">✎</button>
      </div></td></tr>`;
  });
  $("#plan-body").innerHTML = rows.join("") ||
    `<tr><td colspan="6" class="dim" style="padding:16px">没有 open 意图。HARNESS 任务不产生意图，这是正常的。</td></tr>`;
  $("#btn-order-save").disabled = !S.planDirty;
}

$("#plan-body").addEventListener("click", (ev) => {
  const t = ev.target;
  if (t.dataset.up) moveIntent(t.dataset.up, -1);
  else if (t.dataset.down) moveIntent(t.dataset.down, 1);
  else if (t.dataset.steer) selectIntent(t.dataset.steer);
  else { const row = t.closest("tr"); if (row) selectIntent(row.dataset.id); }
});

function moveIntent(id, delta) {
  const arr = S.planOrder, i = arr.indexOf(id), j = i + delta;
  if (i < 0 || j < 0 || j >= arr.length) return;
  [arr[i], arr[j]] = [arr[j], arr[i]];
  S.planDirty = true; renderPlan();
}

$("#plan-body").addEventListener("change", async (ev) => {
  const el = ev.target;
  if (!el.classList.contains("plan-prio")) return;
  const prio = parseInt(el.value, 10);
  if (Number.isNaN(prio)) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(S.task.id)}/intents/${el.dataset.id}`, { method: "PATCH", body: JSON.stringify({ priority: prio }) });
    toast("优先级已更新"); loadIntents(true);
  } catch (e) { toast("更新失败：" + e.message, true); }
});

$("#plan-body").addEventListener("focusout", async (ev) => {
  const el = ev.target;
  if (!el.classList || !el.classList.contains("plan-sum")) return;
  const summary = el.textContent.trim();
  if (!summary) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(S.task.id)}/intents/${el.dataset.id}`, { method: "PATCH", body: JSON.stringify({ summary }) });
    toast("摘要已更新"); loadIntents(true);
  } catch (e) { toast("更新失败：" + e.message, true); }
});

$("#btn-order-save").addEventListener("click", async () => {
  if (!S.planOrder.length) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(S.task.id)}/frontier/order`, { method: "POST", body: JSON.stringify({ ids: S.planOrder.map(Number) }) });
    S.planDirty = false; toast("顺序已保存"); loadIntents(true);
  } catch (e) { toast("保存失败：" + e.message, true); }
});
$("#btn-plan-refresh").addEventListener("click", () => loadIntents());

/* ── Replay ───────────────────────────────────────────────────────────── */
let lastReplay = null;
async function loadReplay() {
  if (!S.task) return toast("先选一个任务", true);
  const qs = new URLSearchParams({ task: S.task.id });
  const f = $("#replay-from").value.trim(), t = $("#replay-to").value.trim();
  if (f) qs.set("from", f);
  if (t) qs.set("to", t);
  try {
    const r = await api("/api/harness/replay?" + qs.toString());
    lastReplay = r;
    const run = r.run || {}, fp = r.fingerprint || {};
    $("#replay-meta").innerHTML =
      `<span>turns ${esc((run.turns || []).length)}</span><span>items ${esc(fp.items ?? 0)}</span>` +
      `<span>blocked ${esc((r.blocks || []).length)}</span><span>fingerprint ${esc((r.hash || "").slice(0, 16))}</span>` +
      (run.truncated ? '<span style="color:var(--warn)">⚠ 已截断</span>' : "");
    $("#replay-body").textContent = r.markdown || "(空)";
  } catch (e) { toast("回放失败：" + e.message, true); }
}
$("#btn-replay").addEventListener("click", loadReplay);
$("#btn-replay-md").addEventListener("click", async () => {
  if (!lastReplay) return toast("先重建一次回放", true);
  try { await navigator.clipboard.writeText(lastReplay.markdown || ""); toast("已复制"); }
  catch { toast("复制失败", true); }
});

/* ── Eval ─────────────────────────────────────────────────────────────── */
async function runConsistency() {
  const base = $("#eval-base").value.trim() || (S.task && S.task.id) || "";
  const cand = $("#eval-cand").value.trim();
  if (!base || !cand) return toast("需要 base 与 cand 两个 task id", true);
  const box = $("#eval-report");
  box.innerHTML = `<p class="dim xs">比对中…</p>`;
  try {
    const rep = await api("/api/harness/consistency", { method: "POST", body: JSON.stringify({ base_task: String(base), cand_task: String(cand) }) });
    const rows = (rep.checks || []).map((c) => `
      <tr><td class="${c.passed ? "eval-pass" : "eval-fail"}">${c.passed ? "PASS" : "FAIL"}</td>
        <td class="mono">${c.hard ? "HARD" : "soft"}</td><td class="mono">${esc(c.name)}</td>
        <td class="eval-detail">${esc(c.detail || "")}</td></tr>`).join("");
    box.innerHTML = `<div class="eval-verdict ${rep.passed ? "ok" : "bad"}">
        ${rep.passed ? "一致 ✅" : "不一致 ❌"} · ${esc(rep.base)} → ${esc(rep.cand)}</div>
      <table class="eval-table"><thead><tr><th style="width:64px">结果</th><th style="width:56px">级别</th>
        <th style="width:170px">检查项</th><th>说明</th></tr></thead><tbody>${rows}</tbody></table>`;
  } catch (e) { box.innerHTML = `<p class="eval-fail">比对失败：${esc(e.message)}</p>`; }
}
$("#btn-eval").addEventListener("click", runConsistency);

/* ── 平台 ─────────────────────────────────────────────────────────────── */
const SURFACE_LABEL = {
  "builtin": "内置", "custom-command": "自定义·命令", "custom-script": "自定义·脚本",
  "custom-http": "自定义·HTTP", "cli-wrapper": "CLI 声明", "mcp": "MCP", "skill": "技能",
};

async function loadPlatform() {
  loadCapabilities(); loadEvidence();
}

async function loadCapabilities() {
  try {
    const r = await api("/api/harness/capabilities");
    const caps = r.capabilities || [], sum = r.summary || {};
    $("#cap-summary").textContent = Object.keys(sum).sort()
      .map((k) => `${SURFACE_LABEL[k] || k} ${sum[k]}`).join(" · ");
    $("#cap-list").innerHTML = caps.map((c) => `
      <div class="cap-row">
        <span class="cap-surface">${esc(SURFACE_LABEL[c.surface] || c.surface)}</span>
        <span class="cap-name ${c.enabled ? "" : "off"}" title="${esc(c.detail || "")}">${esc(c.name)}</span>
        <span class="cap-tags">${c.exec ? '<span class="tag exec">可执行</span>' : ""}
          ${c.enabled ? "" : '<span class="tag">停用</span>'}
          ${(c.agents || []).length ? `<span class="tag">${esc(c.agents.join("/"))}</span>` : ""}</span>
      </div>`).join("") || `<p class="dim xs">没有能力。</p>`;
  } catch (e) { $("#cap-list").innerHTML = `<p class="eval-fail">${esc(e.message)}</p>`; }
}

async function loadEvidence() {
  const box = $("#evd-body");
  if (!S.task) { box.innerHTML = `<p class="dim xs">先选任务。</p>`; return; }
  try {
    const r = await api(`/api/harness/evidence-integrity?task=${encodeURIComponent(S.task.id)}`);
    $("#evd-summary").textContent = `${r.total} 条 · 有证据 ${r.with_evidence} · 无证据 ${r.without_evidence}`;
    box.innerHTML = `<table class="eval-table"><thead><tr><th style="width:64px">ID</th><th>名称</th>
        <th style="width:70px">等级</th><th style="width:52px">绑定</th></tr></thead><tbody>
      ${(r.rows || []).map((f) => `<tr><td class="mono">#${esc(f.finding_id)}</td><td>${esc(f.name)}</td>
        <td class="mono">${esc(f.severity || "-")}</td>
        <td class="mono ${f.bindings ? "eval-pass" : ""}">${esc(f.bindings)}</td></tr>`).join("")}
      </tbody></table><p class="dim xs" style="margin-top:8px">${esc(r.note || "")}</p>`;
  } catch (e) { box.innerHTML = `<p class="eval-fail">${esc(e.message)}</p>`; }
}
$("#btn-platform-refresh").addEventListener("click", loadPlatform);

/* ── tabs ─────────────────────────────────────────────────────────────── */
function switchTab(name) {
  $$(".tab").forEach((b) => b.classList.toggle("active", b.dataset.tab === name));
  ["run", "plan", "replay", "eval", "platform"].forEach((n) => {
    const el = $("#tab-" + n);
    if (el) el.hidden = n !== name;
  });
  if (name === "plan") loadIntents(true);
  if (name === "platform") loadPlatform();
  if (name === "replay") loadReplay();
}
$$(".tab").forEach((b) => b.addEventListener("click", () => switchTab(b.dataset.tab)));


/* ── 终端抽屉（P4）：在任务工作区执行命令；可选把结果交给 agent ────────── */
function toggleDrawer(force) {
  const d = $("#drawer");
  const show = force === undefined ? d.hidden : force;
  d.hidden = !show;
  if (show) $("#drawer-cmd").focus();
}
$("#btn-term").addEventListener("click", () => toggleDrawer());
$("#btn-drawer-close").addEventListener("click", () => toggleDrawer(false));
document.addEventListener("keydown", (ev) => {
  if (ev.ctrlKey && ev.key === "`") { ev.preventDefault(); toggleDrawer(); }
});

async function runDrawer() {
  if (!S.task) return toast("先选一个任务", true);
  const cmd = $("#drawer-cmd").value.trim();
  if (!cmd) return toast("命令不能为空", true);
  const out = $("#drawer-out");
  out.textContent = "$ " + cmd + "\n…运行中";
  try {
    const r = await api(`/api/tasks/${encodeURIComponent(S.task.id)}/exec`, {
      method: "POST",
      body: JSON.stringify({ command: cmd, inject: $("#drawer-inject").checked }),
    });
    $("#drawer-cwd").textContent = r.cwd || "";
    out.textContent = `$ ${cmd}\n${r.output || "(无输出)"}\n\n[退出码 ${r.exit_code} · ${r.duration_ms}ms${r.injected ? " · 已交给 agent" : ""}]`;
    if (r.injected) toast("已交给 agent：它会在同一会话里继续");
  } catch (e) {
    out.textContent = "$ " + cmd + "\n[eval-fail] " + e.message;
    toast("执行失败：" + e.message, true);
  }
}
$("#btn-drawer-run").addEventListener("click", runDrawer);
$("#drawer-cmd").addEventListener("keydown", (ev) => { if (ev.key === "Enter") { ev.preventDefault(); runDrawer(); } });

/* ── init ─────────────────────────────────────────────────────────────── */
renderDockTabs();
boot();

/* sparkline：把每轮输入 token 画成内联 SVG（无外部依赖、无实时通道）。
   compactions 由后端推断，这里用竖线标出"回落点"，与 budget 面板同源。 */
function sparkline(values, w, h) {
  const max = Math.max(...values, 1);
  const step = values.length > 1 ? w / (values.length - 1) : w;
  const pts = values.map((v, i) => `${(i * step).toFixed(1)},${(h - (v / max) * (h - 4) - 2).toFixed(1)}`);
  let drops = "";
  for (let i = 1; i < values.length; i++) {
    if (values[i] < values[i - 1] * 0.8) {
      drops += `<line x1="${(i * step).toFixed(1)}" y1="0" x2="${(i * step).toFixed(1)}" y2="${h}" stroke="var(--ok)" stroke-width="1" stroke-dasharray="2 2" />`;
    }
  }
  return `<svg width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" style="display:block;margin:4px 0">
    ${drops}<polyline fill="none" stroke="var(--accent)" stroke-width="1.5" points="${pts.join(" ")}" />
  </svg>`;
}

// escapeHtmlLite is esc() for text that is already composed locally (no markup).
function escapeHtmlLite(t) { return esc(t); }

/* ════════════════════════════════════════════════════════════════════════════
   控制台外壳：命令面板 · 骨架屏 · 无障碍
   ----------------------------------------------------------------------------
   这一层不碰业务逻辑，只补"专业工具默认该有"的几件事：
   - ⌘/Ctrl+K 命令面板：任务跳转、切换视图、切换右栏、刷新、终端抽屉。四栏界面
     里最贵的是"找到那件事"的成本，键盘一步到位比记忆按钮位置便宜。
   - 骨架屏：首次加载任务列表时给形状，而不是让人盯着空白判断"是不是坏了"。
   - 事件流 aria-live：新事件会被读屏播报，而不是只有看得见的人知道在跑。
   ══════════════════════════════════════════════════════════════════════════ */

function consoleActions() {
  const acts = [];
  const tabNames = { run: "Run", plan: "Plan", replay: "Replay", eval: "Eval", platform: "平台" };
  Object.entries(tabNames).forEach(([k, label]) => {
    acts.push({ key: "视图", label: "切换到 " + label, run: () => switchTab(k) });
  });
  const docks = { status: "状态", files: "文件", diff: "DIFF", budget: "预算", browser: "浏览器", act: "干预" };
  Object.entries(docks).forEach(([k, label]) => {
    acts.push({
      key: "右栏", label: "打开 " + label,
      run: () => { S.dockTab = k; if (typeof renderDockTabs === "function") renderDockTabs(); if (typeof refreshDock === "function") refreshDock(); },
    });
  });
  acts.push({ key: "动作", label: "刷新任务列表", run: () => loadTasks() });
  acts.push({ key: "动作", label: "打开 / 关闭终端抽屉", run: () => toggleDrawer() });
  (S.tasks || []).slice(0, 40).forEach((t) => {
    acts.push({
      key: "任务",
      label: (t.goal || t.description || t.id || "").slice(0, 70),
      hint: (t.status || "") + " · " + (t.id || ""),
      run: () => selectTask(t.id),
    });
  });
  return acts;
}

let cmdkState = null;

function closeCmdk() {
  const el = document.getElementById("cmdk");
  if (el) el.remove();
  cmdkState = null;
}

function openCmdk() {
  if (cmdkState) return closeCmdk();
  const wrap = document.createElement("div");
  wrap.id = "cmdk";
  wrap.className = "cmdk";
  wrap.setAttribute("role", "dialog");
  wrap.setAttribute("aria-modal", "true");
  wrap.setAttribute("aria-label", "命令面板");
  wrap.innerHTML = `
    <div class="cmdk-box">
      <input id="cmdk-input" class="cmdk-input" placeholder="搜索任务、视图、动作…（↑↓ 选择，Enter 执行，Esc 关闭）"
             autocomplete="off" spellcheck="false" />
      <ul id="cmdk-list" class="cmdk-list" role="listbox"></ul>
      <div class="cmdk-foot"><span>⌘/Ctrl+K 打开</span><span>Esc 关闭</span><span id="cmdk-count"></span></div>
    </div>`;
  document.body.appendChild(wrap);

  const input = wrap.querySelector("#cmdk-input");
  const list = wrap.querySelector("#cmdk-list");
  const all = consoleActions();
  let view = all.slice(0, 12);
  let sel = 0;

  function paint() {
    list.innerHTML = view.map((a, i) => `
      <li class="cmdk-item" role="option" data-i="${i}" aria-selected="${i === sel}">
        <span class="k">${esc(a.key)}</span>
        <span>${esc(a.label)}</span>
        ${a.hint ? `<span class="hint">${esc(a.hint)}</span>` : ""}
      </li>`).join("") || `<li class="cmdk-item"><span>没有匹配项</span></li>`;
    const c = document.getElementById("cmdk-count");
    if (c) c.textContent = view.length + " 项";
  }

  function filter(q) {
    const needle = q.trim().toLowerCase();
    view = (needle
      ? all.filter((a) => (a.label + " " + a.key + " " + (a.hint || "")).toLowerCase().includes(needle))
      : all).slice(0, 40);
    sel = 0;
    paint();
  }

  function choose(i) {
    const a = view[i];
    if (!a) return;
    closeCmdk();
    try { a.run(); } catch (e) { toast(e.message || String(e), true); }
  }

  input.addEventListener("input", () => filter(input.value));
  input.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") { ev.preventDefault(); closeCmdk(); }
    else if (ev.key === "ArrowDown") { ev.preventDefault(); sel = Math.min(sel + 1, view.length - 1); paint(); }
    else if (ev.key === "ArrowUp") { ev.preventDefault(); sel = Math.max(sel - 1, 0); paint(); }
    else if (ev.key === "Enter") { ev.preventDefault(); choose(sel); }
  });
  wrap.addEventListener("click", (ev) => {
    if (ev.target === wrap) return closeCmdk();
    const li = ev.target.closest(".cmdk-item");
    if (li && li.dataset.i !== undefined) choose(Number(li.dataset.i));
  });

  paint();
  input.focus();
  cmdkState = { filter };
}

document.addEventListener("keydown", (ev) => {
  const mod = ev.metaKey || ev.ctrlKey;
  if (mod && (ev.key === "k" || ev.key === "K" || ev.key === "p" || ev.key === "P")) {
    ev.preventDefault();
    openCmdk();
  } else if (ev.key === "Escape" && cmdkState) {
    ev.preventDefault();
    closeCmdk();
  }
});

/* 事件流的读屏播报：新条目会被念出来，而不是只有盯屏幕的人知道在跑 */
(function ariaLiveStream() {
  const stream = document.getElementById("stream");
  if (stream && !stream.getAttribute("aria-live")) {
    stream.setAttribute("aria-live", "polite");
    stream.setAttribute("aria-relevant", "additions");
  }
})();

/* 骨架屏：只在首次加载时出现，刷新时不闪 */
(function skeletonOnFirstLoad() {
  const list = document.getElementById("task-list");
  if (!list) return;
  if (!Array.isArray(S.tasks) || S.tasks.length === 0) {
    list.innerHTML = Array.from({ length: 5 }, () => `<li class="skel-row"></li>`).join("");
  }
})();

/* 窄屏：左栏与右栏变成抽屉，用浮动按钮开关 */
(function narrowSwitchers() {
  if (document.getElementById("btn-dock-toggle")) return;
  const bar = document.querySelector(".topbar-actions");
  if (!bar) return;
  const mk = (id, label, title, target, cls) => {
    const b = document.createElement("button");
    b.id = id; b.className = "sm " + cls; b.textContent = label; b.title = title;
    b.setAttribute("aria-expanded", "false");
    b.addEventListener("click", () => {
      const el = document.querySelector(target);
      if (!el) return;
      const open = el.classList.toggle("open");
      b.setAttribute("aria-expanded", String(open));
    });
    bar.appendChild(b);
  };
  mk("btn-dock-toggle", "面板", "切换右侧面板（Esc 关闭）", ".dock", "dock-toggle");
  mk("btn-rail-toggle", "任务", "切换任务栏（窄屏）", ".rail", "rail-toggle");

  // 右栏默认收起，状态记住：多数时间它是空状态，常驻只会挤压事件流。
  const KEY = "artex.harness.dock";
  const dock = document.querySelector(".dock");
  if (dock && getStored(KEY) === "open") dock.classList.add("open");
  document.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape" && dock && dock.classList.contains("open") && !cmdkState) {
      dock.classList.remove("open");
      store(KEY, "closed");
    }
  });
  if (dock) {
    new MutationObserver(() => store(KEY, dock.classList.contains("open") ? "open" : "closed"))
      .observe(dock, { attributes: true, attributeFilter: ["class"] });
  }
})();

/* ── 状态栏：把"连没连上、有多少任务、现在是哪个"放到常驻位置 ─────────────
 * 顶栏的三个 pill 会随视图滚动离开，底部状态栏不会。它是产品"始终知道自己
 * 处于什么状态"的那一行，也是快捷键的唯一发现入口。 */
function updateStatusbar() {
  const conn = document.getElementById("sb-conn");
  const connText = document.getElementById("sb-conn-text");
  if (conn && connText) {
    const live = S.es && S.es.readyState === 1;
    conn.className = "sb-dot " + (live ? "on" : "off");
    connText.textContent = live ? "实时联通" : (S.es ? "重连中" : "未连接");
  }
  const t = document.getElementById("sb-task");
  if (t) t.textContent = S.task ? `#${S.task.id} · ${S.task.status || "—"}` : "无任务";
  const n = document.getElementById("sb-tasks");
  if (n) n.textContent = `${(S.tasks || []).length} 个任务`;
}
setInterval(updateStatusbar, 1500);
updateStatusbar();

/* 状态栏跟随渲染即时更新：定时器只保证"心跳"，首帧不该显示"未连接/0 个任务"。
 * 监听任务列表与状态 pill 的 DOM 变化，比把 updateStatusbar() 塞进每个渲染函数
 * 更可靠——新增渲染路径不会漏掉它。 */
(function statusbarFollowScene() {
  ["task-list", "pill-status", "pill-rev"].forEach((id) => {
    const el = document.getElementById(id);
    if (!el) return;
    new MutationObserver(() => updateStatusbar()).observe(el, {
      childList: true, subtree: true, characterData: true,
    });
  });
})();

/* 轻量本地偏好读写：失败（隐私模式/配额）不抛错，只是不记忆 */
function getStored(key) { try { return localStorage.getItem(key); } catch { return null; } }
function store(key, val) { try { val === null ? localStorage.removeItem(key) : localStorage.setItem(key, val); } catch {} }
