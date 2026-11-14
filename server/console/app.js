/* artex-harness console — zero-build, same-origin, no external deps.
 *
 * Why no framework/build step: the console is shipped inside the single binary
 * (go:embed) so a bare-metal box needs no Node toolchain. Everything here is
 * plain ES2020 + the platform's EventSource.
 *
 * The Run view is driven by the harness protocol stream (S1): every event is an
 * Envelope {rev, thread, turn, item}. `rev` groups events into turns; the SSE
 * `id:` is the activity id and is what the browser replays from on reconnect.
 */
"use strict";

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
const esc = (s) =>
  String(s ?? "").replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const state = {
  tasks: [],
  task: null,
  es: null,
  seen: new Set(),      // activity ids already rendered (stream replay + live overlap)
  turns: new Map(),     // rev -> {el, items}
  intents: new Map(),   // intent id (string) -> intent node DTO
  selectedIntent: null,
  planOrder: [],        // ids, ordered as shown
  planDirty: false,
};

/* ── token ─────────────────────────────────────────────────────────────────
 * The backend accepts the JWT either as `Authorization: Bearer` or as the
 * `artex_token` cookie (server/auth.go extractToken). We do BOTH: the header for
 * fetch(), the cookie for EventSource — which cannot set headers, and the live
 * stream is the whole point of this console.
 * The upstream Next.js client does the same thing from lib/cookie.client.ts.
 */
const TOKEN_KEY = "artex.harness.token";

function getToken() {
  try { return localStorage.getItem(TOKEN_KEY) || ""; } catch { return ""; }
}
function setToken(tok) {
  try {
    if (tok) localStorage.setItem(TOKEN_KEY, tok);
    else localStorage.removeItem(TOKEN_KEY);
  } catch { /* private mode: cookie path still works */ }
  document.cookie =
    "artex_token=" + encodeURIComponent(tok || "") + "; path=/; SameSite=Strict";
}

/* ── HTTP ─────────────────────────────────────────────────────────────── */
async function api(path, opts = {}) {
  const headers = Object.assign({}, opts.headers);
  if (opts.body) headers["Content-Type"] = "application/json";
  const tok = getToken();
  if (tok) headers["Authorization"] = "Bearer " + tok;
  const res = await fetch(path, {
    credentials: "same-origin",
    ...opts,
    headers,
  });
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = text; }
  if (!res.ok) {
    if (res.status === 401 && !String(path).startsWith("/api/auth/")) {
      setToken("");
      showGate();
    }
    const msg = (data && (data.error || data.message)) || res.statusText || `HTTP ${res.status}`;
    const err = new Error(msg);
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
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, 3600);
}

/* ── auth gate ────────────────────────────────────────────────────────── */
async function boot() {
  let st = null;
  try { st = await api("/api/auth/status"); } catch { /* backend not ready */ }
  if (!st || !st.initialized) {
    // First run: the same form initialises the password (username is fixed ARTEX).
    $("#gate-hint").textContent = "首次使用：设置管理密码（用户名固定为 ARTEX）";
    return showGate();
  }
  if (!getToken()) return showGate();
  try {
    await api("/api/tasks");
    enterApp();
  } catch (e) {
    setToken("");
    showGate();
  }
}

function showGate() {
  $("#gate").hidden = false;
  $("#app").hidden = true;
  $("#gate-pass").focus();
}

$("#gate-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const pass = $("#gate-pass").value;
  const errEl = $("#gate-err");
  errEl.hidden = true;
  try {
    const st = await api("/api/auth/status");
    if (!st.initialized) {
      await api("/api/auth/init", { method: "POST", body: JSON.stringify({ password: pass }) });
    }
    const r = await api("/api/auth/login", {
      method: "POST",
      body: JSON.stringify({ username: "ARTEX", password: pass }),
    });
    if (r && r.token) setToken(r.token);
    $("#gate").hidden = true;
    enterApp();
  } catch (e) {
    errEl.textContent = e.message;
    errEl.hidden = false;
  }
});

