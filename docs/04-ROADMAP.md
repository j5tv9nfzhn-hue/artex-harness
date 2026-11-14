# 04 · 路线图

> **状态：S0–S5 全部完成。** 各阶段的交付与验收记录见
> [stages/S1](stages/S1.md) · [S2](stages/S2.md) · [S3](stages/S3.md) ·
> [S4](stages/S4.md) · [S5](stages/S5.md)。
> 下文保留原始规划作为对照；与实现的差异（例如 S5 保留 `/legacy/` 一个发布周期）已在
> 对应阶段文档中说明。

原则：**先补"人与引擎之间的确定性通道"，再补"可回放与可评测"，最后才换 UI 外观。**
理由——①③（干涉）与 ④（一致性）是功能正确性问题，UI 主题是表现层问题；先把地基做对，
换皮才有意义。

---

## S0 · 仓库与底座（已完成，2026-11-14）

| 交付 | 状态 |
|---|---|
| 公开仓库 `artex-harness`，AGPL-3.0 继承 + 最强免责声明 | ✅ |
| ARTEX 执行底座复制（534 个 Go 文件 / 16 MB，排除 `.git`、`node_modules`、密钥、产物） | ✅ |
| 来源台账 `PROVENANCE.md`（commit SHA、复制/排除清单、相对上游改动 1 处） | ✅ |
| 文档：评估报告 / harness 调研 / 架构与引擎设计 / 5 项决策 ADR | ✅ |
| 构建验证 | ✅ `go build ./...` 通过；`guard`/`config`/`llmpool` 单测 ok |

## ✅ S1 · 协议与版本一致性（地基）

**目标**：让"一次运行"变成可寻址、可增量拉取、可回放的数据结构。

| 交付 | 说明 |
|---|---|
| `harness/protocol`：Item / Turn / Thread 三概念的 Go 类型 + 事件信封（带 `revision`） | Thread=任务、Turn=规划轮或 work 轮、Item=工具调用/文本/思考/审批 |
| `harness_revisions` 表 + `tasks.revision` 列 | 每轮结束打点；写入路径 bump |
| SSE 通道升级为信封（保留旧 `activity` 通道做兼容） | 前端按 `rev` seq-merge，断线自愈 |
| `graph_overview` 改单快照读（`REPEATABLE READ`） | 修 ④ 的非原子读 |

**验收**：同一任务连续运行，`rev` 单调递增无空洞；并发 4 worker 下 `graph_overview` 不再
出现"edge 已写、节点未写"的半更新（用注入延迟的测试钩子复现）。

**不触碰**：引擎循环语义、终止语义、提示词。

## ✅ S2 · 干预原语 + 控制台 MVP（最高价值）

**目标**：把"干涉"从"求模型"变成"调 API"，并让它在界面上是一等公民。

| 交付 | 接口 |
|---|---|
| 运行中纠偏 | `POST /api/tasks/{id}/intents/{iid}/steer` → `engine.SteerWork` |
| 意图编辑 | `PATCH /api/tasks/{id}/intents/{iid}`（summary / priority / asset_ids） |
| 立即重规划 | `POST /api/tasks/{id}/replan` |
| frontier 排序 | `POST /api/tasks/{id}/frontier/order` |
| 单 work 停止 / 全局急停 | `POST …/intents/{iid}/kill`、`POST /api/tasks/pause-all` |
| 控制台 `Run` 页 | 实时事件流 + 就地 steer/暂停/停止/编辑意图 |
| 控制台 `Plan` 页 | frontier 列表：可编辑、可排序、显示依赖链 |

**验收**：对一条正在跑的意图，从界面发出 steer 后**不中断当前轮**即被 worker 在下一个
工具调用前消费；编辑意图后 planner 下一轮读到新内容（不再是"删了重建"）。

**注意**：这些原语**只增加人的杠杆**，不改变 agent 默认行为，故不与决策 5 冲突。

## ✅ S3 · 回放与一致性评测（决策 2 的落地）

| 交付 | 说明 |
|---|---|
| `harness/replay` | 按 `rev` 区间重建一次运行：命令、响应、LLM 原文、证据绑定 |
| `eval/scenarios/` | scenario 集（本地授权靶场 + 离线 fixture），格式：目标、提示词快照、策略快照、期望 |

| `harness/consistency` | 双跑对照（上游 `df64cf83` vs 本地），输出指标表 |
| `harness/eval` + `harness_*` 表 | 指标入库、行为指纹回归、CI 门禁 |
| 控制台 `Replay` / `Eval` 页 | 回放界面与指标看板 |

**验收（= 决策 2 的验收标准）**：拦截命中集与拒绝话术**逐字一致**；`TerminalReason` 分布、
工具分布、目标判定在阈值内一致；指纹回归可检出人为注入的行为改动（自测：故意改一句拒绝
话术 → 门禁必须红）。

## ✅ S4 · 收敛与一致性修复

| 交付 | 说明 |
|---|---|
| 会话栈收敛（修 ②） | 一个 runner + capability grants；`main_sessions` / `conversations` 合一（旧数据迁移视图）；`MainAgent.Chat` 与 `ChatAgent.Chat` 去重 |
| 能力注册表（P1） | 5 类供给面统一成"能力"对象：名称 / schema / 风险类别 / 超时 / 输出预算 / 留痕策略 |
| 证据完整性报告 | 列出"有绑定/无绑定"的漏洞结论 —— **只报告，不强制**（决策 5） |
| 上游补丁点归档 | `docs/upstream/SOURCE-REVIEW.md` 第 2/3/4/5 条（HTTP 超时、CORS、DSN 脱敏、上传原子性）按需修，逐条记 PROVENANCE |

## ✅ S5 · 前端主题与排版全面替换

| 交付 | 说明 |
|---|---|
| `web-console/` 成为唯一 UI | 新主题、新信息架构（Run/Plan/Replay/Eval + 只读 Policies） |
| 上游 `web/` 退场 | 替换完成后从 `go:embed` 链路移除；保留在 git 历史中 |
| 内嵌单二进制链路复测 | `-tags embedui` 构建 + 裸机分发验证 |

---

## 每阶段不变量（不得违反）

1. **不改上游提示词正文与内置拦截规则**（决策 2 载体）。
2. **不新增强制硬约束、不加全审批门**（决策 5）。
3. **不预设模型供应商**（决策 3）。
4. **保持裸机可跑**：任何阶段结束时 `go build ./...` + 单进程启动必须成立（决策 4）。
5. **相对上游的每一处代码改动都记入 `PROVENANCE.md`**。
