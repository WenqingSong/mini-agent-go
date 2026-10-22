# 对话页面

React + TypeScript + Vite，Radix UI 提供菜单、对话框和提示组件，Lucide 提供图标，react-markdown / remark-gfm 渲染回答。深浅主题参考 ChatGPT 的中性色；输入框和浮层使用 CSS 透明度、模糊与高光实现玻璃质感，不依赖原生 Apple UI。

## 使用

仓库根目录 `make init`，填好 `.env`，执行 `make up`。打开 `http://127.0.0.1:3000`；CNB 云开发使用 3000 端口预览。无需在本机安装 Node.js。

支持新建/切换/删除对话、发送与停止等待、Markdown、复制回复、浅色/深色/跟随系统、移动端侧栏。接口返回完整回答，页面在等待期间显示“正在思考”，没有流式输出。页面上“服务已连接”只说明健康检查正常，不代表模型配置已验证。

## 会话与数据

- `mini-agent.chats.v1`：当前浏览器的 localStorage 中保存会话标题、后端 Session ID、消息与选择位置，不保存密钥。
- `mini-agent.theme`：外观偏好。存储写入失败会显示提示，不假定已保存。
- 刷新后继续使用原 Session ID；服务重启/会话过期时，已有记录保留，页面提示新建对话。
- 请求中途刷新、断网或主动停止等待时，无法确认服务端是否已提交。该对话保留供阅读，新建对话后再发起请求，不自动重放消息或触发额外模型调用。
- 删除只移除浏览器记录；后端没有会话删除接口。换浏览器、域名或端口不会共享本地记录。历史仍会随正常请求处理进入后端内存与模型上下文。

## 代理与容器

`server.mjs` 使用 Node 标准库提供静态文件，并将 `/api/healthz`、`/api/v1/sessions`、`/api/v1/chat` 转发到 `MINI_AGENT_BACKEND_URL`（默认 `http://mini-agent:8080`）。它在服务端注入 `MINI_AGENT_AUTH_TOKEN`；模型 API Key 只在 Go 容器中。浏览器同源访问，无需修改 Go API 的 CORS 或认证。

代理只转发固定路由和方法，保留 API 错误码，将客户端断连传到后端；跨站 Origin 的 POST 被拒绝。Compose 对外绑定回环地址，前端面向本地或受控开发环境预览，没有独立用户认证。重启前端不影响浏览器存储，也不会主动重建后端会话。

## 开发与检查

本机需要 Node.js 24。先从仓库根目录运行前后端容器，再启动 Vite；开发服务器会将 `/api` 代理到本机的 3000 端口：

```bash
npm --prefix web ci
npm --prefix web run dev
```

如果通过 `MINI_AGENT_WEB_PORT` 更改了容器端口，相应调整 `vite.config.ts` 的代理目标。仅前端源文件变化时可使用上述热更新；容器产物更新需重新 `make up`。

```bash
make web-check   # 存储恢复、代理边界/取消测试 + TypeScript 检查 + 生产构建
make web-image   # 同一套检查在 Node.js 24 容器内执行，产出 mini-agent-web:local
make ci          # Go 构建、静态检查、测试与 race
```

关键测试入口：`test/model.test.ts` 验证刷新恢复及未确认请求的处理；`test/server.test.mjs` 验证真实 HTTP 转发、凭证边界、错误保留和取消传播。测试不调用真实模型。

CNB 分支/PR 检查执行 Go 检查与前端镜像构建；main 发布时，前端使用同一仓库的 `web-latest`、`web-v<VERSION>-<SHA>` 标签，后端继续使用 `latest`、`v<VERSION>-<SHA>`。源码中的流水线配置不等于已完成远端镜像发布。
