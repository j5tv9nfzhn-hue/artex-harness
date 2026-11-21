# 参与开发

先读 [`ARCHITECTURE.md`](ARCHITECTURE.md)（目录职责与不变量）。这里只讲**怎么干活**。

## 环境

| 依赖 | 版本/说明 |
|---|---|
| Go | 见 `go.mod`（`go 1.2x`），`go build ./...` 必须干净 |
| Node.js | 22+（只在构建控制台时需要，`web/` 用 npm） |
| PostgreSQL | 14+；`ARTEX_PG_DSN` 指向它。启动时自动应用 `db/schema.sql` |

启动：

```bash
export ARTEX_PG_DSN="postgres://user:pass@127.0.0.1:5432/artex"
make run                      # 前端用仓库里已构建的产物
```

改控制台后：

```bash
make web && make build        # 重新构建前端 → 打进二进制（npm ci + vite build）
```

## 日常命令

```bash
make            # 列出所有目标
make vet        # 静态检查（提交前必跑）
make test       # 单元测试，不碰数据库
make test-db    # 需要 PG 的测试，必须显式指定测试库
make build-all  # 控制台 + 后端
```

### 关于 `make test-db`（重要）

这些用例会**重建 schema**，等于清空目标库。所以：

- 必须 `ARTEX_TEST_DB=<库名>`，且**必须与 DSN 里的库名一致**，否则测试会主动跳过。
- 守卫在 `db/testmain_test.go` / `agent/testmain_test.go` / `server/testmain_test.go`：
  **先校验、再重建**（顺序反了会连开发库一起清空，这是踩过的坑）。

绝不要拿正在跑服务的库去跑 test-db。

## 提交约定

```
<type>(<scope>): <一句话说明>

<为什么这么改，而不是改了什么——diff 已经说明改了什么>
```

`type`：`feat` / `fix` / `refactor` / `perf` / `docs` / `test` / `chore`。
`scope` 用目录或领域：`server` / `db` / `agent` / `web` / `agui` / `subagent` / `shutdown` …

一条硬规则：**修 bug 的提交要写清触发条件**（什么输入、什么状态会复现），否则下次重构会原样改回去。

## 代码规则

1. **不引入未使用的依赖**；前端优先用已有库（Radix Themes / lucide / motion / cmdk /
   recharts / sonner / react-markdown / @tanstack/react-virtual / date-fns），不要手写组件。
2. **注释解释"为什么"**，不复述"做了什么"。反直觉的地方（阈值、顺序、并发假设）必须写理由。
3. **不做假控件**：后端没有的能力，界面禁用并写明原因。
4. **失败必须可见**：不允许 `catch {}` 静默；每个错误态要有原因 + 重试。
5. **不要提交**：`web/node_modules/`、`data-harness/`、`harness-jwt.key`、`*.log`、编译出的 `artex-harness` 二进制、
   临时脚本与 dump。提交前 `git status` 自查。
6. `server/console-app/` 是**刻意提交**的构建产物（`go:embed` 需要），改了前端就一起提交。

## 加一个接口

1. `db/` 加 store 方法（SQL 只出现在这里）；需要新列就改 `schema.sql`，并**在文件尾部追加**
   `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`（老库要靠它升级）。
2. `server/` 加 handler + `mux.HandleFunc`，返回 DTO（`server/dto.go`），不要把 db 结构体直接吐给前端。
3. 需要给前端的字段写进 DTO；列表接口默认**不带 `detail`**，正文按需懒加载。
4. 加一条测试：至少覆盖"空结果"和"权限不足"两个分支。

## 加一个界面

1. 数据必须来自真实端点；先确认端点存在，没有就先加后端。
2. 长列表用 `@tanstack/react-virtual`；不要整页 `map` 渲染上千行。
3. 任何一次请求失败，都要能显示原因并重试。

## 变更影响面自查

改这几处时请特别小心（都有历史事故）：

| 改动 | 风险 |
|---|---|
| `server/agui.go` 的 since/心跳 | 长任务断线会丢事件 |
| 压缩阈值 / 窗口 | 阈值 > 实际窗口会让每轮都压缩 |
| `agent/harness.go` 的 subagent.Config | 不继承 hooks = 绕过审批门；不设 CanUseTool = 子代理的 Bash 全被拒 |
| 关停路径（`cmd/artex/main.go`） | 不等待收尾 = 把说到一半的回复腰斩 |
| 测试 TestMain 顺序 | 先重建后校验 = 清空开发库 |
