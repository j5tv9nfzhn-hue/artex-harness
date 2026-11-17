# 15 · 功能清单

> 每一项都写清楚**入口在哪、点了发生什么、落到哪个端点**。凡"后端有但界面没有"的，
> 明确标出来，不写成好像已经能点。
>
> 约定：入口路径以控制台为准（`http://<host>:8787/app/`）。`⌘` 在 Windows/Linux 上按 `Ctrl`。
> 所有 API 都需要 `Authorization: Bearer <token>`（登录后自动带上）。
>
> **配套**：模块划分 / 依赖 / 数据流 → [`16-ARCHITECTURE-DETAIL.md`](16-ARCHITECTURE-DETAIL.md)；
> 参考界面截图逐项对照 → [`17-REFERENCE-UI-MAPPING.md`](17-REFERENCE-UI-MAPPING.md)；
> **每一节的真实截图** → [`screenshots/harness/`](screenshots/harness/)（索引见
> [`screenshots/README.md`](screenshots/README.md)，可按脚本复算）。

---

## 一、登录与初始化

| 功能 | 入口 | 行为 |
|---|---|---|
| 首次设置管理密码 | 打开 `/app/` 且服务端无密码 | 显示「首次设置」表单；密码 ≥8 位并二次确认；`POST /api/auth/init` 成功后直接进入控制台。用户名固定 `ARTEX`，不可改。 |
| 登录 | `/app/` 登录门 | `POST /api/auth/login` → 拿 JWT 存 `localStorage`。错误密码显示服务端原话（"用户名或密码错误"），不是原始 JSON。 |
| 数据库不可用时的表现 | 同上 | 服务端回 503 而不是 `initialized:false`——界面显示"无法读取登录状态 + 原因"，**不会**谎称"还没设密码"而让人去覆盖已有密码。 |
| 令牌失效自动回登录 | 任意请求返回 401 | 全局事件 `artex:unauthorized` → 卸载控制台、回到登录门。不在每个面板各报一遍。 |
| 改密码 | 设置 → 运行环境 | `POST /api/auth/change-password`。 |

## 二、两条产品线（对话 vs 运行）

**这是最容易混的一处，界面与数据都分开。**

| | 对话（conversation） | 运行（task） |
|---|---|---|
| 入口 | 侧栏「新对话」按钮 / `⌘N` / 命令面板 | 侧栏「新建任务」/ 顶栏「新建任务」/ 命令面板 |
| 必需字段 | 无 | **goal 必填** |
| 何时开跑 | 发出第一条消息 | 创建即排队，`harness_mode=true` 时进入单 agent 循环 |
| 数据表 | `conversations` / `conversation_activities` | `tasks` / `activity` |
| 事件通道 | 轮询 `GET /api/conversations/{id}/messages?since=N` | 订阅 `GET /api/agui/agent?task=N&since=<seq>&follow=1`（SSE） |
| 预算 / 目标判定 | 无 | 有（墙钟预算、步数上限、goals） |
| 子代理 / 工作台 / 浏览器 / 系统四个面板 | 不可用（顶栏按钮隐藏） | 可用 |

## 三、左栏（会话与工作区）

| 功能 | 入口 | 行为 |
|---|---|---|
| 新建对话 | 侧栏顶部「新对话」 | 创建空对话并选中；不发消息就不跑。`POST /api/conversations`。 |
| 对话列表 | 侧栏「对话」分组 | 列出全部已存在的对话（`GET /api/conversations`），点击打开、`Enter` 同效；行尾显示相对时间。**这一条是修出来的**：此前 `convs` 只用于算高亮、从不进列表，标签页又是内存态，刷新后已有对话在界面上再也打不开——接口一直有返回，是界面漏了。 |
| 运行列表 | 侧栏「工作区」分组（按今天/昨天/本周/更早 + 置顶分组） | 每行 = 一个运行；旧管线任务单独成组，不混在 HARNESS 列表里。 |
| 新建运行 | 侧栏顶部「新建任务」（或顶栏） | 打开对话框：目标（必填）、描述、「HARNESS 单 agent」开关（默认开）。`POST /api/tasks`。 |
| 会话列表 | 侧栏「工作区」分组 | 每行显示状态点、标题（`name → goal → description → task#id` 依次回落）、相对时间（`date-fns` zhCN）。列表用虚拟化，上百条只渲染可见十几行。 |
| 状态分组 | 侧栏分组标题 | 按运行状态分组；折叠侧栏时分组标题与行内文字隐藏，只留图标。 |
| 筛选 | 侧栏「筛选」按钮 | 过滤列表。 |
| 重命名 / 置顶 / 删除 | 行内右键或行尾菜单 | `PATCH /api/tasks/{id}`、`DELETE /api/tasks/{id}`（删除有屏障：先收尾在跑的轮次）。 |
| 待审批提示 | 侧栏行尾 + 顶栏盾牌徽标 | 轮询 `GET /api/intercept/pending`；有等待项时显示计数，点开可就地允许/拒绝。 |
| 折叠 / 展开侧栏 | 侧栏顶部折叠按钮 | 折叠到 56px（只留图标）。**宽度 ≤900px 时侧栏整个隐藏**，见 §九。 |

