# S6-FIX-PROXY-SANDBOX：真实任务 #1 暴露的两个高危问题修复（2026-11-21）

Julie 2026-11-21 16:01 CST 确认只修这两件，不做其他项、不重跑、不提交、不推送。
问题来源：真实完整渗透测试任务 #1（`~/workspace/real-pentest-task1-report-20261121.md`）。

## 1. 问题复现

### 1.1 流量代理对回环目标失效（高）

- 现象：任务 #1 里 agent 对靶场发了 6,523 次请求，录制代理（8798）0 条记录；流量面板空、发现无流量绑定、验证门“须流量证据绑定”对本地靶场永远过不了。
- 根因：`agent/worker.go` 的 `proxyEnv` 只设 `HTTP(S)_PROXY` / `ALL_PROXY`，不覆盖 `NO_PROXY`。本机 `no_proxy` 含 `127.0.0.1`，curl / Python urllib / requests 都认 `no_proxy`，回环请求直连靶场。
- 对照实验（任务 #1 报告 §4）：显式代理 + `NO_PROXY` 为空 → 有记录；默认 `no_proxy` → 无记录。
- 影响面核查：
- **Bash / curl / Python**：走 `proxyEnv` → `agentcore.Options.BashEnv` → SDK `ToolContext.Env` → `withEnv`（`os.Environ()` 后追加，Go exec 保留最后一个重复键，见 `third_party/norma/tool/task.go`）。受影响。
- **WebFetch**：`http.ProxyURL` 固定代理函数，根本不读 `NO_PROXY`（`third_party/norma/tool/webfetch.go`）。不受影响。
- **浏览器 MCP**：`server/manager.go` 的 `syncBrowserMCPProxy` 只挂 `--proxy-server`，不设 `--proxy-bypass-list`；Chromium 不读 `NO_PROXY` 环境变量。所以 `NO_PROXY` 的 bug 不适用于浏览器工具。**未验证**：Chromium 默认是否绕过回环（需 `--proxy-bypass-list=<-loopback>` 才强制走代理）；任务 #1 里浏览器工具启动超时、全程不可用，无实测数据。干净重跑时需补测浏览器流量是否进代理。

### 1.2 沙箱未接入（高）

- 现象：任务 #1 里 agent 以 root 身份 `Read` 了靶场源码 `~/workspace/target-range/app.py`（含列出 4 个预埋漏洞的 docstring），`Write` 可任意写 `/tmp`。后半程发现（XSS、traceback、UNION 拖库确认）是白盒辅助下的，黑盒纯度受损；理论上能读宿主任何文件。
- 根因：`harness/sandbox` 只有策略对象，无生产调用方（阶段一文档已写明“只是策略对象，不是容器运行时”）。本次修的是“把策略真正接到 agent 工具执行上”，不是造容器。

## 2. 修法

### 2.1 proxyEnv 强制清空 NO_PROXY（捕获开启时）

`agent/worker.go` 的 `proxyEnv`：`caCert!= ""` 当且仅当录制 MITM 开启（`Manager.ProxyCACert` 只在捕获开启时非空，见 `server/manager.go`），在该分支追加 `NO_PROXY=` 与 `no_proxy=`（空值 = 全部走代理）。捕获关闭时不动用户原有的 `NO_PROXY`。

调用方只有三处，全走同一函数：`agent/worker.go`（Worker）、`agent/planner.go`（Planner）、`agent/runopts.go`（`ModelRunConfig.ApplyModelDefaults`，MainAgent / ChatAgent / 边问边答共享）。

### 2.2 沙箱策略接到 Guard 的 PreToolUse 钩子

接线点是 `guard/guard.go` 的 `preToolUse`——每个工具调用执行前都经过它（harness agent 与 worker 经 `t.Guard.Hooks()`，子代理委派经同一 `h.hooks`）。沙箱包文档原来就写着调用方是 guard，这里是把它落实：

- `Guard` 加 `sandbox sandbox.Sandbox` 字段 + `WithSandbox` 选项 + `SetSandbox`（与 `SetScopeChecker` 同款）。
- `preToolUse` 在意图硬拒绝之后、RoE 范围检查之前跑沙箱检查（第 1.5 步）：
- **文件工具**（Read / Write / Edit / MultiEdit 取 `file_path`；LS / Glob / Grep 取 `path`）：`ValidatePath`，越界即拦，返回 `` 前缀的明确错误（让 agent 知道这是平台拦截、不是目标防御，别绕）。
- **网络工具**（WebFetch 取 `url`；Bash 用 `guard.ExtractHosts` 提主机）：只管回环 / 内网字面量（`127.0.0.1`、`localhost`、`::1`、RFC1918 等）。公网主机留给 RoE 范围检查；`web_search` 不查——它的查询文本不是连接目标。
- **RoE 授权例外**：回环 / 内网主机先查任务 RoE 范围（`CheckHostAgainstScopes`，经已有的 `ScopeChecker` 实时读库）。在范围内 → 放行（任务 #1 的范围行是 `ip 127.0.0.1/32`，放行成立）；范围行存在但主机不在内 → `sandbox.CheckHost` 拦。**未锚定范围的任务（无范围行）fail open**，与 `CheckScope` 的 legacy 规则一致；文件收容不受此影响，始终执行。
- `server/task_llm.go` 的 `agentsForTask` 里给任务的 Guard 装沙箱：根目录 `<数据目录>/tasks/<taskID>`（与 `HarnessAgent.WorkDirFor` / `ensureRunDir` 同布局，worker 的 `i<intentID>` 子目录在其内）。**只对 harness 模式任务装**；探索模式任务保持旧行为（其 agent 读更宽的树）。沙箱初始化失败时记日志、不启用沙箱、不阻断建任务。

