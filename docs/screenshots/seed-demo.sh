#!/usr/bin/env bash
# 生成用于截图的演示数据（可复算）。
#
# 为什么需要它：截图必须从**真实运行的服务**里抓，不能 PS。但这台构建机没有
# LLM 端点（`llm_profiles = 0`，引擎 idle），跑不出一轮真实的 agent 对话。
# 因此这里把「一次已完成的 HARNESS 运行」写进数据库：task + activity +
# harness_revisions + usage 行，全部走真实表结构与真实接口，界面照常从 /api 读，
# 渲染路径没有一处是假的。
#
# 诚实标注：截图里的**内容**（命令、输出、结论）是为演示构造的，不是对任何真实
# 目标的扫描结果；界面本身、接口、布局、字段都是真的。见 docs/17。
#
# 时间线刻意对齐：`compactions` 只统计**同一 revision 内**输入 token 回落 ≥20% 的
# 次数（db/exploration.go `InputTokenSeries`），所以 revision 与 usage 行的
# created_at 必须落在同一轮里，否则计数永远是 0、预算面板的压缩标记不会出现。
#
# 用法：
#   ./seed-demo.sh <dsn> <data-dir>
# 例：
#   ./seed-demo.sh "postgres:///artex_shots?sslmode=disable" /tmp/shotdata
#
# 前提：服务已用同一个 DSN 与 data-dir 启动过至少一次（表已建好）并设过管理密码。
# 灌完数据后**重启服务**：任务列表在内存里，直接写库不会进管理器。
set -euo pipefail

DSN=${1:?用法: seed-demo.sh <dsn> <data-dir>}
DATA=${2:?用法: seed-demo.sh <dsn> <data-dir>}

psql -q "$DSN" -v ON_ERROR_STOP=1 <<'SQL'
-- 清掉上一次的演示数据，保证脚本可重复执行。
DELETE FROM tasks WHERE goal LIKE '%acme-demo.example%';
DELETE FROM conversations WHERE title LIKE '演示%';

WITH e AS (
  INSERT INTO explorations (description, goal, status)
  VALUES (
    '授权书 AUTH-2026-014；范围 acme-demo.example 及其子域、10.20.30.0/24；禁止 DoS 与数据破坏。',
    '对 acme-demo.example 测试环境完成授权范围内的资产盘查与漏洞验证',
    'open')
  RETURNING id
)
-- 置顶：控制台加载后会自动选中列表第一条（App.jsx：list[0]），
-- 置顶保证截到的是这个正在看的任务，而不是更早的历史基线。
INSERT INTO tasks (name, description, goal, exploration_id, status, first_run_at, completed_at, pinned_at)
SELECT
  'acme-demo 授权渗透',
  '授权书 AUTH-2026-014；范围 acme-demo.example 及其子域、10.20.30.0/24；禁止 DoS 与数据破坏。',
  '对 acme-demo.example 测试环境完成授权范围内的资产盘查与漏洞验证',
  e.id, 'done', now() - interval '22 minutes', now() - interval '7 minutes', now()
FROM e;
SQL

TASK_ID=$(psql -q "$DSN" -tAc "SELECT id FROM tasks WHERE goal LIKE '%acme-demo.example%' ORDER BY id DESC LIMIT 1" | tr -d ' \n')
EXP_ID=$(psql -q "$DSN" -tAc "SELECT exploration_id FROM tasks WHERE id = ${TASK_ID}" | tr -d ' \n')
echo "task=${TASK_ID} exploration=${EXP_ID}"

psql -q "$DSN" -v ON_ERROR_STOP=1 -v exp="$EXP_ID" <<'SQL'
-- 轮次边界：AG-UI 的 STEP_STARTED 一行一个 turn。created_at 决定每条活动属于哪一轮
-- （InputTokenSeries 用「revision.created_at <= activity.created_at 的最大 revision」）。
INSERT INTO harness_revisions (task_id, exploration_id, revision, turn_kind, turn_ref, summary, created_at)
SELECT t.id, :exp, v.rev, 'harness', 'run', '会话执行', v.at
FROM tasks t
CROSS JOIN (VALUES
  (1::bigint, now() - interval '20 minutes 45 seconds'),
  (2::bigint, now() - interval '16 minutes 45 seconds'),
  (3::bigint, now() - interval '9 minutes 30 seconds')
) AS v(rev, at)
WHERE t.goal LIKE '%acme-demo.example%'
ON CONFLICT (task_id, revision) DO NOTHING;

