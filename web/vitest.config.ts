import { defineConfig } from "vitest/config";
import { fileURLToPath, URL } from "node:url";
import viteConfig from "./vite.config";

// Worker pool sizing for the vitest fork pool.
//
// Vitest 4 defaults to `pool: 'forks'` and sizes the pool from
// `os.availableParallelism()`. On a 16-core host that means up to ~16
// concurrent forks, each loading jsdom + the full app graph. Under that
// load the pool has been observed to over-spawn and surface as a
// "mass-failure" run ("51 failed | 14 passed") even though every spec
// passes when run in isolation. The symptom is worker spawn / IPC
// timeout under resource pressure, not a per-spec leak — `installLocalStorageStub`
// re-installs `window.localStorage` per test with `configurable: true`,
// `setActivePinia(createPinia())` runs in every `beforeEach`, and forks
// are independent processes that cannot pollute each other.
//
// We therefore pin the fork pool to a conservative cap. 50 % of the
// available cores leaves headroom for the dev server, the language
// server, and the rest of the user's session, while still running the
// 49 spec files in roughly the same wall-clock time as the unbounded
// default (the test phase itself is < 8 s — most time is jsdom import).
//
// Override locally with `--maxWorkers=N` if a different trade-off is
// needed for a one-off run.
export default defineConfig({
  ...viteConfig,
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  test: {
    environment: "jsdom",
    exclude: ["node_modules/**", "e2e/**", "dist/**"],
    pool: "forks",
    maxWorkers: "50%",
    minWorkers: 1,
  },
});
