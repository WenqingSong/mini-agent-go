# 多阶段构建：编译阶段有 Go 工具链，运行阶段只留一个静态二进制。
FROM golang:1.24 AS build

WORKDIR /src

# 先单独复制 go.mod：依赖解析层可以缓存（本模块零第三方依赖，仍是稳定的缓存边界）。
COPY go.mod ./
RUN go mod download

COPY . .

# CGO_ENABLED=0：产物是静态二进制，运行阶段不需要 libc / Go 工具链。
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/mini-agent ./cmd/mini-agent

FROM alpine:3.21

# 非 root 运行：程序只写 stderr 日志，不落盘、不绑定特权端口。
RUN adduser -D -u 10001 -s /sbin/nologin miniagent
USER miniagent
WORKDIR /app

COPY --from=build /out/mini-agent /app/mini-agent

# 监听地址由运行时环境变量决定（Compose 注入 0.0.0.0:8080）。
# 应用自身 fail-closed：非回环监听必须配置 MINI_AGENT_AUTH_TOKEN。
EXPOSE 8080

# 健康检查用 busybox wget，/healthz 免认证且不探测 Provider。
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=12 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/mini-agent"]
