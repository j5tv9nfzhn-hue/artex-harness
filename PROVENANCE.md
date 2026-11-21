# PROVENANCE — 来源、复制范围与相对上游的改动

本文件是**可核查的来源台账**：说明本仓库的代码从哪来、复制了什么、排除了什么、改了什么。
任何审查者都应能凭本文件 + `git log` 复现「本仓库 = 上游 + 已知改动」这一等式。

## 1. 上游

| 项 | 值 |
|---|---|
| 项目 | ARTEX — AI 自主渗透测试系统（Go 后端 + Next.js 前端） |
| 复制来源（工作区远端 `origin`） | `https://github.com/Hinln/ARTEX.git` |
| 维护/发布远端（工作区远端 `ci`，亦为上游 `selfupdate` 指向的仓库） | `https://github.com/j5tv9nfzhn-hue/ARTEX.git` |
| 复制时的 commit | `df64cf83d9e35105d2f93792462740b61b449856` |
| 该 commit 时间 | 2026-11-14 07:42:48 +0000 |
| 该 commit 标题 | `refactor(shell): flagsFor 返回副本；探测版本不再谎称 bash` |
| 许可证 | GNU AGPL-3.0（`LICENSE` 逐字节复制） |
| 上游 Go module 路径 | `github.com/Autumn-27/artex`（**本仓库保持不变**，理由见下） |

## 2. 复制范围

**已复制**（`tar` 全量复制，除下表排除项）：

- Go 后端全部包：`agent` `cmd` `config` `db` `enrich` `evidence` `guard` `intercept`
  `llmpool` `llmrec` `mcphttp` `notify` `report` `selfupdate` `server` `sidequestion`
  `traffic`（534 个 `.go` 文件）
- 内置 SDK：`third_party/norma`（15 个包；上游仓库已被作者删除，故为本地可补丁副本，
  `go.mod` 中以 `replace` 指向）
- 前端源码：`web/`（含 `src` `public` `media` 与 npm 锁文件）
- 构建/部署：`build.sh` `install.sh` `update.sh` `dev.sh` `start.sh` `start.bat`
  `reset-password.sh` `Dockerfile` `docker-compose*.yml` `config.example.json`
  `.env.example` `.dockerignore` `.gitattributes` `.github/` `skills/`（`screenshots/` 与上游界面一并移除）
- 上游文档原文，**移动**到 `docs/upstream/` 归档：
  - `README.md` → `README-ARTEX.md`（仓库根，保留原名语义：上游说明）
  - `CHANGELOG.md` → `docs/upstream/CHANGELOG-ARTEX.md`
  - `SOURCE-REVIEW.md` → `docs/upstream/SOURCE-REVIEW.md`
  - `docs/漏洞流量证据.md` → `docs/upstream/漏洞流量证据.md`

**已排除**（及其原因）：

| 排除项 | 原因 |
|---|---|
| `.git/` | 不搬运 14 MB 上游历史；来源以 commit SHA 记录在本文件 |
| `web/node_modules/` | 1.1 GB 依赖树，属可再生构建产物 |
| `web/.next/` `web/out/` `web/tsconfig.tsbuildinfo` | 前端构建中间产物 |
| `data/` | 运行时数据目录（含数据库状态、流量 blob） |
| `jwt.key` | **运行时密钥**，绝不入库 |
| `dist/` `artex` `artex.new` `artex.old` `artex.failed` `artex.upgrade.json` | 二进制与换装中间产物 |
| `config.json` `.env` | 本地配置（可能含数据库密码/密钥） |

**复制后扫描**：已对全树（排除 `node_modules`）检索常见凭据形态
（`sk-ant-…` / `ghp_…` / `AKIA…`），结果为空；`jwt.key` 未被复制。

## 3. 相对上游的改动（截至阶段 0，共 2 处）

> 原则：**改动越少越好**。行为一致性（决策 2）依赖与上游同源的提示词、策略与循环语义，
> 任何非必要改动都是行为漂移的来源，因此本阶段只做"结构性改动"，不动行为。

### 3.1 `selfupdate/github.go` — 更新源指向本仓库

```diff
-const Repo = "j5tv9nfzhn-hue/ARTEX"
+const Repo = "j5tv9nfzhn-hue/artex-harness"
```

**原因**：上游 `selfupdate` 的信任根写死为上游仓库。若保留该值，本仓库页面上的「一键更新」
会下载**上游 ARTEX 的发布二进制**并覆盖本仓库改造过的执行底座——这是供应链层面的行为替换，
不是升级。改为指向本仓库后，本仓库暂无 Release 时检查更新只会报"无可用版本"，不会下载
任何东西。**该改动不影响模型行为**（不触碰提示词、工具、策略、循环）。

### 3.2 `config/config.go` — `Redact` 覆盖 keyword/value DSN（S4）

```diff
-func Redact(dsn string) string {           // 只处理 URL 形式
+func Redact(dsn string) string {           // URL 形式 + libpq keyword/value 形式
```

**原因**：上游复核第 4 条（[docs/upstream/SOURCE-REVIEW.md](docs/upstream/SOURCE-REVIEW.md)）。
`host=… user=… password=…` 形式的 DSN 被 `url.Parse` 当作不透明路径、`u.User` 为空，于是**原样返回**，
而它被拼进连库失败的 `fmt.Errorf`，最终落到服务端日志与前端日志页——恰好是用户最常贴日志求助的时刻。
现按 `password/passfile/sslpassword` 掩码，保留键名与其余上下文。**不影响任何模型行为**。

### 3.3 `server/workspace.go` — 上传改为原子写（S4）

```diff
-out, err := os.Create(dest)   // 立即截断目标
-io.Copy(out, src)
+return writeFileAtomic(dest, src)   // 临时文件 + fsync + rename
```

**原因**：上游复核第 5 条。`os.Create` 先截断再拷贝，传输中断会在磁盘上留下**被截断的半截文件**，
而原文件已不可恢复——对"工作区放脚本/字典/证据"是数据丢失级问题。
现改为同目录临时文件 + `Sync` + `Rename`，失败时删除临时件。
**同名覆盖语义保持不变**（API 契约未动），只消除崩溃窗口。**不影响任何模型行为**。

> 上游复核第 2 条（HTTP client 超时 / 429 无界读）与第 3 条（CORS 缺 PATCH/Authorization）
> 已在复制基线 `df64cf83` 之前的提交 `b482105` 中修复，本仓库无需再改。

### 3.4 HARNESS 运行模式（新增路径，不改动既有管线）

| 文件 | 改动 | 性质 |
|---|---|---|
| `db/schema.sql` | `tasks.harness_mode`（默认 **false**） | 新列，历史任务行为不变 |
| `db/tasks.go` | taskCols/scan/opts/INSERT/`SetTaskHarnessMode` | 透传 |
| `server/goals.go` | harness 模式**跳过**目标分解与第 0 轮标记、跳过种子意图 | 分支 |
| `server/engine.go` | `Run()` 按 `HarnessMode` 分支：起 1 个会话 goroutine（不是 1+N） | 分支 |
| `server/task_llm.go` | `taskAgentBundle` 增 `harnessRuntime` / `hx` | 增量 |
| `server/manager.go` | `Task.HarnessMode` + 会话收件箱（惰性 channel） | 增量 |
| `server/dto.go` | `TaskDTO.harness_mode` / `llm_ready` | 增量 |
| `server/server.go` | 新增 `/api/harness/*` 与 `/api/tasks/{id}/{say,harness-mode}` 路由 | 增量 |

新增：`agent/harness.go`、`agent/harness_prompt.go`、`server/harness_loop.go`、
`server/harness_wiring.go`、`server/harness_files.go`、`server/harness_browser.go`、
`harness/diff/`、`eval/cmd/harness-smoke/`。

**未改动**：规划者/执行者的既有实现与行为（仅由 `harness_mode` 决定是否启动），
以及全部提示词、内置拦截规则、默认策略。

### 3.5 `third_party/norma/harness/query.go` — max-tokens 恢复不再重复抬额（Sprint 1.3）

```diff
-if l.in.EscalateMaxTokens && l.maxOutputTokensOverride == 0 {
+if l.in.EscalateMaxTokens && !l.maxOutputTokensEscalated {
+    l.maxOutputTokensEscalated = true
```

**原因**：`maxOutputTokensOverride` 会在注入「继续」提示时被清零（`query.go:468`），
而重入 `handleMaxOutputTokens` 时又用「override == 0」判断是否需要抬额。结果是
escalate 与 nudge 交替触发，`EscalateMaxTokens` 名为「一次」实为多次，永远截断的
provider 会被调用 8 次而不是承诺的 5 次。新增独立布尔标志后，抬额在整个轮次链里
只发生一次，恢复次数回到 `1 + 3` 的设计值。**不影响提示词、拦截规则或任何模型可见文本**，
只修正恢复循环的次数。负向对照：`third_party/norma/harness/recovery_contract_test.go`
的 `TestMaxTokensRecoveryIsBounded` 断言「恰好 5 次模型调用」，去掉该修复即失败。

### 3.6 `README.md` / `CHANGELOG.md` / `docs/` 重排

