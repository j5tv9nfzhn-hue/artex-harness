# artex-harness — 统一命令入口
#
# 目的：把散落在 build.sh / dev.sh / start.sh / update.sh 里的手工步骤收敛成一套
# 可预期的目标，任何人（和任何 AI）都不需要猜「该怎么构建、怎么跑测试」。
# 部署/升级相关（多平台交叉编译、Docker、自更新）仍走原有脚本，见 README。

SHELL := /bin/bash
BIN   := artex-harness
PKG   := ./cmd/artex

.DEFAULT_GOAL := help

.PHONY: help web build build-all vet test test-db run clean tidy

help: ## 列出所有可用目标
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
	  | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

web: ## 构建控制台（产物 server/console-app，被 go:embed 打进二进制）
	cd web && npm ci --no-audit --no-fund && npm run build

build: ## 只编译后端（使用仓库里已有的 embedded 控制台产物）
	go build -o $(BIN) $(PKG)

build-all: web build ## 控制台 + 后端，一次到位

vet: ## 静态检查
	go vet ./...

test: ## 单元测试（不依赖数据库）
	go test ./agent/... ./harness/... ./config/... ./guard/... -count=1

test-db: ## 需要 PostgreSQL 的测试；必须显式指名测试库，见 db/testmain_test.go
	@test -n "$(ARTEX_TEST_DB)" || { \
	  echo "拒绝执行：这些用例会重建 schema。请显式指定 ARTEX_TEST_DB=<测试库名>，"; \
	  echo "且它必须与 ARTEX_PG_DSN 里的库名一致（不匹配时测试会自动跳过）。"; \
	  exit 1; }
	go test ./db/... ./server/... -count=1

run: ## 本地前台运行（数据落在 ./data-harness）
	go run $(PKG) -addr 127.0.0.1:8899 -data ./data-harness

clean: ## 清掉编译产物
	rm -f $(BIN)

tidy: ## 整理 go.mod / go.sum
	go mod tidy
