# 07 · HARNESS 运行模式（无规划者/执行者）与 P0–P4 落实

日期：2026-11-14/15　基线：`59c2f9e` → 本次
决策：**HARNESS 不再使用 ARTEX 的规划者（planner）与执行者（worker）角色。**
一个任务 = 一个会话 agent；是否委派子代理，由这个 agent 自己判断。

---

## 一、为什么不用规划者/执行者（ADR-0004）

ARTEX 的执行底座把任务拆成"规划者决定方向 + 执行者池领取执行"。这对**无人值守**是有利的，
但它带来三个与 HARNESS 目标冲突的性质：

| ARTEX 管线性质 | 与 HARNESS 目标的冲突 |
|---|---|
| 派发权集中在规划者 | 人的指令要经模型转译才能变成"下一步"，不是确定性动作 |
| 意图队列是工作单位 | 模型自己的判断被降级为"给一个 intent 写摘要" |
| 执行者池按 intent 并行 | 并行粒度由队列决定，而非由"这一步会不会产出大量中间输出"决定 |

成熟 harness（Claude Code / Codex / cc-haha / opcode / pi-app）的共同选择是**相反方向**：
一个可对话的 agent 持有全部动词，**它自己**决定下一步、自己决定是否派子代理。
HARNESS 采纳后者，并保留 ARTEX 底座里与角色无关的资产（双图作为知识、流量留痕、证据快照、
审批门、revision/回放/一致性门禁）。

**因此**：

- 新增 `tasks.harness_mode`（默认 false，**历史任务行为不变**）；
- harness 模式下 **不启动** 规划者循环、**不启动** 执行者池、**不产生意图**、**不做目标分解**；
- harness 模式的工具面**过滤掉**意图/纠偏类工具（`add_intent` `steer_work` `add_hint`
  `get_worker_output` `get_worker_trace` `list_worker_traces` `search_all_worker_traces`）；
- 保留 `Plan` 视图只为**历史任务**服务，界面上明说"HARNESS 任务不产生意图"。

替代机制：`Agent` 工具（norma `subagent`）+ 深度护栏 + 工具收窄，**子代理的工具集里不含
`Agent`**，因此不可能递归派发；这是"模型自主委派"与"不会失控放大"的交点。

---

## 二、运行时（`agent/harness.go`）

```
harnessLoop (server/harness_loop.go)
  ├─ 首轮：输入 = "开始执行这个任务…"
  ├─ 之后：阻塞等 HarnessInbox（人的消息）
  ├─ OpenTurn(kind="harness") → 一个 revision
  ├─ HarnessAgent.Run(...)
  │    ├─ ToolSet（图谱读/写）− 意图类工具
  │    ├─ DefaultTools（Bash/Read/Write/Edit/LS/Glob/Grep）
  │    ├─ Agent（subagent: recon / verify）
  │    ├─ AugmentTools(ctx,"harness",…)  → skills / MCP（含 browser）
  │    └─ captureRun → 事件进 activity + 协议信封（含 usage）
  └─ 本轮结束且队列为空 → 状态 done（Say() 会把它拉回 running）
```

与 ARTEX 管线的关键差别：
- **没有 settlement 轮**：会话式 agent 停下来就是停下来，人可以继续说话；管线式必须强制收尾。
- **状态语义**：`running` = 有一轮在跑；`done` = 回合结束且没人再说话。
- 复用同一套 pause/delete 屏障、deadline 协调器、revision、SSE 信封 —— 避免"第二个引擎"。

---

## 三、P0–P4 落实与验收

### P0 内核启用并自证

| 项 | 实现 | 验收（实测） |
|---|---|---|
| P0-1 就绪面板 | `GET /api/harness/ready`：pg / llm / skills / browser_mcp / ready / hint | 返回 `ready=true, llm{profiles=1,active=1,engine=ready}, skills 3, browser_mcp 状态` |
| P0-2 离线冒烟 | `go run ./eval/cmd/harness-smoke`：脚本化 provider（无网络无 key）驱动真实运行时 | ✅ `SMOKE OK`：`tool_use=2 tool_result=2 facts=1 final="离线冒烟完成…"` |
| P0-3 真实端点 | 业主提供的 OpenAI 兼容端点（**密钥只在环境/UI，未入库未入文档**） | ✅ 任务 #30：`status=done`、3 条 fact `confirmed`、终答"完成。3 条事实已写回并确认" |
| P0-4 降级可见 | `TaskDTO.llm_ready`；未配模型时前端 pill 变红并提示 | 实测 `llm ✓` / 无模型时 `llm ✗` + toast |

> 端到端过程中修掉 3 个真实缺陷：① 新建任务返回的 `Task` 未带 `harness_mode`，导致仍走老管线；
> ② 目标分解在建任务时同步跑（harness 模式已跳过，省掉一次 LLM 调用与错误回退）；
> ③ `.app`/`.gate` 的 `display:grid` 压过 `[hidden]`，登录门与主界面同时出现。

### P1 三栏工作区

