# S6-FIX-PUNCHLIST：真实任务 #1 后的小问题清单修复（2026-11-21）

Julie 2026-11-21 16:43 CST 确认修这批小问题。实现、回归测试与前端构建已完成；`make test-db` 全量与测试二进制端到端实测按 Julie 2026-11-21 16:58 CST 决定跳过、未做（见第 3 节）。未 commit、未 push、未跑 LLM 真实任务。

## 0. 问题来源

真实完整渗透测试任务 #1（2026-11-21，目标本地靶场 `http://127.0.0.1:18080`，模型 MiniMax-M3.1-Flash）跑完后，完整报告在 `~/workspace/real-pentest-task1-report-20261121.md`（不在仓库内）。它暴露的 6 个问题中，① 流量代理 NO_PROXY 与 ② 沙箱未接入已在 3.18 修完；本批修剩下的 ③ 预算 20 分钟太短、④ 审计链只记报告生成、⑤ 任务摘要发现计数恒 0、⑥ 报告把 pending 写成“确认发现”。

同日的浏览器核查（2026-11-21，第三次隧道才成功；截图 7 份 PDF 在 `~/workspace/.hatch-browser/outputs/browser-task:13f577bb-5dba-5e7f-a528-1c8fc27b509b/`，不在仓库内）又发现：登录 SQLi 详情的流量绑定文案自相矛盾、血缘节点标 confirmed（发现本身是 pending）、发现 / 流量 / 详情初载时短暂闪现 0 条 / 读取中、报告在窄侧栏渲染拥挤。核查未点确认 / 删除 / 复测，未改任何数据。

## 1. 逐项修复

### 1.1 任务摘要发现计数恒 0

- 问题：`GET /api/tasks/1` 的 findings 全 0，实际有 6 条（2 critical / 1 high / 3 medium）；浏览器里任务卡不显示发现计数。
- 根因：`getTask` 从不填充 `dto.Findings`（聚合查询本身是对的）。列表接口走批量聚合，单任务接口此前完全没填该字段。
- 修法：`db/findings.go` 新增 `FindingCountsForTask(taskID)`（口径与 `TaskListMetricsAll` 一致：severity 白名单外 / 为空的记录不计入任一档）；`server/server.go` 的 `getTask` 用它填充，失败时保持 0 不阻塞详情；`web/src/Sidebar.jsx` 任务行加发现数徽章（>0 才显示，严重 >0 时红色）。
- 验证：`server/punchlist_test.go` 的 `TestPunchlistGetTaskFindingsCounts` 在真库下通过。
- 遗留：计数查询失败时保持 0 是刻意设计（不阻塞任务详情）；severity 不在白名单内的记录不计入任一档，与列表接口口径一致。

### 1.2 报告措辞：确认数与修复建议

- 问题：报告执行摘要写“确认发现：6 个”，而 6 条实际全是 pending；发现明细每条写“修复建议：未提供修复建议。”，但详情页实际有详细修复建议（登录 SQLi 详情有参数化查询等 11 条）。
- 根因：报告模板只按总数计数、不看发现状态；修复建议没有取发现详细报告里的现成内容。
- 修法：`report/pentest.go` 的 `PentestFinding` 加 `Status` 字段；新增 `ExtractRemediation(md)`（找含“修复建议” / “修复方案” / Remediation 的标题，截到下一同级或更高级别标题为止，找不到返回 `""`）；执行摘要改按状态计数——“发现共 N 个：已确认 C 个、待确认 P 个、已排除 E 个，其中独立复测验证 V 个，人工越过验证门 O 个”（confirmed / resolved / fixed 计已确认；false_positive / ignored / duplicate / risk_accepted 计已排除；pending / in_progress / 空计待确认）；摘要行尾改 `\n\n` 使风险表前有空行（之前表头被当成段落续行、渲染成带竖线的纯文本）。`server/harness_verify.go` 的 `pentestReport` 传入 `Status: f.Status` 与 `Remediation: report.ExtractRemediation(f.Report)`；修复建议取自发现详细报告的 `findings.report` 字段，无建议时才写“未提供”。`report/pentest_test.go` 的旧措辞断言已同步更新。
- 验证：`report/pentest_remediation_test.go`（`ExtractRemediation`、按状态计数）通过；`TestPunchlistReportWording` 在真库下通过。
- 遗留：无。

### 1.3 发现登记进审计链