### 2.3 策略对象本身的补强（`harness/sandbox/sandbox.go`）

- `ValidatePath` 增加**符号链接解析**：对已存在的路径（或最近已存在的祖先）做 `EvalSymlinks` 再判收容，工作区内指到区外的链接、经链接目录的路径都被拦。根目录在构造时同样解析，保持两侧一致。
- 增加 `Policy.DenyPrefixes`（默认 `/etc`、`/proc`、`/sys`、`/dev`、`/root`、`$HOME/.ssh`）：宿主敏感位置即使落在某个 `AllowedRoot` 内也被明确拒绝。**不做文件名后缀禁区**（如 `.pem` / `.key`）：agent 可能把目标上拿到的私钥存进工作区再读回，后缀禁区会误伤正常渗透流程；宿主密钥文件（`harness-jwt.key`、`~/.ssh`、服务端数据目录、靶场源码目录）都在任务目录之外，已由收容拦住。
- 导出 `IsSensitiveEnvKey` 与 `EnvScrubOverrides()`：扫描当前进程环境，对每个敏感键返回 `KEY=`（空值）覆盖条。

### 2.4 Bash 环境清洗

`agent/worker.go` 新增 `bashEnv(proxyAddr, caCert)`：`proxyEnv` + `sandbox.EnvScrubOverrides()`，三处 `BashEnv` 调用点（Worker / Planner / `runopts.go`）全部改走它。SDK 的 `withEnv` 是 `append(os.Environ(), tc.Env...)`，Go exec 保留最后一个重复键，所以 `KEY=` 空值覆盖会压掉继承来的密钥（LLM key、token、`ARTEX_PG_DSN` 等）。无代理时也照常清洗（`bashEnv("", "")` 只返回覆盖条）。

不在本次范围：`third_party/norma` 未动；意图级硬拒绝与 RoE 范围检查本身未动；报告措辞、任务计数、审计链、预算默认值未动（另几项）。

## 3. 测试

- `agent/proxyenv_test.go`：捕获开启时 `NO_PROXY` / `no_proxy` 为空串；捕获关闭时不动 `NO_PROXY`；`bashEnv` 覆盖敏感变量、不碰良性变量、代理路由仍在、无代理时仍清洗。
- `agent/proxyenv_e2e_test.go`（`TestProxyEnvLoopbackIsRecorded`，新增）：起真实 `traffic.Open` 录制代理 + `httptest` 回环目标；对照组（只设 `no_proxy=127.0.0.1`、不走 `proxyEnv`）录到 0 条；实验组（同一 `no_proxy` + `proxyEnv` 的 env）curl 200 且代理录到 1 条。curl 不可用时跳过。
- `harness/sandbox/sandbox_test.go`：工作区内链接指区外被拒、经链接目录的路径被拒、区内链接放行；`~/.ssh` 与 `/proc` 被拒；`EnvScrubOverrides` 只覆盖敏感键、不泄露值。
- `guard/sandbox_test.go`（新增）：Read `/etc/passwd`、越界相对路径、靶场源码路径、经符号链接的读取被拦，区内读写放行；RoE 授权的 `127.0.0.1:18080` 上 WebFetch / Bash curl 放行，`192.168.1.10` / `10.0.0.5` 被拦；公网主机过沙箱底线；未锚定任务网络 fail open 但文件收容仍生效；未装沙箱的 Guard 不拦截。
- 全量：`go vet./...`、`go build./...` 通过；无库 `go test./... -count=1` 除已知 11 个需真库的 server 测试外全绿（日志 `~/workspace/.tmp/testdb-fix-nodb.log`）。

## 4. 遗留与边界（诚实写明）

- **沙箱仍是策略层，不是容器隔离**。agent 仍以服务端同一用户（root）运行；Bash 是通用 shell，`cat /etc/passwd` 这类 shell 内文件访问无法靠工具参数解析拦住——文件收容只覆盖 Read / Write / Edit / MultiEdit / LS / Glob / Grep 七个文件工具。真正的进程级隔离（容器 / 用户 / namespace）仍是阶段三的事。
- **浏览器 MCP 的回环代理行为未验证**：Chromium 不读 `NO_PROXY`，但默认是否绕过回环需实测（任务 #1 里浏览器工具不可用）。干净重跑时补测。
- **探索模式任务未装沙箱**（只对 harness 模式装，见 §2.2）。若后续要覆盖探索模式，需先确认其 agent 是否读任务目录外的树。
- **`AllowedRoots` 目前只有任务目录**。若将来绑定 `project_dir`，需把它加进 `AllowedRoots`，`DenyPrefixes` 会继续兜底敏感位置。
- **未锚定范围的任务网络 fail open**（见 §2.2）：与 `CheckScope` 的 legacy 规则一致，是有意保留，不是疏漏。
- 干净重跑前还缺：修完后的二进制 + 录制代理实测（本轮只在测试里验证了代理录制，未跑 LLM 任务）；浏览器工具可用的一轮（补测浏览器流量）；阶段三排期等重跑结果。
