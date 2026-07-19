import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// Dev: Vite serves the SPA on :5173 and proxies /v1 to the Go server so the
// session cookie is same-origin. Prod: `make build` embeds dist/ in the binary.
export default defineConfig({
  plugins: [svelte()],
  server: {
    proxy: {
      '/v1': {
        target: 'http://127.0.0.1:8210',
        changeOrigin: false,
      },
    },
  },
})