/* ── app shell ────────────────────────────────────────────────────────── */
function enterApp() {
  $("#gate").hidden = true;
  $("#app").hidden = false;
  loadTasks();
  setInterval(() => { if (!document.hidden) loadTasks(true); }, 15000);
}

async function loadTasks(quiet = false) {
  try {
    const data = await api("/api/tasks");
    const list = Array.isArray(data) ? data : (data.tasks || data.items || []);
    state.tasks = list;
    renderTasks();
    if (!state.task && list.length) selectTask(list[0].id);
  } catch (e) {
    if (!quiet) toast("加载任务失败：" + e.message, true);
  }
}

function statusClass(s) {
  if (s === "running") return "run";
  if (s === "done" || s === "achieved") return "done";
  if (s === "failed" || s === "timeout") return "bad";
  return "";
}

function renderTasks() {
  const ul = $("#task-list");
  ul.innerHTML = state.tasks
    .map((t) => {
      const goal = t.goal || t.description || t.name || `task ${t.id}`;
      const st = t.status || "—";
      const active = state.task && String(state.task.id) === String(t.id);
      return `<li class="task-item${active ? " active" : ""}" data-id="${esc(t.id)}">
        <span class="t-goal">${esc(goal)}</span>
        <span class="t-meta">
          <span class="pill ${statusClass(st)}" style="padding:0 5px">${esc(st)}</span>
          <span>#${esc(t.id)}</span>
          ${t.paused ? '<span style="color:var(--warn)">paused</span>' : ""}
        </span></li>`;
    })
    .join("");
  $("#rail-stat").textContent = `${state.tasks.length} 个任务`;
}

$("#task-list").addEventListener("click", (ev) => {
  const li = ev.target.closest(".task-item");
  if (li) selectTask(li.dataset.id);
});
$("#btn-refresh").addEventListener("click", () => loadTasks());
$("#btn-pause-all").addEventListener("click", async () => {
  if (!confirm("暂停所有正在运行的任务？")) return;
  try {
    const r = await api("/api/tasks/pause-all", { method: "POST" });
    toast(`已暂停 ${r.paused} 个任务` + (r.failed?.length ? `，失败 ${r.failed.length}` : ""));
    loadTasks(true);
  } catch (e) { toast("急停失败：" + e.message, true); }
});

function selectTask(id) {
  const t = state.tasks.find((x) => String(x.id) === String(id));
  if (!t) return;
  if (state.task && String(state.task.id) === String(id)) return;
  state.task = t;
  state.seen.clear();
  state.turns.clear();
  state.intents.clear();
  state.selectedIntent = null;
  state.planOrder = [];
  state.planDirty = false;
  $("#stream").innerHTML = "";
  $("#task-goal").textContent = t.goal || t.description || t.name || `task ${t.id}`;
  $("#pill-status").textContent = t.status || "—";
  $("#pill-status").className = "pill " + statusClass(t.status);
  renderTasks();
  renderDock();
  connectStream(t.id);
  refreshThreadMeta();
  loadIntents();
}

async function refreshThreadMeta() {
  if (!state.task) return;
  try {
    const th = await api(`/api/harness/thread?task=${encodeURIComponent(state.task.id)}`);
    $("#pill-rev").textContent = `rev ${th.latest ?? 0}`;
    $("#pill-status").textContent = th.status || "—";
    $("#pill-status").className = "pill " + statusClass(th.status);
  } catch { /* non-fatal */ }
}

/* ── live stream ──────────────────────────────────────────────────────── */
function connectStream(taskId) {
  if (state.es) { state.es.close(); state.es = null; }
  const live = $("#pill-live");
  live.textContent = "● 连接中";
  live.className = "pill mono";
  const es = new EventSource(`/api/harness/stream?task=${encodeURIComponent(taskId)}`);
  state.es = es;

  es.onopen = () => { live.textContent = "● 实时"; live.className = "pill mono live"; };
  es.onerror = () => { live.textContent = "● 断开（自动重连）"; live.className = "pill mono dead"; };
  const onEnv = (ev) => {
    let env;
    try { env = JSON.parse(ev.data); } catch { return; }
    renderEnvelope(env);
  };
  es.addEventListener("harness", onEnv);
  es.onmessage = onEnv; // default channel: some proxies drop custom event names
}

