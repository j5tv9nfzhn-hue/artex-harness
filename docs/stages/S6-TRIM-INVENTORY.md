# S6-TRIM-INVENTORY · 后端接口逐条盘点（删 Harness 不需要部分的准备）

日期：2026-11-21　状态：盘点完成，未删任何代码（只读盘点）

背景：artex-harness 的后端是直接拉取的 ARTEX 原版控制台后端，其中一部分 Harness 不需要。下一步是删除那部分后端，再让前端按删完的后端对齐。本文件是删除前的逐条盘点：每条路由都定了去留分组，删除时按分组执行即可；UNCERTAIN 的 62 条在删除前必须逐项再确认。

## 方法说明

1. **路由全集 311 条**：`server/server.go` 里的 `mux.HandleFunc` 注册有 293 条（与 `/tmp/routes.txt` 逐条核对一致）。另外 18 条不在 `server.go`，是在两个 helper 里用字符串拼接注册的，`routes.txt` 漏了它们，本次已补上：`server/finding_traffic.go` 的 `registerFindingTraffic`（证据绑定 7 条，`/api/exploration/findings/{id}/traffic…`）、`server/side_questions.go` 的 `registerSideRoutes`（边问边答 11 条，3 个父路径 × GET/POST/DELETE + 事件流与取消 2 条）。只按 `routes.txt` 的 293 条删会漏这 18 条。

2. **前端是否调用——按方法+路径双重比对**：先抽 `web/src/` 里全部 `/api/…` 字面量，再对每个 fetch 调用做括号配平，取同一调用内的 `method:`（默认 GET）；另有 4 处是变量拼路径后调用，已逐处读代码核实：`Composer.jsx` 的 `POST /api/conversations/{id}/messages` 与 `POST /api/tasks/{id}/say`、`LoginGate.jsx` 的 `POST /api/auth/init|login`、`Trajectory.jsx` 的 `GET /api/harness/trajectory`、`Browser.jsx` 的 `<img>/<a href>` 直出 `GET /api/harness/shot/{hash}`。**只按路径比对会误判**：例如前端只 PATCH `/api/exploration/findings/{id}`，路径比对会误以为同路径的 GET/DELETE 也被调用；前端只 GET `/api/intercept/rules`、`GET /api/mcp`、`GET /api/skills`（SettingsView 里这三块是只读列表，没有新建按钮），路径比对会误以为同路径的 POST 也被调用。方法+路径双重比对后，前端在用为 59 条。

3. **Go 代码内部是否调用**：对每个 handler 名在 `server/ agent/ db/ harness/ traffic/ evidence/ report/ guard/ llmpool/ cmd/` 里按方法引用（`.名字` 或独立调用）检索，排除 `_test.go`、排除注册行与定义行。结论：**没有任何一个 HTTP handler 被 Go 代码直接调用**（唯一的例外是 `GET /api/llm` 被 `POST /api/llm` 的 handler 在内部复用了一次——这两个口是一起死的，都在 DELETE-管理面的“LLM 旧版全局配置”子组里，删的时候不要只删一个）。同名不同物的干扰已排除：例如 `listGoals`/`insertAssets`/`listAssets` 在 `agent/tools.go` 里是 agent 工具的方法，不是 HTTP handler 的调用；`health` 在 `llmpool` 里是字段名。Go 代码也不直接调自己的 HTTP API（只在 `server/auth.go` 里有路径前缀判断、`server/finding_traffic.go` 里有 base 路径常量）。所以“内部用”的判定不能靠 handler 调用，只能靠运行时语义（见分组定义）。

4. **Harness 模式判定**：`server/engine.go:824` 附近——`HarnessMode=true` 时只起一个 goroutine 跑 `harnessLoop`（单 agent），“没有 planner 循环、没有 worker 池、没有 frontier”；`false` 时起 plannerLoop + N 个 workerLoop。`server/goals.go` 也写明 harness 模式“没有种子意图、不做目标分解、没有 planner”。凡是只服务 planner/worker 循环的端点（frontier、intent、节点图、worker、main-session、目标/约束）都归入 DELETE-多智能体 exploration 专属。

5. **阶段一/二归属**：阶段一（P0：guard 意图拦截、RoE 范围、沙箱策略）与阶段二（P1：audit 审计链、verify 验证门、traffic 重放、pentest 报告）本身没有多少专属 HTTP 口——拦截与沙箱在服务端内部生效，审计链/重放/报告的三个新口前端都在用。KEEP-阶段一/二只收了复测 3 条（验证门的第三条腿，见下文）。

6. **删除时只删 HTTP 层，不动底层**：很多被删端点的底层 db 方法仍被 agent 工具或服务端内部使用（例如 `insertAssets`/`listAssets` 的 db 方法是 agent 工具 `insert_assets`/`list_assets` 的实现；`notify` 的投递循环 `server/notifier.go` 直接读 db 的渠道表；`asset-intercept` 的匹配引擎 `db/asset_intercept_match.go` 在拦截链里生效；`guard/scope.go` 直接读 `ListTaskScope`）。删端点 ≠ 删表、删工具、删引擎；删 handler 时要确认 handler 文件里是否还留着被别处复用的非 handler 函数。

## 分组汇总

| 分组 | 条数 | 含义 |
| --- | --- | --- |
| KEEP-前端在用 | 59 | 前端已按方法+路径调用，删了控制台立刻断 |
| KEEP-运行时内部用 | 3 | 前端没调，但属于 harness 线程协议（SSE + 断点续接），有文档记载 |
| KEEP-阶段一/二 | 3 | 阶段二验证门的复测口：前端没调，但门要靠它产生 reproduced 结论 |
| DELETE-多智能体 exploration 专属 | 34 | 只服务 planner/worker 模式，harness_mode=true 时不走这条路 |
| DELETE-管理面 | 150 | 纯管理/运维面：配置管理、归档、通知、同步、自更新等，前端不调、内部不用 |
| UNCERTAIN | 62 | 无法判定，删前必须逐项确认（见末节） |
| **合计** | **311** | 与路由全集逐条对账一致，无重叠、无遗漏 |

删除后后端剩下 65 条（59 + 3 + 3），约为现在的 21%。DELETE 两组共 184 条，约占 59%。

## KEEP-前端在用（59 条）

