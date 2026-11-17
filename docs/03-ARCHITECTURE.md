# 03 · artex-harness 架构与引擎设计

本文回答三件事：**三层怎么选**、**引擎怎么设计**、**怎么保证它是 harness 而不是工具堆**。
所有设计服从业主已确定的 5 项口径（[ADR-0001](adr/0001-owner-decisions.md)、
[ADR-0003](adr/0003-capability-boundary.md)）。

---

## 一、设计原则

从 [02 调研](02-HARNESS-RESEARCH.md) 收敛出 5 条，作为后续所有技术选择的判据：

| # | 原则 | 依据 |
|---|---|---|
| P1 | **能力是插件，不是内核**：harness 不实现扫描/利用逻辑 | Codex "build on the open agent harness" 的职责划分 |
| P2 | **事件日志是资产**：每次工具调用与响应都可回放 | brain/hands/session 三分；AgentAssay 的 trace-first 回归 |
| P3 | **干预是一等公民**：人给指令要能变成确定性 API，而不是求模型 | 上游 [评估](01-EVALUATION-ARTEX.md) ③；OAP 的 pre-action 授权 |
| P4 | **一致性可测**：行为一致用指标门禁证明，不靠声明 | promptfoo/AgentAssay 的回归方法论 |
| P5 | **裸机优先**：单二进制 + PG + 本地文件即可完整运行 | 决策 4 |

## 二、关键决策：复制执行底座 + 前端重做

完整论证见 [ADR-0002](adr/0002-copy-artex-as-execution-base.md)。一句话：
**"行为一致"（决策 2）的最省成本实现是复用同源提示词/hook/循环语义；而 UI 必须重做，
因为上游的信息架构本身就是它可控性差的来源。**

| 层 | 处置 | 理由 |
|---|---|---|
| 执行底座（`agent/` `db/` `server/` `traffic/` `guard/` `intercept/` `llmpool/` `llmrec/` `evidence/` `third_party/norma`） | **复制 + 最小改动** | 行为一致性的载体；改动越少，漂移越小，未来合并上游越容易 |
| 前端（`web/`） | **保留可用，另起控制台替换** | 上游 IA 把"干预"埋在二级页签；且任务页/对话页两套聊天是可控性差的直接来源 |
| 控制面 | **新建 `harness/`** | 承载 P2/P3/P4，全部是新增代码，不碰上游行为路径 |

## 三、三层选型与理由

### 3.1 前端：React 19 + Vite 7 + Radix Themes（自研控制台）

> 本节原方案是 Next.js 15 静态导出；实现时改为 Vite SPA（`web/`），并已下线上游两套前端
> （`/console/` 零构建页与 `/legacy/` Next 静态导出）。下面记录现状与理由。

**选它**：

- **构建产物可直接 `go:embed`**。`web/` 用 `vite build` 输出到 `server/console-app/`，
  由 `server/console_app_embed.go` 的 `go:embed all:console-app` 无条件嵌入 ——
  **不需要 build tag、不需要 Node 运行时**，裸机 `go build` 即自带 UI（P5 成立）。
- **零构建链依赖**。Vite 只有一条 `npm run build`，产物提交进仓库，纯 Go 环境也能出二进制；
  上游那套 Next 静态导出（`web/out` + `-tags embedui`）已随前端下线移除。
- **流式优先**。SSE（AG-UI 事件流）+ React 19 天然适配"事件流即界面"。
- **组件生态**。Radix Themes + lucide + motion + cmdk + recharts + react-markdown，
  长列表用 `@tanstack/react-virtual` 虚拟化。

**不选**：Electron/Tauri（裸机部署场景不需要，且增加跨平台打包负担）；Next.js
（静态导出链路更重，且需要 `-tags embedui` 才能内嵌，反而增加构建分支）。

**信息架构**：控制台走 `/app/`（`/`、`/console`、`/legacy` 一律 302 过去），
一等视图是 **对话 / 轨迹 / 检查点 / 设置**，右栏按需展开**子代理**与**工作台（文件 / DIFF / 终端）**：

| 视图 | 职责 | 解决的缺口 |
|---|---|---|
| **对话（Run）** | 实时事件流按 revision 分组；就地 steer / 暂停 / 恢复；审批与拦截**就地**呈现 | ③ 干涉成本高、① 无确定性纠偏入口 |
| **轨迹** | 按轮次的行模型：工具、耗时、token | P2 可观测 |
| **检查点** | 每轮工作区快照，预览影响范围后显式回滚 | 无 git 环境下的可撤销 |
| **工作台** | 文件树 / DIFF 审阅 / 终端（人执行的命令以 `human.sh` 入流） | 产出物可审阅 |
| **子代理** | 委派列表与每一步明细 | P2 子代理可审计 |