上游 `README.md` 是一份完整的项目说明（安装、更新、架构、免责）；本仓库需要自己的 README
来解释"harness 而非工具集"的定位。故：上游 README 原文保留为 `README-ARTEX.md`，本仓库新增
`README.md`、`DISCLAIMER.md`、`PROVENANCE.md` 与 `docs/`（`docs/` 在上游 `.gitignore` 里被
忽略，本仓库已取消该忽略，因为文档是本项目的交付物）。**纯文档改动，不触碰任何代码路径。**

### 3.7 部署链路修复：脚本与 Dockerfile 不再引用已下线的 Next 前端

**问题**：上游前端下线（提交 `60dfcf2` / `fdaa7e4`）后，控制台改为 `web/`（Vite）产物
`server/console-app/` + 无条件 `go:embed`，`build.sh` 已同步，但 `install.sh`、`update.sh`
与 `Dockerfile` 仍停留在旧链路，导致**按 README 部署会在半路失败**：

| 文件 | 旧行为 | 后果 |
|---|---|---|
| `install.sh` / `update.sh` | `npm run build:static` + `cp web/out server/webui/dist` + `go build -tags embedui` | `build:static` 脚本已不存在，`set -euo pipefail` 下安装/更新直接中止；即使绕过也编不出内嵌 UI |
| `Dockerfile` | `COPY dist/${TARGETARCH}/artex` | `build.sh` 的实际输出是 `dist/artex-linux-<arch>/artex`，镜像构建找不到文件 |

**修复**：脚本改为 `npm ci && npm run build`（产物由 vite 直接写入 `server/console-app/`）+
`go build -trimpath`（无需 build tag）；无 npm 时退回"沿用仓库里已提交的产物"并显式校验
`server/console-app/index.html` 存在。Dockerfile 改为 `COPY dist/artex-linux-${TARGETARCH}/artex`。
`.env.example` 的令牌说明同步改为 `artex-harness` 仓库。

**不影响模型行为**：只改构建/部署路径，不触碰提示词、工具、拦截规则或运行时语义。

### 3.8 控制台补登录门与统一令牌读取（本项目前端，非上游）

- 新增 `web/src/LoginGate.jsx`：首次进入走 `/api/auth/init`（用户名固定 `ARTEX`，≥8 位），
  之后走 `/api/auth/login`；令牌写 `artex_token` localStorage 与同名 cookie（EventSource 用）。
  数据库不可用时如实报错，不谎称"尚未设置密码"（与 `server/auth.go` 的 `503` 契约一致）。
- 所有面板统一经 `web/src/lib/auth.js` 读取令牌（此前 `Sidebar`/`Composer` 只读
  `artex.harness.token`，另一枚键登录的会话在这些面板上全部 401）。
- `Sidebar` 的审批决策改发 `allowed`/`denied`（后端契约），此前发 `allow`/`deny` 稳定 400。
- 任一请求 401 时回到登录门，不再让每个面板各显示一遍"事件流不可用"。

**不影响模型行为**：纯前端与展示层。

### 3.9 其它一致性修正

| 位置 | 修正 |
|---|---|
| `server/harness_loop.go` | 删除一个只剩注释的空 `if` 分支（状态机重构残留，无行为） |
| `docs/03-ARCHITECTURE.md` | 前端选型改为实际实现（React 19 + Vite + Radix，产物 `server/console-app/`）；表清单改为实际的 `harness_revisions` / `harness_turn_items` / `tasks.revision` |
| `docs/stages/S5.md`、`docs/04-ROADMAP.md`、`docs/adr/0002` | 入口改为 `/app/`；`web-console/` 计划未采用，控制台长在 `web/` |
| `docs/adr/0004-harness-runtime-no-planner-worker.md` | 补上被三处引用却缺失的 ADR-0004（内容以 docs/07 为准） |
| `.github/CI.md` | frontend job 描述改为 `npm run build` + 产物一致性校验 |
| `eval/reports/` | 纳入 `.gitignore` 并取消跟踪（门禁运行产物，不该入库） |
| `docker-compose.bench.yml` | 删除（引用不存在的 `bench/.env` 与 `Dockerfile.bench`） |
| `README.md` / `ARCHITECTURE.md` | 控制台行数更正为约 3.8k |

### 3.10 阶段一 S6-P0（2026-11-21，本仓库新增，非上游）

详见 `docs/stages/S6-P0.md`。三处结构性改动，均不改提示词正文与 planner/worker 行为：

| 文件 | 改动 | 性质 |
|---|---|---|
| `guard/intent.go`（新增）、`guard/guard.go` | 意图级确定性分类 + 硬拒绝底座。Critical（`rm -rf /`、`mkfs`、`dd of=/dev/`、`shred`、`DROP/TRUNCATE`）在 DB 规则之前机械拦截，不可从 UI 删除。`hardDeny` 默认 true，`WithHardDeny(false)` 仅测试用。 | 行为变更（有意）：旧测试 `TestPreToolUsePassthrough` 曾断言无拦截器时 `rm -rf /` 放行，现改为硬拒绝并更新测试 |
| `guard/scope.go`（新增）、`server/task_llm.go` | 恢复 RoE 范围强制。有范围行且目标不在范围内时拒绝，提示用 `add_task_scope` 登记；无范围行时 fail open。`DBScopeChecker` 每次读库，不缓存。 | 新增强制点，legacy 任务 fail open |
| `harness/sandbox/sandbox.go`（新增） | 每任务隔离策略最小版：`ValidatePath` 防穿越、`ScrubEnv` 删敏感键、`CheckHost` 默认拒内网/回环 + 白名单。明确不是容器运行时，容器后端留给阶段二。 | 新增包，接口预留 |

验证：Go 1.26.3，`go vet ./guard/... ./harness/sandbox/...` 与 `go vet ./server/...` 通过；`go test ./guard/... ./harness/sandbox/... -count=1` 通过（15 个测试）。未跑 `make test-db`（需 PG）。

### 3.11 Hotfix：与上游 ARTEX 的端口 / 数据库 / 身份隔离（2026-11-21，本仓库新增，非上游）

详见 `docs/stages/S6-HOTFIX-ISOLATION.md`。Julie 提醒解压后与 ARTEX 端口、数据库冲突合并后实施，只改身份与默认值，不改 guard / harness 行为：

| 项 | 改前 | 改后 |
|---|---|---|
| HTTP / 代理端口 | `:8787` / `127.0.0.1:8788` | `:8797` / `127.0.0.1:8798` |
| 数据库 / 用户 | `artex` / `artex` | `artex_harness` / `artex_harness`；本地 PG 宿主端口 `5433:5432` |
| 数据目录 / JWT / 配置 | `./data` / `jwt.key` / `config.json` | `./data-harness` / `harness-jwt.key` / `artex-harness.json`（旧 `config.json` 仅回退，不从 ARTEX 的 `jwt.key` 迁移） |
| 二进制 / Compose | `artex`，镜像 `autumn27/artex`、服务 `artex`、卷 `pgdata` | `artex-harness`，镜像 `artex-harness:latest`（本地构建）、服务 `artex-harness`、卷 `pgdata-harness` |

验证：`go vet ./config/... ./server/... ./guard/... ./harness/sandbox/... ./db/...` 通过；`go test ./config/... ./guard/... ./harness/sandbox/... -count=1` 通过（含新增 `TestPathPrefersHarnessConfig`）；`go build ./cmd/artex` 后 `-h` 显示默认 `:8797` / `127.0.0.1:8798` / `data-harness`；`docker-compose.yml` YAML 解析通过，`bash -n` 检查相关脚本通过。未跑 `make test-db`（需 PG）。迁移注意：旧库 `artex` 不自动搬到 `artex_harness`，旧 `data/` / `jwt.key` 不自动改名，旧会话会失效，需重新登录。

### 3.12 阶段二 S6-P1（2026-11-21，本仓库新增，非上游）

详见 `docs/stages/S6-P1.md`。在上游已有的证据 blob、流量绑定、复测、`evidence_integrity`（报告非门）、基础报告之上，加三层上游没有的东西：

| 文件 | 改动 | 性质 |
|---|---|---|
| `evidence/audit.go`（新增） | 每任务 `audit.jsonl` 哈希链：`hash = SHA256(prev_hash \|\| 规范化条目)`，`Verify()` 重算并检查 seq/prev 链接；篡改、删条、单条伪造均可定位。`AppendToFile` 续写旧链，损坏文件大声失败。 | 新增，防篡改证据（非防篡改证明，见文档局限） |
| `harness/verify/verify.go`（新增） | 独立验证门（纯函数）：标已确认须同时满足流量证据绑定 ≥1、复现步骤非空、最新复测 `reproduced`；人工越过须显式理由并标记。风险评级为严重程度映射，非完整 CVSS。 | 新增强制点 |
| `traffic/replay.go`（新增） | F12 结构化行 + 过滤；`ParseReplaySpec` 脱敏凭据头并生成 curl；`Replay` 30s 超时实发请求并按状态分桶/2xx 同码判定是否复现。 | 新增 |
| `report/pentest.go`（新增） | 专业报告模板：授权范围、执行摘要、风险评级表、按严重程度排序的发现明细（验证状态、复现步骤、证据、修复建议，缺项明示）、附录审计链验证结果。旧 `report.Markdown` 不动。 | 新增 |
| `server/harness_verify.go`（新增）、`server/server.go` | `PATCH findings/{id}` 标 confirmed 时跑门（不通过 422 + 原因；人工越过写审计链）；新接口 `GET /api/harness/audit-chain`、`POST /api/traffic/replay`、`GET /api/report/pentest`。 | 接线 |

