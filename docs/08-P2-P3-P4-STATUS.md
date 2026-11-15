# 08 · 委派实跑、F12 接入、终端与接管 —— 落实状态与已知缺陷

日期：2026-11-15　基线：`88c59bc` → 本次

## 一、委派实跑（P2）

**一句话任务**：`派一个 recon 子代理去探测本机 http://127.0.0.1:8899 的已知端点…`

**结果（实测）**：
- 模型自己选择委派：`tool_use Agent {"agent_type":"recon","prompt":"…"}`（activity 196 / 212）；
- 包装器把委派写进流：`派发子代理 recon：…`（activity 198 / 214）；
- `/api/harness/workers/{id}` 列出该委派（`agent_type=recon`）；
- **首次委派返回 `Error: subagent failed: context canceled`**，随后 agent **自动重试**（activity 212→214）。

**定位（部分）**：`subagent.run` 的 child ctx 继承父 run 的 ctx；父 run 的 `MaxTurns` 用尽时
harness 会取消在飞工具（合成 interrupted 结果），子代理随之被杀。已把 harness agent 的
`max_turns` 从 14 提到 40 作为缓解，但**这不是根治**。

**根治方案（下一步）**：让子代理脱离父 turn 的预算计时——
- 方案 A：`Agent` 工具内用 `context.WithoutCancel(parent)` + 自己的超时；
- 方案 B：父 run 的 settlement/abort 只取消"尚未开始"的工具，不取消已进入 `Agent` 的调用。

> 现状结论：**委派链路通了、可见、可审计；但"子代理被父预算误杀"是真实缺陷**，不能算完成。

### 修复（2026-11-15）

按"在 Agent 工具内部剥离父级取消 + 子代理自有超时"实施：

| 变更 | 位置 |
|---|---|
| `subagentContext(parent)` = `context.WithTimeout(context.WithoutCancel(parent), subagentTimeout())` | `agent/harness.go` |
| 包装器先用 `shouldStartSubagent(ctx)` 拒绝"已取消的轮次**新开**子代理"，再对**已启动**的委派用剥离后的 ctx | `agent/harness.go` |
| 子代理自有超时 `defaultSubagentTimeout = 10m`，可用 `HARNESS_SUBAGENT_TIMEOUT`（`90s` / `10m` / 纯秒数）覆盖 | `agent/harness.go` |
| 超时被如实区分：`子代理 X 超时（10m）`，不再混成 `context canceled` | `agent/harness.go` |

**语义取舍（写清楚）**：父级暂停/删除后，子代理最多再跑一个超时窗口。这是故意的——
"半途而废的侦察"比"跑完并回报"让图谱更脏；而它**有界**，且进程退出会终止它。

**回归测试（两级）**

1. 机制级 `agent/harness_subagent_test.go`：父 ctx 取消后子 ctx 不取消、且带自己的 deadline；
   `HARNESS_SUBAGENT_TIMEOUT` 解析（`90s`/`10m`/`120`/非法/负数）；`shouldStartSubagent` 三种输入。
2. **集成级** `agent/harness_subagent_it_test.go`：跑**真实的 Agent 工具**（假 provider，流式），
   在子代理执行中途取消父 ctx，断言子代理仍返回答案；另测"已取消的轮次拒绝新开子代理"与
   "未知 agent_type 报错"。

**负向对照（证明这是真回归测试）**：

```
# 把 subagentContext(ctx) 换回 ctx（即修复前）：
--- FAIL: TestAgentToolSurvivesParentCancel
    subagent reported an error: Error: subagent failed: context canceled
# 还原修复后：
ok  github.com/Autumn-27/artex/agent
```

即：去掉 detach 立刻复现线上那条错误，加回即通过。未再跑一次真实模型委派（随机且慢），
因为集成测试用确定性方式覆盖了同一条路径。

## 二、F12 面（P3-4）

- 预置 `chrome-devtools` MCP（stdio，`npx -y chrome-devtools-mcp@latest`），与 `browser`（Playwright）分列。
- `GET /api/harness/browser` 同时返回两段：`{browser…, devtools:{configured,enabled,tools_cached,ready}, f12_hint}`。
- 现状：`enabled=true, tools_cached=0, ready=false`；`f12_hint` 明确指岀"需要一个开启远程调试的 Chrome
  （--remote-debugging-port=9222）"。
- **未完成**：真正连通并把 console/network 渲染成面板（需要 Chrome 侧条件 + 结果结构化）。

## 三、终端抽屉与「接管执行一步」（P4）

- `POST /api/tasks/{id}/exec {command, inject, timeout_seconds}`
  - 在**任务工作区**执行 `bash -lc`，超时 60s（可调 ≤300s），输出 64 KB 截断；
  - 以 `worker=human`、`tool=human.sh` 写两条活动（命令 + 结果）→ 事件流与回放里能区分"人做的"；
  - `inject=true` 时把输出交给 agent（`say`），失败会显式报错而不是静默丢弃。
- 控制台：顶栏「终端」按钮 / `Ctrl+`` 打开抽屉；输入框回车执行；勾选「结果交给 agent」。
- 实测：`echo hello-from-terminal; pwd` → `exit 0 · 9ms`，cwd=`/tmp/hdata/tasks/32`。

## 四、仍未落实（明确列出，不含糊）

| 项 | 原因 | 下一步 |
|---|---|---|
| 浏览器**会话生命周期**（崩溃重建、多标签隔离、超时回收） | MCP client 目前每轮由 `AugmentTools` 创建，没有常驻会话管理器 | 引入 `harness/browser.Manager`：按任务持有 session，带 idle 超时与重建 |
| **逐像素内嵌视图** | 需要图像管线（MCP 截图 → 存 blob → 前端渲染），当前事件只存文本 | 在工具结果路径识别 image block，落 `data/browser/`，前端 `<img>` 指向该端点 |
| **项目切换** | 目前"一个任务=一个工作区目录"，没有"项目"这一层实体 | 增 `tasks.project_dir`（或 project 表），控制台左栏按项目分组，文件树/终端/DIFF 都以其为根 |
| F12 面板（console/network 结构化展示） | 依赖上表第一条 + Chrome 侧条件 | 先做 `harness/browser.Manager`，再解析 devtools 工具输出 |

## 五、历史任务核对

```sql
select coalesce(harness_mode,false) harness, count(*) from tasks where deleted_at is null group by 1;
 harness | count
---------+-------
 f       |    18     -- 历史 ARTEX 管线（行为未改）
 t       |     3     -- #29 #30 #32（本次新建）
```

结论：**历史任务一个都没有被改动**（`harness_mode=false`，仍走原管线），符合 ADR-0004 的
"升级不改既有行为"；需要时可以逐个切换：
`PUT /api/tasks/{id}/harness-mode {"harness_mode":true}`（下一轮生效）。
