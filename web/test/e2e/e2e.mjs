// 浏览器端 E2E：真实 Go 后端 + 真实前端代理 + 可脚本化 Provider + Chromium。
//
// 运行方式（本机需一次性安装浏览器）：
//   npm --prefix web run build
//   npx --yes playwright@latest install chromium
//   node web/test/e2e/e2e.mjs            # 或 E2E_ONLY=工具 node web/test/e2e/e2e.mjs
//
// 该脚本不属于 `npm run check`：CI 镜像里没有浏览器，构建检查只跑单元/集成测试。
import assert from 'node:assert/strict'
import { mkdir, readdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { startMockProvider } from './mock-provider.mjs'
import { buildBackend, demoAuthToken, demoProviderKey, distDir, startStack, startStubStack } from './stack.mjs'

let chromium
try {
  ({ chromium } = await import('playwright'))
} catch {
  console.error('✗ 未安装 playwright：请在 web 目录执行 `npm i -D playwright && npx playwright install chromium`')
  process.exit(2)
}

const artifacts = process.env.E2E_ARTIFACTS || '/tmp/mini-agent-e2e'
const composer = 'textarea[aria-label="消息"]'
const sendButton = 'button[aria-label="发送消息"]'
const stopButton = 'button[aria-label="停止回复"]'

async function waitFor(check, { timeout = 20_000, interval = 80, message = '条件未满足' } = {}) {
  const deadline = Date.now() + timeout
  let lastError
  while (Date.now() < deadline) {
    try {
      const value = await check()
      if (value) return value
    } catch (error) { lastError = error }
    await new Promise(done => setTimeout(done, interval))
  }
  throw new Error(`${message}${lastError ? `：${lastError.message}` : ''}`)
}

const storage = page => page.evaluate(() => JSON.parse(localStorage.getItem('mini-agent.chats.v1') || 'null'))

function recordApi(page) {
  const calls = []
  page.on('request', request => {
    const url = new URL(request.url())
    if (url.pathname.startsWith('/api/')) calls.push({ path: url.pathname, method: request.method(), body: request.postData() })
  })
  return calls
}

async function send(page, text) {
  await page.fill(composer, text)
  await page.press(composer, 'Enter')
}

async function waitReply(page, expected) {
  await waitFor(async () => {
    const texts = await page.locator('.assistant-row .markdown').allInnerTexts()
    return texts.length > 0 && (expected === undefined || texts.at(-1).includes(expected))
  }, { message: `未等到助手回复${expected ? `包含「${expected}」` : ''}` })
  return page.locator('.assistant-row .markdown').last().innerText()
}

async function waitError(page, expected) {
  await waitFor(async () => {
    const texts = await page.locator('.assistant-row .message-error').allInnerTexts()
    return texts.length > 0 && (expected === undefined || texts.at(-1).includes(expected))
  }, { message: `未等到错误提示${expected ? `包含「${expected}」` : ''}` })
  return page.locator('.assistant-row .message-error').last().innerText()
}

const chatsOf = list => list.path === '/api/v1/chat'
const sessionsOf = list => list.path === '/api/v1/sessions'

async function startConversation(ctx, text, reply) {
  await send(ctx.page, text)
  return waitReply(ctx.page, reply)
}

const scenarios = [
  {
    name: '首屏：欢迎态、连接指示、无 console 错误',
    async run({ page, stack, issues }) {
      await page.goto(stack.url)
      await page.waitForSelector('.welcome h1')
      assert.equal((await page.locator('.welcome h1').innerText()).trim(), '有什么可以帮忙的？')
      assert.equal(await page.locator('.suggestions button').count(), 3)
      assert.equal(await page.locator(sendButton).isDisabled(), true)
      await waitFor(async () => (await page.locator('button.connection').innerText()).includes('服务已连接'), { message: '连接指示未变为已连接' })
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      assert.ok(overflow <= 0, `首屏出现横向溢出 ${overflow}px`)
      // 推荐问题只填入草稿，不应触发任何请求
      await page.locator('.suggestions button').first().click()
      assert.ok((await page.locator(composer).inputValue()).includes('计算'))
      // 外观切换写入偏好并立即生效
      await page.click('button[aria-label="选择外观"]')
      await page.locator('.menu-item').filter({ hasText: '深色' }).click()
      assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), 'dark')
      assert.equal(await page.evaluate(() => localStorage.getItem('mini-agent.theme')), 'dark')
      assert.deepEqual(issues.pageerrors, [])
    },
  },
  {
    name: '创建会话与首轮对话：请求契约、会话 ID 保存、消息渲染',
    async run({ page, stack, provider }) {
      await provider.script([{ kind: 'text', text: '你好，我是 Mini Agent。' }])
      const api = recordApi(page)
      await page.goto(stack.url)
      await send(page, '你好')
      await waitReply(page, '你好，我是 Mini Agent。')
      const archive = await storage(page)
      assert.equal(archive.chats.length, 1)
      assert.match(archive.chats[0].sessionId, /^[0-9a-f]{32}$/)
      assert.equal(archive.chats[0].messages.length, 2)
      assert.equal(archive.chats[0].messages[0].content, '你好')
      assert.equal(archive.chats[0].messages[1].state, 'done')
      assert.equal(archive.chats[0].title, '你好')
      const session = api.filter(sessionsOf)
      const chat = api.filter(chatsOf)
      assert.equal(session.length, 1, '创建会话请求应当只发一次')
      assert.equal(chat.length, 1, '聊天请求应当只发一次')
      assert.deepEqual(JSON.parse(session[0].body), {})
      assert.deepEqual(JSON.parse(chat[0].body), { session_id: archive.chats[0].sessionId, message: '你好' })
      const requests = provider.requests()
      assert.equal(requests.length, 1)
      assert.equal(requests[0].model, 'e2e-scripted-model')
      assert.deepEqual(requests[0].messages.map(m => m.role), ['system', 'user'])
      assert.equal(requests[0].messages.at(-1).content, '你好')
    },
  },
  {
    name: '多轮对话：第二轮复用同一 Session ID 并带上历史',
    async run({ page, stack, provider }) {
      await provider.script([{ kind: 'text', text: '第一轮回答' }, { kind: 'text', text: '第二轮回答' }])
      const api = recordApi(page)
      await page.goto(stack.url)
      await startConversation({ page }, '第一轮问题', '第一轮回答')
      assert.equal(await page.evaluate(() => document.activeElement?.tagName), 'TEXTAREA', '回复结束后焦点应回到输入框')
      const first = await storage(page)
      await startConversation({ page }, '第二轮问题', '第二轮回答')
      const second = await storage(page)
      assert.equal(second.chats[0].sessionId, first.chats[0].sessionId)
      assert.equal(api.filter(sessionsOf).length, 1, '第二轮不应重新创建会话')
      const chats = api.filter(chatsOf).map(call => JSON.parse(call.body))
      assert.equal(chats.length, 2)
      assert.equal(chats[1].session_id, first.chats[0].sessionId)
      const requests = provider.requests()
      assert.equal(requests.length, 2)
      assert.deepEqual(requests[1].messages.map(m => m.role), ['system', 'user', 'assistant', 'user'])
      assert.equal(requests[1].messages[2].content, '第一轮回答')
    },
  },
  {
    name: '多会话：新建第二个会话、切换会话不串消息',
    async run({ page, stack, provider }) {
      await provider.script([{ kind: 'text', text: '答复一' }, { kind: 'text', text: '答复二' }, { kind: 'text', text: '答复三' }])
      const api = recordApi(page)
      await page.goto(stack.url)
      await startConversation({ page }, '第一个会话', '答复一')
      const firstSession = (await storage(page)).chats[0].sessionId
      await page.click('aside .new-chat')
      await page.waitForSelector('.welcome h1')
      await startConversation({ page }, '第二个会话', '答复二')
      let archive = await storage(page)
      assert.equal(archive.chats.length, 2)
      const secondSession = archive.chats.find(chat => chat.sessionId !== firstSession).sessionId
      assert.notEqual(secondSession, firstSession)

      await page.locator('aside .history-link').filter({ hasText: '第一个会话' }).first().click()
      await waitFor(async () => (await page.locator('.user-message').allInnerTexts()).join('|') === '第一个会话', { message: '切换会话后未显示第一个会话的消息' })
      assert.equal(await page.locator('.user-message').count(), 1, '切换会话后不应出现其他会话的消息')

      await startConversation({ page }, '第一个会话的第二轮', '答复三')
      archive = await storage(page)
      const chatA = archive.chats.find(chat => chat.sessionId === firstSession)
      const chatB = archive.chats.find(chat => chat.sessionId === secondSession)
      assert.equal(chatA.messages.length, 4)
      assert.equal(chatB.messages.length, 2)
      const chatCalls = api.filter(chatsOf).map(call => JSON.parse(call.body))
      assert.deepEqual(chatCalls.map(call => call.session_id), [firstSession, secondSession, firstSession])
      assert.equal(api.filter(sessionsOf).length, 2, '两个会话应各创建一次 Session')
    },
  },
  {
    name: '工具调用：真实 calculator 执行并只展示调用计数，不把工具结果当回答',
    async run({ page, stack, provider }) {
      await provider.script([
        { kind: 'tool', calls: [
          { name: 'calculator', arguments: { op: 'mul', a: 37, b: 48 } },
          { name: 'mock_search', arguments: { query: 'mini agent' } },
          { name: 'todo_create', arguments: { title: '验证前端' } },
        ] },
        { kind: 'text', text: '37 × 48 = 1776。' },
        { kind: 'tool', calls: [
          { name: 'todo_list', arguments: {} },
          { name: 'todo_complete', arguments: { id: 'todo-1' } },
        ] },
        { kind: 'text', text: '待办已完成。' },
      ])
      await page.goto(stack.url)
      await send(page, '请使用计算工具计算 37 × 48。')
      await waitReply(page, '37 × 48 = 1776。')
      assert.equal((await page.locator('.tool-badge').last().innerText()).trim(), '使用了 3 次工具')
      let archive = await storage(page)
      const assistant = archive.chats[0].messages[1]
      assert.equal(assistant.toolCalls, 3)
      assert.equal(assistant.llmCalls, 2)
      assert.equal(assistant.content, '37 × 48 = 1776。')
      const requests = provider.requests()
      assert.equal(requests.length, 2)
      assert.deepEqual(requests[1].messages.map(m => m.role), ['system', 'user', 'assistant', 'tool', 'tool', 'tool'])
      assert.match(requests[1].messages.at(-3).content, /1776/)
      assert.deepEqual([...requests[0].tools].sort(), ['calculator', 'mock_search', 'todo_complete', 'todo_create', 'todo_list'])

      await startConversation({ page }, '再查一下待办', '待办已完成。')
      assert.equal((await page.locator('.tool-badge').last().innerText()).trim(), '使用了 2 次工具')
      archive = await storage(page)
      assert.equal(archive.chats[0].messages[3].toolCalls, 2)
      const todoResult = provider.requests().at(-1).messages.at(-1).content
      assert.match(todoResult, /todo-1/)
      assert.match(todoResult, /completed/)
    },
  },
  {
    name: 'Provider 502：显示明确错误，同一会话可继续',
    async run({ page, stack, provider, issues }) {
      await provider.script([{ kind: 'status', status: 500 }, { kind: 'text', text: '重试成功' }])
      const api = recordApi(page)
      await page.goto(stack.url)
      await send(page, '会失败的问题')
      const error = await waitError(page, '模型服务暂时无法回答')
      assert.ok(error.includes('模型服务暂时无法回答'))
      assert.equal(await page.locator('.thinking').count(), 0, '失败后不应停留在加载态')
      assert.equal(await page.locator(composer).isDisabled(), false, '失败后输入框应可用')
      assert.equal(await page.locator('.expired-notice').count(), 0, '偶发 Provider 失败不应锁死会话')
      await provider.script([{ kind: 'text', text: '重试成功' }])
      await startConversation({ page }, '再来一次', '重试成功')
      assert.equal(api.filter(sessionsOf).length, 1, '继续同一会话不应重新创建 Session')
      assert.equal(issues.pageerrors.length, 0)
    },
  },
  {
    name: '用户停止等待：中断后可继续同一会话',
    async run({ page, stack, provider }) {
      await provider.script([{ kind: 'block' }, { kind: 'text', text: '继续成功' }])
      const api = recordApi(page)
      await page.goto(stack.url)
      await send(page, '很慢的问题')
      await page.waitForSelector('.thinking')
      await page.click(stopButton)
      await waitError(page, '已停止等待回复')
      assert.equal(await page.locator('.thinking').count(), 0)
      assert.equal(await page.locator(composer).isVisible(), true, '停止后应能继续输入')
      assert.equal(await page.locator(composer).isDisabled(), false)
      await provider.script([{ kind: 'text', text: '继续成功' }])
      await startConversation({ page }, '停止后继续', '继续成功')
      assert.equal(api.filter(sessionsOf).length, 1, '停止后继续同一会话不应重新创建 Session')
    },
  },
  {
    name: '429 模型调用上限：错误可读且会话可继续',
    stackOptions: { maxLLMCalls: 1 },
    async run({ page, stack, provider }) {
      await provider.script([{ kind: 'tool', calls: [{ name: 'calculator', arguments: { op: 'add', a: 1, b: 1 } }] }])
      await page.goto(stack.url)
      await send(page, '先调用工具再总结')
      await waitError(page, '模型调用次数上限')
      assert.equal(await page.locator(composer).isDisabled(), false)
      await provider.script([{ kind: 'text', text: '预算恢复后的回答' }])
      await startConversation({ page }, '再问一次', '预算恢复后的回答')
    },
  },
  {
    name: '504 请求超时：提示超时且会话可继续',
    stackOptions: { requestTimeout: '1s' },
    async run({ page, stack, provider }) {
      await provider.script([{ kind: 'block' }, { kind: 'text', text: '超时后恢复' }])
      await page.goto(stack.url)
      await send(page, '会超时的问题')
      await waitError(page, '这次回答超时了')
      assert.equal(await page.locator(composer).isDisabled(), false)
      await startConversation({ page }, '超时后再问', '超时后恢复')
    },
  },
  {
    name: '会话过期（后端重启）：提示明确，新建对话后可恢复',
    async run({ page, stack, provider }) {
      await provider.script([{ kind: 'text', text: '重启前的回答' }, { kind: 'text', text: '新会话的回答' }])
      const api = recordApi(page)
      await page.goto(stack.url)
      await startConversation({ page }, '重启前的问题', '重启前的回答')
      const oldSession = (await storage(page)).chats[0].sessionId
      await stack.restartBackend()
      await send(page, '重启后的问题')
      await waitFor(async () => (await page.locator('.expired-notice').count()) === 1, { message: '会话过期后未出现提示' })
      assert.ok((await page.locator('.expired-notice').innerText()).includes('服务会话已过期'))
      assert.ok((await waitError(page, '服务中的会话已不存在')).includes('本地记录仍然保留'))
      await page.locator('.expired-notice .text-button').click()
      await page.waitForSelector('.welcome h1')
      await startConversation({ page }, '新会话的问题', '新会话的回答')
      const archive = await storage(page)
      assert.equal(archive.chats.length, 2)
      assert.notEqual(archive.chats[0].sessionId, oldSession)
      assert.equal(api.filter(sessionsOf).length, 2)
    },
  },
  {
    name: '网络中断：提示不确定状态，恢复后可新建对话继续',
    async run({ page, stack, provider, issues }) {
      await provider.script([{ kind: 'text', text: '在线回答' }, { kind: 'text', text: '恢复后的回答' }])
      await page.goto(stack.url)
      await startConversation({ page }, '在线问题', '在线回答')
      await stack.stopProxy()
      await send(page, '断网时的问题')
      await waitError(page, '连接中断')
      await waitFor(async () => (await page.locator('button.connection').innerText()).includes('服务未连接'), { message: '断网后连接指示未变为未连接' })
      await waitFor(async () => (await page.locator('.expired-notice').count()) === 1, { message: '断网后未提示会话状态不确定' })
      await stack.restartProxy()
      await page.click('button.connection')
      await waitFor(async () => (await page.locator('button.connection').innerText()).includes('服务已连接'), { message: '恢复后健康检查未重新通过' })
      await page.locator('.expired-notice .text-button').click()
      await startConversation({ page }, '恢复后的问题', '恢复后的回答')
      assert.ok(issues.pageerrors.length === 0)
    },
    allowConsole: [/Failed to load resource/],
  },
  {
    name: '创建会话失败：返回非法 JSON 时明确报错且可重试',
    async run({ page, stack }) {
      const api = recordApi(page)
      await page.goto(stack.url)
      await send(page, '第一次尝试')
      await waitError(page, '服务未返回有效的会话')
      assert.equal(await page.locator(composer).isDisabled(), false, '创建会话失败后应能重试')
      assert.equal(await page.locator('.expired-notice').count(), 0, '未发出聊天请求时不应锁死会话')
      await send(page, '第二次尝试')
      await waitError(page, '服务未返回有效的会话')
      const archive = await storage(page)
      assert.equal(archive.chats[0].sessionId, undefined)
      assert.equal(api.filter(sessionsOf).length, 2)
      assert.equal(api.filter(chatsOf).length, 0, '会话创建失败时不应发出聊天请求')
    },
    async setupStack() {
      return startStubStack((req, res) => {
        if (req.url.startsWith('/healthz')) return res.writeHead(200, { 'Content-Type': 'application/json' }).end('{"status":"ok"}')
        res.writeHead(201, { 'Content-Type': 'application/json' }).end('{}')
      })
    },
  },
  {
    name: '聊天响应不可解析：提示不确定状态，只能新建对话',
    async run({ page, stack }) {
      await page.goto(stack.url)
      await send(page, '会收到坏响应的问题')
      await waitError(page, '服务返回了无法识别的响应')
      await waitFor(async () => (await page.locator('.expired-notice').count()) === 1, { message: '响应不可解析时未提示状态不确定' })
      await page.locator('.expired-notice .text-button').click()
      await page.waitForSelector('.welcome h1')
    },
    async setupStack() {
      return startStubStack((req, res) => {
        if (req.url === '/healthz') return res.writeHead(200, { 'Content-Type': 'application/json' }).end('{"status":"ok"}')
        if (req.url === '/v1/sessions') return res.writeHead(201, { 'Content-Type': 'application/json' }).end('{"session_id":"stub-session"}')
        res.writeHead(200, { 'Content-Type': 'application/json' }).end('oops')
      })
    },
  },
  {
    name: '凭证错误 401：明确提示并且不进入永久加载',
    stackOptions: { proxyToken: 'wrong-token' },
    async run({ page, stack }) {
      await page.goto(stack.url)
      await send(page, '凭证错误的问题')
      await waitError(page, '服务连接配置有误')
      assert.equal(await page.locator('.thinking').count(), 0)
      assert.equal(await page.locator(composer).isDisabled(), false)
      await send(page, '再试一次')
      await waitError(page, '服务连接配置有误')
      assert.equal(await page.locator('.expired-notice').count(), 0)
    },
  },
  {
    name: '输入边界：空消息、超长、Shift+Enter、Emoji 与 HTML 转义',
    async run({ page, stack, provider }) {
      await provider.script([{ kind: 'text', text: '收到' }])
      const api = recordApi(page)
      await page.goto(stack.url)
      assert.equal(await page.locator(sendButton).isDisabled(), true, '空输入时发送按钮应禁用')
      await page.fill(composer, '   ')
      assert.equal(await page.locator(sendButton).isDisabled(), true, '纯空白输入时发送按钮应禁用')

      await page.fill(composer, '第一行')
      await page.press(composer, 'Shift+Enter')
      await page.keyboard.type('第二行')
      assert.equal(await page.locator(composer).inputValue(), '第一行\n第二行')
      assert.equal(api.filter(chatsOf).length, 0, 'Shift+Enter 不应发送消息')

      await page.fill(composer, 'a'.repeat(8193))
      assert.equal(await page.locator(sendButton).isDisabled(), true, '超出 8192 字符时发送按钮应禁用')
      assert.ok((await page.locator('.composer-hint').innerText()).includes('8192'))

      await page.fill(composer, '🙂中文 <b>不是标签</b> & "引号"')
      await page.press(composer, 'Enter')
      await waitReply(page, '收到')
      const userText = await page.locator('.user-message').last().innerText()
      assert.equal(userText, '🙂中文 <b>不是标签</b> & "引号"')
      assert.equal(await page.locator('.user-message b').count(), 0, '用户输入中的 HTML 必须按纯文本渲染')
    },
  },
  {
    name: '模型输出：Markdown 渲染与 XSS 注入防护',
    async run({ page, stack, provider }) {
      await provider.script([{
        kind: 'text',
        text: '<img src=x onerror="window.__xss=1"> **加粗** [危险](javascript:alert(1)) <script>window.__xss2=1</script>',
      }])
      await page.goto(stack.url)
      await send(page, '试试注入')
      await waitReply(page, '加粗')
      assert.equal(await page.evaluate(() => window.__xss), undefined, 'img onerror 必须未被执行')
      assert.equal(await page.evaluate(() => window.__xss2), undefined, 'script 注入必须未被执行')
      assert.equal(await page.locator('.markdown img').count(), 0)
      assert.equal(await page.locator('.markdown script').count(), 0)
      assert.equal(await page.locator('.markdown strong').count(), 1)
      const href = await page.locator('.markdown a').first().getAttribute('href')
      assert.ok(!href || !href.startsWith('javascript:'), `危险链接必须被清洗，实际为 ${href}`)
      assert.equal(await page.locator('.markdown a').first().getAttribute('rel'), 'noopener noreferrer')
    },
  },
  {
    name: '空回复：显示占位说明，不伪造模型内容',
    async run({ page, stack, provider }) {
      await provider.script([{ kind: 'empty' }])
      await page.goto(stack.url)
      await send(page, '给我一个空回复')
      await waitReply(page, '模型返回了空回复')
      const archive = await storage(page)
      assert.equal(archive.chats[0].messages[1].content, '')
    },
  },
  {
    name: '快速重复提交：只产生一次会话创建与一次聊天请求',
    async run({ page, stack, provider }) {
      await provider.script([{ kind: 'slow', text: '只应回复一次', delayMs: 500 }])
      const api = recordApi(page)
      await page.goto(stack.url)
      await page.fill(composer, '快速点击测试')
      await page.$eval(sendButton, button => { button.click(); button.click() })
      await waitReply(page, '只应回复一次')
      assert.equal(api.filter(sessionsOf).length, 1)
      assert.equal(api.filter(chatsOf).length, 1)
      assert.equal(await page.locator('.assistant-row').count(), 1)
    },
  },
  {
    name: '刷新页面：记录与会话恢复，继续同一 Session',
    async run({ page, stack, provider }) {
      await provider.script([{ kind: 'text', text: '刷新前的回答' }, { kind: 'text', text: '刷新后的回答' }])
      const api = recordApi(page)
      await page.goto(stack.url)
      await startConversation({ page }, '刷新前的问题', '刷新前的回答')
      const before = await storage(page)
      await page.reload()
      await waitReply(page, '刷新前的回答')
      assert.equal((await page.locator('.user-message').innerText()).trim(), '刷新前的问题')
      const after = await storage(page)
      assert.equal(after.chats[0].sessionId, before.chats[0].sessionId)
      await startConversation({ page }, '刷新后的问题', '刷新后的回答')
      assert.equal(api.filter(sessionsOf).length, 1, '刷新后继续对话不应重建会话')
      assert.equal(JSON.parse(api.filter(chatsOf).at(-1).body).session_id, before.chats[0].sessionId)
    },
  },
  {
    name: '移动端布局：无横向溢出、侧栏可用、可发送消息',
    viewport: { width: 390, height: 844 },
    async run({ page, stack, provider }) {
      await provider.script([{
        kind: 'text',
        text: '移动端的回答\n\n```go\nfunc veryLongFunctionNameIndeed(input string) string { return input }\n```\n\n| 列一 | 列二 |\n| --- | --- |\n| 很长的内容很长的内容很长的内容很长的内容很长的内容 | 值 |',
      }])
      await page.goto(stack.url)
      await page.waitForSelector('.welcome h1')
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      assert.ok(overflow <= 0, `移动端出现横向溢出 ${overflow}px`)
      assert.equal(await page.locator('aside.sidebar').isVisible(), false, '小屏不应显示常驻侧栏')
      await page.click('button[aria-label="打开对话列表"]')
      await page.waitForSelector('.mobile-sidebar')
      assert.equal(await page.locator('.mobile-sidebar').isVisible(), true)
      await page.waitForTimeout(400)
      assert.equal(await page.locator('[role="tooltip"]').count(), 0, '侧栏打开时不应自动弹出按钮提示')
      await page.keyboard.press('Escape')
      await waitFor(async () => (await page.locator('.mobile-sidebar').count()) === 0, { message: '第一次 Esc 应关闭移动端侧栏' })
      await send(page, '移动端问题')
      await waitReply(page, '移动端的回答')
      const composerBox = await page.locator('.composer').boundingBox()
      assert.ok(composerBox.x >= 0 && composerBox.x + composerBox.width <= 390, '输入框超出视口')
      const afterOverflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      assert.ok(afterOverflow <= 0, `回复后出现横向溢出 ${afterOverflow}px`)
    },
  },
  {
    name: '长对话：滚动到底部且不遮挡输入框',
    async run({ page, stack, provider }) {
      const steps = Array.from({ length: 6 }, (_, index) => ({ kind: 'text', text: `第 ${index + 1} 轮回答\n\n- 要点一\n- 要点二` }))
      await provider.script(steps)
      await page.goto(stack.url)
      for (let index = 1; index <= 6; index += 1) {
        await send(page, `第 ${index} 轮问题`)
        await waitReply(page, `第 ${index} 轮回答`)
      }
      const scrolled = await page.evaluate(() => {
        const scroller = document.querySelector('.messages-scroll')
        return { top: scroller.scrollTop, height: scroller.scrollHeight, client: scroller.clientHeight }
      })
      assert.ok(scrolled.top > 0, '长对话应已滚动')
      assert.ok(Math.abs(scrolled.top + scrolled.client - scrolled.height) <= 4, '长对话应停在底部')
      const last = await page.locator('.assistant-row').last().boundingBox()
      const composerBox = await page.locator('.composer').boundingBox()
      assert.ok(last.y + last.height <= composerBox.y + 1, '最后一条消息不应被输入框遮挡')
    },
  },
]

