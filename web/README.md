# 对话页面

React + TypeScript + Vite，Radix UI 提供菜单、对话框和提示组件，Lucide 提供图标，react-markdown / remark-gfm 渲染回答。深浅主题参考 ChatGPT 的中性色；输入框和浮层使用 CSS 透明度、模糊与高光实现玻璃质感，不依赖原生 Apple UI。

## 使用

仓库根目录 `make init`，填好 `.env`，执行 `make up`。打开 `http://127.0.0.1:8080`；CNB 云开发使用 8080 端口预览。无需在本机安装 Node.js。

支持新建/切换/删除对话、发送与停止等待、Markdown、复制回复、浅色/深色/跟随系统、移动端侧栏。接口返回完整回答，页面在等待期间显示“正在思考”，没有流式输出。页面上“服务已连接”只说明健康检查正常，不代表模型配置已验证。

## 会话与数据

- `mini-agent.chats.v1`：当前浏览器的 localStorage 中保存会话标题、后端 Session ID、消息与选择位置，不保存密钥。
- `mini-agent.theme`：外观偏好。存储写入失败会显示提示，不假定已保存。
- 刷新后继续使用原 Session ID；服务重启/会话过期时，已有记录保留，页面提示新建对话。
- 主动停止等待只取消本次请求：服务端整轮丢弃该 Turn，页面保留已显示的内容，可以直接在同一会话继续追问。
- 请求中途刷新或断网时无法确认服务端是否已提交。该对话保留供阅读，新建对话后再发起请求，不自动重放消息或触发额外模型调用。会话创建失败（聊天请求尚未发出）不在此列，可以直接重试。
- 删除只移除浏览器记录；后端没有会话删除接口。换浏览器、域名或端口不会共享本地记录。历史仍会随正常请求处理进入后端内存与模型上下文。
- 回复结束后输入框重新获得焦点，连续追问不需要再次点击；移动端侧栏可用 Esc 或遮罩关闭。
- 通过局域网 IP 以 `http` 打开时（非安全上下文，没有 `crypto.randomUUID` 与 Clipboard API），页面仍可发送消息，复制按钮会退化为“请手动选择文字”。

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
make web-check   # 单元测试 + TypeScript 检查 + 生产构建
make web-stage   # 同一套检查在 Node.js 24 容器内执行（只跑应用镜像的前端阶段）
make ci          # Go 构建、静态检查、测试与 race
```

`npm test` 执行 `test/*.test.ts` 全部单元测试；`npm run test:e2e` 执行浏览器 E2E（不在 `npm run check` 内，需要 Go 与 Chromium）。关键测试入口：

- `test/model.test.ts`：存档校验、刷新恢复、存储失败、`newId` 在非安全上下文的降级。
- `test/api.test.ts`：对照 Go 侧真实契约检查请求形状，以及每类错误码 / 非 JSON / 字段缺失 / 取消的处理结果。
- `test/e2e/e2e.mjs`：浏览器 E2E（见下节）。
- 同源 `/api/*` 的转发、凭证注入、跨站拒绝与路由隔离由 Go 侧用例覆盖（`httpapi/webui_test.go`、`cmd/mini-agent/main_test.go`）。

以上测试都不调用真实模型。

## 浏览器 E2E

`test/e2e/` 用真实单镜像链路验证页面：可脚本化 Provider（`mock-provider.mjs`）→ 真实 `cmd/mini-agent` 进程（同一端口提供页面、`/api` 与 `/v1`）→ Chromium。**没有 Node BFF 或反向代理参与。**

```bash
npm --prefix web ci
npm --prefix web i -D playwright && npx playwright install chromium   # 首次执行
npm --prefix web run test:e2e                                        # 缺 dist 时自动先 build；E2E_ONLY=工具 只跑匹配场景

# 同一套场景直接跑在最终镜像的容器里（默认跑本地构建的二进制）
MINI_AGENT_E2E_IMAGE=mini-agent-go:final-test npm --prefix web run test:e2e
```

共 31 个场景，覆盖：首屏与连接指示、创建会话、多轮对话、多会话切换与并发返回顺序颠倒、五种内置工具（calculator / mock_search / todo_create / todo_list / todo_complete）、Provider 502、429、504、会话过期、网络中断与恢复、会话创建失败、坏 JSON 响应、响应缺字段、404、401、500、502 兜底、输入边界与 Shift+Enter、Markdown 与 XSS、空回复、快速重复提交、刷新恢复、请求途中刷新、删除进行中的会话、非安全上下文（局域网 http）、移动端布局、长对话滚动、健康检查挂起，以及“前端产物与浏览器存储不含服务凭证 / Provider 密钥”。

其中 401、404、坏 JSON、502、500 与健康检查挂起由 `startStubStack` 的固定响应桩产生：正式单镜像服务不会返回这些响应，用例验证的是页面自身的错误呈现与恢复；静态资源仍来自真实构建产物。

该套件需要 Go 与 Chromium，CI 镜像里没有浏览器，因此不进入 `npm run check`。失败时会把截图与状态快照写入 `/tmp/mini-agent-e2e`。

CNB 分支/PR 检查执行 Go 检查与前端阶段构建；`main` 发布时只推送一个应用镜像：`v<VERSION>-<SHA>` 与 `latest` 指向同一构建产物（页面已在镜像内）。源码中的流水线配置不等于已完成远端镜像发布。
