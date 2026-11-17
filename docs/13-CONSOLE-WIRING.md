# 13 · 控制台接线、主题统一、委派持久化

日期：2026-11-17　基线：`8582852`（main，工作树干净）

本轮解决三类问题，每一类都对应一个**可复现的现状缺陷**，不是"顺便重构"：

1. **文档滞后于代码**：`docs/07`–`docs/10` 仍把浏览器会话、截图管线、项目目录、F12 面板
   列为未完成，而代码里早已实现并有端点。后来者按文档做判断会重复造轮子。
2. **后端有、控制台没接线**：294 条路由里控制台只用到约 40 条。最刺眼的是
   `/api/harness/browser*`、`/api/harness/ready`、`/api/harness/capabilities`、
   `/api/harness/evidence-integrity`、`/api/harness/replay`、`PUT /api/tasks/{id}/project`
   ——这些是产品承诺的一部分（README 写着的"观测点"与"不做假控件"），界面里却一条都不在。
3. **设计上的已知限制**：委派注册表是进程内状态（重启丢审计）、隔离目录无 TTL（长任务累积）。

---

## 一、设计限制的修复（后端）

### 1.1 委派注册表重启后仍可审计

`harness/delegation`：

| 变更 | 位置 |
|---|---|
| 记录 journal 到 `<delegations>/<taskID>/delegations.jsonl`（**每记录一行追加**，不是重写整个文件） | `delegation.go` `persistLocked` |
| `Restore()` 从 journal 重建：同一 id 取最后一版；**torn 行（崩溃写一半）只跳过该行**，不整体失败 | `delegation.go` |
| 重启前仍在 `running` 的记录**一律改为 `failed`** 并写明原因——没有进程可以再完成它，保持 `running` 就是让面板永远撒谎 | `delegation.go` `Restore` |
| `Global.For()` 首次创建注册表时自动 `Restore()`（文件 IO 在全局锁**之外**，不阻塞别的任务） | `delegation.go` |
| 序号从 journal 里恢复的最大 id 继续，新 id 不会与历史冲突 | `delegation.go` |

**语义边界（写清楚，别指望它有更多）**：恢复的是**审计轨迹**，不是可执行状态。
重启后的记录没有 cancel 函数，"停止"按钮对历史委派无效（`Kill` 返回 false）。这是刻意的：
跨进程委派状态需要另一个 ADR，ADR-0005 明确不做跨会话编排。

### 1.2 隔离目录有 TTL

| 变更 | 位置 |
|---|---|
| `Registry.Sweep(now, olderThan)`：只回收**终态**委派且超过窗口的隔离目录 | `delegation.go` |
| 结果正文（内容寻址）**永不删除**——它是证据；只删 `<outputDir>/<id>/` 这个 scratch 目录 | `delegation.go` |
| 记录保留并标记 `reclaimed=true`，面板仍能看到"发生过什么，scratch 已回收" | `Record.Reclaimed` |
| **路径守卫**：`WorkDir` 必须在委派存储目录之内，否则跳过——防止一条脏记录把回收器指向任务工作区或 `/` | `delegation.go` |
| 服务端每小时跑一次，窗口 `harness_delegation_retention_hours`（默认 72，`0` 关闭） | `server/harness_delegations.go` `startDelegationGC` |
| 新增 `db.GetInt/SetInt`（此前只有 GetBool，整数设置无处可读） | `db/settings.go` |

### 1.3 顺手修掉的一个真实缺陷

`/api/harness/ready` 的浏览器项查的是 `SELECT count(*) FROM mcp_tools_cache WHERE server='browser'`
——`mcp_tools_cache` **没有 `server` 列**（键是 `server_id`）。错误被 `_ =` 丢掉，于是这一项
**永远报 0**，就绪页对操作者说"浏览器 MCP 尚未连通"，而实际上 25 个工具早就缓存好了。

这与 `server/harness_browser.go` 里此前修过的是同一类错误（那里的注释已经写明）。已改为 join：

```
实测（本机）：修前 "browser MCP 已启用但尚未连通（工具列表为空）"
              修后 "connected · 25 个工具"   ← 与 mcp_tools_cache 的真实行数一致
```

---

## 二、控制台接线（前端）

所有新面板都遵守同一条规则（ADR-0005）：**界面只呈现后端真实能力，没有的能力禁用并写明原因**。
三个新面板在"没有数据"与"没有能力"之间严格区分——这两种情况此前被混成同一句"不可用"。

### 2.1 右栏改成互斥的单一面板

