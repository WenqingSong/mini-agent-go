export class APIError extends Error {
  constructor(public code: string, message: string) {
    super(message)
  }
}

const explanations: Record<string, string> = {
  unauthorized: '服务连接配置有误，请检查 HTTP 访问凭证。',
  provider_failure: '模型服务暂时无法回答，请稍后再试。',
  deadline_exceeded: '这次回答超时了，请稍后再试或简化问题。',
  llm_call_limit: '这次任务达到了模型调用次数上限，请缩小问题范围。',
  tool_call_limit: '这次任务达到了工具调用次数上限，请缩小问题范围。',
  tool_failure: '工具执行失败，请稍后再试。',
  session_not_found: '服务中的会话已不存在，本地记录仍然保留。',
  session_expired: '服务中的会话已过期，本地记录仍然保留。',
  payload_too_large: '消息太长了，请缩短后发送。',
  invalid_field: '消息格式不符合要求，请检查输入。',
  backend_unavailable: '暂时无法连接对话服务，请确认服务已启动。',
}

async function post(path: string, body: unknown, signal: AbortSignal) {
  let response: Response
  try {
    response = await fetch(`/api/v1/${path}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body), signal,
    })
  } catch (error) {
    if (signal.aborted) throw error
    throw new APIError('network_error', '连接中断，未能确认本次请求的结果。')
  }
  let data
  try { data = await response.json() } catch {
    throw new APIError('invalid_response', '服务返回了无法识别的响应。')
  }
  if (!response.ok) {
    const code = typeof data?.error?.code === 'string' ? data.error.code : 'unknown'
    throw new APIError(code, explanations[code] || '这次请求未能完成，请稍后再试。')
  }
  return data
}

export async function createSession(signal: AbortSignal): Promise<string> {
  const data = await post('sessions', {}, signal)
  if (typeof data?.session_id !== 'string' || !data.session_id) {
    throw new APIError('invalid_response', '服务未返回有效的会话。')
  }
  return data.session_id
}

export type Reply = { reply: string; llm_calls: number; tool_calls: number }
export async function sendMessage(sessionId: string, message: string, signal: AbortSignal): Promise<Reply> {
  const data = await post('chat', { session_id: sessionId, message }, signal)
  if (data?.status !== 'COMPLETED' || data.session_id !== sessionId || typeof data.reply !== 'string'
    || !Number.isSafeInteger(data.llm_calls) || !Number.isSafeInteger(data.tool_calls)) {
    throw new APIError('invalid_response', '服务未返回有效的回答。')
  }
  return data
}
