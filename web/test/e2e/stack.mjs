// 真实链路装配：可脚本化 Provider → 真实 Go 进程（cmd/mini-agent）。
//
// 正式架构是单镜像单进程：页面、同源 /api/*、/v1/* 与 /healthz 都由同一个 Go 进程在
// 同一个端口提供，不存在 Node BFF 或反向代理。因此这里启动的栈只有「一个进程、一个地址」。
// 只使用回环地址与随机端口，不调用任何真实模型服务。
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { access, readFile } from 'node:fs/promises'
import { createServer as createHttpServer } from 'node:http'
import { createServer } from 'node:net'
import { networkInterfaces } from 'node:os'
import { extname, join, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = resolve(fileURLToPath(new URL('.', import.meta.url)), '../..')
const repoRoot = resolve(webRoot, '..')
export const distDir = resolve(webRoot, 'dist')
export const binaryPath = process.env.MINI_AGENT_BIN || resolve(repoRoot, 'bin/mini-agent-e2e')

// 服务端 Bearer 凭证：只注入 Go 进程环境，浏览器永远拿不到（断言见「安全」场景）。
const authToken = 'e2e-server-only-token'
export const demoAuthToken = authToken
export const demoProviderKey = 'e2e-provider-key'

async function exists(path) {
  try {
    await access(path)
    return true
  } catch {
    return false
  }
}

// 前端产物是 Go 进程的运行时依赖（MINI_AGENT_WEB_DIR 缺 index.html 时进程会拒绝启动），
// 因此缺产物时先构建，保证 `npm run test:e2e` 单独执行也能跑通。
export async function buildFrontend() {
  if (await exists(join(distDir, 'index.html'))) return
  const build = spawn(process.env.NPM || 'npm', ['run', 'build'], { cwd: webRoot, stdio: 'inherit' })
  const code = await new Promise(done => build.once('exit', done))
  if (code !== 0) throw new Error(`前端构建失败（exit ${code}），请先在 web 目录执行 npm ci`)
}

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

// pickContainerPort 为容器模式挑选宿主机端口。
//
// 不能直接用内核临时端口：本仓库的验证环境（rootless Docker + 端口转发白名单）只转发
// 低位端口，临时端口段发布后不可达。这里从固定的 18000 段顺序探测，保证可复现。
async function pickContainerPort() {
  const base = Number(process.env.MINI_AGENT_E2E_PORT || 18000)
  for (let offset = 0; offset < 200; offset += 1) {
    const port = base + offset
    const server = createServer()
    try {
      server.listen(port, '127.0.0.1')
      await once(server, 'listening')
      await new Promise((done, fail) => server.close(error => (error ? fail(error) : done())))
      return port
    } catch { /* 端口被占用，试下一个 */ }
  }
  throw new Error(`找不到可用的容器发布端口（起点 ${base}）`)
}

async function listen(server, host = '127.0.0.1') {
  server.listen(0, host)
  await once(server, 'listening')
  return `http://${host}:${server.address().port}`
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

function backendEnv(host, port, providerBaseURL, options) {
  return {
    ...process.env,
    MINI_AGENT_LISTEN_ADDR: `${host}:${port}`,
    // 前端产物目录：同一个进程因此同时提供页面与 /api、/v1（单镜像的正式装配方式）。
    MINI_AGENT_WEB_DIR: distDir,
    MINI_AGENT_AUTH_TOKEN: authToken,
    MINI_AGENT_LLM_BASE_URL: `${providerBaseURL}/v1`,
    MINI_AGENT_MODEL: 'e2e-scripted-model',
    MINI_AGENT_LLM_API_KEY: demoProviderKey,
    MINI_AGENT_SYSTEM_PROMPT: '你是 E2E 测试助手。',
    MINI_AGENT_REQUEST_TIMEOUT: options.requestTimeout || '15s',
    MINI_AGENT_SHUTDOWN_GRACE: '2s',
    MINI_AGENT_MAX_LLM_CALLS: String(options.maxLLMCalls || 8),
    MINI_AGENT_MAX_TOOL_CALLS: String(options.maxToolCalls || 8),
    MINI_AGENT_TOOL_TIMEOUT: '5s',
    // 上下文压缩：0 表示禁用（默认）。显式配置预算后才会真正触发压缩。
    MINI_AGENT_CONTEXT_TOKEN_BUDGET: String(options.tokenBudget ?? 0),
    MINI_AGENT_CONTEXT_KEEP_RECENT: String(options.keepRecent ?? 0),
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

// 容器模式：MINI_AGENT_E2E_IMAGE 指向最终应用镜像时，被测对象改为真正跑在容器里的同一个镜像。
// 容器内固定监听 0.0.0.0:8080（镜像里页面目录 /app/web 已内置），宿主机端口按需发布；
// 可脚本化 Provider 在宿主机上，容器通过 host.docker.internal 访问它。
const e2eImage = process.env.MINI_AGENT_E2E_IMAGE || ''
const containerPort = 8080

// containerEnvKeys 是必须显式传进容器的配置：容器模式不继承宿主机环境。
const containerEnvKeys = [
  'MINI_AGENT_LISTEN_ADDR', 'MINI_AGENT_AUTH_TOKEN', 'MINI_AGENT_LLM_BASE_URL', 'MINI_AGENT_MODEL',
  'MINI_AGENT_LLM_API_KEY', 'MINI_AGENT_SYSTEM_PROMPT', 'MINI_AGENT_REQUEST_TIMEOUT',
  'MINI_AGENT_SHUTDOWN_GRACE', 'MINI_AGENT_MAX_LLM_CALLS', 'MINI_AGENT_MAX_TOOL_CALLS', 'MINI_AGENT_TOOL_TIMEOUT',
  'MINI_AGENT_CONTEXT_TOKEN_BUDGET', 'MINI_AGENT_CONTEXT_KEEP_RECENT',
]

// containerProviderOrigin 把宿主机上的 Provider 地址改写为容器内可达的地址。
export function containerProviderOrigin(providerOrigin) {
  return `http://host.docker.internal:${new URL(providerOrigin).port}`
}

async function runCommand(command, args) {
  const child = spawn(command, args, { stdio: ['ignore', 'pipe', 'pipe'] })
  let stdout = ''
  let stderr = ''
  child.stdout.on('data', chunk => { stdout += chunk })
  child.stderr.on('data', chunk => { stderr += chunk })
  const code = await new Promise(done => child.once('exit', done))
  if (code !== 0) throw new Error(`${command} ${args.join(' ')} 失败（exit ${code}）：\n${stderr}`)
  return stdout
}

function createRunner(host, port, provider, options) {
  const log = { text: '' }
  if (!e2eImage) {
    const child = spawn(binaryPath, [], { env: backendEnv(host, port, provider.origin, options), stdio: ['ignore', 'pipe', 'pipe'] })
    child.stdout.on('data', chunk => { log.text += chunk })
    child.stderr.on('data', chunk => { log.text += chunk })
    const exited = new Promise(done => child.once('exit', done))
    return {
      name: `进程 ${binaryPath}`,
      log,
      stop: async () => {
        if (child.exitCode !== null) return
        child.kill('SIGKILL')
        await exited
      },
    }
  }

  const name = `mini-agent-e2e-${port}`
  const env = backendEnv('0.0.0.0', containerPort, containerProviderOrigin(provider.origin), options)
  const publish = host === '0.0.0.0' ? `${port}:${containerPort}` : `127.0.0.1:${port}:${containerPort}`
  return {
    name: `容器 ${e2eImage}`,
    log,
    start: async () => {
      const args = [
        // 不加 --rm：退出原因由 stop() 里的 docker logs 保留，便于失败诊断。
        'run', '-d', '--name', name,
        '-p', publish,
        // 宿主机上的可脚本化 Provider：host-gateway 在 rootless / 普通 Docker 下都指向宿主机。
        '--add-host', 'host.docker.internal:host-gateway',
      ]
      for (const key of containerEnvKeys) args.push('-e', `${key}=${env[key]}`)
      args.push(e2eImage)
      if (process.env.MINI_AGENT_E2E_DEBUG) console.error('$ docker', args.join(' ').replace(/MINI_AGENT_AUTH_TOKEN=\S+/, 'MINI_AGENT_AUTH_TOKEN=<redacted>'))
      const id = (await runCommand('docker', args)).trim()
      log.text += `container ${id.slice(0, 12)} (${name}) 已启动，发布 ${publish}\n`
    },
    stop: async () => {
      log.text += (await runCommand('docker', ['logs', name]).catch(() => ''))
      await runCommand('docker', ['rm', '-f', name]).catch(() => {})
    },
  }
}

async function startBackend(host, port, provider, options) {
  const runner = createRunner(host, port, provider, options)
  if (runner.start) await runner.start()
  try {
    await waitHealthy(`http://127.0.0.1:${port}`)
  } catch (error) {
    // 启动失败时保留运行时日志与退出原因，避免只剩一句「未就绪」。
    const diagnostic = await runCommand('docker', ['logs', runner.name]).catch(() => '')
    await runner.stop()
    throw new Error(`${error.message}\n${diagnostic}${runner.log.text}`)
  }
  return {
    origin: `http://127.0.0.1:${port}`,
    log: () => runner.log.text,
    stop: runner.stop,
  }
}

// 启动一个单镜像应用进程（页面 + /api + /v1 同端口）。
// 端口固定，因此 restartBackend / restartServer 会保留浏览器 localStorage，只重置服务端会话存储。
export async function startStack(provider, options = {}) {
  const port = e2eImage ? await pickContainerPort() : await freePort()
  const listenHost = options.listenHost || '127.0.0.1'
  let backend = await startBackend(listenHost, port, provider, options)
  const lan = listenHost === '0.0.0.0' ? lanAddress() : ''

  const restart = async extra => {
    await backend.stop()
    backend = await startBackend(listenHost, port, provider, { ...options, ...extra })
    // 等内核释放端口与旧连接，避免把重启窗口当成传输失败。
    await new Promise(done => setTimeout(done, 250))
  }

  return {
    url: backend.origin,
    // 通过局域网 IP 以 http 访问时，浏览器不把它当成安全上下文（crypto.randomUUID、clipboard 都不可用）。
    lanURL: lan ? `http://${lan}:${port}` : '',
    // 页面与 API 同源：backendURL 与 url 是同一个地址。
    backendURL: backend.origin,
    backendLog: () => backend.log(),
    // 重启后端：服务端内存会话全部丢失，浏览器侧 localStorage 保留。
    restartBackend: restart,
    // 模拟整机不可达（没有独立代理进程可以单独停掉）：前端应当看到传输失败而不是服务端错误。
    stopServer: async () => { await backend.stop() },
    restartServer: restart,
    async close() { await backend.stop() },
  }
}

// 静态产物桩：只用于「后端返回异常数据 / 不响应」这类场景。
//
// 正式服务不会产生 401、坏 JSON、502 或健康检查挂起，这些场景验证的是前端自身的
// 错误呈现与恢复能力，因此页面静态资源仍按真实产物提供，只有 /api/* 交给固定响应桩。
const staticTypes = new Map(Object.entries({
  '.css': 'text/css; charset=utf-8',
  '.html': 'text/html; charset=utf-8',
  '.ico': 'image/x-icon',
  '.js': 'text/javascript; charset=utf-8',
  '.json': 'application/json',
  '.map': 'application/json',
  '.mjs': 'text/javascript; charset=utf-8',
  '.png': 'image/png',
  '.svg': 'image/svg+xml',
  '.txt': 'text/plain; charset=utf-8',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
}))

function isAPIPath(pathname) {
  return pathname === '/api' || pathname.startsWith('/api/')
}

async function serveStatic(res, pathname) {
  const rel = pathname === '/' ? 'index.html' : decodeURIComponent(pathname).replace(/^\/+/, '')
  const target = resolve(distDir, rel)
  if (!target.startsWith(distDir + sep) && target !== distDir) {
    res.writeHead(404).end('not found')
    return
  }
  let body
  try {
    body = await readFile(target)
  } catch {
    if (extname(rel) !== '') {
      res.writeHead(404).end('not found')
      return
    }
    body = await readFile(join(distDir, 'index.html'))
  }
  res.writeHead(200, { 'Content-Type': staticTypes.get(extname(rel)) || 'application/octet-stream' }).end(body)
}

// 只用于构造「后端返回异常数据」的固定响应桩：静态资源来自真实产物，/api/* 由 handler 决定。
export async function startStubStack(handler) {
  const stub = createHttpServer((req, res) => {
    const { pathname } = new URL(req.url, 'http://127.0.0.1')
    if (!isAPIPath(pathname)) {
      serveStatic(res, pathname).catch(() => { if (!res.headersSent) res.writeHead(500).end('stub failure') })
      return
    }
    handler(req, res)
  })
  const stubURL = await listen(stub)
  return {
    url: stubURL,
    async close() {
      stub.closeAllConnections()
      await new Promise(done => stub.close(done))
    },
  }
}