前端按方法+路径调用的全部路由。删其中任何一条，控制台对应功能立刻报错。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| DELETE | `/api/llm/profiles/{}` | pgDeleteProfile | server/server_mgmt.go |
| DELETE | `/api/tasks/{}` | pgDeleteTask | server/server_mgmt.go |
| GET | `/api/agui/agent` | aguiAgent | server/agui.go |
| GET | `/api/auth/status` | authStatus | server/auth.go |
| GET | `/api/conversations` | pgListConversations | server/conversations.go |
| GET | `/api/conversations/{}/messages` | pgConversationMessages | server/conversations.go |
| GET | `/api/harness/audit-chain` | harnessAuditChain | server/harness_verify.go |
| GET | `/api/harness/browser` | harnessBrowserStatus | server/harness_browser.go |
| GET | `/api/harness/browser/sessions` | harnessBrowserSessions | server/harness_browser_manager.go |
| GET | `/api/harness/budget/{}` | harnessBudget | server/harness_files.go |
| GET | `/api/harness/capabilities` | harnessCapabilities | server/harness_capability.go |
| GET | `/api/harness/checkpoints/{}` | harnessCheckpoints | server/harness_checkpoint.go |
| GET | `/api/harness/checkpoints/{}/{}` | harnessCheckpoints | server/harness_checkpoint.go |
| GET | `/api/harness/diff/{}` | harnessDiff | server/harness_files.go |
| GET | `/api/harness/evidence-integrity` | harnessEvidenceIntegrity | server/harness_capability.go |
| GET | `/api/harness/file/{}` | harnessFileRead | server/harness_files.go |
| GET | `/api/harness/files/{}` | harnessFiles | server/harness_files.go |
| GET | `/api/harness/ready` | harnessReady | server/harness_wiring.go |
| GET | `/api/harness/replay` | harnessReplay | server/harness_replay.go |
| GET | `/api/harness/search` | harnessSearch | server/harness_search.go |
| GET | `/api/harness/shot/{}` | harnessShot | server/harness_browser_manager.go |
| GET | `/api/harness/subagent/{}` | harnessSubagentDetail | server/harness_subagent.go |
| GET | `/api/harness/trajectory` | harnessTrajectory | server/harness_trajectory.go |
| GET | `/api/harness/workers/{}` | harnessWorkers | server/harness_files.go |
| GET | `/api/health` | health | server/server.go |
| GET | `/api/intercept/pending` | interceptListPending | server/intercept.go |
| GET | `/api/intercept/rules` | interceptListRules | server/intercept.go |
| GET | `/api/llm/model-catalog` | pgModelCatalog | server/server_mgmt.go |
| GET | `/api/llm/profiles` | pgListProfiles | server/server_mgmt.go |
| GET | `/api/llm/records` | pgListLLMRecords | server/commands.go |
| GET | `/api/mcp` | pgListMCP | server/server_mgmt.go |
| GET | `/api/report/pentest` | pentestReport | server/harness_verify.go |
| GET | `/api/skills` | fsListSkills | server/server_mgmt.go |
| GET | `/api/tasks` | listTasks | server/server.go |
| GET | `/api/tokens/daily` | tokenDailyStats | server/server.go |
| GET | `/api/traffic` | getTraffic | server/server.go |
| GET | `/api/traffic/exchange` | getTrafficExchange | server/server.go |
| PATCH | `/api/exploration/findings/{}` | patchFinding | server/server.go |
| PATCH | `/api/tasks/{}` | updateTaskMetadata | server/task_metadata.go |
| POST | `/api/auth/init` | authInit | server/auth.go |
| POST | `/api/auth/login` | authLogin | server/auth.go |
| POST | `/api/conversations` | pgCreateConversation | server/conversations.go |
| POST | `/api/conversations/{}/messages` | pgSendConversationMessage | server/conversations.go |
| POST | `/api/harness/browser/restart` | harnessBrowserRestart | server/harness_browser_manager.go |
| POST | `/api/harness/browser/tabs` | harnessBrowserTabs | server/harness_browser_manager.go |
| POST | `/api/harness/checkpoints/{}/{}/restore` | harnessCheckpointRestore | server/harness_checkpoint.go |
| POST | `/api/harness/delegations/{}/{}/stop` | harnessDelegationStop | server/harness_delegations.go |
| POST | `/api/intercept/pending/{}/decide` | interceptDecide | server/intercept.go |
| POST | `/api/llm/models` | pgListModels | server/server_mgmt.go |
| POST | `/api/llm/profiles` | pgSaveProfile | server/server_mgmt.go |
| POST | `/api/llm/profiles/active` | pgActivateProfile | server/server_mgmt.go |
| POST | `/api/tasks` | createTask | server/server.go |
| POST | `/api/tasks/{}/archive` | queueTaskArchive | server/task_archives.go |
| POST | `/api/tasks/{}/control` | control | server/server.go |
| POST | `/api/tasks/{}/exec` | harnessExec | server/harness_terminal.go |
| POST | `/api/tasks/{}/say` | harnessSay | server/harness_wiring.go |
| POST | `/api/traffic/replay` | trafficReplay | server/harness_verify.go |
| PUT | `/api/tasks/{}/llm` | updateTaskLLMProfiles | server/server.go |
| PUT | `/api/tasks/{}/project` | harnessProject | server/harness_project.go |

## KEEP-运行时内部用（3 条）

前端当前没调，但它们是 harness 线程协议的三件套（`docs/07-HARNESS-RUNTIME.md`、`docs/16-ARCHITECTURE-DETAIL.md`、`docs/adr/0005`、S1/S2 均有记载）：thread 给客户端线程身份与 revision 窗口，revisions 给断点续接的轮次列表，stream 是 SSE 包络流。当前控制台走的是另一条实时通道 `GET /api/agui/agent`（前端在用），所以这三条在现有控制台里是备用/外部客户端入口。删了不影响现有控制台，但等于砍掉该协议的对外入口；要删请三条一起删，并同步改文档。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| GET | `/api/harness/revisions` | harnessRevisions | server/harness_api.go |
| GET | `/api/harness/stream` | harnessStream | server/harness_api.go |
| GET | `/api/harness/thread` | harnessThread | server/harness_api.go |

## KEEP-阶段一/二（3 条）

复测是阶段二验证门的第三条腿：确认一个漏洞必须同时满足证据绑定 ≥1、复现步骤非空、最新复测结论为 reproduced。复测跑在独立的复测 agent 会话里（`db.FindingRetestAgentKey`，服务端启动时 seed，`server.go:255`），`POST /api/exploration/findings/{id}/retests` 是人工发起复测的唯一入口，两个 GET 是读复测列表与进行中的复测。前端还没接复测入口（复测列表页缺失），但删了这 3 条，验证门就永远拿不到 reproduced，确认功能会被 422 锁死。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| GET | `/api/exploration/findings/retests/active` | listActiveFindingRetests | server/finding_retests.go |
| GET | `/api/exploration/findings/{}/retests` | listFindingRetests | server/finding_retests.go |
| POST | `/api/exploration/findings/{}/retests` | startFindingRetest | server/finding_retests.go |

## DELETE-多智能体 exploration 专属（34 条）

只服务 planner/worker 模式。harness_mode=true 时：没有 planner 循环、没有 worker 池、没有 frontier（`server/engine.go:824`）、不产生种子意图、不做目标分解（`server/goals.go`）。以下端点在 harness 任务上要么返回空，要么操作的对象根本不存在。

子组：探索核心读口 6（frontier/intents/graph/nodes/main-sessions/new）、activity 4、exploration tokens 1、覆盖率/资产 4、目标 4、约束 4、intent 控制 9、干预目录 1、深入 1。

注意两点：(1) activity 的 4 条——harness 模式也会写 activity 行（`server/harness_loop.go` 的 `emitActivity`），但控制台读的是 trajectory，不是 activity feed；删 activity 口不影响 trajectory。(2) 约束的 4 条——约束只拼进 planner/worker 的系统提示词（`agent/constraints.go` 的 `constraintBlock`，仅 planner.go/worker.go 引用），harness agent 不读约束；删了约束口，harness 任务不受影响。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| DELETE | `/api/tasks/{}/constraints/{}` | deleteConstraint | server/constraints_api.go |
| DELETE | `/api/tasks/{}/goals/{}` | deleteGoal | server/goals_api.go |
| GET | `/api/exploration/activity` | activity | server/server.go |
| GET | `/api/exploration/activity/history` | activityHistory | server/server.go |
| GET | `/api/exploration/activity/stream` | streamActivity | server/server.go |
| GET | `/api/exploration/activity/{}` | activityDetail | server/server.go |
| GET | `/api/exploration/frontier` | frontier | server/server.go |
| GET | `/api/exploration/graph` | explorationGraph | server/server.go |
| GET | `/api/exploration/intents` | intents | server/server.go |
| GET | `/api/exploration/main-sessions` | mainSessions | server/server.go |
| GET | `/api/exploration/nodes` | explorationNodes | server/server.go |
| GET | `/api/exploration/tokens` | tokenStats | server/server.go |
| GET | `/api/harness/interventions` | interventionsIndex | server/harness_intervene.go |
| GET | `/api/tasks/{}/asset-refs` | taskAssetRefs | server/server.go |
| GET | `/api/tasks/{}/constraints` | listConstraints | server/constraints_api.go |
| GET | `/api/tasks/{}/coverage` | taskCoverage | server/server.go |
| GET | `/api/tasks/{}/coverage-graph` | taskCoverageGraph | server/server.go |
| GET | `/api/tasks/{}/goals` | listGoals | server/goals_api.go |
| GET | `/api/tasks/{}/intent-assets` | taskIntentAssets | server/task_assets.go |
| PATCH | `/api/tasks/{}/constraints/{}` | editConstraint | server/constraints_api.go |
| PATCH | `/api/tasks/{}/goals/{}` | editGoal | server/goals_api.go |
| PATCH | `/api/tasks/{}/intents/{}` | interveneEditIntent | server/harness_intervene.go |
| POST | `/api/exploration/findings/{}/deepen` | deepenFinding | server/findings_groups.go |
| POST | `/api/exploration/main-session/new` | newMainSession | server/server.go |
| POST | `/api/tasks/{}/constraints` | addConstraint | server/constraints_api.go |
| POST | `/api/tasks/{}/frontier/order` | interveneOrder | server/harness_intervene.go |
| POST | `/api/tasks/{}/goals` | addGoal | server/goals_api.go |
| POST | `/api/tasks/{}/intents/rerun-blocked` | rerunBlocked | server/server.go |
| POST | `/api/tasks/{}/intents/{}/control` | controlIntent | server/server.go |
| POST | `/api/tasks/{}/intents/{}/kill` | interveneKill | server/harness_intervene.go |
| POST | `/api/tasks/{}/intents/{}/messages` | sendWorkerMessage | server/intent_intervention.go |
| POST | `/api/tasks/{}/intents/{}/rerun` | rerunIntent | server/server.go |
| POST | `/api/tasks/{}/intents/{}/steer` | interveneSteer | server/harness_intervene.go |
| POST | `/api/tasks/{}/replan` | interveneReplan | server/harness_intervene.go |