验证：`go vet ./server/... ./evidence/... ./harness/verify/... ./traffic/... ./report/...` 通过；`go test ./evidence/... ./harness/verify/... ./report/... ./config/... ./guard/... ./harness/sandbox/...` 通过，traffic 新增测试通过；`go build ./cmd/artex` 通过。未跑 `make test-db`（需 PG），服务端数据库路径未在真库验证。

前端接入（2026-11-21 同日追加，详见 `docs/stages/S6-P1.md`「前端接入」）：控制台此前无发现页与流量页，故四个入口都接在右侧「系统」侧栏（`web/src/System.jsx`），不另开路由。新增 `web/src/Stage2.jsx`（审计链面板、F12 流量表 + 重放、专业报告面板、验证门失败展示 `GateFailure`），`System.jsx` 增加审计链/流量/报告三个签，取证签每条发现下加「标记为已确认」与 422 行内失败原因 + 人工越过（理由必填，写入审计链）；`web/src/styles.css` 仅加三处样式。`npm ci && npm run build` 通过，`server/console-app/` 构建产物已更新；`go vet ./server/...` 与 `go build ./cmd/artex` 通过。未在浏览器里完整点过一遍（需真库 + 真流量），部署后逐项验证。

### 3.13 阶段 S6-TRIM：删除后端不需要的 HTTP 层（2026-11-21，本仓库新增，非上游）

详见 `docs/stages/S6-TRIM-INVENTORY.md`（盘点）与文末“删除执行记录”。后端直接拉取自上游 ARTEX 原版控制台后端，Harness 不需要的部分从后端删除，前端再按删完的后端对齐。本轮只删 HTTP 层，不动底层。

| 改动 | 内容 |
|---|---|
| 删路由 184 条 | DELETE 两组全删：多智能体 exploration 专属 34 条 + 管理面 150 条。`server/server.go` 的 `mux.HandleFunc` 由 293 条减为 109 条，全集 311 条 → 127 条；KEEP 三组（65 条）与 UNCERTAIN 62 条一律未动。脚本逐条核对：184 条注册已全部清除，对应 handler 在 `server/` 下无剩余定义；KEEP/UNCERTAIN 127 条注册全部仍在。 |
| 整文件删除（7 个） | `server/goals_api.go`、`server/triggers.go`、`server/task_assets.go`、`server/task_templates.go`、`server/task_categories.go`、`server/notify_api.go`、`server/sync_scopesentry.go`（文件内所有 handler 都在 DELETE 清单且无别处复用的非 handler 符号）。 |
| 逐函数删除、文件保留 | `server/server.go`（33 个 handler）、`server/server_mgmt.go`（43 个 handler + 若干死符号）、`server/constraints_api.go`、`server/intent_intervention.go`、`server/gitbash.go`、`server/auth.go`、`server/findings_groups.go`、`server/workspace.go`、`server/harness_intervene.go`、`server/asset_intercept.go`、`server/task_intercept.go`、`server/update.go`、`server/customtool.go`、`server/commands.go`、`server/assets.go`、`server/intercept.go`、`server/task_archives.go`。死 helper/死常量一并清理；被 KEEP/UNCERTAIN 代码复用的符号保留（如 `constraintInjectPlanner`、`saveUpload`、`intervenePauseAll` 全局急停、`assetStore()`、拦截引擎 `interceptListPending`/`interceptListRules`/`interceptDecide`、`queueTaskArchive` 工作链、`RestartRequested`/`SetBootUpdateState`）。 |
| 没动的东西 | db 方法、db 表、agent 工具、引擎逻辑、前端一律未动。很多被删 handler 背后的 db 方法仍被 agent 工具或服务端内部使用。 |
| 测试随端点一起删 | 整文件删 `server/update_test.go`、`server/assets_scope_test.go`、`server/harness_intervene_test.go`、`server/prompt_vars_test.go`；另在 7 个测试文件里删掉测被删端点的测试（共 30 个测试函数 + 若干孤儿夹具/helper），测保留功能的测试修好并保留（`server/notify_api_test.go` 保留 5 个推送引擎纯函数防漂移断言）。 |

验证：`go vet ./...` 通过；`go test ./... -count=1` 除 server 包 8 个测试外全部通过——8 个失败（`finding_traffic_test.go` 5 个、`finding_workflow_test.go` 3 个）全部在 `NewManager` 夹具处报“未找到数据库配置”（本机无 `ARTEX_PG_DSN`、无 `server/artex-harness.json`），属 `make test-db` 类、需真库，且测的是 UNCERTAIN 保留功能，与删除无关；`go build -trimpath -o /tmp/artex-harness-trim ./cmd/artex` 通过，`-h` 确认默认 `:8797` / `127.0.0.1:8798`。真库验证见 3.14（2026-11-21 已跑，全绿）。

删除后仍存在的问题：前端未受影响（`web/src/` 内无任何对已删路径的引用，实际调用的方法 + 路径组合全部仍在注册表内），但仍需按指示做“前端按删完的后端对齐”的全面复核；上游文档已于 2026-11-21 同步改写（见 3.14）；`server/update.go` 的 `bootUpdateState` 现已无调用方，为保 `cmd/artex/main.go` 不动而保留。

### 3.14 真库验证 make test-db（2026-11-21，本仓库新增，非上游）

详见 `docs/stages/S6-VERIFY-DB.md`。本机装 PostgreSQL 16.15（apt），起专用测试库 `artex_harness_test`（与开发库 `artex_harness` 隔离，`ARTEX_TEST_DB` 与 DSN 库名一致才放行破坏性测试）。`go test ./... -count=1 -timeout 25m` 30 个包全绿。

| 改动 | 内容 |
|---|---|
| 删后端后遗留的过期测试（11 个） | 第一轮 8 个（`core_test.go` 的 `/api/stats`、`finding_workflow_test.go` 的 PUT `/api/settings` ×3、`gitbash_test.go` 的 settings 面板、`inheritance_api_test.go` 的 activity 详情、`intercept_detail/filter_test.go`、`mgmt_test.go` 的 `/api/agents`）与完整跑才现形的 3 个（`task_categories_test.go`、`task_control_routes_test.go` 的已删路由、`task_archives_test.go` 的已删列表/批量口）——删文件 5 个（`intercept_detail/filter_test.go`、`mgmt_test.go`、`task_categories_test.go`、`task_control_routes_test.go`），其余改写到还存在的层（settings 开关改走 `pg.SetBool`，Git Bash 改走库 + `applyGitBash`，继承关系只保留删 source 后的关系清理断言，归档只保留单任务入队断言）。生产代码未动。 |
| 上游既有的测试隔离 bug（1 个） | `llmrec/TestSideUsageRecordedOnceOnConsumerCancellation` 假定 `llm_usage` 表已存在，本包又没有 TestMain 建表，单独跑或 schema 被重置后就 `42P01`；补 `pg.EnsureLLMUsageTable()`。 |
| 阶段二 HTTP 表面补回归测试 | 新增 `server/stage2_http_test.go`（3 个）：确认 422（无证据）→ 人工越过 200 → 审计链 valid；审计链 + 专业报告渲染；流量重放 200 复现。 |
| 未修（定性为抖动） | `agent/TestBlackboardToolsReadDirectSources` 全量并行跑时偶发 PG 死锁（40P01），单独 `-count=3` 通过；`server/TestBackgroundDelegationNotifierDeliversAndDrains` 全量跑时偶发失败，单独通过。 |

注意：apt 装的 PostgreSQL 不随虚拟机重启保留（本次验证中途虚拟机重启后需重装，运行时的 os-intent replay 未自动恢复它）；测试库与开发库的隔离只靠库名区分，重装后需重建 `harness_test` 用户与 `artex_harness_test` 库。浏览器里点一遍阶段二四个面板仍未做（需部署 + 真流量）。

### 3.15 第二轮后端瘦身：UNCERTAIN 62 条定去留，删 22 条、留 40 条（2026-11-21，本仓库新增，非上游）

Julie 2026-11-21 14:17 CST 确认。标准与第一轮相同：只动 HTTP 层（路由注册 + handler），不动 db 表、agent 工具、引擎逻辑、不改前端；按体验/稳定性，删的是死功能零调用，留的是体验/安全必需。详见 `docs/stages/S6-TRIM-INVENTORY.md` 末尾“第二轮删除执行记录”。

删了 22 条（U5 Eval/一致性 2 条：POST `/api/harness/consistency`、GET `/api/harness/scenarios`；U6 委派读口 2 条：GET `/api/harness/delegations/{id}`、`/{did}`，POST `.../stop` 保留；U8 旧版报告与审计 2 条：GET `/api/report`、GET `/api/audit`，新版 pentest 与 audit-chain 保留；U11 旧版对话面 5 条：POST `/api/chat`、POST `/api/chat/upload`、GET `/api/chat/mentions`、GET `/api/tasks/{id}/chat/status`、POST `/api/tasks/{id}/chat/stop`；U14 边问边答整组 11 条：三组 side-questions 各 GET/POST/DELETE + GET `/api/side-questions/{requestID}/events` + POST `.../cancel`）。后端 127 条 → **105 条**（15 个分组）。