-- ── 事件流：thinking / text / 工具调用与结果 / usage ──────────────────
-- summary 只是首行索引，detail 才是正文（ARCHITECTURE.md 的不变量）。
INSERT INTO activity (exploration_id, worker, kind, tool, tool_use_id, is_error, summary, detail, input_tokens, output_tokens, created_at)
VALUES
(:exp, 'harness', 'thinking', NULL, NULL, false,
 '先看态势再动手',
 E'先看态势：graph_overview 会说清已登记的资产、frontier 与未测面。\n不要重复扫已知端口；这一轮先把 10.20.30.0/24 的存活主机与服务版本补齐。',
 NULL, NULL, now() - interval '20 minutes 30 seconds'),

(:exp, 'harness', 'text', NULL, NULL, false,
 '本轮计划：补服务版本 → 看首页 → 验一个可疑路径',
 E'本轮计划三步：\n\n1. 对 10.20.30.11 补服务版本（上一轮只拿到端口，没有版本）\n2. 取 443 的首页与响应头，判断框架与中间件\n3. 对 `/api/v1/orders/` 验一次越权（需要两个账号，已有一个）\n\n范围以 AUTH-2026-014 为准，不碰生产库、不发压测。',
 NULL, NULL, now() - interval '20 minutes 10 seconds'),

(:exp, 'harness', 'tool_use', 'Bash', 'toolu_01demo_scan', false,
 'nmap -sV -Pn -T4 10.20.30.11',
 E'nmap -sV -Pn -T4 -p- --min-rate 800 10.20.30.11',
 NULL, NULL, now() - interval '19 minutes 40 seconds'),

(:exp, 'harness', 'tool_result', 'Bash', 'toolu_01demo_scan', false,
 '端口与服务版本 · 5 个开放端口',
 E'Nmap scan report for 10.20.30.11\nHost is up (0.0041s latency).\n\nPORT     STATE SERVICE  VERSION\n22/tcp   open  ssh      OpenSSH 8.9p1 Ubuntu 3ubuntu0.6 (Ubuntu Linux; protocol 2.0)\n80/tcp   open  http     nginx 1.24.0\n443/tcp  open  ssl/http nginx 1.24.0\n8080/tcp open  http     Jetty 9.4.43.v20210629\n3306/tcp open  mysql    MySQL 8.0.35\n\nService Info: OS: Linux; CPE: cpe:/o:linux:linux_kernel',
 NULL, NULL, now() - interval '19 minutes 35 seconds'),

(:exp, 'harness', 'thinking', NULL, NULL, false,
 '8080 上的 Jetty 与 443 的 nginx 是两套栈',
 E'8080 上是 Jetty 9.4.43 —— 这个版本在 2021 年之后有过若干反序列化与路径穿越问题，值得单独看。\n先把 443 的首页与响应头取回来确认前端框架，再决定要不要深挖 8080。',
 NULL, NULL, now() - interval '18 minutes 50 seconds'),

(:exp, 'harness', 'tool_use', 'Bash', 'toolu_01demo_curl', false,
 'curl -sSI https://acme-demo.example/',
 E'curl -sS -k -D- -o /tmp/home.html https://acme-demo.example/ | head -30',
 NULL, NULL, now() - interval '18 minutes 30 seconds'),

(:exp, 'harness', 'tool_result', 'Bash', 'toolu_01demo_curl', false,
 'HTTP 200 · nginx/1.24.0 · 会话走 sid Cookie',
 E'HTTP/1.1 200 OK\nServer: nginx/1.24.0\nDate: Tue, 17 Nov 2026 06:41:12 GMT\nContent-Type: text/html; charset=utf-8\nConnection: keep-alive\nX-Frame-Options: SAMEORIGIN\nStrict-Transport-Security: max-age=31536000\nSet-Cookie: sid=8f21c4d0; Path=/; HttpOnly\n\n<title>Acme Shop · 管理后台</title>',
 NULL, NULL, now() - interval '18 minutes 20 seconds'),

