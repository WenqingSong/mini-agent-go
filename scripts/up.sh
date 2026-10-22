#!/bin/sh
set -eu
. scripts/common.sh
read_mode
sh scripts/check-env.sh
compose_cmd config --quiet || die 'Compose 配置无效，请检查 .env 和编排文件'

timeout=${MINI_AGENT_STARTUP_TIMEOUT:-90}
case "$timeout" in ''|*[!0-9]*|0) die 'MINI_AGENT_STARTUP_TIMEOUT 必须是正整数秒数' ;; esac
[ "$timeout" -gt 0 ] || die 'MINI_AGENT_STARTUP_TIMEOUT 必须大于 0'
app_url=${APP_URL:-http://127.0.0.1:${MINI_AGENT_HOST_PORT:-8080}}

case "$mode" in
  image)
    printf '拉取 CNB 预构建镜像（linux/amd64）…\n'
    compose_cmd pull mini-agent || die '应用镜像拉取失败；检查 Docker 到 CNB Registry 的连接与访问权限'
    ;;
  local)
    printf '从当前源码构建单镜像…\n'
    compose_cmd build mini-agent || die '本地镜像构建失败，请查看上方构建错误'
    ;;
esac
# 两种模式均显式禁止在 up 阶段构建/拉取，避免镜像来源发生隐式回退。
compose_cmd up -d --no-build --pull never mini-agent || die '容器启动失败；检查端口占用和 Docker 状态（make ps / make logs）'
container=$(compose_cmd ps -a -q mini-agent)
[ -n "$container" ] || die 'Compose 未返回应用容器，请运行 make ps 检查'

deadline=$(($(date +%s) + timeout))
health=unreachable
page=unreachable
state=unknown
while [ "$(date +%s)" -lt "$deadline" ]; do
  state=$(docker_cmd inspect --format '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container") || die '无法读取应用容器状态'
  case "$state" in
    exited*|dead*|restarting*|*unhealthy*) die "应用容器状态：${state}；运行 make logs 查看启动错误" ;;
  esac
  health=unreachable
  page=unreachable
  if curl --noproxy '*' --connect-timeout 1 --max-time 2 -fsS "$app_url/healthz" >/dev/null 2>&1; then health=ok; fi
  if html=$(curl --noproxy '*' --connect-timeout 1 --max-time 2 -fsS "$app_url/" 2>/dev/null) && printf '%s' "$html" | grep -q '<div id="root">'; then page=ok; fi
  if [ "$state" = 'running healthy' ] && [ "$health" = ok ] && [ "$page" = ok ]; then
    cat <<EOF

mini-agent-go started successfully.

Web UI:
  $app_url

Health Check:
  curl $app_url/healthz

Backend API:
  POST /v1/sessions
  POST /v1/chat
  Authorization: Bearer <your-token>

API examples:
  See README.md — HTTP API / curl

Commands:
  make logs
  make down
EOF
    exit 0
  fi
  sleep 1
done
die "${timeout}s 内未就绪：容器=${state}，/healthz=${health}，页面=${page}。检查端口映射和容器监听（0.0.0.0:8080）；运行 make ps / make logs"
