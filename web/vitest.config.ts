import { defineConfig } from 'vitest/config'
import viteConfig from './vite.config'

export default defineConfig({
  ...viteConfig,
  resolve: {
    alias: {
      '@': '/home/baphled/Projects/FlowState.git/vue-web-frontend/web/src',
    },
  },
  test: {
    environment: 'jsdom',
  },
})
