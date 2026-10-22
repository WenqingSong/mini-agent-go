#!/bin/sh
set -eu
. scripts/common.sh

mode=${MODE:-}
if [ -z "$mode" ]; then
  [ -t 0 ] || die '非交互终端请指定 make init MODE=image 或 make init MODE=local'
  printf '选择启动方式：\n  1) CNB 预构建镜像（推荐）\n  2) 当前源码本地构建\n选择 [1]: '
  IFS= read -r choice || die '未读取到选择；也可使用 MODE=image 或 MODE=local'
  case "$choice" in ''|1) mode=image ;; 2) mode=local ;; *) die '请选择 1 或 2' ;; esac
fi
case "$mode" in image|local) ;; *) die 'MODE 仅支持 image 或 local' ;; esac
check_tools
[ -f .env.example ] || die '缺少 .env.example，仓库不完整'

umask 077
if [ -e .env ] || [ -L .env ]; then
  [ -f .env ] || die '.env 已存在但不是可读取的配置文件，请检查该路径'
  printf '· 保留已有 .env\n'
else
  # noclobber 同时保护并发初始化；不复制模板的宽松文件权限。
  (set -C; cat .env.example > .env) || die '创建 .env 失败；未覆盖已有文件'
  printf '✓ 已创建 .env\n'
fi

temporary=$(mktemp "$MODE_FILE.tmp.XXXXXX")
trap 'rm -f "$temporary"' EXIT HUP INT TERM
printf '%s\n' "$mode" > "$temporary"
mv -f "$temporary" "$MODE_FILE"
printf '✓ 启动模式：%s（保存在 %s）\n\n' "$mode" "$MODE_FILE"
printf '编辑 .env，填写模型 API Key 和本服务 HTTP Token。\n更换模型厂商时同步修改 BaseURL、Model 和 API Key。\n然后执行：make up\n'
