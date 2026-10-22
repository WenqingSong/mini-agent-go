import { test } from 'node:test'
import assert from 'node:assert/strict'
import { emptyArchive, parseArchive, titleFrom } from '../src/model.ts'
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
