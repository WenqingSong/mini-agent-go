# 单镜像多阶段构建：一次构建同时产出前端静态资源与 Go 可执行文件，
# 运行阶段只有一个进程，同一个端口既提供页面也提供 API。
#
# 构建上下文是仓库根（`docker build .`）：
#   阶段 web   → Node.js 24，锁文件安装依赖，测试 + 类型检查 + 生产构建；
#   阶段 build → Go 1.24，编译静态二进制；
#   运行镜像   → Alpine，只含二进制 + 前端产物 + CA 证书。

# ---- 阶段 1：前端静态产物 ----
FROM node:24-alpine AS web

WORKDIR /src/web

# 先只用锁文件安装依赖：package*.json 不变时这一层直接命中缓存。
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ ./

# 前端测试失败、类型检查失败或构建失败都会让镜像构建失败（不会产出半成品镜像）。
RUN npm run check

# ---- 阶段 2：Go 后端 ----
FROM golang:1.24 AS build

WORKDIR /src

# 先单独复制 go.mod：依赖解析层可以缓存（本模块零第三方依赖，仍是稳定的缓存边界）。
COPY go.mod ./
RUN go mod download

COPY . .

# CGO_ENABLED=0：产物是静态二进制，运行阶段不需要 libc / Go 工具链。
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/mini-agent ./cmd/mini-agent

# ---- 阶段 3：最终运行镜像 ----
FROM alpine:3.21

# ca-certificates：调用 HTTPS Provider 需要根证书；alpine 默认可能不含完整证书包。
# 只增加运行时必需的内容，镜像内不安装 Node.js，也不运行任何前端开发服务器。
RUN apk add --no-cache ca-certificates \
	&& adduser -D -u 10001 -s /sbin/nologin miniagent

WORKDIR /app

COPY --from=build /out/mini-agent /app/mini-agent
# 前端产物目录：只读静态文件，由 Go 进程在同一端口提供。
COPY --from=web /src/web/dist /app/web

# 默认开启页面服务；静态目录缺失时应用启动即失败（fail-closed），不会静默只剩 API。
ENV MINI_AGENT_WEB_DIR=/app/web

# 非 root 运行：程序只写 stderr 日志，不落盘、不绑定特权端口。
USER miniagent

# 监听地址由运行时环境变量决定（Compose 注入 0.0.0.0:8080）。
# 应用自身 fail-closed：非回环监听必须配置 MINI_AGENT_AUTH_TOKEN。
EXPOSE 8080

# 健康检查用 busybox wget，/healthz 免认证且不探测 Provider。
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=12 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/mini-agent"]
