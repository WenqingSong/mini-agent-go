#!/bin/sh
# 启动前检查：只读 .env 与本地环境。
#
# 三条边界：
#  1. 不访问 Provider、不发任何模型请求（make up 也不做）；
#  2. 不打印任何取值，只输出变量名与问题描述，避免凭据进入终端或 CI 日志；
#  3. 校验口径与应用一致：变量名、BaseURL 协议、时长/整数格式都以
#     cmd/mini-agent/main.go 与 httpapi/config.go 的真实实现为准。
set -eu

if [ ! -f .env ]; then
  echo "✗ 未找到 .env：先执行 make init" >&2
  exit 1
fi

# value 读取单个键的值。不 source .env：值里的空格会被 shell 当成命令执行。
# 语义与 Docker env_file 保持一致——引号属于值本身，同名键以最后一行为准。
value() {
  awk -v key="$1" '
    index($0, key "=") == 1 {
      value = substr($0, length(key) + 2)
      sub(/\r$/, "", value)
    }
    END { print value }
  ' .env
}

bad=0
report() {
  bad=1
  printf '✗ %s\n  → %s\n' "$1" "$2"
}

# require 只判断“有没有填、是不是还留着示例占位符”。
require() {
  if [ -z "$2" ]; then
    report "$1 未配置" "在 .env 中填写 $1"
    return
  fi
  case "$2" in
    REPLACE_WITH_*)
      report "$1 仍是 .env.example 中的占位符" "替换为真实值"
      ;;
  esac
}

base_url=$(value MINI_AGENT_LLM_BASE_URL)
model=$(value MINI_AGENT_MODEL)
api_key=$(value MINI_AGENT_LLM_API_KEY)
auth_token=$(value MINI_AGENT_AUTH_TOKEN)

require MINI_AGENT_LLM_BASE_URL "$base_url"
require MINI_AGENT_MODEL "$model"
require MINI_AGENT_LLM_API_KEY "$api_key"
# 容器内监听 0.0.0.0:8080，应用对非回环监听的硬要求就是必须有凭证。
require MINI_AGENT_AUTH_TOKEN "$auth_token"

# BaseURL 需要满足 openai.NewClient 的三条校验：http(s) 协议、有主机名、无空格。
if [ -n "$base_url" ]; then
  case "$base_url" in
    http://* | https://*) ;;
    *) report "MINI_AGENT_LLM_BASE_URL 协议不支持" "必须以 http:// 或 https:// 开头" ;;
  esac
  case "$base_url" in
    *" "* | *'	'*) report "MINI_AGENT_LLM_BASE_URL 含空白字符" "地址中不能有空格或制表符" ;;
  esac
  host=$(printf '%s' "$base_url" | sed -e 's#^[A-Za-z][A-Za-z0-9+.-]*://##' -e 's#[/:?].*##')
  if [ -z "$host" ]; then
    report "MINI_AGENT_LLM_BASE_URL 缺少主机名" "示例：https://api.example.com/v1"
  else
    case "$host" in
      *.invalid)
        report "MINI_AGENT_LLM_BASE_URL 仍是示例地址（.invalid 域名不存在）" "换成真实 Provider 的 OpenAI 兼容根地址"
        ;;
    esac
  fi
fi

# 可选配置：只校验格式，不判断业务合理性；未配置时应用使用自己的默认值。
check_duration() {
  name=$1
  current=$(value "$name")
  [ -z "$current" ] && return 0
  # MINI_AGENT_TOOL_TIMEOUT <= 0 表示关闭工具级超时，因此允许负号。
  pattern='^[0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h)$'
  if [ "${2:-}" = "signed" ]; then
    pattern='^-?[0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h)$'
  fi
  printf '%s' "$current" | grep -Eq "$pattern" ||
    report "$name 不是合法时长" "使用 Go 时长格式，例如 60s / 10m / 1h"
}

check_positive_int() {
  name=$1
  current=$(value "$name")
  [ -z "$current" ] && return 0
  case "$current" in
    *[!0-9]*) report "$name 必须是正整数" "例如 8" ;;
    *) [ "$current" -gt 0 ] || report "$name 必须大于 0" "例如 8" ;;
  esac
}

check_duration MINI_AGENT_REQUEST_TIMEOUT
check_duration MINI_AGENT_SHUTDOWN_GRACE
check_duration MINI_AGENT_TOOL_TIMEOUT signed
check_positive_int MINI_AGENT_MAX_REQUEST_BYTES
check_positive_int MINI_AGENT_MAX_LLM_CALLS
check_positive_int MINI_AGENT_MAX_TOOL_CALLS

# make up 依赖 Docker Compose 与宿主机的 curl（轮询 /healthz）。
if ! command -v docker >/dev/null 2>&1; then
  report "未找到 docker" "安装 Docker 后重试（make up 用容器启动服务）"
elif ! docker compose version >/dev/null 2>&1; then
  report "docker compose 不可用" "安装 Docker Compose v2 插件（docker compose version 应能执行）"
elif ! docker info >/dev/null 2>&1; then
  report "Docker daemon 不可用" "启动 Docker Desktop，或把当前用户加入 docker 组后重新登录"
fi

if ! command -v curl >/dev/null 2>&1; then
  report "未找到 curl" "make up 用 curl 轮询 /healthz，请先安装 curl"
fi

if [ "$bad" -ne 0 ]; then
  printf '\n以上问题修改后重新执行 make check\n'
  exit 1
fi

echo "✓ 配置与环境检查通过（未发起任何模型请求）"
