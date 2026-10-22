// 真实链路装配：可脚本化 Provider → 真实 Go binary（cmd/mini-agent）→ 真实前端代理（server.mjs）。
// 只使用回环地址与随机端口，不调用任何真实模型服务。
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { createServer as createHttpServer } from 'node:http'
import { createServer } from 'node:net'
import { networkInterfaces } from 'node:os'
import { fileURLToPath } from 'node:url'
import { resolve } from 'node:path'
import { createFrontendServer } from '../../server.mjs'

const webRoot = resolve(fileURLToPath(new URL('.', import.meta.url)), '../..')
const repoRoot = resolve(webRoot, '..')
export const distDir = resolve(webRoot, 'dist')
export const binaryPath = process.env.MINI_AGENT_BIN || resolve(repoRoot, 'bin/mini-agent-e2e')

const authToken = 'e2e-server-only-token'

// 暴露给测试断言：这个值只应该出现在代理进程里，不能出现在任何前端产物或浏览器存储中。
export const demoAuthToken = authToken
export const demoProviderKey = 'e2e-provider-key'

export async function buildBackend() {
  const build = spawn(process.env.GO || 'go', ['build', '-trimpath', '-o', binaryPath, './cmd/mini-agent'], {
    cwd: repoRoot, stdio: ['ignore', 'pipe', 'pipe'],
  })
  let stderr = ''
  build.stderr.on('data', chunk => { stderr += chunk })
  const code = await new Promise(done => build.once('exit', done))
  if (code !== 0) throw new Error(`go build 失败（exit ${code}）：\n${stderr}`)
}

async function freePort() {
  const server = createServer()
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  const { port } = server.address()
  server.close()
  await once(server, 'close')
  return port
}

async function listen(server, host = '127.0.0.1') {
  server.listen(0, host)
  await once(server, 'listening')
  return `http://127.0.0.1:${server.address().port}`
}

// lanAddress 返回本机第一个非回环 IPv4 地址，用于构造「非安全上下文」的访问方式。
export function lanAddress() {
  for (const entries of Object.values(networkInterfaces())) {
    for (const entry of entries || []) {
      if (entry.family === 'IPv4' && !entry.internal) return entry.address
    }
  }
  return ''
}

function backendEnv(port, providerOrigin, options) {
  return {
    ...process.env,
    MINI_AGENT_LISTEN_ADDR: `127.0.0.1:${port}`,
    MINI_AGENT_AUTH_TOKEN: authToken,
    MINI_AGENT_LLM_BASE_URL: `${providerOrigin}/v1`,
    MINI_AGENT_MODEL: 'e2e-scripted-model',
    MINI_AGENT_LLM_API_KEY: demoProviderKey,
    MINI_AGENT_SYSTEM_PROMPT: '你是 E2E 测试助手。',
    MINI_AGENT_REQUEST_TIMEOUT: options.requestTimeout || '15s',
    MINI_AGENT_SHUTDOWN_GRACE: '2s',
    MINI_AGENT_MAX_LLM_CALLS: String(options.maxLLMCalls || 8),
    MINI_AGENT_MAX_TOOL_CALLS: String(options.maxToolCalls || 8),
    MINI_AGENT_TOOL_TIMEOUT: '5s',
  }
}

async function waitHealthy(origin) {
  const deadline = Date.now() + 10_000
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`${origin}/healthz`)
      if (response.ok) return
    } catch { /* 尚未监听 */ }
    await new Promise(done => setTimeout(done, 50))
  }
  throw new Error(`后端未在预期时间内就绪：${origin}`)
}

async function startBackend(port, provider, options) {
  const child = spawn(binaryPath, [], { env: backendEnv(port, provider.origin, options), stdio: ['ignore', 'pipe', 'pipe'] })
  let log = ''
  child.stdout.on('data', chunk => { log += chunk })
  child.stderr.on('data', chunk => { log += chunk })
  const exited = new Promise(done => child.once('exit', done))
  await waitHealthy(`http://127.0.0.1:${port}`)
  return {
    child,
    origin: `http://127.0.0.1:${port}`,
    log: () => log,
    stop: async () => {
      if (child.exitCode !== null) return
      child.kill('SIGKILL')
      await exited
    },
  }
}

// 启动一个独立的后端 + 前端代理组合；前后端地址与浏览器 origin 都固定，
// 因此 restartBackend 会保留浏览器 localStorage，只重置服务端会话存储。
export async function startStack(provider, options = {}) {
  const backendPort = await freePort()
  let backend = await startBackend(backendPort, provider, options)
  const listenHost = options.listenHost || '127.0.0.1'
  let proxy = createFrontendServer({ backendURL: backend.origin, authToken: options.proxyToken || authToken, distDir })
  const proxyURL = await listen(proxy, listenHost)
  const proxyPort = new URL(proxyURL).port
  const lan = listenHost === '0.0.0.0' ? lanAddress() : ''

  const stopProxy = async () => {
    proxy.closeAllConnections()
    await new Promise(done => proxy.close(done))
  }
  const startProxy = async () => {
    proxy = createFrontendServer({ backendURL: backend.origin, authToken: options.proxyToken || authToken, distDir })
    proxy.listen(Number(proxyPort), listenHost)
    await once(proxy, 'listening')
  }

  return {
    url: proxyURL,
    // 通过局域网 IP 以 http 访问时，浏览器不把它当成安全上下文（crypto.randomUUID、clipboard 都不可用）。
    lanURL: lan ? `http://${lan}:${proxyPort}` : '',
    backendURL: backend.origin,
    backendLog: () => backend.log(),
    async restartBackend(extra = {}) {
      await backend.stop()
      backend = await startBackend(backendPort, provider, { ...options, ...extra })
      // 给代理连接池留出探测旧连接已关闭的时间，避免把重启窗口当成传输失败。
      await new Promise(done => setTimeout(done, 250))
    },
    stopProxy,
    async restartProxy() {
      await stopProxy()
      await startProxy()
    },
    async close() {
      await stopProxy()
      await backend.stop()
    },
  }
}

// 只用于构造「后端返回异常数据」的固定响应桩，用来覆盖前端解析失败路径。
export async function startStubStack(handler) {
  const stub = createHttpServer(handler)
  const stubURL = await listen(stub)
  const proxy = createFrontendServer({ backendURL: stubURL, authToken, distDir })
  const proxyURL = await listen(proxy)
  return {
    url: proxyURL,
    async close() {
      proxy.closeAllConnections()
      await new Promise(done => proxy.close(done))
      stub.closeAllConnections()
      await new Promise(done => stub.close(done))
    },
  }
}
