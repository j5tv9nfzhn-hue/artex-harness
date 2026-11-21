# 06 · HARNESS 完整性差距评估（含对外部项目的事实核查）

> **2026-11-21 修订注（S6-TRIM 之后读本文档时注意）**：本文是 S1 时期的历史分析，正文不重写。但其中引用的几个后端接口已在 S6-TRIM 删除，这些地方“后端已有”的前提不再成立：`/api/workspace/*`（文件树读写口，见 C2 与 P1-1）、`/api/exploration/tokens`（见 C4）、`/api/tokens/usage` 与 `/api/llm/records/by-model`（见 C4）。相应地，C2/C4 所说的“纯前端缺口 / 接线缺口”现已变为“后端接口也需恢复或重做才能接”；其余结论不变。

日期：2026-11-14　评估对象：`artex-harness` @ `59c2f9e`
方法：本仓库源码/运行态实测 + GitHub API 检索与 README 抓取。
**标注约定**：`[已证实]` = 我在本仓库或公开仓库里直接看到；`[假设]` = 未能证实，按推断列出；
`[未证实]` = 检索不到，不采信。

---

## 一、结论摘要

HARNESS 当前不是"缺功能"，而是**三个层面没打通**：

1. **内核未启用**：`llm_profiles = 0`，引擎处于 idle（`[已证实]`：启动日志
   `no LLM provider configured — engine idle`；DB 计数 0）。S1–S5 交付的是**可运行的骨架**，
   但没有模型端点就一次真实编排都跑不起来 —— 这是"底座和内核仍未稳定启用"的准确含义。
2. **编排拓扑是"固定角色管线"**，而成熟 harness 是"**单一交互 agent + 可选委派**"。
   两者不是自动化程度高低，而是**控制权归属**不同：前者把"派什么活"交给 planner，
   后者交给一个可对话的 agent。我们**已有一条**、**缺另一条**。
3. **前端只有"流 + 干预坞"两栏**，缺成熟 harness 桌面端的**文件/DIFF/终端/浏览器/子代理/上下文预算**
   这六个面板。后端数据大多已有（`/api/workspace/*`、`/api/exploration/tokens`），是**接线缺口**，
   不是重做缺口。

---

## 二、HARNESS 完整性差距清单

### A. 底座 / 内核启用

| # | 差距 | 证据 | 影响 |
|---|---|---|---|
| A1 | **无模型端点 → 引擎 idle** | `[已证实]` `select count(*) from llm_profiles` = 0；启动日志明文 | 任何编排都跑不起来；S1–S5 的验收全部靠"人工构造事件"，未经真实 LLM 端到端验证 |
| A2 | 引擎就绪判据是**全局** provider | `Engine.SetReadiness` 只看 `s.llmOn`（`server/server.go`）；任务级另有 `taskRuntimeAvailable` | 全局未配时**所有**任务静默 idle，控制台只显示 `● 实时` 但无事件 → 容易被误解为"卡住" |
| A3 | 无"就绪自检"面向操作者 | 无 `GET /api/harness/ready`（S3 只有 scenarios 元信息） | 无法回答"为什么没动静"：缺 LLM / 缺 PG / 缺 skill / 缺浏览器 MCP，四种原因表现一样 |
| A4 | 任务创建不等模型就绪 | `createTask` 只落库；goals 分解失败回落成单条 goal | 用户以为任务在跑，实际 goal 是"原文照抄"，链路质量静默降级 |
| A5 | 无冒烟场景（离线可跑） | `eval/scenarios/` 只有 3 个规格文件，无 fixture runner | 无法在无端点环境下验证"引擎能走完一轮" |

### B. 编排架构（自动化 / 可控性 / 子代理派发）

| # | 差距 | 证据 | 影响 |
|---|---|---|---|
| B1 | **子代理能力存在但未接入** | `third_party/norma/subagent/subagent.go` 提供 `NewTool`（模型可调用的 Task 式子代理）+ `depth(ctx)` 深度护栏 + `filterTools` 工具收窄；**全仓库 `grep subagent` 在 agent/server 下为空** | 模型无法"自己开一个子代理去干一件事"；只能等 planner 派 intent |
| B2 | 派发权集中在 planner | `agent/planner.go:355` `Plan()` 是唯一 intent 产生点；README/架构文档明示"planner 唯一意图生成者" | 主 agent 只能 `add_intent`/`add_hint`/`steer_work`**追加与纠偏**，不能"直接指挥一个 worker 做这件事" |
| B3 | `spawn_task` 只挂在 `auto` agent | `[已证实]` `tools` 表：`spawn_task` 绑定 `["auto"]`；`orchestration.go:242` | 它是**跨任务**扇出（新建 task），不是子代理；且对话页默认 agent 才有 |
| B4 | 无"会话内工具级并行委派" | worker 内部一轮里工具是 FIFO 串行（`harness/streaming.go` 非并发安全工具互为屏障） | 大量互相独立的探测无法并行；只能靠多 worker 跨 intent 并行 |
| B5 | 人工干预有 API 但**无"接管"** | S2 六原语（steer/kill/edit/order/replan/pause-all）；缺"我来做这一步/把这条 intent 交给我" | 人不能在流里"接手执行"，只能纠偏或停止 |
| B6 | 意图编辑对运行中 work 不生效 | `docs/stages/S2.md` 已注明：改的是下一轮 | 人的指令有延迟，容易被感知为"没生效" |