**登录门**：控制台首屏是登录 / 首次设置密码（`web/src/LoginGate.jsx`），令牌写
`artex_token` localStorage 与同名 cookie（EventSource 用）。服务端连不上库时如实报错，
不谎称"尚未设置密码"。

### 3.2 后端：Go 单二进制 + 新增控制面包

**选它**：

- **与底座同语言同进程**。引擎、双图、SSE、traffic 都在 Go 里；控制面若另起服务，
  所有状态访问都要过网络（并重新处理事务/锁），等于把上游已经踩平的坑再踩一遍。
- **单二进制满足 P5**。`CGO_ENABLED=0 go build` 产物在 Linux/Windows/macOS 全平台可跑，
  上游 `build.sh` 已产出六包矩阵。
- **norma 的扩展点已经够用**：`PreToolUse`/`PostToolUse`/`Stop` hook（`third_party/norma/hook/`）、
  `agentcore.Options` 的 `Tools`/`Hooks`/`PermissionMode`/`Compaction`/`Compactor`/
  `Transcript`/`Tasks`（`third_party/norma/agentcore/agentcore.go:27-82`）。
  → **P1 与 6.1 的 middleware 原则天然成立，不需要新造插件系统。**

**新增包 `harness/`（控制面）的职责边界**：

| 子模块 | 职责 | 是否触碰上游行为 |
|---|---|---|
| `harness/protocol` | 前后端协议：Item/Turn/Thread 三概念的 Go 实现 + 事件信封（带 `revision`） | 否（纯新增，不改引擎语义） |
| `harness/intervene` | 干预原语：`Steer` / `EditIntent` / `Reorder` / `Replan` / `Kill` / `PauseAll` | 否（调用上游 `engine.SteerWork` 等既有方法） |
| `harness/replay` | 回放：从 `activity` + `llmrec` + `traffic` 重建一次运行 | 否（只读） |
| `harness/eval` | scenario 执行、指标计算、指纹回归、门禁 | 否（只读 + 独立表） |
| `harness/consistency` | 行为一致性对照（上游 vs 本地） | 否（离线工具） |

**不选**：Python/Node 重写执行层（必然行为漂移，与决策 2 冲突）；微服务拆分
（裸机优先，运维复杂度不可接受）。

### 3.3 数据库：PostgreSQL 16 唯一真值 + 本地内容寻址文件

**选它（沿用上游）**：

- 上游 schema 已深度绑定 PG 能力：JSONB（`payload`/`open_ports`/`auth`/`params`）、
  数组 + GIN、`CIDR`/GIST、部分唯一索引（`uq_av2_*`）、advisory 锁、`FOR UPDATE SKIP LOCKED`
  （通知投递）。**换库等于重写 49 张表与全部并发语义，收益为负。**
- 上游本身就是从 SQLite 迁到 PG 的（`db/db.go` 顶部注释仍留着旧叙述），方向已被验证。

**不引入**：Redis / MQ（裸机优先，且上游用 PG 的 `FOR UPDATE SKIP LOCKED` + 事件表已达同等效果）；
向量库（当前没有 retrieval 需求；需要时用 `pgvector` 同库扩展）。

**文件侧沿用**：流量正文按 SHA-256 内容寻址落本地（`traffic` 的 `_blobs/sha256/xx/`），
漏洞证据正文独立存 `data/evidence/blobs/`（与可清理的 `data/traffic` 分离，多漏洞共享同一份）。
**大正文绝不进库**——这是上游做对的一件事。

**新增表（最小集，全部新增不改变上游语义）**：

| 表 | 用途 | 解决 |
|---|---|---|
| `harness_revisions` | `(task_id, revision, turn_kind, turn_ref, summary)`：一轮结束打点 | ④ 无版本一致性视图 |
| `harness_turn_items` | `(task_id, revision, activity_id)`：事件↔轮次映射的**旁表**（不动 `activity` 热路径） | P2 回放 |
| `tasks.revision`（加列） | 任务当前版本：读一次即知是否 stale | ④ 跨 agent 交错 |

> 实现说明：S3 的评测指标与场景集**没有**建 `harness_events` / `harness_scenarios` /
> `harness_eval_runs` / `harness_metrics` 表——回放直接从 `harness_revisions` +
> `harness_turn_items` + `activity` 重建，场景集是 `eval/scenarios/*.json` 文件，
> 判定结果由 `harness-gate` 输出 JSON（`eval/reports/`）。`activity` 仍是唯一真值，
> 再加一套表只会让"回放"和"预算"两个口径分叉。

