# artex-harness

**面向渗透场景的 agent HARNESS（执行骨架 + 控制面 + 证据与评测）**。执行底座由
[ARTEX](https://github.com/j5tv9nfzhn-hue/ARTEX)（AGPL-3.0）复制而来，在此之上建设
「可观测、可回放、可干预、可评测」的 harness 层。

> **本项目是 HARNESS，不是又一套渗透工具集。** 工具（nmap / sqlmap / 自研脚本 / MCP
> server / skill）由外部供给，harness 只负责：**循环、状态、调度、留痕、干预、评测**。
> 详见 [docs/02-HARNESS-RESEARCH.md](docs/02-HARNESS-RESEARCH.md) 与
> [docs/03-ARCHITECTURE.md](docs/03-ARCHITECTURE.md)。

---

## ⚠️ 使用边界（请先读）

**本项目仅供授权范围内的安全测试、研究与本地隔离环境验证。** 禁止用于任何未获明确书面
授权的系统。完整条款见 [DISCLAIMER.md](DISCLAIMER.md) —— 使用即表示已阅读并同意。

---

## 这是什么 / 不是什么

| | |
|---|---|
| **是** | agent 执行骨架（loop / 工具注册 / 权限与 hook / 上下文压缩 / 记忆 / 子 agent） |
| **是** | 控制面（运行干预、计划审阅、状态回放、行为一致性评测） |
| **是** | 渗透域的**状态与证据**模型：双图（资产图 + 探索图）、流量留痕、漏洞证据快照 |
| **不是** | 工具集合。harness 不实现扫描器/利用器，工具通过供给面（容器 / MCP / skill / CLI / 自定义工具）接入 |
| **不是** | 托管服务。裸机单二进制 + PostgreSQL 即可跑（决策 4） |

## 当前状态（S1–S5 已完成）

| 阶段 | 交付 | 文档 |
|---|---|---|
| S0 | 执行底座 + 文档 + ADR | [PROVENANCE](PROVENANCE.md) |
| S1 | 协议（Thread/Turn/Item + revision）与 `graph_overview` 单快照读 | [docs/stages/S1](docs/stages/S1.md) |
| S2 | 6 个**确定性干预原语** + Run/Plan 控制台 | [docs/stages/S2](docs/stages/S2.md) |
| S3 | 回放 + 一致性评测 + 门禁 CLI + 场景集 | [docs/stages/S3](docs/stages/S3.md) |
| S4 | 会话栈收敛 + 能力注册表 + 证据完整性 + 上游 2 处修复 | [docs/stages/S4](docs/stages/S4.md) |
| S5 | 控制台成为唯一一等界面 + 平台视图 + 内嵌复测 | [docs/stages/S5](docs/stages/S5.md) |

构建与测试：`go build ./...` 通过；`go test ./harness/... ./agent/ ./config/ ./guard/ ./llmpool/`
通过（`agent` 侧仅有一个**上游既有**失败用例，见 S1 文档）。

## 控制台（默认界面）

`http://localhost:8787/` → 302 → `/console/`。零构建、同源、无条件内嵌进单二进制
（目标机不需要 Node）。

三栏工作区（设计依据见 [docs/design/DESIGN.md](docs/design/DESIGN.md)）：

- **左：会话列表** — 按状态分组，每项带运行中 worker / 目标进度 / high 漏洞数；
- **中：一次运行** — 轮次时间线（每轮带 `rev`、步数、错误、令牌；工具调用与输出配对）
  + 常驻 composer（中断 / 纠偏 / 审批模式 / worker / 令牌）；
- **右：检查器** — 上下文（目标进度、令牌构成、运行状态、漏洞分档）/ 证据 / 能力 / 策略。

主标签：**轨迹 · 计划 · 回放 · 评测**。所有控件调的都是已存在的确定性 API
（`/api/harness/*`、`/intents/{iid}/steer`、`/intercept/judge`）。

上游 Next.js 界面保留在 `/legacy/`（需 `-tags embedui` 构建），作为回退通道。
静态演示（无需后端）：[`docs/demo/harness-console.html`](docs/demo/harness-console.html)。

## 架构一览

```
┌──────────── Console（新前端，待建）────────────┐
│ Run 实时流 · Plan 可编辑 · Replay 回放 · Eval  │
└───────────────────┬───────────────────────────┘
                    │ HTTP + SSE（事件信封带 revision）
┌───────────────────▼──── Control Plane（harness/，待建）─┐
│ 运行干预(steer) · 意图编辑 · 计划审阅 · 回放 · 一致性评测 │
└───────────────────┬────────────────────────────────────┘
┌───────────────────▼──── Execution Plane（已导入）──────┐
│ Engine(planner + worker×N) · agent(goals/planner/worker)│
│ norma harness（loop/hook/permission/compaction/记忆）   │
│ guard + intercept（内置破坏性命令拦截）                 │
│ traffic 记录代理 · evidence 快照 · llmpool · llmrec     │
└───────────────────┬────────────────────────────────────┘
┌───────────────────▼──── PostgreSQL 16 ─────────────────┐
│ 双图 + 49 张表（唯一真值）；流量正文存本地内容寻址文件    │
└────────────────────────────────────────────────────────┘
```

三层选型与理由（前端 / 后端 / 数据库）见 [docs/03-ARCHITECTURE.md](docs/03-ARCHITECTURE.md)。

## 已确定的项目口径

| # | 决策 | 记录 |
|---|---|---|
| 1 | 许可证继承 **AGPL-3.0** + 最强免责声明 | [ADR-0001](docs/adr/0001-owner-decisions.md) |
| 2 | 行为一致 = **同提示词 + 同策略**；验收为拒绝话术与行为逻辑一致（不要求同权重） | [ADR-0001](docs/adr/0001-owner-decisions.md) |
| 3 | LLM 端点**不预设供应商**，沿用双格式 + 多配置轮询 | [ADR-0001](docs/adr/0001-owner-decisions.md) |
| 4 | 部署**裸机优先**（本机云电脑），不做公网部署方案 | [ADR-0001](docs/adr/0001-owner-decisions.md) |
| 5 | 执行能力保留；**只拦最极端破坏行为**；不新增强制硬约束与全审批 | [ADR-0003](docs/adr/0003-capability-boundary.md) |
| — | **复制 ARTEX 作为执行底座**（而非独立重写） | [ADR-0002](docs/adr/0002-copy-artex-as-execution-base.md) |

**注意**：以上是"做什么/不做什么"的项目口径，不是对任何使用行为的授权——见 [DISCLAIMER](DISCLAIMER.md)。

## 快速开始（裸机）

```bash
# 1) 数据库（本机已有 PostgreSQL 16 可跳过）
sudo -u postgres psql -c "CREATE USER artex WITH PASSWORD 'yourpass' CREATEDB;"
sudo -u postgres psql -c "CREATE DATABASE artex OWNER artex;"

# 2) 配置（只填数据库；LLM 在 UI 里配，无预设供应商）
cp config.example.json config.json     # 填 database.*
# 或：export ARTEX_PG_DSN="postgres://artex:yourpass@127.0.0.1:5432/artex?sslmode=disable"

# 3) 编译并启动（不带前端；首次启动自动建表）
CGO_ENABLED=0 go build -o artex ./cmd/artex
./start.sh            # 守护脚本：程序退出按退出码决定是否重拉（换装协议依赖它）
# → http://localhost:8787  （302 到控制台 /console/；首次进入设置管理密码，用户名固定 ARTEX）
```

`start.sh` / `start.bat` 是**守护 + 换装**入口，请用它而不是直接跑 `./artex`。参数透传：
`./start.sh -addr :8787 -proxy :8788`（`:8788` 是流量记录代理）。

**内嵌前端（可选）**：`cd web && npm ci && npm run build:static && cd .. &&
cp -r web/out server/webui/dist && CGO_ENABLED=0 go build -tags embedui -o artex ./cmd/artex`。
（`web/` 是上游界面，属待替换层；控制台另起。）

Docker（可选，非主路径）：`cp .env.example .env && docker compose up -d`。

## 仓库结构

```
artex-harness/
├── README.md / DISCLAIMER.md / PROVENANCE.md
├── LICENSE                     # AGPL-3.0（继承上游）
├── agent/ db/ server/ traffic/ ...   # 执行底座（复制自 ARTEX，见 PROVENANCE）
├── third_party/norma/          # 内置 agent SDK（上游已被删除，本地可补丁副本）
├── harness/                    # 控制面（待建）
├── eval/                       # 行为一致性评测集与门禁（待建）
├── web/                        # 上游前端（待被 web-console 替换）
└── docs/
    ├── 01-EVALUATION-ARTEX.md  # ARTEX 作为 harness 底座的评估
    ├── 02-HARNESS-RESEARCH.md  # 开源 harness 组成与设计惯例调研
    ├── 03-ARCHITECTURE.md      # 三层选型 / 引擎 / 裸机 / 渗透域设计
    ├── 04-ROADMAP.md
    ├── adr/                    # 架构决策记录（含上述 5 项口径）
    └── upstream/               # 上游原文归档（README/CHANGELOG/复核报告/设计文档）
```

## 行为一致性（决策 2）

验收口径：**同提示词 + 同策略**，比对「拒绝话术」与「行为逻辑」（拦截命中、终止原因、
工具分布、目标判定），**不要求模型权重一致**。方法：同一 scenario 集在「上游 ARTEX」与
「artex-harness」上双跑对照，指标化 + 门禁化。见
[docs/03-ARCHITECTURE.md](docs/03-ARCHITECTURE.md#行为一致性如何度量)。

## 来源与许可

执行底座复制自 `j5tv9nfzhn-hue/ARTEX`（上游 `Hinln/ARTEX`）commit
`df64cf83d9e35105d2f93792462740b61b449856`，许可证为 **AGPL-3.0**，本仓库**继承同一许可**：
通过网络提供服务时，必须向使用者提供对应完整源码。复制范围、排除项与相对上游的改动，
逐条记录在 [PROVENANCE.md](PROVENANCE.md)。