只删 HTTP 层，复用符号一律保留：边问边答的引擎（`initSideQuestions`、`drainTaskSideQuestions`、`cancelSideWhere`、`cancelWorkerSide`、快照流水线）仍在——`agent/side_questions.go` 的运行时检查点发布与任务删除/归档时的 drain 仍依赖它；`cancelTaskChat`/`finishTaskChat`/`fallbackChat`、`chatAttachment`/`composeAgentMessage`、`harnessDelegationStop` 与 retention/GC 亦保留。死符号只删确认死的（`errNotFound`/`notFoundError`、`maxChatUpload`、`safeChatID`、`uniqueUploadPath`、`runSide` 等）。

测试随端点一起处理：删 `side_questions_test.go` 的 4 个 HTTP 测试并改写 1 个检查点测试（改走 flush 落库，保留 3 个引擎测试）；删 `chat_mentions_test.go` 的 2 个 handler 测试（db 层断言保留）；删 `task_delete_barrier_test.go` 的 2 个 handler 测试；修掉随之无用的 import。整文件删除：无。

验证：`go vet ./...` 通过；`go build -o /tmp/artex-harness-trim2 ./cmd/artex` 通过，`-h` 确认默认 `:8797` / `127.0.0.1:8798`；路由重数 98（server.go）+ 7（finding_traffic helper）= 105 条，side-questions/audit/chat 三组消失，report 组剩 pentest 1 条；`go test ./... -count=1` 无库时除 server 包 11 个需真库测试（finding_traffic 5、finding_workflow 3、stage2_http 3）外全绿；真库（`artex_harness_test`）全量跑 server 包全绿，另有 2 个并行抖动（agent `TestBlackboardToolsReadDirectSources` 与 db `TestUpsertOtherService`，均为 PG 死锁 40P01，单独 `-count=3` 通过，与删除无关——db/ 与 agent/ 本轮未动）；前端 `web/src/` 对 22 条已删路径 0 引用。README 路由总表与 `docs/16` 架构图同步改为 105 条/15 组（委派审计入口改为 `GET /api/harness/workers/{id}`）。

### 3.16 发现列表页（2026-11-21，本仓库新增，非上游）

Julie 2026-11-21 14:33 CST 确认发现列表页与拒答兜底两件一起做。控制台此前没有发现列表页，只能在右侧「系统」侧栏的「取证」签逐条确认。本轮新增 `web/src/Findings.jsx`：全局发现列表（严重程度 / 状态 / 任务 / 类型 / 关键词筛选、可点统计卡片、服务端分页每页 20 条、按严重程度或时间排序）+ 详情（报告 Markdown、证据、资产、流量绑定、血缘链、复测记录）。详情里接了标记为已确认（复用 `Stage2.jsx` 的 `patchFindingConfirm` + `GateFailure`，422 行内失败原因 + 人工越过理由写入审计链，未另写一套确认逻辑）、发起复测（POST retests，打开详情时与发起后各查一次复测列表与进行中列表，不做轮询）、导出（单条 scope=selected、列表 scope=filtered，格式 md-single/csv/json/md-zip）、删除（Dialog 二次确认）。

入口按现有视图模式接进 `App.jsx`：新 view `"findings"`，顶栏分段加「发现」签（与「用量」一样是全局视图，不要求先选任务；从任务打开时任务筛选默认取当前任务），命令面板（⌘K）「操作」组加「发现列表」；详情里的任务名可点回任务对话视图。样式沿用 `styles.css` 与 `ui.jsx` 现有规范（`.cap-row`、`.traffic-select`、`.report-md`、`.replay-md`），只补了 `.findings-row` / `.findings-stat` 两处；无新依赖；后端未改。

接的接口（全部是第二轮瘦身后保留的）：`GET /api/exploration/findings`（分页 + 筛选）、`GET /api/exploration/findings/stats`（统计卡片与任务/类型下拉）、`GET /api/exploration/findings/{id}`、`GET /api/exploration/findings/{id}/lineage`、`GET /api/exploration/findings/export`、`DELETE /api/exploration/findings/{id}`、`PATCH /api/exploration/findings/{id}`、`POST /api/exploration/findings/{id}/retests`、`GET /api/exploration/findings/{id}/retests`、`GET /api/exploration/findings/retests/active`。`GET /api/exploration/findings/groups` 未接——按任务分组视图暂不做，任务筛选改用 stats 自带的 tasks 下拉。详见 `docs/stages/S6-FINDINGS.md`。

验证：`npm ci --no-audit --no-fund && npm run build` 通过，产物已更新 `server/console-app/`；`go vet ./...` 通过；`go build` 通过，`-h` 默认仍 `:8797`；`go test ./... -count=1` 无库时除 server 包 11 个需真库测试（finding_traffic 5、finding_workflow 3、stage2_http 3）外全绿，与上一轮一致。未在浏览器里点过，需部署后实测。

### 3.17 拒答兜底 S6-REFUSAL（2026-11-21，本仓库新增，非上游）

Julie 确认“发现列表页 + 拒答兜底两件一起处理”，本文是拒答兜底一件。详见 `docs/stages/S6-REFUSAL.md`。只动后端与评测，不改前端；不改系统提示词本身、不动 guard 硬拒绝逻辑、不动 `third_party/`。

| 改动 | 内容 |
|---|---|
| 拒答检测（新） | `llmpool/refusal.go`：`Detect(msg llm.Message) Result`（是否拒答 + 命中信号 + 200 字内原文摘录）。只判无工具调用的纯文本回复；强信号才算（英文 `I can't/cannot help/assist`、`unable to help/assist/comply`、`not allowed to`、`must/have to refuse`，中文 `我无法协助/我不能帮助（协助）/我无法帮助/我不能帮你/抱歉我不能/我无法（不能）提供帮助/不被允许/我不能（无法）执行`），裸 `cannot/无法`、裸 `作为 AI`、裸 `sorry` 不算；大小写不敏感，空白折叠，中文去空格匹配。阈值宁漏勿误判。 |
| 换模型重派 + 一次性授权重提示（`Pool.Complete` 内） | 首次拒答先对同一 member 追加授权提醒（`refusalReminder`，呼应系统提示词“任务目标与描述是授权的全部范围”，追加到复制的 `req.System` 末尾）再试一次；仍拒答才换模型，最多换两次（最多试 3 个配置）；重提示不计换模型次数，但每请求至多一次，总调用有界。用尽后把最后一次拒答原文如实返回（`llm.Message`，nil error），不伪造工具调用/发现，不包 `ErrExhausted`。拒答既不 `Trip` 也不 `Pass` 健康分，只记 `[llmpool-refusal]` 结构化日志。`Pool.Stream` 不做重派（流出事件无法撤回），仅在整条流干净结束且无工具调用、文本命中时记一行拒答日志，拒答的流也不记 `Pass`。 |
| 拒答率评测（新） | `harness/eval/refusal.go`：`MeasureRefusals(ctx, prompts, []ProbeModel{Name, Provider})`，逐模型跑一次、用同一检测判定，输出各模型拒答率 + 命中信号分布（`RefusalReport.String()` 可打印），错误单独计数、不算拒答；测试用 fake provider，不调真实模型。 |
| 子代理拒答标记 | `harness/delegation/delegation.go`：`Record` 加 `Refused bool`（`json:"refused,omitempty"`，journal 自动持久化）+ `FinishWithRefused`；`agent/harness.go` 的 `finishDelegation` 用同一检测判断正文，命中则标记、activity Metadata 写 `refused:true`、Summary 写“子代理拒答”、记 `[delegation]` 日志，`Status` 不变。父级循环无现成重试/升级点可接，由父模型看到 tool_result 后自行决定。 |
| 测试 | 检测正反样例（中英文命中；“目标不可达/连接失败/工具报错/作为 AI，我可以帮你…”不命中；带工具调用的回复不命中）、重派上限（4 个全拒答 fake：调用 `[2 1 1 0]`，第 4 个未碰）、重提示只追加一次（同一 member 调两次、第二次 System 带提醒；第二次拒答的 member 不再重提示）、子代理标记（拒答正文 → `Refused=true` 且 Status 不变、activity 含 `refused:true`；普通失败/成功不标记）、journal 恢复后 `Refused` 仍在。 |

验证：`go vet ./...`、`go build ./...` 通过；无库 `go test ./... -count=1` 除已知需真库的 server 11 个测试外全绿（finding_traffic 5、finding_workflow 3、stage2_http 3，均为“未找到数据库配置”）；真库 `go test ./... -count=1 -timeout 25m`（`artex_harness_test`，日志 `~/workspace/.tmp/testdb-refusal-db.log`）除已知并行抖动 `agent/TestBlackboardToolsReadDirectSources`（PG 死锁 40P01）外全绿，单独 `-count=3` 通过，未修。

### 3.18 真实任务 #1 的两个高危修复：流量代理 NO_PROXY + 沙箱接入（2026-11-21，本仓库新增，非上游）