**不加**：`harness_approvals` / `harness_plan_reviews` / `harness_policies`。
——决策 5 明确不要全审批与计划预审，故**不引入新的强制门**。策略面只做上游规则的**呈现与开关**。

## 四、引擎设计

### 4.1 循环形态：事件驱动 + Frontier 队列（继承上游）+ Hybrid 补充

对照 [02 §3.1](02-HARNESS-RESEARCH.md) 的五种模式：渗透域需要"长时程 + 串行依赖 + 人在环"，
纯 Agent Loop 不够，纯 Graph-flow 太重。上游的 **Event-driven（图变更唤醒 planner）+ Frontier
优先队列（worker CAS 领取）** 已是正确骨架，保留并补三点：

1. **显式重规划触发**（`harness/intervene.Replan`）：上游只有"边沿/心跳"唤醒，人无法要求
   "现在就重判一次"。
2. **frontier 可排序**：上游 `ORDER BY priority DESC, id ASC` 固定；新增排序持久化字段，
   人可置顶/降级（不改变 CAS 领取语义）。
3. **轮次可观测**：上游已有 `round` 活动标记（`server/engine.go:873`），控制台将其升为一等
   时间轴（Turn 概念）。

### 4.2 brain / hands / session 三分映射

| 概念 | 本项目实现 | 现状 |
|---|---|---|
| brain（模型 + harness，无状态） | planner/worker agent + norma session；每轮会话可从 transcript 重建 | 上游已具备（`agent/worker.go:349` `Execute`、transcript 续跑） |
| hands（执行环境，消耗品） | Bash / 交互 shell / 容器化工具 / MCP / 自定义工具 | 上游具备（`tool/` 的 bash + `shell_session`；容器化为后续扩展） |
| session（仅追加事件日志） | `activity`（探索事件）+ `llmrec`（LLM 原文）+ `traffic`（网络留痕） | 上游具备，缺统一协议视图 → `harness/protocol` |

**崩溃恢复**：上游靠"意图状态 + transcript 续跑"（`db/tasks.go`、`LoadExisting`），
已能跨进程重启续跑；补 `harness/replay` 后可做到**完全重放**（用于评测与事故复盘）。

### 4.3 状态机与终止语义（保留上游）

- 任务：`running / paused / queued / done / failed / timeout`（`db/constants.go`、`IsTerminal`）。
- 意图：`open → running → done|blocked|exhausted|stopped|paused|deleted`（CAS 迁移，
  `db/exploration.go:363`）。
- 终止原因：`harness.TerminalReason`（完成/步数/时长/取消/错误）→ **一致性指标之一**
  （决策 2 验收要比较它的分布）。

**不改**：终止语义与预算语义是行为一致性的核心，任何调整都必须走一致性门禁。

### 4.4 上下文投递与压缩（保留上游的取舍）

上游的两个刻意取舍值得写进架构而不是被"优化掉"：

- **planner：态势放本轮 user 输入，system 只留静态正文**（`agent/planner.go:379-382`）——
  为了让 system 每轮稳定、利于 prompt 缓存；代价是长轮次可能被压缩掉态势。
- **worker：意图 + 锚定资产原始数据放 system**（`agent/worker.go:385-400`）——意图是贯穿
  run 的不变量，宁可牺牲跨意图缓存复用也不能被 compaction 压掉。

压缩侧：SDK `compaction`（窗口aturn）+ 上游自研 **cold-digest**（`agent/coldgraph.go`：
R=6 轮冷却、K=2 折叠阈值、同父单点救援，折叠成 `digest` 节点且 `covers` 边可无损还原）。
这是上游最"harness"的一块设计，**保留**。

### 4.5 并发与一致性（修 ④，不改行为）

| 问题 | 修法 | 影响行为吗 |
|---|---|---|
| `graph_overview` 是多查询非原子读 | 包进单事务（`REPEATABLE READ`）读快照 | 否（只影响读到什么，不改写） |
| 无任务级版本 | 写路径末尾 bump `tasks.revision`；控制台展示 revision 与 stale 标记 | 否 |
| 写工具已串行（SDK 保证） | **不动** `streamAsync` 语义 | 否 |

### 4.6 干预原语（修 ①③，全部是"给人更多杠杆"）