scenarios.push(
  {
    name: '非安全上下文（http + 局域网 IP）：仍可发送，复制失败有提示',
    stackOptions: { listenHost: '0.0.0.0' },
    async run({ page, stack, provider }) {
      assert.ok(stack.lanURL, '本机没有可用的非回环 IPv4 地址，无法构造非安全上下文')
      await provider.script([{ kind: 'text', text: '局域网访问的回答' }])
      await page.goto(stack.lanURL)
      assert.equal(await page.evaluate(() => window.isSecureContext), false, '前提：该访问方式不是安全上下文')
      assert.equal(await page.evaluate(() => typeof crypto.randomUUID), 'undefined', '前提：randomUUID 在此上下文不可用')
      // 直接调用会抛错——这正是修复前点击发送“没有任何反应”的原因
      assert.equal(await page.evaluate(() => {
        try { crypto.randomUUID(); return 'ok' } catch (error) { return error.name }
      }), 'TypeError')
      await send(page, '局域网访问')
      await waitReply(page, '局域网访问的回答')
      const archive = await storage(page)
      assert.equal(archive.chats.length, 1)
      assert.equal(archive.chats[0].messages.length, 2)
      assert.ok(archive.chats[0].id.length >= 16, '本地会话标识必须仍然生成')
      await page.locator('.copy-action button').last().click()
      await waitFor(async () => (await page.locator('.copy-action [role="status"]').count()) === 1, { message: '复制失败时未给出可读提示' })
    },
  },
  {
    name: '请求途中刷新：保留历史并提示上次请求中断',
    async run({ page, stack, provider }) {
      await provider.script([{ kind: 'block' }])
      await page.goto(stack.url)
      await send(page, '刷新时仍在等待')
      await page.waitForSelector('.thinking')
      await page.reload()
      await waitFor(async () => (await page.locator('.assistant-row .message-error').count()) === 1, { message: '刷新后未提示上次请求中断' })
      assert.ok((await page.locator('.assistant-row .message-error').innerText()).includes('上次请求中断'))
      assert.equal((await page.locator('.user-message').innerText()).trim(), '刷新时仍在等待')
      assert.equal(await page.locator('.expired-notice').count(), 1, '无法确认提交结果时应停止续聊')
      await page.locator('.expired-notice .text-button').click()
      await page.waitForSelector('.welcome h1')
    },
  },
  {
    name: '跨会话并发：返回顺序颠倒也不串话',
    async run({ page, stack, provider }) {
      await provider.script([{ kind: 'slow', text: 'A 的回答', delayMs: 900 }, { kind: 'text', text: 'B 的回答' }])
      const api = recordApi(page)
      await page.goto(stack.url)
      await send(page, '会话 A 的问题')
      await page.waitForSelector('.thinking')
      await page.click('aside .new-chat')
      await send(page, '会话 B 的问题')
      await waitReply(page, 'B 的回答')
      await waitFor(async () => {
        const chatA = (await storage(page)).chats.find(chat => chat.title.startsWith('会话 A'))
        return chatA?.messages[1]?.content === 'A 的回答'
      }, { timeout: 15_000, message: 'A 会话的回答未落位' })
      const archive = await storage(page)
      const chatA = archive.chats.find(chat => chat.title.startsWith('会话 A'))
      const chatB = archive.chats.find(chat => chat.title.startsWith('会话 B'))
      assert.notEqual(chatA.sessionId, chatB.sessionId)
      assert.equal(chatA.messages[1].content, 'A 的回答')
      assert.equal(chatB.messages[1].content, 'B 的回答')
      const sessionIds = api.filter(chatsOf).map(call => JSON.parse(call.body).session_id)
      assert.equal(new Set(sessionIds).size, 2, '两个会话的聊天请求必须分别使用自己的 Session')
      await page.locator('aside .history-link').filter({ hasText: '会话 A' }).first().click()
      await waitReply(page, 'A 的回答')
    },
  },
  {
    name: '安全：前端产物与浏览器存储不含服务凭证 / Provider 密钥',
    async run({ page, stack, provider }) {
      const files = await readdir(distDir, { recursive: true })
      const leaked = []
      for (const file of files) {
        const text = await readFile(join(distDir, file), 'utf8').catch(() => '')
        for (const secret of [demoAuthToken, demoProviderKey]) if (text.includes(secret)) leaked.push(`${file} 含 ${secret}`)
      }
      assert.deepEqual(leaked, [], `构建产物中出现密钥：${leaked.join(' | ')}`)

      const authHeaders = []
      page.on('request', request => {
        if (new URL(request.url()).pathname.startsWith('/api/')) authHeaders.push(request.headers().authorization || null)
      })
      await provider.script([{ kind: 'text', text: '安全检查的回答' }])
      await page.goto(stack.url)
      await startConversation({ page }, '安全检查', '安全检查的回答')
      const dump = await page.evaluate(() => JSON.stringify(localStorage))
      assert.ok(!dump.includes(demoAuthToken), 'localStorage 不应保存服务凭证')
      assert.ok(!dump.includes(demoProviderKey), 'localStorage 不应保存 Provider 密钥')
      assert.deepEqual(authHeaders.filter(Boolean), [], '浏览器请求不应自行携带 Authorization')
    },
  },
  {
    name: '删除进行中的会话：中断请求且页面可继续新建',
    async run({ page, stack, provider }) {
      await provider.script([{ kind: 'block' }, { kind: 'text', text: '删除后的回答' }])
      await page.goto(stack.url)
      await send(page, '会被删除的会话')
      await page.waitForSelector('.thinking')
      await page.hover('.history-item')
      await page.click('.history-more')
      await page.click('.menu-item.danger')
      await page.waitForSelector('.confirm-dialog')
      await page.click('.danger-button')
      await waitFor(async () => (await storage(page)).chats.length === 0, { message: '删除后本地记录未清除' })
      await page.waitForSelector('.welcome h1')
      await provider.script([{ kind: 'text', text: '删除后的回答' }])
      await startConversation({ page }, '删除后的新问题', '删除后的回答')
    },
  },
  {
    name: '代理层 502：提示服务不可用，不静默失败',
    async run({ page, stack }) {
      await page.goto(stack.url)
      await waitFor(async () => (await page.locator('button.connection').innerText()).includes('服务已连接'))
      await send(page, '后端挂掉时的问题')
      await waitError(page, '暂时无法连接对话服务')
      await waitFor(async () => (await page.locator('.expired-notice').count()) === 1, { message: '提交结果未知时未停止续聊' })
      await waitFor(async () => (await page.locator('button.connection').innerText()).includes('服务未连接'), { message: '网关失败后连接指示未更新' })
      assert.equal(await page.locator('.thinking').count(), 0)
    },
    async setupStack() {
      return startStubStack((req, res) => {
        if (req.url === '/healthz') return res.writeHead(200, { 'Content-Type': 'application/json' }).end('{"status":"ok"}')
        if (req.url === '/v1/sessions') return res.writeHead(201, { 'Content-Type': 'application/json' }).end('{"session_id":"stub-session"}')
        res.writeHead(502, { 'Content-Type': 'application/json' }).end('{"error":{"code":"backend_unavailable","message":"模型服务不可用"}}')
      })
    },
  },
  {
    name: 'HTTP 500 internal：兜底提示清晰且可继续重试',
    async run({ page, stack }) {
      await page.goto(stack.url)
      await send(page, '服务内部错误')
      await waitError(page, '这次请求未能完成')
      assert.equal(await page.locator('.thinking').count(), 0)
      assert.equal(await page.locator(composer).isDisabled(), false, '内部错误后应能重试')
      assert.equal(await page.locator('.expired-notice').count(), 0, '内部错误不应锁死会话')
    },
    async setupStack() {
      return startStubStack((req, res) => {
        if (req.url === '/healthz') return res.writeHead(200, { 'Content-Type': 'application/json' }).end('{"status":"ok"}')
        if (req.url === '/v1/sessions') return res.writeHead(201, { 'Content-Type': 'application/json' }).end('{"session_id":"stub-session"}')
        res.writeHead(500, { 'Content-Type': 'application/json' }).end('{"error":{"code":"internal","message":"服务器内部错误"}}')
      })
    },
  },
  {
    name: '健康检查挂起：状态指示器不会无限显示“连接中”',
    async run({ page, stack }) {
      await page.goto(stack.url)
      await waitFor(async () => (await page.locator('button.connection').innerText()).includes('服务未连接'), { timeout: 12_000, message: '健康检查挂起后指示器未超时' })
      assert.equal(await page.locator('.thinking').count(), 0)
      await page.waitForSelector('.welcome h1')
    },
    async setupStack() {
      return startStubStack((_req, _res) => { /* /healthz 永不响应 */ })
    },
  },
)

