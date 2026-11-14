/* ============================================================================
   artex-harness console — zero-build front end.

   Design rationale: docs/design/DESIGN.md. In one line — the harness is an
   instrument, so the UI exposes *the loop itself*: turns with their budgets and
   terminal reasons, tool calls with their real output, blocks distinguished from
   failures, interrupt separated from steer, and an inspector that answers
   "what is this run actually doing / what can it do / what did it prove".
   ========================================================================== */
"use strict";

const $  = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => Array.from(r.querySelectorAll(s));
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const num = (n) => (n === null || n === undefined || Number.isNaN(n)) ? "—" : Number(n).toLocaleString("en-US");
const tok = (n) => n >= 1e6 ? (n / 1e6).toFixed(1) + "M" : n >= 1000 ? (n / 1000).toFixed(1) + "k" : String(n ?? 0);

const BLOCK_MARK = "【ARTEX 平台管控·非目标防御】";
const TOKEN_KEY = "artex.harness.token";

const state = {
  tasks: [], task: null,
  es: null, seen: new Set(),
  turns: new Map(),          // rev -> { meta, items[], el, bodyEl }
  toolCards: new Map(),      // tool_use_id -> element
  intents: new Map(),        // id -> dto
  planOrder: [], planDirty: false,
  selectedIntent: null,
  caps: null, evidence: null, judge: null,
  tab: "trace", insp: "context",
  pinnedBottom: true, expanded: new Set(),
};

/* ── auth ────────────────────────────────────────────────────────────────── */
function getToken() { try { return localStorage.getItem(TOKEN_KEY) || ""; } catch { return ""; } }
function setToken(t) {
  try { t ? localStorage.setItem(TOKEN_KEY, t) : localStorage.removeItem(TOKEN_KEY); } catch {}
  document.cookie = "artex_token=" + encodeURIComponent(t || "") + "; path=/; SameSite=Strict";
}

async function api(path, opts = {}) {
  const headers = Object.assign({}, opts.headers);
  if (opts.body) headers["Content-Type"] = "application/json";
  const t = getToken();
  if (t) headers["Authorization"] = "Bearer " + t;
  const res = await fetch(path, { credentials: "same-origin", ...opts, headers });
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = text; }
  if (!res.ok) {
    if (res.status === 401 && !String(path).startsWith("/api/auth/")) { setToken(""); showGate(); }
    const msg = (data && (data.error || data.message)) || res.statusText || ("HTTP " + res.status);
    const e = new Error(msg); e.status = res.status; throw e;
  }
  return data;
}

let toastTimer = null;
function toast(msg, bad) {
  const el = $("#toast");
  el.textContent = msg;
  el.className = "toast on" + (bad ? " bad" : "");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.className = "toast" + (bad ? " bad" : ""); }, 3400);
}

function showGate() { $("#gate").classList.remove("hidden"); $("#shell").classList.add("hidden"); $("#gate-pass").focus(); }

async function boot() {
  applyTheme();
  let st = null;
  try { st = await api("/api/auth/status"); } catch {}
  if (!st || !st.initialized) {
    $("#gate-hint").textContent = "首次使用：设置管理密码（用户名固定 ARTEX）";
    return showGate();
  }
  if (!getToken()) return showGate();
  try { await api("/api/tasks"); } catch { setToken(""); return showGate(); }
  enter();
}

$("#gate-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const err = $("#gate-err"); err.hidden = true;
  try {
    const st = await api("/api/auth/status");
    if (!st.initialized) await api("/api/auth/init", { method: "POST", body: JSON.stringify({ password: $("#gate-pass").value }) });
    const r = await api("/api/auth/login", { method: "POST", body: JSON.stringify({ username: "ARTEX", password: $("#gate-pass").value }) });
    if (r && r.token) setToken(r.token);
    $("#gate").classList.add("hidden");
    enter();
  } catch (e) { err.textContent = e.message; err.hidden = false; }
});

function enter() {
  $("#shell").classList.remove("hidden");
  loadTasks();
  setInterval(() => { if (!document.hidden) loadTasks(true); }, 15000);
}

/* ── theme ───────────────────────────────────────────────────────────────── */
function applyTheme() {
  let t = "light";
  try { t = localStorage.getItem("artex.harness.theme") || "light"; } catch {}
  document.documentElement.dataset.theme = t === "dark" ? "dark" : "light";
}
$("#btn-theme").addEventListener("click", () => {
  const next = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
  document.documentElement.dataset.theme = next;
  try { localStorage.setItem("artex.harness.theme", next); } catch {}
});

/* ── sessions ────────────────────────────────────────────────────────────── */
function statusOf(t) {
  if (t.paused) return "paused";
  if (t.queued) return "queued";
  if (t.status === "done") return "done";
  if (t.status === "failed" || t.status === "timeout") return "bad";
  return t.status === "running" ? "running" : (t.status || "created");
}

