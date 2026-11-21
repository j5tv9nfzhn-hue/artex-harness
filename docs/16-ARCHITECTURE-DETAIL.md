# 16 · 架构：模块划分、依赖关系、数据流

> 这一页回答三个问题：**代码分成哪些块、谁依赖谁、一次请求的数据怎么流动**。
> 设计取舍的来龙去脉在 `docs/03-ARCHITECTURE.md` 与 `docs/adr/`，这里是地图。
> 快速上手看 `ARCHITECTURE.md`（根目录），本文是它的展开版。
>
> **配套**：功能的行为 / 入口 / 使用方式 → [`15-FEATURES.md`](15-FEATURES.md)；
> 参考界面截图逐项对照 → [`17-REFERENCE-UI-MAPPING.md`](17-REFERENCE-UI-MAPPING.md)；
> 控制台真实截图（含复算脚本）→ [`screenshots/README.md`](screenshots/README.md)。

---

## 一、进程与部署形态

**一个进程，两种监听，一个静态资源根。**

```
                     ┌─────────────────────────────────────┐
  浏览器 ──HTTP─────▶ │ /app/      内嵌控制台（go:embed）      │
                     │ /api/*     JSON API（105 条路由）      │
                     │ /api/agui/agent, /api/harness/stream  │ SSE
                     └──────────────┬──────────────────────┘
                                    │
                     ┌──────────────▼──────────────────────┐
   目标站点 ─mitm──▶  │ -proxy :8798  流量记录代理（可关）     │
                     └──────────────┬──────────────────────┘
                                    │
        ┌───────────────────────────┼───────────────────────────┐
        ▼                           ▼                           ▼
  PostgreSQL 14+            <data>/ 磁盘                 外部子进程
  （任务/活动/图谱/          （工作区/transcript/          （Bash / MCP server
    审批/通知/发现）            截图/证据 blob）              / npx playwright）
```

- 启动：`artex -addr :8797 -data ./data -proxy 127.0.0.1:8798`
- 单二进制：控制台产物 `server/console-app/` 被 `go:embed` 打进二进制，目标机不需要 Node。
- 数据库：`ARTEX_PG_DSN` 环境变量；首次启动自动建表（`db/schema.sql`，51 张表）。

## 二、模块划分

| 目录 | 行数 / 文件（含测试） | 职责 | 不该出现在这里 |
|---|---|---|---|
| `cmd/artex/` | 0.2k / 1 | 进程入口：flag、信号、自更新引导、优雅关停 | 业务逻辑 |
| `server/` | 41k / 136 文件 | HTTP 路由、JWT 鉴权、DTO、SSE 广播、引擎调度、HARNESS 观测面 | SQL 细节、提示词正文 |
| `db/` | 32k / 103 文件 | `schema.sql` + 各领域 store（资产/探索图/活动/发现/审批/通知/委派） | HTTP 概念 |
| `agent/` | 12k / 62 文件 | LLM 接入与运行编排：provider、提示词、压缩钩子、工具装配 | 路由、鉴权 |
| `harness/` | 6.2k / 32 文件 | 运行期能力包（见下表） | 数据库 schema |
| `intercept/` + `guard/` | 1.8k / 5 文件 | 审批门：规则编译、LLM judge、pending 决议、hook 拦截与审计 | 界面 |
| `traffic/` | 4.2k / 3 文件 | mitmproxy 流量记录与取证 | 业务决策 |
| `notify/` | 5.1k / 15 文件 | 通知渠道与投递重试 | 任务逻辑 |
| `selfupdate/` | 1.6k / 4 文件 | 自更新与回滚（退出码是协议的一部分） | — |
| `llmpool/` `llmrec/` `sidequestion/` `evidence/` `mcphttp/` | 各 0.7–1.4k | 多模型故障转移 / LLM 原文录制 / 旁路问答 / 证据绑定 / MCP 客户端 | — |
| `web/` | 6.8k / 21 文件 | React 19 + Vite 控制台，产物写 `server/console-app/` | 业务规则 |
| `third_party/norma/` | 37.6k / 103 文件 | vendored agent SDK（本地打过补丁） | 本项目代码 |
| `eval/` `evidence/` | — | 评测与取证（`harness-smoke` / `harness-gate` CLI） | 运行时代码 |