const only = process.env.E2E_ONLY ? new RegExp(process.env.E2E_ONLY) : null
const selected = only ? scenarios.filter(scenario => only.test(scenario.name)) : scenarios

await mkdir(artifacts, { recursive: true })
await buildBackend()
const provider = await startMockProvider()
const browser = await chromium.launch()
let sharedStack
const failures = []
let passed = 0

try {
  sharedStack = await startStack(provider)
  for (const scenario of selected) {
    const context = await browser.newContext({ viewport: scenario.viewport || { width: 1280, height: 800 } })
    const page = await context.newPage()
    const issues = { console: [], pageerrors: [], failed: [], assets: [] }
    page.on('console', message => { if (message.type() === 'error') issues.console.push(message.text()) })
    page.on('pageerror', error => issues.pageerrors.push(error.message))
    page.on('requestfailed', request => issues.failed.push(`${request.url()} ${request.failure()?.errorText}`))
    page.on('response', response => {
      const path = new URL(response.url()).pathname
      // 静态资源 404/500 属于页面故障；接口错误由场景自身断言。
      if (!path.startsWith('/api/') && response.status() >= 400) issues.assets.push(`${response.status()} ${path}`)
    })

    let stack = sharedStack
    let dedicated
    const started = Date.now()
    try {
      if (scenario.setupStack) {
        dedicated = await scenario.setupStack()
        stack = dedicated
      } else if (scenario.stackOptions) {
        dedicated = await startStack(provider, scenario.stackOptions)
        stack = dedicated
      }
      await provider.reset()
      if (scenario.steps) await provider.script(scenario.steps)
      await scenario.run({ page, stack, provider, issues })
      // 浏览器会把任意非 2xx 的 fetch 记成 console error（应用已把该错误呈现给用户），
      // 这类“Failed to load resource: ... status of N”不算未预期；静态资源失败会单独断言。
      const allowedConsole = [
        /^Failed to load resource: the server responded with a status of (400|401|403|404|409|413|429|500|502|503|504)\b/,
        ...(scenario.allowConsole || []),
      ]
      const unexpected = issues.console.filter(text => !allowedConsole.some(pattern => pattern.test(text)))
      assert.deepEqual(unexpected, [], `console 出现未预期错误：${unexpected.join(' | ')}`)
      assert.deepEqual(issues.pageerrors, [], `出现未捕获异常：${issues.pageerrors.join(' | ')}`)
      assert.deepEqual(issues.assets, [], `静态资源加载失败：${issues.assets.join(' | ')}`)
      passed += 1
      console.log(`✓ ${scenario.name} (${Date.now() - started}ms)`)
    } catch (error) {
      failures.push({ name: scenario.name, error })
      console.log(`✗ ${scenario.name}`)
      console.log(`  ${String(error.message).split('\n').join('\n  ')}`)
      const slug = scenario.name.replace(/[^\w\u4e00-\u9fa5]+/g, '-').slice(0, 40)
      try {
        await page.screenshot({ path: join(artifacts, `${slug}.png`), fullPage: true })
        const dump = {
          url: page.url(),
          storage: await page.evaluate(() => localStorage.getItem('mini-agent.chats.v1')),
          console: issues.console,
          pageerrors: issues.pageerrors,
          failed: issues.failed,
        }
        await writeFile(join(artifacts, `${slug}.json`), JSON.stringify(dump, null, 2))
      } catch { /* 页面可能已崩溃，忽略快照错误 */ }
    } finally {
      if (dedicated) await dedicated.close()
      await context.close()
    }
  }
} finally {
  if (sharedStack) await sharedStack.close()
  await provider.close()
  await browser.close()
}

console.log(`\n通过 ${passed}/${selected.length}，失败 ${failures.length}`)
if (failures.length > 0) {
  console.log(`失败明细已写入 ${artifacts}`)
  process.exit(1)
}