async function loadTasks(quiet) {
  try {
    const data = await api("/api/tasks");
    state.tasks = Array.isArray(data) ? data : (data.tasks || data.items || []);
    renderSessions();
    if (!state.task && state.tasks.length) {
      const running = state.tasks.find((t) => statusOf(t) === "running");
      selectTask((running || state.tasks[0]).id);
    }
  } catch (e) { if (!quiet) toast("加载任务失败：" + e.message, true); }
}

function renderSessions() {
  const groups = [["running", "进行中"], ["paused", "已暂停"], ["done", "已完成"], ["bad", "失败"]];
  const by = {};
  for (const t of state.tasks) (by[statusOf(t)] ||= []).push(t);
  let html = "";
  let total = 0;
  for (const [key, label] of groups) {
    const list = by[key] || [];
    if (!list.length) continue;
    total += list.length;
    html += `<li class="rail-label" style="padding-left:10px">${label} · ${list.length}</li>`;
    html += list.map((t) => sessionHTML(t, key)).join("");
  }
  // anything with an unlisted status still has to show up
  const known = new Set(groups.map((g) => g[0]));
  const rest = state.tasks.filter((t) => !known.has(statusOf(t)));
  if (rest.length) html += rest.map((t) => sessionHTML(t, statusOf(t))).join("");
  $("#session-list").innerHTML = html || `<li class="note" style="padding:10px 12px">还没有任务。</li>`;
  $("#rail-label").textContent = total ? `任务 · ${state.tasks.length}` : "任务";
}

function sessionHTML(t, st) {
  const active = state.task && String(state.task.id) === String(t.id);
  const goal = t.goal || t.description || t.name || ("task " + t.id);
  const meta = [];
  meta.push(`#${esc(t.id)}`);
  if (t.in_flight) meta.push(`${t.in_flight} running`);
  if (t.goals_total) meta.push(`${t.goals_met || 0}/${t.goals_total}`);
  const f = t.findings || {};
  const hi = (f.critical || 0) + (f.high || 0);
  if (hi) meta.push(`${hi} high`);
  return `<li class="session${active ? " active" : ""}" data-id="${esc(t.id)}">
    <span class="s-title">${esc(goal)}</span>
    <span class="s-meta"><i class="dot ${st}"></i>${esc(st)} · ${meta.join(" · ")}</span>
  </li>`;
}

$("#session-list").addEventListener("click", (ev) => {
  const li = ev.target.closest(".session");
  if (li) selectTask(li.dataset.id);
});
$("#btn-refresh").addEventListener("click", () => loadTasks());
$("#btn-new").addEventListener("click", async () => {
  const goal = prompt("新任务目标（一句话，越具体越好）");
  if (!goal) return;
  try {
    const t = await api("/api/tasks", { method: "POST", body: JSON.stringify({ goal, description: "" }) });
    toast("任务已创建");
    await loadTasks(true);
    if (t && t.id) selectTask(t.id);
  } catch (e) { toast("创建失败：" + e.message, true); }
});
$("#btn-pause-all").addEventListener("click", async () => {
  if (!confirm("暂停所有正在运行的任务？")) return;
  try {
    const r = await api("/api/tasks/pause-all", { method: "POST" });
    toast(`已暂停 ${r.paused} 个任务`);
    loadTasks(true);
  } catch (e) { toast("急停失败：" + e.message, true); }
});

async function selectTask(id) {
  const t = state.tasks.find((x) => String(x.id) === String(id));
  if (!t) return;
  if (state.task && String(state.task.id) === String(id)) return;
  state.task = t;
  state.seen.clear(); state.turns.clear(); state.toolCards.clear();
  state.intents.clear(); state.planOrder = []; state.planDirty = false;
  state.selectedIntent = null; state.expanded.clear();
  state.caps = state.evidence = null;
  $("#transcript").innerHTML = `<div class="transcript-inner" id="trace-inner"></div>`;
  renderSessions(); renderHeader(); renderComposerTarget();
  connectStream(t.id);
  loadIntents();
  loadEvidence();
  loadJudge();
  renderInspector();
}

function renderHeader() {
  const t = state.task;
  if (!t) return;
  $("#task-title").textContent = t.goal || t.description || ("task " + t.id);
  const st = statusOf(t);
  const cls = st === "running" ? "ok" : st === "paused" ? "warn" : st === "bad" ? "bad" : "";
  const parts = [
    `<span class="chip ${cls}"><i class="dot ${st}"></i>${esc(st)}</span>`,
    `<span class="chip">#${esc(t.id)}</span>`,
    `<span class="chip" id="chip-rev">rev —</span>`,
  ];
  if (t.goals_total) parts.push(`<span class="chip">目标 ${t.goals_met || 0}/${t.goals_total}</span>`);
  if (t.in_flight) parts.push(`<span class="chip accent">${t.in_flight} worker 运行中</span>`);
  if (t.tokens && (t.tokens.total || t.tokens.input)) parts.push(`<span class="chip">${tok(t.tokens.total || t.tokens.input || 0)} tok</span>`);
  $("#task-sub").innerHTML = parts.join("");
  $("#ctl-workers").textContent = t.in_flight ?? "0";
  $("#ctl-tokens").textContent = t.tokens ? tok(t.tokens.total || t.tokens.input || 0) : "—";
}

