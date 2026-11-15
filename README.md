<div align="center">

# artex-harness

**面向授权渗透场景的单 agent 执行骨架**

循环 · 状态 · 调度 · 留痕 · 干预 · 评测

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
[![ci](https://github.com/j5tv9nfzhn-hue/artex-harness/actions/workflows/ci.yml/badge.svg)](https://github.com/j5tv9nfzhn-hue/artex-harness/actions/workflows/ci.yml)
![License](https://img.shields.io/badge/license-AGPL--3.0-blue)
![UI](https://img.shields.io/badge/UI-React%2019%20·%20Vite%207-61DAFB?logo=react&logoColor=white)
![DB](https://img.shields.io/badge/PostgreSQL-14%2B-336791?logo=postgresql&logoColor=white)
![Binary](https://img.shields.io/badge/deploy-single%20binary-success)

[快速开始](#快速开始) · [架构](ARCHITECTURE.md) · [文档](#文档索引) · [参与开发](CONTRIBUTING.md)

</div>

---

## 它是什么

一个**单进程 Go 服务**：HTTP API + agent 引擎 + 内嵌控制台。

把一次渗透任务交给**一个**对话式 agent——它自己判断、自己动手、需要时把独立子任务派给子代理。
harness 负责的是**循环、状态、调度、留痕、干预、评测**这六件事，工具本身
（Bash / HTTP / MCP server / skill）由外部供给。

> **不是**又一套漏洞扫描器，**也不是** planner + worker 的流水线。
> 一个任务只有一个 agent，决策与执行在同一段对话里发生。

### 三条边界

| | |
|---|---|
| **范围** | 任务的目标与描述就是授权的全部范围；越界要先登记（`add_task_scope`）并说明依据 |
| **结论** | 只有真实触发过、拿到可复现证据的结论才登记为漏洞；"看起来可能有问题"不算 |
| **使用** | 仅限**已获明确书面授权**的系统。完整条款见 [DISCLAIMER.md](DISCLAIMER.md) |

## 亮点

| | 能力 | 具体是什么 |
|---|---|---|
| 🧠 | **单 agent，不分层** | 没有 planner 决策、没有 worker 池；agent 自己规划并执行，失败自己换机理 |
| 🧵 | **子代理并行** | 同一条回复里可同时派 `recon` / `verify`；各自独立 transcript 与 10 分钟预算，父轮次被取消不会杀掉已在跑的 |
| 📡 | **事件流可续传** | AG-UI 协议；`?since=<seq>` 断点续传 + 15s 心跳，长任务断线不丢事件、不重放 |
| 📊 | **上下文可观测** | 实时占用百分比与窗口总量；按窗口比例压缩（默认 0.7 摘要 / 0.5 清旧工具结果） |
| 🛡 | **审批门不可绕** | 拦截规则 + 任务级审批门；子代理继承父会话同一套 hooks，破坏性命令照样拦 |
| 🔀 | **多模型可编排** | 供应商在 UI 里配；故障转移链、任务级思考深度覆盖、按模型目录自动补上下文与档位 |
| 🗂 | **记忆在图谱里** | 资产 / 事实 / 发现落库，每轮重新注入上下文；压缩丢的是过程，不是结论 |
| 📦 | **单二进制部署** | 控制台 `go:embed` 进二进制，目标机不需要 Node / bun；数据落 PostgreSQL + 磁盘原文 |

## 快速开始

### 方式一：源码构建（推荐）

```bash
# 1) 数据库（已有 PostgreSQL 可跳过）
docker run -d --name artex-pg -p 5432:5432 \
  -e POSTGRES_USER=artex -e POSTGRES_PASSWORD=yourpass -e POSTGRES_DB=artex \
  postgres:16-alpine

# 2) 指向它（首次启动自动建表）
export ARTEX_PG_DSN="postgres://artex:yourpass@127.0.0.1:5432/artex?sslmode=disable"

# 3) 构建：控制台 + 后端（产物是单个二进制）
make build-all

# 4) 启动
./start.sh                      # 守护式启动（推荐）
# 或直接跑：./artex -addr :8787 -data ./data
```

打开 `http://localhost:8787/` → 302 到 `/app/`。
**首次进入要设置管理密码**（用户名固定 `ARTEX`），随后在「设置 → 供应商与模型」里填 LLM
端点与密钥——仓库不带任何预设供应商。

> 默认端口 `8787`；`-proxy :8788` 是流量记录代理（`-proxy ""` 可关闭）。

### 方式二：Docker

```bash
./build.sh                 # 交叉编译，产物在 dist/
docker build -t artex-harness .
docker run -d -p 8787:8787 -e ARTEX_PG_DSN="postgres://..." artex-harness
```

> `docker-compose.yml` 里的 `artex` 服务默认指向**上游镜像** `autumn27/artex`。
> 要用本仓库的构建，把 `image:` 换成你自己 build 的 tag。

## 界面

一个界面，三块区域，没有第二个前端。

```
┌───────────── 会话 ─────────────┬──────────────── 一次运行 ────────────────┬──── 侧栏 ────┐
│ ＋ 新建对话                     │  轮次时间线（思考 / 正文 / 工具与结果成对） │ 子代理        │
│ 对话                            │  · 长列表虚拟化，几千条事件不卡            │  进行中 / 已完成│
│  ● 用一句话说明你是谁            │  · 断线自动续传，不重放、不丢事件           │  点进去看明细  │
│ HARNESS 运行（41）              │  · 工具结果超过上限时溢出落盘并留指针        │ 上下文占用    │
│  ● free.ai                      │                                          │ 预算曲线      │
│  ● 并行派发两个子代理            │ ┌──────────────────────────────────────┐ │              │
│                                 │ │ 对 agent 说话…  模型 ▾ 思考 ▾ 上下文 7% │ │              │
└─────────────────────────────────┴──────────────────────────────────────────┴──────────────┘
```

- **对话** 与 **运行** 是两个东西：对话没有目标、发第一条消息才开跑；运行有目标、带预算与目标判定。
- **设置** 覆盖：供应商与模型 / MCP / 技能 / 权限与审批 / 会话日志 / 运行环境 / 外观。
- 每个控件背后都是真实端点；后端没有的能力，界面**禁用并写明原因**，不做假开关。

## 架构

```
web/           React 19 + Vite 控制台（约 2.5k 行）→ 产物内嵌进二进制
    ↓ go:embed
server/        HTTP 层：路由 / 鉴权 / DTO / AG-UI / 引擎调度
agent/         LLM 接入与运行编排：provider、提示词、压缩钩子、工具装配
harness/       运行期能力：浏览器会话、diff 基线、评测闸门
db/            PostgreSQL schema + 各领域 store（资产图 / 探索图 / 活动 / 对话）
third_party/   vendored agent SDK（带本地补丁）
```

目录职责、数据流与**必须守住的不变量**见 [ARCHITECTURE.md](ARCHITECTURE.md)。

## 可观测性（这是它区别于"能跑就行"的地方）

| 观测点 | 怎么看 |
|---|---|
| 上下文压力 | 输入框右侧实时百分比 + 已用/窗口；≥70% 琥珀、≥90% 红 |
| 压缩 | 压缩按窗口比例触发，`compactions_observed` 分 micro/auto 计数；摘要模板要求「已排除路径」强制保留 |
| 子代理 | 侧栏列出进行中/已完成/失败 + 耗时；点进去看该子代理自己的每一步（工具入参、输出、思考） |
| 预算 | `/api/harness/budget/{id}` 给出窗口、最近输入、压缩次数、轮次曲线 |
| 事件 | `/api/agui/agent?task=N&since=<seq>&follow=1`，断点续传 + 心跳 |

## 工程

```bash
make            # 列出全部目标
make build-all  # 控制台 + 后端
make test       # 单元测试（不碰数据库）
make vet        # 静态检查
ARTEX_TEST_DB=<库名> make test-db   # 需要 PG 的测试：会重建 schema，必须显式指名测试库
```

- CI：`go vet ./...` + 单元探针 + 二进制引信（`.github/workflows/ci.yml`）
- 测试库守卫：`ARTEX_TEST_DB` 与 DSN 库名不一致时**先跳过、不重建 schema**（踩过清库的坑）
- 提交约定与「改哪里最容易出事」清单：[CONTRIBUTING.md](CONTRIBUTING.md)

## 文档索引

| 文档 | 内容 |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | 分层、目录职责、两条产品线、数据流、不变量 |
| [CONTRIBUTING.md](CONTRIBUTING.md) | 环境、命令、提交约定、加接口/加界面步骤 |
| [docs/03-ARCHITECTURE.md](docs/03-ARCHITECTURE.md) | 设计取舍 |
| [docs/07-HARNESS-RUNTIME.md](docs/07-HARNESS-RUNTIME.md) | HARNESS 运行期 |
| [docs/adr/](docs/adr/) | 已定型的决策（含"以 ARTEX 为执行底座"的由来） |
| [docs/09-LONGSESSION-BROWSER-PROJECT.md](docs/09-LONGSESSION-BROWSER-PROJECT.md) | 长会话 / 浏览器 / 项目目录 |
| [DISCLAIMER.md](DISCLAIMER.md) | 使用条款 |

## 来源与许可

执行底座来自 [ARTEX](https://github.com/j5tv9nfzhn-hue/ARTEX)（AGPL-3.0），在其之上建设
harness 层：单 agent 运行期、事件流、子代理、预算与压缩策略、控制台。

本项目沿用 **AGPL-3.0**。改编/部署为网络服务时，须按该许可证开放对应源码。
上游说明保留在 [README-ARTEX.md](README-ARTEX.md)，来源清单见 [PROVENANCE.md](PROVENANCE.md)。
