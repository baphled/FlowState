# flowstate-web

The Vue 3 chat frontend for FlowState. Vite-built, Pinia state, Vitest +
Playwright tests, TypeScript strict.

## Local development

```bash
npm install
npm run dev          # Vite dev server, default :5173
npm run dev:full     # ditto, with the Go backend running alongside
```

`npm run dev` proxies `/api/*` to the local Go server (see
`vite.config.ts`). The frontend hits same-origin paths by default.

## Tests

```bash
npx vitest run                      # unit + component (vitest 4)
npx vitest run path/to/file.test.ts # focus on one file
npx playwright test                 # default e2e config (port 5173)
```

E2E specs live in `web/e2e/*.spec.ts`. Per-feature configs:

| Config                            | Purpose                 |
| --------------------------------- | ----------------------- |
| `playwright.config.ts`            | Default — most specs    |
| `playwright.delegation.config.ts` | Delegation-driven flows |
| `playwright.multi-turn.config.ts` | Multi-turn streaming    |

## Type checking

```bash
npx vue-tsc --noEmit
```

The TS Session/SessionSummary types are hand-mirrored against the Go
`internal/api/session_response.go` SessionResponse struct. The contract
spec at `src/types/contract.spec.ts` enumerates every JSON tag and
asserts the TS side mirrors it — adding a Go-side field without
updating both fails the test.

## Conventions

- See `CONTRIBUTING.md` for test-file naming, mock conventions, commit
  attribution.
- See `KEYBINDINGS.md` for the document-level keyboard shortcuts and
  picker key handling.

## Recommended Content Security Policy (production)

The dev server intentionally ships without a CSP so HMR works. **For
production deployment, set a CSP on the response that serves
`index.html`** — either via the Go server's response headers or via a
reverse proxy. Recommended starting point (adjust `default-src` /
`connect-src` to your deployment):

```http
Content-Security-Policy:
  default-src 'self';
  script-src 'self';
  style-src 'self' 'unsafe-inline';
  img-src 'self' data:;
  font-src 'self' data:;
  connect-src 'self';
  frame-ancestors 'none';
  base-uri 'self';
  form-action 'self';
```

Notes:

- `style-src 'self' 'unsafe-inline'` is required because Vue's scoped
  styles inject inline `<style>` blocks at build time. Tightening to
  `'self'` only requires switching to `<style>` extraction at build.
- `script-src 'self'` is enough for the Vite-built bundle (no inline
  scripts in `index.html`). If you serve a custom analytics snippet,
  either add it via a hash or move it to a separate file under the same
  origin.
- `connect-src 'self'` works for same-origin API. If the API moves to a
  different origin (`https://api.flowstate.app` while the SPA serves at
  `https://app.flowstate.app`), add the API origin to `connect-src` AND
  add it to `extraAllowedOrigins` in `apiHostAllowlist.ts` (see the
  allowlist module's docstring).
- `frame-ancestors 'none'` defeats clickjacking — drop only if you
  intentionally embed the chat in a parent frame (rare).

A meta-tag CSP in `index.html` is a fallback option but does NOT support
`frame-ancestors` and cannot be set via `report-only`. Prefer the
header form.

## Security posture

- API host overrides in localStorage are validated against
  `lib/apiHostAllowlist.ts`. The default policy permits same-origin,
  relative paths, and `http://localhost:*` only — anything else is
  rejected and the offending key is cleared from localStorage. Operator
  opt-in for cross-origin via `extraAllowedOrigins`.
- Markdown rendering uses `markdown-it` with `html: false` and
  `linkify: false` — no raw HTML reaches the renderer. Do not add the
  `html: true` option without an updated threat-model review.
- localStorage keys are global per-origin; FlowState assumes a single
  user per browser profile. Multi-user support requires per-user
  prefixing (see `settingsStore.ts` for the namespacing TODO).

## Build

```bash
npm run build        # vue-tsc + vite build → dist/
npm run preview      # serve the dist build locally
```