/* ── stream ──────────────────────────────────────────────────────────────── */
function connectStream(taskId) {
  if (state.es) { state.es.close(); state.es = null; }
  const es = new EventSource(`/api/harness/stream?task=${encodeURIComponent(taskId)}`);
  state.es = es;
  const onEnv = (ev) => { try { renderEnvelope(JSON.parse(ev.data)); } catch {} };
  es.addEventListener("harness", onEnv);
  es.onmessage = onEnv;
  es.onerror = () => { /* browser auto-reconnects from Last-Event-ID */ };
}

function turnKey(env) { return env.rev > 0 ? env.rev : "legacy"; }

function ensureTurn(env) {
  const key = turnKey(env);
  let t = state.turns.get(key);
  if (t) return t;
  const kind = (env.turn && env.turn.kind) || "";
  const inner = $("#trace-inner");
  if (!inner) return null;
  const el = document.createElement("div");
  el.className = "turn " + (kind === "planner" ? "planner" : kind === "system" ? "system" : "");
  el.innerHTML = `
    <div class="turn-head">
      <span class="rev">rev ${esc(env.rev ?? 0)}</span>
      <span class="kind">${esc(kind || (env.rev > 0 ? "event" : "legacy"))}</span>
      <span class="sum">${esc((env.turn && (env.turn.summary || env.turn.ref)) || "")}</span>
      <span class="stat"></span>
    </div>
    <div class="turn-body"></div>`;
  inner.appendChild(el);
  t = { meta: env.turn || {}, items: [], el, bodyEl: el.querySelector(".turn-body") };
  state.turns.set(key, t);
  return t;
}

function renderEnvelope(env) {
  const it = env.item || {};
  const aid = it.activity_id || 0;
  if (aid && state.seen.has(aid)) return;
  if (aid) state.seen.add(aid);

  const turn = ensureTurn(env);
  if (!turn) return;
  turn.items.push(it);

  // A tool_result is the second half of an existing card: attach it there instead
  // of rendering a duplicate card (which would also double the visible output).
  const paired = it.kind === "tool_result" && it.tool_use_id && state.toolCards.get(it.tool_use_id);
  if (paired) {
    attachResult(paired, it);
  } else {
    const el = itemEl(it, env);
    if (el) {
      turn.bodyEl.appendChild(el);
      if (it.tool_use_id && (it.kind === "tool_use" || it.kind === "tool_result")) {
        state.toolCards.set(it.tool_use_id, el);
      }
      if (it.kind === "tool_result") attachResult(el, it);
    }
  }

  // turn stats: items · errors · tokens
  const errs = turn.items.filter((x) => x.is_error).length;
  const toks = turn.items.reduce((n, x) => n + (x.usage ? (x.usage.input_tokens || 0) + (x.usage.output_tokens || 0) : 0), 0);
  const stat = [];
  stat.push(`${turn.items.length} 步`);
  if (errs) stat.push(`<span style="color:var(--danger)">${errs} 错误</span>`);
  if (toks) stat.push(`${tok(toks)} tok`);
  turn.el.querySelector(".stat").innerHTML = stat.join(" · ");
  if (env.rev > 0) { const c = $("#chip-rev"); if (c) c.textContent = "rev " + env.rev; }

  if (state.pinnedBottom) {
    const box = $("#transcript");
    box.scrollTop = box.scrollHeight;
  }
}

$("#transcript").addEventListener("scroll", () => {
  const b = $("#transcript");
  state.pinnedBottom = b.scrollHeight - b.scrollTop - b.clientHeight < 60;
});

function isBlocked(it) {
  const txt = (it.summary || "") + (it.detail || "");
  return it.is_error && txt.includes(BLOCK_MARK);
}

function itemEl(it, env) {
  const kind = it.kind || "text";

  if (kind === "thinking") {
    const d = document.createElement("div");
    d.className = "card msg";
    d.innerHTML = `<div class="msg-text muted" style="font-size:12.5px;font-style:italic">${esc(it.summary || it.detail || "")}</div>`;
    return d;
  }

  if (kind === "text" || kind === "result") {
    const d = document.createElement("div");
    d.className = "card msg";
    d.innerHTML = `<div class="msg-text${it.is_error ? " err" : ""}">${esc(it.summary || it.detail || "")}</div>`;
    return d;
  }

  if (kind === "lifecycle") {
    const d = document.createElement("div");
    d.className = "card msg";
    d.innerHTML = `<div class="tiny mono muted">— ${esc(it.summary || it.detail || "")}</div>`;
    return d;
  }

  if (kind === "round") return null; // the turn header already says it

  if (kind === "tool_use" || kind === "tool_result" || kind === "intercept") {
    const blocked = isBlocked(it);
    const d = document.createElement("div");
    d.className = "card tool" + (blocked ? " blocked" : it.is_error ? " err" : "");
    const name = it.tool || (kind === "intercept" ? "审批" : "tool");
    const args = it.summary || "";
    d.innerHTML = `
      <div class="tool-line">
        <span class="tool-name">${esc(name)}</span>
        <span class="tool-args" title="${esc(args)}">${esc(args)}</span>
        <span class="status"></span>
      </div>
      <div class="card-extra"></div>`;
    if (it.node_id) {
      const link = document.createElement("span");
      link.className = "intent-link";
      link.textContent = "#" + it.node_id;
      link.addEventListener("click", (e) => { e.stopPropagation(); selectIntent(it.node_id); });
      d.querySelector(".tool-line").appendChild(link);
    }
    d.querySelector(".status").innerHTML = `<span class="chip">运行中</span>`;
    return d;
  }

  const d = document.createElement("div");
  d.className = "card msg";
  d.innerHTML = `<div class="msg-text">${esc(it.summary || it.detail || "")}</div>`;
  return d;
}

