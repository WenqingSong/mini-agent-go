# 运行指南

首次使用按 [README](../README.md)：`make init → 编辑 .env → make up`。本页说明镜像来源、运行位置与故障排查。

## 初始化与启动模式

```bash
make init             # 交互选择，回车默认 image
make init MODE=image  # 非交互：使用 CNB latest
make init MODE=local  # 非交互：当前源码构建
```

初始化检查 Docker CLI、Compose v2 和 curl，创建权限为 0600 的 `.env`；已有文件逐字保留。模式只以 `image` 或 `local` 保存在 `.mini-agent-mode`，不含密钥、不执行该文件内容，且被 Git 和 Docker 构建忽略。再次初始化可切换模式，实际容器更新发生在下一次 `make up`。

| 模式 | `make up` 的动作 | 平台 |
| --- | --- | --- |
| `image` | Compose 拉取 `docker.cnb.cool/framework-station/mini-agent-go:latest`，然后 `up --no-build --pull never` | 当前发布物为 `linux/amd64`；ARM 主机需要模拟支持 |
| `local` | Compose 用根 Dockerfile 构建 `mini-agent-go:local`，然后 `up --no-build --pull never` | 默认使用 Docker daemon 的平台 |

两条路径共用 [compose.yaml](../compose.yaml) 的服务、`.env`、端口和监听配置，只有 [image](../compose.image.yaml) / [local](../compose.local.yaml) 覆盖文件决定镜像来源。切换模式仍使用同一 Compose 项目和 `mini-agent` 服务，不新增前端容器。`make down / logs / ps` 自动使用已保存模式；升级前没有模式文件时也可管理原有 Compose 容器。

运行时只有一个 Go 进程，同时提供 `/`、静态资源、`/healthz`、`/api/*` 和 `/v1/*`。Dockerfile 的 Node.js 24 阶段执行前端测试与构建，Go 1.24 阶段编译后端，Alpine 运行阶段只保留程序、页面和 CA 证书，以 UID 10001 运行，不运行 Node。

## 配置与地址