Julie 2026-11-21 16:01 CST 确认只修这两件。详见 `docs/stages/S6-FIX-PROXY-SANDBOX.md`。问题来自真实完整渗透测试任务 #1（报告 `~/workspace/real-pentest-task1-report-20261121.md`，不在仓库内）。

| 改动 | 内容 |
|---|---|
| 流量代理强制清空 NO_PROXY（`agent/worker.go` 的 `proxyEnv`） | 捕获开启时（`caCert != ""`，即 `Manager.ProxyCACert` 非空）追加 `NO_PROXY=` 与 `no_proxy=` 空值；捕获关闭时不动用户原有的 `NO_PROXY`。根因：本机 `no_proxy` 含 `127.0.0.1`，curl / Python 认它，回环请求直连靶场，任务 #1 的 6,523 次请求 0 条录制。调用方只有三处，全走同一函数（Worker、Planner、`runopts.go` 的共享默认值）。WebFetch 不受影响（`http.ProxyURL` 固定代理，不读 `NO_PROXY`）；浏览器 MCP 也不受影响（Chromium 不读 `NO_PROXY` 环境变量，只挂 `--proxy-server`，未设 `--proxy-bypass-list`；其回环默认行为未验证，见阶段文档遗留）。 |
| 沙箱策略接进 Guard（`guard/guard.go`） | `Guard` 加 `sandbox` 字段 + `WithSandbox` + `SetSandbox`（与 `SetScopeChecker` 同款）。`preToolUse` 在意图硬拒绝之后、RoE 范围检查之前跑沙箱检查：文件工具（Read / Write / Edit / MultiEdit 取 `file_path`；LS / Glob / Grep 取 `path`）由 `ValidatePath` 收容进任务目录；网络工具（WebFetch 取 `url`、Bash 用 `ExtractHosts`）只管回环 / 内网字面量，公网主机留给范围检查，`web_search` 不查（查询文本不是连接目标）。回环 / 内网主机先查任务 RoE 范围（`CheckHostAgainstScopes`，实时读库）：在范围内放行（任务 #1 的范围行 `ip 127.0.0.1/32`），范围行存在但不在内则 `CheckHost` 拦；未锚定范围的任务 fail open（与 `CheckScope` 的 legacy 规则一致），文件收容始终执行。拦截返回带 `【ARTEX 平台管控·非目标防御】` 前缀的明确错误。 |
| 策略对象补强（`harness/sandbox/sandbox.go`） | `ValidatePath` 增加符号链接解析（已存在路径或最近已存在祖先做 `EvalSymlinks` 再判收容，工作区内指区外的链接与经链接目录的路径都被拦）；新增 `Policy.DenyPrefixes`（默认 `/etc`、`/proc`、`/sys`、`/dev`、`/root`、`$HOME/.ssh`），宿主敏感位置即使落在 `AllowedRoot` 内也被明确拒绝；不做文件名后缀禁区（`.pem` / `.key` 会误伤 agent 把目标上拿到的私钥存进工作区再读回的正常流程）；导出 `IsSensitiveEnvKey` 与 `EnvScrubOverrides()`（扫描进程环境，对敏感键返回 `KEY=` 空值覆盖条）。 |
| Bash 环境清洗（`agent/worker.go` 新增 `bashEnv`） | `proxyEnv` + `EnvScrubOverrides()`，Worker / Planner / `runopts.go` 三处 `BashEnv` 调用点全部改走它。SDK 的 `withEnv` 是 `append(os.Environ(), tc.Env...)`，Go exec 保留最后一个重复键，空值覆盖压掉继承来的 LLM 密钥 / token / `ARTEX_PG_DSN`。无代理时也照常清洗。 |
| 服务端接线（`server/task_llm.go` 的 `agentsForTask`） | 只对 harness 模式任务给任务的 Guard 装沙箱，根目录 `<数据目录>/tasks/<taskID>`（与 `HarnessAgent.WorkDirFor` / `ensureRunDir` 同布局）；harness agent 与其子代理委派经同一 Guard hooks 受控。探索模式任务保持旧行为。沙箱初始化失败记日志、不启用沙箱、不阻断建任务。 |

未动：意图级硬拒绝与 RoE 范围检查本身；`third_party/norma`；报告措辞、任务计数、审计链、预算默认值（另几项，Julie 没选）。

测试：`proxyEnv` 捕获开启时 `NO_PROXY` / `no_proxy` 为空串、关闭时不动；`bashEnv` 覆盖敏感变量、不碰良性变量；`ValidatePath` 符号链接逃逸被拒、区内链接放行、`~/.ssh` 与 `/proc` 被拒；Guard 层 Read 越界 / 靶场源码 / 经链接读取被拦、区内读写放行、RoE 授权的 `127.0.0.1:18080` 上 WebFetch 与 Bash curl 放行、`192.168.1.10` / `10.0.0.5` 被拦、公网主机过底线、未锚定任务网络 fail open 但文件收容仍生效、未装沙箱的 Guard 不拦截。端到端（`agent/proxyenv_e2e_test.go`）：真实 `traffic.Open` 录制代理 + `httptest` 回环目标，对照组（只设 `no_proxy`、不走 `proxyEnv`）录到 0 条，实验组（同一 `no_proxy` + `proxyEnv` 的 env）curl 200 且代理录到 1 条。

验证：`go vet ./...`、`go build ./...` 通过；无库 `go test ./... -count=1`（日志 `~/workspace/.tmp/testdb-fix-nodb.log`）除已知需真库的 server 11 个测试外全绿（28 个包 `ok`，finding_traffic 5、finding_workflow 3、stage2_http 3，均为“未找到数据库配置”）。真库 `make test-db`（`artex_harness_test`，日志 `~/workspace/.tmp/testdb-fix-db.log`）：`db` 与 `server` 两包全绿，exit 0，本次无抖动失败。

### 3.19 真实任务 #1 后的小问题清单修复（2026-11-21，本仓库新增，非上游）

Julie 2026-11-21 16:43 CST 确认修这批小问题。详见 `docs/stages/S6-FIX-PUNCHLIST.md`。问题来自真实完整渗透测试任务 #1（报告 `~/workspace/real-pentest-task1-report-20261121.md`，不在仓库内）与同日的浏览器核查（截图 `~/workspace/.hatch-browser/outputs/browser-task:13f577bb-5dba-5e7f-a528-1c8fc27b509b/`，不在仓库内）。

| 改动 | 内容 |
|---|---|
| 任务摘要计数恒 0（`db/findings.go` 新增 `FindingCountsForTask`；`server/server.go` 的 `getTask`；`web/src/Sidebar.jsx`） | 根因：`getTask` 从不填充 `dto.Findings`（聚合查询本身是对的）；列表接口走批量聚合，单任务接口此前完全没填该字段。新增 `FindingCountsForTask(taskID)`（口径与 `TaskListMetricsAll` 一致：severity 白名单外 / 为空的记录不计入任一档）；`getTask` 用它填充，失败时保持 0 不阻塞详情；Sidebar 任务行加发现数徽章（>0 才显示，严重 >0 时红色）。 |
| 报告措辞（`report/pentest.go`；`server/harness_verify.go` 的 `pentestReport`） | `PentestFinding` 加 `Status` 字段；新增 `ExtractRemediation(md)`（找含“修复建议” / “修复方案” / Remediation 的标题，截到下一同级或更高级别标题为止，找不到返回 `""`）；执行摘要改按状态计数——“发现共 N 个：已确认 C 个、待确认 P 个、已排除 E 个，其中独立复测验证 V 个，人工越过验证门 O 个”（confirmed / resolved / fixed 计已确认；false_positive / ignored / duplicate / risk_accepted 计已排除；pending / in_progress / 空计待确认）；摘要行尾改 `\n\n` 使风险表前有空行（之前表头被当成段落续行、渲染成带竖线的纯文本）。`pentestReport` 传入 `Status: f.Status` 与 `Remediation: report.ExtractRemediation(f.Report)`；修复建议取自发现详细报告的 `findings.report` 字段，无建议时才写“未提供”。`report/pentest_test.go` 的旧措辞断言已同步更新。 |
| 发现登记进审计链（`evidence/store.go`；`server/finding_traffic.go` 的 `evidenceStore()`） | `Store` 加 `OnFindingRecorded` 回调，`Store.Record` 落库成功后才调用；`evidenceStore()` 挂回调，调 `auditAppend(taskID, evidence.AuditFindingReported, actor, …)`（actor 取 in.Worker、空则 "harness"）。状态变化（确认 / 越过）此前已在链上，无需再接。 |
| 详情绑定文案与血缘标签（`server/server.go` 的 `getFinding` / `findingLineage`；`web/src/Findings.jsx`） | `getFinding` 在 `findingFromDB` 后调 `GetFindingTraffic` 填充 `dto.TrafficBindings`（出错只记日志）；`findingLineage` 把与 `f.NodeID` 匹配的节点 DTO 的 `State` 改写为发现的实际 `f.Status`（不改库里存的探索图状态）；详情流量绑定一节删掉 `f.traffic_count` 那句，全部以 `bindings.length` 为准，无绑定时只显示一句“尚未绑定流量证据——绑定入口在 System 侧栏的流量面板，绑定后这里会列出来。”核对结论：绑定口径本身没错——真实库里发现 1 的唯一绑定就是合法的 baseline GET /（note 明确说明 PoC 报文未被记录器落盘），矛盾纯因详情 DTO 不返回绑定列表，所以只修了文案 / DTO，没改绑定逻辑。 |
| 初载闪现与窄栏报告（`web/src/Findings.jsx`、`Stage2.jsx`、`styles.css`） | 列表徽章未加载时显示 "…"；`TrafficPanel` 未加载时显示“读取中…”（区分“加载中”与“已加载为空”，加载中不渲染空态）；`.report-md` 加窄栏样式（h1 16px、h2 14px、h3 13px、可断行、表格 `width:100%; table-layout:fixed`、pre 横向滚动）。 |
| 墙钟预算建任务可传参（`db/schema.sql`、`db/tasks.go`、`agent/harness.go`、`server/manager.go`、`server/dto.go`、`server/server.go`、`server/task_llm.go`、`server/harness_files.go`） | `tasks` 表加 `run_budget_minutes INTEGER NOT NULL DEFAULT 0`（含 ALTER 补旧库）；`db.Task`、`taskCols`、`scanTask`、`TaskCreateOptions`、`CreateTaskWithOptions` 贯穿；新增 `ValidateRunBudgetMinutes`（合法范围 5–480 分钟）；`server/manager.go` 的 `Task` 与 `taskFromPG`、`server/dto.go` 的 `TaskDTO`（`omitempty`）、`server/server.go` 的 `createTaskReq.run_budget_minutes`（`*int`）贯穿；`agent/harness.go` 的 `HarnessAgent` 加 `runBudget` 字段 + `SetRunBudget` / `RunBudget()`，`Run` 里用 `h.RunBudget()`；`server/task_llm.go` 的 `agentsForTask` 在 `t.RunBudgetMinutes > 0` 时调用 `SetRunBudget`；`server/harness_files.go` 的 `harnessBudget` 在任务上下文中优先报任务级预算。默认值不改：不传用全局 `HARNESS_RUN_BUDGET` / 20 分钟。口径：显式传 0 报 400（防误配），只有缺省才用默认；db 层 0 仍表示“未设置”。 |
| 前端产物（`server/console-app/`） | `npm run build` 通过，新 bundle `index-BQKvlJP5.js` / `index-DSxWSa7u.css`，旧文件已删。 |

