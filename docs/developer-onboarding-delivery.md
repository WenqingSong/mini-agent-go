# 开发者初始化与交付记录

本分支从 `develop` 的 `ee60a12` 起在 `docs/developer-onboarding-v1` 实施，统一入口为 `make init → 编辑 .env → make up`。两种模式均在真实 Docker 中完成启动验收；未修改 Agent Loop、Session Manager、Tool Executor、Context Manager、Dockerfile、VERSION 与发布流水线。

## 改动

| 文件 | 内容 |
| --- | --- |
| `Makefile`、`scripts/init.sh`、`scripts/common.sh` | 统一初始化入口，简短菜单与非交互 `MODE=image/local`，保存模式；已有 `.env` 不覆盖 |
| `scripts/up.sh`、`scripts/compose.sh`、`scripts/check-env.sh` | 按模式拉取或构建、启动、配置检查、容器与宿主机健康/页面检查；保留 down、logs、ps |
| `compose.yaml`、`compose.image.yaml`、`compose.local.yaml` | 共享单容器配置，仅区分镜像来源；容器监听固定为 `0.0.0.0:8080`，宿主机默认仅映射 `127.0.0.1:8080` |
| `.env.example`、`.gitignore`、`.dockerignore` | 必填配置置顶；忽略密钥文件与本地模式文件，避免进入构建上下文 |
| `README.md`、`docs/running.md`、`docs/verification.md`、`web/README.md` | 重组入口与运行说明，保留架构、Memory、已有测试和 AI 开发记录 |
| `scripts/onboarding_test.go` | 最小离线测试，覆盖初始化保护、模式分支和启动失败路径 |

## 使用方式

1. 执行 `make init`：回车默认选择预构建镜像；选 2 从源码构建。无交互时使用 `make init MODE=image` 或 `make init MODE=local`。
2. 编辑 `.env`：填写模型 API Key 和本服务 HTTP Token；切换模型厂商时同步修改 BaseURL、Model、API Key。
3. 执行 `make up`，成功后访问 `http://127.0.0.1:8080`。

模式以纯文本保存到 `.mini-agent-mode`，不包含凭证、不作为 Shell 执行。重新初始化可以切换模式，已有 `.env` 按字节保留。

- **image**：先拉取 `docker.cnb.cool/framework-station/mini-agent-go:latest`，再以 `--no-build --pull never` 启动。当前发布镜像为 AMD64，编排显式指定 `linux/amd64`。
- **local**：先按根 Dockerfile 构建 React 与 Go 的完整应用镜像，再以同样参数启动；不启动额外 Node 服务。

启动检查要求容器运行且健康、宿主机 `/healthz` 成功、首页含 React 挂载节点；任一不满足即非零退出并指出失败项。默认就绪等待为 90 秒，不包含拉取/构建耗时。成功输出只打印地址与命令，不含任何凭证。

## 配置与安全

`.env.example` 默认 BaseURL 为 `https://tokenhub.tencentmaas.com/v1`，Model 为 `deepseek/deepseek-flash`，两类凭证均为替换占位符。必填区之后留空，再列可选默认值：请求超时 60s、关闭宽限 10s、请求上限 1 MiB、模型与工具调用预算各 8、工具超时 30s、Context 预算 0、近期消息下限 8，以及原系统提示词和静态目录说明。

首次创建 `.env` 使用 0600 权限；已有文件逐字保留。`.env` 与模式文件均被 Git 和 Docker 构建上下文忽略。容器监听由 Compose 明确注入，Go 程序直接运行时仍默认监听回环地址。README 用 Bash 隐藏输入设置 HTTP Token，不执行或 `source .env`，也不要求把凭证写入 Shell 配置文件。

## 验收结果

2026-10-22 在本机 Docker（Engine 29.6.2、Compose v5.3.1、linux/amd64）实际执行，端口为默认 `127.0.0.1:8080`。模型调用使用本机离线 mock Provider（OpenAI 兼容、含 calculator 工具调用），**未调用真实付费模型**。

| 项目 | 结果 |
| --- | --- |
| `make ci` | 通过：`go build`、`go vet`、单元/离线 E2E 与 `-race`，含 `scripts` 初始化与启动流程测试 |
| 首次初始化 | 全新技术预览（`git archive` 解包）中 `make init MODE=local` 由模板创建 `.env`，权限 600 且与 `.env.example` 一致，模式文件写入 `local` |
| 交互初始化 | TTY 下直接回车保存 `image`，输入 `2` 保存 `local`；两次执行后 `.env` 字节不变 |
| 非交互与非法输入 | 无 TTY 且未给 `MODE` 时提示 `MODE=image` / `MODE=local` 并非零退出；非法模式不创建任何文件 |
| 占位符保护 | 未填凭证时 `make up` 以非零状态退出，逐项指出仍为占位符的变量，不启动容器 |
| 预构建模式 | `make init MODE=image` + `make up` 实际拉取 `latest`（镜像 ID `sha256:a243e468…`，标注版本 `v0.1.0`、源码 revision `0322094a`）并启动；单容器 healthy，`127.0.0.1:8080->8080/tcp`，首页 200 且含 `<div id="root">`，`/healthz` 返回 `{"status":"ok"}` |
| 本地构建模式 | `make init MODE=local` + `make up` 用当前源码经 Dockerfile 前端与 Go 阶段构建 `mini-agent-go:local`（`sha256:22a214b7…`）并启动；上述页面、健康检查与端口结论一致 |
| 容器监听回归 | `.env` 故意写入 `MINI_AGENT_LISTEN_ADDR=127.0.0.1:8080`，容器内实际为 `0.0.0.0:8080`（Compose 覆盖生效），宿主机访问正常；不再出现 Healthy 但 Connection Reset |
| README curl | 两种模式下按 README 的 Bash 示例创建 Session、计算 `37*48` 得 `1776`（`llm_calls=2`、`tool_calls=1`），同会话追问加 24 得 `1800`，两次均为 `COMPLETED`；未带 Bearer 创建 Session / 调用 chat 返回 401 |
| 凭证注入 | mock Provider 收到的模型名与 `Authorization` 与 `.env` 一致；`make init` / `make up` 未发起模型请求；初始化与启动输出、前端产物均未出现测试凭证 |
| 其他命令 | 真实 Docker 下 `make ps` 显示容器与健康状态，`make down` 移除容器与网络且保留 `.env` 与模式文件 |
| 容器数量 | 两种模式均为单个应用容器，页面与 API 同端口，无额外前端容器 |

## 环境前提与剩余限制

- 首次使用需要可访问的 Docker daemon、Compose v2、Make、curl，以及有效模型服务配置。启动检查验证应用可访问，不验证模型凭证有效性。
- 当前发布镜像只有 AMD64；Apple Silicon 需要 Docker 的 AMD64 模拟支持，或选择本地构建。
- 可选 GoLand 开发镜像是开发环境镜像，不是应用镜像。本次未拉取或启动该镜像；自行使用时必须另外确认 Docker CLI、Compose、daemon / Socket 与端口访问位置。
- Session 仍为服务端内存状态，重启后丢失；浏览器本地记录不能恢复服务端上下文。
- 本轮只做交付流程验收：未重新执行浏览器 E2E、前端单测与远端流水线，其历史证据见[测试与验收](verification.md)；未调用真实模型，语义质量证据同样以既有记录为准。

本分支推送不等于正式镜像发布；`main` 的 push 才会构建并发布镜像。