| 栏 | 内容 | 接口 |
|---|---|---|
| 左 | 任务列表（`H`/`A` 标记运行模式、`no-llm` 标记）、**项目目录树**（变更标记）、**对话输入**（⌘/Ctrl+Enter 发送） | `/api/tasks`、`/api/harness/files/{id}` |
| 中 | 事件流（按 revision 分组）+ Plan/Replay/Eval/平台 | `/api/harness/stream` |
| 右 | **状态**（委派列表）、**文件**、**DIFF**、**预算**、**浏览器**、**干预** | `/api/harness/{workers,files,diff,budget,browser}` |

- **DIFF 不依赖 git**：任务启动时快照工作区为 `.harness-baseline/`，之后按内容哈希对比；
  缺基线时**就地建立**并在响应里说明（`baseline:false`），避免"永远显示没有变更"。
  `harness/diff` 是纯函数（LCS，>4000 行退化为整file替换），5 个单测覆盖。
- **预算**：`last_input` 取最近一条 usage 行（不是累加，累加会高估当前上下文）；超过 80% 变红。
- **委派面板**：`Agent` 工具调用/结果配对，running/done/error。

验收：控制台实测渲染三栏，状态面板显示 `模式 HARNESS · 委派 0`；文件树/DIFF/预算均为真实数据
（预算实测 `最近输入 86519 · 约 67.6%`）。

### P2 子代理（打通唯一的能力缺失）

- `Agent` 工具由 norma `subagent.NewTool` 提供，定义 `recon` / `verify` 两个 agent_type，
  各自**显式工具白名单**；任何定义都不含 `Agent` → 无法递归。
- 包装器把"派发/返回"写进事件流（`派发子代理 recon：…` / 结果摘要），所以"模型自己决定并行"
  这件事在 transcript 与回放里可审计。
- 验收：`/api/harness/workers/{id}` 能在有委派时列出条目（本次冒烟 agent 未委派 → 返回空列表且
  说明"由 agent 自己决定"，这是正确行为，不是失败）。

### P3 浏览器自动化

| 项 | 状态 |
|---|---|
| P3-1 默认启用 | ✅ `ensureBrowserEnabled()`：启动时把种子 `browser`（stdio, npx @playwright/mcp）启用；可用 `harness_browser=false` 关闭 |
| P3-2 状态如实 | ✅ `GET /api/harness/browser`：configured/enabled/tools_cached/npx 存在性 + 最近 browser 动作 |
| P3-3 会话生命周期 | ⚠ **未完成**：MCP client 每轮由 `AugmentTools` 创建，天然"每轮重连"，但没有崩溃后主动重建与多标签隔离 |
| P3-4 F12 级能力 | ⚠ **未完成**：只有 Playwright MCP 的快照/点击/输入；console/network 需要 `chrome-devtools-mcp` 或 CDP 端点 |
| 内嵌页面视图 | ⚠ **未完成**：控制台显示状态与最近动作，不渲染页面截图 |

实测：`enabled=true, npx found, tools_cached=0` → 就绪面板如实报"已启用但尚未连通"，不假装可用。

### P4 会话接管（部分）

- ✅ `POST /api/tasks/{id}/say`：直接对 agent 说话，**revive** 已 done 的任务并在同一会话续跑
  （transcript 续跑，不重放历史）。
- ⏳ 未做：终端抽屉（xterm）、"我来执行这一步"（人执行工具并写回）。

---

## 四、明确未做 / 已知缺口

1. **P3 的会话生命周期与 F12**：需要引入 CDP 或 `chrome-devtools-mcp`，并给浏览器会话加
   超时/重建/标签隔离。当前只保证"默认可用 + 状态如实"。
2. **内嵌浏览器视图**：需要截图流式回传与渲染管线（MCP `browser_take_screenshot` → 前端 blob）。
3. **多会话/项目切换**：控制台仍是"单任务视角"。
4. **历史任务仍在规划者/执行者管线**：这是刻意的（升级不改行为）；可用
   `PUT /api/tasks/{id}/harness-mode {"harness_mode":true}` 逐个切换，切换在下一轮生效。
5. **目标分解**：harness 模式直接跳过；如果希望 agent 得到"子目标"，它可以用 `set_goals` 自己写。

## 五、验收命令

```bash
# 1) 离线自证（无网络无 key）
ARTEX_PG_DSN=... go run ./eval/cmd/harness-smoke

# 2) 就绪
curl -s -H "Authorization: Bearer $TOK" localhost:8787/api/harness/ready | jq .

# 3) 真实一轮（需端点）
curl -s -X POST localhost:8787/api/tasks -H "Authorization: Bearer $TOK" \
  -H 'Content-Type: application/json' \
  -d '{"goal":"…","description":"…","harness_mode":true}'

# 4) 对话续跑
curl -s -X POST localhost:8787/api/tasks/<id>/say -H "Authorization: Bearer $TOK" \
  -d '{"message":"把结论收敛成 3 条"}' -H 'Content-Type: application/json'

# 5) 三栏数据
curl -s localhost:8787/api/harness/diff/<id>?path=notes.txt
curl -s localhost:8787/api/harness/budget/<id>
curl -s localhost:8787/api/harness/workers/<id>
```