(:exp, 'harness', 'text', NULL, NULL, false,
 '已确认：管理后台、nginx 前置、会话只有 HttpOnly',
 E'首页是**管理后台**（不是商城前台），前面挡的是 nginx 1.24.0，会话靠 `sid` Cookie —— 只有 `HttpOnly`，**没有 SameSite**。\n\n下一步验 `/api/v1/orders/` 的横向越权。这条路径上一轮只在未登录状态下拿到 401，没有验证登录后换账号读别人的订单。',
 NULL, NULL, now() - interval '18 minutes'),

(:exp, 'harness', 'tool_use', 'Write', 'toolu_01demo_write', false,
 '写越权验证脚本到工作区',
 E'Write scripts/orders-idor.sh',
 NULL, NULL, now() - interval '16 minutes 20 seconds'),

(:exp, 'harness', 'lifecycle', NULL, NULL, false,
 '本轮结束，等待人工指示（发送消息即继续）',
 NULL, NULL, NULL, now() - interval '7 minutes'),

-- 第 1 轮两次模型调用
(:exp, 'harness', 'usage', NULL, NULL, false, NULL, NULL, 18240, 412, now() - interval '20 minutes 20 seconds'),
(:exp, 'harness', 'usage', NULL, NULL, false, NULL, NULL, 22115, 508, now() - interval '19 minutes 10 seconds'),
-- 第 2 轮：26890 → 9800 是同一轮内的一次压缩（回落 63%），再涨回 13420
(:exp, 'harness', 'usage', NULL, NULL, false, NULL, NULL, 26890, 604, now() - interval '16 minutes 10 seconds'),
(:exp, 'harness', 'usage', NULL, NULL, false, NULL, NULL,  9800, 318, now() - interval '15 minutes 20 seconds'),
(:exp, 'harness', 'usage', NULL, NULL, false, NULL, NULL, 13420, 402, now() - interval '14 minutes 40 seconds'),
-- 第 3 轮：两轮对话之后上下文又长回去
(:exp, 'harness', 'usage', NULL, NULL, false, NULL, NULL, 17960, 466, now() - interval '8 minutes 40 seconds'),
(:exp, 'harness', 'usage', NULL, NULL, false, NULL, NULL, 21410, 520, now() - interval '7 minutes 30 seconds');
SQL

# ── 子代理：Agent 工具的 tool_use / tool_result 配对 ────────────────────
# 侧栏「子代理」读的是 /api/harness/workers：它把 tool='Agent' 的
# tool_use 与随后的 tool_result 配成一行（server/harness_files.go
# `parseAgentSummary` 解析 summary 的固定前缀）。所以只写委派 journal 不够，
# 事件流里必须有这一对，否则面板是空态。
psql -q "$DSN" -v ON_ERROR_STOP=1 -v exp="$EXP_ID" <<'SQL'
INSERT INTO activity (exploration_id, worker, kind, tool, tool_use_id, is_error, summary, detail, metadata, created_at)
VALUES
(:exp, 'harness', 'tool_use', 'Agent', 'toolu_01demo_agent1', false,
 '后台派发子代理 recon (#d1)：枚举 10.20.30.0/24 的存活主机与服务版本，不要触发任何写操作。',
 '{"agent_type":"recon","prompt":"枚举 10.20.30.0/24 的存活主机与服务版本，不要触发任何写操作。","run_in_background":true}',
 jsonb_build_object('delegation_id', 'd1', 'background', true),
 now() - interval '18 minutes 10 seconds'),

