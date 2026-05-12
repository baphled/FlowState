import { defineConfig, devices } from '@playwright/test'

// Project-split runner.
//
// The bulk of the e2e suite mocks `/api/v1/*` via `page.route` and is
// safe to run on the default 8-worker pool — those specs are isolated
// per browser context and do not share state. A small number of specs
// drive the live FlowState backend at http://localhost:8080: they POST
// real sessions, consume real SSE streams, and call
// `restoreStateFromBackend` against the backend's actual session list.
// Those specs share the single backend process across all workers, so
// running them on the default pool produced cross-test interference
// (one worker's freshly-created session showing up in another worker's
// `restoreStateFromBackend` fallback, races on POST /messages, and
// SSE chunk cross-pollination).
//
// The fix is structural: a `mocked` project that runs in parallel and
// a `real-backend` project that runs `workers: 1`. Within the
// `real-backend` project, `chat-real-backend.spec.ts` already declares
// `test.describe.configure({ mode: 'serial' })`; pinning workers to 1
// extends that guarantee across files so the two real-backend specs
// cannot collide either.
//
// `--workers=1` on the command line still works for the whole run if
// a contributor wants to bypass the split for a debugging session.

const REAL_BACKEND_PATTERN = /(chat-real-backend|manual-session-drive|session-regression)\.spec\.ts/

export default defineConfig({
  testDir: './e2e',
  forbidOnly: !!process.env['CI'],
  retries: process.env['CI'] ? 2 : 0,
  reporter: 'html',
  use: {
    baseURL: process.env['PLAYWRIGHT_BASE_URL'] || 'http://localhost:5173',
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'mocked',
      testIgnore: REAL_BACKEND_PATTERN,
      fullyParallel: true,
      workers: process.env['CI'] ? 1 : undefined,
      use: { ...devices['Desktop Chrome'] },
    },
    {
      name: 'real-backend',
      testMatch: REAL_BACKEND_PATTERN,
      fullyParallel: false,
      workers: 1,
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: {
    command: 'npm run dev',
    url: 'http://localhost:5173',
    reuseExistingServer: !process.env['CI'],
    timeout: 120_000,
  },
})