function turnKey(env) {
  return env.rev > 0 ? env.rev : "legacy";
}

function renderEnvelope(env) {
  const it = env.item || {};
  const id = it.activity_id || 0;
  if (id && state.seen.has(id)) return;   // replay/live overlap
  if (id) state.seen.add(id);

  const key = turnKey(env);
  let turn = state.turns.get(key);
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
    state.turns.set(key, turn);
  }

  turn.items.appendChild(renderItem(it, env));
  maybeScroll();
  if (env.rev > 0) $("#pill-rev").textContent = `rev ${env.rev}`;
}

let pinnedToBottom = true;
$("#stream").addEventListener("scroll", () => {
  const s = $("#stream");
  pinnedToBottom = s.scrollHeight - s.scrollTop - s.clientHeight < 80;
});
function maybeScroll() {
  if (pinnedToBottom) $("#stream").scrollTop = $("#stream").scrollHeight;
}

function renderItem(it, env) {
  const kind = it.kind || "text";
  const row = document.createElement("div");
  row.className = "item";

  let body = "";
  if (kind === "tool_use") {
    body = `<span class="tool">${esc(it.tool || "tool")}</span> <span class="txt">${esc(it.summary || "")}</span>`;
  } else if (kind === "tool_result") {
    body = `<span class="txt detail">${esc(it.summary || it.detail || "(no output)")}</span>`;
  } else if (kind === "intercept") {
    body = `<span style="color:var(--warn)">${esc(it.summary || "审批请求")}</span>`;
  } else {
    body = `<span class="txt">${esc(it.summary || it.detail || "")}</span>`;
  }
  // A tool_use row whose work belongs to an intent becomes a handle for the dock.
  const nodeId = it.node_id;
  if (nodeId) {
    body += ` <span class="intent-ref" data-intent="${esc(nodeId)}">#${esc(nodeId)}</span>`;
  }
  row.innerHTML = `<div class="item-badge ${esc(kind)}">${esc(kind.replace("_", " "))}</div>
    <div class="item-body${it.is_error ? " err" : ""}">${body}</div>`;
  const ref = row.querySelector(".intent-ref");
  if (ref) ref.addEventListener("click", () => selectIntent(ref.dataset.intent));
  return row;
}

/* ── intervention dock ────────────────────────────────────────────────── */
async function selectIntent(intentId) {
  const id = String(intentId);
  state.selectedIntent = id;
  if (!state.intents.has(id)) await loadIntents(true);
  renderDock();
}

function renderDock() {
  const box = $("#dock-body");
  const id = state.selectedIntent;
  if (!id) {
    box.innerHTML = `<p class="dim xs">在事件流或 Plan 里点一条意图，即可在这里纠偏 / 停止 / 编辑。</p>`;
    return;
  }
  const it = state.intents.get(String(id)) || {};
  const payload = parsePayload(it.payload);
  const summary = payload.summary || "(无摘要)";
  const st = it.state || "—";
  box.innerHTML = `
    <div class="dock-intent">
      <div class="i-head"><span>意图 #${esc(id)}</span><span>${esc(st)}</span></div>
      <div>${esc(summary)}</div>
    </div>
    <label for="steer-msg">运行中纠偏（不中断当前轮）</label>
    <textarea id="steer-msg" rows="3" placeholder="例如：不要继续爆破 /admin，先看 /api/v1 的越权"></textarea>
    <button id="btn-steer" class="primary sm">发送纠偏</button>
    <div class="dock-row" style="margin-top:4px">
      <button id="btn-kill" class="danger sm">停止该 work</button>
      <button id="btn-edit" class="sm">编辑意图</button>
    </div>
    <div id="edit-box" hidden style="display:grid;gap:6px">
      <label for="edit-summary">摘要</label>
      <textarea id="edit-summary" rows="3"></textarea>
      <label for="edit-prio">优先级（越大越先）</label>
      <input id="edit-prio" type="number" step="1" />
      <button id="btn-edit-save" class="primary sm">保存修改</button>
    </div>`;

  $("#edit-summary").value = summary;
  $("#edit-prio").value = it.priority ?? 0;

  $("#btn-steer").addEventListener("click", () => doSteer(id));
  $("#btn-kill").addEventListener("click", () => doKill(id));
  $("#btn-edit").addEventListener("click", () => {
    const b = $("#edit-box");
    b.hidden = !b.hidden;
    if (!b.hidden) b.style.display = "grid";
  });
  $("#btn-edit-save").addEventListener("click", () => doEdit(id));
}

