# 0005 · HARNESS 成熟度建设：借鉴边界与内核设计原则

- 日期：2026-11-15
- 状态：已接受
- 关联：[0001 业主决策](0001-owner-decisions.md) · [0002 复制执行底座](0002-copy-artex-as-execution-base.md) ·
  [0003 能力边界](0003-capability-boundary.md) · [07 运行模式](../07-HARNESS-RUNTIME.md) ·
  [10 gate → 长会话观测 → F12](../10-GATE-OBSERVATION-F12.md)

## 背景

S0–S5 交付了可运行的骨架后，我们拉取了 `NanmiCoder/cc-haha`（`/workspace/vendor/cc-haha`，
HEAD `2aa2fc7`）作为成熟 HARNESS 的对照样本，逐层分析其内核与前端。目的是回答两个问题：

1. 一个成熟 HARNESS 应具备哪些机制，我们缺什么；
2. 其中哪些可以进入本项目，以什么方式进入。

同时向业主确认了本项目的意图（2026-11-15）：

| 问题 | 口径 |
|---|---|
| 核心意图 | **对话式单 Agent**：执行权收归一个可对话 agent，由模型自主决定是否委派 |
| 使用与分发 | **开源项目，AGPL-3.0** |
| 下一阶段重点 | **内核健壮性 → 前端工作台 → 多代理编排** |
| 对 cc-haha 的借鉴边界 | **只借鉴设计，不复制代码** |

## 决策 1：cc-haha 只作为设计参照，代码零引入

**cc-haha 当前许可是 `cc-haha Community Source License 1.0`**（见其 `LICENSE` / `LICENSING.md`）：
源码可见但**不是** OSI 认可的开源许可证；接单、商业交付、收费二次开发与对外商业分发需事先取得
书面授权。其仓库亦普遍被认定为源自 Claude Code 泄露源码的本地可运行版本。本仓库是
AGPL-3.0，两者许可证不兼容，代码不能混用。

因此：

- `/workspace/vendor/cc-haha` 是**只读参照检出**，不进入 `artex-harness` 的构建、import 或依赖；
- 允许吸收的是**架构模式、协议形状、交互范式、质量门禁的组织方式**；
- 任何实现必须是 clean-room 的：用本仓库自己的类型、自己的命名、自己的测试重新写；
- 禁止从该检出复制源码片段、提示词文本、组件实现或 schema 定义；
- 每吸收一类设计，在本 ADR 或后续 ADR 里登记「借鉴了什么、差异是什么」，延续
  [PROVENANCE.md](../../PROVENANCE.md) 的可核查纪律。

## 决策 2：吸收能力清单（只列机制，不列实现）

对照结论是：**norma SDK 里已经存在 cc-haha 内核的大部分机制**，本阶段的工作主要是
「启用 / 暴露 / 接线」，而不是重写引擎。这也是最低风险、最符合行为一致性（决策 2）的路线。

| 机制 | cc-haha 形态 | 本仓库已有 | 本阶段动作 |
|---|---|---|---|
| 流式工具执行 | 模型生成中即开始工具 | `harness/streaming.go` 的 `streamExec` | 已存在，补观测与测试 |
| 只读并行 / 写入串行 | `partitionToolCalls` + 10 并发 | `streamExec` 的 FIFO + exclusivity + 信号量 | 已存在，补元数据标注与测试 |
| 孤儿 `tool_use` 补发 | 中断时合成 `tool_result` | `drainSynthetic` / `results()` | 已存在，补回归测试 |
| 工具契约 | `isReadOnly` / `isConcurrencySafe` / `maxResultSize` | `CoreTool` 已有前两者；全局 `capOutput` | 扩展能力注册表元数据 |
| 权限规则与模式 | 规则 + 6 模式 + 决策原因 | `norma/permission` 已有 6 模式与规则；平台 `guard/intercept` 已有 allow/deny/ask | 在 HARNESS 路径接线并暴露 |
| 恢复策略 | escalate / resume(3×) / 反应式压缩 | `query.go` 已有 `handleMaxOutputTokens` / `Reactive` | 启用 `EscalateMaxTokens` 并补测试 |
| 事件协议 | 类型化 WebSocket 事件 | `harness/protocol` 的 Envelope + AG-UI SSE | 补决策原因 / 计时字段 |
| 后台子代理 | `LocalAgentTask` + 通知队列 | norma `Task`/`TaskOutput` + `Agent` 委派 | 放到 Sprint 3 |
| 会话检查点 / 轨迹 | 轮次检查点、撤销、轨迹视图 | revision + replay | 放到前端工作台阶段 |

