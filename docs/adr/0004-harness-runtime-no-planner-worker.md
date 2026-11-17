# ADR-0004 · HARNESS 运行模式：不用规划者/执行者

- 日期：2026-11-14
- 状态：已接受
- 关联：[0001 业主决策](0001-owner-decisions.md) · [0002 复制执行底座](0002-copy-artex-as-execution-base.md) ·
  [0005 成熟度与借鉴边界](0005-harness-maturity-and-borrowing-boundary.md) · [07 运行模式](../07-HARNESS-RUNTIME.md)

> 本 ADR 的正文一直写在 [docs/07-HARNESS-RUNTIME.md](../07-HARNESS-RUNTIME.md) 第一章，
> 但 `docs/07`、`docs/08`、`docs/adr/0005` 都以 "ADR-0004" 引用它而文件缺失。
> 这里补上编号文件，**内容以 docs/07 为准**，不另立新决策。

## 决定

HARNESS **不再使用** ARTEX 的规划者（planner）与执行者（worker）角色。
一个任务 = 一个会话 agent；是否委派子代理，由这个 agent 自己判断。

## 理由（摘要，详见 docs/07 §1）

| ARTEX 管线性质 | 与 HARNESS 目标的冲突 |
|---|---|
| 派发权集中在规划者 | 人的指令要经模型转译才能变成"下一步"，不是确定性动作 |
| 意图队列是工作单位 | 模型自己的判断被降级为"给一个 intent 写摘要" |
| 执行者池按 intent 并行 | 并行粒度由队列决定，而非由"这一步会不会产出大量中间输出"决定 |

成熟 harness（Claude Code / Codex / cc-haha / opcode / pi-app）的共同选择是相反方向：
一个可对话的 agent 持有全部动词。HARNESS 采纳后者，并保留 ARTEX 底座里与角色无关的资产
（双图作为知识、流量留痕、证据快照、审批门、revision/回放/一致性门禁）。

## 落地约束

- 新增 `tasks.harness_mode`（默认 false，**历史任务行为不变**）；
- harness 模式下**不启动**规划者循环、**不启动**执行者池、**不产生意图**、**不做目标分解**；
- harness 模式的工具面**过滤掉**意图/纠偏类工具（`add_intent` `steer_work` `add_hint`
  `get_worker_output` `get_worker_trace` `list_worker_traces` `search_all_worker_traces`）；
- 替代机制：`Agent` 工具（norma `subagent`）+ 深度护栏 + 工具收窄，**子代理的工具集里不含
  `Agent`**，因此不可能递归派发；
- 历史任务可用 `PUT /api/tasks/{id}/harness-mode` 逐个切换，切换在下一轮生效。

## 后果

- 正面：执行权收归一个可对话 agent，人的消息是确定性输入而非"求模型转译"。
- 代价：两套运行模式长期并存（旧管线只服务历史任务），引擎里保留分支。
- 风险：HARNESS 路径的审批门必须显式接线（`HarnessAgent.SetHooks`），漏接即等于没有 gate
  —— 这一点已由 `harness-gate` 抓到过一次真实缺陷（docs/10）。
