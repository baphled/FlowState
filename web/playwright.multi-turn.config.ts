import { defineConfig, devices } from "@playwright/test";

// Dedicated config for the multi-turn streaming verification spec.
// Uses port 5273 (port 5173 is taken by a parallel worktree's dev server).
// The vite dev server is started externally before running this config so we
// do not contend with the default playwright.config.ts webServer setup.
export default defineConfig({
  testDir: "./e2e",
  testMatch: /chat-multi-turn-streaming\.spec\.ts/,
  fullyParallel: false,
  retries: 0,
  workers: 1,
  reporter: "list",
  use: {
    baseURL: "http://127.0.0.1:5273",
    trace: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