### C. 三栏工作区

| # | 差距 | 证据 | 影响 |
|---|---|---|---|
| C1 | 现为"左任务列 + 中流 + 右干预坞"，**非**用户描述的三栏 | `web/src/App.jsx` 的三栏结构 | 左栏不是"对话 + 项目目录"；右栏只有干预，没有状态/文件/DIFF/TOKEN |
| C2 | 无文件树（后端已有） | `[已证实]` `/api/workspace/{list,read,write,mkdir,delete,download,upload}` | 纯前端缺口 |
| C3 | **无 DIFF** | `[已证实]` 无 diff 端点，也无前端 diff 视图 | 工作区文件是"脚本/字典/证据"，被 agent 改过之后**无法审阅** |
| C4 | 无 token 预算面板（后端已有） | `[已证实]` `/api/exploration/tokens`、`/api/tokens/daily`、`/api/tokens/usage`、`/api/llm/records/by-model` | 上下文用完不可见；S1 已有 `env.item.usage` 但未渲染 |
| C5 | 无终端 | 后端有 `shell_open/shell_send` 工具；无 Web 终端（claudecodeui 用 xterm） | 人要看/接管 shell 必须去桌面 |
| C6 | 无子代理/后台任务状态面板 | 无 `/api/harness/agents` 类端点；worker 状态只能从 activity 推 | 3 个 worker 谁在干什么不可一眼看到 |
| C7 | 无会话/项目切换（单会话） | 控制台只有"任务"维度 | 无法像 cc-haha/opcode 那样"多会话并行 + 项目切换" |

### D. 浏览器自动化

| # | 差距 | 证据 | 影响 |
|---|---|---|---|
| D1 | **浏览器 MCP 默认关闭** | `[已证实]` `mcp_servers`: `name=browser, transport=stdio, command=npx, enabled=f`；`mcp_tools_cache` 无行（未连上过） | "AI 可任意打开地址"实际不可用 |
| D2 | 无内嵌视图 / 无标签页 | 控制台无 webview；纯 MCP 工具调用 | 人看不到 AI 在看什么；无法像 cc-haha 那样"内置浏览器预览（真实 Cookie）" |
| D3 | 无 F12/DevTools 级能力 | Playwright MCP 只有快照/点击/输入/读文；CDP 端点未暴露 | 排查前端问题需要"网络/控制台/性能"，当前缺 |
| D4 | 无会话生命周期管理 | MCP client 在 `mcphttp/`（一次连接、工具包装）；无"标签/会话隔离/崩溃重启" | 长任务里浏览器崩了会连带工具调用失败 |
| D5 | 与稳定性耦合未评估 | 浏览器 = 长会话 + 重状态 + 大输出（快照） | 会放大上下文压力与工具超时；现有 `capOutput` 只兜底输出，不兜生命周期 |

---

## 三、对四条猜想的验证 / 修正 / 反例

### 猜想 1：ARTEX 是"完整自动化体系、人工难干预"；Claude Code / Codex 是"直接对话，由 AI 自由判断是否派子代理"

**判定：方向对，但归因需要修正。**