## DELETE-管理面（150 条）

纯管理/运维面。共同特征：前端不调、Go 代码不调 handler、底层功能要么由 agent 工具或服务端内部直接读 db（删口不影响运行），要么整个功能就是控制台的管理 UI。子组如下（括号内为条数）：

| 子组 | 条数 | 说明 |
| --- | --- | --- |
| Agents 配置管理 + triggers | 21 | agent 的增删改、提示词、wrapup、超时、变量、可见性、触发器。注意：agents 表本身还在用（复测 agent 是 seed 出来的；`GetAgentByKey("reporter")` 等），删的只是 HTTP 管理口，改提示词以后只能改代码（`agent/promptcatalog.go` 的默认值）或直接改库 |
| 技能管理 | 10 | 技能的上传/删除/文件读写/元数据/用量；技能列表与新建已接（KEEP），删的是文件级管理 |
| 通知渠道 | 8 | 通知渠道的增删改测与投递记录；投递循环 `server/notifier.go` 直接读 db，删口不影响已配渠道继续投递 |
| 任务归档 | 7 | 归档列表/恢复/批量删除；归档本身是 db 状态，删口等于去掉归档管理 UI |
| 工作区文件操作 | 7 | workspace 的 list/read/write/mkdir/delete/download/upload；控制台用的是 `/api/harness/files`，不是这组 |
| 自定义工具 | 7 | tools 列表/修改/自定义工具 CRUD/测试/重置；工具定义在 db 里，引擎启动时加载（删口不删加载逻辑） |
| 任务分类 | 6 | 分类的增删改 + 批量改分类 + 单任务改分类；前端全文没有 "categor" 字样，分类系统整体无 UI |
| 公司 | 6 | 公司的增删改查 + 范围 + 重归属；资产/范围功能本身由 agent 工具走 db |
| 设置 | 6 | 全局设置读/写 + git-bash/python 检测 + web 搜索测试；前端从不调 `/api/settings`（SettingsView 只调 profiles/mcp/skills/rules/records） |
| 拦截 judge + 历史 | 6 | judge 配置/用量 + 拦截历史/详情/执行记录；审批待办与规则列表已接（KEEP），删的是 judge 与历史读口 |
| 任务级拦截规则 | 5 | 单任务的拦截规则管理；全局规则列表/新建已接（KEEP） |
| 资产拦截规则 | 5 | 按资产匹配的拦截规则管理；匹配引擎 `db/asset_intercept_match.go` 在拦截链里生效，删口不影响匹配 |
| ScopeSentry 同步 | 5 | 同步状态/数据源/项目/任务/触发同步；无后台定时同步，全靠这组口触发 |
| 日志 + 统计 | 4 | 日志列表/历史/流 + 全局 stats |
| 资产 | 4 | 资产的列表/计数/新增/删除；agent 工具 `insert_assets`/`list_assets` 直接走 db，不受影响 |
| LLM 池与重试策略 | 4 | 模型池状态/重置 + 重试策略读/写；策略本身由服务端启动时从 db 读（`server/llmretry.go`） |
| LLM 旧版全局配置 | 3 | 裸 `/api/llm` 的读/写/测试；已被 profiles 体系取代（SettingsView 只用 profiles） |
| LLM 记录明细 | 3 | 记录的按任务/按模型/单条；记录列表已接（KEEP） |
| MCP 单个管理 | 3 | MCP 服务的删除/工具列表/刷新；MCP 列表与新建已接（KEEP） |
| 流量删除 | 3 | 流量全删/按 host 删/全清；流量列表与重放已接（KEEP） |
| 任务模板 | 4 | 模板的增删改查 |
| 可见性 | 4 | 资源/技能的可见性查询与开关 |
| 自更新 | 4 | 更新检查/应用/回滚/流 |
| 拦截规则写口 | 4 | 全局规则的新建/修改/删除/开关；规则列表已接（KEEP），SettingsView 里的规则区是只读列表，没有新建按钮——删了写口，自定义规则只能改库 |
| Token 统计 | 2 | 按会话 / 全局的 token 用量统计；按日统计已接（KEEP） |
| 命令统计 | 2 | 工具调用列表与按工具聚合统计 |
| 其它 | 7 | 改密码（`POST /api/auth/change-password`，改密码另有 `reset-password.sh`）、GC（`POST /api/gc`）、任务挂资产/摘资产（2 条）、删除 LLM 记录（1 条）、新建 MCP（`POST /api/mcp`，列表已接，MCP 区只读）、新建技能（`POST /api/skills`，列表已接，技能区只读） |

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| DELETE | `/api/agents/{}` | pgDeleteAgent | server/server_mgmt.go |
| DELETE | `/api/asset-intercept/rules/{}` | assetInterceptDeleteRule | server/asset_intercept.go |
| DELETE | `/api/assets` | deleteAssets | server/assets.go |
| DELETE | `/api/companies/{}` | deleteCompany | server/assets.go |
| DELETE | `/api/intercept/rules/{}` | interceptDeleteRule | server/intercept.go |
| DELETE | `/api/llm/records` | pgDeleteLLMRecords | server/commands.go |
| DELETE | `/api/mcp/{}` | pgDeleteMCP | server/server_mgmt.go |
| DELETE | `/api/notify/channels/{}` | notifyDeleteChannel | server/notify_api.go |
| DELETE | `/api/skills/{}` | fsDeleteSkill | server/server_mgmt.go |
| DELETE | `/api/skills/{}/files/{}` | fsDeletePath | server/server_mgmt.go |
| DELETE | `/api/task-archives/{}` | queueTaskArchiveDelete | server/task_archives.go |
| DELETE | `/api/task-categories/{}` | pgDeleteTaskCategory | server/task_categories.go |
| DELETE | `/api/task-templates/{}` | pgDeleteTaskTemplate | server/task_templates.go |
| DELETE | `/api/tasks/{}/assets/{}` | detachTaskAsset | server/task_assets.go |
| DELETE | `/api/tasks/{}/intercept-rules/{}` | taskInterceptDeleteRule | server/task_intercept.go |
| DELETE | `/api/tools/custom/{}` | pgDeleteCustomTool | server/customtool.go |
| DELETE | `/api/traffic` | deleteTraffic | server/server.go |
| DELETE | `/api/traffic/all` | deleteAllTraffic | server/server.go |
| DELETE | `/api/traffic/hosts` | deleteTrafficHosts | server/server.go |
| DELETE | `/api/triggers/{}` | pgDeleteTrigger | server/triggers.go |
| DELETE | `/api/workspace/delete` | wsDelete | server/workspace.go |
| GET | `/api/agents` | pgListAgents | server/server_mgmt.go |
| GET | `/api/agents/{}` | pgGetAgent | server/server_mgmt.go |
| GET | `/api/agents/{}/prompts` | pgListPromptVersions | server/server_mgmt.go |
| GET | `/api/agents/{}/triggers` | pgListTriggers | server/triggers.go |
| GET | `/api/agents/{}/variables` | pgPromptVars | server/server_mgmt.go |
| GET | `/api/agents/{}/visibility` | pgGetAgentVisibility | server/server_mgmt.go |
| GET | `/api/asset-intercept/rules` | assetInterceptListRules | server/asset_intercept.go |
| GET | `/api/assets` | listAssets | server/assets.go |
| GET | `/api/assets/counts` | assetCounts | server/assets.go |
| GET | `/api/commands` | pgListCommands | server/commands.go |
| GET | `/api/commands/stats` | pgToolStats | server/commands.go |
| GET | `/api/companies` | listCompanies | server/assets.go |
| GET | `/api/companies/{}` | getCompany | server/assets.go |
| GET | `/api/intercept/history` | interceptHistory | server/intercept.go |
| GET | `/api/intercept/history/{}` | interceptDetail | server/intercept.go |
| GET | `/api/intercept/history/{}/execution` | interceptExecution | server/intercept.go |
| GET | `/api/intercept/judge` | interceptGetJudgeConfig | server/intercept.go |
| GET | `/api/intercept/judge/usage` | interceptJudgeUsage | server/intercept.go |
| GET | `/api/llm` | getLLM | server/server.go |
| GET | `/api/llm/pool` | pgLLMPoolStatus | server/server_mgmt.go |
| GET | `/api/llm/records/by-model` | pgTokenByModel | server/commands.go |
| GET | `/api/llm/records/tasks` | pgLLMTasks | server/commands.go |
| GET | `/api/llm/records/{}` | pgGetLLMRecord | server/commands.go |
| GET | `/api/llm/retry-policy` | pgGetLLMRetryPolicy | server/server_mgmt.go |
| GET | `/api/logs` | getLogs | server/server.go |
| GET | `/api/logs/history` | getLogsHistory | server/server.go |
| GET | `/api/logs/stream` | streamLogs | server/server.go |
| GET | `/api/mcp/{}/tools` | pgMCPTools | server/server_mgmt.go |
| GET | `/api/notify/channels` | notifyListChannels | server/notify_api.go |
| GET | `/api/notify/deliveries` | notifyListDeliveries | server/notify_api.go |
| GET | `/api/notify/meta` | notifyMeta | server/notify_api.go |
| GET | `/api/settings` | getSettings | server/server.go |
| GET | `/api/settings/git-bash/browse` | gitBashBrowse | server/gitbash.go |
| GET | `/api/skills/missing` | fsMissingSkills | server/server_mgmt.go |
| GET | `/api/skills/{}/files` | fsListFiles | server/server_mgmt.go |
| GET | `/api/skills/{}/files/{}` | fsReadFile | server/server_mgmt.go |
| GET | `/api/skills/{}/usage` | fsSkillUsage | server/server_mgmt.go |
| GET | `/api/stats` | stats | server/server.go |
| GET | `/api/sync/scopesentry/projects` | syncSSProjects | server/sync_scopesentry.go |
| GET | `/api/sync/scopesentry/status` | syncSSStatus | server/sync_scopesentry.go |
| GET | `/api/sync/scopesentry/tasks` | syncSSTasks | server/sync_scopesentry.go |
| GET | `/api/task-archives` | listTaskArchives | server/task_archives.go |
| GET | `/api/task-archives/{}` | getTaskArchive | server/task_archives.go |
| GET | `/api/task-categories` | pgListTaskCategories | server/task_categories.go |
| GET | `/api/task-templates` | pgListTaskTemplates | server/task_templates.go |
| GET | `/api/tasks/{}/intercept-rules` | taskInterceptListRules | server/task_intercept.go |
| GET | `/api/tokens/conversations` | conversationTokens | server/server.go |
| GET | `/api/tokens/usage` | pgUsageStats | server/commands.go |
| GET | `/api/tools` | pgListTools | server/server_mgmt.go |
| GET | `/api/update/check` | updateCheck | server/update.go |
| GET | `/api/update/stream` | updateStream | server/update.go |
| GET | `/api/visibility/skill/{}` | pgSkillVisibility | server/server_mgmt.go |
| GET | `/api/visibility/{}/{}` | pgResourceVisibility | server/server_mgmt.go |
| GET | `/api/workspace/download` | wsDownload | server/workspace.go |
| GET | `/api/workspace/list` | wsList | server/workspace.go |
| GET | `/api/workspace/read` | wsRead | server/workspace.go |
| PATCH | `/api/agents/{}` | pgUpdateAgent | server/server_mgmt.go |
| PATCH | `/api/notify/channels/{}` | notifyUpdateChannel | server/notify_api.go |
| PATCH | `/api/task-categories/{}` | pgRenameTaskCategory | server/task_categories.go |
| PATCH | `/api/task-templates/{}` | pgUpdateTaskTemplate | server/task_templates.go |
| PATCH | `/api/tasks/{}/category` | updateTaskCategory | server/task_categories.go |
| PATCH | `/api/triggers/{}` | pgUpdateTrigger | server/triggers.go |
| POST | `/api/agents` | pgCreateAgent | server/server_mgmt.go |
| POST | `/api/agents/{}/prompt/preview` | pgPreviewPrompt | server/server_mgmt.go |
| POST | `/api/agents/{}/prompt/reset` | pgResetPrompt | server/server_mgmt.go |
| POST | `/api/agents/{}/triggers` | pgCreateTrigger | server/triggers.go |
| POST | `/api/agents/{}/wrapup/reset` | pgResetWrapup | server/server_mgmt.go |
| POST | `/api/agents/{}/wrapup/task-timeout/reset` | pgResetTaskTimeoutWrapup | server/server_mgmt.go |
| POST | `/api/asset-intercept/rules` | assetInterceptCreateRule | server/asset_intercept.go |
| POST | `/api/asset-intercept/rules/{}/toggle` | assetInterceptToggleRule | server/asset_intercept.go |
| POST | `/api/assets` | insertAssets | server/assets.go |
| POST | `/api/auth/change-password` | authChangePassword | server/auth.go |
| POST | `/api/companies` | createCompany | server/assets.go |
| POST | `/api/companies/reattribute` | reattribute | server/assets.go |
| POST | `/api/companies/{}/scope` | addCompanyScope | server/assets.go |
| POST | `/api/gc` | gc | server/server.go |
| POST | `/api/intercept/rules` | interceptCreateRule | server/intercept.go |
| POST | `/api/intercept/rules/{}/toggle` | interceptToggleRule | server/intercept.go |
| POST | `/api/llm` | setLLM | server/server.go |
| POST | `/api/llm/pool/reset` | pgLLMPoolReset | server/server_mgmt.go |
| POST | `/api/llm/retry-policy` | pgSaveLLMRetryPolicy | server/server_mgmt.go |
| POST | `/api/llm/test` | testLLM | server/server.go |
| POST | `/api/mcp` | pgSaveMCP | server/server_mgmt.go |
| POST | `/api/mcp/{}/refresh` | pgRefreshMCP | server/server_mgmt.go |
| POST | `/api/notify/channels` | notifyCreateChannel | server/notify_api.go |
| POST | `/api/notify/channels/{}/test` | notifyTestChannel | server/notify_api.go |
| POST | `/api/notify/deliveries/{}/retry` | notifyRetryDelivery | server/notify_api.go |
| POST | `/api/settings/git-bash/detect` | pgDetectGitBash | server/gitbash.go |
| POST | `/api/settings/python/detect` | pgDetectPython | server/server.go |
| POST | `/api/settings/web-search/test` | testWebSearch | server/server.go |
| POST | `/api/skills` | fsCreateSkill | server/server_mgmt.go |
| POST | `/api/skills/upload` | fsUploadSkill | server/server_mgmt.go |
| POST | `/api/skills/{}/dirs` | fsCreateDir | server/server_mgmt.go |
| POST | `/api/sync/scopesentry/datasource` | syncSSDatasource | server/sync_scopesentry.go |
| POST | `/api/sync/scopesentry/sync` | syncSSRun | server/sync_scopesentry.go |
| POST | `/api/task-archives/delete/batch` | deleteTaskArchivesBatch | server/task_archives.go |
| POST | `/api/task-archives/restore/batch` | restoreTaskArchivesBatch | server/task_archives.go |
| POST | `/api/task-archives/{}/restore` | queueTaskArchiveRestore | server/task_archives.go |
| POST | `/api/task-categories` | pgCreateTaskCategory | server/task_categories.go |
| POST | `/api/task-templates` | pgCreateTaskTemplate | server/task_templates.go |
| POST | `/api/tasks/archive/batch` | queueTaskArchivesBatch | server/task_archives.go |
| POST | `/api/tasks/category/batch` | updateTasksCategoryBatch | server/task_categories.go |
| POST | `/api/tasks/{}/assets` | attachTaskAssets | server/task_assets.go |
| POST | `/api/tasks/{}/intercept-rules` | taskInterceptCreateRule | server/task_intercept.go |
| POST | `/api/tasks/{}/intercept-rules/{}/toggle` | taskInterceptToggleRule | server/task_intercept.go |
| POST | `/api/tools/custom` | pgCreateCustomTool | server/customtool.go |
| POST | `/api/tools/custom/test` | pgTestCustomTool | server/customtool.go |
| POST | `/api/tools/{}/reset` | pgResetTool | server/server_mgmt.go |
| POST | `/api/update/apply` | updateApply | server/update.go |
| POST | `/api/update/rollback` | updateRollback | server/update.go |
| POST | `/api/visibility/skill/toggle` | pgToggleSkillVisibility | server/server_mgmt.go |
| POST | `/api/visibility/toggle` | pgToggleVisibility | server/server_mgmt.go |
| POST | `/api/workspace/mkdir` | wsMkdir | server/workspace.go |
| POST | `/api/workspace/upload` | wsUpload | server/workspace.go |
| POST | `/api/workspace/write` | wsWrite | server/workspace.go |
| PUT | `/api/agents/{}/config` | pgSaveAgentConfig | server/server_mgmt.go |
| PUT | `/api/agents/{}/prompt` | pgSavePrompt | server/server_mgmt.go |
| PUT | `/api/agents/{}/visibility` | pgSetAgentVisibility | server/server_mgmt.go |
| PUT | `/api/agents/{}/wrapup` | pgSaveWrapup | server/server_mgmt.go |
| PUT | `/api/agents/{}/wrapup/task-timeout` | pgSaveTaskTimeoutWrapup | server/server_mgmt.go |
| PUT | `/api/asset-intercept/rules/{}` | assetInterceptUpdateRule | server/asset_intercept.go |
| PUT | `/api/intercept/judge` | interceptSetJudgeConfig | server/intercept.go |
| PUT | `/api/intercept/rules/{}` | interceptUpdateRule | server/intercept.go |
| PUT | `/api/settings` | putSettings | server/server.go |
| PUT | `/api/skills/{}/files/{}` | fsWriteFile | server/server_mgmt.go |
| PUT | `/api/skills/{}/meta` | fsUpdateSkillMeta | server/server_mgmt.go |
| PUT | `/api/tasks/{}/intercept-rules/{}` | taskInterceptUpdateRule | server/task_intercept.go |
| PUT | `/api/tools/custom/{}` | pgUpdateCustomTool | server/customtool.go |
| PUT | `/api/tools/{}` | pgUpdateTool | server/server_mgmt.go |