## 四、顶栏（一次运行的头部）

| 元件 | 行为 |
|---|---|
| 标题 | 当前运行的 `name / goal / description`；对话显示对话标题。 |
| 状态徽标 | `created / running / done / failed / paused` 等，带颜色与文字（颜色不是唯一信号）。 |
| 视图页签：**对话 / 轨迹 / 检查点 / 用量** | 切换中栏内容。对话=事件流；轨迹=按轮次的行模型表；检查点=每轮快照与回滚；用量=上下文预算与 token 曲线。**对话没有 task 时后三个禁用并说明原因**，不假装能点。 |
| 搜索 | 在事件流正文里检索（`GET /api/harness/search`）。 |
| 子代理 | 右栏切到子代理面板（见 §六）。 |
| 工作台 | 右栏切到工作台（见 §七）。 |
| 浏览器 | 右栏切到浏览器面板（见 §八）。 |
| 系统 | 右栏切到系统面板（见 §八）。 |
| 暂停 / 恢复 | `POST /api/tasks/{id}/control {action:pause|resume}`。暂停会收尾在跑的轮次。 |
| 审批徽标 | 等待中的审批数量，点开就地处理。 |
| 四个右栏按钮互斥 | 任一时刻最多一个面板；再点同一个关闭。由单一状态保证，不靠每个按钮记得关别人。 |

## 五、中栏与输入区

| 功能 | 行为 |
|---|---|
| 轮次时间线 | 每轮一张块：`rev` / 类型 / 摘要 / 步数 · 错误 · 令牌。长列表虚拟化，几千条事件不卡。 |
| 思考块 | 单独标注（不混进正文）。 |
| 工具调用 ↔ 结果 | 按 `tool_use_id` 配对成**一张卡**（名字/参数/状态/输出），不是两行互不相干。 |
| 连续同类工具合并 | 显示「Bash × 6」，展开逐条。 |
| 正文取自 `detail` | `activity.summary` 只是首行索引（200/400 字截断），展示必须取 `detail`——否则用户看到的是被砍掉第一行之后的内容。这是 `ARCHITECTURE.md` 里写明的不变量。 |
| 断线续传 | SSE `?since=<seq>` 断点续传 + 15s 心跳；重连不重放、不丢事件。 |
| Markdown | `react-markdown` + `remark-gfm`；代码块带复制按钮。 |
| 就地审批卡片 | AG-UI `INTERCEPT_REQUEST` → 事件流内卡片，允许/拒绝直接调 `POST /api/intercept/pending/{id}/decide`。前端词汇 `allow/deny` 映射到后端 `allowed/denied`（写错就是永远 400）。 |
| 输入框 | 占位随状态变：「今天要我做什么？」/「先新建一个对话，或在左侧选择会话」。`Enter` 发送。 |
| 模型选择 | 任务级覆盖，不改全局配置。`PUT /api/tasks/{id}/llm`。未配置时下拉禁用并写「未配置模型」。 |
| 思考深度 | 只作用于本会话，`跟随模型` 为默认。 |
| 权限模式 | **只读事实**：后端没有任务级权限开关，控件禁用并写「由平台管控规则与审批门决定」——不做假开关。 |
| 插入文件 | `@` 引用工作区文件。 |
| 上下文占用 | 输入框右侧实时百分比 + 已用/窗口；≥70% 琥珀、≥90% 红。取最近一条 usage，不累加（累加会高估）。 |
| 生成速度 | 本轮流式输出字符数 / 已用时长。 |
| 发送 / 停止 | `POST /api/tasks/{id}/say`、`POST /api/conversations/{id}/messages`；停止走 `POST /api/tasks/{id}/chat/stop`。 |
| 跳到最新 | 向上翻阅时不打扰；出现「↓ 跳到最新」按钮。 |

### 五之二、用量页（「对话 / 轨迹 / 检查点 / 用量」里的第四页）