| 维度 | ARTEX 实测 | Claude Code / Codex 类 | 结论 |
|---|---|---|---|
| 控制权归属 | **planner 独占派发权**（唯一 intent 产生点） | **对话中的主 agent 独占**（模型决定调用工具/子代理） | 两者都是"单一决策点"，区别在于**决策点是否可对话** |
| 自动化程度 | **更高**：无人在环也能连跑（心跳 + 目标达成自动收尾） | **更低**：默认人类每轮驱动；有 `/loop`、`--goal` 类持续模式但不是默认 | ARTEX 在"无人值守"上领先，不是落后 |
| 人工介入粒度 | 任务级 pause / intent 级 steer-kill / 目标与约束 CRUD（S2 后 6 个确定性 API） | 每一步工具都可拒绝/改写/中断（permission + hooks）；**可编辑中间结果** | ARTEX 介入粒度**粗一档**：改不了"正在想的那一步" |
| 子代理派发 | **无模型驱动子代理**（B1：norma subagent 未接入）；只有 `auto` 的 `spawn_task` 扇任务 | **有**：Task/subagent 工具，模型自主决定并行/隔离/继承哪些工具 | 这是**真实差距**，且是唯一一条"能力缺失"而非"取舍不同" |
| 反例（修正） | "人工难以干预"**已不成立**：S2 之后 steer 不中断当前轮、意图可改、frontier 可重排 | —— | 应改为"人工可干预，但**不能接管执行**、且**改动对运行中 work 有延迟**" |

**修正后的差距表述**：ARTEX 缺的不是"交互"，而是**"主 agent 拥有全部动词"**——
对话能读图、能给提示、能纠偏，但不能"替 AI 派一个子代理去做这件事并等它回来"。
`subagent.NewTool` 已经在 SDK 里，接上即可把这条路径打开，且不破坏现有管线（它是"模型可选的一次委派"）。

### 猜想 2：三栏（右：子代理状态/文件/DIFF/TOKEN；左：对话+项目目录）"已大致形成基本架构"

**判定：不成立（当前是两栏 + 右坞）。** `[已证实]` 控制台只有 5 个视图页签 + 一个干预坞，
没有文件树、DIFF、TOKEN 面板、子代理状态、终端、项目目录。**后端数据大多已有**（C2/C4），
所以这是"接线 + 两个新能力（DIFF、终端）"的差距，不是从零设计。

### 猜想 3：内置浏览器 + 侧边栏可视化窗口 + 完整操控（含 F12），是 HARNESS 稳定性的组成部分

**判定：能力存在但未启用；"稳定性"关系成立，但真正的缺口是"会话生命周期"而非"打开 URL"。**
`[已证实]` 浏览器 MCP 默认 `enabled=false`，且从未连上（工具缓存为空）。
一个长会话浏览器会带来四类稳定性债务：**上下文污染（快照巨大）/ 会话隔离 / 崩溃恢复 / 并发标签**。
现有代码只解决"输出预算"（`capOutput`），没有解决生命周期。

### 猜想 4：存在 HARMES DESKTOP、CLAUDE CODE DESKTOP、CC-HAHA 等成熟桌面体系

**判定：CC-HAHA 真实存在（15.4k star）；"HARMES DESKTOP" 检索不到，标 `[未证实]`。**
`gh api search/repositories -f q="harmes desktop"` 返回 12 条，全部无关（Kotlin 减害应用、
测速清单等）。CC-HAHA 的实体是 **`NanmiCoder/cc-haha`**。若你指的是别的项目，请给出链接，
我不按其字面去猜。
`[假设]` 可能想说的是 Hermes（cc-haha/Orca 生态里提到的 "Hermes Agent"）或 **Orca**
（`stablyai/orca`，多 agent 编队），但那与"HARMES"拼写不符，故不作为事实。

---

## 四、相关开源项目清单与框架/前端对比

数据来源：GitHub API（star 数为抓取时值）+ 各仓库 README 原文。

