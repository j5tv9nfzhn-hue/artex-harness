# S6 恢复管理面路由 147 条 + 前端接线

日期：2026-11-21。Julie 17:25 CST 确认第一批与第二批一起恢复。PROVENANCE 3.21 节。

## 0. 背景与范围

瘦身第一轮（2026-11-21）按“前端不调就删”删了管理面 150 条路由。事后核查发现其中大量是管理面该有的能力：设置开关没有界面、没有更新入口、改密码、日志统计、通知渠道、归档等全断。本次把其中 **147 条**恢复回来，扣除已被 profiles 体系取代的旧版 LLM 全局配置 3 条（GET/POST `/api/llm`、POST `/api/llm/test`，明确不恢复）。

**不恢复的**：exploration 专属 34 条（含 `goals_api.go` 整文件、`taskIntentAssets`、interventions、constraints、deepenFinding）、第二轮删的 22 条（U5/U6/U8/U11/U14）、UNCERTAIN 剩余 40 条（已在后端，勿动）、KEEP 的 65 条。

**方法（关键纠正）**：被删代码从 git 历史取回，来源是 **d46c53b（瘦身前的父提交），不是 b4dccfe**。b4dccfe 已是瘦身第一轮之后的状态（127 条路由），其 server.go 只有 109 条注册；d46c53b 的 server.go 有 290 条注册。恢复前逐条核对：147 条全部在 d46c53b 存在、HEAD 缺失；d46c53b→HEAD 的 server.go 删除总数 195 条 = 147（本次）+ 34（exploration）+ 3（旧版 LLM）+ 11（第二轮），账对得上。只动 HTTP 层（路由 + handler），不动 db 表、agent 工具、引擎逻辑。7 个工人按文件并行恢复，协调员中央核对与验证。

## 1. 逐分组恢复清单

每组：路由数、文件、前端是否接线。路由逐条清单见 `S6-TRIM-INVENTORY.md` 的 DELETE-管理面表（减去旧版 LLM 3 条）。

| 分组 | 条数 | 文件 | 前端 |
|---|---|---|---|
| Agents 配置管理 + triggers | 21 | `server/server_mgmt.go`、`server/triggers.go` | 后端已恢复，UI 待接 |
| 技能管理 | 10 | `server/server_mgmt.go` | 后端已恢复，UI 待接（设置技能区仍只读） |
| 通知渠道 | 8 | `server/notify_api.go`（整文件取回） | 后端已恢复，UI 待接 |
| 任务归档 | 7 | `server/task_archives.go` | 后端已恢复，UI 待接 |
| 工作区文件操作 | 7 | `server/workspace.go` | 后端已恢复，UI 待接 |
| 自定义工具 | 7 | `server/customtool.go` | 后端已恢复，UI 待接 |
| 任务分类 | 6 | `server/task_categories.go`（整文件取回） | 后端已恢复，UI 待接 |
| 公司 | 6 | `server/assets.go` | 后端已恢复，UI 待接 |
| 设置 | 6 | `server/server.go`、`server/gitbash.go` | **已接**：设置“运行开关”分区 |
| 拦截 judge + 历史 | 6 | `server/intercept.go` | 后端已恢复，UI 待接 |
| 任务级拦截规则 | 5 | `server/task_intercept.go` | 后端已恢复，UI 待接 |
| 资产拦截规则 | 5 | `server/asset_intercept.go` | 后端已恢复，UI 待接 |
| ScopeSentry 同步 | 5 | `server/sync_scopesentry.go`（整文件取回） | 后端已恢复，UI 待接 |
| 日志 + 统计 | 4 | `server/server.go` | **已接**：系统侧栏「日志」签 |
| 资产 | 4 | `server/assets.go` | 后端已恢复，UI 待接 |
| LLM 池与重试策略 | 4 | `server/server_mgmt.go` | 后端已恢复，UI 待接 |
| LLM 记录明细 | 3 | `server/commands.go` | 后端已恢复，UI 待接 |
| MCP 单个管理 | 3 | `server/server_mgmt.go` | 后端已恢复，UI 待接 |
| 流量删除 | 3 | `server/server.go` | 后端已恢复，UI 待接 |
| 任务模板 | 4 | `server/task_templates.go`（整文件取回） | 后端已恢复，UI 待接 |
| 可见性 | 4 | `server/server_mgmt.go` | 后端已恢复，UI 待接 |
| 拦截规则写口 | 4 | `server/intercept.go` | 后端已恢复，UI 待接 |
| Token 统计 | 2 | `server/commands.go`、`server/server.go` | 后端已恢复，UI 待接 |
| 命令统计 | 2 | `server/commands.go` | 后端已恢复，UI 待接 |
| 其它 | 7 | 改密码 `server/auth.go`；GC `server/server.go`；任务挂/摘资产 `server/task_assets.go`（整文件取回）；删除 LLM 记录 `server/commands.go`；新建 MCP、新建技能 `server/server_mgmt.go` | 改密码**已接**（设置“账户安全”分区）；其余 UI 待接 |