| 数值 | 来源 | 口径 |
|---|---|---|
| 每轮输入 token 曲线 | `GET /api/harness/budget/{id}` → `series` | 每条 usage 活动的输入 token 按 id 排序；同一轮内回落 ≥20% 处画竖虚线（压缩点） |
| **压缩次数** | 优先 `compactions_observed.total`（SDK 真实压缩事件），否则 `compactions`（同轮内回落推断） | 推断出来的值把「推断」两字直接写进单位：`1 次（推断）`，不冒充真实事件。**这是修出来的**：原先写 `observed.total ?? compactions`，而 0 不是 nullish——只要接口带了 `compactions_observed`（它总带），推断值就永远显示不出来，界面恒报 0 次，与旁边那条按推断画的竖虚线自相矛盾 |
| 占用 % | `pct` | 最近一条 usage 的输入 token ÷ 窗口，**不累加**（累加会高估） |
| 墙钟预算 | `run_budget_seconds` | `HARNESS_RUN_BUDGET`，默认 1200 秒 |
| 日历热力格 | `GET /api/tokens/daily` | 按 **UTC 日历日**聚合全部任务的 `kind='result'` 活动 token（不是按任务分） |

截图：[`harness-04-usage-light.png`](screenshots/harness/harness-04-usage-light.png)

## 六、右栏 ①：子代理

| 功能 | 行为 |
|---|---|
| 委派列表 | `GET /api/harness/workers/{id}`（去重后的委派）；状态 `运行中/已完成/失败`，带耗时与「后台」徽标。 |
| 点进去看明细 | `GET /api/harness/subagent/{agent}?task=N`，读磁盘 sidechain：该子代理自己的每一步（工具入参、输出、思考）。 |
| 停止后台委派 | `POST /api/harness/delegations/{id}/{did}/stop`。只对**运行中**的委派有效；重启后的历史委派不能再停（返回 400 并说明）。 |
| 委派结果正文 | `GET /api/harness/delegations/{id}/{did}`，内容寻址的结果文件。 |
| 为什么子代理不会被父轮次杀掉 | 子代理用 `context.WithoutCancel(parent)` + 自有 10 分钟预算（`HARNESS_SUBAGENT_TIMEOUT` 可调）。父轮次结束只拒绝**新开**子代理。 |
| 为什么不能递归 | 子代理定义的工具白名单不含 `Agent` 工具。 |

## 七、右栏 ②：工作台（项目 / 文件 / DIFF / 终端）

| 页签 | 功能 | 端点 |
|---|---|---|
| **项目** | 查看当前根目录；改绑到绝对路径；改回任务自有目录 | `PUT /api/tasks/{id}/project` |
| **文件** | 文件树 + 变更标记（A/M/D）+ 大小；点击预览 | `GET /api/harness/files/{id}`、`GET /api/harness/file/{id}?path=` |
| **DIFF** | 与基线对比的 unified diff，按文件列出增删行数 | `GET /api/harness/diff/{id}[?path=]` |
| **终端** | 在**任务工作区**执行 `bash -lc`，60s 超时（可调 ≤300s），输出 64 KB 截断；可勾选「交给 agent」把输出注入会话 | `POST /api/tasks/{id}/exec` |

要点：

- **项目目录互斥**：一个目录同一时刻只归一个活任务。冲突返回 409 并**点名占用者**（"已被任务 #1 占用"）。前端不复制这套判断，只显示服务端原话。
- **基线位置**：绑定项目目录后基线不写进你的目录，改放 `<data>/baselines/<taskID>`，所以 DIFF 里不会多出隐藏文件。
- **路径校验**：必须绝对路径、必须存在且是目录、不能含 `..`。
- **没有基线时**：DIFF 明确显示 `baseline=false` 并说明原因，不假装"没有变更"。
- **文件预览**：1 MiB 上限、二进制拒绝、`safeJoin` 防路径穿越。超大/二进制不内联，直接说明。
- **人执行的命令**以 `worker=human`、`tool=human.sh` 写两条活动，事件流与回放里能区分"人做的"。
- **检查点目录** `.harness-checkpoints` 在 sidecar 名单里，不出现在文件树/DIFF/基线。

## 八、右栏 ③④：浏览器 与 系统

### 浏览器

| 页签 | 功能 | 端点 |
|---|---|---|
| 会话 | 两个 MCP 服务器状态（Playwright / DevTools）、重建会话（返回耗时）、标签开/关 | `GET /api/harness/browser/sessions`、`POST …/restart`、`GET|POST …/tabs` |
| F12 | DevTools 的 console 错误（按 error/warn 分级）+ 最近 `mcp__browser__*` / `mcp__chrome-devtools__*` 动作 | `GET /api/harness/browser?task=N` |
| 截图 | 最近一张浏览器截图，点击看原图 | `GET /api/harness/shot/{hash}` |

