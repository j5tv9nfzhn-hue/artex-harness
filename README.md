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
![API](https://img.shields.io/badge/API-290%20routes-informational)
![Tests](https://img.shields.io/badge/tests-819%20funcs%20·%20191%20files-brightgreen)

[快速开始](#快速开始) · [功能](#功能矩阵) · [配置](#配置参考) · [架构](#架构) · [API](#api-概览) · [运维](#运维手册) · [排障](#排障手动核对清单) · [文档](#文档索引)

</div>

---

## 目录

- [它解决什么问题](#它解决什么问题)
- [它是什么 / 不是什么](#它是什么--不是什么)
- [界面](#界面)
- [功能矩阵](#功能矩阵)
- [快速开始](#快速开始)
- [首次运行走一遍](#首次运行走一遍)
- [配置参考](#配置参考)
- [架构](#架构)
- [API 概览](#api-概览)
- [可观测性](#可观测性)
- [安全模型](#安全模型)
- [运维手册](#运维手册)
- [测试与质量门](#测试与质量门)
- [排障：手动核对清单](#排障手动核对清单)
- [兼容性与已知上限](#兼容性与已知上限)
- [定位对比](#定位对比)
- [进度与路线](#进度与路线)
- [文档索引](#文档索引)
- [参与开发](#参与开发)
- [来源与许可](#来源与许可)

---

## 它解决什么问题

把一个会自己动手的 LLM agent 放进**真实系统**去干活，难点从来不是"够不够聪明"，而是：

1. **过程不可见**——它到底跑了什么命令、拿到什么输出、为什么得出这个结论？
2. **失控不可拦**——它可能 rm -rf、可能打到范围外、可能在生产库上写数据。
3. **结论不可信**——"看起来有 SQL 注入"和"拿到可复现证据"是两件事。

`artex-harness` 就是为这三件事做的执行层：**每一次工具调用都留痕、每一个破坏性动作都要过审批门、
每一条结论都必须挂证据**。智能体的部分交给模型，剩下的交给我们。

## 它是什么 / 不是什么

**是**：一个单进程 Go 服务（HTTP API + agent 引擎 + 内嵌控制台），把一次渗透任务交给**一个**
对话式 agent——它自己判断、自己动手、需要时把独立子任务派给子代理。harness 负责
**循环、状态、调度、留痕、干预、评测**这六件事；工具本身（Bash / HTTP / MCP server / skill）由外部供给。

**不是**：

| 不是 | 为什么 |
|---|---|
| 又一套漏洞扫描器 | 它不内置任何 POC 库与扫描逻辑；判据见 [ADR-0003](docs/adr/0003-capability-boundary.md) |
| planner + worker 的流水线 | 一个任务只有一个 agent，决策与执行在同一段对话里发生（[ADR-0004](docs/adr/0004-harness-runtime-no-planner-worker.md)） |
| 云端 SaaS / 多租户平台 | 单进程、单实例、面向一台机器上的操作者；没有组织/配额/多租户隔离 |
| 无人值守的自动化武器 | 审批门是唯一否决点，**默认拦破坏性动作**；使用仅限已获书面授权的系统 |

### 三条边界

| | |
|---|---|
| **范围** | 任务的目标与描述就是授权的全部范围；越界要先登记（`add_task_scope`）并说明依据，落库可审计 |
| **结论** | 只有真实触发过、拿到可复现证据的结论才登记为漏洞；"看起来可能有问题"不算 |
| **使用** | 仅限**已获明确书面授权**的系统。完整条款见 [DISCLAIMER.md](DISCLAIMER.md) |

## 界面

一个界面，三块区域，没有第二个前端。右栏是**互斥的单一面板**（子代理 / 工作台 / 浏览器 / 系统），
不是四个可以同时展开的抽屉。

```
┌───────────── 会话 ─────────────┬──────────────── 一次运行 ────────────────┬──── 右栏 ────┐
│ ＋ 新建对话                     │  轮次时间线（思考 / 正文 / 工具与结果成对） │ 子代理        │
│ 对话                            │  · 长列表虚拟化，几千条事件不卡            │ 工作台        │
│  ● 用一句话说明你是谁            │  · 断线自动续传，不重放、不丢事件           │  项目/文件/   │
│ HARNESS 运行（41）              │  · 工具结果超过上限时溢出落盘并留指针        │  DIFF/终端    │
│  ● free.ai                      │                                          │ 浏览器        │
│  ● 并行派发两个子代理            │ ┌──────────────────────────────────────┐ │  会话/F12/截图│
│                                 │ │ 对 agent 说话…  模型 ▾ 思考 ▾ 上下文 7% │ │ 系统          │
└─────────────────────────────────┴──────────────────────────────────────────┴──────────────┘
                                                                               就绪/能力/回放/取证
```

### 真实截图

| | |
|---|---|
| ![工作区](docs/screenshots/harness/harness-02-home-light.png) | ![事件流与就地审批](docs/screenshots/harness/harness-03-conversation-light.png) |
| 工作区：侧栏（对话 + 置顶运行）、事件流、composer | 事件流：思考块、`Bash × N` 合并卡、就地审批卡片 |
| ![工作台 DIFF](docs/screenshots/harness/harness-07-workbench-diff-file.png) | ![子代理](docs/screenshots/harness/harness-09-subagents.png) |
| 工作台 → DIFF：相对基线的 unified diff | 子代理：后台委派、状态与耗时（可单独停止） |
| ![浏览器 F12](docs/screenshots/harness/harness-11-browser-f12.png) | ![系统就绪](docs/screenshots/harness/harness-12-system-ready.png) |
| 浏览器 → F12：console 错误分级 + 最近动作 | 系统 → 就绪：四项自检，各自给出原因与下一步 |

全部 17 张（含深色与 900px 窄窗口）在 [`docs/screenshots/harness/`](docs/screenshots/harness/)，
索引与**复算脚本**见 [`docs/screenshots/README.md`](docs/screenshots/README.md)。
截图取自真实运行的服务（DOM 断言后才拍），演示数据由 `seed-demo.sh` 灌入——边界写在
[`docs/17-REFERENCE-UI-MAPPING.md`](docs/17-REFERENCE-UI-MAPPING.md)。

**视觉**：配色与排版只有一个来源 [`web/src/tokens.css`](web/src/tokens.css)（从参考产品截图逐像素采样：
canvas `#fcfcfb`、accent `#d97757` clay、hairline `#e8e6dc`，浅/深两套）。控件是只读令牌的手写原语层
（[`web/src/ui.jsx`](web/src/ui.jsx)），通知 / 命令面板 / 折线图也都在仓库内实现，因此没有第三方调色板、
半径或阴影与之打架。逐条取舍见 [docs/13](docs/13-CONSOLE-WIRING.md)。

## 功能矩阵

每条都写清**行为**与**入口**；端点细节见 [docs/15](docs/15-FEATURES.md)。

### 运行期（agent 内核）

| 能力 | 行为 | 入口 |
|---|---|---|
| **单 agent 循环** | 一个任务一个 goroutine：一轮一个 turn，结束后停在收件箱上（不退出），人工消息与子代理通知都能唤醒 | 创建任务时 `harness_mode=true` |
| **墙钟预算** | 每次 run 有硬上限，到点进入收尾阶段让 agent 落结论，而不是被半路杀掉 | `HARNESS_RUN_BUDGET`，默认 **20 分钟** |
| **步数上限** | 回合数用尽同样走收尾（提示词区分"时间到"与"步数到"） | 任务/agent 配置 |
| **按比例压缩** | 上下文按**窗口比例**压缩：0.7 摘要、0.5 清旧工具结果；摘要模板强制保留「已排除路径」 | `HARNESS_COMPACT_AUTO_RATIO` / `HARNESS_COMPACT_MICRO_RATIO` |
| **图谱记忆** | 资产 / 事实 / 发现落 PostgreSQL，每轮 `graph_overview` 重新注入；压缩丢过程不丢结论 | agent 工具 |
| **子代理并行** | 同一条回复里可派 `recon` / `verify`；各自独立 transcript，父轮次取消不会杀掉已在跑的 | `Agent` 工具（`run_in_background`） |
| **后台委派** | 立即返回委派 id，完成后投递 `<task-notification>` 到父会话，空闲任务自动唤醒 | 同上 |
| **委派审计** | 注册表 journal 落盘，进程重启后仍能列出历史委派（终态如实标注"已中断"） | `/api/harness/delegations/{id}` |

### 干预与治理

| 能力 | 行为 | 入口 |
|---|---|---|
| **审批门** | 规则 + 任务级审批门；命中 `ask` 时阻塞执行并落一条待决记录，人点了才放行 | 事件流内卡片 / 侧栏徽标 |
| **不可绕过** | `HarnessAgent.SetHooks` 挂的 `HookRunner` 是唯一否决点；**子代理继承同一套 hooks** | — |
| **审计轨迹** | 每次工具调用与结果成对落 `activity`，可回放；人执行的命令以 `human.sh` 入流 | `/api/exploration/activity` |
| **暂停 / 恢复 / 停止** | 暂停会**收尾在跑的轮次**再停，不腰斩回复 | 顶栏按钮 / `POST /api/tasks/{id}/control` |
| **纠偏原语** | steer / kill / 重排 frontier / replan / 编辑意图（S2 六原语） | `/api/tasks/{id}/intents/{iid}/…` |
| **就地接管** | 在任务工作区执行命令，输出可勾选交给 agent | 工作台 → 终端 |
| **范围登记** | 越界前必须 `add_task_scope` 并给 reason，落库可查 | agent 工具 / `/api/tasks/{id}/scope` |

### 可观测

| 能力 | 行为 | 入口 |
|---|---|---|
| **事件流** | AG-UI 协议，`?since=<seq>` 断点续传 + 15s 心跳；断线不丢事件、不重放 | `/api/agui/agent?task=N&since=<seq>` |
| **上下文占用** | 实时百分比 + 已用/窗口；≥70% 琥珀、≥90% 红 | composer 右侧 |
| **压缩计数** | SDK 真实事件优先，没有则按同轮内输入 token 回落推断，并把「推断」写进单位 | 用量页 |
| **预算曲线** | 每轮输入 token；压缩回落处画竖虚线 | `/api/harness/budget/{id}` |
| **子代理明细** | 列表带状态/耗时/「后台」徽标；点进去看它自己的每一步 | `/api/harness/workers/{id}`、`/api/harness/subagent/{agent}` |
| **就绪自检** | 数据库 / 模型 / 技能 / 浏览器 MCP 四项分别给原因与下一步 | 系统 → 就绪 |
| **能力注册表** | 全部供给面（builtin / MCP / skill）逐项列出可执行、并行安全、延迟解锁 | 系统 → 能力 |
| **回放与指纹** | 按 `rev` 区间重建一次运行，给出结构指纹、工具分布、拒绝事件 | 系统 → 回放、`/api/harness/replay` |

### 工作台与集成

| 能力 | 行为 | 入口 |
|---|---|---|
| **项目目录** | 运行绑定一个绝对路径作为根；**一个目录同一时刻只归一个活任务**（冲突 409 并点名占用者） | 工作台 → 项目 |
| **文件树 / DIFF** | 相对基线比对（A/M/D）；unified diff；二进制与超大文件拒绝内联并说明 | 工作台 → 文件 / DIFF |
| **浏览器会话** | 一任务一会话，Playwright 与 chrome-devtools 共用；崩溃重建、标签隔离、30 分钟空闲回收 | 右栏「浏览器」 |
| **F12 面** | console 错误按级别解析展示 + 最近 `mcp__*` 动作；**network 表后端未建模，界面明说** | 浏览器 → F12 |
| **截图管线** | 内容寻址存 `<data>/browser/<sha256>.png`，`/api/harness/shot/{hash}` 直出 | 浏览器 → 截图 |
| **MCP / 技能** | MCP server（stdio/http/sse）与 skill（agentskills.io 规范）在界面里增删、测连通 | 设置 → MCP / 技能 |
| **多模型** | 供应商在 UI 里配；故障转移链、任务级覆盖、按模型目录补上下文与档位 | 设置 → 供应商与模型 |
| **通知** | 邮件 / webhook 渠道 + 投递重试 + 摘要节流 | 设置 → 通知 |

### 交付

| 能力 | 行为 |
|---|---|
| **单二进制** | 控制台 `go:embed` 进二进制，目标机不需要 Node；数据落 PostgreSQL + 磁盘原文 |
| **自更新** | 换装后以约定退出码让守护脚本重启；连续启动失败自动回滚 |
| **跨平台** | Linux / macOS / Windows（Windows 下 shell 能力探测 Git Bash / busybox / pwsh） |
| **归档** | 任务可打包归档与恢复（含数据目录），带校验 |

## 快速开始

### 环境要求

| | 最低 | 说明 |
|---|---|---|
| Go | **1.26** | 仅源码构建需要 |
| PostgreSQL | **14+** | 首次启动自动建表（51 张），无需手工跑迁移 |
| Node | **20.19+ / 22.12+** | 仅**改前端**时需要（Vite 7 的引擎要求）；仓库已提交构建产物，纯 Go 构建不需要 |
| 浏览器 | Chrome / Chromium | 仅浏览器面板需要；由 `npx @playwright/mcp` 拉起 |
| 内存 | 运行数百 MB 级；**构建建议 ≥4 GB** | `make build-all` 同时跑 npm + vite + go，3.8 GB 机器会 OOM；低内存请分步构建（见[排障](#排障手动核对清单)） |

### 方式一：Docker Compose（最快）

```bash
git clone https://github.com/j5tv9nfzhn-hue/artex-harness.git
cd artex-harness

cat > .env <<'EOF'
POSTGRES_USER=artex
POSTGRES_PASSWORD=change-me-please
POSTGRES_DB=artex
ARTEX_TAG=latest
EOF

docker compose up -d          # postgres + artex
open http://localhost:8787/
```

> `docker-compose.yml` 里的 `artex` 服务默认指向**上游镜像** `autumn27/artex`。
> 要跑本仓库的构建：`./build.sh && docker build -t artex-harness .`，再把 `image:` 换成该 tag。

### 方式二：源码构建（推荐用于二次开发）

```bash
# 1) 数据库（已有 PostgreSQL 可跳过）
docker run -d --name artex-pg -p 5432:5432 \
  -e POSTGRES_USER=artex -e POSTGRES_PASSWORD=yourpass -e POSTGRES_DB=artex \
  postgres:16-alpine

# 2) 指向它（首次启动自动建表）
export ARTEX_PG_DSN="postgres://artex:yourpass@127.0.0.1:5432/artex?sslmode=disable"

# 3) 构建：控制台 + 后端 → 单个二进制
make build-all

# 4) 启动
./start.sh                      # 守护式启动（推荐；自更新靠它拉起）
# 或前台：./artex -addr :8787 -data ./data
```

### 方式三：只跑后端（前端产物已在仓库里）

```bash
export ARTEX_PG_DSN="postgres://…"
make build && ./artex -addr :8787 -data ./data
```

`server/console-app/` 是被提交进仓库的构建产物（`go:embed` 需要它在编译时存在），
所以纯 Go 构建不需要 `node_modules`。**改前端后必须 `make web` 再提交**，否则产物与源码不一致。

打开 `http://localhost:8787/` → 302 到 `/app/`。默认端口 `8787`；`-proxy :8788` 是流量记录代理
（`-proxy ""` 可关闭）。

## 首次运行走一遍

1. **设置管理密码** —— 首次打开 `/app/` 是「首次设置」表单，密码 ≥8 位且二次确认。
   用户名固定 `ARTEX`，不可改。忘记密码在服务器上运行 `./reset-password.sh`。
2. **配模型** —— 「设置 → 供应商与模型」填端点与密钥，点「测试」确认连通。
   **仓库不带任何预设供应商**；也可以只设 `ARTEX_LLM_PROVIDER/MODEL/BASE_URL` 环境变量。
3. **看就绪** —— 右栏「系统 → 就绪」四项应全绿：PostgreSQL / 模型端点 / 技能目录 / 浏览器 MCP。
   红的项会写明原因与下一步（这是排障第一站）。
4. **建一个运行** —— 侧栏「新建任务」→ 目标必填、写清范围与禁止项（它们会进提示词），
   「HARNESS 单 agent」保持开启。
5. **看它动手** —— 中栏事件流：思考块、工具卡片（名字/参数/输出成对）、连续同类合并成 `Bash × N`。
6. **审批一次** —— 命中审批规则时事件流里出现卡片，点「允许 / 拒绝」。
   拒绝会让 agent 收到"被拒绝 + 原因"，它自己换机理。
7. **审改动** —— 右栏「工作台」：文件树看它写了什么、DIFF 看相对基线的改动、终端可自己跑一条命令。
8. **看用量** —— 「用量」页：上下文占用、压缩次数（真实事件 / 推断分开标）、每轮输入曲线。
9. **收口** —— 结论登记为漏洞时必须带证据；「系统 → 取证」列出有绑定与无绑定的发现（只报告，不阻断）。

## 配置参考

配置优先级：**命令行 flag > 环境变量 > `config.json` > 内置默认**。

### 命令行

| Flag | 默认 | 说明 |
|---|---|---|
| `-addr` | `:8787` | HTTP 监听地址 |
| `-data` | 可执行文件同级 `data/` | 数据目录（工作区、transcript、截图、证据） |
| `-proxy` | `127.0.0.1:8788` | 流量记录代理；传 `""` 关闭 |

### 环境变量

| 变量 | 默认 | 作用 |
|---|---|---|
| `ARTEX_PG_DSN` | 空 | PostgreSQL DSN；**优先级高于 `config.json` 的 database** |
| `ARTEX_CONFIG` | 可执行文件同级 `config.json` | 配置文件路径 |
| `ARTEX_SKILL_DIR` | 可执行文件同级 `skills/` | skill 根目录（每个子目录一个 skill，含 `SKILL.md`） |
| `ARTEX_LLM_PROVIDER` / `ARTEX_LLM_MODEL` / `ARTEX_LLM_BASE_URL` / `ARTEX_LLM_PROXY` | 空 | 无 UI 时的模型端点引导；留空则引擎 idle |
| `ARTEX_LLM_STREAM` | 跟随供应商 | 强制流式 / 非流式 |
| `ARTEX_BTW_MAX_OUTPUT_TOKENS` | 供应商默认 | 输出 token 上限覆盖 |
| `HARNESS_RUN_BUDGET` | `20m` | **一次 run** 的墙钟上限（`90s` / `20m` / 纯秒数） |
| `HARNESS_SUBAGENT_TIMEOUT` | `10m` | 单个子代理预算（脱钩父轮次取消后仍受此限制） |
| `HARNESS_COMPACT_AUTO_RATIO` | `0.7` | 摘要压缩触发比例（占**有效窗口**） |
| `HARNESS_COMPACT_MICRO_RATIO` | `0.5` | 清旧工具结果的触发比例 |
| `HARNESS_SHUTDOWN_DRAIN` | 默认开 | 关停前先给在跑的轮次收尾 |
| `ARTEX_NOTIFY_ALLOW_LOCAL` | 空 | 允许通知投递到回环/内网地址（默认拦，因为这是 SSRF 面；本机 SMTP 中继常需显式打开） |
| `ARTEX_UPDATE_GITHUB_TOKEN` | 空 | 自更新读取私有仓库 Release 用 |

### `config.json`（可选）

只放**不适合放进数据库**的东西：`database`（分字段或 `dsn` 二选一）与 `skill_dir`。
其余（模型、MCP、技能、审批规则、通知渠道）都在数据库里，由界面维护。
模板见 [`config.example.json`](config.example.json)。

### 运行期参数（代码里的常量，改动需重新编译）

| 参数 | 值 | 位置 |
|---|---|---|
| 浏览器会话空闲回收 | 30 分钟 | `harness/browser/manager.go` |
| 每会话标签上限 | 8 | 同上 |
| MCP 启动超时 | 20 秒 | 同上 |
| 截图保留 | 24 小时 / 最多 2000 张 | `server/harness_browser_manager.go` |
| 委派隔离目录保留 | 72 小时（`harness_delegation_retention_hours`，`0` 关闭） | `db/settings.go` |
| 工具输出内联上限 | 超出即溢出落盘并留指针 | `agent/` 工具装配 |

### 界面里维护的设置

`llm_pool_enabled`（故障转移）、`harness_browser`（浏览器自动化）、`harness_delegation_retention_hours`、
`notify_enabled` / `notify_digest_interval_min` / `notify_public_base_url`、`task_concurrency_enabled` /
`task_concurrency_limit`、`python_interpreter`、`git_bash_path`、`global_proxy`、`llm_record`（LLM 原文录制）等。
完整清单见 [docs/15 §十](docs/15-FEATURES.md)。

## 架构

**一个进程，两种监听，一个静态资源根。**

```
                     ┌─────────────────────────────────────┐
  浏览器 ──HTTP─────▶ │ /app/      内嵌控制台（go:embed）      │
                     │ /api/*     JSON API（290 条路由）      │
                     │ /api/agui/agent, /api/harness/stream │ SSE（?since= 续传）
                     └──────────────┬──────────────────────┘
                                    │
                     ┌──────────────▼──────────────────────┐
   目标站点 ─mitm──▶  │ -proxy :8788  流量记录代理（可关）     │
                     └──────────────┬──────────────────────┘
                                    │
        ┌───────────────────────────┼───────────────────────────┐
        ▼                           ▼                           ▼
  PostgreSQL 14+            <data>/ 磁盘                 外部子进程
  （51 张表：任务/活动/        （工作区/transcript/          （Bash / MCP server
    图谱/审批/通知/发现）          截图/证据）                   / npx playwright）
```

### 分层

```
web/            React 19 + Vite 控制台（约 6.7k 行）→ 产物内嵌进二进制
    ↓ go:embed
server/         HTTP 层：路由 / 鉴权 / DTO / AG-UI / 引擎调度 / 观测面
agent/          LLM 接入与运行编排：provider、提示词、压缩钩子、工具装配
harness/        运行期能力：协议、浏览器会话、委派、diff、回放、评测、能力注册表
db/             PostgreSQL schema + 各领域 store（资产图 / 探索图 / 活动 / 对话）
third_party/    vendored agent SDK（带本地补丁）
```

| 目录 | 规模（含测试） | 职责 | 不该出现在这里 |
|---|---|---|---|
| `cmd/artex/` | 0.2k / 1 文件 | 进程入口：flag、信号、自更新引导、优雅关停 | 业务逻辑 |
| `server/` | 41k / 136 | 路由、JWT、DTO、SSE、引擎调度、HARNESS 观测面 | SQL 细节、提示词正文 |
| `db/` | 32k / 103 | `schema.sql` + 各领域 store | HTTP 概念 |
| `agent/` | 12k / 62 | provider、提示词、压缩、工具装配 | 路由、鉴权 |
| `harness/` | 6.2k / 32 | 运行期能力包 | 数据库 schema |
| `web/` | 6.7k / 21 | 控制台，产物写 `server/console-app/` | 业务规则 |

完整模块表（含 `intercept` / `guard` / `traffic` / `notify` / `selfupdate` / `llmpool` / `llmrec` /
`sidequestion` / `evidence` / `mcphttp` / `eval`）与该表的**行数口径说明**见
[docs/16 §二](docs/16-ARCHITECTURE-DETAIL.md)。

### 两条产品线，不要混

| | 对话（conversation） | 运行（task） |
|---|---|---|
| 入口 | 侧栏「新对话」/ `⌘N` / 命令面板 | 侧栏「新建任务」/ 顶栏 / 命令面板 |
| 必需字段 | 无 | **goal 必填** |
| 何时开跑 | 发出第一条消息 | 创建即排队 |
| 数据表 | `conversations` / `conversation_activities` | `tasks` / `activity` |
| 事件通道 | 轮询 `?since=N` | SSE `?since=<seq>` |
| 预算 / 目标判定 / 四个面板 | 无 | 有 |

### 一次发言的数据流

```
浏览器 POST /api/conversations/{id}/messages
   → engine 起一轮 agent（norma）
   → 每个事件写 activity（summary=首行索引，detail=完整正文）
   → 前端轮询 ?since=N（对话）或订阅 /api/agui/agent（任务）
```

**关键约定**：`activity.summary` 只是 `firstLine(s, 200/400)` 的索引，**完整正文在 `detail`**。
任何要展示正文的地方都必须取 `detail`，否则用户看到的是被砍掉第一行之后的内容。

### 磁盘布局

```
<data>/
├── tasks/<taskID>/          任务自有工作区（未绑定项目目录时）
├── transcripts/             会话 transcript（含 subagents/ sidechain）
├── baselines/<taskID>/      与基线比对的参照（绑定项目目录时移到这里，不污染用户目录）
├── checkpoints/<taskID>/    每轮文件快照与回滚
├── delegations/<taskID>/    委派结果（内容寻址）+ 隔离目录 + journal
├── evidence/                取证 blob
├── browser/<sha256>.png     浏览器截图（内容寻址）
└── sessions/                会话级持久状态
```

### 必须守住的不变量

1. **审批门不能绕**：`HarnessAgent.SetHooks` 挂的 `HookRunner` 是唯一否决点；子代理继承同一份 hooks。
2. **上下文是工作台，不是档案柜**：结论落图谱，每轮重新注入；压缩阈值按窗口比例，不是固定 token 数。
3. **失败必须可见**：不允许静默吞异常；每个失败态都要有原因 + 重试入口。
4. **不做假控件**：后端没有的能力，界面禁用并写明原因。
5. **一个视觉来源**：`web/src/tokens.css`；不引入自带调色板/半径/阴影的组件库。
6. **进程内状态要么落盘、要么标注**：委派有 journal（重启可审计但不可再停）；浏览器会话是纯内存。

## API 概览

**290 条 `/api` 路由，36 个分组。** 约定：

- 鉴权：除 `/api/auth/*` 与 `/api/health` 外都要 `Authorization: Bearer <jwt>`（登录后前端自动带上）。
- 错误：统一信封 `{"error":"一句话说明"}`，HTTP 状态码表意（400 参数、401 未授权、404 不存在、409 冲突、503 数据源不可用）。
- 分页：多数列表用游标（`before=` / `since=` / `next_before`），不用 offset（offset 在长表上会漂移）。

| 分组 | 条数 | 代表端点 |
|---|---|---|
| `tasks` | 51 | `POST /api/tasks`、`POST /api/tasks/{id}/control`、`PUT /api/tasks/{id}/project` |
| `harness` | 33 | `/api/harness/{ready,capabilities,files,diff,budget,workers,delegations,replay,checkpoints,browser}` |
| `exploration` | 24 | `GET /api/exploration/{frontier,intents,graph,activity,findings}` |
| `agents` | 19 | `/api/agents/{key}/{config,prompt,triggers,visibility}` |
| `llm` | 18 | `/api/llm/{profiles,records,models,model-catalog}` |
| `intercept` | 17 | `GET /api/intercept/pending`、`POST /api/intercept/pending/{id}/decide` |
| `skills` | 12 | `/api/skills/{name}/files/{file...}` |
| `conversations` | 10 | `POST /api/conversations/{id}/messages` |
| `notify` `workspace` `traffic` `tools` | 8 / 7 / 7 / 7 | 通知渠道、工作区文件、流量取证、工具开关 |
| 其余 24 组 | — | `assets` `companies` `settings` `task-archives` `task-templates` `update` `auth` `tokens` … |

### 事件流契约（AG-UI）

```
GET /api/agui/agent?task=N&since=<seq>&follow=1
```

| 事件 | 语义 |
|---|---|
| `RUN_STARTED` / `RUN_FINISHED` | 一次连接的重放 + 跟随窗口 |
| `STEP_STARTED` / `STEP_FINISHED` | 轮次边界（一行一个 turn） |
| `TEXT_MESSAGE_*` | `kind=text` 与 `kind=thinking` |
| `TOOL_CALL_START` / `ARGS` / `END` / `RESULT` | `tool_use` 与 `tool_result` 配对（按 `tool_use_id`） |
| `INTERCEPT_REQUEST` | 审批请求（卡片可点，直接调 decide 端点） |
| 心跳 | 15 秒一次，配合 `?since=` 做断线续传 |

## 可观测性

| 观测点 | 怎么看 |
|---|---|
| 上下文压力 | 输入框右侧实时百分比 + 已用/窗口；≥70% 琥珀、≥90% 红 |
| 压缩 | 按窗口比例触发；**SDK 真实事件与推断分开**，推断值单位为「次（推断）」 |
| 子代理 | 右栏「子代理」列出进行中/已完成/失败 + 耗时；点进去看它自己的每一步 |
| 预算 | 右栏「用量」与 `/api/harness/budget/{id}`：窗口、最近输入、压缩次数、轮次曲线 |
| 浏览器 / F12 | 一任务一会话、重建耗时、标签、console 错误分级、最近截图 |
| 就绪自检 | 「系统 → 就绪」：四项分别给出原因与下一步 |
| 能力与取证 | 「系统 → 能力」列出全部供给面；「取证」列出有绑定与无绑定的发现 |
| 回放 | 「系统 → 回放」：结构指纹、工具分布、拒绝事件、Markdown 原文（与 harness-gate 同源） |
| 事件 | `/api/agui/agent?task=N&since=<seq>&follow=1` |
| 日志 | `GET /api/logs` 与 `GET /api/logs/stream`（内存环形 + 落库历史） |
| 令牌 | `/api/tokens/{daily,usage}`、`/api/llm/records`（含原文录制） |

界面原则：**后端没有的能力，界面禁用并写明原因，不做假控件**。例如 F12 的 network 表后端尚未建模，
面板就直接写明「不在此版本」，而不是画一个空表格。

## 安全模型

这是一个**在真实系统上执行命令**的工具，安全边界必须写清楚。

| 面 | 现状 |
|---|---|
| **鉴权** | 单账户（用户名固定 `ARTEX`）+ bcrypt 密码 + JWT；首个登录前必须先设密码 |
| **审批门** | 规则匹配（正则/工具名/参数）+ 可选 LLM judge；命中 `ask` 必须人工放行才执行；子代理继承同一套 hooks |
| **审计** | 每次工具调用与结果成对落库（含被拒绝的那次及原因）；可回放、可导出 |
| **范围** | 任务目标 + 已登记的 scope 就是授权边界；越界需显式登记并给依据 |
| **密钥** | LLM 密钥存库，接口**不回显**；JWT 密钥来自可执行文件同级 `jwt.key`（权限 0600） |
| **上传** | 原子落盘（临时文件 + rename），不会留半截文件 |
| **DSN 脱敏** | 日志与错误里不打印连接串密码 |
| **网络** | 通知投递默认拒绝回环/内网地址（SSRF 面），需要本机 SMTP 中继时显式设 `ARTEX_NOTIFY_ALLOW_LOCAL=1` |
| **不做什么** | 不提供沙箱/容器隔离——agent 的命令以**运行本服务的用户权限**执行，请自行用容器或专用主机隔离 |

> 部署建议：跑在专用主机或容器里，`-addr` 绑内网，前面再套一层反向代理做 TLS 与来源限制。
> 界面没有多用户与角色，别把它直接暴露到公网。

## 运维手册

### 拓扑与端口

| 端口 | 用途 | 默认绑定 |
|---|---|---|
| `8787` | 控制台 + API | `:8787`（全接口） |
| `8788` | 流量记录代理（mitm） | `127.0.0.1:8788` |

### 备份

需要备份两处，缺一不可：

1. **PostgreSQL**：任务、活动、图谱、发现、审批、通知、设置全在这里。
2. **`<data>/`**：transcript、工作区文件、基线、检查点、委派结果、截图、证据 blob。
   数据库里有指针（例如 `output_path`），只备份库会拿到一堆指向空文件的记录。

### 升级与回滚

`start.sh` 是守护脚本，自更新靠"退出码 → 重新拉起"完成：

```bash
./artex -h                  # 列出全部 flag（默认值即内置默认）
curl -s localhost:8787/api/health   # {"ok":true,"version":"..."}
# 界面「设置 → 更新」或 POST /api/update/apply
```

换装后进程以约定退出码退出，守护脚本拉起新版本；**连续启动失败会自动回滚到上一版**。
不走守护脚本（直接前台跑）时，自更新只会换文件，需要你自己重启。

### 保留策略

| 数据 | 保留 | 可调 |
|---|---|---|
| 委派隔离目录 | 72 小时 | `harness_delegation_retention_hours`（`0` 关闭） |
| 委派结果正文 | 永久（内容寻址，重复结果只存一份） | — |
| 浏览器截图 | 24 小时 / 最多 2000 张 | 代码常量 |
| 检查点 | 随任务 | 界面可删 |
| 活动 / 对话 | 永久 | 任务删除时级联 |

### 并发与容量

- **单进程**：任务级并发闸门（`task_concurrency_enabled` / `task_concurrency_limit`）；
  工具按 FIFO 调度，声明 `concurrency_safe` 的才并行。
- **委派注册表与浏览器会话是进程内状态**：重启后委派仍可列出（journal），但**不可再停止**；
  浏览器会话重启即失，下一次工具调用自动重建。
- **上下文**：窗口由模型目录决定，压缩按比例；长任务靠压缩 + 图谱记忆维持，不靠堆 token。

## 测试与质量门

```bash
make            # 列出全部目标
make build-all  # 控制台 + 后端
make test       # 单元测试（不碰数据库）
make vet        # 静态检查
ARTEX_TEST_DB=<库名> make test-db   # 需要 PG 的测试：会重建 schema，必须显式指名测试库
```

| 项 | 现状 |
|---|---|
| 测试函数 | **819** 个，分布在 **191** 个 `_test.go` |
| 无数据库的包 | `agent` `harness` `config` `guard` 等 **14 个包**全绿（`make test`） |
| 需要数据库 | `db` / `server` 共 400+ 用例，**必须**显式 `ARTEX_TEST_DB`，且与 DSN 库名一致才执行 |
| 离线自证 | `go run ./eval/cmd/harness-smoke`（假 provider，无网络无 key） |
| 行为门禁 | `go run ./eval/cmd/harness-gate`：双跑对照，硬/软判据；改一句拒绝话术就会红 |
| 前端 | `vite build` 确定性（两次构建 hash 一致），产物与源码同步由 CI 校验 |
| CI 作业 | `gate` → `plan` → `signals` → `frontend` → `build` → `finalize` →（有 tag 时）`release` |

**测试库守卫**：`ARTEX_TEST_DB` 与 `ARTEX_PG_DSN` 里的库名不一致时**先跳过、不重建 schema**——
这条是因为踩过"跑测试把开发库清了"的坑才加的。

## 排障：手动核对清单

| 症状 | 先看 | 常见原因 / 处理 |
|---|---|---|
| 控制台能打开，但事件流一条不动 | 「系统 → 就绪」的 `llm` 项 | `profiles=0` → 引擎 idle。去「供应商与模型」加端点并激活 |
| 页面满屏 401 | 浏览器 `localStorage` 里的 `artex_token` | 令牌过期或 `jwt.key` 换过 → 重新登录；服务端密钥变了要清 local storage |
| 登录页说"无法读取登录状态" | 服务端日志 + PG 连通性 | 数据库不可用时服务端回 503 而**不是** `initialized:false`（避免误覆盖已设密码） |
| 浏览器面板「未启动 / 未连通」 | 是否有 `npx`；`chrome-devtools` 是否要 `--remote-debugging-port=9222` | 装 Node 或镜像里预装；点「重建会话」看耗时与错误 |
| 任务卡在审批 | 侧栏盾牌徽标 / `GET /api/intercept/pending` | 有 `ask` 命中，去点允许或拒绝；拒绝也要点（不点就一直等） |
| 子代理一直「运行中」 | `/api/harness/delegations/{id}` 的 `pending` | 通知未投递时留在队列（人工消息优先），恢复任务后会补投 |
| 工作台 DIFF 显示「无基线」 | 任务是否 HARNESS 模式、是否新建 | 基线在首轮前建立；历史任务下一轮会自动补 |
| 窄窗口右栏变成竖条 | 见 [docs/12 §四之二](docs/12-SPRINT4-WORKBENCH.md) | 已在 `b07fcc3` 前修复；升级后仍然出现请报 issue |
| `make test-db` 拒绝执行 | `ARTEX_TEST_DB` 是否设置且与 DSN 一致 | 这是保护，不是 bug：它不让你误清开发库 |
| 构建时 OOM | 机器内存 | `make build-all` 会同时跑 npm + vite + go；低内存请分步：`cd web && npx vite build` 再 `go build` |

## 兼容性与已知上限

| 项 | 支持 / 上限 |
|---|---|
| 操作系统 | Linux / macOS / Windows（Windows 下自动探测 Git Bash / busybox / pwsh） |
| PostgreSQL | 14+（用 `ON CONFLICT`、`jsonb`、部分索引；首次启动建表） |
| 模型供应商 | 任意 OpenAI 兼容 / Anthropic 兼容端点；仓库不预设供应商 |
| 浏览器 | Chrome / Chromium（Playwright MCP）；F12 面需 `chrome-devtools-mcp` |
| 单实例 | **单进程**，不支持多副本共享一个数据库（进程内有委派注册表与会话状态） |
| 多租户 | 不支持：单账户，没有组织/配额/角色 |
| 沙箱 | 不提供：命令以服务进程权限执行 |
| 规模 | **未做容量压测**：单进程 + 单库，上限取决于 PostgreSQL 与磁盘；进程内状态（委派/浏览器会话）是横向扩展的主要障碍 |
| 深色主题 | 令牌与首帧切换已实现，端到端截图目前只覆盖浅色 |

## 定位对比

| | artex-harness | 传统扫描器 | planner/worker 流水线 | 通用 coding agent |
|---|---|---|---|---|
| 决策权 | 一个 agent，全程对话 | 规则/插件 | 规划者派活 | 一个 agent |
| 过程可干预 | 运行中纠偏 / 暂停 / 接管执行一步 | 基本不能 | 只能改意图队列 | 停/继续 |
| 留痕 | 工具调用与结果成对、可回放、可导指纹 | 报告级 | 意图级 | 对话级 |
| 结论判定 | 必须挂证据，证据完整性可查 | 插件自报 | 依赖 worker 自述 | 无 |
| 适用场景 | 授权测试、需要人盯着的高风险动作 | 面广、浅 | 大规模并行侦察 | 写代码 |

不是说谁更好：**它的取舍是把人的杠杆做进循环里**，代价是单实例、单账户、要人参与。

## 进度与路线

已完成（阶段记录见 [docs/stages/](docs/stages/) 与 [docs/04](docs/04-ROADMAP.md)）：
S0 仓库与底座 → S1 协议与 revision → S2 干预原语 + 控制台 MVP → S3 回放与一致性评测 →
S4 会话栈收敛与能力注册表 → S5 控制台成为唯一界面 → Sprint 3 后台委派 → Sprint 4 工作台 →
浏览器会话 / 项目目录 / F12 观测面 / 委派持久化。

**明确还没做的**（不写"即将支持"）：

| 缺口 | 原因 |
|---|---|
| F12 的 **network** 结构化面板 | 后端只按 console 级别解析 `chrome-devtools` 输出，请求/响应表尚未建模 |
| 侧栏**项目树 / 多项目** | 数据模型是"一个运行 = 一个工作区目录"且互斥，做树要先引入 project 实体并重做互斥语义 |
| 代码块**语法高亮**与语言标签 | 移除了写死浅色的 GitHub 主题；恢复需要一份读 `data-theme` 的自有配色映射 |
| 多用户 / 角色 / 配额 | 单账户设计，不在当前范围 |
| 多副本部署 | 进程内状态未外置 |
| 真实压缩事件的端到端观测 | 需要一次跨阈值的长跑（当前计数以推断兜底并标注） |

## 文档索引

三份**主要阅读文档**（按"想知道什么"排序）：

| 想知道什么 | 读这份 |
|---|---|
| 有哪些功能、每项怎么点、落到哪个端点 | [docs/15-FEATURES.md](docs/15-FEATURES.md) — 功能清单（行为 · 入口 · 使用方式） |
| 代码分成哪些块、谁依赖谁、一次请求怎么流动 | [docs/16-ARCHITECTURE-DETAIL.md](docs/16-ARCHITECTURE-DETAIL.md) — 模块划分 / 依赖关系 / 数据流 |
| 参考截图画了什么、我们做到哪、没做的为什么 | [docs/17-REFERENCE-UI-MAPPING.md](docs/17-REFERENCE-UI-MAPPING.md) — 界面逐项对照 |
| 界面长什么样、这些图怎么复算 | [docs/screenshots/README.md](docs/screenshots/README.md) — 真实截图索引 + 种子/抓图脚本 |

其余文档：

| 文档 | 内容 |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | 分层、目录职责、两条产品线、数据流、不变量（16 的快速版） |
| [CONTRIBUTING.md](CONTRIBUTING.md) | 环境、命令、提交约定、加接口/加界面步骤 |
| [docs/03-ARCHITECTURE.md](docs/03-ARCHITECTURE.md) | 设计取舍 |
| [docs/04-ROADMAP.md](docs/04-ROADMAP.md) | 阶段路线与验收判据 |
| [docs/07-HARNESS-RUNTIME.md](docs/07-HARNESS-RUNTIME.md) | HARNESS 运行期（无 planner/worker 的理由） |
| [docs/09-LONGSESSION-BROWSER-PROJECT.md](docs/09-LONGSESSION-BROWSER-PROJECT.md) | 长会话预算 / 浏览器会话管理器 / 截图管线 / 项目目录 |
| [docs/12-SPRINT4-WORKBENCH.md](docs/12-SPRINT4-WORKBENCH.md) | 工作台（文件 / DIFF / 终端）与窄窗口布局缺陷 |
| [docs/13-CONSOLE-WIRING.md](docs/13-CONSOLE-WIRING.md) | 控制台接线、主题统一、委派持久化与目录 TTL |
| [docs/adr/](docs/adr/) | 5 篇 ADR：业主决策、以 ARTEX 为执行底座、能力边界、无 planner/worker、成熟度与借鉴边界 |
| [docs/stages/](docs/stages/) | S1–S5 每阶段的交付与验收记录 |
| [docs/06-GAP-ANALYSIS.md](docs/06-GAP-ANALYSIS.md) | 与成熟 harness 的差距评估（含对四条猜想的事实核查） |
| [DISCLAIMER.md](DISCLAIMER.md) | 使用条款 |
| [PROVENANCE.md](PROVENANCE.md) | 来源台账：上游 commit、复制/排除清单、相对上游的每一处改动 |

## 参与开发

见 [CONTRIBUTING.md](CONTRIBUTING.md)。三条硬要求：

1. **改前端必须 `make web` 并提交产物**（`server/console-app/` 被 `go:embed`，产物与源码不一致 CI 会红）。
2. **相对上游的每一处代码改动都要记进 `PROVENANCE.md`**（这是许可证与可审计性要求）。
3. **不新增"假控件"**：后端没有的能力，界面禁用并写明原因。

提交信息用 `<type>(<scope>): <subject>`；测试库相关改动请确认在 `ARTEX_TEST_DB` 下通过。

## 来源与许可

执行底座来自 [ARTEX](https://github.com/j5tv9nfzhn-hue/ARTEX)（AGPL-3.0），在其之上建设
harness 层：单 agent 运行期、事件流、子代理、预算与压缩策略、控制台。

本项目沿用 **AGPL-3.0**。改编/部署为网络服务时，须按该许可证开放对应源码。
上游说明保留在 [README-ARTEX.md](README-ARTEX.md)，来源清单见 [PROVENANCE.md](PROVENANCE.md)。

界面视觉参考了 Claude Code Desktop 的设计语言（第三方开源复刻项目 `dsh-claude-style`，MIT）；
**只取设计语言，不取品牌标识**——品牌标记、吉祥物等一律未采用，令牌来源与取舍见
[docs/design/DESIGN.md](docs/design/DESIGN.md) 与 [docs/17](docs/17-REFERENCE-UI-MAPPING.md)。
