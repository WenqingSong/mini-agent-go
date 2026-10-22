export type Theme = 'system' | 'light' | 'dark'
export type Message = {
  id: string
  role: 'user' | 'assistant'
  content: string
  state: 'done' | 'pending' | 'error'
  llmCalls?: number
  toolCalls?: number
}
export type Chat = {
  id: string
  title: string
  sessionId?: string
  updatedAt: number
  unavailable?: 'expired' | 'interrupted'
  messages: Message[]
}
export type Archive = { version: 1; activeId: string | null; chats: Chat[] }
export const STORAGE_KEY = 'mini-agent.chats.v1'
export const THEME_KEY = 'mini-agent.theme'

export function emptyArchive(): Archive {
  return { version: 1, activeId: null, chats: [] }
}

export function newChat(): Chat {
  return { id: crypto.randomUUID(), title: '新对话', updatedAt: Date.now(), messages: [] }
}

export function titleFrom(text: string): string {
  const title = text.trim().replace(/\s+/g, ' ')
  return Array.from(title).slice(0, 28).join('')
}

function isMessage(value: unknown): value is Message {
  if (!value || typeof value !== 'object') return false
  const m = value as Message
  return typeof m.id === 'string' && typeof m.content === 'string'
    && ['user', 'assistant'].includes(m.role) && ['done', 'pending', 'error'].includes(m.state)
    && (m.llmCalls === undefined || Number.isSafeInteger(m.llmCalls))
    && (m.toolCalls === undefined || Number.isSafeInteger(m.toolCalls))
}

function isChat(value: unknown): value is Chat {
  if (!value || typeof value !== 'object') return false
  const chat = value as Chat
  return typeof chat.id === 'string' && typeof chat.title === 'string'
    && typeof chat.updatedAt === 'number' && Number.isFinite(chat.updatedAt)
    && (chat.sessionId === undefined || typeof chat.sessionId === 'string')
    && (chat.unavailable === undefined || ['expired', 'interrupted'].includes(chat.unavailable))
    && Array.isArray(chat.messages) && chat.messages.every(isMessage)
}

// 刷新发生在请求途中时，服务端可能已经提交；保留记录并停止续聊，避免猜测提交结果。
export function parseArchive(raw: string | null): Archive {
  if (!raw) return emptyArchive()
  const data: unknown = JSON.parse(raw)
  if (!data || typeof data !== 'object') throw new Error('invalid archive')
  const archive = data as Archive
  if (archive.version !== 1 || !Array.isArray(archive.chats) || !archive.chats.every(isChat)) {
    throw new Error('invalid archive')
  }
  if (new Set(archive.chats.map(c => c.id)).size !== archive.chats.length) throw new Error('duplicate chat')
  return {
    version: 1,
    activeId: archive.chats.some(c => c.id === archive.activeId) ? archive.activeId : null,
    chats: archive.chats.map(chat => chat.messages.some(m => m.state === 'pending') ? {
      ...chat,
      unavailable: 'interrupted',
      messages: chat.messages.map(m => m.state === 'pending'
        ? { ...m, state: 'error', content: '上次请求中断，未能确认回复。' } : m),
    } : chat),
  }
}

export function loadArchive(): { archive: Archive; warning: string } {
  try {
    return { archive: parseArchive(localStorage.getItem(STORAGE_KEY)), warning: '' }
  } catch {
    return { archive: emptyArchive(), warning: '无法读取本地记录。请确认浏览器允许本地存储。' }
  }
}

export function saveArchive(archive: Archive): string {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(archive))
    return ''
  } catch {
    return '本地存储不可用或已满，本次修改暂未保存。关闭页面前请复制需要保留的内容。'
  }
}

export function loadTheme(): Theme {
  try {
    const theme = localStorage.getItem(THEME_KEY)
    return theme === 'dark' || theme === 'light' ? theme : 'system'
  } catch {
    return 'system'
  }
}
