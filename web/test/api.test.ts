// 对照 Go 侧真实契约（httpapi/handler.go、httpapi/errors.go）验证请求形状与错误映射。
// 用注入的 fetch 覆盖网络层：断言前端发出的 URL / 方法 / Header / Body，以及每种后端响应
// （成功、错误封套、非 JSON、字段缺失、类型不符、连接中断、主动取消）的处理结果。
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { APIError, createSession, sendMessage } from '../src/api.ts'

type Call = { url: string; init: RequestInit }

function stubFetch(handler: (url: string) => Response | Promise<Response>) {
  const original = globalThis.fetch
  const calls: Call[] = []
  globalThis.fetch = (async (input: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: String(input), init: init ?? {} })
    return handler(String(input))
  }) as typeof fetch
  return { calls, restore: () => { globalThis.fetch = original } }
}

const json = (status: number, payload: unknown) => new Response(JSON.stringify(payload), {
  status, headers: { 'Content-Type': 'application/json; charset=utf-8' },
})
const errorBody = (code: string) => ({ error: { code, message: '固定文案' } })
const signal = () => new AbortController().signal

test('创建会话：POST /api/v1/sessions，JSON 空对象，取 session_id', async () => {
  const stub = stubFetch(() => json(201, { session_id: 'a'.repeat(32) }))
  try {
    assert.equal(await createSession(signal()), 'a'.repeat(32))
    assert.equal(stub.calls.length, 1)
    assert.equal(stub.calls[0].url, '/api/v1/sessions')
    assert.equal(stub.calls[0].init.method, 'POST')
    assert.equal(stub.calls[0].init.body, '{}')
    const headers = stub.calls[0].init.headers as Record<string, string>
    assert.equal(headers['Content-Type'], 'application/json')
    assert.equal('Authorization' in headers, false, '浏览器侧不得自行携带凭证')
  } finally { stub.restore() }
})

test('创建会话：成功响应缺少 session_id 时按无效响应处理', async () => {
  const stub = stubFetch(() => json(201, {}))
  try {
    await assert.rejects(createSession(signal()), (error: unknown) => {
      assert.ok(error instanceof APIError)
      assert.equal(error.code, 'invalid_response')
      assert.match(error.message, /未返回有效的会话/)
      return true
    })
  } finally { stub.restore() }
})

test('错误封套：code 映射为固定中文提示，未知 code 走兜底文案', async () => {
  const cases: Array<[string, number, RegExp]> = [
    ['unauthorized', 401, /访问凭证/],
    ['session_not_found', 404, /会话已不存在/],
    ['session_expired', 404, /会话已过期/],
    ['llm_call_limit', 429, /调用次数上限/],
    ['tool_call_limit', 429, /工具调用次数上限/],
    ['provider_failure', 502, /模型服务暂时无法回答/],
    ['deadline_exceeded', 504, /超时/],
    ['backend_unavailable', 502, /无法连接对话服务/],
    ['payload_too_large', 413, /消息太长/],
    ['invalid_field', 400, /格式不符合要求/],
    ['internal', 500, /请求未能完成/],
  ]
  for (const [code, status, expected] of cases) {
    const stub = stubFetch(() => json(status, errorBody(code)))
    try {
      await assert.rejects(createSession(signal()), (error: unknown) => {
        assert.ok(error instanceof APIError)
        assert.equal(error.code, code)
        assert.match(error.message, expected)
        return true
      })
    } finally { stub.restore() }
  }
})

test('响应体不是 JSON 时给出可读提示，而不是抛解析异常', async () => {
  const stub = stubFetch(() => new Response('<html>502</html>', { status: 502 }))
  try {
    await assert.rejects(createSession(signal()), (error: unknown) => {
      assert.ok(error instanceof APIError)
      assert.equal(error.code, 'invalid_response')
      assert.match(error.message, /无法识别的响应/)
      return true
    })
  } finally { stub.restore() }
})

test('聊天：请求体为 {session_id,message}，成功返回回复与调用计数', async () => {
  const stub = stubFetch(() => json(200, {
    session_id: 'session-1', run_id: 'run-1', status: 'COMPLETED', reply: '1776', llm_calls: 2, tool_calls: 1,
  }))
  try {
    const reply = await sendMessage('session-1', '37 × 48', signal())
    assert.equal(reply.reply, '1776')
    assert.equal(reply.llm_calls, 2)
    assert.equal(reply.tool_calls, 1)
    assert.equal(stub.calls[0].url, '/api/v1/chat')
    assert.deepEqual(JSON.parse(String(stub.calls[0].init.body)), { session_id: 'session-1', message: '37 × 48' })
  } finally { stub.restore() }
})

test('聊天：非 COMPLETED、会话不一致、计数非整数、回复非字符串都算无效响应', async () => {
  const payloads: unknown[] = [
    { session_id: 'session-1', status: 'FAILED', reply: '', llm_calls: 1, tool_calls: 0 },
    { session_id: 'other', status: 'COMPLETED', reply: 'ok', llm_calls: 1, tool_calls: 0 },
    { session_id: 'session-1', status: 'COMPLETED', reply: 'ok', llm_calls: 1.5, tool_calls: 0 },
    { session_id: 'session-1', status: 'COMPLETED', reply: 'ok', tool_calls: 0 },
    { session_id: 'session-1', status: 'COMPLETED', reply: null, llm_calls: 1, tool_calls: 0 },
  ]
  for (const payload of payloads) {
    const stub = stubFetch(() => json(200, payload))
    try {
      await assert.rejects(sendMessage('session-1', 'hi', signal()), (error: unknown) => {
        assert.ok(error instanceof APIError)
        assert.equal(error.code, 'invalid_response')
        return true
      })
    } finally { stub.restore() }
  }
})

test('聊天：会话不存在时保留后端错误码，便于页面区分“需要新会话”', async () => {
  const stub = stubFetch(() => json(404, errorBody('session_not_found')))
  try {
    await assert.rejects(sendMessage('gone', 'hi', signal()), (error: unknown) => {
      assert.ok(error instanceof APIError)
      assert.equal(error.code, 'session_not_found')
      return true
    })
  } finally { stub.restore() }
})

test('连接失败：归为 network_error，提示结果未知', async () => {
  const stub = stubFetch(() => { throw new TypeError('Failed to fetch') })
  try {
    await assert.rejects(createSession(signal()), (error: unknown) => {
      assert.ok(error instanceof APIError)
      assert.equal(error.code, 'network_error')
      assert.match(error.message, /连接中断/)
      return true
    })
  } finally { stub.restore() }
})

test('主动取消：原样抛出 AbortError，页面据此展示“已停止”而不是网络错误', async () => {
  const controller = new AbortController()
  controller.abort()
  const stub = stubFetch(() => { throw new DOMException('The operation was aborted.', 'AbortError') })
  try {
    await assert.rejects(createSession(controller.signal), (error: unknown) => {
      assert.equal(error instanceof APIError, false)
      assert.equal((error as Error).name, 'AbortError')
      return true
    })
  } finally { stub.restore() }
})

test('取消信号被透传给 fetch，便于浏览器中断在途请求', async () => {
  const controller = new AbortController()
  const stub = stubFetch(() => json(200, { session_id: 'session-1', status: 'COMPLETED', reply: 'ok', llm_calls: 1, tool_calls: 0 }))
  try {
    await sendMessage('session-1', 'hi', controller.signal)
    assert.equal(stub.calls[0].init.signal, controller.signal)
  } finally { stub.restore() }
})