async function doSteer(intentId) {
  const msg = $("#steer-msg").value.trim();
  if (!msg) return toast("纠偏内容不能为空", true);
  try {
    await api(`/api/tasks/${encodeURIComponent(state.task.id)}/intents/${intentId}/steer`, {
      method: "POST", body: JSON.stringify({ message: msg }),
    });
    $("#steer-msg").value = "";
    toast("纠偏已投递给运行中的 work（下个工具调用前生效）");
  } catch (e) { toast("纠偏失败：" + e.message, true); }
}

async function doKill(intentId) {
  if (!confirm(`停止意图 #${intentId} 的运行中 work？`)) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(state.task.id)}/intents/${intentId}/kill`, { method: "POST" });
    toast("已请求停止");
    loadIntents(true);
  } catch (e) { toast("停止失败：" + e.message, true); }
}

async function doEdit(intentId) {
  const summary = $("#edit-summary").value.trim();
  const prio = parseInt($("#edit-prio").value, 10);
  const patch = {};
  if (summary) patch.summary = summary;
  if (!Number.isNaN(prio)) patch.priority = prio;
  if (!Object.keys(patch).length) return toast("没有要修改的字段", true);
  try {
    await api(`/api/tasks/${encodeURIComponent(state.task.id)}/intents/${intentId}`, {
      method: "PATCH", body: JSON.stringify(patch),
    });
    toast("意图已修改");
    loadIntents(true);
  } catch (e) { toast("修改失败：" + e.message, true); }
}

$("#btn-dock-close").addEventListener("click", () => {
  state.selectedIntent = null;
  renderDock();
});

/* ── Plan ─────────────────────────────────────────────────────────────── */
function parsePayload(raw) {
  if (!raw) return {};
  if (typeof raw === "object") return raw;
  try { return JSON.parse(raw); } catch { return {}; }
}

async function loadIntents(quiet = false) {
  if (!state.task) return;
  try {
    const data = await api(`/api/exploration/intents?task=${encodeURIComponent(state.task.id)}&limit=500`);
    const list = Array.isArray(data) ? data : (data.items || []);
    state.intents.clear();
    for (const n of list) state.intents.set(String(n.id), n);
    if (!state.planDirty) rebuildPlanOrder();
    renderPlan();
    if (state.selectedIntent) renderDock();
  } catch (e) {
    if (!quiet) toast("加载意图失败：" + e.message, true);
  }
}

function isOpenIntent(n) {
  return (n.type === "intent" || n.kind === "intent") && n.state === "open";
}

function rebuildPlanOrder() {
  const open = Array.from(state.intents.values()).filter(isOpenIntent);
  // Mirror the server's claim order so what you see is what workers get.
  open.sort((a, b) => (b.priority ?? 0) - (a.priority ?? 0) || Number(a.id) - Number(b.id));
  state.planOrder = open.map((n) => String(n.id));
}

function renderPlan() {
  const body = $("#plan-body");
  const rows = state.planOrder.map((id, idx) => {
    const n = state.intents.get(id) || {};
    const p = parsePayload(n.payload);
    const sel = state.selectedIntent === id ? " sel" : "";
    return `<tr class="${sel}" data-id="${esc(id)}">
      <td class="mono">${idx + 1}</td>
      <td class="mono"></td>
      <td><span class="plan-sum" contenteditable="true" data-id="${esc(id)}">${esc(p.summary || "")}</span></td>
      <td><input class="plan-prio" type="number" value="${esc(n.priority ?? 0)}" data-id="${esc(id)}" /></td>
      <td>${esc(n.state || "")}</td>
      <td><div class="plan-ops">
        <button class="icon sm" data-up="${esc(id)}" title="上移">↑</button>
        <button class="icon sm" data-down="${esc(id)}" title="下移">↓</button>
        <button class="icon sm" data-steer="${esc(id)}" title="选中并纠偏">✎</button>
      </div></td>
    </tr>`;
  });
  body.innerHTML = rows.join("") ||
    `<tr><td colspan="6" class="dim" style="padding:16px">frontier 为空（没有 open 状态的意图）。</td></tr>`;
  $("#btn-order-save").disabled = !state.planDirty;
}

$("#plan-body").addEventListener("click", (ev) => {
  const t = ev.target;
  const up = t.dataset.up, down = t.dataset.down, steer = t.dataset.steer;
  if (up) moveIntent(up, -1);
  else if (down) moveIntent(down, 1);
  else if (steer) { selectIntent(steer); switchTab("run"); }
  else {
    const row = t.closest("tr");
    if (row) { state.selectedIntent = row.dataset.id; renderPlan(); renderDock(); }
  }
});

function moveIntent(id, delta) {
  const arr = state.planOrder;
  const i = arr.indexOf(id);
  const j = i + delta;
  if (i < 0 || j < 0 || j >= arr.length) return;
  [arr[i], arr[j]] = [arr[j], arr[i]];
  state.planDirty = true;
  renderPlan();
}

$("#plan-body").addEventListener("change", async (ev) => {
  const el = ev.target;
  if (!el.classList.contains("plan-prio")) return;
  const id = el.dataset.id;
  const prio = parseInt(el.value, 10);
  if (Number.isNaN(prio)) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(state.task.id)}/intents/${id}`, {
      method: "PATCH", body: JSON.stringify({ priority: prio }),
    });
    toast("优先级已更新");
    loadIntents(true);
  } catch (e) { toast("更新失败：" + e.message, true); }
});