## UNCERTAIN（62 条）

无法判定，删前必须逐项确认。按簇列出，每簇写明不确定的原因与删错的后果。

### U1 · 发现读侧与导出（8 条）

发现是 planner 与 harness 两个模式的共享输出（阶段二验证门读的就是同一张 findings 表），前端现在没有发现列表页，只能在「取证」签看到绑定数。删了这组，发现就只能靠 PATCH 单条确认，列表、统计、分组、资产树、导出、血缘全没了——等于把发现的读口删光。留到发现列表页定下来再决定。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| DELETE | `/api/exploration/findings/{}` | deleteFinding | server/server.go |
| GET | `/api/exploration/findings` | findings | server/server.go |
| GET | `/api/exploration/findings/asset-tree` | findingAssetTree | server/findings_groups.go |
| GET | `/api/exploration/findings/export` | findingsExport | server/server.go |
| GET | `/api/exploration/findings/groups` | findingGroups | server/findings_groups.go |
| GET | `/api/exploration/findings/stats` | findingStats | server/server.go |
| GET | `/api/exploration/findings/{}` | getFinding | server/server.go |
| GET | `/api/exploration/findings/{}/lineage` | findingLineage | server/server.go |

### U2 · 证据绑定 HTTP 口（7 条）

流量证据绑定的增删改查。agent 有工具直写 db 的路径（`finding_traffic.go` 的 `get_finding_traffic` 工具绑定），阶段二验证门只读 db 的绑定数，所以删了这 7 条验证门不受影响；但它们是人工绑定/解绑流量的唯一入口，删了就只能由 agent 绑定。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| DELETE | `/api/exploration/findings/{}/traffic/{}` | editFindingTraffic | server/finding_traffic.go |
| GET | `/api/exploration/findings/{}/traffic` | getFindingTraffic | server/finding_traffic.go |
| GET | `/api/exploration/findings/{}/traffic/{}` | getFindingTrafficDetail | server/finding_traffic.go |
| GET | `/api/exploration/findings/{}/traffic/{}/body` | getFindingTrafficBody | server/finding_traffic.go |
| PATCH | `/api/exploration/findings/{}/traffic/{}` | editFindingTraffic | server/finding_traffic.go |
| POST | `/api/exploration/findings/{}/traffic` | bindFindingTraffic | server/finding_traffic.go |
| PUT | `/api/exploration/findings/{}/traffic/order` | editFindingTraffic | server/finding_traffic.go |

