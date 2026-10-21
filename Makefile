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
export MINI_AGENT_HOST_PORT ?= 8080
HEALTH_URL ?= http://127.0.0.1:$(MINI_AGENT_HOST_PORT)/healthz

.DEFAULT_GOAL := help
.PHONY: help init check up down logs ps build test vet ci image test-engine

help: ## 显示命令说明
	@awk 'BEGIN { FS = ":.*## " } /^[a-z][a-z-]*:.*## / { printf "  %-12s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

init: ## 首次使用：检查 Docker、创建 .env（不启动服务、不调用模型）
	@command -v $(DOCKER) >/dev/null 2>&1 || { echo "✗ 未找到 docker：make up 需要 Docker" >&2; exit 1; }
	@$(COMPOSE) version >/dev/null 2>&1 || { echo "✗ docker compose 不可用：需要 Docker Compose v2" >&2; exit 1; }
	@test -f .env.example || { echo "✗ 缺少 .env.example，仓库不完整" >&2; exit 1; }
	@if [ -f .env ]; then \
		echo "· .env 已存在，保留现有内容（不覆盖已有 API Key 等配置）"; \
	else \
		cp .env.example .env; \
		echo "✓ 已根据 .env.example 创建 .env"; \
	fi
	@echo ""
	@echo "下一步：编辑 .env，至少填写这 4 项"
	@echo "  MINI_AGENT_LLM_BASE_URL   Provider 的 OpenAI 兼容根地址（含 /v1 等前缀）"
	@echo "  MINI_AGENT_MODEL          该服务支持的模型名"
	@echo "  MINI_AGENT_LLM_API_KEY    模型服务密钥"
	@echo "  MINI_AGENT_AUTH_TOKEN     本服务对外 Bearer 凭证（openssl rand -hex 32 可生成）"
	@echo ""
	@echo "填完后执行：make check && make up"

check: ## 校验 .env 与本地环境（不发任何模型请求）
	@sh scripts/check-env.sh

up: check ## 构建镜像并后台启动，用 /healthz 确认就绪（不调用模型）
	@$(COMPOSE) up -d --build
	@printf '等待 %s 就绪' '$(HEALTH_URL)'
	@i=0; while [ $$i -lt 30 ]; do \
		if curl -fsS '$(HEALTH_URL)' >/dev/null 2>&1; then \
			echo " ✓"; \
			echo "服务已启动：$(HEALTH_URL)"; \
			exit 0; \
		fi; \
		printf '.'; \
		i=$$((i + 1)); \
		sleep 1; \
	done; \
	echo " ✗"; \
	echo "✗ 30 秒内 /healthz 未就绪，最近日志：" >&2; \
	$(COMPOSE) logs --tail=50 >&2; \
	exit 1

down: ## 停止并移除容器（不删除 .env）
	@$(COMPOSE) down

logs: ## 跟随查看服务日志
	@$(COMPOSE) logs -f --tail=100

ps: ## 查看容器状态与健康检查结果
	@$(COMPOSE) ps

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

image: ## 本地构建 Docker 镜像（不做健康检查）
	$(DOCKER) build -t $(IMAGE) .

# .agent/engine 是独立 Module，必须单独测试，避免被根模块 ./... 吞掉或反向关联
test-engine: ## 单独测试 Workflow 校验器（独立 Module）
	GOWORK=off $(GO) -C $(ENGINE_DIR) test ./...