$("#plan-body").addEventListener("focusout", async (ev) => {
  const el = ev.target;
  if (!el.classList || !el.classList.contains("plan-sum")) return;
  const id = el.dataset.id;
  const summary = el.textContent.trim();
  const current = parsePayload((state.intents.get(id) || {}).payload).summary || "";
  if (!summary || summary === current) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(state.task.id)}/intents/${id}`, {
      method: "PATCH", body: JSON.stringify({ summary }),
    });
    toast("摘要已更新");
    loadIntents(true);
  } catch (e) { toast("更新失败：" + e.message, true); }
});

$("#btn-order-save").addEventListener("click", async () => {
  if (!state.planOrder.length) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(state.task.id)}/frontier/order`, {
      method: "POST", body: JSON.stringify({ ids: state.planOrder.map(Number) }),
    });
    state.planDirty = false;
    toast("顺序已保存，worker 将按新顺序领取");
    loadIntents(true);
  } catch (e) { toast("保存失败：" + e.message, true); }
});

$("#btn-plan-refresh").addEventListener("click", () => loadIntents());

/* ── top actions ──────────────────────────────────────────────────────── */
$("#btn-replan").addEventListener("click", async () => {
  if (!state.task) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(state.task.id)}/replan`, { method: "POST" });
    toast("已请求立即重规划");
  } catch (e) { toast("触发失败：" + e.message, true); }
});

async function taskControl(action) {
  if (!state.task) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(state.task.id)}/control`, {
      method: "POST", body: JSON.stringify({ action }),
    });
    toast(action === "pause" ? "任务已暂停" : "任务已恢复");
    loadTasks(true);
    refreshThreadMeta();
  } catch (e) { toast("操作失败：" + e.message, true); }
}
$("#btn-pause").addEventListener("click", () => taskControl("pause"));
$("#btn-resume").addEventListener("click", () => taskControl("resume"));

/* ── tabs ─────────────────────────────────────────────────────────────── */
function switchTab(name) {
  $$(".tab").forEach((b) => b.classList.toggle("active", b.dataset.tab === name));
  ["run", "plan", "replay", "eval"].forEach((n) => {
    const el = $("#tab-" + n);
    if (el) el.hidden = n !== name;
  });
  if (name === "plan") loadIntents(true);
  if (name === "replay") $("#replay-meta").dataset.stale = "1";
}
$$(".tab").forEach((b) => b.addEventListener("click", () => switchTab(b.dataset.tab)));