(:exp, 'harness', 'tool_result', 'Agent', 'toolu_01demo_agent1', false,
 '子代理 d1 返回：10.20.30.11  22/80/443/8080/3306  nginx 1.24.0 / Jetty 9.4.43 ｜ 10.20.30.12  22/443/8443  nginx 1.18.0 ｜ 10.20.30.14  5432/22  postgres 14.1',
 E'10.20.30.11  22/80/443/8080/3306  nginx 1.24.0 / Jetty 9.4.43\n10.20.30.12  22/443/8443  nginx 1.18.0\n10.20.30.14  5432/22  postgres 14.1',
 jsonb_build_object('delegation_id', 'd1', 'background', true),
 now() - interval '14 minutes'),

(:exp, 'harness', 'tool_use', 'Agent', 'toolu_01demo_agent2', false,
 '后台派发子代理 verify (#d2)：对 /api/v1/orders/ 做一次未登录访问，确认是否为 401。',
 '{"agent_type":"verify","prompt":"对 /api/v1/orders/ 做一次未登录访问，确认是否为 401。","run_in_background":true}',
 jsonb_build_object('delegation_id', 'd2', 'background', true),
 now() - interval '12 minutes'),

(:exp, 'harness', 'tool_result', 'Agent', 'toolu_01demo_agent2', true,
 '子代理 d2 返回：子代理 verify 超时（10m0s）：context deadline exceeded',
 E'Error: subagent failed: 子代理 verify 超时（10m0s）：context deadline exceeded',
 jsonb_build_object('delegation_id', 'd2', 'background', true),
 now() - interval '11 minutes');
SQL