> 口径说明：上表行数**含 `_test.go`**（`server` 41k 里约 10.7k 是测试；只看非测试代码是 30.5k / 77 文件）。
> 之所以按含测试的口径给数：它反映"改一处要连带看懂多少代码"。文件数同理。

### `harness/` 内部的包

| 包 | 职责 |
|---|---|
| `protocol` | Item / Turn / Thread 三概念 + 事件信封（带 `revision`），SSE 的 schema |
| `browser` | 浏览器会话 Manager（一任务一会话、崩溃重建、标签、GC）+ `rawclient`（截图直取） |
| `delegation` | 子代理委派的进程内权威注册表 + journal 持久化 + 隔离目录 TTL |
| `checkpoint` | 每轮文件快照与回滚 |
| `diff` | 与基线对比的 unified diff |
| `capability` | 把 5 类供给面统一成"能力"对象（`exec` / `concurrency_safe` / `deferred` 是事实描述） |
| `permission` | 权限授予模型 |
| `replay` | 按 `rev` 区间重建一次运行（只读投影） |
| `eval` | 行为指纹（`Steps` / `Tools` / `TurnKinds` / `Hash`） |
| `consistency` | 双跑对照，输出硬/软检查表 |
| `trajectory` | 回放历史的按轮次行模型 |

**`capability` 的 surface 取值**（能力页直接呈现这些）：`builtin`、`custom-command`、
`custom-script`、`custom-http`、`cli-wrapper`、`mcp`、`skill`。

## 三、依赖关系

### 3.1 内部包的依赖（实测 import 计数）

```
                 ┌────────┐
                 │  cmd   │
                 └───┬────┘
        ┌────────────┼────────────┐
        ▼            ▼            ▼
   ┌────────┐   ┌────────┐   ┌──────────┐
   │server  │   │ agent  │   │selfupdate│
   └───┬────┘   └───┬────┘   └──────────┘
       │            │
       │  ┌─────────┴──────────┬─────────────┐
       │  ▼                    ▼             ▼
       │ guard ──▶ intercept   harness/*    evidence
       │            │             │
       ▼            ▼             ▼
   ┌────────────────────────────────────┐
   │              db                    │──────▶ config
   └────────────────────────────────────┘
              │
              ▼
        ┌──────────┐
        │  notify  │  (db 反向依赖 notify：投递记录的读写)
        └──────────┘
```

**关键事实（都从 import 里数出来的，不是画的）：**

- `server` 依赖 `db`(85) > `agent`(39) > `intercept`(13) > `traffic`(8) > `harness/protocol`(7)。
- `agent` 依赖 `db`(27) > `intercept`(7) > `sidequestion`/`llmrec`/`guard`(各 3) > `harness/delegation`(2)。
- `guard` **只**依赖 `intercept` + `db`：它是 hook 的唯一否决点，不碰 HTTP。
- `harness/*` 只依赖 `db`(3)，**不反向依赖** `server`/`agent`——所以这些包可以单独测。
- `db` 反向依赖 `notify`(3)：投递记录的种类由通知层定义，避免两处各写一份枚举。
- `llmpool` / `notify` / `selfupdate` / `traffic` 之间无互相依赖，是叶节点。
- **没有循环依赖**；`db` 是唯一的共享底座。

### 3.2 外部依赖（`go.mod` 直接依赖）

| 依赖 | 用途 |
|---|---|
| `Autumn-27/norma` | agent 内核（`replace` 到 `third_party/norma`，本地补丁：Windows shell 探测 + 覆盖钩子） |
| `jackc/pgx/v5` | PostgreSQL 驱动 |
| `golang-jwt/v5` | 鉴权令牌 |
| `go-mitmproxy` | 流量记录代理 |
| `projectdiscovery/dnsx` + `miekg/dns` | 资产解析 |
| `modernc.org/sqlite` | 旧 ARTEX 存储（未删） |
| `google/uuid`, `klauspost/compress`, `golang.org/x/*` | 工具类 |

### 3.3 前端依赖（刻意只有 9 个）

