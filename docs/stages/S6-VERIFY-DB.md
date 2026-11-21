# S6 真库验证（make test-db）

日期：2026-11-21。背景：阶段一、隔离 hotfix、阶段二、删后端 184 条之后，`make test-db`
一直没跑过（本机无 PostgreSQL、无 `ARTEX_PG_DSN`）。

## 环境

- PostgreSQL 16.15，apt 安装，集群 `16/main`，端口 5432，`pg_ctlcluster 16 main start`
  手动拉起（`policy-rc.d` 禁止自启）。
- 专用测试库 `artex_harness_test`，用户 `harness_test`（CREATEDB），与开发库
  `artex_harness` 隔离；`ARTEX_TEST_DB=artex_harness_test` 与 DSN 库名一致，
  破坏性测试才放行（`db.TestDatabaseAllowed`）。
- Go 1.26.3（`~/goroot/go/bin/go`），缓存指到 `~/workspace` 下。
- 注意：apt 装的 PostgreSQL 不随虚拟机重启保留——本次验证中途虚拟机重启，
  重启后需重装并重建用户与测试库（运行时的 os-intent replay 未自动恢复它）。

## 结果

`go test ./... -count=1 -timeout 25m`：30 个包全部 `ok`，exit 0。
（`go vet ./server/...` 与 `go vet ./...` 同样通过。）

## 过程：三轮才绿

1. 第一轮 `make test-db` 跑到默认 10 分钟超时（server 包），只拿到末尾的
   goroutine dump；改用 `-timeout 25m` 并把输出落文件后重跑才看到完整结果。
   中间一次 server 单独跑被外部中断，只跑了 103 个测试就断了——不要拿它当结论，
   以完整重跑为准。
2. 第二轮完整跑：db 通过，server 8 个失败，全部是 404（测已删的路由）。
3. 修完 8 个再跑：又冒出 3 个同样原因的失败——它们在上一轮的 103 个里根本没跑到，
   是被截断的运行漏掉的。这说明“部分跑通”不可信，必须等完整跑完再下结论。
4. 第三轮：又发现 `llmrec` 1 个失败（上游既有 bug，见下）与 agent 1 个死锁抖动。
   修完 llmrec 后全量重跑，30 个包全绿。

## 修了什么（只动测试，生产代码未动）

| 位置 | 处理 |
|---|---|
| `server/core_test.go` | 删 `/api/stats` 断言块 |
| `server/finding_workflow_test.go` ×3 处 | PUT `/api/settings` 改 `pg.SetBool`（开关读的本来就是同一张表） |
| `server/gitbash_test.go` | 重写：settings HTTP 面板已删，改走库 + `applyGitBash` + `shellStatus`；400 校验与引号清洗在已删 handler 里，无法保留 |
| `server/inheritance_api_test.go` | 去掉 activity 详情的可见性断言（端点已删），只保留删 source 后的关系清理断言，测试改名 |
| `server/intercept_detail_test.go`、`intercept_filter_test.go`、`mgmt_test.go`、`task_categories_test.go`、`task_control_routes_test.go` | 整文件删（只测已删端点） |
| `server/task_archives_test.go` | 去掉已删的列表/批量口断言，只保留单任务归档入队（口还在），测试改名 |
| `llmrec/llmrec_test.go` | 上游既有隔离 bug：`TestSideUsageRecordedOnceOnConsumerCancellation` 假定 `llm_usage` 表已存在，本包无 TestMain 建表，单独跑或 schema 被重置后就 `42P01`；补 `pg.EnsureLLMUsageTable()` |
| `server/stage2_http_test.go`（新增） | 阶段二四个口此前零测试覆盖，补 3 个回归测试：确认 422 → 人工越过 200 → 审计链 valid；审计链 + 专业报告渲染；流量重放 200 复现 |

## 未修，定性为抖动

- `agent/TestBlackboardToolsReadDirectSources`：全量并行跑时偶发 PG 死锁（40P01），
  单独 `-count=3` 通过。
- `server/TestBackgroundDelegationNotifierDeliversAndDrains`：全量跑时偶发失败一次，
  单独 `-count=3` 通过。

## 还没验证的

- 浏览器里点一遍阶段二四个面板（审计链 / 确认 / 流量重放 / 报告）：需部署 + 真流量，
  HTTP 表面已验证，UI 点按未验证。
- UNCERTAIN 62 条路由仍未定去留；阶段三（P2）未确认。
