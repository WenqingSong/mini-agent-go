# 对话页面

React + TypeScript + Vite，Radix UI 提供菜单、对话框和提示组件，Lucide 提供图标，react-markdown / remark-gfm 渲染回答。深浅主题参考 ChatGPT 的中性色；输入框和浮层使用 CSS 透明度、模糊与高光实现玻璃质感，不依赖原生 Apple UI。

## 使用

仓库根目录 `make init`，填好 `.env`，执行 `make up`。打开 `http://127.0.0.1:8080`；CNB 云开发使用 8080 端口预览。无需在本机安装 Node.js。

支持新建/切换/删除对话、发送与停止等待、Markdown、复制回复、浅色/深色/跟随系统、移动端侧栏。接口返回完整回答，页面在等待期间显示“正在思考”，没有流式输出。页面上“服务已连接”只说明健康检查正常，不代表模型配置已验证。

## 会话与数据

- `mini-agent.chats.v1`：当前浏览器的 localStorage 中保存会话标题、后端 Session ID、消息与选择位置，不保存密钥。
- `mini-agent.theme`：外观偏好。存储写入失败会显示提示，不假定已保存。
- 刷新后继续使用原 Session ID；服务重启/会话过期时，已有记录保留，页面提示新建对话。
- 请求中途刷新、断网或主动停止等待时，无法确认服务端是否已提交。该对话保留供阅读，新建对话后再发起请求，不自动重放消息或触发额外模型调用。
- 删除只移除浏览器记录；后端没有会话删除接口。换浏览器、域名或端口不会共享本地记录。历史仍会随正常请求处理进入后端内存与模型上下文。

## 页面如何被提供

构建产物（`vite build` 生成的 `dist/`）在镜像构建时被拷贝到运行镜像的 `/app/web`，由 Go 进程在同一个端口提供，**没有独立的前端服务**：

- `/` 与未知的无扩展名路径返回 `index.html`（SPA fallback），刷新或直接访问页面路径不会 404；
- `/assets/*` 按内容哈希长期缓存（`immutable`），`index.html` 使用 `no-cache`，避免换镜像后新旧资源混用；
- 浏览器只调用同源 `/api/healthz`、`/api/v1/sessions`、`/api/v1/chat`，Go 进程复用原有 API 处理器并在服务端注入 `MINI_AGENT_AUTH_TOKEN`，模型 API Key 只在容器环境变量中；
- `/v1/*` 与 `/api/*` 优先于静态资源匹配，未知 API 路径返回 JSON 404，不会被页面吃掉；跨站写入在代理入口即被拒绝。

端口只发布到宿主机回环地址，当前没有访客登录体系。重启容器不影响浏览器存储，但服务端会话在内存中，会随之清空。

## 开发与检查

本机需要 Node.js 24。先从仓库根目录 `make up` 启动应用容器，再启动 Vite；开发服务器会把 `/api` 代理到本机的 8080 端口（可用 `MINI_AGENT_DEV_BACKEND` 覆盖）：

```bash
npm --prefix web ci
npm --prefix web run dev
```

如果通过 `MINI_AGENT_HOST_PORT` 更改了容器端口，用 `MINI_AGENT_DEV_BACKEND=http://127.0.0.1:<端口> npm --prefix web run dev` 指定代理目标。仅前端源文件变化时可使用上述热更新；容器产物更新需重新 `make up`。

```bash
make web-check   # 浏览器存储恢复测试 + TypeScript 检查 + 生产构建
make web-stage   # 同一套检查在 Node.js 24 容器内执行（只跑应用镜像的前端阶段）
make ci          # Go 构建、静态检查、测试与 race
```

关键测试入口：`test/model.test.ts` 验证刷新恢复及未确认请求的处理。同源代理的转发、凭证注入、跨站拒绝与路由隔离由 Go 侧用例覆盖（`httpapi/webui_test.go`），测试不调用真实模型。

CNB 分支/PR 检查执行 Go 检查与前端阶段构建；`main` 发布时只推送一个应用镜像：`v<VERSION>-<SHA>` 与 `latest` 指向同一构建产物（页面已在镜像内）。源码中的流水线配置不等于已完成远端镜像发布。
