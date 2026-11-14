# 02 · 开源 HARNESS 调研：这类项目通常包含哪些部分、架构如何设计

调研时间：2026-11-14。方法：GitHub API 检索 + 原文抓取（本环境 `web_search` 不可用，改以
`gh api search/repositories` 与直接抓取 README 完成）。
数据源：[ai-boost/awesome-harness-engineering](https://github.com/ai-boost/awesome-harness-engineering)
（741 行索引，按"解决的问题"分类，本节的分类骨架取自它）、
[HKUDS/OpenHarness](https://github.com/HKUDS/OpenHarness)、
[langchain-ai/deepagents](https://github.com/langchain-ai/deepagents)，
以及渗透域既有实现（见第五节）。

---

## 一、术语先对齐：什么是 harness

> "An **Agent Harness** is the complete infrastructure that wraps around an LLM to make it a
> functional agent. The model provides intelligence; the harness provides **hands, eyes,
> memory, and safety boundaries**."
> — HKUDS/OpenHarness

其把 harness 表示为 `Harness = Tools + Knowledge + Observation + Action + Permissions`。
另一句广为引用的工程总结来自 GitHub Copilot 团队：**"模型是引擎，harness 是车。"**
两个说法指向同一件事：**harness 是"模型之外的全部工程"**——它决定模型能看见什么、能碰什么、
做了什么会被记住、以及出错时谁来兜。

检索规模感：`gh api search/repositories -f q='"agent harness"'` 返回 **11,740** 个仓库；
`topic:agent-harness` 返回 **2,009** 个。这是一个已经形成生态的品类，不是新概念。

## 二、这类项目通常包含哪些部分（分类骨架）

以下 12 类是被索引项目实际覆盖的组成面。每类给 1–3 个可直接研究的代表实现。

| # | 组成面 | 一个 harness 在这里要回答的问题 | 代表实现 |
|---|---|---|---|
| 1 | **Agent Loop** | 一轮迭代做什么：observe → plan → act → verify；终止条件怎么写 | ReAct（论文）、Codex Agent Loop、LangGraph 的图式循环 |
| 2 | **规划与任务分解** | 长目标怎么切成可验证子任务；DAG 还是队列 | LangGraph 2.0、open-multi-agent |
| 3 | **上下文投递与压缩** | 何时注入什么上下文；何时压缩/折叠；压缩后如何不丢因果 | Codex `/responses/compact`、Claude Code 五级渐进压缩（公开逆向分析） |
| 4 | **工具设计** | 能力怎么暴露：单一强工具 vs 一组小工具 vs 代码执行 | 工具架构对照研究（"The Devil Is in the Interface"）、CodeAct 模式 |
| 5 | **Skills 与 MCP** | 领域知识如何按需加载；外部能力如何标准化接入 | [modelcontextprotocol/servers](https://github.com/modelcontextprotocol/servers)、Claude Code Skills、`SKILL.md` 约定 |
| 6 | **权限与授权** | 谁在什么时候可以做什么；决策在哪一层落地 | Claude Agent SDK 权限模型、OWASP LLM06:2025「Excessive Agency」 |
| 7 | **记忆与状态** | 跨会话/跨任务保留什么；状态放内存还是库 | Letta(MemGPT) 三层记忆、ADK `DatabaseSessionService` |
| 8 | **任务运行器与编排** | 多 agent 怎么协作；子任务如何隔离与回收 | [strands-agents/harness-sdk](https://github.com/strands-agents/harness-sdk)、Microsoft Agent Framework、AutoGen、CrewAI |
| 9 | **验证与 CI** | 怎么证明"这轮改动没变差" | [promptfoo](https://github.com/promptfoo/promptfoo)、AgentBench、OpenAI「Skill Evals」四维法 |
| 10 | **可观测与追踪** | 每步能否被复现、被审计 | [FailproofAI/failproofai](https://github.com/FailproofAI/failproofai)（harness 全量捕获 + 策略执行） |
| 11 | **调试与开发者体验** | 人怎么看懂 agent 在想什么 | Codex DevTools、DevUI、MCP Inspector |
| 12 | **Human-in-the-Loop** | 人从哪里介入、以什么粒度介入 | OAP（确定性预行动授权 + 签名审计）、Aegis（执行前防火墙） |

另有三个横切面被单独成章：**Security / Sandbox / Permissions**、
**Evals & Verification**、**Production Infrastructure & Operations**。

## 三、底层架构惯例

### 3.1 循环形态：五种执行模式，60% 选 Agent Loop

一篇对 70 个开源 LLM agent 项目的系统分析（《A Scheduler-Theoretic Framework for LLM Agent
Execution》，索引收录于第 1 类）给出经验分布与形式化：**约 60% 采用 Agent Loop 模式**，
其余为 **Event-driven（事件驱动）/ State-machine（状态机）/ Graph-flow（图/流）/ Hybrid**，
并把三者的取舍表述为 **可控性 / 表达力 / 可实现性** 的三角。

对渗透域的含义：单轮 ReAct 不够（长时程、串行依赖、需要人干预），图/事件驱动更像真实攻击链。
ARTEX 的引擎正是 **Event-driven + Frontier 队列**（`server/engine.go:800` 事件唤醒 planner、
`:916` worker 领取）——这是它值得继承的架构判断，而不是缺陷。

### 3.2 职责解耦：brain / hands / session 三分

《Scaling Managed Agents: Decoupling the Brain from the Hands》给出的生产级拆法：

- **brain** = 模型 + harness（循环、提示词、压缩）：无状态、可重启；
- **hands** = 沙箱与工具（执行环境）：可替换、可隔离；
- **session** = **仅追加的事件日志**：崩溃恢复靠 `wake(sessionId)` + `getEvents()` 重放。

这条与渗透向 harness 高度契合：**执行环境是消耗品（Kali 容器随起随销毁），
事件日志是资产（每一步命令与响应都要能回放）**。

### 3.3 协议层：把引擎与前端解耦

- **Codex app-server**：Item / Turn / Thread 三概念，JSON-RPC/JSONL over stdio。官方明确说明
  **"为什么审批流、流式 diff、线程持久化必须自造协议"——MCP 的"面向工具"模型不够用**。
- **ACP（Agent Client Protocol）**：把 LSP 模型套到 agent 上（stdio JSON-RPC），解决
  N×M 编辑器×agent 的集成问题，2026 年底已有 60+ agent / 12+ 编辑器接入。
- **AG-UI**：agent → 前端的流式事件标准（含 HITL 中断），填补 MCP（工具）与 A2A（agent 间）
  之外"agent 到 UI"这一格。

**对我们的直接结论**：harness 需要一个**自有协议**（我们要的是 Item/Turn/Thread 那一层：
线程=任务、轮次=规划轮或 work 轮、项=工具调用/文本/审批），且**运行时令牌**（stop/tool/build
都要 token）——理由与 Codex 相同：审批流与流式 diff 不是"工具调用"，塞进 MCP 会打架。

### 3.4 权限与控制流：分层，而不是一件事

Claude Agent SDK 的权限评估顺序是最常被引用的参考实现：
**hooks → deny 规则 → permission mode → allow 规则 → canUseTool**，
配合 `allowedTools`/`disallowedTools` 声明式收窄与四种权限模式（含 `dontAsk` 的 deny-by-default）。
开源侧的另一种思路是**意图级**而非"命令名黑名单"（`nah`：把工具调用映射到
`filesystem_delete` / `network_outbound` / `lang_exec` 等意图分类，因为**同一个二进制因参数
不同可以无害也可以毁灭**）；再往前一步是 OAP：执行前同步拦截 + 声明式策略 + **签名审计记录**，
其对抗测试中"严格策略 0% 攻击成功 vs 宽松策略 74.6%"。

### 3.5 一个必须知道的反直觉事实：审批提示基本是表演

三份独立证据指向同一结论：

1. Claude Code Auto Mode 工程文：**用户批准了 93% 的权限提示**——审批疲劳让提示失去意义；
   因此改为两阶段分类器（快速单 token 门 → 仅对可疑动作走思维链）。
2. 2026-08 的用户研究（113 名非技术参与者）：**预授权规则反而比逐动作审批少挡约 20 个
   百分点**——因为用户全选"ask"，然后在运行时照样批准，静态承诺退化成运行时提示。
3. 《When "Do Not" Is Not Deny》：481 份公开 `CLAUDE.md` 里**只有约 4%** 的自然语言安全规则
   有对应的内置强制控制——**提示词不是护栏，除非它映射到确定性强制**。

**这条如何落到本项目**：它恰好支持业主口径（决策 5）的一半——**不要造"全审批"**；但它同时
说明另一半：**留下来的那一小撮拦截必须是确定性的、机械的**（极端破坏行为 = `rm -rf`、
`mkfs`、`dd`、`DROP DATABASE`、`TRUNCATE` 这类可由字符串/意图机械判定的类别）。详见
[ADR-0003](adr/0003-capability-boundary.md)。

### 3.6 横切关注点用 middleware，不要写进每个 agent

《How Middleware Lets You Customize Your Agent Harness》把 hook 点固化为六个：
`before_agent` / `before_model` / `wrap_model_call` / `wrap_tool_call` / `after_model` /
`after_agent`，其价值在于**"确定性策略执行（如 PII 脱敏）不能托付给提示词"**。
ARTEX/norma 已有 `PreToolUse`/`PostToolUse`/`Stop` hook 注册表
（`third_party/norma/hook/`、`guard/guard.go:48`），扩展点天然存在，不需要新造。

### 3.7 隔离优于协调

《Scion》（Google 实验项目，"agents 的 hypervisor"）的结论是 **isolation over coordination**：
让每个 agent 在自己的容器/工作树/凭据/网络策略里完全自主，**把护栏放在基础设施层**，
而不是靠 prompt 级别"你们不要互相干扰"。渗透域尤其如此：一个 agent 的扫端口不该影响另一个
agent 的状态。

## 四、评测层：harness 的"没有回归"证明

| 做法 | 出处 | 可借用的点 |
|---|---|---|
| 行为指纹回归 | AgentAssay | 二值 pass/fail 对非确定性工作流无用：**指纹法检出 86% 回归 vs 二值法 0%**；且可在 trace 上离线跑，零额外推理成本 |
| 能力 vs 回归分离 | Agent Evaluation Readiness Checklist（33 项） | 能力集（低通过率、要提升）与回归集（近 100%、要保护）**必须分开**，混在一起会做出错误的优先级判断 |
| 只对"有风险的地方"加贵检查 | OpenAI《Testing Agent Skills Systematically with Evals》 | 先确定性检查（命令序列、token 预算、仓库整洁度），再用 rubric 打分 |
| 分维度评 | 同上 | outcome / process / style / efficiency 四维 |

**对决策 2（行为一致 = 同提示词 + 同策略，验收为拒绝话术与行为逻辑一致）** 的落地意义：
这属于**回归集**而非能力集——目标是"近 100% 一致"，指标应选确定性的、可 diff 的
（话术逐字、拦截命中、终止原因分布），并用行为指纹法做开发期回归。

## 五、渗透域的既有实现（避免重复造轮子）

检索 `autonomous penetration testing agent LLM` 等高星结果，与本项目最相关的几个：

| 项目 | star | 与本项目的关系 |
|---|---|---|
| [jiwoochris/artex-global](https://github.com/jiwoochris/artex-global) | 3.3k | ARTEX 的全球版 fork（英文默认、自带 "authorized/defensive use only" 声明）——说明上游已有国际衍生 |
| [SanMuzZzZz/LuaN1aoAgent](https://github.com/SanMuzZzZz/LuaN1aoAgent) | 1.5k | **最接近本项目的形态**：Planner–Executor–Observer 协作、因果图推理、Plan-on-Graph 动态规划、**Evidence & Artifact Fidelity**、结构化 agent 控制、Tool Runtime、Sandbox Isolation、Durable Runtime State |
| [Broskigx/Oihk-pentesting](https://github.com/Broskigx/Oihk-pentesting) | 10 | **harness 味最正**：governed Kali sandbox + **不可变证据与验证门**（findings 必须"真实执行证据 + 独立验证"）+ 可复现评测环境 + `audit.jsonl` + 沙箱子 agent 波次（1–7 个） |
| [aielte-research/HackSynth](https://github.com/aielte-research/HackSynth) | 323 | **agent + 评测框架**双交付：Planner + Summarizer 双模块，附 CTF 基准（PicoCTF / OverTheWire，200 题）与 `run_bench.py`。论文 [arXiv:2412.01778](https://arxiv.org/abs/2412.01778) |
| [martian56/redcell](https://github.com/martian56/redcell) | 391 | LangGraph plan/act + LiteLLM 供应商无关 + FastAPI/React，在 Kali 容器内端到端跑并出报告 |
| [ASCIT31/Dark-Moon](https://github.com/ASCIT31/Dark-Moon) | 1.2k | 50 个专精 agent + 本地 LLM + Privacy Gateway（隐私网关）|
| [wudidike/pentest_skill](https://github.com/wudidike/pentest_skill) | 35 | 把黑盒 Web 渗透流程做成 **给 AI agent 用的 skill/框架**（而非又一套扫描器）|
| [marvang/ctf-agent](https://github.com/marvang/ctf-agent) | 4 | CHAP：长时程任务中的**上下文交接**协议 |
| 索引 | — | [Yeti-791/Awesome-Offensive-AI-Agentic-Landscape](https://github.com/Yeti-791/Awesome-Offensive-AI-Agentic-Landscape)、[insidetrust/awesome-ai-pentest](https://github.com/insidetrust/awesome-ai-pentest) |

**观察**：这条赛道里已经**有大量"工具集 + 多 agent 角色表"**（50 agents / 227 tools 这种叙事
很常见），但**少有把"可回放 + 可干预 + 可评测"当第一交付物**的。真正稀缺的恰是
OIHK 的"证据门"与 HackSynth 的"基准集"这两件事——也说明本项目的定位（harness，而非工具堆）
是对的差异化方向。

## 六、结论：怎样才是"渗透向 HARNESS"而不是"又一堆渗透工具"

### 6.1 职责划分（学 Codex "build on the open agent harness"）

> 应用拥有：界面、业务上下文、工具（MCP）、审批门。
> harness 拥有：**agent loop 与沙箱化执行**。

映射到本项目：

| 归属 | 内容 |
|---|---|
| **harness 负责（我们的代码）** | 循环与调度、状态与事务、上下文投递与压缩、事件日志与回放、干预原语（steer/编辑/停止）、证据与验证门、评测集与门禁、协议（前后端解耦） |
| **外部供给（不写进 harness）** | 扫描器/利用器/口令工具、字典与 payload、漏洞库、MCP server、skill 文档、Kali 容器镜像 |

**判据（防止滑回"工具堆"）**：任何一条 PR 若新增的是"某个漏洞的检测逻辑"→ 应落在
**skill / MCP / 自定义工具**；若新增的是"让 agent 能更好地使用任意检测逻辑"→ 才属于 harness。

### 6.2 能力供给面（5 类，harness 只认统一接口）

1. **容器化工具**：Kali/专用镜像，`docker exec` 或容器内 agent（OIHK、redcell 路线）；
2. **MCP server**：远程/本地工具标准化接入（`mcphttp/` 已支持 Streamable HTTP 与旧版 SSE）；
3. **Skill 文档**：`SKILL.md` + 参考文件，按需加载（ARTEX `skills/` 已有 api-recon / playwright-cli / scopesentry）；
4. **CLI 包装**：把现成二进制声明为 `kind=shell` 工具，只用于告诉模型"环境里有什么"；
5. **自定义工具**：command / script / http 三类（ARTEX 已实现 `create_custom_tool`）。

harness 的职责是**把这 5 类统一成同一种"能力"对象**（名称、schema、风险类别、超时、
输出预算、留痕策略），而不是替它们实现逻辑。

### 6.3 渗透域特有的四件"必须由 harness 承担"的事

1. **留痕即资产**：每条命令/请求/响应都能回放（ARTEX 的 `traffic` + `llmrec` 已具备）。
2. **证据门**：漏洞结论必须绑定"真实执行证据"，且可被独立验证（ARTEX 已有
   `finding_traffic_bindings` + `evidence_version` + `retester` 复测 agent；OIHK 的同名概念可参考）。
3. **串行链的可控推进**：真实攻击链有前后依赖，需要跨轮共享的规划清单（ARTEX 的
   planner todolist 已实现）与人可干预的 frontier。
4. **长时程可持久**：任务可跨进程重启续跑（ARTEX 的 `db/tasks.go` + transcript 续跑已具备）。

### 6.4 因此本项目的架构取舍（一句话版）

**把"能力"当插件，把"证据"当归档，把"干预"当一等公民，把"一致"当可测指标。**
—— 这四句就是 [03-ARCHITECTURE.md](03-ARCHITECTURE.md) 的设计主线。
