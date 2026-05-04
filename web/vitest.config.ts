import { defineConfig } from 'vitest/config'
import { fileURLToPath, URL } from 'node:url'
import viteConfig from './vite.config'

export default defineConfig({
  ...viteConfig,
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    environment: 'jsdom',
    exclude: ['node_modules/**', 'e2e/**', 'dist/**'],
  },
})