### 前端已接的 4 处

- **运行开关**（设置新分区）：GET/PUT `/api/settings`，7 个开关（流量捕获、Agent 自动绑定流量、LLM 轮询、任务并发、联网搜索、LLM 录制、推送总闸）+ 并发上限数字框；懒加载、未读到显示“读取中…”、切换失败自动回滚并 toast。
- **自更新**（设置新分区）：update/check（进分区自动查一次，按钮带 `?force=1`）、apply、rollback、stream（EventSource，现有 `.replay-md` 样式显示进度）；界面写明应用/回滚后服务将重启、请稍后刷新。
- **改密码**（设置“账户安全”分区）：POST `/api/auth/change-password`，字段 `old_password`/`new_password`，前端校验新密码 ≥8 位、两次一致。
- **日志与统计**（系统侧栏「日志」签）：`GET /api/logs?limit=500`、`GET /api/logs/history?before=<db_id>`（加载更早）、`GET /api/stats`（engine_mode、流量条数、资产数、模型是否已配）。

## 2. 恢复中的适配（逐条）

以当前 HEAD 为准，未回滚任何后续修复：

1. **`triggers.go` 的 `agentByPathKey` 与 `agentByKey` 重复**：W2 取回时 `agentByKey` 在 HEAD 已不存在，于是在 `triggers.go` 内新建了逐字相同的本地 helper `agentByPathKey`；W1 同批恢复了 `server_mgmt.go` 里的 `agentByKey`。协调员已统一为 `agentByKey`、删掉重复 helper，两处调用点改走它。
2. **`syncSSRun` 的 `s.companyStore()` 在 HEAD 已不存在**（随公司 HTTP 口一并删除）：改为直接用 db 层 `pg.Companies()`，与 HEAD 现状（`chat_mentions.go`、`manager.go`）一致，原地留注释。
3. **`task_assets.go` 整文件取回带进清单外的 handler**：d46 原文除清单 2 条外还含 `taskIntentAssets`（`GET /api/tasks/{id}/intent-assets`，属 exploration 34 条）。已由协调员删掉该函数，未注册路由。
4. **`tokenStats` handler 恢复但未注册路由**：为让恢复的测试 `TestTokenStatsWithoutTaskReturnsStableShape` 能编译而一并恢复进 `server.go`，但 `GET /api/exploration/tokens` 属 exploration 34 条，不在本次范围，**未注册**；测试本身无库通过。
5. **通知测试消息文案**：d46 原文“这是 ARTEX 推送渠道的测试消息”改为 artex-harness（3.20 身份一致；其余 ARTEX 字样均为功能标识——管理员用户名、注释里的上游模型描述——未动）。
6. **`gitbash_test.go` 漏掉的 `doFunc` 类型声明**：W6 恢复 3 个测试时漏了 d46 原文第 20 行的 `type doFunc` 声明，`go vet` 报 `undefined: doFunc`；协调员补回后通过。其余 diff 是 W6 有意保留的 HEAD 版 `TestGitBashSettingsPersistence` 改写，未动。
7. **`server.go` 的 gofmt**：W6 用 `gofmt -w` 时顺带规范了 HEAD 已有的一处 `createTaskReq` 对齐漂移（HEAD 的 server.go 本来就不是 gofmt-clean），纯空白改动。
8. **注册顺序**：147 条注册行与 d46 逐字一致、零逆序（242 条 d46 路由保持 d46 相对顺序，另 3 条为 HEAD 新增的 pentestReport/harnessAuditChain/trafficReplay）；路径参数名用 d46 原文（`{key}`/`{name}`/`{id}`/`{rid}`/`{assetID}`/`{file...}`/`{kind}`），handler 经 `PathValue`/`pathInt` 读的就是这些名字，统一改 `{id}` 会断掉它们。
9. **各文件与 d46 的一致度**：`server_mgmt.go`、`skill_upload_test.go`、`prompt_vars_test.go`、`notify_api.go`、`task_assets.go`（删掉 taskIntentAssets 后除外）、`task_templates.go`、`task_categories.go`、`assets.go`、`task_archives.go`、`workspace.go`、`customtool.go`、`assets_scope_test.go`、`notify_api_test.go`（与 d46 逐字节一致，HEAD 的 5 个引擎测试与 d46 实测一致）恢复后与 d46c53b 逐字节相同；`intercept.go` 三文件、`triggers.go`（除第 1 条适配外）亦然。`update.go` 只补回 handler 与 `releaseCache`/`updateHub`/`requestRestart`，HEAD 的 restartCh/bootState 脚手架未动。