未动：3.18 批（proxyEnv / 沙箱）的文件；proxyEnv 与沙箱的语义；现网服务端、靶场、隧道与任务 #1 的 6 条发现；未 commit、未 push、未跑 LLM 真实任务。

测试：`go vet ./...`、`go build ./...` 通过。新增回归测试：`report/pentest_remediation_test.go`（`ExtractRemediation`、按状态计数）、`agent/harness_budget_test.go`（预算覆盖 / 清零回默认）、`db/run_budget_test.go`（5–480 边界）、`server/punchlist_test.go`（5 个：摘要计数、登记进审计链、详情绑定 + 血缘 pending、报告措辞、建任务预算传参 / 不传参 / 非法值 400）。`report` / `agent` / `db` 三包通过；5 个 punchlist 测试在真库（`artex_harness_test`）下 5/5 通过。期间修了一个测试本身的 bug（复用 map 导致上一轮的 `run_budget_minutes: 60` 残留），产品代码无问题。

验证：无库 `go test ./... -count=1`（日志 `~/workspace/.tmp/test-punchlist-nodb.log`）exit 1，失败 16 个——11 个是已知的需真库 server 测试（finding_traffic 5、finding_workflow 3、stage2_http 3），另 5 个就是新增的 punchlist 测试（它们用 `trafficEvidenceServer` 夹具，本来就需要 PG，在真库下已全绿）；其余 29 个包全绿。**无库基线从此是 16 个失败，不是 11 个。**`make test-db` 全量与测试二进制端到端实测按 Julie 2026-11-21 16:58 CST 决定跳过、未做。

### 3.20 自称 artex-harness + 启动自动打开控制台（2026-11-21，本仓库新增，非上游）

Julie 2026-11-21 17:07 CST 确认两件一起做。起因：构建产物检查发现可执行文件已是 `artex-harness`（`Makefile` 的 `BIN`、`build.sh` 产出、`start.sh` 的 `BIN=./artex-harness`），默认端口已是 `:8797` / `127.0.0.1:8798`（Go 代码里无 8787/8788），但程序自己在日志、接口与控制台里仍自称 ARTEX，且启动后不自动打开 web UI。详见 `docs/stages/S6-IDENTITY-BROWSER.md`。

| 改动 | 内容 |
|---|---|
| 自称改为 artex-harness（`cmd/artex/main.go`、`server/server.go`） | 启动横幅的 ASCII ARTEX 图改为纯文本 `artex-harness`；监听日志改为 `artex-harness %s backend listening on %s`；包注释与 `server.go` 的包注释同步改写。`GET /api/health` 的 `service` 字段由 `"artex"` 改为 `"artex-harness"`。 |
| 控制台品牌字（`web/src/Sidebar.jsx`、`SettingsView.jsx`、`ui.jsx`） | 侧栏品牌行 `ARTEX` 改为 `artex-harness`；设置页环境区服务名的兜底值同步改为 `"artex-harness"`；`ui.jsx` 的 logo 注释同步改写。`npm run build` 后 console-app 产物已更新（新 bundle `index-BwDk6e9j.js`，旧 `index-BQKvlJP5.js` 已删）。 |
| 启动自动打开控制台（`cmd/artex/browser.go`，新增；`main.go` 接线） | 新增 `-open-browser` flag（默认 true，`-open-browser=false` 关闭）。监听启动后在后台等 `/api/health` 有响应（每 200ms 轮询，最多 5 秒），再按操作系统打开控制台 URL（Linux `xdg-open`、macOS `open`、Windows `rundll32 url.dll,FileProtocolHandler`）。URL 由 `-addr` 导出（`:8797` / `0.0.0.0:8797` / `[::]:8797` 一律变 `http://127.0.0.1:8797/`，真实主机名保留，IPv6 回环包方括号）。无图形会话时静默跳过（Linux 需 `DISPLAY` 或 `WAYLAND_DISPLAY`，macOS / Windows 直接尝试）；打开失败只记一条 `[browser]` 日志并提示手动访问，永不影响启动。 |

未动：Go module 路径（仍 `github.com/Autumn-27/artex`，见 §4）；管理员用户名仍固定 `ARTEX`（功能标识，改它会断登录）；默认端口 `:8797` / `127.0.0.1:8798` 与数据目录 `data-harness` 不变；未 commit、未 push、未碰现网服务端与任务 #1 的数据。

测试：新增 `cmd/artex/browser_test.go`（`consoleURL` 8 个用例、`canOpenBrowser` 的 DISPLAY/WAYLAND_DISPLAY 组合、`openBrowserCmd` 按操作系统选命令）；`go vet ./...`、`go build ./...`、`go test ./cmd/... -count=1` 通过；`npm run build` 通过。

验证：隔离实测（测试库 `artex_harness_test`、端口 18997/18998、scratch 数据目录，未碰现网服务端）：有 DISPLAY 时假 `xdg-open` 被调到 `http://127.0.0.1:18997/`，日志 `[browser] 已在浏览器中打开控制台 http://127.0.0.1:18997/`；无 DISPLAY 时服务正常、静默跳过；`-open-browser=false` 时有 DISPLAY 也不打开。三次实测的横幅均为 `artex-harness`，监听日志为 `artex-harness dev backend listening`，`/api/health` 返回 `{"ok":true,"service":"artex-harness","version":"dev"}`。未跑 `make test-db` 全量（本轮只动启动路径与两处字符串，server 包的改动是一行常量）。

### 3.21 恢复管理面路由 147 条 + 前端接线（2026-11-21，本仓库新增，非上游）

Julie 2026-11-21 17:25 CST 确认第一批与第二批一起恢复：把瘦身第一轮删掉的管理面 150 条路由加回来，扣除已被 profiles 体系取代的旧版 LLM 全局配置 3 条（GET/POST `/api/llm`、POST `/api/llm/test`），实恢复 **147 条**。详见 `docs/stages/S6-RESTORE-MGMT.md`。

方法：被删代码从 git 历史取回——**取回来源是 d46c53b（瘦身前的父提交），不是 b4dccfe**：b4dccfe 已是瘦身第一轮之后的状态（127 条路由），其 server.go 只有 109 条注册；d46c53b 的 server.go 有 290 条注册，7 个整文件删除的文件也都在 d46c53b 存在、b4dccfe 缺失。恢复前逐条核对：147 条全部在 d46c53b 存在、HEAD 缺失；d46c53b→HEAD 的 server.go 删除总数 195 条 = 147（本次恢复）+ 34（exploration）+ 3（旧版 LLM）+ 11（第二轮），账对得上。工作由 7 个工人按文件并行做（server_mgmt / 整文件四组 / 通知+同步 / 拦截三组 / 资产+归档+工作区+自定义工具 / server.go+更新+认证+命令+git-bash / 前端），协调员中央核对、验证、写文档。只动 HTTP 层（路由 + handler），不动 db 表、agent 工具、引擎逻辑。