原来 `subOpen` / `benchOpen` 是两个布尔量，每个按钮的 `onClick` 里手动关掉另一个。
新增两个面板就会变成四个布尔量与六处互相关闭。

现在是一张表：`aside ∈ {"", subagents, workbench, browser, system}` + `toggleAside(name)`。
互斥由数据结构保证，不靠每个按钮都记得关别人。

### 2.2 新面板

| 面板 | 端点 | 位置 |
|---|---|---|
| **浏览器**（会话 / F12 / 截图） | `GET /api/harness/browser`、`/browser/sessions`、`POST /browser/restart`、`GET|POST /browser/tabs`、`GET /api/harness/shot/{hash}` | `web/src/Browser.jsx` |
| **系统**（就绪 / 能力 / 回放 / 取证） | `GET /api/harness/ready`、`/capabilities`、`/replay`、`/evidence-integrity` | `web/src/System.jsx` |
| **工作台 → 项目** | `PUT /api/tasks/{id}/project` | `web/src/Workbench.jsx` `ProjectPane` |

要点：

- **F12 页**渲染 `console_errors`（按 `error`/`warn` 分级）与最近 `mcp__browser__*` /
  `mcp__chrome-devtools__*` 动作，并显示"另有 N 条无法结构化解析"。
- **network 面板不做**：后端没有请求/响应表。界面直接写"完整 network 面板不在这个版本里"，
  而不是画一个永远空着的表格（这正是 ADR-0005 禁止的"假控件"）。
- **截图页**只认一个契约：工具结果摘要里的 `[thumb] <sha256>`。没有截图时说"还没有截图"，
  不显示破图。
- **就绪页**四项（pg / llm / skills / browser_mcp）各自带 `hint`，顺序固定且有含义
  （前一项失败时后一项的值都建立在其上）。实测这台机器同时出现"pg ok / llm idle /
  browser ok"，界面分别说明，没有合并成"引擎有问题"。

### 2.3 顺带修掉的两个显示缺陷

**(a) 错误消息把原始 JSON 显示给用户。** 所有面板此前都是
`throw new Error((await r.text()).slice(0, 160))`，而服务端统一返回 `{"error":"..."}`，
于是界面显示的是 `{"error":"目录不存在或不是目录: /tmp/x"}`——带引号、花括号、转义。
现在统一走 `lib/auth.js` 的 `errorText(r)`：解析信封取那句话，解析不出来才退回原文
（那种情况说明返回的真不是 JSON，例如反代 HTML 错误页，原文才有排查价值）。
本次替换了 20 处调用点。

```
实测（本机）：修前 UI 显示  {"error":"目录不存在或不是目录: /tmp/nope-12345"}
              修后 UI 显示  目录不存在或不是目录: /tmp/nope-12345
```

**(b) 项目面板把"输入框里的值"当成"已生效的根目录"。** 两者原来是同一份 state，
所以输入一个非法路径点「绑定」失败后，"当前根目录"显示的是那个**没有被接受**的路径。
现在 `draft`（输入）与 `projectDir`（事实）分开，失败只改 draft。

```
实测（本机）：绑定 /tmp/nope-12345 失败后
              当前根目录 仍显示 /tmp/projtest  ← 事实
              输入框     保留 /tmp/nope-12345  ← 待改
```

### 2.4 接线覆盖率

| | 修前 | 修后 |
|---|---|---|
| 控制台用到的 `/api/harness/*` 端点 | 9 | 16 |
| 右栏面板 | 2 | 4（+ 工作台的"项目"页） |

仍未接线的（**明确列出**，不是遗漏）：`/api/harness/permission*`（权限替换/授权，属危险操作，
需要单独设计确认流程）、`/api/harness/consistency`（双跑对比，CLI 门禁已在用，
控制台需要"选两个任务"的交互，超出本轮）、`/api/harness/scenarios`（场景集只读元信息）、
`/api/harness/interventions`（干预索引，S2 原语的只读视图）。

---

## 三、把第三方设计系统换成仓库自己的 CLAUDE 主题

### 3.1 结论：CLAUDE 主题**已经存在**，位置是 `web/src/tokens.css`

不是"缺失"，而是**被并存的第三方设计系统稀释**。这份令牌是本仓库从参考产品
（`refs/dsh-claude-style`，即 Claude Code Desktop 主题）的截图逐像素采样得出的：

- 五种子色：`--c-accent: #d97757`（clay）、`--c-canvas: #fcfcfb`（**不是纯白**）、
  `--c-text: #141413`（**不是纯黑**）、`--c-link`、`--c-code`；
