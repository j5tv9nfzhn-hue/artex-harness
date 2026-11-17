/**
 * 统一的鉴权请求入口。
 *
 * 为什么需要它：仓库里同时存在两个 localStorage 键（lib/api.ts 用 `artex_token`，
 * 早期控制台用 `artex.harness.token`），而服务端的 JWT 密钥来自可执行文件同目录的
 * jwt.key —— 换过二进制位置或不同入口各自登录过，就会同时存在两枚「一枚有效、
 * 一枚过期」的令牌。此前前端只挑其中一枚，挑错就整站 401，且错误信息与真实原因
 * （会话过期 vs 没有权限）混在一起。
 *
 * 做法：依次用两枚令牌请求；401 就换下一枚；全部失败才把最后一次 401 交给调用方，
 * 让界面显示真实原因。调用方传进来的 Authorization 头会被忽略（统一由这里决定）。
 */

const KEYS = ["artex_token", "artex.harness.token"];

// tokenCandidates returns every stored token, de-duplicated, in preference order.
export function tokenCandidates() {
  const out = [];
  try {
    for (const k of KEYS) {
      const v = localStorage.getItem(k);
      if (v && !out.includes(v)) out.push(v);
    }
  } catch { /* 隐私模式下 localStorage 可能不可用 */ }
  return out;
}

// authHeaders builds the Authorization header from the SAME key list authedFetch
// tries. Components that cannot use authedFetch (raw fetch, SSE) must call this
// instead of reading a single key: reading only "artex.harness.token" left every
// request unauthenticated for sessions that had logged in through the main key,
// which surfaced as a wall of 401s in exactly the panels that use raw fetch.
export function authHeaders() {
  const [t] = tokenCandidates();
  return t ? { Authorization: "Bearer " + t } : {};
}

export async function authedFetch(url, opts = {}) {
  const { headers: raw = {}, ...rest } = opts;
  const base = {};
  for (const [k, v] of Object.entries(raw)) {
    if (k.toLowerCase() !== "authorization") base[k] = v;
  }
  const toks = tokenCandidates();
  const tries = toks.length ? toks : [null];
  let last = null;
  for (const t of tries) {
    const headers = t ? { ...base, Authorization: "Bearer " + t } : base;
    const r = await fetch(url, { credentials: "include", ...rest, headers });
    if (r.status !== 401) return r;
    last = r;
  }
  return last;
}
