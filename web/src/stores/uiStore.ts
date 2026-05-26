import { defineStore } from 'pinia'

/**
 * uiStore — render-environment flags that affect the whole SPA shell.
 *
 * Currently holds a single flag: whether the app is being rendered
 * inside Carbonyl (Chromium-in-terminal) via `./flowstate chat`. The Go
 * bridge appends `?carbonyl=1` to the URL it hands to Carbonyl
 * (see internal/carbonyl/run.go buildTargetURL) — the SPA reads that
 * flag at boot and exposes it here so any component can branch on it
 * without re-parsing the URL.
 *
 * Why a separate store (not chatStore / settingsStore):
 *  - chatStore (3300+ lines) is the chat-domain aggregate; render-mode
 *    flags do not belong with messages, agents, or streaming state.
 *  - settingsStore persists user preferences via localStorage; carbonyl
 *    mode is detected fresh per page load from the URL — different
 *    lifecycle entirely.
 *  - Future CSS-override work (terminal-friendly palette, no transitions,
 *    block-aligned spacing) will read this from many places — keeping
 *    it in a focused store keeps the touch points obvious.
 *
 * The flag is initialised to false; the boot path in main.ts is the
 * single writer (see detectCarbonylMode below). Test code can call
 * setCarbonylMode() directly.
 */
export const useUIStore = defineStore('ui', {
  state: () => ({
    carbonylMode: false,
  }),

  actions: {
    setCarbonylMode(active: boolean): void {
      this.carbonylMode = active
    },
  },
})

/**
 * detectCarbonylMode — single source of truth for the URL-flag check.
 *
 * Returns true when window.location.search contains `carbonyl=1`. The
 * boot path in main.ts uses this to (a) toggle the body class so future
 * CSS can target `.carbonyl-mode` selectors and (b) seed the Pinia flag
 * for component consumers (notably MessageInput's onMounted focus).
 *
 * Exported so the Vitest spec can exercise the same parser the runtime
 * uses — no second implementation drifts out of sync.
 */
export function detectCarbonylMode(search: string = window.location.search): boolean {
  try {
    const params = new URLSearchParams(search)
    return params.get('carbonyl') === '1'
  } catch {
    // URLSearchParams should not throw on any string, but defence in
    // depth — a broken URL must not crash the boot path.
    return false
  }
}