`react` / `react-dom` / `lucide-react`（图标）/ `motion`（过渡）/ `react-markdown` + `remark-gfm` /
`@tanstack/react-virtual`（虚拟化）/ `date-fns`（相对时间）/ `vite`（构建）。

**视觉只有一个来源**：`web/src/tokens.css`。`ui.jsx` 是手写原语层，只读令牌。
通知、命令面板、折线图都是仓库内实现（`toast.jsx` / `Palette.jsx` / `Sparkline.jsx`）——
上一版并存的 sonner / cmdk / recharts / highlight.js 主题已移除，它们各自带一套设计系统。

## 四、数据流

### 4.1 一次发言（对话）

```
浏览器  POST /api/conversations/{id}/messages
   │
   ▼
server/conversations.go ──▶ Engine.Run（起一轮 agent）
   │
   ▼
agent/capture.go: captureRunSession
   │  · 每产生一个事件调用 emit(db.Activity)
   │  · intercept.WithTrace 记录审批上下文
   ▼
server/engine.go: emitActivity
   │  ① appendActivity → t.Store.AppendActivity（失败重试 3 次，仍失败则**显式记日志**并不静默吞）
   │  ② Broadcaster.Publish（SSE 扇出）
   ▼
db: activity 表（summary = firstLine(200/400)，detail = 完整正文，metadata = 结构化小字段）
   │
   ▼
前端轮询 GET /api/conversations/{id}/messages?since=N
```

**关键约定**：`activity.summary` **只是首行索引**，完整正文在 `detail`。任何展示正文的地方
都必须取 `detail`，否则用户看到的是被砍掉第一行之后的内容（这是 `ARCHITECTURE.md` 写明的不变量）。

### 4.2 一次运行（task，HARNESS 单 agent）

```
浏览器  POST /api/tasks {goal, harness_mode:true}
   │
   ▼
server/manager.go: createTask ──▶ tasks 表（status=created）
   │
   ▼ engine.Run(t)  每任务一个 goroutine
server/harness_loop.go: harnessLoop
   │  循环：取 inbox 消息 → beginTaskOperation → 置 running → OpenTurn
   │
   ▼
agent/harness.go: HarnessAgent.Run
   │  · domainTools（图谱工具）+ defaultToolsExcept（Bash/Read/Write/Edit…）+ Agent 工具
   │  · 浏览器工具来自 harness/browser.Manager（一任务一会话）
   │  · opts.Hooks = intercept/guard 的 HookRunner ← **唯一否决点**
   │  · opts.MaxDuration = 墙钟预算；settlement 按原因给不同收尾提示
   ▼
agentcore.Session（norma）──▶ LLM Provider（流式）
   │  每个事件 → emit → engine.emitActivity → db + Broadcaster
   ▼
一轮结束：若无待处理消息 → 置 done 并写 lifecycle 活动，**停在 inbox 上**（不 return）
```

> **为什么停在 inbox 而不 return**：return 会让 `started` 保持置位而没有 goroutine 读队列，
> 下一条 `Say()` / 后台委派唤醒会把消息"接受但永不执行"。停泊则一条 goroutine 对应一个空闲任务。

### 4.3 事件流（SSE / AG-UI）

```
activity 表 ──▶ server/agui.go: 投影成 AG-UI 事件
                     │  未知 kind 一律保留，不丢行
                     ▼
             GET /api/agui/agent?task=N&since=<seq>&follow=1
                     │  · since = 断点续传游标
                     │  · 15s 心跳
                     ▼
             前端：按 rev seq-merge，断线自动重连，不重放、不丢事件
```

### 4.4 审批门（唯一的否决点）

```
agent 要调工具
   │
   ▼ norma 触发 PreToolUse hook
guard/guard.go: preToolUse
   │
   ▼ intercept.Interceptor.Match(tool, input)
compile 后的规则 → {allow | ask | deny}
   │
   ├─ allow → 放行
   ├─ deny  → 拦截，理由回给模型（琥珀色「平台拦截」，≠ 目标失败）
   └─ ask   → 写 intercept_pending + 发 INTERCEPT_REQUEST 活动
                 │
                 ├─ 前端事件流内卡片 → POST /api/intercept/pending/{id}/decide
                 │     （前端词汇 allow/deny → 后端 allowed/denied）
                 └─ 无人在线 → LLM judge 兜底（失败动作/等待超时动作可配）
```