| 原语 | 映射到上游 | 新增接口 |
|---|---|---|
| 运行中纠偏 | `engine.SteerWork`（已存在，仅模型工具） | `POST /api/tasks/{id}/intents/{iid}/steer` |
| 意图编辑 | 无（上游无 update 路径） | `PATCH /api/tasks/{id}/intents/{iid}`（summary/priority/asset_ids），落库 `content_version++` |
| 立即重规划 | `t.Notify()`（内部） | `POST /api/tasks/{id}/replan` |
| frontier 排序 | 固定 `priority DESC, id ASC` | `POST /api/tasks/{id}/frontier/order` |
| 单 work 停止 | `engine.KillWork`（planner 工具） | `POST /api/tasks/{id}/intents/{iid}/kill` |
| 全局急停 | 逐任务 pause | `POST /api/tasks/pause-all` |

**设计约束**：这些原语**直接调引擎方法，不经模型**——这就是"确定性"的含义；
且它们**只增加人的能力，不改变 agent 的默认行为**，因此不违反决策 5（不加限制）。

### 4.7 可观测：事件信封 + revision

```
{ "rev": 128, "task": "12", "thread": "exp34", "turn": "planner#7",
  "item": { "kind":"tool_use|tool_result|text|thinking|round|intercept",
            "tool":"Bash", "tool_use_id":"…", "is_error":false,
            "summary":"…", "detail":"…" } }
```

`rev` 来自 `harness_revisions`；前端按 `rev` 做 seq-merge（上游 `activity-merge.ts` 的思路），
断线自愈；`Replay` 页按 `rev` 区间拉取。**协议存在的理由与 Codex 相同**：审批/流式 diff/
线程持久化不是"工具调用"，塞进 MCP 会打架。

## 五、业务裸机设计（决策 4）

### 5.1 运行形态

```
artex-harness（单二进制，CGO_ENABLED=0）
  ├── :8787  HTTP（API + SSE + 内嵌控制台）
  └── :8788  流量记录代理（MITM，可关）
PostgreSQL 16（同机）
本地文件系统：data/（jwt.key、traffic blob、evidence 快照、transcripts、tasks 工作区）
```

**无 Node / 无 Redis / 无容器编排 / 无云依赖**。前端已静态导出并 `go:embed`；
工具链在宿主环境（或按需由 agent 调用容器）。

### 5.2 目录约定

| 路径 | 内容 | 备份需否 |
|---|---|---|
| `config.json` | 数据库连接（含密码，**不入库**） | 是 |
| `data/jwt.key` | 会话签名密钥 | 是 |
| `data/traffic/` | 流量 blob（可清理） | 可选 |
| `data/evidence/blobs/` | 漏洞证据正文（**必须与流量分开备份**） | **必须** |
| `data/transcripts/` `data/tasks/` | LLM 原文、任务工作区 | 可选 |
| PostgreSQL | 49 张表 + `harness_*` | **必须** |

### 5.3 守护与自启（裸机）

主题：**用宿主 init 管进程，不要自造 daemon**。上游已有两层——
`start.sh`（守护 + 换装协议，靠退出码 75 契约）与 `selfupdate`（暂存/校验/冒烟/回滚）。
systemd 单元（示例）：

```ini
# /etc/systemd/system/artex-harness.service
[Unit]
Description=artex-harness
After=network-online.target postgresql.service
Wants=postgresql.service

[Service]
Type=simple
User=artex
WorkingDirectory=/opt/artex-harness
Environment=ARTEX_PG_DSN=postgres://artex:***@127.0.0.1:5432/artex?sslmode=disable
ExecStart=/opt/artex-harness/artex -addr :8787 -proxy 127.0.0.1:8788
Restart=on-failure
RestartSec=3
# 换装协议：程序主动退出并期望被重拉，需允许 restart
SuccessExitStatus=0 75

[Install]
WantedBy=multi-user.target
```

Windows 裸机：`start.bat` + 计划任务/服务包装（上游已处理 Git Bash 工具层的探测，
见 `third_party/norma/tool/shell.go`）。

### 5.4 资源与并发建议

- worker 数：默认 3（上游"系统设置"可调）；单机 4C8G 建议 ≤4。
- PG `maxOpenConns=32`（上游 `db/db.go:34`），低于 PG 默认 `max_connections=100`。
- 流量留痕：按"记录开关"控制；关闭时**不注入**代理与流量工具（上游已如此设计）。

### 5.5 升级与迁移

- 备份：`pg_dump artex > artex.sql` + 打包 `data/`。
- 迁移：**重启即迁移**（`db/schema.sql` 幂等，advisory lock 串行化多实例初始化）。
- 升级：沿用 `selfupdate`，但更新源已改为本仓库（见 [PROVENANCE §3.1](PROVENANCE.md)）。

