# 工程入口（公共文件，不属于任何业务模块）。
# `make test` 是 .cnb.yml 中 pr-check 使用的入口，必须保持与 CI 一致。

GO ?= go
ENGINE_DIR ?= .agent/engine

.PHONY: test build vet test-engine

# 根模块测试入口（与 CI 一致）：go test ./... 不会进入 .agent/engine（点目录 + 嵌套 Module 剪枝）
test:
	$(GO) test ./...

build:
	$(GO) build ./...

vet:
	$(GO) vet ./...

# .agent/engine 是独立 Module，必须单独测试，避免被根模块 ./... 吞掉或反向关联
test-engine:
	GOWORK=off $(GO) -C $(ENGINE_DIR) test ./...