### U3 · RoE 范围管理（3 条）

任务范围（RoE）的增删改查。范围本身是阶段一的核心：guard 的 `DBScopeChecker`（`guard/scope.go:238`）每次工具调用都读 `ListTaskScope`，agent 工具 `add_task_scope` 也是写路径。HTTP 这 3 条是人工管理范围的唯一入口，删了范围只能由 agent 登记。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| DELETE | `/api/tasks/{}/scope/{}` | taskScopeDelete | server/server.go |
| GET | `/api/tasks/{}/scope` | taskScopeList | server/server.go |
| POST | `/api/tasks/{}/scope` | taskScopeAdd | server/server.go |

### U4 · Harness 模式切换与权限策略（4 条）

`PUT /api/tasks/{id}/harness-mode` 是任务在两种运行模式之间切换的唯一口（创建任务时可直接指定 `harness_mode=true`，前端未接切换 UI）；权限策略 3 条（GET/PUT/grant）是 harness permission policy 的唯一管理口——策略本身是活的，`wirePolicyEvents`（`server/harness_policy_events.go`）每起工具调用都会按策略算一次行为并写入 activity（只做观测，不做否决；否决点仍是 guard/intercept）。删了这 4 条，模式与策略都只能改库。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| GET | `/api/harness/permission` | harnessPermissionView | server/harness_permission.go |
| POST | `/api/harness/permission/grant` | harnessPermissionGrant | server/harness_permission.go |
| PUT | `/api/harness/permission` | harnessPermissionReplace | server/harness_permission.go |
| PUT | `/api/tasks/{}/harness-mode` | harnessMode | server/harness_wiring.go |

