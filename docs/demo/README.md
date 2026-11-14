# 控制台静态演示（无需后端）

`harness-console.html` 是**单文件、零依赖**的静态页面，用于在没有服务器、没有 LLM 端点、
没有 Node 的情况下查验控制台。它不是"另画一套皮"：`build.py` 把
`server/console/app.css` 与 `app.js` **原样内联**，只用一个 mock 层替换 `fetch()` 与
`EventSource` —— 所以你看到的就是生产渲染路径，演示里的缺陷就是产品的缺陷。

- **无外部请求**：CSS/JS 全部内联，没有 `<script src>`、没有 CDN、没有字体外链。
- **无需后端**：直接双击打开即可（`file://`）；界面上有一条「静态演示 · 数据为模拟」标签。
- **数据是写死的**：4 个任务、7 个轮次、6 条意图、18 项能力、3 条漏洞结论。
  这不是真实运行数据，**不能**用来推断任何实际扫描结果。
- **可交互**：纠偏会真的追加一条 `system` 轮次；中断/暂停/优先级/顺序/回放/评测都有本地反馈
  （只改内存，不发请求）。

## 重新生成

```bash
python3 docs/demo/build.py      # 内联真实 CSS/JS + mock → harness-console.html
```

改了 `server/console/*` 之后跑一次即可同步，不要手改生成的 HTML。
设计依据见 [../design/DESIGN.md](../design/DESIGN.md)。

## 打开方式

```bash
# 方式一：浏览器直接打开（最快）
xdg-open docs/demo/harness-console.html      # Linux
open     docs/demo/harness-console.html      # macOS
start    docs\demo\harness-console.html      # Windows

# 方式二：本机静态服务（避免个别浏览器对 file:// 的限制）
cd docs/demo && python3 -m http.server 8890
# → http://127.0.0.1:8890/harness-console.html
```

地址栏形式：`file:///home/user/Desktop/harness-console.html`（云电脑里已放一份到桌面）。

## 五个视图与可交互点

| 视图 | 内容 | 可交互 |
|---|---|---|
| **轨迹** | 轮次时间线：规划 / work / 人工纠偏 / 平台拦截 / 收尾。每轮带 `rev`、步数、错误数与令牌；工具调用与结果配对成一张卡并显示真实输出；拦截单独成琥珀色并附平台话术 | 点 `#101` 等意图锚点 → 设为纠偏目标；composer 发送纠偏 → 追加 `system` 轮次；中断；暂停/恢复；重新规划 |
| **Plan** | frontier 四条意图，按 worker 领取顺序 | ↑↓ 调序 → 「保存顺序」（演示翻译成严格递减 priority）；行内改优先级 |
| **Replay** | 由轮次重建的确定性 Markdown + 指纹/拦截计数 | 填 `from/to` 区间重建；复制 Markdown |
| **Eval** | 一致性判定表：4 条硬检查 + 3 条软检查 | 「查看失败示例」→ 切到红线命中版（话术缺失 / 拦截放行 / 结构变化） |
| **检查器** | 上下文（目标进度 / 令牌构成 / 运行状态 / 漏洞分档）、证据、能力、策略（审批模式开关） | 切换分页；模式开关可点 |

## 真实控制台 vs 本演示

| | 本文件 | 真实控制台 |
|---|---|---|
| 数据 | 写死 | `GET /api/harness/{stream,replay,capabilities,evidence-integrity}` |
| 更新 | 点击后本地重绘 | SSE `/api/harness/stream`（`event: harness`，`id:` = activity id，可断点续传） |
| 干预 | 只改内存 | `POST /api/tasks/{id}/intents/{iid}/steer`、`PATCH …/intents/{iid}`、`POST …/frontier/order` 等 |
| 启动 | 双击 | `./start.sh` → `http://localhost:8787/` → 302 `/console/` |

真实控制台的资源在 [`server/console/`](../../server/console/)（`index.html` / `app.css` / `app.js`），
通过 `//go:embed all:console` 无条件内嵌进单二进制，所以裸机 `go build` 出来的程序自带 UI。
