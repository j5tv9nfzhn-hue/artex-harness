# 01 · ARTEX 作为 HARNESS 底座的评估

评估对象：`j5tv9nfzhn-hue/ARTEX` @ `df64cf83`（本仓库执行底座的来源）。
方法：**只读**源码审阅 + 运行中 PostgreSQL 实测（`agents` / `tools` / `settings` /
`intercept_rules` 表）；不修改上游任何代码，不对任何外部目标发起测试。
所有结论均可按文中的 `文件:行` 复核。

---

## 一、结论：是不是"完整 HARNESS 的雏形 / 可复用底座"

**ARTEX 本身不是 HARNESS，而是"跑在 HARNESS 上的一个应用"；HARNESS 骨架在其仓库内的
`third_party/norma`（15 个包）。评级：可复用底座 = 是；完整 HARNESS 雏形 = 否（半成品）。**

| 层 | 在 ARTEX 中是谁 | 证据 |
|---|---|---|
| **HARNESS（执行骨架）** | norma SDK：`agentcore` `harness` `tool` `permission` `hook` `transcript` `memory` `skill` `mcp` `subagent` `plan` `coordinator` `compaction` `noa` | `third_party/norma/`，`go.mod` 用 `replace` 指向本地副本（上游仓库已被删除） |
| **应用/编排** | `agent`（goals/planner/worker/mainagent/chat/retester）+ `server`（Engine 双循环、Manager、245 条 REST 路由） | `server/engine.go:748-1237`、`server/server.go:654` 起 |
| **支撑** | traffic（MITM 记录）/ evidence（快照）/ guard+intercept（审批门）/ enrich / llmpool / llmrec / notify / report | 各自独立包 |

### 1.1 可直接继承的底座能力

| 能力 | 位置 | 为什么值得继承 |
|---|---|---|
| 事件驱动双循环引擎 | `server/engine.go:748` `Run`、`:800` `plannerLoop`、`:916` `workerLoop` | 每任务 1 planner + N worker，`Notify()` 边沿 + 心跳兜底，已处理暂停/删除/收尾竞态 |
| 意图领取的 CAS | `server/engine.go:1237` `claimNext` → `db/exploration.go:1173` `ClaimIntent` | 单条 `UPDATE … WHERE state='open'`，无需额外锁即可防重复领取 |
| 双图 + 锚点血缘 | `db/schema.sql`（49 表）；`exploration_nodes` / `exploration_edges` / `exploration_anchors` | "目标是什么"与"测到什么程度"解耦，覆盖度与复用都建立在此 |
| 幂等启动迁移 | `db/db.go:60-96` advisory lock `7337741001` | "重启即迁移"，无需手工 DDL |
| LLM 可靠性 | `llmpool/`（轮询 + 熔断落库恢复）、`agent/provider.go:292-451`（quota-aware） | 多端点故障转移已经工程化 |
| LLM 原始留痕 | `llmrec/`（请求/响应全文 + 用量） | harness 的"可回放"基础 |
| 流量记录代理 | `traffic/traffic.go:137-545`（inline/spill + FTS + 内容寻址 blob） | 渗透域最难得的资产：**每条 HTTP 都留痕** |
| 漏洞证据快照 | `evidence/store.go`、`db/finding_traffic.go` | SHA-256 校验 + `evidence_version` 乐观锁 + 有序绑定 |
| 换装/回滚协议 | `selfupdate/bootstrap.go`（退出码 75 契约、3 次启动失败回滚） | 裸机升级路径已解决 |

### 1.2 作为"完整 HARNESS"缺的东西

1. **策略强制层缺失**：约束是提示词级软约束，越界无执法。`guard/guard.go:1-8` 明确写着
   "RoE 授权范围机制已移除"。
2. **缺"人可调的确定性原语"**：大量能力是**模型工具**而非接口（见第三节 ①③）。
3. **缺事务/一致性边界**：跨 agent 无任务级事务；图读取是非原子多查询（见第三节 ④）。
4. **缺回放与评测层**：无 scenario 集、无 golden transcript、无一致性门禁。
5. **会话栈重复**：三套对话运行器 + 三张会话表（见第三节 ②）。

---

## 二、先回答"拦截能力是否足以阻止不可控行为"

**判断：具备一定拦截能力，但结构性不足以阻止不可控行为；且"用户不方便干涉"成立的成因是
可定位的。** 一手实测（运行中 PostgreSQL）：

