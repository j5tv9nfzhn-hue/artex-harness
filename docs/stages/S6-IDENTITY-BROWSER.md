# S6 自称 artex-harness + 启动自动打开控制台

日期：2026-11-21。Julie 17:07 CST 确认两件一起做。PROVENANCE 3.20 节。

## 0. 问题来源

构建产物检查（`make build` 产出 `./artex-harness`，隔离启动实测）发现三处不一致：

1. 可执行文件名已是 `artex-harness`，默认端口已是 `:8797` / `127.0.0.1:8798`（Go 代码里无 8787/8788），但程序自己仍自称 ARTEX：启动横幅是 ASCII ARTEX 图、监听日志写 `ARTEX dev backend listening`、`/api/health` 返回 `"service":"artex"`、控制台侧栏品牌行写 `ARTEX`。
2. 启动后不自动打开 web UI：`main.go` 与 `start.sh`、`dev.sh`、`install.sh`、`update.sh` 里都没有打开浏览器的逻辑，只打印地址，得手动开 `http://localhost:8797`。

## 1. 改法

### 1.1 自称改为 artex-harness

- `cmd/artex/main.go`：包注释改为 artex-harness；横幅的 ASCII ARTEX 图改为纯文本 `artex-harness`（副标题 `AI 自主渗透测试系统`与版本行不动）；监听日志改为 `artex-harness %s backend listening on %s`。
- `server/server.go`：`GET /api/health` 的 `service` 由 `"artex"` 改为 `"artex-harness"`；包注释同步改写。
- `web/src/Sidebar.jsx`：品牌行 `ARTEX` 改为 `artex-harness`（12px 展开行，flex 空间足够）；注释同步改写。关于对话框标题此前已是 `artex-harness`，不动。
- `web/src/SettingsView.jsx`：设置页环境区服务名的兜底值 `"artex"` 改为 `"artex-harness"`（正常值来自 `/api/health`）。
- `web/src/ui.jsx`：logo 注释 `ARTEX's own mark` 改为 `artex-harness's own mark`。

未动：Go module 路径（仍 `github.com/Autumn-27/artex`，见 PROVENANCE §4）；管理员用户名仍固定 `ARTEX`（功能标识，改它会断登录与已有会话）；默认端口与数据目录不变。

### 1.2 启动自动打开控制台

- 新增 `cmd/artex/browser.go`：`consoleURL(addr)`（`:8797` / `0.0.0.0:8797` / `[::]:8797` 一律变 `http://127.0.0.1:8797/`，真实主机名保留，IPv6 回环包方括号，无端口返回 `""`）；`canOpenBrowser()`（macOS / Windows 直接 true；Linux 需 `DISPLAY` 或 `WAYLAND_DISPLAY`）；`openBrowserCmd(url)`（Linux `xdg-open`、macOS `open`、Windows `rundll32 url.dll,FileProtocolHandler`）；`maybeOpenBrowser(addr, enabled)`（后台等 `/api/health` 有响应，每 200ms 轮询最多 5 秒，再打开；打开失败只记一条 `[browser]` 日志并提示手动访问，永不影响启动）。
- `cmd/artex/main.go`：新增 `-open-browser` flag，默认 true；`start.sh` 透传参数（`"$@"`），故 `-open-browser=false` 可直接经启动脚本关闭。

设计取舍：默认开、而不是默认关——桌面用户双击即开控制台是这件改动的目的；无头服务器、容器、CI 里没有图形会话，`canOpenBrowser()` 会静默跳过，不会多出失败进程或报错日志。等 health 就绪再打开，避免浏览器先于服务就绪打开一个打不开的页面。

## 2. 测试与验证

- 新增 `cmd/artex/browser_test.go`：`consoleURL` 8 个用例、`canOpenBrowser` 的 DISPLAY/WAYLAND_DISPLAY 组合、`openBrowserCmd` 按操作系统选命令。`go vet ./...`、`go build ./...`、`go test ./cmd/... -count=1` 通过；`npm run build` 通过，console-app 新 bundle `index-BwDk6e9j.js`（旧 `index-BQKvlJP5.js` 已删）。
- 隔离实测（测试库 `artex_harness_test`、端口 18997/18998、scratch 数据目录，未碰现网服务端与任务 #1 数据）：有 DISPLAY 时假 `xdg-open` 被调到 `http://127.0.0.1:18997/`，日志 `[browser] 已在浏览器中打开控制台 http://127.0.0.1:18997/`；无 DISPLAY 时服务正常、静默跳过；`-open-browser=false` 时有 DISPLAY 也不打开。三次实测横幅均为 `artex-harness`，监听日志为 `artex-harness dev backend listening`，`/api/health` 返回 `{"ok":true,"service":"artex-harness","version":"dev"}`。
- 未跑 `make test-db` 全量：本轮只动启动路径与两处字符串，server 包的改动是一行常量。

## 3. 遗留

- `server/server.go` 在 `gofmt -l` 里是脏的，但脏的是 1113 行附近的 DTO 结构体标签对齐（上一批 punchlist 加 `RunBudgetMinutes` 时留下的），不是本轮改的两行；未顺手格式化，避免 diff 噪音。
- 真机桌面上的自动打开（macOS `open` / Windows `rundll32`）未实测，只在 Linux 上用假 `xdg-open` 验证了接线；命令选择有单测覆盖。
