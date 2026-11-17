# 12 · Sprint 4：前端工作台（文件树 / DIFF / 终端 / 就地审批 / 合并）

日期：2026-11-16

原则不变（[ADR-0005](adr/0005-harness-maturity-and-borrowing-boundary.md)）：界面只呈现后端真实能力，
没有的能力禁用并写明原因；审批门仍是 `guard/intercept` 的 hooks，前端只是它的入口。

---

## 一、交付

| 能力 | 实现 | 位置 |
|---|---|---|
| 工作台三栏面板 | 右栏「文件 / DIFF / 终端」页签，与子代理面板互斥展开 | `web/src/Workbench.jsx` |
| 文件树 | `GET /api/harness/files/{id}` + 变更标记（A/M/D），点击预览 | 同上 |
| 文件预览 | `GET /api/harness/file/{id}?path=`，1 MiB 上限、二进制拒绝、路径穿越防护（`safeJoin`） | `server/harness_files.go` |
| DIFF 审阅 | `GET /api/harness/diff/{id}[?path=]`，unified diff；`baseline=false` 时明确说明 | 同上 |
| 终端 | `POST /api/tasks/{id}/exec`，bash -lc、60s 超时、可勾选「交给 agent」；人执行的命令以 `human.sh` 入流 | `server/harness_terminal.go` |
| 就地审批卡片 | AG-UI `INTERCEPT_REQUEST` → 事件流内卡片，允许/拒绝直接调 decide 端点 | `server/agui.go`、`web/src/App.jsx` |
| 工具调用合并 | 连续同类工具合并为「Bash × 6」，展开逐条 | `web/src/App.jsx` `groupRows` |
| 基线时机修正 | HARNESS 任务跳过目标分解，原先没有基线；现在每轮开始前建立，改动才可见 | `server/harness_checkpoint.go` |
| 检查点隔离 | `.harness-checkpoints` 纳入 sidecar 名单，不出现在文件树/DIFF/基线 | `server/harness_files.go` |

## 二、验证

| 项 | 方法 | 结果 |
|---|---|---|
| 文件树 + 预览 | 浏览器实测：`agent.txt 17B` → 点开显示正文 | ✅ |
| DIFF 变更检测 | 终端写入 `diff-probe.txt` → DIFF 显示 `1 个变更`、`A` 标记、unified diff 头 | ✅ |
| 终端执行 | `echo sprint4-ok; pwd` → `exit 0 · 20ms`，输出正确；事件流出现 `human.sh` | ✅ |
| 就地审批（真闭环） | 建 `ask` 规则 → 任务调用 Write → 卡片显示「需要审批：Write #13」→ 点允许 → `pending=allowed`、文件写入 `ok-final` | ✅ |
| 工具合并 | 事件流显示「Bash × 2 展开」 | ✅ |
| AG-UI 投影 | 新增 `server/agui_projection_test.go`（INTERCEPT_REQUEST / PERMISSION_DECISION / 未知 kind） | ✅ |
| `go vet ./...` | — | ✅ |
| `make test`（14 包） | — | ✅ |
| `go test ./server/...`（独立测试库） | `ARTEX_TEST_DB=artex_s4_verify` | ✅ |
| `harness-smoke` | 假 provider 离线 | ✅ `SMOKE OK` |
| `harness-gate` 自比对 | 216 vs 216 | ✅ 全部 HARD/soft 通过 |

## 三、验证中发现并修复的真实缺陷（三个）

### 3.1 审批卡片拿不到寻址字段

`ActivityList` 的查询**不返回 `detail`**（正文可能很大，按设计懒加载），而 `intercept_request`
的 `pending_id` / `tool` / `input` 只写在 `detail` 里。于是事件流里的卡片显示「需要审批：工具」、
编号为空，按钮无从解析请求。

修复：`HandleAsk` 同时把这份小 JSON 写入 `Metadata`（列表查询会返回它）；AG-UI 投影优先用
`Metadata`，回退 `detail`，再回退 `summary`，老数据也不丢。

### 3.2 审批按钮永远 400

后端 decide 端点只接受 `decision: "allowed" | "denied"`，前端发的是 `"allow" | "deny"`。
卡片看得见、点得动，但每次点击都失败，任务继续卡着。

修复：前端映射 `allow → allowed`、`deny → denied`，并在注释里写清两套词汇的区别。
实测同一张卡点「允许」后 `pending=allowed`、文件落盘。

### 3.3 HARNESS 首轮不置 running

`harnessLoop` 起轮次时从不把任务状态改为 `running`，新建的 HARNESS 任务在整个首轮里显示
`created`——界面在撒谎。

修复：每轮开始前若状态不是 running 就置为 running。实测任务 #233/#234 起轮后状态正确。