### U5 · Eval / 一致性（2 条）

`POST /api/harness/consistency`（两任务运行一致性比对）与 `GET /api/harness/scenarios`（评测场景目录）。注释写明供控制台的 Eval 标签用，但前端没有 Eval 标签、无调用。是一次性评测工具还是要保留的能力，需产品决策。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| GET | `/api/harness/scenarios` | evalScenarios | server/harness_replay.go |
| POST | `/api/harness/consistency` | harnessConsistency | server/harness_replay.go |

### U6 · 委派读口（2 条）

harness 子代理委派的列表与单条详情。委派本身是 harness 机制（单 agent 派生子代理），但控制台读的是 `GET /api/harness/workers/{id}`（已接，返回里自带 delegations），这两个独立读口与 workers 的返回重复，无调用方。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| GET | `/api/harness/delegations/{}` | harnessDelegations | server/harness_delegations.go |
| GET | `/api/harness/delegations/{}/{}` | harnessDelegationOne | server/harness_delegations.go |

### U7 · 流量配套读口（2 条）

`GET /api/traffic/hosts`（host 列表）与 `GET /api/traffic/blob`（超大 body 按哈希取全文）。流量列表与重放已接，但大 body 的详情目前只返回预览 + blob 指针，删了 blob 口，大 body 就看不全。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| GET | `/api/traffic/blob` | getTrafficBlob | server/server.go |
| GET | `/api/traffic/hosts` | getTrafficHosts | server/server.go |

### U8 · 旧版报告与审计（2 条）

`GET /api/report`（旧 `report.Markdown`）与 `GET /api/audit`（guard 内存审计）。前端已改用新版 pentest 报告与 audit-chain，但旧口是旧格式的唯一读者；删之前确认没有外部脚本或文档引用旧格式。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| GET | `/api/audit` | getAudit | server/server.go |
| GET | `/api/report` | getReport | server/server.go |

### U9 · 全局急停、批量控制与激活（3 条）

`POST /api/tasks/pause-all` 对全部任务调单任务 pause（含 harness 任务，`intervenePauseAll` 直接复用 `applyTaskControl`），是全局急停；`POST /api/tasks/control/batch` 是单任务 control（已接）的批量版；`POST /api/active` 是“把某任务设为活跃并恢复其引擎”的入口。三者都无前端调用（前端切任务只改本地 state，不调这口），删了急停与显式激活能力就没了。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| POST | `/api/active` | setActive | server/server.go |
| POST | `/api/tasks/control/batch` | controlTasksBatch | server/task_control.go |
| POST | `/api/tasks/pause-all` | intervenePauseAll | server/harness_intervene.go |

### U10 · 任务 LLM 解析读口（1 条）

`GET /api/tasks/{id}/llm/resolution`：返回 mainagent/planner/worker 三个角色各解析到哪个模型。`PUT /api/tasks/{id}/llm`（已接）是写口，这是读口；删了写完模型就看不到解析结果。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| GET | `/api/tasks/{}/llm/resolution` | taskLLMResolutionHandler | server/task_resolution.go |

### U11 · 旧版对话面（5 条）

`POST /api/chat`（旧版对话入口）、`POST /api/chat/upload`、`GET /api/chat/mentions`、`GET /api/tasks/{id}/chat/status`、`POST /api/tasks/{id}/chat/stop`。Composer 现在走 `/api/conversations/{id}/messages` 或 `/api/tasks/{id}/say`（已接），旧版对话面无调用。删之前确认没有外部客户端还在用旧版对话入口。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| GET | `/api/chat/mentions` | searchChatMentions | server/chat_mentions.go |
| GET | `/api/tasks/{}/chat/status` | taskChatStatus | server/server.go |
| POST | `/api/chat` | chat | server/server.go |
| POST | `/api/chat/upload` | chatUpload | server/chatupload.go |
| POST | `/api/tasks/{}/chat/stop` | stopChat | server/server.go |

### U12 · 会话生命周期（6 条）

会话的改名、改 profile、删除、批量删除、停止、单条消息详情。会话的列表/新建/发消息已接（KEEP），这 6 条是会话的管理操作，前端没有删除/改名会话的 UI。删了会话就删不掉、改不了名。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| DELETE | `/api/conversations/{}` | pgDeleteConversation | server/conversations.go |
| GET | `/api/conversations/{}/messages/{}` | pgConversationMsgDetail | server/conversations.go |
| PATCH | `/api/conversations/{}` | pgRenameConversation | server/conversations.go |
| PATCH | `/api/conversations/{}/profile` | pgUpdateConversation | server/conversations.go |
| POST | `/api/conversations/delete/batch` | pgDeleteConversationsBatch | server/conversations.go |
| POST | `/api/conversations/{}/stop` | pgStopConversation | server/conversations.go |

### U13 · 审批配套读口（4 条）

审批待办列表与裁决已接（KEEP），这 4 条是配套读口：单条待办、按任务列待办、工具拦截配置读/写。删了按任务看待办与改工具配置的能力就没了。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| GET | `/api/intercept/pending/{}` | interceptGetOne | server/intercept.go |
| GET | `/api/intercept/task/{}` | interceptListTaskItems | server/intercept.go |
| GET | `/api/intercept/tool-config` | interceptGetToolConfig | server/intercept.go |
| PUT | `/api/intercept/tool-config` | interceptSetToolConfig | server/intercept.go |

### U14 · Side questions 整组（11 条）

边问边答：不打断主流程，向会话/主对话/worker 追加提问，有独立的 SSE 事件流与取消口。整组 11 条在前端与 Go 代码里零调用（只有注册本身），但它是一个完整功能，不是零散端点——删就是整个功能下线，留就是整组保留，不能只删一半。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| DELETE | `/api/conversations/{}/side-questions` | handleSideQuestions | server/side_questions.go |
| DELETE | `/api/tasks/{}/chat/side-questions` | handleSideQuestions | server/side_questions.go |
| DELETE | `/api/tasks/{}/intents/{}/side-questions` | handleSideQuestions | server/side_questions.go |
| GET | `/api/conversations/{}/side-questions` | handleSideQuestions | server/side_questions.go |
| GET | `/api/side-questions/{}/events` | sideEvents | server/side_questions.go |
| GET | `/api/tasks/{}/chat/side-questions` | handleSideQuestions | server/side_questions.go |
| GET | `/api/tasks/{}/intents/{}/side-questions` | handleSideQuestions | server/side_questions.go |
| POST | `/api/conversations/{}/side-questions` | handleSideQuestions | server/side_questions.go |
| POST | `/api/side-questions/{}/cancel` | cancelSideRequest | server/side_questions.go |
| POST | `/api/tasks/{}/chat/side-questions` | handleSideQuestions | server/side_questions.go |
| POST | `/api/tasks/{}/intents/{}/side-questions` | handleSideQuestions | server/side_questions.go |

### U15 · 其它读口（2 条）

`GET /api/harness/browser/tabs`：浏览器标签列表；标签的开/关走 POST（已接），列表由 sessions 口返回，读口无调用。`GET /api/tasks/{id}`：单任务详情；任务列表已接（KEEP），前端没调过单条读口。

| 方法 | 路径 | Handler | 文件 |
| --- | --- | --- | --- |
| GET | `/api/harness/browser/tabs` | harnessBrowserTabs | server/harness_browser_manager.go |
| GET | `/api/tasks/{}` | getTask | server/server.go |

## 删除时的注意事项

- **先删路由，再删 handler，再删 db**：本次只盘点了 HTTP 层。很多 handler 背后的 db 表与 agent 工具仍在用（见方法说明第 6 点），删 handler 时不要顺手删 db 方法与工具定义；db 清理另做一次盘点。

