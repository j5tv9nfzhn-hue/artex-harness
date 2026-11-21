# S6 发现列表页（2026-11-21）

## 背景

控制台此前没有发现列表页：发现只能在右侧「系统」侧栏的「取证」签里逐条看到并确认，
看不到全局列表、发不起复测、导不出清单。这是当前最大的体验缺口（第二轮瘦身特意留了
U1 发现读口 8 条，就是为这一页）。

## 页面结构（`web/src/Findings.jsx`）

- `Findings`（默认导出）：列表页。
  - 顶部：标题 + 总条数 + 导出（格式下拉 md-single/csv/json/md-zip，导当前筛选）+ 刷新。
  - 统计卡片（可点）：全部 / 待确认 / 严重 / 高 / 中 / 低，点一张就按它筛选，
    点「全部」清掉严重程度与状态筛选。
  - 筛选行：严重程度、状态、任务（stats 自带的 tasks 下拉，只列有发现的任务）、
    漏洞类型（stats 自带的 vulnclasses）、排序（按严重程度 / 按时间）、关键词搜索。
    改筛选后回到第 1 页。
  - 列表行：严重程度徽标、状态徽标、名称（无名时回退 vulnclass）、摘要（最多两行）、
    任务、流量条数、证据绑定数、资产、时间。点一行进详情。
  - 服务端分页：每页 20 条，上一页 / 下一页 + 第 x / y 页。
- `FindingDetail`（同一文件内）：详情页，点「返回列表」回列表（并刷新列表与统计）。
  - 头部：严重程度 / 状态徽标、复测中徽标、名称、类型、任务（可点回任务对话视图）、时间。
  - 确认：复用 `Stage2.jsx` 的 `patchFindingConfirm` + `GateFailure`——
    422 时行内列出验证门失败原因，填理由可人工越过（理由写入审计链）；
    已确认的显示已确认文案，不再出按钮。
  - 报告：Markdown 渲染（`.report-md`）；`report_stale` 时提示报告已过期。
  - 证据：原文放 `<pre class="replay-md">`。
  - 资产：类型徽标 + 标签。
  - 流量绑定：role、方法、状态、URL、备注；无绑定时说明绑定入口在 System 侧栏流量面板。
  - 血缘：节点链（序号 + 类型 + payload 里最能识别的一行 + 状态/来源/时间）；
    无节点时说明源任务已删除或无关联节点。
  - 复测记录：每条显示状态（等待中/复测中/已完成）与结论徽标（已复现/已修复/未定论）、
    摘要、时间；点开看证据原文与错误。发起复测带可选补充说明（最多 4000 字）。
    打开详情时与发起后各查一次复测列表与进行中列表，不做轮询。
  - 导出本条：`scope=selected` 的 md-single。删除：Dialog 二次确认，删完回列表。

## 入口（`web/src/App.jsx`，按现有视图模式）

- 新 view `"findings"`，顶栏分段（`SegmentedView`）加「发现」签。
  与「用量」一样是全局视图：不要求先选任务，从任务打开时任务筛选默认取当前任务。
  分段一直在顶栏，所以任何视图都能一切就回发现页、再一切就回任务视图。
- 命令面板（⌘K）：「操作」组加「发现列表」。
- 未在 Sidebar 加入口：Sidebar 是任务/对话列表，不是视图导航。

## 接口映射（只用保留的口，后端未改）

| 页面动作 | 接口 |
|---|---|
| 列表 | `GET /api/exploration/findings?page=&limit=20&severity=&status=&vulnclass=&task_id=&q=&sort=` → `{items, total, page, page_size}` |
| 统计卡片与下拉 | `GET /api/exploration/findings/stats` → `{total, pending, critical, high, medium, low, vulnclasses, tasks[]}` |
| 详情 | `GET /api/exploration/findings/{id}`（含 report；`id` 与 `finding_id` 是同一值） |
| 血缘 | `GET /api/exploration/findings/{id}/lineage` → `{nodes, edges}` |
| 导出 | `GET /api/exploration/findings/export?scope=filtered\|selected&format=md-single\|md-zip\|csv\|json`（流式文件下载） |
| 删除 | `DELETE /api/exploration/findings/{id}` |
| 确认 | `PATCH /api/exploration/findings/{id}` `{status:"confirmed"}`，422 → `{error, reasons}`；人工越过加 `human_override` |
| 发起复测 | `POST /api/exploration/findings/{id}/retests` `{notes}` → `{retest, created}`；409 表示复测 Agent 未配置 |
| 复测记录 | `GET /api/exploration/findings/{id}/retests` → `{retests}` |
| 复测中状态 | `GET /api/exploration/findings/retests/active` → `{retests}`（打开详情与发起后各查一次） |

## 没接的口与没做的功能

- `GET /api/exploration/findings/groups` 未接：按任务分组视图暂不做；
  任务筛选改用 stats 自带的 tasks 下拉（只列有发现的任务，带条数）。
- `GET /api/exploration/findings/asset-tree` 未接：按资产浏览是另一视图，不在本页范围。
- 详情里不改严重程度/名称/类型：PATCH 支持这些字段，但本页只做确认、复测、导出、删除，
  改字段是三态（triage）面的事，留给以后。
- 不在列表页直接确认：确认是严肃操作（要过验证门、可能要人工越过写审计链），
  只在详情里做，列表保持只读浏览。
- 复测不轮询：复测跑在独立对话里，结论落库后下次打开详情即见；进行中状态只查一次。
- 流量绑定只展示：绑定/解绑入口仍在 System 侧栏流量面板，不在本页重复做。

## 验证

- `cd web && npm ci --no-audit --no-fund && npm run build` 通过，产物已更新 `server/console-app/`。
- `go vet ./...` 通过；`go build` 通过，`-h` 默认仍 `:8797`。
- `go test ./... -count=1`（无库）除 server 包 11 个需真库测试
  （finding_traffic 5、finding_workflow 3、stage2_http 3）外全绿，与上一轮一致。
- 未在浏览器里点过：需部署后实测列表筛选/分页、详情确认 422 与人工越过、复测发起、导出下载、删除。