### 2.1 拦截层的实际默认值（`settings` 表实测）

| 键 | 实测值 | 含义 |
|---|---|---|
| `llm_judge_enabled` | `false` | **模型兜底审批默认关闭** |
| `llm_judge_fail_action` | `allow` | 审批调用失败 → **放行**（fail-open） |
| `llm_judge_ask_timeout_seconds` | `300` | 转人工后 5 分钟无人应答 |
| `llm_judge_ask_timeout_action` | `deny` | 超时 → 拒绝 |
| `intercept_enabled_tools` | `["Bash","WebFetch","web_search","shell_open","shell_send","Write","Edit","MultiEdit"]` | **审批门只覆盖这 7 个工具** |

### 2.2 内置拦截规则（`intercept_rules` 实测 20 条，全部 `action=deny`）

优先级 100：`rm -rf` / 删除系统关键目录 / `mkfs` / `dd` 覆写设备 / Fork 炸弹 / 关机重启 /
杀死全部进程 / `shred`·`wipe` / 清空防火墙规则 / 破坏性系统命令。
优先级 90：SQL `DROP DATABASE|TABLE|SCHEMA` / `TRUNCATE` / MongoDB drop / Redis `FLUSHALL`。
优先级 80：`curl`·`wget` 发 DELETE / Python HTTP DELETE / 脚本声明 DELETE / 批量清空接口路径 /
删除类接口路径（`/delete|/del|/remove|/unlink|/erase|/destroy`）。
另有 `[内置] 数据外泄管道`，**默认 `enabled=false`**。

规则级默认：`timeout_enabled=true, timeout_seconds=60, timeout_action=deny`。

### 2.3 结论与成因

**拦截是「静态 deny 黑名单 + 7 个工具的开关」**，而不是「策略强制 + 可干预」：

| 缺口 | 证据 | 后果 |
|---|---|---|
| 默认**没有人工审批** | 20 条规则**无一条 `ask`**；judge 默认关 | 人不会被问，也无从介入；`ask` 需手工建规则或开 judge |
| 失败方向朝放行 | `llm_judge_fail_action=allow`；工具表读取失败 `return tools`（`server/assembly.go:239-244`，见 `docs/upstream/SOURCE-REVIEW.md` 第 1 条） | 抖动/异常时**更宽松** |
| 审批面窄 | `intercept_enabled_tools` 仅 7 个 | 自定义工具（`create_custom_tool` 造出的 command/script/http）、MCP 工具、SDK 读工具都不在门内 |
| 范围无强制 | `guard/guard.go:1-8`（RoE 已移除）；`task_scope` 只是覆盖率分母 | 越界靠模型自觉 |
| SDK 权限层被旁路 | `agent/planner.go:412` 与 `agent/worker.go` 均 `PermissionMode: permission.ModeBypass` | 只剩 hook 里的 DB 规则 |

**自主性放大通道**（让"不可控行为"有了放大器）：审批通过后的 Bash 可派生任意子进程、
`run_in_background` 后台任务与 `shell_open` 交互式 shell；`auto` agent 的 `spawn_task` 可从
一次对话扇出整个任务群；`create_custom_tool` 造出的执行类工具不在审批门内。

---

## 三、逐条核实四个现象

### ① 「与主 AGENT 对话时，规划者意图完全无法修改和纠正」

**结论：部分成立。**
**「意图不可修改」= 成立（硬事实）**：全仓库没有任何修改意图内容的路径——`grep "SET payload"`
仅两处，`db/exploration.go:282`（**goal** 可编辑）与 `db/findings.go:729`（finding 报告字段）。
意图节点自 `agent/tools.go:1032` `addOneIntent` 创建后，只有状态迁移
（`db/exploration.go:363` `CompareAndSetIntentState` / `:389` `SoftDeleteIntent` / `:434`
`CancelIntent`）：**summary / asset_ids / priority 一律不可改**。这是设计取向——README 明确
planner 是"唯一意图生成者"，人只能追加。

**「完全无法纠正」= 不成立**：主 agent 实测拥有 5 条间接纠偏通道（`tools` 表绑定）：

| 工具 | 绑定 agent | 作用 |
|---|---|---|
| `steer_work` | mainagent, planner | **运行中不打断**注入纠偏 |
| `add_hint` | mainagent（仅） | 下一轮 planner 读到的战略提示 |
| `add_intent` | mainagent, planner | 追加新方向；`intentItem` 带 `Priority`（`agent/tools.go:1021`）可插高优先级 |
| `set_goals` | mainagent, goals | 运行时补目标（可复活终态任务） |
| `set_constraints` | mainagent, goals | 运行时补/改操作约束 |