function attachResult(card, it) {
  if (!card) return;
  const blocked = isBlocked(it);
  const status = card.querySelector(".status");
  if (status) {
    status.innerHTML = blocked
      ? `<span class="chip warn">平台拦截</span>`
      : it.is_error ? `<span class="chip bad">失败</span>` : `<span class="chip ok">完成</span>`;
  }
  card.classList.toggle("blocked", blocked);
  card.classList.toggle("err", !blocked && !!it.is_error);

  const extra = card.querySelector(".card-extra");
  if (!extra) return;
  const text = it.detail || it.summary || "(no output)";
  if (blocked) {
    // The exact wording matters: it is the evidence compared by the consistency
    // gate, so it is shown verbatim rather than paraphrased.
    extra.innerHTML = `<div class="block-note"><b>平台管控</b><span>${esc(text.replace(BLOCK_MARK, "").trim())}</span></div>`;
    return;
  }
  const id = "out-" + (it.activity_id || Math.random().toString(36).slice(2));
  const long = text.length > 220 || text.split("\n").length > 4;
  const open = state.expanded.has(id);
  extra.innerHTML = `
    <div class="out ${long && !open ? "clip" : ""}" id="${id}">${esc(text)}</div>
    ${long ? `<div class="out-toggle" data-out="${id}">${open ? "收起" : "展开全部"}</div>` : ""}`;
  const toggle = extra.querySelector(".out-toggle");
  if (toggle) toggle.addEventListener("click", () => {
    const open2 = !state.expanded.has(id);
    open2 ? state.expanded.add(id) : state.expanded.delete(id);
    const box = extra.querySelector(".out");
    box.classList.toggle("clip", !open2);
    toggle.textContent = open2 ? "收起" : "展开全部";
  });
}

/* ── composer ────────────────────────────────────────────────────────────── */
function runningIntents() {
  return Array.from(state.intents.values()).filter((n) => n.state === "running");
}

function renderComposerTarget() {
  const box = $("#composer-target");
  const sel = state.selectedIntent;
  const running = runningIntents();
  let target = null;
  if (sel && state.intents.has(String(sel))) target = state.intents.get(String(sel));
  else if (running.length) target = running[0];

  if (!target) {
    box.innerHTML = `纠偏目标：<span class="muted">暂无运行中的 work</span>`;
    $("#btn-steer").disabled = true;
    $("#btn-interrupt").disabled = true;
    return;
  }
  state.selectedIntent = target.id;
  const p = parsePayload(target.payload);
  box.innerHTML = `纠偏目标：<span class="chip accent">#${esc(target.id)}</span>
    <span class="muted" style="max-width:44ch;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(p.summary || "（无摘要）")}</span>`;
  $("#btn-steer").disabled = false;
  $("#btn-interrupt").disabled = target.state !== "running";
}

function parsePayload(raw) {
  if (!raw) return {};
  if (typeof raw === "object") return raw;
  try { return JSON.parse(raw); } catch { return {}; }
}

async function doSteer() {
  const msg = ($("#composer-input").value || "").trim();
  const id = state.selectedIntent;
  if (!id) return toast("先选一条运行中的意图（在轨迹或计划里点 #id）", true);
  if (!msg) return toast("纠偏内容不能为空", true);
  try {
    await api(`/api/tasks/${encodeURIComponent(state.task.id)}/intents/${id}/steer`, {
      method: "POST", body: JSON.stringify({ message: msg }),
    });
    $("#composer-input").value = ""; autoGrow();
    toast("纠偏已投递：当前动作结束后生效");
  } catch (e) { toast("纠偏失败：" + e.message, true); }
}

