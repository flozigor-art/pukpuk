import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'

const outDir = resolve(__dirname, '../internal/webui/dist')

// The Go binary embeds ../internal/webui/dist; keep its placeholder file so
// `go build` works on a fresh checkout.
const keepPlaceholder = (): Plugin => ({
  name: 'keep-placeholder',
  closeBundle() {
    writeFileSync(resolve(outDir, '.keep'), 'web UI is not built\n')
  },
})

export default defineConfig({
  plugins: [react(), keepPlaceholder()],
  build: {
    outDir,
    emptyOutDir: true,
    chunkSizeWarningLimit: 900,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://localhost:8080', changeOrigin: false },
    },
  },
})