## 3. 测试与验证（照实写）

- `go vet ./...`、`go build ./...` 通过；`go test ./cmd/... ./config/... -count=1` 通过。
- 无库 `go test ./... -count=1`（日志 `~/workspace/.tmp/test-restore-nodb.log`）exit 1，失败 **30** 个——11 个已知需真库（finding_traffic 5、finding_workflow 3、stage2_http 3）+ 5 个 punchlist + **14 个恢复的通知接口测试**（均在 `trafficEvidenceServer` 夹具处报“未找到数据库配置”，与已知 16 个同类）；其余 28 个包全绿。**无库基线从此是 30 个失败，不是 16 个。**
- 恢复的纯函数测试无库通过：5 个技能上传、5 个 releaseCache、2 个 withGlobalVars、1 个公司 scope 输入、1 个 tokenStats 形状。需库的恢复测试无库时干净跳过：gitbash 3 个、任务模板 1 个、公司 6 个。
- 路由数核对：`grep -c mux.HandleFunc server/server.go` 由 98 变 **245**（+147），helper 7 条不动，总计 **252 条**；新增集合与 147 条清单逐条比对零缺失、零多余；旧版 LLM、exploration tokens、intent-assets、第二轮路由均不在注册表内。README 路由总表已同步为 252 条/34 分组。
- 隔离实测（重建后的测试库 `artex_harness_test`，端口 18997/18998，scratch 数据目录，未碰现网服务端）：`GET /api/settings` 返回 22 个键；`PUT /api/settings` 改 `traffic_capture=true` 后 GET 回显为 true；`GET /api/update/check` 200（仓库尚未发布正式版本，如实报错，见遗留第 2 条）；`GET /api/update/stream` 正常出 SSE（`phase: idle`）；`GET /api/stats`、`/api/logs`、`/api/logs/history`、`/api/notify/channels`、`GET /api/agents` 均 200；`POST /api/auth/change-password` 返回 `{"ok":true}` 且新密码可登录。
- 定向真库子集（**不是**全量 `make test-db`，只跑恢复的测试）：`go test ./server/ -run 'TestNotify|TestGitBash|TestTaskTemplate|TestCompany|TestDeleteCompany|TestListAssets|TestCreateCompany|TestUploadSkill|TestReleaseCache|TestTokenStats|TestWithGlobalVars' -count=1` 在真库下 **42 通过、0 失败**（日志 `~/workspace/.tmp/test-restore-db.log`）。`make test-db` 全量仍未跑（按 Julie 此前决定）。

## 4. 环境注记

验证期间发现虚拟机重启后 PG 集群虽在、但角色与库已被清空（只剩 postgres 角色与系统库，现网服务端进程亦不在）；为做定向真库验证重建了测试角色与 `artex_harness_test` 空库，schema 由服务端/测试夹具自动迁移。未动真实库（`artex_harness` 本就不存在了，未尝试恢复）；现网服务端与靶场的状态恢复不在本次任务内。

## 5. 遗留

- **UI 待接清单**：除第 1 节已接的 4 处外，其余 140 条左右的恢复路由只恢复了后端，前端无入口（Agents 配置、技能文件管理、通知渠道、归档、分类、公司、资产、模板、拦截规则写口、自定义工具、可见性、工作区文件、ScopeSentry 同步、LLM 池/重试、流量删除、Token/命令统计等）。接线时按“用户体验/稳定性优先”排。
- **自更新查不到新版是符合现状的**：`GET /api/update/check` 如实返回“仓库尚未发布正式版本”——`j5tv9nfzhn-hue/artex-harness` 从未发布过 GitHub Release；一键更新要真正可用，需先发布一个带二进制资产的 Release（另加 `ARTEX_UPDATE_GITHUB_TOKEN`，如为私有仓库）。
- **自动更新仍没有**：本次恢复的是一键手动更新（检查/应用/回滚/进度流），无自动检查、无定时任务、无自动应用；如要自动更新需另立项（设置项 + 定时检查）。
- **旧的 toast 调用形**：`SettingsView.jsx` 老代码用的 `toast?.(msg, isErr)` 在 `toast.jsx` 的 `{success, error, info}` 对象上会抛错；本次新写的三处经 `say()` helper 统一走 `toast.success/error`，老调用未动，另派任务修。
- **`make test-db` 全量未跑**：只跑了恢复测试的定向子集（42 通过）；全量仍按此前决定跳过。
- **README 外的上游文档未同步**：`docs/16`、`docs/design/DESIGN.md` 等仍写 105 条/旧分组；本次只改了 README 路由总表。