- **一任务一会话**：Playwright 与 chrome-devtools 共用同一会话条目，所以 F12 连的是同一个浏览器上下文。
- **崩溃恢复**：某服务器失败 → `MarkBroken` → 下次 `Ensure` 只重建那一个，互不牵连。
- **回收**：会话空闲 30 分钟回收；截图 TTL 24h + 上限 2000 张。
- **诚实的边界**：**network 面板没有**。后端只把 chrome-devtools 输出按 console 级别解析，没有请求/响应表。界面直接写明"不在此版本"，**不画空表格**。
- 截图契约只有一条：工具结果摘要里的 `[thumb] <sha256>`。没有截图就说"还没有截图"，不显示破图。

### 系统

| 页签 | 功能 | 端点 |
|---|---|---|
| 就绪 | 数据库 / 模型 / 技能 / 浏览器 MCP **四项分别**给 ok + detail + hint | `GET /api/harness/ready` |
| 能力 | 全部供给面：`surface`、`exec`（可执行）、`concurrency_safe`（并行安全）、`deferred`（延迟解锁）、启用状态 | `GET /api/harness/capabilities` |
| 回放 | 指纹、轮次/条目/拒绝事件计数、工具分布、Markdown 原文展开 | `GET /api/harness/replay?task=N` |
| 取证 | 有绑定 / 无绑定的发现逐条列出（**只报告，不阻止**提交或导出） | `GET /api/harness/evidence-integrity?task=N` |

- 四项独立失败：缺模型="引擎 idle"，缺技能只是少一层领域知识，缺浏览器只影响浏览器工具。**不把四种原因合并成"卡住了"**。
- 能力里的 `exec` / `concurrency_safe` 是**事实描述，不是风险评分**。判据留在操作者手里（ADR-0005）。

## 九、布局与响应式（含刚修的两个缺陷）

| 宽度 | 行为 |
|---|---|
| >1200px | 侧栏 280（折叠 56）+ 主区 + 右栏 340 |
| 901–1200px | 右栏收窄到 300，主区保住可读宽度 |
| ≤900px | 侧栏整个隐藏；打开右栏时它变成**底部横带**（`max-height:46vh`），顺序是「主区 → 右栏 → 状态栏」 |

**≤900px 曾有两条真实缺陷，已修（见 `docs/12-SPRINT4-WORKBENCH.md` §四之二）**：

1. 隐藏侧栏的媒体查询写在 `.rail` 定义之前 → 同优先级下被 `display:flex` 压掉，侧栏在窄窗口仍占满一整行，把 `.app` 撑成四条横带。
2. 窄窗口的一列声明压不住 `:has(.rail.collapsed)` → 网格留着 56px 空槽。

用户描述为"透视/倾斜"的观感就是这个：面板被压在 56px 列里，文字逐字竖排换行。
`>900px` 全部组合修复前后数值完全一致。

## 十、设置（七个子页）

入口：顶栏右侧「设置」。是整页导航，不是弹窗、不是堆叠卡片。

| 子页 | 能做什么 |
|---|---|
| 供应商与模型 | 增删改 LLM 端点（格式/密钥/模型/上下文窗口）；发现模型清单；激活某个配置作为默认；测试连通性。**仓库不带任何预设供应商**。 |
| 插件 · MCP | 增删 MCP 服务器（stdio/http/sse）；刷新工具列表；查看已缓存工具数；启用/停用。 |
| 技能 | 列出 `skills/` 下的技能目录；上传 zip；查看文件树、读/写/删单个文件；未命中（想调但不存在）的技能名统计。 |
| 权限与审批 | 审批规则（`allow`/`ask`/`deny`）增删改与开关；LLM judge 配置（失败动作、等待超时动作）。**审批门是唯一否决点**，前端只是它的入口。 |
| 会话日志 | `GET /api/llm/records`：每次 LLM 调用的原文记录，可按任务/模型筛选与删除。 |
| 运行环境 | 工作区根目录、python 探测、Git Bash 探测（Windows）、shell 选择、改密码。 |
| 外观 | 亮/暗/跟随系统（`artex.appearance`，首帧前应用，避免先亮后翻）。 |

## 十一、命令面板与快捷键

