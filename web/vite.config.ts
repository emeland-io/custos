import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

// For local development, `npm run dev` serves the SPA on Vite's own port
// and proxies /api, /auth and /git to a real `custos serve` (default
// http://127.0.0.1:8080, overridable with CUSTOS_DEV_BACKEND) so the SPA
// talks to the same origin in dev as it does once embedded.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const backend = env.CUSTOS_DEV_BACKEND || 'http://127.0.0.1:8080'
  return {
    plugins: [vue()],
    server: {
      proxy: {
        '/api': { target: backend, changeOrigin: true },
        '/auth': { target: backend, changeOrigin: true },
        '/git': { target: backend, changeOrigin: true },
      },
    },
    build: {
      outDir: 'dist',
    },
  }
})
