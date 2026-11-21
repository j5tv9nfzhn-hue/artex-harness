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
