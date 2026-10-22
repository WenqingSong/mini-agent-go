# 开发者初始化与交付记录

本次从 `develop` 的 `ee60a12` 开始，在 `docs/developer-onboarding-v1` 实施。按用户最终要求停止后续测试，提交并推送该独立分支；不修改 `develop`、`main`、VERSION 或 GitHub 同步机制。

## 改动

| 文件 | 内容 |
| --- | --- |
| `Makefile`、`scripts/init.sh`、`scripts/common.sh` | 统一初始化入口，简短菜单与非交互 `MODE=image/local`，保存模式；已有 `.env` 不覆盖 |
| `scripts/up.sh`、`scripts/compose.sh`、`scripts/check-env.sh` | 按模式启动、配置检查、容器与宿主机健康/页面检查；保留 down、logs、ps |
| `compose.yaml`、`compose.image.yaml`、`compose.local.yaml` | 共享单容器配置，仅区分镜像来源；容器监听固定为 `0.0.0.0:8080`，宿主机默认仅映射 `127.0.0.1:8080` |
| `.env.example`、`.gitignore`、`.dockerignore` | 必填配置置顶；忽略密钥文件与本地模式文件，避免进入构建上下文 |
| `README.md`、`docs/running.md`、`docs/verification.md`、`web/README.md` | 重组入口与运行说明，保留架构、Memory、已有测试和 AI 开发记录 |
| `scripts/onboarding_test.go` | 最小离线测试，覆盖初始化保护、模式分支和启动失败路径 |

README 按项目简介、代码托管、环境准备、快速开始、HTTP API / curl、系统设计与 Memory、测试与 AI 辅助开发、其他命令组织。Agent Runtime、前端 UI、Dockerfile 和发布流水线未改动。

## 使用方式

1. 执行 `make init`：回车默认选择预构建镜像；选 2 从源码构建。无交互时使用 `make init MODE=image` 或 `make init MODE=local`。
2. 编辑 `.env`：填写模型 API Key 和本服务 HTTP Token；切换模型厂商时同步修改 BaseURL、Model、API Key。
3. 执行 `make up`，成功后访问 `http://127.0.0.1:8080`。

模式以纯文本保存到 `.mini-agent-mode`，不包含凭证、不作为 Shell 执行。重新初始化可以切换模式，已有 `.env` 按字节保留。

- **image**：先拉取 `docker.cnb.cool/framework-station/mini-agent-go:latest`，再以 `--no-build --pull never` 启动。当前发布镜像为 AMD64，编排显式指定 `linux/amd64`。
- **local**：先按根 Dockerfile 构建 React 与 Go 的完整应用镜像，再以同样参数启动；不启动额外 Node 服务。

两种模式的启动后检查均要求容器运行且健康、宿主机 `/healthz` 成功、首页含 React 挂载节点；失败返回非零退出码。默认就绪等待为 90 秒，不包含拉取/构建耗时。成功输出访问地址、curl 入口与 Bearer 占位符，不打印凭证。

## 配置与安全

`.env.example` 默认 BaseURL 为 `https://tokenhub.tencentmaas.com/v1`，Model 为 `deepseek/deepseek-flash`，两类凭证均为替换占位符。保留原有可选默认值：请求超时 60s、关闭宽限 10s、请求上限 1 MiB、模型与工具调用预算各 8、工具超时 30s、Context 预算 0、近期消息下限 8，以及原系统提示词和静态目录说明。

首次创建 `.env` 使用限制性权限；已有文件不覆盖。`.env` 和模式文件均被 Git 与 Docker 构建上下文忽略。容器监听由 Compose 明确注入，不改变 Go 程序直接启动时默认监听回环地址的行为。README 使用 Bash 隐藏输入设置 HTTP Token，不执行或 `source .env`。

## 已完成的验证及边界

以下结果发生在用户要求停止测试之前；停止后未继续测试，也没有调用真实付费模型。

| 项目 | 结果 |
| --- | --- |
| `make ci` | 通过：Go build、vet、单元/离线 E2E 与 race；随后新增的页面不可达失败用例通过 scripts 包定向测试 |
| 初始化 | 实际交互默认选 image、重跑切换 local；已有 `.env` 内容保持一致。离线测试覆盖非交互参数、非法模式与首次创建 |
| Compose 配置 | 两种模式均只有一个应用服务、默认回环端口映射、固定容器监听；仅 local 包含 build 配置 |
| 预构建模式 | 实际拉取并成功启动；无本地构建，单容器健康，页面、静态资源、健康接口可访问 |
| README curl | 使用本地模拟 Provider，按 README 的 Bash 示例创建 Session、计算得到 1776、同会话追问得到 1800；两次回复均为 COMPLETED。未认证创建 Session 返回 401 |
| 配置注入与凭证 | 模拟 Provider 收到预期模型与认证信息；前端产物未发现测试凭证，成功提示无凭证；初始化及启动过程没有模型调用 |
| 容器监听回归 | 验证时故意将 `.env` 设置为回环监听，实际容器仍由 Compose 覆盖为 `0.0.0.0:8080`，宿主机访问成功 |
| 本地构建模式 | 已实现并通过离线模式选择测试；实际 Docker 构建未完成，按用户要求停止，**未验证完整启动结果** |
| 前端及远端流水线 | 本次未重新完成浏览器测试、前端构建检查或远端 CNB 流水线验收；历史证据仍见 `docs/verification.md` |

实际容器验证使用回环端口 **18080**，因为本机 8080 已被其他服务占用；README curl 仅替换端口，默认 8080 映射另经 Compose 配置检查确认。验证的已发布镜像 ID 为 `sha256:a243e468cc6a09ab7f982308f7bbf26b78a1b842e008af57208bf80f9117b2bd`，镜像版本标签为 `v0.1.0`、源码 revision 为 `0322094a6ccea064335a3e8efba46cbf0a3518d1`。`latest` 后续可能更新。

## 环境前提与剩余限制

- 首次使用仍需可访问的 Docker daemon、Compose、Make、curl，以及有效模型服务配置。启动检查验证应用可访问，不验证模型凭证有效性。
- 当前发布镜像只有 AMD64；Apple Silicon 需要 Docker 的 AMD64 模拟支持，或选择本地构建。
- 可选 GoLand 开发镜像不是应用镜像。本次未拉取或启动该开发镜像；自行使用时必须另外确认 Docker CLI、Compose、daemon / Socket 与端口访问位置，不能仅凭拉取镜像认定 DinD 可用。
- 已验证的预构建路径未发现阻止使用的问题；本地完整构建与启动仍未经本次验收，不能声称两个模式都已实际通过。
- Session 仍为服务端内存状态，重启后丢失；浏览器本地记录不能恢复服务端上下文。

最终提交及远端 SHA 以本次交付回复为准。本分支推送不等于正式镜像发布。