**不变量**：`HarnessAgent.SetHooks` 挂的 `HookRunner` 是唯一否决点；**子代理继承同一份 hooks**，
否则等于没有 gate。前端只是这个门的入口，不是门本身。

### 4.5 子代理委派

```
模型调 Agent 工具 {agent_type, prompt, run_in_background}
   │
   ▼ agent/harness.go: agentTool
   ├─ 同步：直接在当轮内跑完，返回结果
   └─ 后台：注册到 harness/delegation.Registry → 立即返回 id
                │
                ├─ 隔离目录 <data>/delegations/<taskID>/<did>/（不污染任务工作区/DIFF/检查点）
                ├─ ctx = context.WithoutCancel(parent) + 自有 10m 预算（父轮次结束不杀它）
                ├─ 工具白名单不含 Agent → 无法递归
                └─ 完成 → 写内容寻址结果文件 → 配对 tool_result
                          → 投递 <task-notification> 到父会话
                          → 父会话空闲则**自动唤醒**（投递成功才 DrainNotifications）
```

**持久化边界**：记录追加到 `delegations.jsonl`，重启后仍可审计；但重启前 `running` 的记录
一律改 `failed` 并写明原因（没有进程能再完成它），历史委派**不能再 stop**。

### 4.6 浏览器与截图

```
agent 调 mcp__browser__* 工具
   │
   ▼ harness/browser.Manager.Ensure(taskID)
   ├─ Playwright（stdio, npx @playwright/mcp）   ┐ 同一会话条目
   └─ chrome-devtools（stdio）                    ┘ → F12 连的是同一上下文
        │  某服务器失败 → MarkBroken → 下次 Ensure 只重建它
        ▼
   截图：SDK 的 content union 没有 image 类型
        → rawclient.go 用最小 stdio JSON-RPC 直取 PNG
        → 内容寻址存 <data>/browser/<sha256>.png
        → 工具结果里留 "[thumb] <hash>"
        → 前端 <img src="/api/harness/shot/<hash>">（immutable 缓存，TTL 24h/上限 2000）
```

### 4.7 一次读请求（典型）

```
GET /api/harness/files/{id}
   ▼ 鉴权（JWT）→ ResolveTask
   ▼ 工作区根 = project_dir（绑定后）或 <data>/tasks/<id>
   ▼ 遍历 + 变更标记（对比 <data>/baselines/<taskID> 或 <data>/checkpoints/<taskID>）
   ▼ safeJoin 防路径穿越，1 MiB 上限，二进制拒绝
   ▼ DTO（json tag）→ 前端
```

## 五、数据库与磁盘

### 5.1 表的分区（`db/schema.sql`，51 张 `CREATE TABLE`）

| 组 | 表 |
|---|---|
| 产品实体 | `tasks`、`conversations`、`conversation_activities`、`task_categories`、`task_templates`、`task_archives`、`task_relations` |
| 探索图谱 | `explorations`、`exploration_nodes`、`exploration_edges`、`exploration_anchors`、`activity` |
| 资产 | `companies`、`assets`、`company_scope`、`task_scope`、`task_asset_links` |
| 发现与取证 | `findings`、`finding_retests`、`traffic_evidence_snapshots`、`finding_traffic_bindings` |
| 审批 | `intercept_rules`、`intercept_pending`、`asset_intercept_rules`、`task_intercept_rules` |
| 能力与配置 | `llm_profiles`、`llm_profile_health`、`task_llm_profiles`、`agents`、`agent_prompts`、`agent_prompt_vars`、`tools`、`mcp_servers`、`mcp_tools_cache`、`skill_usage`、`tool_usage`、`settings` |
| 观测 | `harness_revisions`、`harness_turn_items`、`server_logs`、`scheduler_state` |
| 通知 | `notification_channels`、`notification_events`、`notification_deliveries` |

> 迁移约定：`ALTER ... IF NOT EXISTS` **追加在 `schema.sql` 尾部**，不改历史语句。

### 5.2 磁盘