**真正的问题在"确定性"**：这 5 条全部**要经过模型转译**；`frontier` 排序是
`priority DESC, id ASC`（`db/exploration.go:1136`）——**人不能排序、不能置顶、没有"立刻重规划"
的接口**（无 REST 重规划端点；hint 也无 REST 端点）。最坏可用手段是"取消（必须填原因）+
自然语言重新引导"，且取消会丢掉该 work 的执行中间态。

**影响**：误规划代价高、纠偏无保证，是"不可控行为"的主要入口。

### ② 「对话页独立的 AGENT 设计过于冗余」

**结论：部分成立，且实质比"冗余"更严重——同一能力三套实现、且在两个入口能力不一致。**

| 运行器 | 入口 | 会话表 | 能力 |
|---|---|---|---|
| `MainAgent` | `POST /api/chat`（任务页） | `main_sessions` | 任务图工具全开（`agent/tools.go` `MainAgentTools`） |
| `ChatAgent` | `/api/conversations/{id}/messages`（对话页） | `conversations` + `conversation_activities` | **纯助手**：`actool.DefaultTools()` + skills/MCP，**不注入任务图上下文** |
| `SideQuestionService` | 旁路提问 | `side_question_sessions` / `_requests` | 单次问答 |

证据：`agent/mainagent.go:112`、`agent/chat.go:105`、`server/conversations.go:563`、
`sidequestion/service.go:22`；`server/server.go:3684`（`/api/chat`）。

- 实测 `agents` 表 10 行 = 6 个内置（goals/planner/mainagent/worker/auto/pentest）
  + reporter/retester + 2 个**测试残留**（`lock_agent_*`、`write_lock_agent_*`，`builtin=false`）。
- 对话页下拉再过滤为 `!builtin || role === "assistant"`
  （`web/src/app/(main)/chat/page.tsx:1203`）→ 只列 `auto` / `pentest` / 自定义。
- 工具面交叠（实测）：`list_assets`/`insert_assets`/`report_finding`/`list_companies` 在 `auto`
  与 `pentest` 上重复绑定；`add_hint`(mainagent) 与 `add_task_hint`(auto) 是同义两份。

**成因**：不是一次性设计失误，而是"每来一个用途就新增一个 agent key + 一套提示词 + 一次
工具绑定"的历史叠加，且两个聊天入口各有一套运行器，**没有统一的 conversation/agent 抽象**。

**影响**：同一句话在不同入口得到不同权限与行为（任务页能改图、对话页不能）；维护面任何
改动都要改两处（`MainAgent.Chat` 与 `ChatAgent.Chat` 结构高度重复）。

### ③ 「人工干涉能力较差」

**结论：成立。** 已有能力（不否认）：任务 pause/resume（含 100 条批量）、delete/archive/restore、
任务级 LLM 切换、排队与并发上限、**目标 CRUD**、**约束 CRUD**、任务范围 CRUD、意图
pause/resume/cancel(soft|hard)/rerun、向已暂停 worker 发消息、工作区文件管理、三级拦截规则
+ 审批队列页、通知渠道、命令审计、手动复测、日志与 LLM 录制页。

关键缺口（逐条有据）：

| 缺口 | 证据 | 后果 |
|---|---|---|
| **运行中纠偏无接口** | `steer_work` 只是模型工具；245 条路由中无 steer；`web/src/lib/api.ts` 无对应函数 | 只能"暂停→发消息"，**暂停即中断当前轮**；不存在不打断的接管 |
| **意图不可编辑** | 见 ① | 无法改目标/资产范围/优先级，只能删了重建 |
| **hint 无 REST** | 全路由表无 hint 端点；`add_hint` 仅 mainagent 绑定；`broadcast-tab.tsx` 仅**展示** kind=hint | 想插提示必须让**模型**替你调工具 |
| **默认无人工审批** | 见 2.1；规则级 `timeout_action=deny` | 人不在 5 分钟→自动拒绝；长任务会卡死或中断 |
| **审批面窄 + fail-open** | 见 2.3 | 抖动/异常时更宽松 |
| **无范围硬约束** | `guard/guard.go:1-8` | 越界靠自觉，`task_scope` 不是执法边界 |

