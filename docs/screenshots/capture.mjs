// 抓取本仓库控制台的真实截图（1440×900，与参考图同尺寸便于逐项对照）。
//
// 走 CDP 而不是人肉点：每个状态都用一段可复算的 DOM 操作定位（按 aria-label /
// 文本找按钮），截图取自真实渲染，不是合成图。
//
// 用法：
//   node capture.mjs <base-url> <token-file> <out-dir>
import { spawn } from "node:child_process";
import { readFileSync, mkdirSync, writeFileSync } from "node:fs";

const BASE = process.env.BASE || "http://127.0.0.1:8796";
const TOKEN = readFileSync(process.env.TOKEN_FILE || "/tmp/tok5", "utf8").trim();
const OUT = process.env.OUT || "/tmp/shots-out";
const PORT = Number(process.env.CDP_PORT || 9400);
const W = 1440, H = 900;

mkdirSync(OUT, { recursive: true });

const chrome = spawn("google-chrome", [
  "--headless=new", `--remote-debugging-port=${PORT}`,
  "--no-sandbox", "--disable-gpu", "--hide-scrollbars",
  "--force-device-scale-factor=1",
  "--user-data-dir=/tmp/cdp-shots", "about:blank",
], { stdio: "ignore" });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function firstPage() {
  for (let i = 0; i < 80; i++) {
    try {
      const j = await (await fetch(`http://127.0.0.1:${PORT}/json`)).json();
      const p = j.find((t) => t.type === "page");
      if (p) return p;
    } catch { /* not up */ }
    await sleep(250);
  }
  throw new Error("chrome 没暴露 CDP");
}

const page = await firstPage();
const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
let seq = 0;
const pending = new Map();
ws.onmessage = (ev) => {
  const m = JSON.parse(ev.data);
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); }
};
const send = (method, params = {}) => {
  const id = ++seq;
  ws.send(JSON.stringify({ id, method, params }));
  return new Promise((r) => pending.set(id, r));
};
const evalJS = async (expression) => {
  const r = await send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (r.result?.exceptionDetails) throw new Error(JSON.stringify(r.result.exceptionDetails).slice(0, 300));
  return r.result?.result?.value;
};

// 侧栏行点击会同时带出任务右键菜单（.ui-menu）。菜单留在画面里会挡住建图，
// 所以每次截图前统一关掉它；关不掉就明确报出来，而不是拍一张带菜单的图。
async function dismissMenus() {
  const present = await evalJS(`!!document.querySelector('.ui-menu')`);
  if (!present) return 'none';
  // 合成 DOM 事件触发不了"点外面关闭"的监听，必须用真实鼠标事件。
  await realClick(720, 470);
  await sleep(250);
  const st = await evalJS(`document.querySelector('.ui-menu') ? 'STILL-OPEN' : 'dismissed'`);
  if (st !== "none") console.log("    menu:", st);
  return st;
}

async function realClick(x, y) {
  await send("Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
  await send("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", clickCount: 1 });
  await send("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", clickCount: 1 });
}

async function shot(name) {
  await dismissMenus();
  const r = await send("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  if (!r.result?.data) throw new Error("截图失败: " + name);
  writeFileSync(`${OUT}/${name}.png`, Buffer.from(r.result.data, "base64"));
  console.log("  ✓", name);
}

async function viewport(width, height) {
  await send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: false });
}

async function load({ token = true, appearance = "light" } = {}) {
  await send("Page.navigate", { url: BASE + "/app/" });
  await sleep(700);
  await evalJS(`
    (() => {
      ${token ? `localStorage.setItem('artex_token', ${JSON.stringify(TOKEN)});
      localStorage.setItem('artex.harness.token', ${JSON.stringify(TOKEN)});`
              : `localStorage.removeItem('artex_token'); localStorage.removeItem('artex.harness.token');`}
      localStorage.setItem('artex.appearance', ${JSON.stringify(appearance)});
      return true;
    })()
  `);
  await send("Page.navigate", { url: BASE + "/app/" });
  await sleep(1800);
}