```
<data>/
├── tasks/<id>/                 任务自有工作区（未绑定项目目录时）
├── baselines/<taskID>/         绑定项目目录后的基线（不写进用户目录）
├── checkpoints/<taskID>/       每轮文件快照
├── transcripts/<session>.jsonl transcript（子代理在 <session>/subagents）
├── delegations/<taskID>/
│   ├── <did>/                  后台委派的隔离目录（TTL 回收）
│   ├── <hash>.txt              内容寻址的结果正文（永不删除）
│   └── delegations.jsonl       委派 journal（重启恢复审计）
├── browser/<sha256>.png        浏览器截图（内容寻址）
├── evidence/blobs/<ab>/<hash>.bin  流量证据 blob
└── traffic/                    mitmproxy 记录
```

## 六、进程内状态（重启即失，已知边界）

| 状态 | 位置 | 重启后 |
|---|---|---|
| 任务运行时 | `Engine` 的 `taskRuntime` map | 丢失；启动时按存活任务重建 |
| 委派注册表 | `delegation.Global` | 从 journal 恢复为**终态**，不能 stop |
| 浏览器会话 | `browser.Manager` | 丢失，下次工具调用重建 |
| 项目目录占用 | `projectGuard` | 启动时按 `tasks.project_dir` 重建绑定 |
| 事件流游标 | 客户端 `since` | 服务端不存，客户端自己续传 |
| SSE 订阅者 | `Broadcaster` | 丢失，前端自动重连 |

## 七、并发与生命周期要点

| 关注点 | 做法 |
|---|---|
| 一任务一轮 | `beginTaskOperation` 闸门；并发闸门限制同时活跃的 LLM 调用数 |
| 停泊而非退出 | `harnessLoop` 一轮结束后停在 inbox 上，`Say`/委派唤醒可直接续跑 |
| 暂停语义 | 收尾在跑的轮次再停（重启前不腰斩回复）；`BeginDelete`/`AbortDelete` 是删除屏障 |
| 活动写入 | 重试 3 次；仍失败写**显式错误日志**（含丢弃计数、FK 诊断），不静默吞 |
| 图读取 | `graph_overview` 单快照读（`REPEATABLE READ`），并用 `snapMu` 串行化——否则两个消费者会互相覆盖共享事务字段 |
| 子代理预算 | `context.WithoutCancel(parent)` + 自有超时；父轮次取消只拒绝**新开**子代理 |
| 工具并发 | 流式执行器按 `IsConcurrencySafe` 并行安全工具，非安全工具互为屏障 |

## 八、构建与产物

```bash
make web        # cd web && npm ci && npm run build → server/console-app/
make build      # go build -o artex ./cmd/artex（用仓库里已有的产物）
make build-all  # 两者
make vet        # go vet ./...
make test       # 单元测试（不碰数据库）：agent/harness/config/guard
ARTEX_TEST_DB=<库名> make test-db   # 需要 PG 的测试；会重建 schema，必须显式指名
```

- `server/console-app/` 是**被提交进仓库的构建产物**：`go:embed` 需要它在编译时存在，
  这样纯 Go 构建不需要 node_modules。**改前端后必须 `make web` 再提交**，否则二进制里是旧界面。
- `web/node_modules/`、`data-harness/`、`harness-jwt.key`、`*.log`、编译出的 `artex-harness` 一律不进版本库。
- CI 四段式：gate（判定构建什么、要不要发布）→ plan（校验三端六包）→ job（前端产物 + 六包编译）
  → release（仅显式 tag 时）。见 `.github/CI.md`。

## 九、技术债与已知缺陷（诚实清单）

| 项 | 现状 |
|---|---|
| 巨型文件 | `server/server.go` 4172 行、`db/exploration.go` 2195 行、`server/manager.go` 1902 行 |
| 无 Go 测试的包 | `report`、`enrich`、`skills`、`eval` |
| F12 network 面板 | 后端未建模请求/响应表，界面明确写"不在此版本" |
| 无任务级权限开关 | 后端没有这个能力，界面禁用并写明 |
| 跨进程停止委派 | 需要另一个 ADR |
| 旧 planner/worker 管线 | 仍保留（ADR-0004「升级不改既有行为」），控制台默认不展示 |