async function doInterrupt() {
  const id = state.selectedIntent;
  if (!id) return toast("没有可中断的 work", true);
  if (!confirm(`立即中断意图 #${id} 的运行中 work？`)) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(state.task.id)}/intents/${id}/kill`, { method: "POST" });
    toast("已中断");
    loadIntents();
  } catch (e) { toast("中断失败：" + e.message, true); }
}

function autoGrow() {
  const el = $("#composer-input");
  el.style.height = "auto";
  el.style.height = Math.min(el.scrollHeight, 180) + "px";
}
$("#composer-input").addEventListener("input", autoGrow);
$("#composer-input").addEventListener("keydown", (ev) => {
  if ((ev.metaKey || ev.ctrlKey) && ev.key === "Enter") { ev.preventDefault(); doSteer(); }
});
$("#btn-steer").addEventListener("click", doSteer);
$("#btn-interrupt").addEventListener("click", doInterrupt);

/* ── stage actions / tabs ────────────────────────────────────────────────── */
$$("[data-act]").forEach((b) => b.addEventListener("click", async () => {
  const act = b.dataset.act;
  if (act === "replan") {
    try { await api(`/api/tasks/${encodeURIComponent(state.task.id)}/replan`, { method: "POST" }); toast("已请求立即重规划"); }
    catch (e) { toast("失败：" + e.message, true); }
    return;
  }
  try {
    await api(`/api/tasks/${encodeURIComponent(state.task.id)}/control`, { method: "POST", body: JSON.stringify({ action: act }) });
    toast(act === "pause" ? "任务已暂停" : "任务已恢复");
    loadTasks(true);
  } catch (e) { toast("失败：" + e.message, true); }
}));

$$("#tabs .tab").forEach((b) => b.addEventListener("click", () => {
  state.tab = b.dataset.tab;
  $$("#tabs .tab").forEach((x) => x.classList.toggle("active", x === b));
  ["trace", "plan", "replay", "eval"].forEach((n) => {
    const p = $("#pane-" + n); if (p) p.hidden = n !== state.tab;
  });
  if (state.tab === "plan") loadIntents();
  if (state.tab === "replay") renderReplay();
  if (state.tab === "eval") { if (!state.evalDone) renderEvalIdle(); }
}));

/* ── plan ────────────────────────────────────────────────────────────────── */
async function loadIntents(quiet) {
  if (!state.task) return;
  try {
    const data = await api(`/api/exploration/intents?task=${encodeURIComponent(state.task.id)}&limit=500`);
    const list = Array.isArray(data) ? data : (data.items || []);
    state.intents.clear();
    for (const n of list) state.intents.set(String(n.id), n);
    if (!state.planDirty) rebuildOrder();
    renderPlan(); renderComposerTarget();
    if (state.insp === "context") renderInspector();
    $("#tab-plan-count").textContent = state.planOrder.length ? String(state.planOrder.length) : "";
  } catch (e) { if (!quiet) toast("加载意图失败：" + e.message, true); }
}

function isOpen(n) { return (n.type === "intent" || n.kind === "intent") && n.state === "open"; }

function rebuildOrder() {
  const open = Array.from(state.intents.values()).filter(isOpen);
  open.sort((a, b) => (b.priority ?? 0) - (a.priority ?? 0) || Number(a.id) - Number(b.id));
  state.planOrder = open.map((n) => String(n.id));
}

function renderPlan() {
  const body = $("#plan-body");
  if (!body) return;
  body.innerHTML = state.planOrder.map((id, i) => {
    const n = state.intents.get(id) || {};
    const p = parsePayload(n.payload);
    return `<tr class="${String(state.selectedIntent) === id ? "sel" : ""}" data-id="${esc(id)}">
      <td class="num muted">${i + 1}</td>
      <td><span class="sum-edit" contenteditable="true" data-id="${esc(id)}">${esc(p.summary || "")}</span></td>
      <td><input class="pri" type="number" value="${esc(n.priority ?? 0)}" data-prio="${esc(id)}" /></td>
      <td class="mono tiny">${esc(n.state || "")}</td>
      <td><div class="ops">
        <button class="btn sm ghost" data-up="${esc(id)}" title="上移">↑</button>
        <button class="btn sm ghost" data-down="${esc(id)}" title="下移">↓</button>
        <button class="btn sm ghost" data-pick="${esc(id)}" title="设为纠偏目标">◎</button>
      </div></td>
    </tr>`;
  }).join("") || `<tr><td colspan="5" class="muted" style="padding:20px">frontier 为空：没有 open 状态的意图。</td></tr>`;
  $("#btn-order-save").disabled = !state.planDirty;
}

$("#plan-body").addEventListener("click", (ev) => {
  const t = ev.target.closest("[data-up],[data-down],[data-pick]");
  if (!t) {
    const tr = ev.target.closest("tr[data-id]");
    if (tr && !ev.target.isContentEditable) selectIntent(Number(tr.dataset.id));
    return;
  }
  if (t.dataset.up) return move(t.dataset.up, -1);
  if (t.dataset.down) return move(t.dataset.down, 1);
  if (t.dataset.pick) { selectIntent(Number(t.dataset.pick)); toast("已设为纠偏目标"); }
});

$("#plan-body").addEventListener("change", async (ev) => {
  const el = ev.target;
  if (!el.dataset.prio) return;
  const v = parseInt(el.value, 10);
  if (Number.isNaN(v)) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(state.task.id)}/intents/${el.dataset.prio}`, {
      method: "PATCH", body: JSON.stringify({ priority: v }),
    });
    toast("优先级已更新"); loadIntents(true);
  } catch (e) { toast("更新失败：" + e.message, true); }
});