| 改动 | 内容 |
|---|---|
| Agents 配置管理 + triggers（21 条，`server/server_mgmt.go`、`server/triggers.go`） | agent 增删改、提示词/版本/预览/重置、wrapup 与任务超时 wrapup、变量、可见性、触发器 CRUD；另恢复 helper `agentByKey`、`cleanStrs`、`rewriteSkillFrontmatter`、`skillNameFromFrontmatter`、`readZipEntry`、`globalPromptVars`、`withGlobalVars`、`validateTemplate`、`renderPrompt`、`templateFields` 与 import。`server_mgmt.go` 恢复后与 d46c53b 逐字节相同（HEAD 保留代码与 d46 保留代码一致，无后续修复可供回滚）。 |
| 技能管理（10 条，`server/server_mgmt.go`） | 技能上传/删除/文件读写/元数据/用量/缺失检查；列表与新建此前已接，本次补文件级管理。恢复测试 `skill_upload_test.go`（5 个上传测试 + `TestSkillNameFromFrontmatterQuoted`）与 `prompt_vars_test.go`（2 个 `withGlobalVars` 测试）。 |
| 通知渠道（8 条，`server/notify_api.go` 整文件取回） | 渠道增删改测、投递记录、投递重试、meta；推送引擎 `server/notifier.go` 本就未删。恢复测试 `notify_api_test.go` 的 14 个接口测试（HEAD 保留的 5 个引擎纯函数测试原样保留）。测试消息文案顺手改为 artex-harness（3.20 身份一致）。 |
| 任务归档（7 条，`server/task_archives.go`） | 归档列表/单条/恢复/批量删除/批量恢复/批量归档；`queueTaskArchive` 链本就保留。 |
| 工作区文件操作（7 条，`server/workspace.go`） | list/read/write/mkdir/delete/download/upload；`saveUpload` 本就保留。 |
| 自定义工具（7 条，`server/customtool.go`） | tools 列表/修改/自定义工具 CRUD/测试/重置；引擎加载逻辑本就保留。 |
| 任务分类（6 条，`server/task_categories.go` 整文件取回） | 分类的增删改 + 批量改分类 + 单任务改分类。 |
| 公司（6 条，`server/assets.go`） | 公司的增删改查 + 范围 + 重归属；另恢复 `companyStore()`、`companyScopeInputs`、`decodeCompanyMutationRequest()`。恢复测试 `assets_scope_test.go`（7 个）。 |
| 设置（6 条，`server/server.go`、`server/gitbash.go`） | GET/PUT `/api/settings`（`getSettings`/`putSettings` 函数体在 HEAD 只剩孤儿注释，已从 d46 恢复；键以 HEAD `settingsPayload` 为准，含 notify/noa/constraints 等新增键）、git-bash 浏览/检测、python 检测、web 搜索测试。恢复测试 `gitbash_test.go` 的 3 个测试（另补漏掉的 `doFunc` 类型声明）。 |
| 拦截 judge + 历史（6 条，`server/intercept.go`） | judge 配置/用量 + 拦截历史/详情/执行记录；恢复 `interceptRuleReq`、`validateInterceptRuleReq`。 |
| 任务级拦截规则（5 条，`server/task_intercept.go`）、资产拦截规则（5 条，`server/asset_intercept.go`） | 两组规则的增删改查 + 开关；`taskInterceptRuleReq`/`buildTaskInterceptRules` 与 `assetInterceptRuleReq` 本就保留。 |
| ScopeSentry 同步（5 条，`server/sync_scopesentry.go` 整文件取回） | 同步状态/数据源/项目/任务/触发同步；唯一适配：`syncSSRun` 调的 `s.companyStore()` 在 HEAD 已不存在，改为直接用 db 层 `pg.Companies()`（与 `chat_mentions.go`、`manager.go` 同款用法）。 |
| 日志 + 统计（4 条，`server/server.go`） | `GET /api/logs`、`/api/logs/history`、`/api/logs/stream`、`/api/stats`。 |
| 资产（4 条，`server/assets.go`） | 资产列表/计数/新增/删除。 |
| LLM 池与重试策略（4 条，`server/server_mgmt.go`） | 模型池状态/重置 + 重试策略读/写；策略本身由服务端启动时从 db 读。 |
| LLM 记录明细（3 条，`server/commands.go`） | 记录的按任务/按模型/单条；恢复 `pgDeleteLLMRecords` 等 7 个 handler（含命令统计 2 条）。 |
| MCP 单个管理（3 条，`server/server_mgmt.go`） | MCP 服务的删除/工具列表/刷新；另恢复 `POST /api/mcp`（新建，属“其它”组）。 |
| 流量删除（3 条，`server/server.go`） | 流量全删/按 host 删/全清。 |
| 任务模板（4 条，`server/task_templates.go` 整文件取回） | 模板的增删改查；恢复测试 `TestTaskTemplateHTTPCRUD`。 |
| 可见性（4 条，`server/server_mgmt.go`） | 资源/技能的可见性查询与开关。 |
| 拦截规则写口（4 条，`server/intercept.go`） | 全局规则的新建/修改/删除/开关；SettingsView 的规则区此前只读。 |
| Token 统计（2 条，`server/commands.go`） | 按会话 / 全局的 token 用量统计（`conversationTokens` 在 `server/server.go`、`pgUsageStats` 在 `commands.go`）。另：`tokenStats` handler（`GET /api/exploration/tokens`）为让恢复的测试 `TestTokenStatsWithoutTaskReturnsStableShape` 能编译而一并恢复，但**未注册路由**——该路由属 exploration 34 条，不在本次范围；测试本身无库通过。 |
| 命令统计（2 条，`server/commands.go`） | 工具调用列表与按工具聚合统计。 |
| 其它（7 条） | 改密码（`POST /api/auth/change-password`，`server/auth.go`）、GC（`POST /api/gc`，`server/server.go`）、任务挂/摘资产 2 条（`server/task_assets.go` 整文件取回）、删除 LLM 记录 1 条（`commands.go`）、新建 MCP 1 条（`server_mgmt.go`）、新建技能 1 条（`server_mgmt.go`）。另：整文件取回的 `task_assets.go` 还含清单外的 `taskIntentAssets`（`GET /api/tasks/{id}/intent-assets`，属 exploration 34 条），已由协调员删掉，未注册。 |
| 前端接线（`web/src/SettingsView.jsx`、`web/src/System.jsx`） | 设置新增 3 个分区：运行开关（GET/PUT `/api/settings`，7 个开关 + 并发上限数字框：流量捕获、Agent 自动绑定流量、LLM 轮询、任务并发、联网搜索、LLM 录制、推送总闸）、更新（update/check + apply + rollback + stream，应用后提示服务将重启）、账户安全（改密码）；系统侧栏新增「日志」签（`/api/logs`、`/api/logs/history`、`/api/stats`）。其余恢复的路由只恢复后端，UI 待接（见阶段文档清单）。`npm run build` 通过，新 bundle `index-BxsVq5m1.js`，旧 `index-BwDk6e9j.js` 已删。 |

未动：exploration 34 条（含 `goals_api.go` 整文件、`taskIntentAssets`、interventions、constraints、deepenFinding）、旧版 LLM 全局配置 3 条、第二轮删的 22 条（U5/U6/U8/U11/U14）、UNCERTAIN 剩余 40 条、KEEP 的 65 条；现网服务端、靶场、隧道、任务 #1 的数据；未 commit、未 push、未跑 LLM 真实任务。

测试：`go vet ./...`、`go build ./...` 通过；`go test ./cmd/... ./config/... -count=1` 通过。恢复中修了两处装配问题：`gitbash_test.go` 漏掉的 `doFunc` 类型声明（vet 报错 `undefined: doFunc`，补回后通过）；`triggers.go` 的本地 helper `agentByPathKey` 与 W1 恢复的 `agentByKey` 函数体逐字相同，已由协调员统一为 `agentByKey`、删掉重复。无库 `go test ./... -count=1`（日志 `~/workspace/.tmp/test-restore-nodb.log`）exit 1，失败 30 个——11 个已知需真库 + 5 个 punchlist + 14 个恢复的通知接口测试（均在夹具处报“未找到数据库配置”，与已知 16 个同类）；其余 28 个包全绿。**无库基线从此是 30 个失败，不是 16 个。**恢复的纯函数测试无库通过（5 个技能上传、5 个 releaseCache、2 个 withGlobalVars、1 个公司 scope 输入、1 个 tokenStats 形状），需库的恢复测试无库时干净跳过（gitbash 3 个、任务模板 1 个、公司 6 个）。

