# 11 · Sprint 3：多代理编排（后台委派 / 工具池 / 隔离 / 通知）

日期：2026-11-16　基线：`f1289cb`

原则不变（[ADR-0005](adr/0005-harness-maturity-and-borrowing-boundary.md)）：**执行权仍在一个可对话
agent 手里**；这里补的是"它派出去的子代理能安全、可审计地跑完并把结果送回来"，
不引入 Agent Teams / 跨会话编排。

---

## 一、交付

| 能力 | 实现 | 位置 |
|---|---|---|
| 后台委派 | `Agent` 工具新增 `run_in_background` 与 `description`；立即返回委派 id，不阻塞当前轮次 | `agent/harness.go` |
| 委派注册表 | 进程内权威状态：id / 类型 / 提示词 / 状态 / 起止 / 耗时 / 输出路径 / 隔离目录 | `harness/delegation/` |
| 工具池三层过滤 | 子代理定义只拿到显式白名单（`recon`/`verify`），任何定义都不含 `Agent` → 无法递归 | `agent/harness.go` `agentDefinitions` |
| 工作区隔离 | 后台委派在 `<delegations>/<taskID>/<did>/` 独立目录运行，不污染任务工作区 / DIFF / 检查点；空目录自动清理，写过文件则保留为证据 | `agent/harness.go` |
| 完成通知 | 子代理完成后写结果正文（内容寻址）、发配对 `tool_result`、投递 `<task-notification>` 到父会话；**空闲任务自动唤醒** | `agent/harness.go`、`server/harness_loop.go` |
| 查询/停止 API | `GET /api/harness/delegations/{id}`、`GET …/{did}`、`POST …/{did}/stop` | `server/harness_delegations.go` |
| 控制台 | 委派列表显示「后台」徽标、状态、耗时、结果摘要；可单独停止后台委派 | `web/src/Subagents.jsx` |

## 二、验证

**确定性测试**（假 provider，无网络）：

- `TestBackgroundDelegationRunsAndNotifies`：立即返回 id、终态、配对 `tool_result`
- `TestBackgroundDelegationIsKilled`：停止后台委派 → `killed`
- `TestBackgroundDelegationRunsIsolated`：隔离目录在委派存储下，空目录清理
- `TestBackgroundDelegationNotifierDeliversAndDrains`：通知交给父会话**且**清空注册表队列
- `TestBackgroundDelegationKeepsNotificationWhenUndeliverable`：投递失败时**不丢**通知
- `TestSubagentToolPoolExcludesAgent`：子代理工具池不含 `Agent`
- `harness/delegation`：Start/Finish/Kill/内容寻址输出/列表汇总/全局注册表

**真实端到端**（MiniMax-M3.1-Flash，任务 #228）：

```
父轮次：派发 d1 → 立即结束（done, rev 1）
子代理：sleep 15 → BG-DONE → 完成（pending=1）
唤醒：  rev 2 · "后台委派完成，自动唤醒会话"
父模型：Read 输出文件 → "后台子代理已完成，唤醒链路验证通过。"
队列：  pending=0（已投递并清空）
```

`go vet ./...`、`make test`、`harness-smoke`、`harness-gate` 自比对（全部 HARD/soft 通过）、
`make build-all` 均通过；控制台产物与源码一致。

## 三、验证中发现并修复的真实缺陷

**后台委派完成通知从未送达父会话。** 三个环节同时缺失：

1. `HarnessAgent.delegationNotify` 字段与 setter 存在，但**从未被接线**（服务端没有调用）；
2. `delegation.Registry.DrainNotifications` 在生产代码里**无人调用**，`pending` 永远 ≥1；
3. 投递接口没有返回值，无法区分"已交给父会话"与"父会话暂停/队列满"。

后果（实测任务 #227）：模型派发后台委派后，因为收不到完成通知，只能反复 `sleep`、
`TaskList`、甚至尝试 `env | grep token` 去找 API 鉴权——既浪费轮次，又产生不必要的敏感操作倾向。

修复：

- `NotifyDelegation` 返回 `bool`，只有成功投递才由 agent 侧 `DrainNotifications()`；
- 服务端接线：投递成功后调用 `engine.Run`（已运行时是 no-op，进程重启后可拉起循环）；
- 通知正文加一行说明，并写一条 `lifecycle` 活动"后台委派完成，自动唤醒会话"；
- 唤醒尊重暂停/删除：投递失败时通知留在队列里，恢复后仍可送达。

## 四、明确不做

| 不做 | 原因 |
|---|---|
| Agent Teams / leader-member 邮箱 / 团队级分派 | 把编排移出会话，与 ADR-0005「单 agent 决策权」冲突；需要时另立 ADR |
| 子代理递归 | 工具白名单不含 `Agent`；深度守卫保留为纵深防御 |
| 后台子代理写回任务工作区 | 并发委派会互相覆盖，也会污染操作者的 DIFF/检查点视图；隔离目录 + 结果文件是明确契约 |
| 跨进程委派状态 | 注册表是进程内状态；进程重启后历史委派仍可从 `activity` 回放，但不能停止 |

## 五、已知风险

1. **通知与人工消息竞争收件箱**（深度 8）：队列满时投递失败、通知保留，需人工消息后再投递；
   这是刻意的（人工输入优先）。
2. **唤醒可能增加 token 消耗**：每次后台完成都会触发一轮。模型应只在"结果会改变计划"时派发，
   提示词里已写明"完成后会收到通知"；如需更严格，可在策略层限制并发委派数。
3. **隔离目录的保留策略**：写过文件的目录会保留，长期任务可能积累；当前无自动 TTL，
   删除任务时随数据目录一起清理。
