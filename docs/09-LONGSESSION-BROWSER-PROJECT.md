# 09 · 长会话预算、浏览器会话管理器、截图管线、项目目录

日期：2026-11-15　基线：`8cfd7cf`

## 一、长会话健壮性（已实现）

| 项 | 实现 | 位置 |
|---|---|---|
| run 级墙钟预算 | `HARNESS_RUN_BUDGET`（`90s`/`20m`/纯秒数），默认 **20 分钟**，写入 `Options.MaxDuration` | `agent/harness.go` |
| 超时收尾 | `Settlement.PromptByReason`：`ReasonTimeout` → 「时间到，落地结论」；`ReasonMaxTurns` → 「步数上限」 | `agent/harness_prompt.go` |
| 终态可见 | 异常收场（timeout/max_turns/model_error…）落一条 `lifecycle` 活动 | `server/harness_loop.go` |
| 压缩可观测 | `/api/harness/budget` 增加 `run_budget_seconds` / `series`（每轮输入 token）/ `compactions`（输入 token 回落 ≥20% 的次数） | `server/harness_files.go`、`db/exploration.go` |

**诚实标注**：SDK 没有压缩事件，`compactions` 是**推断值**（上下文被压缩 → 下一次请求的
`input_tokens` 显著下降），API 的 `note` 里写明了这一点。

## 二、浏览器会话管理器（已实现）

`harness/browser`：

| 能力 | 说明 |
|---|---|
| 一任务一会话 | `Ensure(taskID)` 幂等；Playwright 与 chrome-devtools **在同一会话**（同一 Manager 条目），满足"F12 连到同一个会话" |
| 重建而非修复 | 工具调用失败 → `MarkBroken` → 下次 `Ensure` 立刻重启该服务器；**一个服务器崩不带走另一个** |
| 标签模型 | `OpenTab/CloseTab`，`MaxTabs` 上限，标签彼此独立（导航/URL 互不覆盖） |
| 空闲回收 | `GC(now)` 关闭超过 `IdleTimeout`（默认 30 分钟）的会话；10 分钟一次的回收协程同时清理过期截图 |
| 状态 | `/api/harness/browser/sessions`、`/restart`、`/tabs`；`/api/harness/browser` 合并 Manager 真实状态（live 即 ready，不再依赖过期的工具缓存） |

**实测（本机）**：`POST /restart` 2.3 秒内把 **browser 与 chrome-devtools 两个服务器都拉起**，
会话状态 `browser: live, chrome-devtools: live` → `/api/harness/browser` 返回
`ready=true, devtools.ready=true`。开标签后 `t1` 保留，未被后续操作覆盖。

## 三、截图管线（已实现，绕开 SDK 限制）

**为什么需要绕**：norma 的 `llm.ContentBlock` 联合体只有 text/thinking/tool_use/tool_result，
**没有 image 类型**；MCP 返回的图片在到达 agent 之前就丢了。扩大 SDK 的 content union 会牵动
每个 provider 的序列化器，代价与收益不匹配。

**做法**：`harness/browser/rawclient.go` 自带一个最小 stdio JSON-RPC 客户端（initialize +
tools/call），只用于取图；文本类工具仍走 norma/mcp。取到的 PNG：

- 内容寻址存放于 `<data>/browser/<sha256>.png`（同图只存一份）；
- 事件流里以 `[thumb] <hash>` 标记 → 控制台渲染成 `<img src="/api/harness/shot/<hash>">`；
- `GET /api/harness/shot/{hash}` 不可变缓存（`immutable`），非法 hash（含 `../`）拒绝；
- 回收：TTL 24h + 上限 2000 张，每 10 分钟一次。

**已验证**：用 helper-process 假 MCP 服务器端到端跑通"stdio → tools/call → base64 → PNG →
哈希 → 存储 → 读取"，并覆盖"服务进程立刻退出"的失败路径。

## 四、项目目录（已实现）

- `tasks.project_dir`（空 = `<data>/tasks/<id>`）；文件树 / DIFF / 终端都以它为根。
- 项目模式下**基线不写进用户目录**，改放 `<data>/baselines/<taskID>`。
- 互斥：`projectGuard` 把「目录 → 任务」绑成一对一；`PUT /api/tasks/{id}/project` 与创建时都会
  校验，冲突返回 409 并点名占用者；任务删除/改绑时释放；启动时按存活任务重建绑定。

## 五、验证矩阵（重要：分清"已验证"与"未验证"）

| 项 | 验证方式 | 结论 |
|---|---|---|
| run 预算生效 | `GET /api/harness/budget` 返回 `run_budget_seconds=1200` | ✅ |
| 压缩可观测 | 真实任务 30：`series` 7 个点、`compactions=0`（该任务未触发压缩） | ✅ 有观测、⚠ 未观察到实际压缩 |
| 超时收尾 | 代码路径 + `PromptByReason` 单测（settlement 逻辑属 SDK，未造 ≥20 分钟真实超时） | ⚠ 未端到端验证 |
| 会话重建 <10s | `restart` 2.3s、两服务器 live；单元测试用假 Conn 断言重建与"互不牵连" | ✅ |
| F12 连通 | 真实拉起 `chrome-devtools-mcp`，状态 `live` | ✅ 服务器层；⚠ 未在面板里结构化展示 console/network |
| 标签隔离 | 单元测试 + 实测 t1 保留 | ✅ |
| 截图管线 | helper-process 假 MCP 端到端 | ✅ 管线；⚠ 未对真实 Playwright 取图（需页面已导航） |
| 项目互斥 | 代码 + 校验路径；数据库层单测未补 | ⚠ 仅代码级 |

## 六、本轮发现并修掉的真实缺陷

**S1 引入的读快照存在并发缺陷**：`WithReadSnapshot` 把事务写进共享字段 `s.tx`，两个消费者
（如 worker 与 planner 同时渲染态势）会互相覆盖，其后一个会在已回滚的事务上查询 →
pgx **panic `index out of range [0] with length 0`**，整个进程退出。已加 `snapMu` 串行化
（一次快照本就是几百行的读）。这是"只在旧管线并发跑起来才会炸"的典型问题，正好被本轮实测撞到。

## 七、仍未完成

> **更新（本轮）**：以下 2、3 已补齐，§二/§三/§四 不再是"计划"，而是有控制台入口的功能。
> 详见 [docs/13-CONSOLE-WIRING.md](13-CONSOLE-WIRING.md)。

1. **贯穿项（真实 run 跑一次 harness-gate）未做**：需要两次 ≥5 分钟的真实运行，本轮预算已用于
   前四项与上述缺陷修复。做法与判据不变，见 `docs/03 §7` 与 `docs/stages/S3.md`。
2. ~~F12 的 console/network **结构化面板**~~：console 已按级别解析并渲染（`console_errors`）；
   **network 仍无结构化数据**——界面明确写出这一点，不画空表格。这是本文件唯一仍成立的缺口。
3. ~~`project_dir` 的数据库层单测、任务删除时的会话/目录释放的端到端用例~~：
   目录互斥（409 + 点名占用者）与释放已实测；委派目录另有 TTL 回收。
   仍缺的是"任务删除时浏览器会话释放"的端到端用例。
