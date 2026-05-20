import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// R8.regression — Dual-source boundary pin for the Live-indicator dual-source-
// of-truth between FE-side `chatStore.streamingFor()` and backend-authoritative
// `SessionSummary.activeTurnId`.
//
// Child Session Turn Registry Plumbing (May 2026) plan §R8:
//   "FE dual-source-of-truth between `streamingFor` (current-session) and
//    `activeTurnId` (list rendering) drifts over time as new consumers are
//    added without choosing a side."
//
// Boundary (mandatory):
//   - Current-session optimistic UI:        ChatView.vue + MessageInput.vue
//     → MUST consult chatStore.streamingFor
//   - Child-session / session-list surfaces: ChildSessionsPanel.vue +
//     SessionBrowser.vue + SessionSwitcher.vue
//     → MUST consult SessionSummary.activeTurnId; MUST NOT consult
//       chatStore.streamingFor for child-row Live indicators
//
// This spec is a static-text pin: it grep-scans each component's source and
// asserts the boundary holds. If a future engineer adds chatStore.streamingFor
// back into one of the three list-surface components — or removes it from
// ChatView / MessageInput — this spec blows up and forces them to consult
// §R8 before crossing the boundary.
//
// Why static-text rather than runtime: the boundary is a code-shape contract,
// not a runtime behaviour. Runtime asserting "did the component invoke
// streamingFor with id X" requires per-call spies that are fragile against
// refactors (e.g. wrapping in a computed). A grep on the source file is
// stable, fast, and uses the same observable surface a code reviewer would.

const WEB_ROOT = resolve(__dirname, '../../..')

function readComponent(relativePath: string): string {
  return readFileSync(resolve(WEB_ROOT, relativePath), 'utf8')
}

// Strip /* ... */ and // ... line comments so the assertions catch
// production-code references only, not the loud-disclosure comment block
// that DOES intentionally name both streamingFor and activeTurnId.
//
// Implementation note: the regex pair below is intentionally simple. It
// does not handle every edge case of nested comments or strings (Vue
// single-file-components don't have those in <script setup> blocks).
function stripComments(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '')
    .replace(/\s\/\/[^\n]*$/gm, '')
}

describe('Live-indicator dual-source boundary (R8 regression pin)', () => {
  it('ChildSessionsPanel must consult activeTurnId, never streamingFor in production code', () => {
    const raw = readComponent('src/components/chat/ChildSessionsPanel.vue')
    const code = stripComments(raw)

    // Must consume the backend-authoritative field.
    expect(code).toContain('activeTurnId')
    // Must NOT consume the FE-side optimistic-UI slot.
    expect(code).not.toContain('streamingFor')
  })

  it('SessionBrowser must consult activeTurnId, never streamingFor in production code', () => {
    const raw = readComponent('src/components/session-browser/SessionBrowser.vue')
    const code = stripComments(raw)

    expect(code).toContain('activeTurnId')
    expect(code).not.toContain('streamingFor')
  })

  it('SessionSwitcher must consult activeTurnId, never streamingFor in production code', () => {
    const raw = readComponent('src/components/session-switcher/SessionSwitcher.vue')
    const code = stripComments(raw)

    expect(code).toContain('activeTurnId')
    expect(code).not.toContain('streamingFor')
  })

  // The two STAY-on-streamingFor components. These pins guard against the
  // OTHER direction of drift: a future engineer who "consolidates" by
  // flipping current-session surfaces to activeTurnId would lose the
  // optimistic UI gap that motivates the dual-source.
  it('ChatView must consult streamingFor for current-session optimistic UI', () => {
    const raw = readComponent('src/views/ChatView.vue')
    const code = stripComments(raw)

    expect(code).toContain('streamingFor')
  })

  it('MessageInput must consult streamingFor for the Send/Stop swap optimistic UI', () => {
    const raw = readComponent('src/components/chat/MessageInput.vue')
    const code = stripComments(raw)

    expect(code).toContain('streamingFor')
  })
})