验证：路由数核对——`grep -c mux.HandleFunc server/server.go` 由 98 变 **245**（+147），helper 7 条不动，总计 **252 条**；新增集合与 147 条清单逐条比对零缺失、零多余；旧版 LLM、exploration tokens、intent-assets、第二轮路由均不在注册表内。隔离实测（重建后的测试库 `artex_harness_test`，端口 18997/18998，scratch 数据目录）：`GET /api/settings` 返回 22 个键；`PUT /api/settings` 改 `traffic_capture=true` 后 GET 回显为 true；`GET /api/update/check` 200（仓库尚未发布正式版本，如实报错）；`GET /api/update/stream` 正常出 SSE（`phase: idle`）；`GET /api/stats`、`/api/logs`、`/api/logs/history`、`/api/notify/channels`、`GET /api/agents` 均 200；`POST /api/auth/change-password` 返回 `{"ok":true}` 且新密码可登录。另对恢复的测试做了一次定向真库子集（非全量 `make test-db`）：`go test ./server/ -run 'TestNotify|TestGitBash|TestTaskTemplate|TestCompany|TestDeleteCompany|TestListAssets|TestCreateCompany|TestUploadSkill|TestReleaseCache|TestTokenStats|TestWithGlobalVars' -count=1` 在真库下 **42 通过、0 失败**（日志 `~/workspace/.tmp/test-restore-db.log`）。`make test-db` 全量仍未跑（按 Julie 此前决定）。

环境注记：验证期间发现虚拟机重启后 PG 集群虽在、但角色与库已被清空（只剩 postgres 角色与系统库，现网服务端进程亦不在）；为做定向真库验证重建了测试角色与 `artex_harness_test` 空库（`CREATE ROLE harness_test … CREATEDB` + `CREATE DATABASE artex_harness_test OWNER harness_test`），schema 由服务端/测试夹具自动迁移；未动真实库（`artex_harness` 本就不存在了，未尝试恢复）。

### 3.22 前后端接线核对 + toast 调用形修复（2026-11-21，本仓库新增，非上游）

Julie 2026-11-21 18:08 CST 指示：不再重跑整轮真实任务，只确保前后端接线完成、前端没有问题。做法：静态抽取 `web/src/` 全部 `/api/` 调用（76 条 distinct 路径，含模板字面量动态段）逐条对照后端路由表，6 条初筛“缺失”逐一核对均为抽取伪影（`${qs}` 查询串、`encodeURIComponent` 截断、文案里的 `/api/notify` 字样、`/api/update/check${force}` 模板后缀）；`GET /api/harness/budget/{id}` 等动态段路由均存在。结论：**前端调用的接口后端全部有注册，零缺口**。

核对中发现一个真前端 bug：`toast` 在 App 里是 `{success, error, info}` 对象（`web/src/toast.jsx`，不可当函数调用），但 `SettingsView.jsx` 与 `Sidebar.jsx` 共 21 处用 `toast?.(msg)` / `toast?.(msg, true)` 调用形，运行时抛 `TypeError: toast is not a function`——命中路径包括切换默认模型、删除模型、新增/更新供应商与模型、审批允许/拒绝、任务置顶/归档/删除/重命名；抛错点在 await 成功之后，导致提示不显示、后续刷新（如 `reloadProfiles()`）被跳过。已全改为 `toast.success(msg)` / `toast.error(msg)`（其余文件本来就用正确调用形），`SettingsView.jsx` 的 `say()` 兼容壳保留，过期注释同步改写。

验证：`npm run build` 通过，新 bundle `index-B-Je1v-K.js`，`/app/` 下发验证已含新 bundle；隔离冒烟（新建空库 `artex_harness_smoke`，端口 18997/18998，scratch 数据目录，用后已删库）：`/api/health` 返回 `{"ok":true,"service":"artex-harness"}`，`/` 302 到 `/app/`；18 个重点接口（含 settings、update/check、stats、logs、notify/channels、agents、tasks、findings/stats、tokens/daily、llm/profiles、mcp、skills、intercept、traffic、harness/ready、harness/capabilities）用初始化后的 token 测全部 200；`PUT /api/settings` 改 `traffic_capture=true` 回显 true（后改回 false）；`GET /api/update/stream` 正常出 SSE（`phase: idle`）；`POST /api/auth/change-password` 返回 `{"ok":true}` 且新密码可登录。未跑 `make test-db` 全量；未 commit、未 push。

遗留：约 140 条恢复路由无前端入口（后端已恢复，UI 待接，清单见 3.21 与 `docs/stages/S6-RESTORE-MGMT.md`）；自更新 `check` 如实报仓库尚未发布正式版本，一键更新要真正可用需先发布带二进制资产的 Release；自动更新仍没有。

### 3.23 发布包命名断裂修复（云端 CI 三连败）

**发现**：2026-11-21 18:19，Julie 让检查云端 CI（产物 artifact 名为 `artex-CL`）最后打包的压缩包名字。结论：名字是错的，且已经导致 CI 连续失败三次。

- 证据：`gh run list` 显示 2026-11-21 三次推送到 main 的 CI 全部 failure（run 44888762798、44887699179、44874463134，分别对应 c86c09f、ce14587、b4dccfe）；此前 11-17 为 success。失败日志：六个编译任务（linux/darwin/windows × amd64/arm64）全灭在「校验归档与命名」，`##[error]缺少产物 ACTION/artex-0.0.0-cl.22-c86c09f-windows-amd64.zip`；finalize 与 release 被跳过。
- 根因：隔离 hotfix（3.11）把二进制改名为 artex-harness 时，只改了 build.sh 的包名（`artex-harness-<version>-<os>-<arch>.zip`），漏了四处仍期望旧名的校验/消费方：① CI plan.sh 生成的矩阵包名与更新器 `AssetName` 均为 `artex-<version>-<os>-<arch>.zip`（另有 `TestAssetNameMatchesBuildScript` 把该命名钉死为 build.sh 的约定）；② 更新器 `extractBinary` 按基名找包内 `artex`/`artex.exe`，而包内实际是 `artex-harness`；③ assemble.sh 内容校验要求包内 `${root}/artex`（windows 为 `artex.exe`）；④ ci.yml linux/amd64 冒烟测试跑 `$tmp/<包名>/artex -h`。本地实测复现：`ARTEX_PACKAGE=1 ./build.sh --target linux/amd64` 产出 `artex-harness-0.3.3-linux-amd64.zip`，而 CI 期望 `artex-0.3.3-linux-amd64.zip`。
- 修复（以 CI 与更新器已达成一致的 `artex-…zip` 为准，包名改回；包内二进制保持 artex-harness）：`build.sh` 的 `package_name` 改回 `artex-${ARTEX_BUILD_VERSION}-${goos}-${goarch}`（包内目录随之变为 `artex-<version>-<os>-<arch>/`，与 CI 期望一致；构建输出目录 `dist/artex-harness-<os>-<arch>/` 不动）；`selfupdate/stage.go` 的 `extractBinary` 改找 `artex-harness`/`artex-harness.exe`；`.github/scripts/assemble.sh` 包内校验改为 `artex-harness`/`artex-harness.exe`；`.github/workflows/ci.yml` 冒烟测试路径改为 `…/artex-harness`；`.github/scripts/plan.sh` 提示文案、`.github/CI.md` 约定、测试 `TestExtractBinaryFindsNestedEntry` 的 fixture 与 `selfupdate.go` 注释同步改名；`docs/stages/S6-HOTFIX-ISOLATION.md` 发布包一行订正并附订正注。
- 验证：本地重新打包产出 `artex-0.3.3-linux-amd64.zip`，CI 三项校验逐项模拟通过（文件存在、`unzip -tq`、包内含 `artex-harness`/`start.sh`/`skills/`/`config.example.json`、冒烟 `-h` 含 `-addr`）；`gofmt`、`go vet ./selfupdate/...`、`go test ./selfupdate/...`、`go build ./...` 通过。
- 注意：存档名刻意保持 `artex-…zip` 是为了与 CI、更新器一致；若日后想把存档名也改成 `artex-harness-…zip`，须同步改 plan.sh（NAME_RE 与矩阵包名）、AssetName、github_test.go fixture、assemble.sh、ci.yml 冒烟路径，缺一不可。本次推送触发的新 CI 运行是否变绿，见推送后核查；仓库至今未发布过 Release，一键更新仍需先发 Release 才能真正可用。

## 4. Go module 路径为什么保持不变

本仓库继续使用 `module github.com/Autumn-27/artex`，不改为新路径。原因：

1. 上游自身在既有 fork（`j5tv9nfzhn-hue/ARTEX`）上采用同一做法（其 README 明确写着
   "原项目的 Go module 路径保持不变"），是本项目族的既有约定；
2. 全树 534 个 `.go` 文件的 import 与 `go.sum` 无需任何改写，把"相对上游的差异"压到最小
   ——而这正是行为一致性与未来合并上游改动的关键；
3. `third_party/norma` 的 `replace` 在本仓库内自洽。

## 5. 验证记录

在复制后的仓库内执行（2026-11-14，云电脑，Linux/amd64，Go 1.26.3）：

| 命令 | 结果 |
|---|---|
| `go build ./...` | 通过（无输出） |
| `go test ./guard/... ./config/... ./llmpool/...` | `ok`（`guard` 0.008s / `config` 0.004s / `llmpool` 0.006s） |
| `find . -name '*.go' \| wc -l` | 534 |
| `du -sh .` | 16 MB |

## 6. 许可证继承

上游为 AGPL-3.0；复制其源码形成的衍生作品**必须同样以 AGPL-3.0 授权**（决策 1）。本仓库
`LICENSE` 为上游 `LICENSE` 的逐字节副本。AGPL 第 13 条的**网络服务源码公开义务**适用于本
仓库及其任何衍生部署。使用边界另见 [DISCLAIMER.md](DISCLAIMER.md)。
