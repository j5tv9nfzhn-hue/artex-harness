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
  `.env.example` `.dockerignore` `.gitattributes` `.github/` `skills/` `screenshots/`
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

### 3.4 `README.md` / `CHANGELOG.md` / `docs/` 重排

上游 `README.md` 是一份完整的项目说明（安装、更新、架构、免责）；本仓库需要自己的 README
来解释"harness 而非工具集"的定位。故：上游 README 原文保留为 `README-ARTEX.md`，本仓库新增
`README.md`、`DISCLAIMER.md`、`PROVENANCE.md` 与 `docs/`（`docs/` 在上游 `.gitignore` 里被
忽略，本仓库已取消该忽略，因为文档是本项目的交付物）。**纯文档改动，不触碰任何代码路径。**

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
