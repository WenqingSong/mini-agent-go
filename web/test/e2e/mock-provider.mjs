// 可脚本化的 OpenAI 兼容服务，仅供离线 E2E 使用：只监听回环地址，不访问任何真实 Provider。
//
// 控制面（仅测试进程使用）：
//   POST /__script  {"steps": [...]}  覆盖脚本（每次请求按顺序消费一步；用尽后返回默认文本）
//   GET  /__requests                  返回已收到的请求（messages / tools）
//   POST /__reset                     清空脚本与记录
//
// 脚本步：
//   {kind:'text', text}                    正常 stop 终稿
//   {kind:'empty'}                         内容为空的 stop 终稿
//   {kind:'tool', calls:[{id?,name,arguments}]}  finish_reason=tool_calls
//   {kind:'status', status, body?}          非 2xx
//   {kind:'invalid_json'}                   2xx 但响应体不是 JSON
//   {kind:'block'}                          不响应，直到上游取消连接
//   {kind:'slow', text, delayMs}            延迟后返回文本
import { createServer } from 'node:http'

const defaultText = '这是脚本用尽后的默认回复。'

function replyBody(id, text) {
  return {
    id,
    choices: [{ message: { role: 'assistant', content: text }, finish_reason: 'stop' }],
    usage: { prompt_tokens: 11, completion_tokens: 7 },
  }
}

function toolCallsBody(id, calls, step) {
  return {
    id,
    choices: [{
      message: {
        role: 'assistant',
        content: '',
        // 工具调用 ID 在整段会话历史内必须唯一，因此带上脚本步序号。
        tool_calls: calls.map((call, index) => ({
          id: call.id || `call-${step}-${index}`,
          type: 'function',
          function: { name: call.name, arguments: typeof call.arguments === 'string' ? call.arguments : JSON.stringify(call.arguments ?? {}) },
        })),
      },
      finish_reason: 'tool_calls',
    }],
    usage: { prompt_tokens: 11, completion_tokens: 7 },
  }
}

async function readBody(req) {
  const chunks = []
  for await (const chunk of req) chunks.push(chunk)
  return Buffer.concat(chunks).toString('utf8')
}

export async function startMockProvider() {
  const state = { steps: [], cursor: 0, requests: [], blocked: new Set() }

  const server = createServer(async (req, res) => {
    const url = new URL(req.url, 'http://127.0.0.1')
    if (url.pathname === '/__script') {
      const payload = JSON.parse((await readBody(req)) || '{}')
      state.steps = payload.steps || []
      state.cursor = 0
      res.writeHead(200, { 'Content-Type': 'application/json' }).end('{"ok":true}')
      return
    }
    if (url.pathname === '/__requests') {
      res.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify(state.requests))
      return
    }
    if (url.pathname === '/__reset') {
      state.steps = []
      state.cursor = 0
      state.requests = []
      state.blocked.clear()
      res.writeHead(200, { 'Content-Type': 'application/json' }).end('{"ok":true}')
      return
    }
    if (url.pathname !== '/v1/chat/completions' || req.method !== 'POST') {
      res.writeHead(404, { 'Content-Type': 'application/json' }).end('{"error":"not_found"}')
      return
    }

    const body = await readBody(req)
    let parsed = {}
    try { parsed = JSON.parse(body) } catch { parsed = { parse_error: true } }
    state.requests.push({ messages: parsed.messages || [], tools: (parsed.tools || []).map(tool => tool.function?.name), model: parsed.model })

    const step = state.steps[state.cursor]
    state.cursor += 1
    const kind = step?.kind || 'text'

    if (kind === 'block') {
      state.blocked.add(res)
      res.on('close', () => state.blocked.delete(res))
      return
    }
    if (kind === 'status') {
      res.writeHead(step.status, { 'Content-Type': 'application/json' })
        .end(step.body ?? JSON.stringify({ error: { message: 'scripted failure' } }))
      return
    }
    if (kind === 'invalid_json') {
      res.writeHead(200, { 'Content-Type': 'application/json' }).end('not-json-at-all')
      return
    }
    if (kind === 'slow') {
      const timer = setTimeout(() => {
        if (!res.destroyed) {
          res.writeHead(200, { 'Content-Type': 'application/json' })
            .end(JSON.stringify(replyBody(`resp-${state.cursor}`, step.text ?? defaultText)))
        }
      }, step.delayMs ?? 500)
      res.on('close', () => clearTimeout(timer))
      return
    }
    if (kind === 'tool') {
      res.writeHead(200, { 'Content-Type': 'application/json' })
        .end(JSON.stringify(toolCallsBody(`resp-tool-${state.cursor}`, step.calls || [], state.cursor)))
      return
    }

    const text = kind === 'empty' ? '' : (step?.text ?? defaultText)
    res.writeHead(200, { 'Content-Type': 'application/json' })
      .end(JSON.stringify(replyBody(`resp-${state.cursor}`, text)))
  })

  server.listen(0, '127.0.0.1')
  await new Promise(resolve => server.once('listening', resolve))
  const origin = `http://127.0.0.1:${server.address().port}`

  return {
    origin,
    requests: () => state.requests,
    reset: async () => {
      state.steps = []
      state.cursor = 0
      state.requests = []
      state.blocked.clear()
    },
    script: async steps => {
      await fetch(`${origin}/__script`, { method: 'POST', body: JSON.stringify({ steps }) })
      state.requests = []
    },
    close: () => new Promise(resolve => {
      for (const res of state.blocked) res.destroy()
      server.closeAllConnections()
      server.close(() => resolve())
    }),
  }
}
