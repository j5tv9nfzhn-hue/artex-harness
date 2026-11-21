# S6 Hotfix — 与上游 ARTEX 的端口 / 数据库 / 身份隔离

日期：2026-11-21。Julie 提醒：后端改动少，解压后与 ARTEX 共用端口和数据库，导致冲突、数据合并。本 hotfix 在阶段一之后、阶段二之前实施，只改身份与默认值，不改 guard / harness 行为。

## 问题（核实后）

| 冲突面 | 改前（与 ARTEX 完全相同） | 后果 |
|---|---|---|
| HTTP / 代理端口 | `:8787` / `127.0.0.1:8788`（`cmd/artex/main.go`、`Dockerfile`、`dev.sh`） | 后启动的进程 bind 失败，或误连到对方 |
| 数据库 | 库名 `artex`、用户 `artex`（`docker-compose.yml`、`.env.example`、`config.example.json`、DSN 默认 `5432/artex`） | 同一套 51 张表，任务、配置、拦截规则、发现全部合并 |
| Compose | 镜像 `autumn27/artex:latest`、服务名 `artex`、卷 `pgdata`、宿主端口 8787/8788 | `docker compose up` 跑起来的根本不是 harness |
| 数据目录 | 默认 `./data`（可执行文件旁） | 同目录解压时互相覆盖 |
| JWT 密钥 | `jwt.key`（可执行文件旁） | 同目录解压时共用/覆盖签名密钥，会话串台 |
| 配置文件 | `config.json`（可执行文件旁 / CWD） | 同目录解压时共用数据库配置 |
| 二进制 / 自更新 | `artex`、`artex.new/.old/.upgrade.json` | 同目录解压时互相覆盖、互相升级 |

## 改后默认值

| 项 | 新值 | ARTEX（上游） |
|---|---|---|
| HTTP | `:8797` | `:8787` |
| 流量代理 | `127.0.0.1:8798` | `127.0.0.1:8788` |
| 数据库 | `artex_harness`，用户 `artex_harness` | `artex` / `artex` |
| 本地 PG 宿主端口 | `5433:5432`（`install.sh` Docker 分支与示例 DSN） | `5432:5432` |
| 数据目录 | `./data-harness` | `./data` |
| JWT 密钥 | `harness-jwt.key` | `jwt.key` |
| 配置文件 | `artex-harness.json`（`HARNESS_CONFIG` 优先，`ARTEX_CONFIG` 仍可用；旧 `config.json` 仅作回退） | `config.json` |
| 二进制 | `artex-harness`（`artex-harness.exe`） | `artex` |
| Compose 服务 / 卷 | `postgres-harness` / `artex-harness`，卷 `pgdata-harness`，镜像 `artex-harness:latest`（本地构建） | `postgres` / `artex`，卷 `pgdata`，镜像 `autumn27/artex` |
| 发布包 | `artex-harness-<version>-<os>-<arch>.zip`，`dist/artex-harness-<os>-<arch>/` | `artex-…` |

注意：`selfupdate.ResolvePaths` 按可执行文件名派生 `.new/.old/.upgrade.json`，改名后自动变成 `artex-harness.new` 等，无需另改代码。`loadOrCreateJWTKey` 不从 ARTEX 的 `jwt.key` 迁移，只迁移本产品旧位置 `data-harness/jwt.key`（如存在）。

## 改动文件

代码：`cmd/artex/main.go`、`config/config.go`、`config/config_test.go`（新增 `TestPathPrefersHarnessConfig`）、`server/auth.go`、`server/server.go`、`db/db.go`。
部署/脚本：`Dockerfile`、`docker-compose.yml`、`.env.example`、`config.example.json`、`Makefile`、`build.sh`、`start.sh`、`start.bat`、`install.sh`、`update.sh`、`reset-password.sh`、`.gitignore`。
文档：`README.md`、`ARCHITECTURE.md`、`CONTRIBUTING.md`、`docs/03-ARCHITECTURE.md`、`docs/07-HARNESS-RUNTIME.md`、`docs/15-FEATURES.md`、`docs/16-ARCHITECTURE-DETAIL.md`、`web/src/App.jsx`、`web/src/lib/auth.js`。
未动：`README-ARTEX.md` 与 `docs/upstream/`（上游原文）、`third_party/norma`、以及 `agent/*_test.go` / `traffic/proxy_test.go` 里作为解析用例的 `8788`  fixture。

## 迁移（已在用旧版的用户）

- 旧库 `artex` 里的数据不会自动搬到 `artex_harness`。要保留历史任务，需手动 `pg_dump artex | psql artex_harness`，或继续用旧 DSN 启动（显式 `-addr :8787` + `ARTEX_PG_DSN=…/artex` 仍可用，默认值变了而已）。
- 旧 `config.json` 仍会被读取（回退），但新安装请复制 `config.example.json` 为 `artex-harness.json`；旧 `data/` 不会自动改名，`data-harness/` 是全新空目录，旧工作区文件需手动拷过去。
- 旧 `jwt.key` 不会被当作新密钥：已登录会话会失效，需重新登录。

## 验证

- `go vet ./config/... ./server/... ./guard/... ./harness/sandbox/... ./db/...` 通过（`gofmt -l` 列出的 `server/*_test.go`、`db/*` 等是改前已有的格式问题，本次改动的文件不在其中）。
- `go test ./config/... ./guard/... ./harness/sandbox/... -count=1` 通过（`config` / `guard` / `harness/sandbox` 均为 ok，含新增的 `TestPathPrefersHarnessConfig`）。
- `go build -trimpath -o /tmp/artex-harness-test ./cmd/artex` 通过；`./artex-harness-test -h` 显示默认 `:8797`、`127.0.0.1:8798`、`data-harness`。
- `docker-compose.yml` 经 YAML 解析通过；`bash -n` 检查 `install.sh`、`update.sh`、`start.sh`、`dev.sh`、`build.sh`、`reset-password.sh` 均通过。
- 未跑 `make test-db`（需 PostgreSQL，`DBScopeChecker` 真库路径未覆盖，与阶段一相同）。