# ── 一条等待处理的审批（事件流里的就地卡片 + 侧栏徽标）───────────────────
# psql 对 INSERT ... RETURNING 会同时输出返回行与命令标签（"INSERT 0 1"），
# 用 -q 关掉标签，否则标签会被拼进 :pid 变量里。
PENDING_ID=$(psql -q "$DSN" -tAc "
  INSERT INTO intercept_pending (task_id, agent_name, tool_name, tool_input, status, reason, created_at)
  VALUES ('${TASK_ID}', 'harness', 'Write',
          '{\"file_path\":\"/tmp/acme-shots/orders-idor.sh\",\"content\":\"#!/usr/bin/env bash\\n# 用两个账号的 Cookie 交叉请求同一订单号\\n\"}'::jsonb,
          'pending', '规则命中：工作区外的写入需要人确认', now() - interval '16 minutes 15 seconds')
  RETURNING id" | tr -d ' \n')
echo "pending=${PENDING_ID}"

psql -q "$DSN" -v ON_ERROR_STOP=1 -v exp="$EXP_ID" -v pid="$PENDING_ID" <<'SQL'
INSERT INTO activity (exploration_id, worker, kind, tool, tool_use_id, is_error, summary, detail, metadata, created_at)
VALUES (:exp, 'harness', 'intercept_request', 'Write', 'toolu_01demo_write', false,
 '需要审批：Write #' || :pid,
 '{"pending_id":' || :pid || ',"tool":"Write","input":{"file_path":"/tmp/acme-shots/orders-idor.sh"}}',
 jsonb_build_object('pending_id', :pid::bigint, 'tool', 'Write',
                    'input', '{"file_path":"/tmp/acme-shots/orders-idor.sh"}'),
 now() - interval '16 minutes 15 seconds');
SQL

# ── 一个历史任务：只为让「用量」的日历热力格有东西（它按 kind='result' 聚合）──
# 放在独立任务里，避免往当前运行的流里塞一堆历史正文。
psql -q "$DSN" -v ON_ERROR_STOP=1 <<'SQL'
WITH e AS (
  INSERT INTO explorations (description, goal, status)
  VALUES ('历史基线（演示数据）：前几天的三次短跑，用于展示用量热力格。',
          '历史基线：acme-demo.example 的前期摸底', 'achieved')
  RETURNING id
)
INSERT INTO tasks (name, description, goal, exploration_id, status, first_run_at, completed_at, created_at)
SELECT 'acme-demo 历史基线',
       '历史基线（演示数据）：前几天的三次短跑，用于展示用量热力格。',
       '历史基线：acme-demo.example 的前期摸底',
       e.id, 'done', now() - interval '6 days', now() - interval '1 day', now() - interval '6 days'
FROM e;

INSERT INTO activity (exploration_id, worker, kind, summary, detail, input_tokens, output_tokens, created_at)
SELECT t.exploration_id, 'harness', 'result', v.sum, v.det, v.i, v.o, v.at
FROM tasks t,
(VALUES
  ('摸底：存活主机与服务面', '10.20.30.0/24 存活 3 台；80/443 同栈，8080 独立。', 41200, 1180, now() - interval '6 days'),
  ('摸底：目录与路径',       '后台入口 /login、/api/v1；上传接口 /api/v1/upload。', 38600, 940,  now() - interval '4 days'),
  ('摸底：账号与权限',       '两个测试账号；订单接口按 id 直取，未见归属校验。',   52100, 1620, now() - interval '2 days'),
  ('收口：三条待验结论',     '整理为三条待验：Jetty 独立栈、Cookie 缺 SameSite、订单横向越权。', 47800, 1400, now() - interval '1 day')
) AS v(sum, det, i, o, at)
WHERE t.goal LIKE '%历史基线%';
SQL

# ── 项目目录 + 基线（让「工作台 → 文件 / DIFF」有真实内容）───────────────
PROJ="${DATA}/projects/acme-demo"
BASE="${DATA}/baselines/${TASK_ID}"
rm -rf "$PROJ" "$BASE"
mkdir -p "$PROJ/scripts" "$PROJ/evidence" "$BASE/scripts" "$BASE/evidence"

cat > "$PROJ/notes.md" <<'EOF'
# acme-demo.example · 运行笔记

- 授权：AUTH-2026-014（书面），范围含子域与 10.20.30.0/24
- 禁止：DoS / 压测 / 对生产库写入
- 已确认：管理后台在 443，nginx 1.24.0 前置，8080 是 Jetty 9.4.43
- 待验：`/api/v1/orders/` 登录后横向越权
EOF
cp "$PROJ/notes.md" "$BASE/notes.md"

cat > "$PROJ/evidence/scan-10.20.30.11.txt" <<'EOF'
22/tcp   open  ssh      OpenSSH 8.9p1 Ubuntu 3ubuntu0.6
80/tcp   open  http     nginx 1.24.0
443/tcp  open  ssl/http nginx 1.24.0
8080/tcp open  http     Jetty 9.4.43.v20210629
3306/tcp open  mysql    MySQL 8.0.35
EOF
cp "$PROJ/evidence/scan-10.20.30.11.txt" "$BASE/evidence/scan-10.20.30.11.txt"

# 基线里先放一版空脚本，之后改动它 → DIFF 里是 M
printf '#!/usr/bin/env bash\nset -euo pipefail\n\n# TODO: 补越权验证\n' > "$PROJ/scripts/orders-idor.sh"
cp "$PROJ/scripts/orders-idor.sh" "$BASE/scripts/orders-idor.sh"

# 基线之后新增的文件 → DIFF 里是 A
cat > "$PROJ/scripts/fingerprint.sh" <<'EOF'
#!/usr/bin/env bash
# 取首页与响应头，判断框架与中间件
set -euo pipefail
curl -sS -k -D- -o /tmp/home.html "https://acme-demo.example/" | head -30
grep -o '<title>[^<]*</title>' /tmp/home.html || true
EOF

# 改写基线里的脚本 → DIFF 里是 M（含新增与删除行）
cat > "$PROJ/scripts/orders-idor.sh" <<'EOF'
#!/usr/bin/env bash
# 用两个账号的 Cookie 交叉请求同一订单号，确认是否存在横向越权。
set -euo pipefail

BASE_URL="https://acme-demo.example"
A_SID=${A_SID:?需要账号 A 的 sid}
B_SID=${B_SID:?需要账号 B 的 sid}
ORDER=${ORDER:-10086}

code_a=$(curl -sS -k -o /tmp/a.json -w '%{http_code}' -H "Cookie: sid=${A_SID}" "${BASE_URL}/api/v1/orders/${ORDER}")
code_b=$(curl -sS -k -o /tmp/b.json -w '%{http_code}' -H "Cookie: sid=${B_SID}" "${BASE_URL}/api/v1/orders/${ORDER}")

echo "A -> ${code_a}  B -> ${code_b}"
if [ "$code_b" = "200" ] && cmp -s /tmp/a.json /tmp/b.json; then
  echo "越权成立：B 读到了 A 的订单，且响应体一致"
fi
EOF

echo "project-dir=${PROJ}"

# ── 子代理（委派 journal：重启后仍可审计，本仓库的持久化格式）────────────
DELEG="${DATA}/delegations/${TASK_ID}"
mkdir -p "${DELEG}/d1"
printf '10.20.30.11  22/80/443/8080/3306  nginx 1.24.0 / Jetty 9.4.43\n10.20.30.12  22/443/8443        nginx 1.18.0\n10.20.30.14  5432/22             postgres 14.1\n' \
  > "${DELEG}/d1/recon-hosts.txt"

python3 - "$DELEG" <<'PY'
import json, sys, pathlib, datetime
d = pathlib.Path(sys.argv[1])
now = datetime.datetime.now(datetime.timezone.utc)
def ts(mins):
    return (now - datetime.timedelta(minutes=mins)).isoformat().replace("+00:00", "Z")
recs = [
  {"id": "d1", "agent_type": "recon",
   "prompt": "枚举 10.20.30.0/24 的存活主机与服务版本，不要触发任何写操作。",
   "description": "网段存活与版本", "status": "done", "background": True,
   "started_at": ts(18), "ended_at": ts(14), "duration_ms": 241000,
   "output_path": str(d / "d1" / "recon-hosts.txt"),
   "result": "10.20.30.11  22/80/443/8080/3306  nginx 1.24.0 / Jetty 9.4.43\n10.20.30.12  22/443/8443  nginx 1.18.0\n10.20.30.14  5432/22  postgres 14.1",
   "work_dir": str(d / "d1")},
  {"id": "d2", "agent_type": "verify",
   "prompt": "对 /api/v1/orders/ 做一次未登录访问，确认是否为 401。",
   "description": "未登录基线", "status": "failed", "background": True,
   "started_at": ts(12), "ended_at": ts(11), "duration_ms": 58000,
   "error": "子代理 verify 超时（10m0s）：context deadline exceeded",
   "work_dir": ""},
]
with open(d / "delegations.jsonl", "w") as f:
    for r in recs:
        f.write(json.dumps(r, ensure_ascii=False) + "\n")
print("delegations journal:", d / "delegations.jsonl")
PY

# ── 一个对话（无 goal 的那条产品线）─────────────────────────────────────
CONV_ID=$(psql -q "$DSN" -tAc "
  INSERT INTO conversations (agent_key, title) VALUES ('chat', '演示：把结论收敛成三条')
  RETURNING id" | tr -d ' \n')
psql -q "$DSN" -v ON_ERROR_STOP=1 -v conv="$CONV_ID" <<'SQL'
INSERT INTO conversation_activities (conversation_id, worker, kind, summary, detail, created_at) VALUES
(:conv, 'chat', 'user', '把当前结论收敛成三条，每条带证据',
 '把当前结论收敛成三条，每条带证据', now() - interval '6 minutes'),
(:conv, 'chat', 'result', '三条结论',
 E'1. **8080 上的 Jetty 9.4.43 是独立栈**，与 443 的 nginx 不是同一套应用——证据：服务版本指针与 443 首页 `title` 不一致。\n2. **管理后台的 `sid` 缺 SameSite**：只有 `HttpOnly`，未见 SameSite；改密接口需要人工复核是否存在 CSRF。\n3. **未登录 401 不等于没有越权**：上一轮只测了未登录，登录后换账号读订单的横向越权还没验证（脚本已写，等待审批）。',
 now() - interval '6 minutes');
SQL

echo "conversation=${CONV_ID}"
echo "完成。重启服务后，用 task=${TASK_ID} conv=${CONV_ID} 打开控制台截图。"
