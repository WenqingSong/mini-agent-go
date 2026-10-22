import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 生产环境不存在独立前端服务：页面与 /api 由同一个 Go 进程提供（同源）。
// 开发服务器只做代理，把 /api 转给本机已启动的应用容器，行为与生产同源一致。
const backend = process.env.MINI_AGENT_DEV_BACKEND || 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: { '/api': backend },
  },
})