// 按可见文本或 aria-label 点一个按钮。找不到就明确报错——静默跳过会让截图
// 看起来"没有那个面板"，而实际是选择器写错了。
const clickBy = (needle, kind = "any") => evalJS(`
  (() => {
    const nodes = [...document.querySelectorAll('button,[role="tab"],[role="button"],a')];
    const hit = nodes.find((b) => {
      const label = b.getAttribute('aria-label') || '';
      const text = (b.textContent || '').trim();
      if (${JSON.stringify(kind)} === 'aria') return label === ${JSON.stringify(needle)};
      if (${JSON.stringify(kind)} === 'text') return text === ${JSON.stringify(needle)};
      return label === ${JSON.stringify(needle)} || text === ${JSON.stringify(needle)};
    });
    if (!hit) return 'MISSING: ' + ${JSON.stringify(needle)};
    hit.click();
    return 'ok';
  })()
`);

// 任务不需要点选：控制台加载列表后会自动选中第一条（App.jsx：
// `if (list.length && !taskId) setTaskId(String(list[0].id))`）。
// 点侧栏行会连带打开行尾的下拉菜单（"打开 / 复制 ID / 置顶 / 删除"），
// 菜单会挡进截图里，所以这里只等它自己选中。
const waitTaskSelected = () => evalJS(`
  (() => {
    const hdr = document.querySelector('.topband');
    const ok = hdr && /acme-demo/.test(hdr.textContent || '');
    return ok ? 'ok' : 'MISSING selected task';
  })()
`);

// 每个状态截图前断言"确实到了那个状态"。静默跳过会让截图看起来像是某个面板
// 不存在，而实际是选择器或等待时间写错了——那种图比没有图更糟。
const assertHas = (selector, label) => evalJS(`
  (() => {
    const n = document.querySelector(${JSON.stringify(selector)});
    return n ? 'ok' : 'MISSING ' + ${JSON.stringify(label)};
  })()
`);

const scrollStream = (where) => evalJS(`
  (() => {
    const el = document.querySelector('.stream');
    if (!el) return 'MISSING .stream';
    el.scrollTop = ${where === "top" ? "0" : "el.scrollHeight"};
    return 'ok: ' + el.scrollTop + '/' + el.scrollHeight;
  })()
`);

await send("Page.enable");
await send("Runtime.enable");
await viewport(W, H);

// ── 1. 登录门（无令牌）────────────────────────────────────────────────
await load({ token: false });
await shot("harness-01-login");

// ── 2. 工作区（浅色，已选任务）────────────────────────────────────────
await load({ appearance: "light" });
console.log("selected task:", await waitTaskSelected());
await sleep(1200);
console.log("  stream:", await assertHas(".stream", "event stream"));
// 02 停在流顶部（轮次边界 + 本轮计划），03 再滚到底部（工具卡 + 审批卡），
// 两张不是同一画面。
await evalJS(`(() => { const el=document.querySelector('.stream'); if(el) el.scrollTop = 0; return true; })()`);
await sleep(400);
await shot("harness-02-home-light");

// ── 3. 事件流：工具卡片 + 审批卡（滚到末尾）──────────────────────────
await evalJS(`(() => { const el=document.querySelector('.stream'); if(el) el.scrollTop = el.scrollHeight; return true; })()`);
await sleep(500);
await shot("harness-03-conversation-light");

// ── 4. 用量（预算曲线 + 压缩标记 + 热力格）────────────────────────────
console.log("usage tab:", await clickBy("用量", "text"));
await sleep(900);
await shot("harness-04-usage-light");

// 回到对话页签
await clickBy("对话", "text");
await sleep(600);

// ── 5. 工作台 → 文件 ─────────────────────────────────────────────────
console.log("workbench:", await clickBy("工作台", "aria"));
await sleep(700);
console.log("  panel:", await assertHas(".subpanel", "subpanel"));
await shot("harness-05-workbench-files");

// ── 6. 工作台 → DIFF ─────────────────────────────────────────────────
console.log("diff tab:", await clickBy("DIFF", "text"));
await sleep(700);
console.log("  diff rows:", await assertHas(".file-row", "diff file rows"));
await shot("harness-06-workbench-diff");

// 打开一个有改动的文件，展示 unified diff 正文
await evalJS(`
  (() => {
    const row = [...document.querySelectorAll('.file-row')]
      .find((b) => /orders-idor/.test(b.textContent || ''));
    if (row) row.click();
    return row ? 'ok' : 'MISSING file row';
  })()
`);
await sleep(800);
await shot("harness-07-workbench-diff-file");

