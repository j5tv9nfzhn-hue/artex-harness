# 架构总览

> 面向新加入的人（和 AI）。读完这一页应该能回答三个问题：**东西在哪、为什么这样分层、
> 我要动的地方归谁管**。设计取舍的来龙去脉在 `docs/`，这里是地图不是论文。

## 一句话

一个**单进程 Go 服务**：HTTP API + 后台 agent 引擎 + 内嵌的 React 控制台。
数据落 PostgreSQL（任务/活动/图谱），文件落 `data/`（工作区、transcript、证据截图）。

## 分层

```
┌──────────────────────────────────────────────────────────────┐
│  web/            React 19 + Vite 控制台（约 6.7k 行）          │
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
| `web/` | React 19 + Vite 控制台，产物写 `server/console-app/` 由 `go:embed` 进二进制 |
| `web/src/tokens.css` | **唯一视觉来源**：CLAUDE 主题令牌（浅/深两套，从参考截图测量） |
| `web/src/ui.jsx` | 手写原语层：只读令牌，不自带调色板/半径/阴影 |
| `web/src/{toast,Palette,Sparkline}.jsx` | 自有通知 / 命令面板 / 折线图（替代 sonner / cmdk / recharts） |
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
   错误信封统一由 `web/src/lib/auth.js` 的 `errorText` 解析成一句话——**不要把
   `{"error":"…"}` 原始 JSON 直接显示给操作者**。
4. **不做假控件**：后端没有的能力，界面上禁用并写明原因，不要伪造开关。
   典型例子：F12 的 network 表后端没有建模，面板就直接写"不在此版本"，不画空表格。
5. **界面只有一个视觉来源**：`web/src/tokens.css`。新控件从这里取令牌，不要再引入
   带自己调色板/半径/阴影的组件库——每一次引入都要在 `styles.css` 里逐条打回去，
   这也是"零散库观感"的来源。
6. **进程内状态要么落盘、要么标注**：委派注册表现在有 journal（重启仍可审计），
   但仍不可跨进程停止；浏览器会话是纯内存（重启即失）。新增这类状态时二选一。

## 运行期子系统（`harness/browser`）

浏览器不是"调一个 MCP 工具"那么简单，它有自己的生命周期，因此单独成包：

| 关注点 | 做法 |
|---|---|
| 一会话一任务 | `Manager.Ensure(taskID)` 幂等；Playwright 与 chrome-devtools **同条目**，所以 F12 连的是同一上下文 |
| 崩溃恢复 | 工具调用失败 → `MarkBroken` → 下次 `Ensure` 只重建那一个服务器；两者互不牵连 |
| 标签 | `OpenTab/CloseTab`，`MaxTabs` 上限，标签之间导航互不覆盖 |
| 回收 | `GC(now)` 关掉空闲超 30 分钟的会话；截图另有 24h TTL + 2000 张上限 |
| 截图 | SDK 的 content union 没有 image 类型 → `rawclient.go` 用最小 stdio JSON-RPC 直取 PNG → 内容寻址存 `<data>/browser/<sha256>.png` → `/api/harness/shot/{hash}` |

## 构建与产物

- `server/console-app/` 是**被提交进仓库的构建产物**，因为 `go:embed` 需要它在编译时存在，
  这样纯 Go 构建不需要 node_modules。改前端后必须 `make web` 再提交。
- `web/node_modules/`、`data/`、`jwt.key`、`*.log`、编译出的 `artex` 一律不进版本库。

## 相关文档

- `docs/03-ARCHITECTURE.md` — 设计取舍
- `docs/07-HARNESS-RUNTIME.md` — HARNESS 运行期
- `docs/adr/` — 已定型的决策（含"以 ARTEX 为执行底座"的由来）
- `README-ARTEX.md` — 上游 ARTEX 的原始说明（保留作为来源说明）
