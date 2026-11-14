# 控制台静态演示（无需后端）

`harness-console.html` 是**单文件、零依赖**的静态页面，用模拟数据复刻
`artex-harness` 控制台（`server/console/`）的完整界面，用于在没有服务器、没有 LLM 端点的
情况下查验前端效果。

- **无外部请求**：CSS/JS 全部内联，没有 `<script src>`、没有 `fetch`、没有 CDN、没有字体外链。
- **无需后端**：直接双击打开即可（`file://`）。
- **数据是写死的**：4 个任务、7 个轮次、4 条意图、18 项能力、3 条漏洞结论。
  这不是真实运行数据，**不能**用来推断任何实际扫描结果。

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
| **Run** | 按 `rev` 分组的轮次：规划 / work / 人工纠偏 / 平台拦截 / 收尾。拦截条目用真实话术（`【ARTEX 平台管控·非目标防御】…`） | 点 `#101` 等意图锚点 → 右侧干预坞；发送纠偏 → 追加一条 `system` 轮次；停止 work；编辑摘要/优先级；暂停/恢复；立即重规划 |
| **Plan** | frontier 四条意图，按 worker 领取顺序 | ↑↓ 调序 → 「保存顺序」（演示翻译成严格递减 priority）；行内改优先级 |
| **Replay** | 由轮次重建的确定性 Markdown + 指纹/拦截计数 | 填 `from/to` 区间重建；复制 Markdown |
| **Eval** | 一致性判定表：4 条硬检查 + 3 条软检查 | 「查看失败示例」→ 切到红线命中版（话术缺失 / 拦截放行 / 结构变化） |
| **平台** | 能力注册表（5 类供给面，含"可执行"标记）+ 证据完整性（有/无绑定） | 只读；刷新 |

## 真实控制台 vs 本演示

| | 本文件 | 真实控制台 |
|---|---|---|
| 数据 | 写死 | `GET /api/harness/{stream,replay,capabilities,evidence-integrity}` |
| 更新 | 点击后本地重绘 | SSE `/api/harness/stream`（`event: harness`，`id:` = activity id，可断点续传） |
| 干预 | 只改内存 | `POST /api/tasks/{id}/intents/{iid}/steer`、`PATCH …/intents/{iid}`、`POST …/frontier/order` 等 |
| 启动 | 双击 | `./start.sh` → `http://localhost:8787/` → 302 `/console/` |

真实控制台的资源在 [`server/console/`](../../server/console/)（`index.html` / `app.css` / `app.js`），
通过 `//go:embed all:console` 无条件内嵌进单二进制，所以裸机 `go build` 出来的程序自带 UI。
