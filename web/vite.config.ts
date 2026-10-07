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
    // 覆盖率口径：只看 src/ 下的业务代码，测试自身与测试辅助不计入。
    // 不设 thresholds——覆盖率是给人看的度量，不是 CI 闸门。
    coverage: {
      provider: 'v8',
      include: ['src/**/*.{ts,vue}'],
      exclude: ['src/**/*.test.ts', 'src/test/**', 'src/main.ts'],
      reporter: ['text'],
    },
  },
})