| 快捷键 | 行为 |
|---|---|
| `⌘K` / `Ctrl+K` | 打开命令面板。↑↓ 选择、`Enter` 打开、`Esc` 关闭。 |
| `⌘N` / `Ctrl+N` | 新建对话。 |
| `Enter`（输入框） | 发送（`Shift+Enter` 换行；输入法组字中不触发发送）。 |
| `Enter`（终端/项目目录/标签地址） | 执行或提交。 |
| `Esc` | 关闭命令面板 / 对话框 / 下拉菜单 / 提示条。 |
| `↑` `↓`（命令面板） | 在可选行之间移动高亮。 |

> 侧栏折叠、打开终端等**目前只有按钮入口，没有键盘快捷键**——不要以为有。
> 新增快捷键前先在这个表里登记。

命令面板的分组：**操作**（新建对话 / 新建运行）、**任务**（每个运行，显示 `#id · status`）。过滤是大小写不敏感子串，**不做模糊打分**——错误排序会在用户按方向键时把行换掉。关键词可命中标签之外的词（如 `new chat` 命中「新建对话」）。

## 十二、浏览器的存储与运行期

| 项 | 位置 |
|---|---|
| 工作区（任务自有） | `<data>/tasks/<id>` |
| 项目目录（改绑后） | 用户指定目录；基线改放 `<data>/baselines/<taskID>` |
| transcript | `<data>/transcripts/<session>.jsonl`，子代理在 `<data>/transcripts/<session>/subagents` |
| 浏览器截图 | `<data>/browser/<sha256>.png`（内容寻址，同图只存一份） |
| 委派结果 / 隔离目录 / journal | `<data>/delegations/<taskID>/<did>/`、`delegations.jsonl` |
| 检查点 | `<data>/checkpoints/<taskID>` |
| 流量证据 blob | `<data>/evidence/blobs/<hash前2位>/<hash>.bin` |
| 流量记录（mitmproxy） | `<data>/traffic`（`-proxy` 端口，默认 `127.0.0.1:8788`，`-proxy ""` 关闭） |

**进程内状态**（重启即失，已知边界）：

- 委派注册表：记录落 journal，**重启后仍可审计**（历史委派变 `failed` 并写明原因，不能再 stop）。
- 浏览器会话：纯内存，重启即失；下次工具调用重建。
- 事件流游标：客户端持有 `since`，服务端不存。

## 十三、明确没有的功能（不写"即将支持"）

| 没有 | 原因 |
|---|---|
| F12 的 network 结构化面板 | 后端未建模请求/响应表 |
| DIFF 的行级评论、逐 hunk stage | 需要 VCS 语义；本项目 DIFF 是与基线对比，不引入 git 依赖 |
| 终端多标签 / 分屏 | 一任务一终端足够；多标签增加会话生命周期管理面 |
| 语法高亮文件预览 | 只做纯文本预读，避免新增前端依赖 |
| 任务级权限开关 | 后端没有这个能力；界面禁用并写明 |
| 跨进程停止委派 | 需要另一个 ADR；ADR-0005 不做跨会话编排 |
| Agent Teams / leader-member 邮箱 | 与 ADR-0005「单 agent 决策权」冲突 |

## 十四、后端已有、控制台尚未接线的端点

诚实列出（不是遗漏，是排期）：

| 端点 | 为什么还没接 |
|---|---|
| `GET/PUT/POST /api/harness/permission*` | 权限替换/授权属危险操作，需要单独设计确认流程 |
| `POST /api/harness/consistency` | 双跑对比，CLI 门禁已在用；控制台需要"选两个任务"的交互 |
| `GET /api/harness/scenarios` | 场景集只读元信息，单独一页价值不大 |
| `GET /api/harness/interventions` | S2 干预原语的只读索引，轨迹页已能覆盖日常使用 |
| `/api/exploration/*` 大部分 | 旧 planner/worker 管线的视图；控制台默认不展示那条产品线 |

调用示例（都需要 Bearer token）：

```bash
# 就绪自检
curl -s -H "Authorization: Bearer $TOK" localhost:8787/api/harness/ready | jq .
# 能力清单
curl -s -H "Authorization: Bearer $TOK" localhost:8787/api/harness/capabilities | jq '.summary'
# 回放指纹
curl -s -H "Authorization: Bearer $TOK" "localhost:8787/api/harness/replay?task=1" | jq '{hash,fingerprint}'
# 绑定项目目录
curl -s -X PUT localhost:8787/api/tasks/1/project -H "Authorization: Bearer $TOK" \
  -H 'Content-Type: application/json' -d '{"project_dir":"/home/user/project"}'
```