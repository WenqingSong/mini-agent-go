import { createServer, request as httpRequest, validateHeaderValue } from 'node:http'
import { request as httpsRequest } from 'node:https'
import { createReadStream } from 'node:fs'
import { stat } from 'node:fs/promises'
import { resolve, extname, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const types = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8', '.svg': 'image/svg+xml', '.png': 'image/png', '.ico': 'image/x-icon', '.woff2': 'font/woff2' }
const routes = new Map([['/api/healthz', 'GET'], ['/api/v1/sessions', 'POST'], ['/api/v1/chat', 'POST']])

function error(res, status, code) {
  res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' })
  res.end(JSON.stringify({ error: { code } }))
}

// 静态文件与三个固定 API 共用一个来源。HTTP 凭证只在容器进程中读取和转发。
export function createFrontendServer({ backendURL, authToken, distDir = fileURLToPath(new URL('./dist', import.meta.url)) }) {
  const backend = new URL(backendURL)
  if (!['http:', 'https:'].includes(backend.protocol) || !authToken?.trim() || /[\r\n]/.test(authToken)) {
    throw new Error('前端服务需要有效的后端地址和 HTTP 凭证')
  }
  try { validateHeaderValue('Authorization', `Bearer ${authToken}`) } catch {
    throw new Error('HTTP 凭证包含不允许的 Header 字符')
  }
  const root = resolve(distDir)
  return createServer(async (req, res) => {
    res.setHeader('X-Content-Type-Options', 'nosniff')
    res.setHeader('Referrer-Policy', 'no-referrer')
    res.setHeader('Content-Security-Policy', "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
    let pathname
    try { pathname = decodeURIComponent(new URL(req.url, 'http://localhost').pathname) } catch { return error(res, 400, 'invalid_path') }

    if (pathname.startsWith('/api/')) {
      if (!routes.has(pathname)) return error(res, 404, 'not_found')
      if (req.method !== routes.get(pathname)) return error(res, 405, 'method_not_allowed')
      // Fetch Metadata 保留浏览器的同源判定，兼容云开发端口代理改写 Host。
      // 旧浏览器回退到 Origin/Host 比较；无 Origin 的本地命令行请求仍可使用。
      if (req.method === 'POST' && req.headers['sec-fetch-site'] === 'cross-site') return error(res, 403, 'forbidden_origin')
      if (req.method === 'POST' && req.headers.origin && req.headers['sec-fetch-site'] !== 'same-origin') {
        try {
          if (new URL(req.headers.origin).host !== req.headers.host) return error(res, 403, 'forbidden_origin')
        } catch { return error(res, 403, 'forbidden_origin') }
      }
      const target = new URL(pathname.slice(4), backend)
      const request = target.protocol === 'https:' ? httpsRequest : httpRequest
      const upstream = request(target, { method: req.method, headers: {
        'Content-Type': req.headers['content-type'] || 'application/json',
        Authorization: `Bearer ${authToken}`,
      } }, response => {
        res.writeHead(response.statusCode || 502, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store' })
        response.pipe(res)
        response.on('error', () => res.destroy())
      })
      // 取消向上传递：浏览器停止等待时，后端能取消仍在执行的 Run。
      res.on('close', () => { if (!res.writableFinished) upstream.destroy() })
      req.on('aborted', () => upstream.destroy())
      req.on('error', () => upstream.destroy())
      upstream.on('error', () => { if (!res.destroyed && !res.headersSent) error(res, 502, 'backend_unavailable') })
      req.pipe(upstream)
      return
    }
    if (req.method !== 'GET' && req.method !== 'HEAD') return error(res, 405, 'method_not_allowed')
    const file = resolve(root, `.${pathname === '/' ? '/index.html' : pathname}`)
    if (!file.startsWith(root + sep)) return error(res, 404, 'not_found')
    try {
      const info = await stat(file)
      if (!info.isFile()) return error(res, 404, 'not_found')
      res.writeHead(200, { 'Content-Type': types[extname(file)] || 'application/octet-stream', 'Content-Length': info.size,
        'Cache-Control': pathname.startsWith('/assets/') ? 'public, max-age=31536000, immutable' : 'no-cache' })
      if (req.method === 'HEAD') return res.end()
      const stream = createReadStream(file)
      stream.on('error', () => res.destroy())
      res.on('close', () => stream.destroy())
      stream.pipe(res)
    } catch { error(res, 404, 'not_found') }
  })
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const server = createFrontendServer({
    backendURL: process.env.MINI_AGENT_BACKEND_URL || 'http://mini-agent:8080',
    authToken: process.env.MINI_AGENT_AUTH_TOKEN,
  })
  server.listen(Number(process.env.PORT || 3000), '0.0.0.0', () => console.log('Mini Agent Web 已启动，监听 3000（或 PORT 指定端口）'))
  const shutdown = () => {
    server.close(() => process.exit(0))
    setTimeout(() => { server.closeAllConnections(); process.exit(0) }, 10_000).unref()
  }
  process.on('SIGTERM', shutdown)
  process.on('SIGINT', shutdown)
}