- 问题：`audit/1.jsonl` 只有 report_generated，发现登记本身一条都没进链；链能证明的只是“报告生成过”，不是“发现过程可信”。
- 根因：`auditAppend` 未接入 `report_finding` 等 agent 工具调用。
- 修法：`evidence/store.go` 的 `Store` 加 `OnFindingRecorded` 回调，`Store.Record` 落库成功后才调用；`server/finding_traffic.go` 的 `evidenceStore()` 挂回调，调 `auditAppend(taskID, evidence.AuditFindingReported, actor, …)`（actor 取 in.Worker、空则 "harness"）。状态变化（确认 / 越过）此前已在链上，无需再接。
- 验证：`TestPunchlistFindingReportedInAuditChain` 在真库下通过。
- 遗留：只对新登记的发现生效；任务 #1 已登记的 6 条发现不会补写进链。

### 1.4 详情流量绑定文案与血缘标签

- 问题：登录 SQLi 详情页同时出现“流量绑定（0 条）”“该发现共关联 1 条流量快照”“尚未绑定流量证据”三种说法，且下方无记录列表；血缘节点标 “confirmed · harness”，而发现本身是 pending。
- 根因：详情 DTO 不返回绑定列表（`TrafficBindings` 只在导出查询里填充），前端只好拿 `f.traffic_count` 拼文案；血缘节点直接用了探索图里存的状态。
- 修法：`server/server.go` 的 `getFinding` 在 `findingFromDB` 后调 `GetFindingTraffic` 填充 `dto.TrafficBindings`（出错只记日志）；`findingLineage` 把与 `f.NodeID` 匹配的节点 DTO 的 `State` 改写为发现的实际 `f.Status`（不改库里存的探索图状态）；`web/src/Findings.jsx` 详情流量绑定一节删掉 `f.traffic_count` 那句，全部以 `bindings.length` 为准，无绑定时只显示一句“尚未绑定流量证据——绑定入口在 System 侧栏的流量面板，绑定后这里会列出来。”
- 验证：`TestPunchlistFindingDetailBindingsAndLineage` 在真库下通过。核对结论：绑定口径本身没错——真实库里发现 1 的唯一绑定就是合法的 baseline GET /（note 明确说明 PoC 报文未被记录器落盘），矛盾纯因详情 DTO 不返回绑定列表，所以只修了文案 / DTO，没改绑定逻辑。
- 遗留：baseline GET / 绑定仍会在详情里列出（它是合法绑定）；流量绑定本身的缺失（任务 #1 的 6,523 次请求 0 录制）由 3.18 的 proxyEnv 修复解决，需干净重跑验证。

### 1.5 初载闪现与窄栏报告

- 问题：发现列表、流量面板、详情页初载时短暂显示“0 条 / — / 读取中…”后才出现真实数据；报告在 340px 系统侧栏渲染拥挤（大标题换行过大、等级表格显示为带竖线的纯文本）。
- 根因：加载态与空态未区分，数据未到就渲染了空态；报告面板缺少窄栏样式；风险表前的空行缺失是 1.2 已修的模板问题。
- 修法：`Findings.jsx` 列表徽章未加载时显示 "…"；`Stage2.jsx` 的 `TrafficPanel` 未加载时显示“读取中…”（加载中不渲染空态）；`web/src/styles.css` 的 `.report-md` 加窄栏样式（h1 16px、h2 14px、h3 13px、可断行、表格 `width:100%; table-layout:fixed`、pre 横向滚动）。
- 验证：`npm run build` 通过，`server/console-app/` 产物已更新；浏览器复查尚未做。
- 遗留：无。

### 1.6 墙钟预算建任务可传参

- 问题：默认 `HARNESS_RUN_BUDGET` 20 分钟，任务 #1 只做完“发现 + 登记”就被掐停，确认 / 复测没做。
- 根因：预算只认全局环境变量，建任务时无法按任务调整。
- 修法：`db/schema.sql` 加 `run_budget_minutes INTEGER NOT NULL DEFAULT 0`（含 ALTER 补旧库）；`db.Task`、`taskCols`、`scanTask`、`TaskCreateOptions`、`CreateTaskWithOptions` 贯穿；新增 `ValidateRunBudgetMinutes`（合法范围 5–480 分钟）；`server/manager.go` 的 `Task` 与 `taskFromPG`、`server/dto.go` 的 `TaskDTO`（`omitempty`）、`server/server.go` 的 `createTaskReq.run_budget_minutes`（`*int`）贯穿；`agent/harness.go` 的 `HarnessAgent` 加 `runBudget` 字段 + `SetRunBudget` / `RunBudget()`，`Run` 里用 `h.RunBudget()`；`server/task_llm.go` 的 `agentsForTask` 在 `t.RunBudgetMinutes > 0` 时调用 `SetRunBudget`；`server/harness_files.go` 的 `harnessBudget` 在任务上下文中优先报任务级预算。默认值不改：不传用全局 `HARNESS_RUN_BUDGET` / 20 分钟。口径：显式传 0 报 400（防误配），只有缺省才用默认；db 层 0 仍表示“未设置”。
- 验证：`db/run_budget_test.go`（5–480 边界）、`agent/harness_budget_test.go`（预算覆盖 / 清零回默认）通过；`TestPunchlistCreateTaskRunBudget`（传参 / 不传参 / 非法值 400）在真库下通过。
- 遗留：默认值仍是 20 分钟；下次真实任务需建任务时显式传更长的预算。