- **成组删除**：同一功能的读写口要一起删（例如 agents 17 条 + triggers 4 条；side-questions 11 条要么全留要么全删；harness thread/revisions/stream 要么全留要么全删）。

- **删后必跑**：`go vet ./...` 与 `go test ./...`（不含 `make test-db`，需真库）；`go build` 后确认 `server/console-app` 不需要改（前端只调 KEEP 的 59 条）；再按 KEEP-前端在用的 59 条逐一过一遍控制台。

- **UNCERTAIN 的 62 条不要在这轮删**：等产品逐项确认后再进第二轮。

## 删除执行记录（2026-11-21）

按 Julie 2026-11-21 12:52 CST 确认：DELETE 两组全删（多智能体 exploration 专属 34 条 + 管理面 150 条），共 **184 条**；KEEP 三组（65 条）与 UNCERTAIN 62 条一律不动。只动 HTTP 层：删路由注册与 handler 函数本身，不删 db 方法、不删 db 表、不删 agent 工具、不删引擎逻辑、不改前端。

### 删除结果

- 路由注册：`server/server.go` 的 `mux.HandleFunc` 由 293 条减为 **109 条**；helper 内拼接注册的 18 条（`server/finding_traffic.go` 7 条、`server/side_questions.go` 11 条）未动。全集 311 条 → **127 条**。
- 核对：脚本逐条比对 DELETE 清单与注册表——184 条路由注册已全部清除，184 条对应的 handler 函数在 `server/` 下无剩余定义；KEEP/UNCERTAIN 127 条注册全部仍在（16 条 helper 拼接注册的用字符串比对确认，属误报排除）。
- 整文件删除（7 个，均满足“文件内所有 handler 都在 DELETE 清单且无别处复用的非 handler 符号”）：
  `server/goals_api.go`、`server/triggers.go`、`server/task_assets.go`、`server/task_templates.go`、`server/task_categories.go`、`server/notify_api.go`、`server/sync_scopesentry.go`。
- 逐函数删除、文件保留（含死 helper/死常量一并清理，保留被 KEEP/UNCERTAIN 代码复用的符号）：
  `server/server.go`（33 个 handler；修掉无用 import `sort` 与 `norma/tool` 的 `actool`）、`server/server_mgmt.go`（43 个 handler + `agentByKey`、`cleanStrs`、`rewriteSkillFrontmatter`、`skillNameFromFrontmatter`、`readZipEntry`、`globalPromptVars`、`withGlobalVars`、`validateTemplate`、`renderPrompt`、`templateFields` 等死符号；修掉无用 import `archive/zip`、`bytes`、`path`、`text/template`、`text/template/parse`；保留 `pgListLLMRecords`、`pgListProfiles`、`pgSaveProfile`、`pgDeleteProfile`、`pgActivateProfile`、`pgListModels`、`pgModelCatalog`、`pgListMCP`、`fsListSkills`、`skillRelPath`、`pgDeleteTask` 及其工作链）、`server/constraints_api.go`（4 个 handler；保留 `constraintInjectPlanner`/`constraintInjectWorker`）、`server/intent_intervention.go`（`sendWorkerMessage`；保留 `validWorkerMessageRequestID`，UNCERTAIN 的 `side_questions.go` 在用）、`server/gitbash.go`（2 个 handler；保留 `settingGitBashPath`、`shellApplicable`、`gitBashPath`、`applyGitBash`、`shellStatus`）、`server/auth.go`（`authChangePassword`）、`server/findings_groups.go`（`deepenFinding`；保留 UNCERTAIN 的 `findingAssetTree`、`findingGroups`）、`server/workspace.go`（7 个 handler；保留 `saveUpload`，UNCERTAIN 的 `chatupload.go` 在用）、`server/harness_intervene.go`（6 个 handler；保留 UNCERTAIN U9 的全局急停 `intervenePauseAll`）、`server/asset_intercept.go`（5 个 handler；保留 `assetInterceptRuleReq`，`task_intercept.go` 在用）、`server/task_intercept.go`（5 个 handler；保留 `taskInterceptRuleReq`、`buildTaskInterceptRules`，建任务流程在用）、`server/update.go`（4 个 handler；保留 `restartCh`、`RestartRequested`、`bootState`/`SetBootUpdateState`，`cmd/artex/main.go` 在用）、`server/customtool.go`（4 个 handler；保留引擎加载自定义工具的全部逻辑）、`server/commands.go`（7 个 handler；保留 KEEP 的 `pgListLLMRecords`）、`server/assets.go`（10 个 handler；保留 `assetStore()`，`platform_tools.go` 在用）、`server/intercept.go`（10 个 handler；保留拦截引擎 `interceptListPending`/`interceptListRules`/`interceptDecide` 等与 `chatGuard`、`wireInterceptReviewer`）、`server/task_archives.go`（7 个 handler；保留 KEEP 的 `queueTaskArchive` 及其工作链）。
- 测试随端点一起删（测被删端点的测试删，测保留功能的测试修好）：
  整文件删 `server/update_test.go`（只测已删 `releaseCache`）、`server/assets_scope_test.go`（只测已删公司/资产接口）、`server/harness_intervene_test.go`（只测已删 `normalizeSteerMessage`/`interventionsIndex`）、`server/prompt_vars_test.go`（只测已删 `withGlobalVars`/`globalPromptVars`）；
  逐函数删 `server/chat_mentions_test.go`（`TestChatMentionWorkerReceivesServerDetails`，测已删 `sendWorkerMessage`）、`server/findings_groups_test.go`（3 个 `deepenFinding` 测试）、`server/gitbash_test.go`（`TestGitBashDetect`、`TestGitBashBrowseIsWindowsOnlyAndPathGuarded`、`TestValidateGitBashPath`）、`server/skill_upload_test.go`（5 个 `fsUploadSkill` 测试 + `TestSkillNameFromFrontmatterQuoted` + 孤儿 helper `uploadZip`/`buildZip`/`nopWriteCloser`/`zipFile`/`zhSkillMD`）、`server/task_admission_test.go`（`TestRerunAdmissionFailureRestoresIntentState`、`TestTokenStatsWithoutTaskReturnsStableShape`）、`server/task_templates_test.go`（`TestTaskTemplateHTTPCRUD`）、`server/notify_api_test.go`（14 个通知接口测试 + 孤儿夹具 `notifyFixture`/`fakeWebhook` 等；保留 5 个推送引擎纯函数防漂移断言，文件头注释已改写）。
  另修掉上述测试文件里随之无用的 import。

### 验证（2026-11-21，Go 1.26.3，缓存指到 `~/workspace` 下）

| 命令 | 结果 |
|---|---|
| `go vet ./...` | 通过 |
| `go test ./... -count=1` | 除 server 包 8 个测试外全部通过；8 个失败（`finding_traffic_test.go` 5 个、`finding_workflow_test.go` 3 个）全部在 `NewManager` 夹具处报“未找到数据库配置”（本机无 `ARTEX_PG_DSN`、无 `server/artex-harness.json`），属 `make test-db` 类、需真库；且这两文件测的是 UNCERTAIN 保留功能，与删除无关。未跑 `make test-db`。另 `agent` 包的 `TestBackgroundDelegationNotifierDeliversAndDrains` 在全量跑时偶发失败一次，单独 `-count=3` 通过，属时序敏感抖动，与删除无关。 |
| `go build -trimpath -o /tmp/artex-harness-trim ./cmd/artex` | 通过；`-h` 确认默认 `:8797` / `127.0.0.1:8798` |

### 删除后仍存在的问题