| 项目 | star | 桌面壳 | 前端 | 后端/内核 | 前端信息架构要点（来自 README 原文表述） |
|---|---|---|---|---|---|
| [NanmiCoder/cc-haha](https://github.com/NanmiCoder/cc-haha) | 15.4k | **Electron** | React + Vite（终端 UI 用 Ink） | 本地 server + API（自有 channel 系统） | 多会话**标签页**、侧边栏可拖拽；右栏"本轮改动逐文件 DIFF、整轮可撤销"；**内置浏览器预览**（真实登录态 Cookie）；"会话活动面板"集中看**后台任务/SubAgent/来源**；Agent Teams 协作工作台（成员/任务/通信/依赖泳道）；**Computer Use**（macOS 原生，Win/Linux 复用 agent-desktop）；手机 App/H5/IM 接力审批 |
| [winfunc/opcode](https://github.com/winfunc/opcode) | 22.4k | **Tauri 2（Rust）** | React | Rust 后端 + SQLite | 以"项目浏览器"为入口（扫 `~/.claude/projects/`）；CC Agents = 可建自有 system prompt 的 agent 库；会话管理 + 用量追踪 + 后台 agent。**桌面壳最轻**（Tauri），但**无浏览器/终端面板** |
| [siteboon/claudecodeui](https://github.com/siteboon/claudecodeui) | 14.6k | 无（Web / 手机浏览器） | React + **xterm** | 包装多 CLI（Claude Code/OpenCode/Cursor/Codex） | 纯 Web：会话列表 + 终端 + 文件浏览器；定位"离开电脑也能用" |
| [BloopAI/vibe-kanban](https://github.com/BloopAI/vibe-kanban) | 28.7k | 本地 Web | React | Rust + **git worktree** | 以"任务卡片/看板"为第一公民，每次尝试一个隔离 worktree + DIFF 审阅；**编排在卡片层，不在对话层** |
| [justhil/pi-app](https://github.com/justhil/pi-app) | 0.4k | **Electron** | React | 复用 `~/.pi/agent` | **最接近"三栏"的范本**：左=时间线/对话；右=**Review 面板**（Git diff，可拖宽、并排、逐 hunk stage）+ Files 面板（多标签预览）+ **Run 面板（上下文预算环）** + Context 面板（逐条 token 估算）；`Ctrl+\`` 从对话下方拉起**自带终端**（Tabs、最多 4 分屏） |
| [ttnear/Clarc](https://github.com/ttnear/Clarc) | 0.4k | **SwiftUI 原生**（macOS） | SwiftUI | 包装 Claude Code | 原生客户端路线：证明"不做 Electron 也能做"（代价：单平台） |
| [AMAP-ML/LongHorizon-Harness](https://github.com/AMAP-ML/LongHorizon-Harness) | 1.9k | — | React | Python + FastAPI | **computer-use 型 harness**：跨桌面应用与 CLI 的长时程执行，最接近"渗透/操作类"任务的稳定性课题 |

### 对比结论（框架层）

1. **桌面壳三派**：Electron（cc-haha、pi-app）＝生态最全、Web 技术复用最大化，代价是包体与内存；
   **Tauri/Rust**（opcode、vibe-kanban）＝轻、启动快、IPC 有类型，代价是 Rust 侧工作量；
   **原生 SwiftUI**（Clarc）＝最贴合系统与权限模型，代价是单平台。
   我们目前是**浏览器 + 服务端渲染（Go embed）**第四派：**零安装、跨平台、可远程**，
   代价是没有系统级能力（截图/窗口焦点/全局快捷键/Computer Use）。
2. **前端信息架构的共同骨架**：**左=会话/项目，中=对话或时间线，右=产出物（DIFF/文件/预算）**。
   cc-haha 与 pi-app 都把它做成了**可拖拽三栏 + 面板可换**；差别在右栏的"深度"：
   pi-app 做到**逐 hunk stage 与行级评论回填对话**，cc-haha 做到**整轮可撤销 + 内置浏览器预览**。
3. **编排在哪里**：cc-haha/opcode/pi-app 把编排放在**agent 的对话回合里**（模型自己决定用哪个工具/子代理）；
   vibe-kanban 把它放在**卡片/看板**（人安排任务，agent 在隔离 worktree 里干）；
   ARTEX 把它放在**planner + frontier 队列**（第三种：无人值守流水线）。
   **三者的"稳定性"来源不同**：对话式靠"人每轮兜底"，看板式靠"隔离 + DIFF 门禁"，流水线靠"心跳 + 目标判定"。
4. **我们没有的、且这些项目都有的**：可拖拽面板布局、DIFF 审阅（含 hunk 级）、上下文预算可视化、
   终端抽屉。**我们独有的**：双图血缘 + 意图队列 + 一致性门禁（`harness-gate`）——
   这三样是"防止 AI 走偏"的机制，cc-haha 类产品靠人在环，vibe-kanban 靠隔离。

---

## 五、按优先级排列的下一步（含可验收标准）

> 排序原则：**先让内核真的转起来**（否则一切面板都是空壳），再补"人能看见产出"（DIFF/预算），
> 再补"模型能自主委派"（subagent），最后才是浏览器与多会话。

### P0 · 让内核启用并自证（1–2 天）

| 项 | 动作 | 验收标准 |
|---|---|---|
| P0-1 | 控制台加 **就绪面板**：`GET /api/harness/ready` 返回 `{llm, pg, skills, browser_mcp, engine_state}` 与"下一步提示" | 未配模型时首页明确显示"引擎 idle：未配置 LLM"，并给跳转；四种缺失原因可区分 |
| P0-2 | **离线冒烟场景**：`harness/eval/smoke.go` + `-tags smoke` 用假 provider 跑通"建任务→分解→派 intent→执行→写回→收尾" | `go run ./eval/cmd/harness-smoke` 在**无网络无端点**下输出完整一轮 replay，退出码 0 |
| P0-3 | 配一个**真实端点**跑一次端到端（由业主提供，代码里不预设） | 一个真实任务在控制台 Run 页出现 ≥1 个 planner 轮 + ≥1 个 work 轮，`harness-gate` 自比对通过 |
| P0-4 | 任务创建时若模型不可用 → 显式标记 `degraded` 并在 UI 提示 | DB/UI 可见 `goals_decomposed=false`；不再静默用原文当 goal |

### P1 · 三栏工作区（3–5 天，能力大多已有）

| 项 | 动作 | 验收标准 |
|---|---|---|
| P1-1 | 左栏改为**会话 + 项目目录树**（接 `/api/workspace/list`） | 可点开目录、打开文件、看到本任务工作目录 |
| P1-2 | 右栏改为**分段面板**：状态 / 文件 / DIFF / 预算 | 四个面板可切换；面板宽度可拖拽（存 localStorage） |
| P1-3 | **DIFF 端点**：`GET /api/harness/diff?task=&path=&rev=`（基于 `harness_revisions` 前后快照或 git-less 逐次写盘快照） | 能对"agent 本轮改过的文件"给出 unified diff；无 git 环境也可用 |
| P1-4 | **预算面板**：接 `/api/exploration/tokens` 与 `env.item.usage` | 显示 输入/输出/缓存 与占比；超 80% 变黄 |
| P1-5 | **子代理/worker 状态**：`GET /api/harness/workers?task=` | 3 个 worker 的 intent/步数/时长一屏可见 |

### P2 · 子代理派发（打通 norma subagent 这条唯一"能力缺失"）

| 项 | 动作 | 验收标准 |
|---|---|---|
| P2-1 | 在 `agent/tools.go` 注册 `Task`（norma `subagent.NewTool`）给 **mainagent**（先只给人用的主 agent） | 对话里说"派一个子代理去枚举 /api 的端点"，流里出现 `[tool_use] Task` 与其独立子会话 |
| P2-2 | 子代理的工具集收窄 + 深度护栏（`filterTools` / `depth`） | 子代理不可再派子代理（depth 超限报错）；默认工具集不含 `spawn_task` |
| P2-3 | 子代理事件进 harness 协议（`turn.kind="subagent"`） | Replay 能看到 `subagent` 轮次与会话 id |
| P2-4 | 可选：给 planner 也开（`planner` 一次性派 N 个只读子代理做并行侦察） | 一轮内出现 ≥2 个并行子代理且互不串上下文 |

### P3 · 浏览器自动化（先"稳"再"全"）

| 项 | 动作 | 验收标准 |
|---|---|---|
| P3-1 | 默认打开 `browser` MCP（`enabled=true`）并加**连接自检** | 控制台就绪面板显示 `browser_mcp: connected (N tools)` |
| P3-2 | **会话生命周期**：每任务一个 browser session，超时/崩溃可重建；工具调用前 ping | 杀掉浏览器进程后，下一次工具调用自动重连成功（日志可见） |
| P3-3 | 侧边栏**内嵌视图**（截图流式：MCP `screenshot` + 轮询渲染） | 能看到 AI 当前页面截图，可点击"打开当前 URL"到本机浏览器 |
| P3-4 | **F12 能力**：接 Chrome DevTools MCP（`chrome-devtools-mcp`）或 CDP 端点，暴露 console/network | 能取到页面 console 报错与 network 请求列表 |
| P3-5 | 多标签隔离 | 同任务可开 2 个独立 session，互不覆盖 Cookie/导航 |

### P4 · 收尾（可选，视使用反馈）

- 多会话/项目切换（控制台从"单任务视角"到"多任务并行"）；
- 终端抽屉（xterm + `shell_open` 后端）；
- "接管执行"：人可直接在某条 intent 上执行一步工具并写回（当前只能纠偏）。

---

## 六、需要业主确认的三件事（缺一不可推进 P0）

1. **端点**：给一个可用的 base_url + 模型名（**密钥请勿贴对话**，放到 box 的环境变量或 UI 里配）。
2. **P0-1 的"就绪"语义**：`engine idle` 时你希望控制台是"黄条提示"还是"阻塞式引导"？
3. **P1-3 DIFF 的基线**：用「每轮快照」（我们自己存，无需 git）还是「git 工作区」（要求目标机有 git）？
   `[假设]` 靶场机器常常没有 git 仓库，所以默认按"每轮快照"设计更稳。