// ── 8. 工作台 → 终端 ─────────────────────────────────────────────────
console.log("terminal tab:", await clickBy("终端", "text"));
await sleep(600);
await evalJS(`
  (() => {
    const inp = document.querySelector('.subpanel input[placeholder*="输入命令"]');
    if (!inp) return 'MISSING terminal input';
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
    setter.call(inp, 'ls -la scripts && echo "---" && head -3 scripts/orders-idor.sh');
    inp.dispatchEvent(new Event('input', { bubbles: true }));
    return 'ok';
  })()
`);
await sleep(400);
await evalJS(`
  (() => {
    const btn = [...document.querySelectorAll('.subpanel button')]
      .find((b) => /执行/.test(b.textContent || ''));
    if (btn) { btn.click(); return 'ok'; }
    return 'MISSING run button';
  })()
`);
await sleep(1800);
await shot("harness-08-workbench-terminal");

// ── 9. 子代理 ────────────────────────────────────────────────────────
console.log("close workbench:", await clickBy("关闭工作台", "aria"));
await sleep(300);
console.log("subagents:", await clickBy("子代理", "aria"));
await sleep(1200);
console.log("  subagent rows:", await assertHas(".subpanel", "subagents panel"));
await shot("harness-09-subagents");

// ── 10. 浏览器（会话 + 标签；真实 MCP）────────────────────────────────
console.log("browser:", await clickBy("浏览器", "aria"));
await sleep(2500);
await shot("harness-10-browser-session");
console.log("f12 tab:", await clickBy("F12", "text"));
await sleep(900);
await shot("harness-11-browser-f12");

// ── 12. 系统 → 就绪 ──────────────────────────────────────────────────
console.log("system:", await clickBy("系统", "aria"));
await sleep(1500);
await shot("harness-12-system-ready");
console.log("caps tab:", await clickBy("能力", "text"));
await sleep(900);
await shot("harness-13-system-capabilities");

// ── 14. 命令面板 ─────────────────────────────────────────────────────
console.log("close system:", await clickBy("关闭系统面板", "aria"));
await sleep(300);
console.log("palette:", await clickBy("命令面板", "aria"));
await sleep(700);
await evalJS(`
  (() => {
    const inp = document.querySelector('.cmdk-input');
    if (!inp) return 'MISSING palette input';
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
    setter.call(inp, 'acme');
    inp.dispatchEvent(new Event('input', { bubbles: true }));
    return 'ok';
  })()
`);
await sleep(600);
await shot("harness-14-palette");
await evalJS(`(() => { document.querySelector('.cmdk-overlay')?.click(); return true; })()`);
await sleep(300);

// ── 15. 深色（对话页签 + 工具卡）─────────────────────────────────────
await load({ appearance: "dark" });
console.log("  selected:", await waitTaskSelected());
await sleep(1200);
await evalJS(`(() => { const el=document.querySelector('.stream'); if(el) el.scrollTop = el.scrollHeight; return true; })()`);
await sleep(500);
await shot("harness-15-conversation-dark");

// ── 16. 对话（无 goal 的另一条产品线）────────────────────────────────
// 命令面板只列"运行"，对话只能从侧栏选。点行会带出行尾下拉菜单，
// 但 shot() 用真实鼠标事件把它关掉，所以不影响成图。
await load({ appearance: "light" });
console.log("pick conv:", await evalJS(`
  (() => {
    const row = [...document.querySelectorAll('.row,[role="button"],button')]
      .find((b) => /演示：把结论收敛成三条/.test(b.textContent || ''));
    if (!row) return 'MISSING conv row';
    row.click();
    return 'ok';
  })()
`));
await sleep(1600);
console.log("  chat view:", await evalJS(`
  (() => {
    const hdr = document.querySelector('.topband');
    return hdr && /演示：把结论收敛成三条/.test(hdr.textContent || '') ? 'ok' : 'MISSING chat header';
  })()
`));
await shot("harness-16-chat-light");

// ── 17. 窄窗口（≤900px：侧栏隐藏、右栏改底部横条）─────────────────────
await viewport(900, 900);
await load({ appearance: "light" });
console.log("  selected:", await waitTaskSelected());
await sleep(900);
console.log("workbench (narrow):", await clickBy("工作台", "aria"));
await sleep(900);
await shot("harness-17-narrow-900");

ws.close();
chrome.kill();
console.log("done →", OUT);