- **前端未动、且未受影响**：前端当前实际调用的方法 + 路径组合全部仍在注册表内（逐项核对过：`GET /api/traffic`、`GET /api/intercept/pending`、`/api/llm/profiles` 系列、`/api/mcp`、`GET /api/skills` 等），`web/src/` 内无任何对已删路径的引用（字符串搜索 0 命中），所以现有控制台页面不会因本次删除直接 404。下一步仍按 Julie 指示“前端按删完的后端对齐”做一次全面复核。
- **上游文档仍引用已删接口**：`docs/15-FEATURES.md`、`docs/16-ARCHITECTURE-DETAIL.md`、`docs/design/DESIGN.md`、`README.md`（上游 ARTEX 原文）仍描述已删的管理面接口，本次未改——它们是上游文档而非 harness 文档，是否同步改写待定。
- **8 个 DB 测试在本机环境下无法通过**：`finding_traffic`/`finding_workflow` 的 8 个测试在 `NewManager` 失败时用 `t.Fatal` 而不是像其它 DB 测试那样 `t.Skipf`，无库环境下直接判失败而非跳过；属测试写法既有不一致，需真库（`make test-db`）验证。
- **保留的死符号**：`server/update.go` 的 `bootUpdateState` 现已无调用方（`SetBootUpdateState` 仍被 `cmd/artex/main.go` 调用），为保 `main.go` 不动而保留；`server/` 内无其它仅定义、无引用的导出符号（脚本扫描 0 个）。
- **删代码的副作用**：AST 删函数工具会留下与函数体空行分隔的孤儿 doc 注释，本次已随测试清理一并处理；`go build ./server/ | head` 会掩盖失败退出码，验证须用 `set -o pipefail` 或直接检查退出码。

## 第二轮删除执行记录（2026-11-21，Julie 14:17 CST 确认）

按体验/稳定性标准对 UNCERTAIN 62 条逐簇定去留：留 40 条、删 22 条。后端 127 条 → **105 条**（15 个分组）。只动 HTTP 层，与第一轮相同。

### 已删（22 条）

| 簇 | 条数 | 路由 | 说明 |
|---|---|---|---|
| U5 Eval/一致性 | 2 | POST `/api/harness/consistency`、GET `/api/harness/scenarios` | 一次性评测工具，Eval 标签从未接线 |
| U6 委派读口 | 2 | GET `/api/harness/delegations/{id}`、GET `/api/harness/delegations/{id}/{did}` | 与 `GET /api/harness/workers/{id}` 返回重复；POST `.../stop` 保留，前端 Subagents.jsx 在用 |
| U8 旧版报告与审计 | 2 | GET `/api/report`、GET `/api/audit` | 已被新版 `GET /api/report/pentest` 与 `GET /api/harness/audit-chain` 替代 |
| U11 旧版对话面 | 5 | POST `/api/chat`、POST `/api/chat/upload`、GET `/api/chat/mentions`、GET `/api/tasks/{id}/chat/status`、POST `/api/tasks/{id}/chat/stop` | Composer 已走 `/api/conversations/{id}/messages` 与 `/api/tasks/{id}/say` |
| U14 边问边答整组 | 11 | `/api/conversations/{id}/side-questions`（GET/POST/DELETE）、`/api/tasks/{id}/chat/side-questions`（GET/POST/DELETE）、`/api/tasks/{id}/intents/{iid}/side-questions`（GET/POST/DELETE）、GET `/api/side-questions/{requestID}/events`、POST `/api/side-questions/{requestID}/cancel` | 完整功能但前后端零调用；只删 HTTP 层，旁路引擎（`initSideQuestions`、`drainTaskSideQuestions`、`cancelSideWhere`、`cancelWorkerSide`、快照流水线）保留——`agent/side_questions.go` 的运行时检查点发布仍在用，任务删除/归档时仍调 drain |

### 保留（40 条，UNCERTAIN 剩余）

U1 发现读侧 8 条（发现列表页的前提）、U2 证据绑定 7 条（人工绑定唯一入口）、U3 RoE 范围管理 3 条（授权范围须人手可改）、U4 模式切换与权限策略 4 条、U7 流量配套 2 条、U9 全局急停/批量控制/激活 3 条、U10 LLM 解析读口 1 条、U12 会话生命周期 6 条、U13 审批配套 4 条、U15 单任务详情与浏览器标签 2 条。

### 改了什么

- `server/server.go`：删 12 条路由注册 + `registerSideRoutes` 调用；删 handler `chat`、`taskChatStatus`、`stopChat`、`getReport`、`getAudit`；保留 `cancelTaskChat`/`finishTaskChat`/`fallbackChat`（任务删除/归档/暂停流程在用）；修掉无用 import `intercept`。
- `server/harness_replay.go`：删 `harnessConsistency`、`evalScenarios` 与死 helper `errNotFound`/`notFoundError`；修掉无用 import `encoding/json`、`harness/consistency`。
- `server/harness_delegations.go`：删 `harnessDelegations`、`harnessDelegationOne`；保留 `harnessDelegationStop` 与 retention/GC；修掉无用 import `os`。
- `server/chat_mentions.go`：只删 `searchChatMentions`；`prepareChatMentionMessage` 等保留（`conversations.go` 在用）。
- `server/chatupload.go`：删 `chatUpload` 与死符号 `maxChatUpload`、`safeChatID`、`uniqueUploadPath`；保留 `chatAttachment`、`composeAgentMessage`、`userActivityWithAttachments`、`humanBytes`（会话消息流在用）。
- `server/side_questions.go`：只删 HTTP 层（`registerSideRoutes`、`sideParent`、`handleSideQuestions`、`runSide`、`cancelSideRequest`、`sideEvents`）；引擎与快照流水线保留（见上表）。
- 测试随端点一起删/改：删 4 个边问边答 HTTP 测试（`side_questions_test.go`，改写 1 个检查点测试为直接等 flush 落库，保留 3 个引擎测试）；删 `chat_mentions_test.go` 的 2 个 handler 测试（db 层断言保留）；删 `task_delete_barrier_test.go` 的 2 个 handler 测试；另修掉随之无用的 import。
- 整文件删除：无（5 个文件都还有保留代码）。

### 验证（2026-11-21，Go 1.26.3）

| 命令 | 结果 |
|---|---|
| 路由重数 | `grep -c mux.HandleFunc server/server.go` = 98，+ helper 拼接注册 7 条（finding_traffic）= **105 条**；分组 harness 29、exploration 19、tasks 18、conversations 10、llm 7、intercept 7、traffic 5、auth 3、health/agui/tokens/report/mcp/skills/active 各 1，共 15 组；side-questions、audit、chat 三组消失，report 组剩 pentest 1 条 |
| `go vet ./...` | 通过 |
| `go build -o /tmp/artex-harness-trim2 ./cmd/artex` | 通过；`-h` 确认默认 `:8797` / `127.0.0.1:8798` |
| `go test ./... -count=1`（无库） | 除 server 包 11 个测试外全部通过；11 个失败（finding_traffic 5 个、finding_workflow 3 个、stage2_http 3 个）全部在夹具处报“未找到数据库配置”，属需真库类，与删除无关 |
| `go test ./... -count=1 -timeout 25m`（真库 `artex_harness_test`） | server 包全绿；另有 2 个并行抖动（agent `TestBlackboardToolsReadDirectSources` 与 db `TestUpsertOtherService`，均为 PG 死锁 40P01，单独 `-count=3` 通过，与删除无关——db/ 与 agent/ 本轮未动） |
| 前端核对 | `web/src/` 对 22 条已删路径 0 引用；唯一命中是保留的 POST `.../delegations/{id}/{did}/stop`（Subagents.jsx） |
| 文档 | PROVENANCE 3.15 节；README 路由总表改为 105 条/15 组；`docs/16` 架构图路由数改为 105 条 |

### 删除后仍存在的问题

- 边问边答的引擎代码仍在（快照流水线是活的，删除/归档流程依赖它），但已无任何入口能发起一次旁路提问；将来若重新接线，需恢复 HTTP 层（handler 已删，git 可找回）。
- `side_questions_test.go` 里删掉的 4 个 HTTP 测试曾覆盖旁路并发上限、忙隔离、归档前 drain 等行为；drain 路径本身仍被 `server_mgmt.go`/`task_archives.go` 调用，但不再有直接测试。