**明确不吸收**：

- Electron / Tauri 桌面壳、手机 App、IM 接入、桌面宠物、Computer Use —— 与决策 4
  （裸机单二进制）冲突；
- Agent Teams / 邮箱通信 / 团队层编排 —— 与「对话式单 Agent」冲突（见决策 3）；
- 计划预审门、全审批门、强制范围硬约束 —— 决策 5 明确不做；
- 把执行层换成 Node/Bun —— 与同语言同进程、行为一致性冲突。

## 决策 3：多代理编排不越过「单 Agent 决策权」

业主确认的意图与 [ADR-0004 的运行模式](../07-HARNESS-RUNTIME.md) 一致：**一个任务一个会话 agent，
是否委派由这个 agent 自己判断**。因此后续多代理建设限定为：

- 强化**模型自主委派**：后台子代理、显式工具白名单、输出文件、完成通知、隔离目录；
- 子代理**不可递归**（现有 `Agent` 工具白名单不含 `Agent`，保留为纵深防御）；
- **不引入** Agent Teams、leader/member 邮箱、团队级任务分派这类把编排移到会话之外的结构。
  若将来确有需求，必须另立 ADR 由业主确认，不得在实现里悄悄引入。

## 决策 4：Sprint 1 的实现顺序与不变量

顺序：**能力契约与并发可观测（1.1）→ 分层权限接线（1.2）→ 恢复策略启用（1.3）**，
三者共用同一批 `agentcore.Options` 与事件协议，拆开做会返工。

必须守住：

1. **审批门仍是唯一否决点**：`guard.Guard` / `intercept.Interceptor` 的 hooks 链不能被新权限层绕过；
   新的模式/规则只是把它配置化、可观测化。
2. **行为一致性红线不变**：提示词正文、内置拦截规则、拒绝话术逐字不变；任何
   「原本被拦截的动作变为放行」都必须由 `harness-gate` 判红。
3. **失败必须可见**：新增决策、恢复、降级都要产生可回放的事件，不允许静默吞掉。
4. **不做假控件**：后端没有的能力，界面禁用并写明原因。
5. **默认行为不漂移**：新能力默认不改变既有任务的运行结果；HARNESS 仍保持
   `PermissionModeBypass` + 平台 hooks 否决，新增模式是 opt-in。

## 决策 5：验证方式

沿用本仓库已建立的确定性验证，不引入真实模型依赖：

- `make vet`、`make test`（不碰数据库）；
- `go run ./eval/cmd/harness-smoke`（假 provider，离线跑通真实运行时）；
- `harness/consistency` 的 gate 比对（HARD 检查：拒绝话术、拦截集合、无新增放行）；
- 新增机制级回归测试 + 负向对照（把修复去掉必须复现原缺陷，见
  [08 §1](../08-P2-P3-P4-STATUS.md) 的做法）；
- `make build-all` 验证内嵌单二进制链路。

## 后果

- 正面：内核成熟度对齐成熟 HARNESS，且改动集中在「启用与接线」，行为漂移风险最小。
- 代价：能力注册表与权限模型会新增少量 DB/API 表面，需要按
  [CONTRIBUTING.md](../../CONTRIBUTING.md) 的「加一个接口」步骤补测试。
- 风险：cc-haha 检出若被误引入构建，会造成许可证污染。CI 应保证 `vendor/` 下的参照检出
  不参与 `go build ./...`（本仓库 `go.mod` 不含该路径，天然满足）；后续若移动检出位置需复核。