## 六、渗透域设计：让它长成 harness 而不是工具堆

### 6.1 能力供给面（harness 只认统一接口）

| 供给面 | 接入方式（上游已具备） | harness 侧需补 |
|---|---|---|
| 容器化工具 | `Bash` / 交互 shell 调 `docker exec`（后续可加容器生命周期管理） | 容器清单与生命周期 |
| MCP server | `mcphttp/`（Streamable HTTP + 旧版 SSE）、`mcp_servers` / `mcp_tools_cache` 表 | 工具元数据（风险类别/超时/输出预算） |
| Skill 文档 | `skills/` + `skill_usage` 表 + 可见性表 | 加载时机与预算（上游 `DeferredTools`/`UnlockSet` 已是雏形） |
| CLI 包装 | `tools.kind='shell'`：只声明环境里有什么，拼进 Bash 描述 | — |
| 自定义工具 | `create_custom_tool`（command/script/http） | 统一进能力注册表 |

**判据（防滑回工具堆）**：新增"某漏洞的检测逻辑"→ 应落在 skill/MCP/自定义工具；
新增"让 agent 更好地使用任意检测逻辑"→ 才进 harness。

### 6.2 四件必须由 harness 承担的事

1. **留痕即资产**：`traffic`（HTTP 全留痕 + FTS 检索）+ `llmrec`（LLM 原文）。
2. **证据门**：漏洞必须绑定真实执行证据；上游已有有序 `finding_traffic_bindings` +
   `evidence_version` 乐观锁 + 正文 SHA-256 校验 + `retester` 复测 agent。
   → harness 侧补"证据完整性报告"（哪些结论有绑定、哪些没有），**不强制**（决策 5）。
3. **串行链可控推进**：planner 跨唤醒共享 todolist（上游已实现）+ 人可排序 frontier（新增）。
4. **长时程持久化**：意图状态 + transcript 续跑（上游已实现）。

## 七、行为一致性如何度量（决策 2 的验收）

**口径**：同提示词 + 同策略；验收 = 拒绝话术与行为逻辑一致；**不要求模型权重一致**。

**方法**：同一 scenario 集在「上游 ARTEX（`df64cf83`）」与「artex-harness」上双跑对照。

| 指标 | 类型 | 门禁 |
|---|---|---|
| 拦截命中集（哪些调用被拦）| 确定性 | **必须完全一致** |
| 拒绝/阻断话术 | 确定性（逐字 diff） | **必须完全一致** |
| `TerminalReason` 分布 | 确定性 | 同分布（允许采样波动，设阈值） |
| 工具调用分布 | 统计 | 偏离阈值告警 |
| `prove_goal` 判定与目标达成时机 | 逻辑 | 同判定 |
| guard 归因分布（ok/blocked/error） | 统计 | 偏离阈值告警 |

**回归检测用行为指纹法**（AgentAssay 路线：二值 pass/fail 对非确定性工作流无效，
指纹法在其实验中检出 86% 回归 vs 二值法 0%）：对同一 scenario 的**轨迹结构**做指纹
（工具序列 + 终止原因 + 证据绑定集合），指纹变化即回归。

**红线**：开发期任何改动若导致"原本被拦截/被拒绝的动作变为放行"，视为**不一致**，
必须修复或显式记录在 ADR 中由业主确认。

## 八、明确不做（对齐决策 5）

| 不做 | 原因 |
|---|---|
| 强制范围硬约束（出站白名单/目标白名单强制执行） | 业主口径：不加强制硬约束 |
| 全审批 / 计划预审门 | 业主口径；且 [02 §3.5](02-HARNESS-RESEARCH.md) 证据显示审批提示基本是表演 |
| 改动上游内置拦截规则与其默认值 | 保持与原版行为一致（决策 2 的载体） |
| 预设任何模型供应商 | 决策 3 |
| 公网部署方案（反代/HTTPS/多租户） | 决策 4（裸机优先） |

## 九、与上游的差异管理

1. **差异越小越好**：本阶段相对上游仅 1 处代码改动（`selfupdate` 更新源，见 PROVENANCE）。
2. **新增代码隔离**：控制面全部在新包 `harness/` 与新目录 `eval/`；前端是仓库自己的 `web/`；
   对上游包的修改必须**逐条记入 PROVENANCE**。
3. **可合并**：module 路径保持 `github.com/Autumn-27/artex`，import 与 `go.sum` 未动，
   上游改动可按 git 三方合并引入；冲突集中在 `harness/` 之外的少量补丁点。
4. **行为改动必须过一致性门禁**（§7），否则不进主干。
