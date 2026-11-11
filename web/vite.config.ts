import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// The build lands in the Go package that embeds it; in dev, /api is proxied to the Go server.
// API_URL points the proxy elsewhere (compose.yaml sets it to the api container).
// changeOrigin stays false so the browser's Host (localhost:5173) reaches Go unchanged:
// the server's CSRF check compares Origin with Host, and a rewritten Host would fail it.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.', '')
  return {
    plugins: [react(), tailwindcss()],
    build: {
      outDir: '../internal/webui/dist',
      emptyOutDir: true,
    },
    server: {
      proxy: {
        '/api': { target: env.API_URL || 'http://localhost:8080', changeOrigin: false },
      },
    },
  }
})
