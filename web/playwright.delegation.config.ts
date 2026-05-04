import { defineConfig, devices } from '@playwright/test'

// Standalone Playwright config used to verify the delegation-card-navigation
// regression on a dedicated port. The default config reuses any vite server
// already on :5173, which in a multi-worktree sandbox can be a stale copy of
// the codebase. Pinning a unique port keeps this spec deterministic.
const PORT = 5273

export default defineConfig({
  testDir: './e2e',
  testMatch: /delegation-card-navigation\.spec\.ts/,
  fullyParallel: false,
  forbidOnly: !!process.env['CI'],
  retries: 0,
  workers: 1,
  reporter: 'list',
  use: {
    baseURL: `http://localhost:${PORT}`,
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: {
    command: `npx vite --port ${PORT} --strictPort`,
    url: `http://localhost:${PORT}`,
    reuseExistingServer: false,
    timeout: 120_000,
  },
})
