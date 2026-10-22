# 对话页面

React + TypeScript + Vite，Radix UI 提供菜单、对话框和提示组件，Lucide 提供图标，react-markdown / remark-gfm 渲染回答。深浅主题参考 ChatGPT 的中性色；输入框和浮层使用 CSS 透明度、模糊与高光实现玻璃质感，不依赖原生 Apple UI。

## 使用

仓库根目录 `make init`，填好 `.env`，执行 `make up`。打开 `http://127.0.0.1:3000`；CNB 云开发使用 3000 端口预览。无需在本机安装 Node.js。

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
make web-check   # 存储恢复、API 契约、代理边界/取消测试 + TypeScript 检查 + 生产构建
make web-image   # 同一套检查在 Node.js 24 容器内执行，产出 mini-agent-web:local
make ci          # Go 构建、静态检查、测试与 race
```

关键测试入口：

- `test/model.test.ts`：存档校验、刷新恢复、存储失败、`newId` 在非安全上下文的降级。
- `test/api.test.ts`：对照 Go 侧真实契约检查请求形状，以及每类错误码 / 非 JSON / 字段缺失 / 取消的处理结果。
- `test/server.test.mjs`：真实 HTTP 转发、凭证边界、错误保留和取消传播。
- `test/e2e/e2e.mjs`：浏览器 E2E（见下节），不在 `npm run check` 内。

以上测试都不调用真实模型。

## 浏览器 E2E

`test/e2e/` 用真实链路验证页面：可脚本化 Provider（`mock-provider.mjs`）→ 真实 `cmd/mini-agent` 进程 → 真实代理（`server.mjs`）→ Chromium。

```bash
npm --prefix web run build
npm --prefix web i -D playwright && npx playwright install chromium   # 首次执行
npm --prefix web run test:e2e                                        # 或 E2E_ONLY=工具 ... 只跑匹配场景
```

覆盖 29 个场景：首屏与连接指示、创建会话、多轮对话、多会话切换与并发返回顺序颠倒、五种内置工具（calculator / mock_search / todo_create / todo_list / todo_complete）、Provider 502、429、504、会话过期、网络中断与恢复、会话创建失败、坏 JSON 响应、401 凭证错误、500 兜底、输入边界与 Shift+Enter、Markdown 与 XSS、空回复、快速重复提交、刷新恢复、请求途中刷新、删除进行中的会话、非安全上下文（局域网 http）、移动端布局、长对话滚动、健康检查挂起，以及“前端产物与浏览器存储不含服务凭证 / Provider 密钥”。

该套件需要 Go 与 Chromium，CI 镜像里没有浏览器，因此不进入 `npm run check`。失败时会把截图与状态快照写入 `/tmp/mini-agent-e2e`。

CNB 分支/PR 检查执行 Go 检查与前端镜像构建；main 发布时，前端使用同一仓库的 `web-latest`、`web-v<VERSION>-<SHA>` 标签，后端继续使用 `latest`、`v<VERSION>-<SHA>`。源码中的流水线配置不等于已完成远端镜像发布。