## 2. 测试与验证（照实写）

- `go vet ./...`、`go build ./...` 通过。
- 新增回归测试：`report/pentest_remediation_test.go`（`ExtractRemediation`、按状态计数）、`agent/harness_budget_test.go`（预算覆盖 / 清零回默认）、`db/run_budget_test.go`（5–480 边界）、`server/punchlist_test.go`（5 个：`TestPunchlistGetTaskFindingsCounts`、`TestPunchlistFindingReportedInAuditChain`、`TestPunchlistFindingDetailBindingsAndLineage`、`TestPunchlistReportWording`、`TestPunchlistCreateTaskRunBudget`）。`report` / `agent` / `db` 三包通过；5 个 punchlist 测试在真库（`ARTEX_PG_DSN` + `ARTEX_TEST_DB=artex_harness_test`）下 5/5 通过。期间修了一个测试本身的 bug（复用 map 导致上一轮的 `run_budget_minutes: 60` 残留），产品代码无问题。
- `npm run build` 通过，`server/console-app/` 产物已更新（新 bundle `index-BQKvlJP5.js` / `index-DSxWSa7u.css`，旧文件已删）。
- 无库全量 `go test ./... -count=1`（日志 `~/workspace/.tmp/test-punchlist-nodb.log`）exit 1，失败 16 个——11 个是已知的需真库 server 测试（finding_traffic 5、finding_workflow 3、stage2_http 3），另 5 个就是新增的 punchlist 测试（它们用 `trafficEvidenceServer` 夹具，本来就需要 PG，在真库下已全绿）；其余 29 个包全绿。

## 3. 测试口径说明

- **无库基线从此是 16 个失败，不是 11 个**：11 个已知需真库的 server 测试 + 5 个新增的 punchlist 测试（夹具需 PG）。PROVENANCE.md 3.19 节已同步更新这个口径；后续章节引用无库基线时以 16 个为准。
- 真库跑法（与既往一致）：`ARTEX_PG_DSN` + `ARTEX_TEST_DB=artex_harness_test`，库 `artex_harness_test` 与开发库 `artex_harness` 分开。
- 未做的验证（Julie 2026-11-21 16:58 CST 决定跳过）：`make test-db` 全量；测试二进制 + scratch 库的端到端实测（调新二进制的任务摘要接口看计数、生成一份报告看措辞、建任务传预算后看回显）。浏览器复查（1.5 的窄栏报告与初载闪现）也尚未做。

## 4. 未做事项

- `make test-db` 全量、测试二进制端到端实测（Julie 决定跳过，见第 3 节）。
- 未跑 LLM 真实任务（干净重跑已由 Julie 暂缓）；未 commit、未 push。
- 未动 3.18 批（proxyEnv / 沙箱）的文件，无联动；未碰现网服务端、靶场、隧道与任务 #1 的 6 条发现。

## 5. 改动文件清单

`db/findings.go`、`db/tasks.go`、`db/schema.sql`、`agent/harness.go`、`evidence/store.go`、`report/pentest.go`、`report/pentest_test.go`（旧措辞断言已同步更新）、`server/server.go`、`server/dto.go`、`server/manager.go`、`server/task_llm.go`、`server/harness_verify.go`、`server/harness_files.go`、`server/finding_traffic.go`、`web/src/Findings.jsx`、`web/src/Stage2.jsx`、`web/src/Sidebar.jsx`、`web/src/styles.css`；新增 `report/pentest_remediation_test.go`、`agent/harness_budget_test.go`、`db/run_budget_test.go`、`server/punchlist_test.go`；`server/console-app/` 产物已更新。
