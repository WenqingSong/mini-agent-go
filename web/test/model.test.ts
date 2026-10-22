import { test } from 'node:test'
import assert from 'node:assert/strict'
import { emptyArchive, loadArchive, loadTheme, newId, parseArchive, saveArchive, STORAGE_KEY, THEME_KEY, titleFrom } from '../src/model.ts'
import type { Chat } from '../src/model.ts'

const first: Chat = { id: 'a', sessionId: 'remote-a', title: '第一次计算', updatedAt: 1, messages: [
  { id: 'u1', role: 'user', content: '37 * 48', state: 'done' },
  { id: 'a1', role: 'assistant', content: '1776', state: 'done', llmCalls: 2, toolCalls: 1 },
] }

test('刷新后保留多个会话、服务端 ID、回答和选择位置', () => {
  const second = { ...first, id: 'b', sessionId: 'remote-b', messages: [] }
  const archive = { version: 1, activeId: 'a', chats: [first, second] }
  assert.deepEqual(parseArchive(JSON.stringify(archive)), archive)
  assert.deepEqual(parseArchive(null), emptyArchive())
})

test('刷新中断请求后保留已完成历史，并防止把未知提交状态当成可继续会话', () => {
  const pending = { ...first, messages: [...first.messages, { id: 'a2', role: 'assistant', content: '', state: 'pending' }] }
  const archive = parseArchive(JSON.stringify({ version: 1, activeId: 'a', chats: [pending] }))
  assert.equal(archive.chats[0].unavailable, 'interrupted')
  assert.deepEqual(archive.chats[0].messages.slice(0, 2), first.messages)
  assert.equal(archive.chats[0].messages[2].state, 'error')
  assert.equal(archive.chats[0].sessionId, 'remote-a')
})

test('损坏或不支持的存档不能变成有效记录', () => {
  for (const value of ['{', 'null', '{"version":2,"chats":[]}', '{"version":1,"chats":[{"messages":null}]}']) {
    assert.throws(() => parseArchive(value))
  }
  assert.throws(() => parseArchive(JSON.stringify({ version: 1, activeId: 'a', chats: [first, first] })))
})

test('标题截取不会切断 Unicode 字符', () => {
  assert.equal(titleFrom('  你好\n 世界  '), '你好 世界')
  assert.equal(Array.from(titleFrom('🙂'.repeat(35))).length, 28)
})

test('存档字段类型异常时整体判为损坏，不把错误数据当有效记录', () => {
  const invalid = [
    { version: 1, activeId: 'a', chats: [{ ...first, updatedAt: 'yesterday' }] },
    { version: 1, activeId: 'a', chats: [{ ...first, sessionId: 42 }] },
    { version: 1, activeId: 'a', chats: [{ ...first, unavailable: 'lost' }] },
    { version: 1, activeId: 'a', chats: [{ ...first, messages: [{ id: 'm', role: 'system', content: '', state: 'done' }] }] },
    { version: 1, activeId: 'a', chats: [{ ...first, messages: [{ id: 'm', role: 'assistant', content: '', state: 'thinking' }] }] },
    { version: 1, activeId: 'a', chats: [{ ...first, messages: [{ id: 'm', role: 'assistant', content: '', state: 'done', toolCalls: '1' }] }] },
    { version: 1, activeId: 'a', chats: [{ ...first, messages: 'nope' }] },
  ]
  for (const value of invalid) assert.throws(() => parseArchive(JSON.stringify(value)))
})

test('activeId 指向不存在的会话时退回空闲态，不渲染空聊天', () => {
  const archive = parseArchive(JSON.stringify({ version: 1, activeId: 'missing', chats: [first] }))
  assert.equal(archive.activeId, null)
  assert.equal(archive.chats.length, 1)
})

test('newId 在缺少 crypto.randomUUID 的非安全上下文仍生成唯一标识', () => {
  const descriptor = Object.getOwnPropertyDescriptor(crypto, 'randomUUID')
  Object.defineProperty(crypto, 'randomUUID', { configurable: true, value: undefined })
  try {
    const firstId = newId()
    const secondId = newId()
    assert.match(firstId, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
    assert.notEqual(firstId, secondId)
  } finally {
    if (descriptor) Object.defineProperty(crypto, 'randomUUID', descriptor)
    else delete (crypto as { randomUUID?: unknown }).randomUUID
  }
})

type StorageLike = Pick<Storage, 'getItem' | 'setItem'>

function withStorage(storage: StorageLike) {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  return () => {
    if (descriptor) Object.defineProperty(globalThis, 'localStorage', descriptor)
    else delete (globalThis as { localStorage?: unknown }).localStorage
  }
}

test('本地存储可用时保存并读回会话，主题偏好缺省跟随系统', () => {
  const store = new Map<string, string>()
  const restore = withStorage({
    getItem: key => store.get(key) ?? null,
    setItem: (key, value) => { store.set(key, value) },
  })
  try {
    const archive = { version: 1 as const, activeId: 'a', chats: [first] }
    assert.equal(saveArchive(archive), '')
    assert.deepEqual(loadArchive(), { archive, warning: '' })
    assert.equal(loadTheme(), 'system')
    store.set(THEME_KEY, 'dark')
    assert.equal(loadTheme(), 'dark')
    store.set(THEME_KEY, 'neon')
    assert.equal(loadTheme(), 'system')
  } finally { restore() }
})

test('存储写入失败或内容损坏时给出提示，而不是抛错或渲染半份记录', () => {
  const restoreBroken = withStorage({
    getItem: () => { throw new Error('blocked') },
    setItem: () => { throw new Error('quota') },
  })
  try {
    const loaded = loadArchive()
    assert.deepEqual(loaded.archive, emptyArchive())
    assert.match(loaded.warning, /无法读取本地记录/)
    assert.match(saveArchive(emptyArchive()), /本地存储不可用/)
    assert.equal(loadTheme(), 'system')
  } finally { restoreBroken() }

  const store = new Map<string, string>([[STORAGE_KEY, '{broken']])
  const restoreCorrupt = withStorage({
    getItem: key => store.get(key) ?? null,
    setItem: () => {},
  })
  try {
    const loaded = loadArchive()
    assert.deepEqual(loaded.archive, emptyArchive())
    assert.match(loaded.warning, /无法读取本地记录/)
  } finally { restoreCorrupt() }
})