`.env` 使用模板中的单行 `KEY=value`。默认只需填写模型 API Key 与 HTTP Token；更换 Provider 要同步修改 BaseURL、Model、API Key。不要 `source .env`。预检查按模板读取，最终解析与校验由 Compose 和应用完成；变量完整语义见 [HTTP API 配置表](design/http-api.md#8-配置契约)。

Compose **始终覆盖**容器内的 `MINI_AGENT_LISTEN_ADDR=0.0.0.0:8080`，即使已有 `.env` 写成回环地址也不会让容器只在内部可达。宿主机仅映射 `127.0.0.1:8080:8080`。Go 二进制直接运行时仍默认监听 `127.0.0.1:8080`，没有改动该安全默认值。

```bash
MINI_AGENT_HOST_PORT=9090 make up
curl -fsS http://127.0.0.1:9090/healthz
```

后续 `make up` 也应带相同端口设置。应用检查在默认 90 秒内验证容器为 `running healthy`、宿主机 `/healthz` 成功及页面包含 React 根节点；curl 每次有连接和响应超时。失败返回非零并指出容器状态或访问失败项，不会仅凭容器内部 Healthy 宣布成功。需要更长时间时可用 `MINI_AGENT_STARTUP_TIMEOUT=120 make up`。

`/v1/*` 要求 HTTP Bearer Token；浏览器通过同源 `/api/*` 调用，凭证由 Go 内部注入。页面没有独立登录体系，适用于本地或受控开发环境预览。模型 API Key 与 HTTP Token 均不进入前端构建；不要把预览端口公开给不受信任的访问者。

服务端 Session / Summary / Todo 只在内存中；容器重启后丢失。浏览器本地记录可阅读，但不恢复服务端记忆。`make init / check / up` 均不调用模型。

## 开发容器与 Docker daemon

可选开发镜像：`docker.cnb.cool/arthur-cloud-native/jetbrains/goland:5.0.1`。它与正式应用镜像是两个不同用途的制品。

[`.cnb.yml`](../.cnb.yml) 的 `vscode` 环境声明 `vscode`、`docker` 配套服务，并执行 `go version`、`git --version`、`docker version`、`docker compose version`。云开发工作区中仍应确认：

```bash
docker version         # 必须同时能连接 Server，仅有 Client 不够
docker compose version
docker info
```

本地仅执行 `docker pull docker.cnb.cool/arthur-cloud-native/jetbrains/goland:5.0.1` 只下载开发镜像。自行装配时必须提供源码工作区、Git / Make / curl、Docker CLI / Compose，以及可访问且有权限的 daemon / Socket。连接宿主机 Socket 和启动独立 DinD daemon 是不同方案，仓库没有提供一键部署该开发环境的编排文件，也不假定镜像自身会启动 daemon。

端口属于 **Docker daemon 所在主机的网络环境**。Docker Desktop 通常把发布端口转到本机；自建远端 daemon 或容器化 DinD 时，需让运行 `make up` 的终端能够访问 daemon 发布的回环端口，并配置相应的端口预览。CNB 使用工作区的 8080 端口预览；不要把它与自己电脑的 `127.0.0.1` 混淆。

## 直接开发源码

体验应用不需要这些工具；仅宿主机开发时需要 Go 1.24+、race 所需 C 工具链，前端开发需要 Node.js 24。

```bash
make build
# 在当前进程环境中设置模型配置与 HTTP Token（程序不会自动加载 .env）。
# 可选：设置 MINI_AGENT_WEB_DIR=web/dist 提供已构建页面。
./bin/mini-agent
```

二进制默认回环监听，允许无认证的本地兼容 Provider 使用空 API Key；容器快速启动路径要求填好 `.env` 顶部四项。前端热更新与浏览器测试配置见 [web/README.md](../web/README.md)。

## 常见问题

| 现象 | 处理 |
| --- | --- |
| 未选择启动模式 / 无终端输入 | `make init MODE=image` 或 `make init MODE=local` |
| `.env` 仍含占位符 | 填写两种不同用途的凭证后运行 `make check` |
| Docker daemon 不可用 | 启动 Docker Desktop / Engine，检查 Socket 或远端访问权限 |
| CNB 镜像拉取失败 | 检查 Registry 网络与访问权限；失败不会自动退回源码构建，可显式重新初始化为 local |
| ARM 主机无法运行 AMD64 | 启用 Docker Desktop 模拟支持，或用 `make init MODE=local` |
| 容器 Healthy，宿主机不可达 | 检查 daemon 所在位置、端口转发，以及 Compose 的容器监听覆盖；`make up` 会明确失败 |
| 端口已占用 | 指定空闲的 `MINI_AGENT_HOST_PORT`，或自行停止占用端口的服务 |
| 健康检查正常但聊天失败 | 健康检查不探测模型；核对 BaseURL / Model / API Key 与 Provider 网络 |
| `401 unauthorized` | `/v1/*` 使用的是 HTTP Token，不是模型 API Key；README 提供隐藏输入方式 |
| `404 session_not_found` | 先创建 Session；服务重启后旧 Session 不存在 |
| `502 provider_failure` / `504 deadline_exceeded` | 查看对应 Run 日志、模型响应与请求超时配置 |
| `429 llm_call_limit` / `tool_call_limit` | 本次 Run 达到预算，缩小问题范围或调整预算 |

BaseURL 支持兼容根地址及完整 `/chat/completions` 端点。当前客户端移除 URL 的 query / fragment，依赖 `?api-version=...` 的服务不能直接按此方式接入。

## 构建、发布与证据

`.cnb.yml` 配置分支 push / PR 执行 `make ci` 与前端构建；develop 额外完整构建并检查镜像文件结构；main 在检查、完整镜像构建和容器冒烟通过后发布一个应用镜像：

```text
docker.cnb.cool/framework-station/mini-agent-go:latest
docker.cnb.cool/framework-station/mini-agent-go:v<VERSION>-<提交短SHA>
```

两个标签来自同一构建产物；精确锁定制品时以实际 digest 为准。GitHub 自动同步由仓库侧另行配置，本任务没有修改同步机制、VERSION 或发布触发条件。当前仓库源码可能领先于 latest，使用当前改动请选择 local，直到相应版本正式发布。

本次两种启动模式的实测与已知限制见[交付报告](developer-onboarding-delivery.md)。既有 Runtime、浏览器及真实模型证据见[测试与验收](verification.md)。
