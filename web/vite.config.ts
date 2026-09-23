import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:6718',
      '/v1': 'http://localhost:6718',
    },
  },
  test: {
    environment: 'happy-dom',
    // Generous timeout: first run after a cold `npm ci` (e.g. CI) can spend
    // >5s in module transform before a test even starts.
    testTimeout: 20000,
  },
})
