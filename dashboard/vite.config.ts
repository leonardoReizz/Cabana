import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    // Built straight into the Go embed path so `make build` produces one binary.
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    // Proxying keeps dev same-origin, so the API client never needs a base URL
    // and there is no CORS to reason about in development.
    proxy: {
      '/api': 'http://localhost:7373',
      '/healthz': 'http://localhost:7373',
    },
  },
})
