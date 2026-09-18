import react from '@vitejs/plugin-react'
import { defineConfig, type ProxyOptions } from 'vite'
import path from 'path'

// The dev server serves only the bundle, so every /v1 and /v1/events call has to
// reach the running daemon or `pnpm dev` renders an app with no data. Point
// KEEPER_DAEMON at a different address when keeperd is not on its default port.
const daemon = process.env.KEEPER_DAEMON ?? 'http://127.0.0.1:7799'

// keeperd refuses any write whose Origin is not its own, which a browser on the
// dev server's port never is. changeOrigin only rewrites Host, so the proxy
// restates Origin as the daemon's before forwarding.
const daemonOrigin = new URL(daemon).origin

const proxy = (extra: ProxyOptions = {}): ProxyOptions => ({
  target: daemon,
  changeOrigin: true,
  configure: (instance) => {
    instance.on('proxyReq', (request) => {
      if (request.getHeader('origin')) request.setHeader('origin', daemonOrigin)
    })
  },
  ...extra,
})

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/v1': proxy({ ws: true }),
    },
  },
})
