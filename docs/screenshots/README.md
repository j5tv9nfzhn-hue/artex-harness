# 界面截图

本目录是**我们自己控制台**的真实截图，路径 `harness/`。参考产品的截图**不随本仓库分发**
（第三方品牌物料），只在本机 `refs/` 下用于取色与对照——见本文末「参考图」一节。

## 文件清单

全部 1440×900（`harness-17` 为 900×900，专门展示窄窗口布局），浅色为主，含一张深色。

| 文件 | 画面内容 |
|---|---|
| `harness-01-login.png` | 登录门：`artex-harness` 卡片、用户名固定 `ARTEX`、密码框、条款一行 |
| `harness-02-home-light.png` | 工作区：左栏（对话 + 置顶运行 + 旧管线分组）、中栏事件流顶部（轮次块 + 本轮计划）、composer |
| `harness-03-conversation-light.png` | 事件流底部：思考块、连续 `Bash × N` 合并卡、Write 调用、**就地审批卡片**（拒绝 / 允许）、输入区 |
| `harness-04-usage-light.png` | 用量页：日历热力格（369.7k tokens / 近 26 周）、六张统计卡（含「压缩 1 次（推断）」）、每轮输入 token 曲线与压缩回落竖虚线 |
| `harness-05-workbench-files.png` | 右栏工作台 → 文件：文件树与 A/M/D 变更标记、大小 |
| `harness-06-workbench-diff.png` | 工作台 → DIFF：相对基线的变更清单（`A` 新增 / `M` 修改） |
| `harness-07-workbench-diff-file.png` | DIFF 正文：unified diff（`@@` 块、`+`/`-` 着色） |
| `harness-08-workbench-terminal.png` | 工作台 → 终端：人在任务工作区执行命令、退出码与耗时，并在事件流里记为 `human.sh` |
| `harness-09-subagents.png` | 子代理面板：两条后台委派（侦察 已完成 4分10秒 / 验证 失败 1分0秒）、计数徽标 |
| `harness-10-browser-session.png` | 浏览器面板 → 会话：Playwright 与 DevTools 均 `live`、标签 2 条、最近使用时间 |
| `harness-11-browser-f12.png` | 浏览器 → F12：console 错误分级列表、最近 `mcp__*` 动作，并写明 network 表不在此版本 |
| `harness-12-system-ready.png` | 系统 → 就绪：PostgreSQL / 模型端点 / 技能目录 / 浏览器 MCP 四项，各自带原因与下一步 |
| `harness-13-system-capabilities.png` | 系统 → 能力：54 项能力的 surface / 可执行 / 并行安全 / 延迟解锁 / 启用状态 |
| `harness-14-palette.png` | 命令面板（⌘K）：分组、「新建对话 / 新建运行」、任务跳转、关键词命中 |
| `harness-15-conversation-dark.png` | 深色：同一事件流（近黑画布、边框作高光） |
| `harness-16-chat-light.png` | 对话这条产品线：侧栏「对话」分组、用户消息 + Markdown 三段结论 |
| `harness-17-narrow-900.png` | 窄窗口 900px：侧栏隐藏、右栏转为底部横条（工作台页签仍在） |

## 这些图是怎么来的（可复算）

两个脚本，都在本目录：

1. `seed-demo.sh` —— 把「一次已完成的 HARNESS 运行」写进一个**独立测试库**：
   task + activity（thinking / text / tool_use / tool_result / intercept_request / usage）
   + `harness_revisions`（轮次边界）+ 项目目录与基线 + 委派 journal + 一条对话。
2. `capture.mjs` —— 用 CDP（headless Chrome）登录、逐个打开面板、**断言到达预期状态**后截图。

```bash
# 1) 起一个独立库与数据目录（不要指向开发库）
createdb artex_shots
ARTEX_PG_DSN="postgres:///artex_shots?sslmode=disable" ./artex -addr 127.0.0.1:8796 -data /tmp/shotdata &
TOK=$(curl -s -X POST localhost:8796/api/auth/init -H 'Content-Type: application/json' \
      -d '{"password":"…"}' | jq -r .token)

# 2) 灌演示数据（脚本会删掉上一次的演示任务，可重复执行）
./docs/screenshots/seed-demo.sh "postgres:///artex_shots?sslmode=disable" /tmp/shotdata

# 3) 重启服务：任务列表在内存里，直接写库不会进管理器
# 4) 绑定项目目录 + 真实拉起浏览器会话（截图里那两个 live 是真的 MCP 进程）
curl -X PUT localhost:8796/api/tasks/1/project -H "Authorization: Bearer $TOK" \
     -H 'Content-Type: application/json' -d '{"project_dir":"/tmp/shotdata/projects/acme-demo"}'
curl -X POST localhost:8796/api/harness/browser/restart -H "Authorization: Bearer $TOK" \
     -H 'Content-Type: application/json' -d '{"task":"1"}'

# 5) 抓图（写到自己指定的目录）
TOKEN_FILE=/tmp/tok OUT=/tmp/shots-out node docs/screenshots/capture.mjs
```

### 诚实边界

- **内容是构造的**：命令、输出、结论都是演示数据，不是对任何真实目标的扫描结果。
- **界面是真实的**：同一份二进制、同一套接口、同一份 `tokens.css`；`capture.mjs` 每个状态
  都有断言（找不到元素就报 `MISSING`，不静默跳过），所以图能反映真实渲染。
- 构建这套图的机器**没有 LLM 端点**（`llm_profiles = 0`，引擎 idle），因此跑不出一轮真实
  agent 对话——这是为什么要用种子数据。真实长跑的截图需要在配好端点的环境里重抓。
- 子代理明细（点进去看每一步）需要磁盘 sidechain，种子数据没有，所以那两行显示
  「没有落盘记录」——**这是界面在如实说"没有"**，没有被伪造。

## 参考图（不在本仓库）

对照用的参考截图来自第三方项目 `dsh-claude-style`（MIT）的 `docs/screenshots/`，
共 8 张：`claude-{home,conversation}-{light,dark}.png` 与 4 张 DeepSeek 配色变体。
它们属于参考产品的品牌物料，**不进本仓库**；逐项对照记录在
[`../17-REFERENCE-UI-MAPPING.md`](../17-REFERENCE-UI-MAPPING.md)，令牌采样值在
[`../../web/src/tokens.css`](../../web/src/tokens.css) 头部注释里。