- 一条共用色阶；浅/深两套（`--dark` 由 `:root[data-theme="dark"]` 切换，`main.jsx` 在首帧前设置）；
- 深色边缘画成"高光"而不是勾边；分隔线比最浅边框低一档。

`web/src/ui.jsx` 是配套的手写原语层（Button/Select/Dialog/…），**只读令牌，不定义任何颜色、
圆角或间距**。

### 3.2 修前的问题：主题之外还有三套设计系统在同一个界面里出图

| 库 | 它自带什么 | 与本仓库令牌的冲突 |
|---|---|---|
| ~~Radix Themes~~ | 整套调色板、半径、1px 边框、控件几何 | 每一项都要逐条 CSS 打回去（上一版 `ui.jsx` 的注释里已是这个抱怨） |
| `sonner`（toast） | 自己的容器、圆角、阴影、字体、配色 | 提示条永远比界面"另一套风格" |
| `cmdk`（命令面板） | `[cmdk-item]` 选中态、列表内边距 | 选中行颜色由它决定，不跟 `--c-selected` |
| `recharts`（图表） | 自带渲染/动画/tooltip 门户/默认配色 | 为一条折线引入约 200 KB，tooltip 还得重写样式 |
| `highlight.js` 主题 | GitHub 配色（写死的浅色） | 深色模式下代码块仍是浅底 |

Radix 在更早的提交里已移除；本轮清掉剩下四处。

### 3.3 替换（都用 `tokens.css` 的令牌，样式集中在 `styles.css`）

| 新文件 | 替换掉 | 说明 |
|---|---|---|
| `web/src/toast.jsx` | `sonner` | 同样的 `toast.success/error` 调用形状（**调用点未改**）；错误常驻直到手动关闭，成功自动消失；`role="status"/"alert"` |
| `web/src/Palette.jsx` | `cmdk` | 自实现 ↑↓/Enter/Esc/过滤；listbox 模式（`aria-activedescendant`）。过滤用大小写不敏感子串，**不做模糊打分**——错误排序会在用户按方向键时把行换掉 |
| `web/src/Sparkline.jsx` | `recharts` | 内联 SVG：accent 描边 + 令牌渐变填充 + 压缩点竖虚线；无动画，`preserveAspectRatio` 随容器缩放 |
| （删除导入） | `highlight.js` 主题 | 代码块改用 `.md pre`（已读令牌）；不再引入写死浅色的 GitHub 主题 |

`web/package.json` 依赖从 18 个降到 9 个；JS 产物 **1269 KB → 709 KB**，CSS 27.5 KB → 30.9 KB
（多出的 3.4 KB 是新面板与自实现控件的样式）。

剩余依赖逐条说明为什么留：

- `lucide-react` — 纯几何图标，不带设计系统；
- `motion` — 只做过渡/位移，不定义颜色、圆角、阴影；
- `react-markdown` + `remark-gfm` — Markdown 解析，不产生视觉决策；
- `@tanstack/react-virtual` — 虚拟化，无样式；
- `date-fns` — 相对时间格式化。

---

## 四、验证（全部在本机实跑）

### 4.1 后端

```
go build ./...                                  ✅
go vet ./...                                    ✅ 无输出
go test ./agent/... ./harness/... ./config/... ./guard/...   ✅ 14 包全 ok
go test ./harness/delegation/ -run 'Restore|Sweep' -v        ✅ 5 个新用例
```

新增测试（`harness/delegation/retention_test.go`）：

| 用例 | 断言什么 |
|---|---|
| `TestRestoreKeepsTrailAndFailsInterruptedRuns` | 重启后审计完整；中断的 running → failed 且带原因；重复 Restore 不重复；新 id 不撞历史 id |
| `TestRestoreSkipsTornJournalLine` | journal 尾部半行（崩溃写一半）不导致整体失败，完整记录不丢 |
| `TestSweepReclaimsOldDirectoriesAndKeepsRecords` | 过期终态目录被回收、记录保留并标记 reclaimed、**结果正文不动**、新记录不动 |
| `TestSweepNeverTouchesRunning` | 运行中的委派任何年龄都不回收 |
| `TestSweepRefusesPathsOutsideStore` | `WorkDir` 指向存储目录之外时拒绝删除（目录仍在） |

### 4.2 前端

```
cd web && npx vite build     ✅ 3628 modules → index-*.js 709 KB / css 30.9 KB
                             ✅ 产物写入 server/console-app/（go:embed 读它）
```

