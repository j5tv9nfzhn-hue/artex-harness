# 架构总览

> 面向新加入的人（和 AI）。读完这一页应该能回答三个问题：**东西在哪、为什么这样分层、
> 我要动的地方归谁管**。设计取舍的来龙去脉在 `docs/`，这里是地图不是论文。

## 一句话

一个**单进程 Go 服务**：HTTP API + 后台 agent 引擎 + 内嵌的 React 控制台。
数据落 PostgreSQL（任务/活动/图谱），文件落 `data/`（工作区、transcript、证据截图）。

## 分层

```
┌──────────────────────────────────────────────────────────────┐
│  web/            React 19 + Vite 控制台（约 2.5k 行）          │
│                  构建产物 → server/console-app/ → go:embed     │
├──────────────────────────────────────────────────────────────┤
│  server/         HTTP 层：路由、鉴权、DTO、SSE/AG-UI、引擎调度  │
│  ├ engine.go       任务生命周期、并发闸门、LLM 调用计数         │
│  ├ agui.go         活动流 → AG-UI 事件（since 续传 + 心跳）     │
│  ├ conversations.go 对话（无 goal 的聊天）与任务（有 goal 的运行）│
│  └ harness_*.go    HARNESS 观测面：文件、预算、子代理、回放      │
├──────────────────────────────────────────────────────────────┤
│  agent/          LLM 接入与运行编排（provider、提示词、压缩钩子）│
│  harness/        运行期能力（浏览器会话、diff 基线、评测闸门）    │
├──────────────────────────────────────────────────────────────┤
│  db/             PostgreSQL schema.sql + 各领域 store           │
├──────────────────────────────────────────────────────────────┤
│  third_party/norma/   vendored 的 agent SDK（本地打过补丁）      │
└──────────────────────────────────────────────────────────────┘
```

## 目录职责（改代码前先认领）

| 目录 | 归它管 | 不该出现在这里 |
|---|---|---|
| `cmd/artex/` | 进程入口：flag、信号、优雅关停（先收尾在跑的轮次再退出） | 业务逻辑 |
| `server/` | HTTP 路由、鉴权、DTO 映射、SSE 广播、引擎调度 | SQL 细节、提示词 |
| `db/` | `schema.sql`、迁移（`ALTER ... IF NOT EXISTS` 追加在尾部）、各 store | HTTP 概念 |
| `agent/` | provider 构建、提示词、压缩策略、工具装配 | 路由、鉴权 |
| `harness/` | 运行期能力：浏览器会话、diff 基线、gate | 数据库 schema |
| `web/` | 控制台（React + Radix Themes） | 业务规则 |
| `docs/` | 设计与 ADR（`docs/adr/NNNN-*.md`） | 教程 |
| `eval/ evidence/` | 评测与取证相关 | 运行时代码 |

## 两条产品线，不要混

- **对话（conversation）**：`POST /api/conversations`，**没有 goal**，发第一条消息才开跑。
  适合"聊两句"。
- **运行（task）**：`POST /api/tasks`，**必须有 goal**，带目标判定、预算、子代理。
  `harness_mode=true` 才是 HARNESS 的单 agent 对话式执行；`false` 是旧 ARTEX
  planner/worker 模式，属于另一条产品线，控制台默认不展示。

数据模型上两者是不同表（`conversations` / `tasks`），不要为了省事把对话塞进任务。

## 数据流（一次发言）

```
浏览器 POST /api/conversations/{id}/messages
   → engine 起一轮 agent（norma）
   → 每个事件写 activity（summary=首行索引，detail=完整正文）
   → 前端轮询 ?since=N（对话）或订阅 /api/agui/agent（任务）
```

**关键约定**：`activity.summary` 只是 `firstLine(s, 200/400)` 的索引，**完整正文在
`detail`**。任何要展示正文的地方都必须取 detail，否则用户看到的是被砍掉第一行之后的内容。

## 必须守住的几条不变量

1. **审批门不能绕**：`HarnessAgent.SetHooks` 挂的 `HookRunner` 是唯一否决点；子代理也必须
   继承同一份 hooks（否则就等于没有 gate）。
2. **上下文是工作台，不是档案柜**：资产/事实/发现落图谱，每轮 `graph_overview` 重新注入；
   压缩阈值按窗口比例（默认 auto 0.7 / micro 0.5），不是固定 token 数。
3. **失败必须可见**：不允许静默吞异常；前端每个失败态都要有原因 + 重试入口。
4. **不做假控件**：后端没有的能力，界面上禁用并写明原因，不要伪造开关。

## 构建与产物

- `server/console-app/` 是**被提交进仓库的构建产物**，因为 `go:embed` 需要它在编译时存在，
  这样纯 Go 构建不需要 node_modules。改前端后必须 `make web` 再提交。
- `web/node_modules/`、`data/`、`jwt.key`、`*.log`、编译出的 `artex` 一律不进版本库。

## 相关文档

- `docs/03-ARCHITECTURE.md` — 设计取舍
- `docs/07-HARNESS-RUNTIME.md` — HARNESS 运行期
- `docs/adr/` — 已定型的决策（含"以 ARTEX 为执行底座"的由来）
- `README-ARTEX.md` — 上游 ARTEX 的原始说明（保留作为来源说明）