$("#plan-body").addEventListener("focusout", async (ev) => {
  const el = ev.target;
  if (!el.classList || !el.classList.contains("sum-edit")) return;
  const id = el.dataset.id;
  const summary = el.textContent.trim();
  const cur = parsePayload((state.intents.get(id) || {}).payload).summary || "";
  if (!summary || summary === cur) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(state.task.id)}/intents/${id}`, {
      method: "PATCH", body: JSON.stringify({ summary }),
    });
    toast("意图已修改"); loadIntents(true);
  } catch (e) { toast("修改失败：" + e.message, true); }
});

function move(id, d) {
  const i = state.planOrder.indexOf(id), j = i + d;
  if (i < 0 || j < 0 || j >= state.planOrder.length) return;
  [state.planOrder[i], state.planOrder[j]] = [state.planOrder[j], state.planOrder[i]];
  state.planDirty = true; renderPlan();
}

$("#btn-plan-refresh").addEventListener("click", () => loadIntents());
$("#btn-order-save").addEventListener("click", async () => {
  try {
    await api(`/api/tasks/${encodeURIComponent(state.task.id)}/frontier/order`, {
      method: "POST", body: JSON.stringify({ ids: state.planOrder.map(Number) }),
    });
    state.planDirty = false; toast("顺序已保存"); loadIntents(true);
  } catch (e) { toast("保存失败：" + e.message, true); }
});

function selectIntent(id) {
  state.selectedIntent = id;
  renderComposerTarget(); renderPlan();
}

/* ── replay ──────────────────────────────────────────────────────────────── */
let replayCache = null;
async function renderReplay() {
  if (!state.task) return;
  const qs = new URLSearchParams({ task: state.task.id });
  const f = $("#replay-from").value.trim(), t = $("#replay-to").value.trim();
  if (f) qs.set("from", f);
  if (t) qs.set("to", t);
  try {
    const r = await api("/api/harness/replay?" + qs.toString());
    replayCache = r;
    const run = r.run || {}, fp = r.fingerprint || {};
    $("#replay-meta").innerHTML = [
      `<span>rev ${esc(run.from ?? 0)}..${esc(run.to ?? 0)}</span>`,
      `<span>${esc((run.turns || []).length)} 轮</span>`,
      `<span>${esc(fp.items ?? 0)} 步</span>`,
      `<span>拦截 ${esc((r.blocks || []).length)}</span>`,
      `<span>指纹 ${esc((r.hash || "").slice(0, 14))}</span>`,
      run.truncated ? `<span style="color:var(--warn)">⚠ 已截断，不可用于判定</span>` : "",
    ].join("");
    $("#replay-body").textContent = r.markdown || "(空)";
  } catch (e) { toast("回放失败：" + e.message, true); }
}
$("#btn-replay").addEventListener("click", renderReplay);
$("#btn-replay-copy").addEventListener("click", async () => {
  if (!replayCache) return toast("先重建一次回放", true);
  try { await navigator.clipboard.writeText(replayCache.markdown || ""); toast("Markdown 已复制"); }
  catch { toast("复制失败（浏览器不允许）", true); }
});

/* ── eval ────────────────────────────────────────────────────────────────── */
function renderEvalIdle() {
  $("#eval-body").innerHTML = `<div class="empty">填两个任务 id 后点「比对」；只填 base 时与当前任务比对。</div>`;
}
$("#btn-eval").addEventListener("click", async () => {
  const base = $("#eval-base").value.trim() || (state.task && state.task.id) || "";
  const cand = $("#eval-cand").value.trim() || (state.task && state.task.id) || "";
  if (!base || !cand) return toast("需要两个任务 id", true);
  const box = $("#eval-body");
  box.innerHTML = `<div class="empty">比对中…</div>`;
  try {
    const r = await api("/api/harness/consistency", {
      method: "POST", body: JSON.stringify({ base_task: String(base), cand_task: String(cand) }),
    });
    state.evalDone = true;
    box.innerHTML = `
      <div class="verdict ${r.passed ? "ok" : "bad"}">
        ${r.passed ? "一致" : "不一致"}
        <span class="tail">${esc(r.base)} → ${esc(r.cand)} · 候选拦截 ${esc(r.blocked_events ?? 0)}</span>
      </div>
      <table class="grid">
        <thead><tr><th style="width:70px">结果</th><th style="width:60px">级别</th>
          <th style="width:180px">检查项</th><th>说明</th></tr></thead>
        <tbody>${(r.checks || []).map((c) => `
          <tr><td style="color:${c.passed ? "var(--ok)" : "var(--danger)"};font-weight:600">${c.passed ? "PASS" : "FAIL"}</td>
          <td class="mono tiny">${c.hard ? "HARD" : "soft"}</td>
          <td class="mono tiny">${esc(c.name)}</td>
          <td class="tiny" style="color:var(--ink-2)">${esc(c.detail || "")}</td></tr>`).join("")}
        </tbody></table>
      <p class="note" style="margin-top:12px">base 指纹 ${esc((r.base_fingerprint || "").slice(0, 16))} · cand 指纹 ${esc((r.cand_fingerprint || "").slice(0, 16))}</p>`;
  } catch (e) {
    box.innerHTML = `<div class="empty" style="color:var(--danger)">比对失败：${esc(e.message)}</div>`;
  }
});

/* ── inspector ───────────────────────────────────────────────────────────── */
$$("#insp-tabs .insp-tab").forEach((b) => b.addEventListener("click", () => {
  state.insp = b.dataset.insp;
  $$("#insp-tabs .insp-tab").forEach((x) => x.classList.toggle("active", x === b));
  renderInspector();
}));

function renderInspector() {
  const box = $("#insp-body");
  if (!state.task) { box.innerHTML = `<p class="note">选择任务后显示。</p>`; return; }
  if (state.insp === "context") return renderContextPanel(box);
  if (state.insp === "evidence") return renderEvidencePanel(box);
  if (state.insp === "capability") return renderCapabilityPanel(box);
  if (state.insp === "policy") return renderPolicyPanel(box);
}

function renderContextPanel(box) {
  const t = state.task;
  const tk = t.tokens || {};
  const input = tk.input || 0, output = tk.output || 0, cr = tk.cache_read || 0, cw = tk.cache_write || 0;
  const total = tk.total || (input + output + cr + cw);
  const pct = (v) => total ? Math.max(0, Math.round((v / total) * 100)) : 0;
  const f = t.findings || {};
  const goals = t.goals_total || 0, met = t.goals_met || 0;
  box.innerHTML = `
    <div class="panel">
      <div class="panel-h"><span>目标进度</span><span>${met}/${goals || "—"}</span></div>
      <div class="bar"><span class="b3" style="width:${goals ? (met / goals) * 100 : 0}%"></span></div>
      <div class="legend"><span><i style="background:var(--ok)"></i>已证明 ${met}</span>
        <span><i style="background:var(--sunken)"></i>未达 ${Math.max(0, goals - met)}</span></div>
    </div>
    <div class="panel">
      <div class="panel-h"><span>令牌</span><span>${tok(total)}</span></div>
      <div class="bar">
        <span class="b1" style="width:${pct(input)}%"></span>
        <span class="b2" style="width:${pct(output)}%"></span>
        <span class="b3" style="width:${pct(cr + cw)}%"></span>
      </div>
      <div class="legend">
        <span><i style="background:var(--accent)"></i>输入 ${tok(input)}</span>
        <span><i style="background:var(--ink-3)"></i>输出 ${tok(output)}</span>
        <span><i style="background:var(--ok)"></i>缓存 ${tok(cr + cw)}</span>
      </div>
    </div>
    <div class="panel">
      <div class="panel-h"><span>运行状态</span></div>
      <dl class="kv">
        <dt>状态</dt><dd>${esc(statusOf(t))}</dd>
        <dt>运行中 worker</dt><dd>${num(t.in_flight || 0)}</dd>
        <dt>frontier（open）</dt><dd>${num(state.planOrder.length)}</dd>
        <dt>最近活动</dt><dd>${t.last_activity_unix ? new Date(t.last_activity_unix * 1000).toLocaleTimeString("zh-CN") : "—"}</dd>
        <dt>创建</dt><dd>${t.created_at ? esc(String(t.created_at).slice(0, 16).replace("T", " ")) : "—"}</dd>
        <dt>LLM</dt><dd>${t.active_llm_profile_id ? "#" + t.active_llm_profile_id : "全局激活"}</dd>
        ${t.llm_failover_state ? `<dt>故障转移</dt><dd>${esc(t.llm_failover_state)}</dd>` : ""}
      </dl>
    </div>
    <div class="panel">
      <div class="panel-h"><span>漏洞</span><span>${(f.critical || 0) + (f.high || 0) + (f.medium || 0) + (f.low || 0)}</span></div>
      <div class="legend" style="gap:12px">
        <span style="color:var(--danger)">严重 ${f.critical || 0}</span>
        <span style="color:var(--warn)">高 ${f.high || 0}</span>
        <span>中 ${f.medium || 0}</span>
        <span class="muted">低 ${f.low || 0}</span>
      </div>
      <p class="note" style="margin-top:8px">按严重度分档的任务内计数；“证据”页给出每条结论是否绑定了真实流量。</p>
    </div>`;
}

async function loadEvidence() {
  if (!state.task) return;
  try {
    state.evidence = await api(`/api/harness/evidence-integrity?task=${encodeURIComponent(state.task.id)}`);
  } catch { state.evidence = null; }
  if (state.insp === "evidence") renderInspector();
}

function renderEvidencePanel(box) {
  const r = state.evidence;
  if (!r) { box.innerHTML = `<p class="note">加载中…</p>`; return; }
  const rows = (r.rows || []).map((f) => `
    <li class="row">
      <span class="name" title="${esc(f.name)}">${esc(f.name)}</span>
      <span class="tag ${f.bindings ? "ok" : ""}">${f.bindings ? f.bindings + " 证据" : "无证据"}</span>
    </li>`).join("");
  box.innerHTML = `
    <div class="panel">
      <div class="panel-h"><span>证据完整性</span><span>${r.total}</span></div>
      <dl class="kv">
        <dt>有流量证据</dt><dd style="color:var(--ok)">${num(r.with_evidence)}</dd>
        <dt>无流量证据</dt><dd style="color:var(--warn)">${num(r.without_evidence)}</dd>
      </dl>
      <ul class="list" style="margin-top:10px">${rows || `<li class="note">本任务没有漏洞记录。</li>`}</ul>
    </div>
    <p class="note">${esc(r.note || "")}</p>`;
}

async function loadCapabilities() {
  if (state.caps) return;
  try { state.caps = await api("/api/harness/capabilities"); } catch { state.caps = { capabilities: [], summary: {} }; }
  if (state.insp === "capability") renderInspector();
}

const SURFACE = {
  "builtin": "内置", "custom-command": "命令", "custom-script": "脚本", "custom-http": "HTTP",
  "cli-wrapper": "CLI", "mcp": "MCP", "skill": "技能",
};

function renderCapabilityPanel(box) {
  if (!state.caps) { loadCapabilities(); box.innerHTML = `<p class="note">加载中…</p>`; return; }
  const caps = state.caps.capabilities || [];
  const sum = state.caps.summary || {};
  const head = Object.keys(sum).sort().map((k) => `${SURFACE[k] || k} ${sum[k]}`).join(" · ");
  box.innerHTML = `
    <div class="panel">
      <div class="panel-h"><span>能力注册表</span><span>${caps.length}</span></div>
      <p class="note" style="margin-bottom:10px">${esc(head)}</p>
      <ul class="list">${caps.map((c) => `
        <li class="row">
          <span class="name" title="${esc(c.detail || "")}">
            <span class="muted mono tiny">${esc(SURFACE[c.surface] || c.surface)}</span> ${esc(c.name)}
          </span>
          <span style="display:flex;gap:4px">
            ${c.exec ? `<span class="tag exec">可执行</span>` : ""}
            ${c.enabled ? "" : `<span class="tag off">停用</span>`}
          </span>
        </li>`).join("")}</ul>
    </div>
    <p class="note">exec 为事实描述（该能力可在主机上执行代码或发起请求），不是风险评分；harness 不替人下判断。</p>`;
}

async function loadJudge() {
  try { state.judge = await api("/api/intercept/judge"); } catch { state.judge = null; }
  const sel = $("#mode-select");
  if (state.judge) sel.value = state.judge.enabled ? "manual" : "auto";
  if (state.insp === "policy") renderInspector();
}

async function setJudgeMode(enabled) {
  if (!state.judge) return toast("读不到审批配置", true);
  const body = Object.assign({}, state.judge, { enabled });
  if (!body.fail_action) body.fail_action = "allow";
  if (!body.ask_timeout_action) body.ask_timeout_action = "deny";
  try {
    await api("/api/intercept/judge", { method: "PUT", body: JSON.stringify(body) });
    state.judge.enabled = enabled;
    $("#mode-select").value = enabled ? "manual" : "auto";
    toast(enabled ? "已开启模型兜底审批：可疑动作转人工（超时按配置处理）" : "已回到自动：按内置规则直接执行");
    if (state.insp === "policy") renderInspector();
  } catch (e) { toast("保存失败：" + e.message, true); }
}

$("#mode-select").addEventListener("change", (ev) => setJudgeMode(ev.target.value === "manual"));

function renderPolicyPanel(box) {
  const j = state.judge;
  box.innerHTML = `
    <div class="panel">
      <div class="panel-h"><span>审批模式</span><span>${j ? (j.enabled ? "人工" : "自动") : "—"}</span></div>
      <label class="switch">
        <input type="checkbox" id="policy-judge" ${j && j.enabled ? "checked" : ""} />
        <span class="track"></span>
        <span>模型兜底审批</span>
      </label>
      <p class="note" style="margin-top:8px">
        关闭（默认，与原版一致）：工具调用只受内置拦截规则约束，命中即拒，不打扰人。<br />
        开启：未命中规则的动作交给模型判定，可疑动作转为人工审批（超时 ${esc(j ? j.ask_timeout_seconds : 300)} 秒，按 ${esc(j ? (j.ask_timeout_action || "deny") : "deny")} 处理）。
      </p>
    </div>
    <div class="panel">
      <div class="panel-h"><span>硬拦截</span><span>内置规则</span></div>
      <p class="note">
        极端破坏行为（<span class="mono">rm -rf</span>、<span class="mono">mkfs</span>、
        <span class="mono">dd</span>、<span class="mono">DROP DATABASE</span>、
        <span class="mono">TRUNCATE</span>、清空防火墙、删库类接口…）由上游内置的 deny 规则挡住，
        本仓库一条未改。规则内容见「系统 → 命令拦截」。
      </p>
    </div>
    <div class="panel">
      <div class="panel-h"><span>可观测</span></div>
      <dl class="kv">
        <dt>拦截话术</dt><dd>逐字可比对</dd>
        <dt>一致性判定</dt><dd>「评测」页 / harness-gate</dd>
        <dt>回放</dt><dd>按 revision 重建</dd>
      </dl>
    </div>`;
  const cb = $("#policy-judge");
  if (cb) cb.addEventListener("change", () => setJudgeMode(cb.checked));
}

/* ── boot ────────────────────────────────────────────────────────────────── */
boot();