## 四、明确不做 / 已知限制

| 项 | 原因 |
|---|---|
| 行级评论、逐 hunk stage | 需要 VCS 语义；本项目 DIFF 是与基线对比，不引入 git 依赖 |
| 终端多标签 / 分屏 | 当前一任务一终端足够；多标签会增加会话生命周期管理面 |
| 语法高亮文件预览 | 只做纯文本预读，避免引入新的前端依赖与体积 |
| 二进制 / 超大文件内联 | 明确拒绝并说明，不做截断假装成功 |

## 四之二、右栏在窄窗口下的布局缺陷（已修）

**现象**：文件 / DIFF / 终端面板在窗口变窄（≤900px）后，或侧栏折叠后，格子被压成
一条极窄的竖条、内容竖排换行，看起来像整块被"透视/倾斜"压扁；主区与状态栏被挤到
别的行，视觉顺序变成「侧栏 → 面板 → 状态栏 → 主区」。它只改渲染，不改数据，
所以刷新、切换页签都复现，但接口返回一直是对的。

**根因（两处，都是 CSS 层叠顺序与优先级问题，不是几何计算错）**：

1. **窄窗口隐藏侧栏的规则写在组件定义之前。**
   `@media (max-width:900px) { .rail { display:none } }` 原本与 `.app`
   的网格声明挤在 shell 一节（文件第 42 行），而 `.rail { display:flex }`
   在第 100 行。两条规则优先级相同（0,1,0），由**源码顺序**决定胜负，
   于是 `flex` 赢——侧栏在 ≤900px 时根本没有被隐藏，仍占满一整行。
   多出来的那一行让 `.app` 从 2 行变 4 行：`rail / subpanel / statusbar / main`。

2. **窄窗口的一列声明压不住 `:has()` 选择器。**
   修好第 1 点后侧栏确实消失了，但
   `.app:has(.rail.collapsed) { grid-template-columns: 56px minmax(0,1fr) }`
   （0,2,0）与 `.app:has(.rail.collapsed):has(.subpanel)`（0,3,0）依旧比
   窄窗口里的 `.app { grid-template-columns: 1fr }`（0,1,0）优先级高。
   侧栏虽然 `display:none`，网格仍留着一列 **56px 的空槽**，主区白窄一截。

**修复**（`web/src/styles.css`，只动这三处，无其它行为变化）：

| 位置 | 改动 |
|---|---|
| shell 一节 | 把 `.rail{display:none}` 从第 42 行的媒体查询里**移走**，该行只留 `.app` 的列声明 |
| `.rail` 定义之后 | 新增 `@media (max-width:900px){ .rail{display:none} }`，保证它排在 `display:flex` 之后 |
| 窄窗口媒体查询 | 列声明覆盖 `.app` / `.app:has(.rail.collapsed)` / `.app:has(.subpanel)` / 两者兼有，统一成 `minmax(0,1fr)` |

**验证方式（可复算，不靠肉眼）**：用 CDP 起 headless Chrome，遍历
`宽度 × 侧栏展开/折叠 × 面板开/关` 矩阵，读 `getBoundingClientRect()` 与
`getComputedStyle()` 的 `gridTemplateColumns/Rows`、各子元素的 `grid-row` 与
实际 `x/y/w/h`。判据只有两条：**侧栏在 ≤900px 的 `display` 必须是 `none`**，
以及**视觉顺序必须是「主区 → 面板 → 状态栏」且面板宽度等于视口宽度**。

| 宽度 | 修复前（折叠 + 面板打开） | 修复后 |
|---|---|---|
| 1440 | `cols=56px 1384px`，面板落在 `x=56 w=56`（侧栏列竖条） | `cols=56px 1044px 340px`，面板 `x=1100 w=340` |
| 900 | `cols=56px 844px`，`rows` 4 条，顺序 `rail/main/subpanel/statusbar` | `cols=900px`，`rows=708/124/28`，顺序 `main/subpanel/statusbar`，侧栏 `display:none` |

>900px 的全部组合在修复前后**数值完全一致**（1440/1200/1100/901 × 展开/折叠 × 开关），
所以这次改动只影响窄窗口，常规宽度下没有任何视觉变化。

**别把那条媒体查询挪回文件开头**——它和 `.rail` 基础规则的优先级相同，位置就是它的正确性。

## 五、验证环境说明

- 云电脑内存 3.8G，`make build-all`（npm+vite+go）会 OOM；改用 `vite build` +
  `go build` 分步执行，产物一致。
- 模型端点偶发超时（`timeout awaiting response headers`），审批验证因此重试过一轮；
  这不影响确定性测试与 UI 验证结论。
