#!/bin/sh
# 由仓库根目录的 Makefile 调用。模式文件只保存 image/local，绝不当 Shell 执行。
DOCKER=${DOCKER:-docker}
COMPOSE=${COMPOSE:-"$DOCKER compose"}
MODE_FILE=.mini-agent-mode

die() { printf '✗ %s\n' "$*" >&2; exit 1; }

read_mode() {
  [ -f "$MODE_FILE" ] || die '尚未选择启动模式：运行 make init（非交互使用 MODE=image 或 MODE=local）'
  mode=$(cat "$MODE_FILE")
  case "$mode" in image|local) ;; *) die '启动模式无效：重新运行 make init MODE=image 或 MODE=local' ;; esac
}

docker_cmd() {
  # 与 Makefile 的 DOCKER / COMPOSE 覆盖方式一致：只分词，不 eval。
  $DOCKER "$@"
}

compose_cmd() {
  if [ -f "$MODE_FILE" ]; then
    read_mode
    $COMPOSE --env-file .env -f compose.yaml -f "compose.$mode.yaml" "$@"
  else
    # 兼容升级前的容器：未初始化时仍可使用 down / logs / ps。
    $COMPOSE --env-file .env -f compose.yaml "$@"
  fi
}

check_tools() {
  command -v "$DOCKER" >/dev/null 2>&1 || die '未找到 Docker CLI'
  $COMPOSE version >/dev/null 2>&1 || die '需要 Docker Compose v2（docker compose version）'
  command -v curl >/dev/null 2>&1 || die '需要 curl 检查宿主机访问地址'
}
