# 本地开发与容器编排入口。
# CI（.cnb.yml）直接调用 `make ci`，本地与流水线共用同一套检查命令，避免两处漂移。
SHELL := /bin/sh
GO ?= go
DOCKER ?= docker
COMPOSE ?= $(DOCKER) compose
ENGINE_DIR ?= .agent/engine
BIN_DIR ?= bin
BINARY ?= $(BIN_DIR)/mini-agent
IMAGE ?= mini-agent-go:local

# 宿主机回环端口：只绑定 127.0.0.1，容器内部固定监听 0.0.0.0:8080。
# 导出后 compose.yaml 的 ${MINI_AGENT_HOST_PORT} 与下面的健康检查使用同一个值。
# 单镜像只发布一个端口：页面与 API 同源同端口。
export MINI_AGENT_HOST_PORT ?= 8080
APP_URL ?= http://127.0.0.1:$(MINI_AGENT_HOST_PORT)
export DOCKER COMPOSE APP_URL MODE

.DEFAULT_GOAL := help
.PHONY: help init check up down logs ps build test vet ci web-check web-stage image test-engine

help: ## 显示命令说明
	@awk 'BEGIN { FS = ":.*## " } /^[a-z][a-z-]*:.*## / { printf "  %-12s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

init: ## 初始化 .env 并选择启动模式（MODE=image / MODE=local）
	@sh scripts/init.sh

check: ## 校验启动模式、.env 与 Docker 环境（不调用模型）
	@sh scripts/check-env.sh

up: ## 按已保存的模式拉取或构建镜像，启动并验证单容器应用
	@sh scripts/up.sh

down: ## 停止并移除容器（保留 .env 与启动模式）
	@sh scripts/compose.sh down

logs: ## 跟随查看服务日志
	@sh scripts/compose.sh logs -f --tail=100

ps: ## 查看容器状态与健康检查结果
	@sh scripts/compose.sh ps

build: ## 本地构建可执行文件到 bin/mini-agent
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -o $(BINARY) ./cmd/mini-agent
	@echo "✓ $(BINARY)"

test: ## 运行 Go 测试（离线，不访问真实 Provider）
	$(GO) test -count=1 ./...

vet: ## 运行 go vet
	$(GO) vet ./...

ci: ## CI 同款全量检查：build + vet + test + race
	$(GO) build ./...
	$(GO) vet ./...
	$(GO) test -count=1 ./...
	$(GO) test -race ./...

web-check: ## 前端测试、类型检查与构建（本机需 Node.js 24；先 npm --prefix web ci）
	npm --prefix web run check

web-stage: ## 在 Node.js 24 容器内执行前端测试、类型检查与构建（只跑前端阶段）
	$(DOCKER) build --target web -t mini-agent-web-build:local .

image: ## 本地构建单镜像（Go 后端 + 前端产物；构建内含前端测试与类型检查）
	$(DOCKER) build -t $(IMAGE) .

# .agent/engine 是独立 Module，必须单独测试，避免被根模块 ./... 吞掉或反向关联
test-engine: ## 单独测试 Workflow 校验器（独立 Module）
	GOWORK=off $(GO) -C $(ENGINE_DIR) test ./...
