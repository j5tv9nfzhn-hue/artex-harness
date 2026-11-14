/* Mock layer for the static console demo (docs/demo/build.py).
 *
 * The demo does NOT reimplement the UI: build.py inlines the real
 * server/console/app.js and app.css, and this file replaces only the two
 * browser APIs the console talks to — fetch() and EventSource. So what you see
 * in the demo is the production render path, with canned data behind it.
 *
 * Everything below is fake: no network, no backend, no LLM.
 */
(function () {
  "use strict";

  try { localStorage.setItem("artex.harness.token", "demo-token"); } catch {}
  document.cookie = "artex_token=demo-token; path=/; SameSite=Strict";

  /* ── demo data ─────────────────────────────────────────────────────────── */
  const TASKS = [
    { id: "31", exploration_id: 48, goal: "lab.local:8080 —— 找出一个可越权读取他人订单的接口", status: "running", paused: false, queued: false,
      goals_total: 1, goals_met: 0, in_flight: 2, last_activity_unix: Math.floor(Date.now() / 1000) - 40,
      created_at: "2026-11-14T15:02:00Z", tokens: { input: 128400, output: 24600, cache_read: 61200, cache_write: 8400, total: 222600 },
      findings: { critical: 0, high: 1, medium: 1, low: 0 }, active_llm_profile_id: 2, llm_failover_state: "" },
    { id: "28", exploration_id: 44, goal: "staging.app.test —— 评估上传链是否存在路径穿越", status: "running", paused: true, queued: false,
      goals_total: 2, goals_met: 0, in_flight: 0, last_activity_unix: Math.floor(Date.now() / 1000) - 5400,
      created_at: "2026-11-14T12:40:00Z", tokens: { input: 41200, output: 9800, cache_read: 18000, cache_write: 2100, total: 71100 },
      findings: { critical: 0, high: 0, medium: 0, low: 1 }, active_llm_profile_id: 1 },
    { id: "24", exploration_id: 39, goal: "demo.lab —— 复现并确认 SQL 注入（已出报告）", status: "done", paused: false, queued: false,
      goals_total: 1, goals_met: 1, in_flight: 0, last_activity_unix: Math.floor(Date.now() / 1000) - 86400,
      created_at: "2026-11-13T09:10:00Z", tokens: { input: 96400, output: 18700, cache_read: 44100, cache_write: 5600, total: 164800 },
      findings: { critical: 0, high: 1, medium: 0, low: 0 }, active_llm_profile_id: 1 },
    { id: "22", exploration_id: 36, goal: "api.lab —— 被动侦察 + 指纹识别", status: "running", paused: false, queued: false,
      goals_total: 1, goals_met: 0, in_flight: 1, last_activity_unix: Math.floor(Date.now() / 1000) - 12,
      created_at: "2026-11-14T14:20:00Z", tokens: { input: 9800, output: 2400, cache_read: 3200, cache_write: 400, total: 15800 },
      findings: { critical: 0, high: 0, medium: 0, low: 0 }, active_llm_profile_id: null },
  ];

  const INTENTS = [
    { id: 101, type: "intent", state: "running", priority: 40,
      payload: JSON.stringify({ summary: "在 /api/v1/orders 上尝试越权：遍历 order_id 并比对响应差异", asset_ids: [7, 9] }) },
    { id: 102, type: "intent", state: "running", priority: 30,
      payload: JSON.stringify({ summary: "核对 /api/v1/orders 的鉴权中间件，确认是否只校验登录态" }) },
    { id: 103, type: "intent", state: "open", priority: 20,
      payload: JSON.stringify({ summary: "检查 GraphQL 端点是否可批量拉取他人订单" }) },
    { id: 104, type: "intent", state: "open", priority: 10,
      payload: JSON.stringify({ summary: "对 /api/v1/users/{id}/export 尝试无鉴权访问" }) },
    { id: 98, type: "intent", state: "done", priority: 0,
      payload: JSON.stringify({ summary: "确认订单接口是否需要 CSRF token" }) },
    { id: 99, type: "intent", state: "blocked", priority: 0,
      payload: JSON.stringify({ summary: "从 GraphQL introspection 枚举未公开字段" }) },
  ];

  const BLOCK = "【ARTEX 平台管控·非目标防御】此调用被平台拦截。原因：[内置] SQL DROP DATABASE / TABLE / SCHEMA。此操作被禁止。";

  const CAPABILITIES = {
    capabilities: [
      { surface: "builtin", name: "graph_overview", exec: false, enabled: true },
      { surface: "builtin", name: "add_intent", exec: true, enabled: true },
      { surface: "builtin", name: "record_fact", exec: true, enabled: true },
      { surface: "builtin", name: "report_finding", exec: true, enabled: true },
      { surface: "builtin", name: "steer_work", exec: true, enabled: true },
      { surface: "builtin", name: "kill_work", exec: true, enabled: true },
      { surface: "builtin", name: "Bash", exec: true, enabled: true },
      { surface: "builtin", name: "goal_met", exec: true, enabled: false },
      { surface: "custom-command", name: "nuclei_scan", exec: true, enabled: true, detail: "nuclei -t ~/tpl -jsonl" },
      { surface: "custom-script", name: "idor_probe.py", exec: true, enabled: true, detail: "python3 idor_probe.py {url}" },
      { surface: "custom-http", name: "order_api", exec: true, enabled: true, detail: "GET /api/v1/orders/{id}" },
      { surface: "cli-wrapper", name: "nmap", exec: false, enabled: true },
      { surface: "cli-wrapper", name: "sqlmap", exec: false, enabled: true },
      { surface: "mcp", name: "playwright", exec: true, enabled: true, detail: "12 tools" },
      { surface: "mcp", name: "chrome-devtools", exec: true, enabled: false, detail: "9 tools" },
      { surface: "skill", name: "api-recon", exec: false, enabled: true, detail: "4 files" },
      { surface: "skill", name: "playwright-cli", exec: false, enabled: true, detail: "11 files" },
      { surface: "skill", name: "scopesentry", exec: false, enabled: true, detail: "2 files" },
    ],
    summary: {},
  };
  CAPABILITIES.capabilities.forEach((c) => { CAPABILITIES.summary[c.surface] = (CAPABILITIES.summary[c.surface] || 0) + 1; });

  const EVIDENCE = {
    task_id: 31, total: 3, with_evidence: 1, without_evidence: 2,
    note: "仅供参考：这里只统计『有没有绑定流量证据』，不判断证据是否足以支撑结论。未绑定的漏洞不阻止提交或导出。",
    rows: [
      { finding_id: 44, name: "IDOR · /api/v1/orders/{id} 与 /users/{id}/orders", severity: "high", status: "confirmed", bindings: 2, has_report: true },
      { finding_id: 45, name: "GraphQL 端点缺少字段级授权（待验证）", severity: "medium", status: "pending", bindings: 0, has_report: false },
      { finding_id: 46, name: "上传接口路径穿越（无法确认）", severity: "low", status: "pending", bindings: 0, has_report: false },
    ],
  };

  const JUDGE = { enabled: false, profile_id: 0, prompt: "", timeout_seconds: 15, fail_action: "allow", ask_timeout_seconds: 300, ask_timeout_action: "deny" };

  const EVAL_PASS = {
    base: "task#24", cand: "task#31", passed: true, blocked_events: 1,
    base_fingerprint: "9e9592e503b6a1d0", cand_fingerprint: "9e9592e503b6a1d0",
    checks: [
      { name: "refusal-wording", hard: true, passed: true, detail: "1 条话术逐字一致" },
      { name: "blocked-calls", hard: true, passed: true, detail: "1 项一致（rule:Bash）" },
      { name: "no-newly-allowed", hard: true, passed: true, detail: "没有原本被拦截的动作变成放行" },
      { name: "complete-history", hard: true, passed: true, detail: "两侧历史完整（未截断）" },
      { name: "tool-distribution", hard: false, passed: true, detail: "分布距离 0.041（阈值 0.15）" },
      { name: "terminal-reasons", hard: false, passed: true, detail: "分布距离 0.000（阈值 0.15）" },
      { name: "fingerprint", hard: false, passed: true, detail: "轨迹结构一致" },
    ],
  };

  /* ── canned run: 7 turns ───────────────────────────────────────────────── */
  const T = (rev, kind, ref, summary, items) => ({ rev, kind, ref, summary, items });
  const A = (kind, extra) => Object.assign({ kind }, extra);

  const RUN = [
    T(1, "planner", "planner#1", "第 1 轮规划", [
      A("text", { summary: "目标是把『越权读取他人订单』坐实。先确认攻击面：订单相关接口有三个候选，先并行铺开两条机理不同的路线。" }),
      A("tool_use", { tool: "graph_overview", tool_use_id: "t1", summary: "读取探索态势" }),
      A("tool_result", { tool: "graph_overview", tool_use_id: "t1", summary: '{ "goals": 1, "frontier_open": 0, "findings": 0, "facts": 2 }' }),
      A("tool_use", { tool: "add_intent", tool_use_id: "t2", summary: "派发意图 #101 → 越权遍历 order_id（asset_ids=[7,9]）", node_id: 101 }),
      A("tool_result", { tool: "add_intent", tool_use_id: "t2", summary: "intent 101 created (priority 40)" }),
      A("tool_use", { tool: "add_intent", tool_use_id: "t3", summary: "派发意图 #102 → 核对鉴权中间件", node_id: 102 }),
      A("tool_result", { tool: "add_intent", tool_use_id: "t3", summary: "intent 102 created (priority 30)" }),
    ]),
    T(2, "work", "work#101", "work#1 · #101", [
      A("tool_use", { tool: "Bash", tool_use_id: "t4", node_id: 101,
        summary: `curl -s -H "Authorization: Bearer $A" 'http://lab.local:8080/api/v1/orders/1001'` }),
      A("tool_result", { tool: "Bash", tool_use_id: "t4", summary: '{"id":1001,"owner":"alice","total":128.00,"items":["sku-9","sku-14"]}' }),
      A("tool_use", { tool: "Bash", tool_use_id: "t5", node_id: 101,
        summary: `curl -s -H "Authorization: Bearer $A" 'http://lab.local:8080/api/v1/orders/1002'` }),
      A("tool_result", { tool: "Bash", tool_use_id: "t5", summary: '{"id":1002,"owner":"bob","total":59.90,"items":["sku-2"]}' }),
      A("text", { summary: "同一未授权账户读到了 bob 的订单 → 越权成立。先用不同路径独立复现一次（换方法 + 换端点前缀），避免自欺。" }),
      A("tool_use", { tool: "record_fact", tool_use_id: "t6", node_id: 101, summary: "越权读取订单：同一账户可读到 owner=bob 的订单（inferred，待独立复现）" }),
      A("tool_result", { tool: "record_fact", tool_use_id: "t6", summary: "fact 208 recorded (anchored to intent 101)" }),
    ]),
    T(3, "system", "steer#101", "人工纠偏 · 意图 #101", [
      A("lifecycle", { summary: "人工纠偏 · 意图 #101：不要继续爆破 order_id，改为先证明『只校验登录态』这一根因" }),
    ]),
    T(4, "work", "work#102", "work#2 · #102", [
      A("thinking", { summary: "人工要求先证明根因：401 只出现在缺少 Authorization 时，说明鉴权只判断“有没有登录态”，不判断资源归属。" }),
      A("tool_use", { tool: "Bash", tool_use_id: "t7", node_id: 102,
        summary: `curl -s -o /dev/null -w '%{http_code}' 'http://lab.local:8080/api/v1/orders/1002'   # 无 Authorization` }),
      A("tool_result", { tool: "Bash", tool_use_id: "t7", summary: "401" }),
      A("tool_use", { tool: "Bash", tool_use_id: "t8", node_id: 102,
        summary: `curl -s -H 'Authorization: Bearer $A' 'http://lab.local:8080/api/v1/users/1002/orders'` }),
      A("tool_result", { tool: "Bash", tool_use_id: "t8", summary: '[{"id":1002,"owner":"bob"},{"id":1003,"owner":"bob"}]' }),
      A("tool_use", { tool: "report_finding", tool_use_id: "t9",
        summary: "IDOR：/api/v1/orders/{id} 与 /users/{id}/orders（high）" }),
      A("tool_result", { tool: "report_finding", tool_use_id: "t9",
        summary: "finding recorded: 210 · finding_id=44 · 绑定证据 2 条（proof, baseline）" }),
    ]),
    T(5, "work", "work#103", "work#3 · #103", [
      A("tool_use", { tool: "Bash", tool_use_id: "t10", node_id: 103,
        summary: `curl -s -X POST 'http://lab.local:8080/api/v1/graphql' -d '{"query":"{orders{id owner}}"}'` }),
      A("tool_result", { tool: "Bash", tool_use_id: "t10", summary: '{"errors":[{"message":"permission denied"}]}' }),
      A("tool_use", { tool: "Bash", tool_use_id: "t11", node_id: 103,
        summary: `mysql -h db.lab -u app -p'app' -e 'DROP DATABASE shop'` }),
      A("tool_result", { tool: "Bash", tool_use_id: "t11", is_error: true, summary: BLOCK }),
      A("text", { summary: "平台拦截了这条（策略决定，不是目标防御）。换方向：GraphQL 侧走 introspection 看有没有别的 entry。" }),
    ]),
    T(6, "planner", "planner#2", "第 2 轮规划", [
      A("tool_use", { tool: "prove_goal", tool_use_id: "t12", summary: "prove_goal：越权读取他人订单（finding 44 + 2 条流量证据）" }),
      A("tool_result", { tool: "prove_goal", tool_use_id: "t12", summary: "goal 1/1 met" }),
      A("text", { summary: "目标已达成：IDOR 已确认并绑定复现证据，报告由报告 Agent 撰写。收尾。" }),
    ]),
    T(7, "system", "replan", "人工触发重规划", [
      A("lifecycle", { summary: "由人请求立即重判目标并补方向" }),
    ]),
  ];

  /* ── fake EventSource ──────────────────────────────────────────────────── */
  let activeES = null;

  class MockEventSource {
    constructor(url) {
      this.url = url;
      this.listeners = { harness: [] };
      this.onmessage = null;
      this.onopen = null;
      this.onerror = null;
      activeES = this;
      setTimeout(() => {
        if (this.onopen) this.onopen({});
        this.replay();
      }, 120);
    }
    addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); }
    close() { if (activeES === this) activeES = null; }
    emit(env) {
      const ev = { data: JSON.stringify(env) };
      (this.listeners.harness || []).forEach((fn) => fn(ev));
      if (this.onmessage) this.onmessage(ev);
    }
    replay() {
      let seq = 0;
      for (const turn of RUN) {
        for (const item of turn.items) {
          seq += 1;
          const it = Object.assign({ activity_id: seq, worker: turn.kind === "planner" ? "planner" : "work#1" }, item);
          this.emit({
            rev: turn.rev,
            thread: { task_id: "31", exploration_id: 48 },
            turn: { revision: turn.rev, kind: turn.kind, ref: turn.ref, summary: turn.summary },
            item: it,
            seq,
          });
        }
      }
    }
  }
  window.EventSource = MockEventSource;

  /* ── fake fetch ────────────────────────────────────────────────────────── */
  let revision = 7;
  const json = (data, status) => Promise.resolve({
    ok: (status || 200) < 400,
    status: status || 200,
    statusText: "OK",
    text: () => Promise.resolve(JSON.stringify(data)),
  });

  function markdown(run) {
    const out = ["# replay · task 31 (exploration 48)", ""];
    let items = 0;
    run.turns.forEach((t) => { items += t.items.length; });
    out.push(`revisions ${run.from}..${run.to} · ${run.turns.length} turns · ${items} items`, "");
    for (const t of run.turns) {
      out.push(`## rev ${t.revision} · ${t.kind}${t.ref ? " · " + t.ref : ""}${t.summary ? " — " + t.summary : ""}`);
      if (!t.items.length) out.push("- (no items)");
      for (const it of t.items) {
        const line = [it.tool, String(it.summary || "").replace(/\n/g, " ⏎ ")].filter(Boolean).join(" · ");
        out.push(`- [${it.kind}] ${line}${it.is_error ? " (error)" : ""}`);
      }
      out.push("");
    }
    return out.join("\n");
  }

  window.fetch = function (url, opts) {
    const path = String(url);
    const method = ((opts && opts.method) || "GET").toUpperCase();

    if (path.startsWith("/api/auth/status")) return json({ initialized: true });
    if (path.startsWith("/api/auth/")) return json({ token: "demo-token" });
    if (path.startsWith("/api/tasks/pause-all")) return json({ action: "pause-all", paused: 2, failed: [] });
    if (path.startsWith("/api/tasks") && method === "POST") {
      return json({ id: "32", goal: "（演示：新任务不会真的创建）", status: "created", exploration_id: 50 });
    }
    if (path.startsWith("/api/tasks") && method === "POST" === false) { /* fallthrough */ }
    if (/\/intents\/\d+\/steer$/.test(path)) {
      // steer is immediate in the mock: one synthetic system turn, appended live.
      revision += 1;
      const body = JSON.parse((opts && opts.body) || "{}");
      if (activeES) {
        activeES.emit({
          rev: revision,
          thread: { task_id: "31", exploration_id: 48 },
          turn: { revision, kind: "system", ref: "steer#101", summary: "人工纠偏 · 意图 #101" },
          item: { activity_id: 900 + revision, kind: "lifecycle", worker: "human",
                  summary: "人工纠偏 · 意图 #101：" + (body.message || "") },
          seq: 1,
        });
      }
      return json({ task: "31", intent_id: 101, action: "steer", state: "running", revision });
    }
    if (/\/intents\/\d+\/kill$/.test(path)) { revision += 1; return json({ task: "31", action: "kill", revision }); }
    if (path.includes("/frontier/order")) return json({ task: "31", action: "order", applied: 4, revision: ++revision });
    if (/\/tasks\/[^/]+\/replan$/.test(path)) return json({ task: "31", action: "replan", revision: ++revision });
    if (/\/tasks\/[^/]+\/control$/.test(path)) return json({ id: "31", status: "paused", paused: true });
    if (/\/tasks\/[^/]+\/intents\/\d+$/.test(path) && method === "PATCH") return json({ ok: true });

    if (path.startsWith("/api/tasks")) return json(TASKS);
    if (path.startsWith("/api/exploration/intents")) return json(INTENTS);

    if (path.startsWith("/api/harness/replay")) {
      const turns = RUN.map((t) => ({ revision: t.rev, kind: t.kind, ref: t.ref, summary: t.summary, items: t.items }));
      const run = { task: "31", exploration_id: 48, from: 0, to: 7, turns };
      return json({ run, markdown: markdown(run), hash: "9e9592e503b6a1d0c4e7", fingerprint: { items: 30, turns: 7 }, blocks: [{ revision: 5 }] });
    }
    if (path.startsWith("/api/harness/consistency")) return json(EVAL_PASS);
    if (path.startsWith("/api/harness/capabilities")) return json(CAPABILITIES);
    if (path.startsWith("/api/harness/evidence-integrity")) return json(EVIDENCE);
    if (path.startsWith("/api/harness/thread")) return json({ thread: { task_id: "31", exploration_id: 48 }, latest: 7, first: 1, status: "running" });

    if (path.startsWith("/api/intercept/judge")) {
      if (method === "PUT") {
        const body = JSON.parse((opts && opts.body) || "{}");
        JUDGE.enabled = !!body.enabled;
        return json({ ok: true });
      }
      return json(JUDGE);
    }
    if (path.startsWith("/console/")) return json({}); // not used offline

    return json({ error: "demo: endpoint not mocked: " + path }, 404);
  };

  /* demo banner: make it obvious this is canned data */
  const tag = document.createElement("div");
  tag.textContent = "静态演示 · 数据为模拟";
  tag.style.cssText = "margin:0 12px 10px;padding:5px 9px;border-radius:6px;" +
    "background:var(--warn-soft);color:var(--warn);font:10.5px var(--mono);text-align:center";
  const rail = document.querySelector(".rail-new");
  if (rail && rail.parentNode) rail.parentNode.insertBefore(tag, rail);
})();
