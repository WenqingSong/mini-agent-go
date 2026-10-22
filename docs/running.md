# 运行指南

首次运行和连续对话示例见[README](../README.md)。本页补充开发镜像、直接运行二进制、容器操作与故障排查。

## CNB 开发环境与镜像

[CNB 主仓库](https://cnb.cool/framework-station/mini-agent-go)的云原生开发使用 [`.cnb.yml`](../.cnb.yml) 中的 `vscode` 配置：

- 开发镜像：`docker.cnb.cool/arthur-cloud-native/jetbrains/goland:5.0.1`。
- 配套服务：`vscode` 与 `docker`。
- 环境检查：Go、Git、Docker 与 Docker Compose 版本。

启动云原生开发后，平台拉取镜像并准备仓库工作区。在该环境的终端执行 `make init`，填写 `.env`，然后 `make up`。默认只绑定工作区环境的回环端口，直接在云开发终端运行 README 的 curl 示例即可。

自行准备开发环境时，可以预先拉取同一镜像：

```bash
docker pull docker.cnb.cool/arthur-cloud-native/jetbrains/goland:5.0.1
```

这条命令只下载开发镜像。进入自行配置的开发容器后，还需要仓库源码、可用的 Docker daemon 与 Compose；CNB 的 `docker` 配套服务由平台提供。本仓库没有提供独立的本地 GoLand 容器编排文件，开发镜像的本地交互启动未在本次文档整理中验证。

应用镜像由仓库根 [Dockerfile](../Dockerfile) 三阶段构建：Node.js 24 阶段执行前端测试、类型检查并构建 React 静态资源；Go 1.24 阶段编译静态二进制；Alpine 运行阶段只保留二进制、`/app/web` 前端产物与 CA 证书，以 UID 10001 非 root 用户运行。`make up` 只启动这一个容器：页面、`/api` 与 `/v1` 由同一个进程在同一个端口提供。

## 本地容器操作

本机需要 Git、Make、Docker、Docker Compose 和 curl；Docker daemon 应已启动。配置模板为 [`.env.example`](../.env.example)。

```bash
make init     # 检查 Docker/Compose，创建 .env，保留已有文件
# 编辑 .env：填写模型根地址、模型 ID、模型密钥、HTTP 访问凭证
make check    # 配置与环境预检查
make up       # 构建、启动，等待网页与 API 就绪；应用 http://127.0.0.1:8080
make ps       # 容器状态
make logs     # JSON 日志，Ctrl-C 退出查看
make down     # 停止并移除容器，保留 .env
```

`make init`、`make check` 与 `make up` 不发送模型请求；只有调用 `/v1/chat` 才会进入 Agent Loop。启动过程中若应用自身的配置校验失败，进程退出；`make check` 是预检查，不替代应用的最终校验。

`.env` 按模板使用单行 `KEY=value`，普通值不加引号。不要把 `.env` 当 shell 脚本执行。完整变量、默认值及适用条件见 [HTTP API 配置表](design/http-api.md#8-配置契约)。

Compose 固定注入 `MINI_AGENT_LISTEN_ADDR=0.0.0.0:8080`，所以模板没有重复列出该项；镜像内已设置 `MINI_AGENT_WEB_DIR=/app/web`，宿主机端口只映射到 `127.0.0.1`。更换宿主机端口：

```bash
MINI_AGENT_HOST_PORT=9090 make up
curl -sS http://127.0.0.1:9090/healthz
```

后续请求也应使用新端口。服务退出会清空内存中的会话，重启后需要重新创建。

页面与 API 共用 `MINI_AGENT_HOST_PORT`（默认 `127.0.0.1:8080`），没有第二个端口，也没有独立的前端容器。浏览器同源访问 `/api/healthz`、`/api/v1/sessions`、`/api/v1/chat`，凭证由 Go 进程在服务端注入：前端产物与浏览器都没有 `MINI_AGENT_AUTH_TOKEN`，模型 API Key 只存在于容器环境变量。`/v1` 端点仍要求 Bearer 凭证，`/api` 入口只接受同源写入请求；当前没有访客登录体系，端口仍只发布到宿主机回环地址。

网页只在浏览器保存会话列表和消息。关闭容器不会删除浏览器中的记录；模型的会话上下文仍随后端退出而消失。浏览器存储不可用、服务会话失效、请求中断时，页面会给出对应提示。详见[前端说明](../web/README.md)。

## 直接运行 Go 二进制

需要 Go 1.24 或更高版本。程序只读取进程环境变量，不会自动读取 `.env`。在独立终端中设置配置后运行，以下值均需替换：

```bash
make build
export MINI_AGENT_LLM_BASE_URL='https://your-provider.example/v1'
export MINI_AGENT_MODEL='your-tool-calling-model'
export MINI_AGENT_LLM_API_KEY='your-provider-key'
export MINI_AGENT_AUTH_TOKEN='your-http-token'
export MINI_AGENT_SYSTEM_PROMPT='你是一个简洁的助手。'
./bin/mini-agent
```

默认监听 `127.0.0.1:8080`。此模式没有 `make check` 的额外约束：应用允许 LLM API Key 为空，以支持不需要认证的兼容服务；Compose 快速启动路径则要求填入模板中的四项配置。

前台按 `Ctrl-C` 停止。服务默认给在途请求 10 秒退出宽限期，之后取消运行并关闭连接。优先使用构建后的二进制调试进程信号；`go run` 还会涉及父子进程之间的信号传递。

## 常见问题

| 现象 | 检查方法 |
| --- | --- |
| `.env` 仍含占位符 | 填好四项必填配置，再运行 `make check` |
| 找不到 Docker 或 daemon 不可用 | 确认 Docker/Compose 已安装且 Docker 服务运行；CNB 环境检查配套 Docker 服务 |
| `/healthz` 正常，但对话失败 | 健康检查不探测模型；检查 BaseURL、模型 ID、密钥和模型服务网络 |
| `401 unauthorized` | 请求的 HTTP Token 应等于 `.env` 中 `MINI_AGENT_AUTH_TOKEN`；不要使用模型 API Key 代替 |
| `404 session_not_found` | 先创建会话；进程重启后旧会话已丢失 |
| `409 session_exists` | 显式 Session ID 重复；使用新的 ID，或继续在已有会话发起对话 |
| `502 provider_failure` | 模型请求、协议响应或结束原因不符合预期；查看该 `run_id` 的日志 |
| `504 deadline_exceeded` | 请求级超时；检查 Provider 响应及 `MINI_AGENT_REQUEST_TIMEOUT` |
| `429 llm_call_limit` / `tool_call_limit` | 当前 Run 耗尽相应调用预算；不是 Provider 限流码的直接透传 |

BaseURL 应为兼容根地址，如 `https://host/v1`，客户端会追加 `/chat/completions`。当前实现会移除 URL 的 query 与 fragment，因此依赖 `?api-version=...` 的接口不能按此配置直接使用。

## 构建、发布与仓库同步

[Makefile](../Makefile) 的 `make ci` 检查 Go 业务模块；`make web-stage` 在 Node.js 24 容器内执行前端测试、类型检查和构建（只跑镜像的前端阶段），本机也可安装前端依赖后执行 `make web-check`。[`.cnb.yml`](../.cnb.yml) 的任意分支 push / PR 检查前后端；`develop` push 额外完整构建单镜像检查产物结构；`main` push 在检查、产物结构与容器冒烟全部通过后才推送唯一一个应用镜像（`latest` 与版本标签指向同一构建产物）。开发镜像与应用镜像地址不同：

```text
开发环境：docker.cnb.cool/arthur-cloud-native/jetbrains/goland:5.0.1
应用镜像：docker.cnb.cool/framework-station/mini-agent-go:latest
版本标签：docker.cnb.cool/framework-station/mini-agent-go:v<VERSION>-<提交短SHA>
```

版本标签由 [VERSION](../VERSION) 与构建提交派生，便于追溯源码；`latest` 与版本标签由同一个本地镜像打标签后推送，不会重复构建出内容不同的制品。可变基础镜像、重复构建和 Registry 权限仍会影响制品，源码中的命名规则本身不保证标签不可覆盖。需要精确锁定制品时使用实际镜像 digest。

CNB `main` 更新后会自动同步到 GitHub；同步由仓库侧另行配置，当前 `.cnb.yml` 只描述开发环境、检查与应用镜像发布。发布结果以 CNB 实际构建记录为准。

## Workflow 校验器

`.agent/engine` 是开发协作使用的独立 Go Module，业务服务运行和根模块测试不依赖它。维护协作流程时单独测试：

```bash
make test-engine
```

该模块依赖 `gopkg.in/yaml.v3`，首次运行需要下载依赖或已有缓存。入口规范见 [AGENTS.md](../AGENTS.md)。