**成本量化**：一次纠偏的最短路径是「暂停(中断) → 输入自然语言 → 期望模型选对工具 → 期望
planner 采纳」，全程**无确定性保证**。

### ④ 「并发调用工具时读写不完整」

**结论：按字面不成立；但风险在另两个面上成立。** 这条与直觉相反，必须给证据。

**同一轮内多个工具调用，写操作被串行化**：
- `readTool` 显式声明 `Concurrent: true`（`agent/tools.go:280-290`）；
  `writeTool` **不声明** → SDK 默认 `false`（`third_party/norma/tool/tool.go:147`）。
- `streamExec` 的调度规则（`third_party/norma/harness/streaming.go:53-112`）：`canStartLocked`
  在"有非安全工具在跑"时拒绝启动**任何**新工具；`processQueue` 遇首个非安全项即 `break`
  保 FIFO。
- ⇒ 12 个写工具（`insert_assets` `record_fact` `report_finding` `add_intent` `prove_goal`
  `add_hint` `set_goals` `set_constraints` `kill_work` `steer_work` `expand_digest` `goal_met`）
  互为 FIFO 屏障；`t.writes.*` 无锁计数因此**不会**竞态（每个 run 有独立 `ToolSet`）。

**"读写不完整"在下面三处是真的**：

| 面 | 证据 | 说明 |
|---|---|---|
| **无任务级事务边界** | 每个工具调用自带事务；`agent/tools_insert.go:341` 逐项 upsert 并返回 `results`+`errors` | 一轮里 5 笔成功、第 6 笔因 max_turns/取消失败 → 前 5 笔照留，无补偿 |
| **图读是非原子快照** | `agent/tools.go:354` `graphOverviewData` 内部 ~10 次独立查询（ListByKind×3 / Edges / CoveredMembers / Frontier / findings / assets…），无事务无版本 | 与此同时默认 3 个 worker（`server/engine.go:764`）+ 1 个 planner 轮在并发写同一张图 → planner 的"态势"可能是**半更新**的 |
| **跨 agent 交错** | 安全性**部分**有保障：资产 upsert 用 `ON CONFLICT … DO UPDATE` 且**同语句内**归并数组（`db/assets.go:531`、`:750`）；公司范围/证据/归档有 advisory / `FOR UPDATE` | 但**没有**"整张图一个版本"的一致视图，也没有任务级写锁 |

**旁证**：上游自带的 `docs/upstream/SOURCE-REVIEW.md` 第 6/7 条（`db/commands.go` 的
`COUNT(*)` 建在 JOIN 结果上）正是**读侧**口径不一致，与本条第 2 点同源。

### 四现象总表

| 现象 | 结论 | 主因 |
|---|---|---|
| ① 意图无法修改/纠正 | **部分成立** | 意图内容无 update 路径（真）；纠偏 5 通道存在但需模型转译、无排序/无立即重规划（"完全无法纠正"为假） |
| ② 对话页独立 AGENT 冗余 | **部分成立（实质更重）** | 3 套对话运行器 / 3 张会话表 / 6 个内置角色 + 工具面交叠 |
| ③ 人工干涉能力差 | **成立** | steer 无接口、意图不可编辑、hint 无 REST、默认无 ask 审批、fail-open、审批面 7 工具、无范围强制 |
| ④ 并发调用工具读写不完整 | **字面不成立** | 写工具被 SDK 串行化；真实缺口在"无任务级事务 + 图读非原子 + 跨 agent 交错" |

---

## 四、对 harness 设计的直接含义

| 上游缺口 | 本项目的应对 | 是否属于"加限制" |
|---|---|---|
| ① 意图不可编辑 / 纠偏无接口 | 提供**确定性干预 API**：意图编辑、`steer`、显式重规划、frontier 排序 | 否，是给人**更多杠杆** |
| ② 会话栈重复 | 收敛为「一个 runner + capability grants」，会话表合一 | 否，是工程收敛 |
| ③ 干涉差 | 控制台把干预做成**一等公民**（就地 steer/编辑/回放）；**保留**上游默认拦截口径 | 否，默认口径**与原版一致**（见 [ADR-0003](adr/0003-capability-boundary.md)） |
| ④ 一致性 | 图读改用单快照事务；引入 `task_revision` 乐观锁与前端 stale 标记 | 否，是正确性修复 |

**本仓库明确不做**（业主口径，见 ADR-0003）：不新增强制范围硬约束、不新增全审批机制、
不改动上游内置拦截规则与默认值。