第三方设计系统残留检查：

```
grep -rn 'radix|sonner|cmdk|recharts|highlight' web/src/*.jsx web/package.json
→ 仅剩注释里说明"为什么移除"的文字，无可执行引用
```

### 4.3 真实端到端（PostgreSQL + 真 MCP，非假数据）

本机起服务（`-addr 127.0.0.1:8791`，独立库 `artex_ui_verify`），用浏览器逐面板核对：

| 项 | 结果 |
|---|---|
| 就绪页四项 | pg `connected` / llm `profiles=0 engine=idle` + 提示 / skills `/tmp/skills · 0 个技能` / browser `connected · 25 个工具` |
| 浏览器会话 | `POST /browser/restart` **1.97s** 拉起 `browser` 与 `chrome-devtools`，两者 `live`，界面显示徽标 live |
| 标签 | `POST /browser/tabs {open}` → `t1`；界面列出，可关闭 |
| F12 页 | console 错误 0 条时显示"还没有抓到"，**不是**空表格；写明 network 不在此版本 |
| 能力页 | 54 项（builtin 51 + mcp 3），可并行 24 / 独占 30；MCP 行显示 `25 tools`/`33 tools` |
| 回放页 | 指纹 `4307b321…`、轮次/条目/拒绝事件计数、工具分布、Markdown 展开 |
| 项目页 | 绑定 `/tmp/projtest` 后文件树以它为根（`a.txt` 标 `A`），基线落 `<data>/baselines/<taskID>` |
| 目录互斥 | 任务 #3 抢占 #1 的目录 → **409** + "已被任务 #1 占用"；#1 释放后 #3 绑定成功 |
| 路径校验 | 相对路径 → "必须是绝对路径"；不存在 → "目录不存在或不是目录: …" |
| 错误信封 | UI 显示纯文本，不再是 `{"error":"…"}` |
| 输入/事实分离 | 绑定失败后"当前根目录"仍显示已生效的 `/tmp/projtest` |
| 改绑成功 | 绑定 `/tmp/projtest2` → 提示「项目目录已绑定」（自有 toast），根目录立即切换 |
| 命令面板（替换 cmdk） | ⌘K 打开；分组渲染；输入 `new chat` 用关键词命中「新建对话」；Esc 关闭 |
| 通知（替换 sonner） | 绑定成功弹出「项目目录已绑定」，带关闭按钮，位置底部居中 |

### 4.4 本轮没做的验证（说清楚）

- **未跑 `make test-db`**（`./db/... ./server/...` 共 419 个用例需要独立测试库并会重建 schema）；
  上面端到端用的是手工验证，不是全量回归。
- **未跑真实 LLM 一轮**：本机无模型端点（`profiles=0`），所以"agent 真的调用浏览器工具并
  产生 `[thumb]` 截图"这条链路只验证到端点与渲染逻辑，没有验证到真实截图内容。
- **未做 network 结构化**（见 §二）。
- **深色主题只做了代码层确认**（`data-theme=dark` 的令牌与 `main.jsx` 的首帧切换逻辑就位），
  端到端截图取证只覆盖浅色。

---

## 五、修改清单

后端：

- `harness/delegation/delegation.go` — journal / Restore / Sweep / SweepAll / Reclaimed
- `harness/delegation/retention_test.go` — 新增 5 个用例
- `server/harness_delegations.go` — `startDelegationGC`、`delegationRetention`
- `server/server.go` — 启动 GC 协程
- `server/harness_wiring.go` — 修 `mcp_tools_cache` 列名缺陷
- `db/settings.go` — 新增 `GetInt` / `SetInt`

前端：

- 新增 `web/src/Browser.jsx`、`web/src/System.jsx`、`web/src/Palette.jsx`、
  `web/src/Sparkline.jsx`、`web/src/toast.jsx`
- `web/src/Workbench.jsx` — 新增「项目」页；draft/事实分离；错误信封
- `web/src/lib/auth.js` — 新增共用 `errorText`
- `web/src/App.jsx` — 右栏改互斥单值；两个新按钮；移除 cmdk/sonner/recharts/highlight 接入
- `web/src/styles.css` — 新面板样式（全部读令牌）
- `web/src/main.jsx` — 注释改为说明自有 Theme 原语
- `web/package.json` — 移除 4 个带设计系统的库
- `server/console-app/` — 重新构建的产物（`go:embed` 需要它，必须一并提交）

文档：

- `docs/07`、`08`、`09`、`10` — 标注代码现状，保留原文作为当时记录
- 本文件