/* ── Replay ─────────────────────────────────────────────────────────────
 * A replay is a value: rebuilt from revisions, deterministic to render, so the
 * same slice can be re-read, copied as Markdown, or fed to harness-gate.
 */
let lastReplay = null;

async function loadReplay() {
  if (!state.task) return toast("先选一个任务", true);
  const from = $("#replay-from").value.trim();
  const to = $("#replay-to").value.trim();
  const qs = new URLSearchParams({ task: state.task.id });
  if (from) qs.set("from", from);
  if (to) qs.set("to", to);
  try {
    const r = await api("/api/harness/replay?" + qs.toString());
    lastReplay = r;
    const run = r.run || {};
    const fp = r.fingerprint || {};
    $("#replay-meta").innerHTML =
      `<span>rev ${esc(run.from ?? 0)}..${esc(run.to ?? 0)}</span>` +
      `<span>turns ${esc((run.turns || []).length)}</span>` +
      `<span>items ${esc(fp.items ?? 0)}</span>` +
      `<span>blocked ${esc((r.blocks || []).length)}</span>` +
      `<span>fingerprint ${esc((r.hash || "").slice(0, 16))}</span>` +
      (run.truncated ? '<span style="color:var(--warn)">⚠ 已截断，不可用于判定</span>' : "");
    $("#replay-body").textContent = r.markdown || "(空)";
  } catch (e) { toast("回放失败：" + e.message, true); }
}

$("#btn-replay").addEventListener("click", loadReplay);
$("#btn-replay-md").addEventListener("click", async () => {
  if (!lastReplay) return toast("先重建一次回放", true);
  try {
    await navigator.clipboard.writeText(lastReplay.markdown || "");
    toast("Markdown 已复制");
  } catch { toast("复制失败（浏览器不允许）", true); }
});

/* ── Eval ───────────────────────────────────────────────────────────────
 * Runs the same comparison the CLI (eval/cmd/harness-gate) does, so the verdict
 * the operator sees here is the verdict CI would give.
 */
async function runConsistency() {
  const base = $("#eval-base").value.trim() || (state.task && state.task.id) || "";
  const cand = $("#eval-cand").value.trim();
  if (!base || !cand) return toast("需要 base 与 cand 两个 task id", true);
  const box = $("#eval-report");
  box.innerHTML = '<p class="dim xs">比对中…</p>';
  try {
    const rep = await api("/api/harness/consistency", {
      method: "POST",
      body: JSON.stringify({ base_task: String(base), cand_task: String(cand) }),
    });
    const ok = rep.passed;
    const rows = (rep.checks || []).map((c) => `
      <tr>
        <td class="${c.passed ? "eval-pass" : "eval-fail"}">${c.passed ? "PASS" : "FAIL"}</td>
        <td class="mono">${c.hard ? "HARD" : "soft"}</td>
        <td class="mono">${esc(c.name)}</td>
        <td class="eval-detail">${esc(c.detail || "")}</td>
      </tr>`).join("");
    box.innerHTML = `
      <div class="eval-verdict ${ok ? "ok" : "bad"}">
        ${ok ? "一致 ✅" : "不一致 ❌"} · ${esc(rep.base)} → ${esc(rep.cand)}
      </div>
      <table class="eval-table">
        <thead><tr><th style="width:64px">结果</th><th style="width:56px">级别</th>
          <th style="width:170px">检查项</th><th>说明</th></tr></thead>
        <tbody>${rows}</tbody>
      </table>
      <p class="dim xs" style="margin-top:10px">
        base fingerprint ${esc((rep.base_fingerprint || "").slice(0, 16))} ·
        cand fingerprint ${esc((rep.cand_fingerprint || "").slice(0, 16))} ·
        候选拦截事件 ${esc(rep.blocked_events ?? 0)}
      </p>`;
  } catch (e) {
    box.innerHTML = `<p class="eval-fail">比对失败：${esc(e.message)}</p>`;
  }
}

$("#btn-eval").addEventListener("click", runConsistency);

boot();
