import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createServer, request } from 'node:http'
import { once } from 'node:events'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createFrontendServer } from '../server.mjs'

async function fixture(t, handler) {
  const directory = await mkdtemp(join(tmpdir(), 'mini-web-test-'))
  await writeFile(join(directory, 'index.html'), '<h1>Mini Agent</h1>')
  const backend = createServer(handler)
  backend.listen(0, '127.0.0.1')
  await once(backend, 'listening')
  const frontend = createFrontendServer({ backendURL: `http://127.0.0.1:${backend.address().port}`, authToken: 'test-only-server-secret', distDir: directory })
  frontend.listen(0, '127.0.0.1')
  await once(frontend, 'listening')
  t.after(async () => {
    frontend.closeAllConnections(); backend.closeAllConnections()
    await Promise.all([new Promise(done => frontend.close(done)), new Promise(done => backend.close(done))])
    await rm(directory, { recursive: true, force: true })
  })
  return `http://127.0.0.1:${frontend.address().port}`
}

test('仅转发固定 API，注入服务端凭证，保留上游错误且静态页不含凭证', async t => {
  const seen = []
  const url = await fixture(t, async (req, res) => {
    let body = ''
    for await (const chunk of req) body += chunk
    seen.push({ url: req.url, token: req.headers.authorization, body })
    res.writeHead(404, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ error: { code: 'session_not_found' } }))
  })
  const response = await fetch(url + '/api/v1/chat', { method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: 'Bearer from-browser' }, body: '{"session_id":"old","message":"hello"}' })
  assert.equal(response.status, 404)
  assert.equal((await response.json()).error.code, 'session_not_found')
  assert.deepEqual(seen, [{ url: '/v1/chat', token: 'Bearer test-only-server-secret', body: '{"session_id":"old","message":"hello"}' }])
  for (const path of ['/api/secret', '/.env', '/%2e%2e%2f.env']) assert.equal((await fetch(url + path)).status, 404)
  const page = await fetch(url)
  assert.equal(page.status, 200)
  assert.equal(await page.text(), '<h1>Mini Agent</h1>')
  assert.match(page.headers.get('content-security-policy'), /frame-ancestors 'none'/)
})

test('跨站写入和错误方法在代理层被拒绝，不访问后端', async t => {
  let calls = 0
  const url = await fixture(t, (_req, res) => { calls++; res.end('{}') })
  assert.equal((await fetch(url + '/api/v1/chat', { method: 'POST', headers: { Origin: 'https://unrelated.example' }, body: '{}' })).status, 403)
  assert.equal((await fetch(url + '/api/v1/chat')).status, 405)
  assert.equal(calls, 0)
  assert.equal((await fetch(url + '/api/v1/sessions', { method: 'POST', headers: { Origin: url }, body: '{}' })).status, 200)
  assert.equal((await fetch(url + '/api/v1/sessions', { method: 'POST', headers: { Origin: 'https://workspace-preview.example', 'Sec-Fetch-Site': 'same-origin' }, body: '{}' })).status, 200)
  assert.equal((await fetch(url + '/api/v1/sessions', { method: 'POST', headers: { Origin: url, 'Sec-Fetch-Site': 'cross-site' }, body: '{}' })).status, 403)
})

test('无凭证或无效 Header 凭证在启动时被拒绝', () => {
  for (const authToken of ['', 'a\nb', '不能作为HTTP头']) {
    assert.throws(() => createFrontendServer({ backendURL: 'http://127.0.0.1:8080', authToken }))
  }
})

test('浏览器断开后，代理关闭对应上游请求', { timeout: 5000 }, async t => {
  let began, ended
  const started = new Promise(resolve => { began = resolve })
  const closed = new Promise(resolve => { ended = resolve })
  const url = await fixture(t, (req, res) => {
    req.resume()
    res.on('close', ended)
    began()
  })
  const client = request(url + '/api/v1/chat', { method: 'POST' })
  client.on('error', () => {})
  client.end('{}')
  await started
  client.destroy()
  await closed
})
