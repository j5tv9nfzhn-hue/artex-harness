import { useEffect, useRef, useState } from "react";
import { Box, Button, Card, Code, Flex, Heading, Text, TextField } from "./ui.jsx";
import { KeyRound, ShieldCheck } from "lucide-react";
import { parseErrorBody } from "./lib/auth.js";

/* 登录 / 首次设置密码（/app/ 的唯一入口门）。
 *
 * 为什么需要它：服务端有 /api/auth/status|init|login，但控制台此前完全没有入口，
 * 于是首次部署打开 /app/ 时每个请求都是 401，界面只能显示一堆"事件流不可用"。
 * README 也承诺了"首次进入要设置管理密码"，这里把它兑现。
 *
 * 契约（见 server/auth.go）：
 *   GET  /api/auth/status → {initialized}
 *   POST /api/auth/init   {password} → {token}   （仅首次，用户名固定 ARTEX）
 *   POST /api/auth/login  {username,password} → {token}
 *
 * 令牌写入两个 localStorage 键：artex_token（主）与 artex.harness.token（旧键兼容，
 * 见 lib/auth.js），并写 artex_token cookie 供 EventSource 一类不能设头的请求使用。
 */

const TOKEN_KEYS = ["artex_token", "artex.harness.token"];

export function storeToken(token) {
  try {
    for (const k of TOKEN_KEYS) localStorage.setItem(k, token);
  } catch { /* 隐私模式：cookie 仍可用 */ }
  // EventSource 不能设置请求头；服务端 extractToken 也接受这个 cookie。
  try { document.cookie = `artex_token=${token}; path=/; SameSite=Lax; max-age=${7 * 24 * 3600}`; } catch { /* ignore */ }
}

export function clearToken() {
  try { for (const k of TOKEN_KEYS) localStorage.removeItem(k); } catch { /* ignore */ }
  try { document.cookie = "artex_token=; path=/; max-age=0"; } catch { /* ignore */ }
}

// statusRequest resolves to the server's auth state, or an error string.
async function statusRequest() {
  const r = await fetch("/api/auth/status", { credentials: "include" });
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
  return r.json();
}

export default function LoginGate({ onAuthed }) {
  const [state, setState] = useState("checking"); // checking | init | login | down
  const [detail, setDetail] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const pwRef = useRef(null);

  const probe = async () => {
    setErr("");
    try {
      const s = await statusRequest();
      setState(s.initialized ? "login" : "init");
      setDetail("");
    } catch (e) {
      // 数据库不可用时服务端回 503 而不是 initialized:false —— 不能把故障
      // 包装成"还没设密码"，否则用户会去覆盖已有密码。这里如实显示原因。
      setState("down");
      setDetail(e.message || String(e));
    }
  };
  useEffect(() => { probe(); }, []);
  useEffect(() => { if (state !== "checking" && state !== "down") pwRef.current?.focus(); }, [state]);

  async function submit(e) {
    e?.preventDefault?.();
    setErr("");
    const pw = password;
    if (!pw) { setErr("请输入密码"); return; }
    if (state === "init") {
      if (pw.length < 8) { setErr("密码长度至少 8 位"); return; }
      if (pw !== confirm) { setErr("两次输入的密码不一致"); return; }
    }
    setBusy(true);
    try {
      const url = state === "init" ? "/api/auth/init" : "/api/auth/login";
      const body = state === "init" ? { password: pw } : { username: "ARTEX", password: pw };
      const r = await fetch(url, {
        method: "POST", credentials: "include",
        headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
      });
      const text = await r.text();
      if (!r.ok) {
        // 和后端统一错误信封一致：只把那句人话交给界面。
        // 原先直接把 text 抛出去，登录页显示的是 {"error":"用户名或密码错误"} 这串原始 JSON。
        throw new Error(parseErrorBody(text, r) || `HTTP ${r.status}`);
      }
      let tok = "";
      try { tok = (JSON.parse(text) || {}).token || ""; } catch { /* ignore */ }
      if (!tok) throw new Error("服务端未返回令牌");
      storeToken(tok);
      setPassword(""); setConfirm("");
      onAuthed?.();
    } catch (e2) {
      setErr(e2.message || String(e2));
    } finally { setBusy(false); }
  }

  return (
    <Flex align="center" justify="center" style={{ height: "100%", padding: 24 }}>
      <Card size="3" style={{ width: "min(460px, 94vw)" }}>
        <Flex direction="column" gap="4">
          <Flex align="center" gap="2">
            <ShieldCheck size={18} />
            <Heading size="5">artex-harness</Heading>
          </Flex>

          {state === "checking" && <Text size="2" color="gray">正在检查登录状态…</Text>}

          {state === "down" && (
            <Flex direction="column" gap="3">
              <Text size="2" color="red">无法读取登录状态：{detail}</Text>
              <Text size="1" color="gray">
                服务端连不上数据库时不会谎称"尚未设置密码"（那会让下一步覆盖已有密码）。
                请确认 PostgreSQL 可用后重试。
              </Text>
              <Button variant="soft" onClick={probe}>重试</Button>
            </Flex>
          )}

          {(state === "init" || state === "login") && (
            <form onSubmit={submit}>
              <Flex direction="column" gap="3">
                <Text size="2" color="gray">
                  {state === "init"
                    ? "首次使用：设置管理员密码（用户名固定 ARTEX，至少 8 位）。"
                    : "使用管理员密码登录。"}
                </Text>
                {state === "login" && (
                  <TextField.Root value="ARTEX" readOnly aria-label="用户名" />
                )}
                <TextField.Root ref={pwRef} type="password" placeholder="密码" value={password}
                                autoComplete={state === "init" ? "new-password" : "current-password"}
                                onChange={(e) => setPassword(e.target.value)} aria-label="密码">
                  <TextField.Slot><KeyRound size={13} /></TextField.Slot>
                </TextField.Root>
                {state === "init" && (
                  <TextField.Root type="password" placeholder="再输一次" value={confirm}
                                  autoComplete="new-password"
                                  onChange={(e) => setConfirm(e.target.value)} aria-label="确认密码" />
                )}
                {err ? <Text size="1" color="red">{err}</Text> : null}
                <Button type="submit" variant="solid" disabled={busy}>
                  {busy ? "处理中…" : state === "init" ? "设置密码并进入" : "登录"}
                </Button>
                {state === "login" && (
                  <Text size="1" color="gray">
                    忘记密码可在服务器上运行 <Code size="1">./reset-password.sh</Code>。
                  </Text>
                )}
              </Flex>
            </form>
          )}

          <Box style={{ borderTop: "1px solid var(--c-divider)", paddingTop: 10 }}>
            <Text size="1" color="gray">
              仅限已获明确书面授权的系统使用。完整条款见仓库 DISCLAIMER.md。
            </Text>
          </Box>
        </Flex>
      </Card>
    </Flex>
  );
}
