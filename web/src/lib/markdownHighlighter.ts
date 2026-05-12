/**
 * Shiki-based syntax highlighter for MarkdownRenderer fenced code blocks.
 *
 * B1 (Vue UI Parity vs OpenCode, May 2026). Pre-fix the MarkdownIt
 * instance had no `highlight` callback. This module supplies one
 * backed by Shiki, lazy-loaded so the initial JS bundle stays under
 * the 300 KB cap from the PR brief.
 *
 * N3 (May 2026 UI Parity PR4 — theme polish). Pre-fix the highlighter
 * hardcoded `vitesse-dark` as the single theme; tokens carried
 * `style="color:#xxxxxx"` and code blocks could not re-skin under a
 * `data-theme` swap on <html>. Post-fix we load the full set of
 * shipped FlowState themes and pass them to Shiki's multi-theme
 * mode with `defaultColor: false`. Shiki then emits each token as
 * `<span style="--shiki-dark:#fff;--shiki-light:#000;…">…</span>`,
 * and `themes.css` picks which `--shiki-<key>` is active per theme via
 * `[data-theme="X"] .shiki span { color: var(--shiki-X); }`. Toggling
 * the theme re-paints existing code without re-tokenisation.
 *
 * Lazy-loading strategy
 * ---------------------
 * - The grammars + theme + engine + Shiki core are dynamically
 *   imported on first call to `ensureHighlighterLoaded()`. The
 *   Promise is memoised so concurrent callers share the work.
 * - The result is cached in a module-local `highlighter` ref.
 * - Until the Promise resolves, `highlightCode()` returns `null`
 *   and the fence renderer falls back to plain `<pre><code>`.
 *   Once loaded, subscribers (`onHighlighterReady`) re-render so
 *   the same blocks get tokenised on the second paint.
 *
 * Bundle weight
 * -------------
 * - `@shikijs/core` (~30 KB) + `@shikijs/engine-javascript` (no
 *   WASM) + 9 grammars + 1 theme. All lazy. Initial JS bundle
 *   carries only the dynamic-import shim (a few bytes).
 * - Vite splits the lazy chunk automatically — the user sees the
 *   shiki bundle requested on first paint of a fenced block.
 *
 * Synchronous fence callback
 * --------------------------
 * MarkdownIt's `highlight` option is sync. After load, the cached
 * highlighter satisfies that contract via `codeToHtml`. Before
 * load, the callback signals failure with `null` and the fence
 * renderer falls back to plain `<pre><code>`.
 *
 * Graceful fallback
 * -----------------
 * Unknown languages, plain fences, or any Shiki error degrades to
 * the caller's plain fallback. Failure is silent — the bubble keeps
 * rendering.
 */
import type { HighlighterCore } from '@shikijs/core'

// Languages the highlighter knows about. Membership check before
// calling Shiki avoids surfacing "Language not loaded" errors from
// inside the highlight callback.
const SUPPORTED_LANGS = new Set([
  'bash',
  'javascript',
  'typescript',
  'markdown',
  'powershell',
  'zig',
  'json',
  'go',
  'python',
])

// Alias map — accept the common shorthand spellings users type in
// fences. Shiki only matches the canonical grammar names so we
// normalise here.
const LANG_ALIASES: Record<string, string> = {
  js: 'javascript',
  jsx: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  shell: 'bash',
  sh: 'bash',
  zsh: 'bash',
  ps: 'powershell',
  ps1: 'powershell',
  md: 'markdown',
  py: 'python',
  golang: 'go',
}

let highlighter: HighlighterCore | null = null
let loadPromise: Promise<HighlighterCore> | null = null
const readyListeners = new Set<() => void>()

/**
 * Resolve a fence-info language tag to a supported Shiki grammar
 * name, or `null` if unsupported. Strips whitespace and lower-cases
 * to match how Shiki's bundled grammar names are spelled.
 */
function resolveLang(raw: string): string | null {
  const cleaned = raw.trim().toLowerCase()
  if (cleaned === '') return null
  const mapped = LANG_ALIASES[cleaned] ?? cleaned
  if (!SUPPORTED_LANGS.has(mapped)) return null
  return mapped
}

/**
 * Kick off the lazy Shiki load. Returns a Promise that resolves
 * when the highlighter is ready. Idempotent — concurrent calls
 * share the same Promise. Callers do not need to await this; the
 * module re-renders subscribers via `onHighlighterReady` once
 * loading completes.
 */
export function ensureHighlighterLoaded(): Promise<HighlighterCore> {
  if (highlighter !== null) return Promise.resolve(highlighter)
  if (loadPromise !== null) return loadPromise

  loadPromise = (async (): Promise<HighlighterCore> => {
    // N3 — multi-theme palette. One Shiki theme per FlowState
    // `data-theme` value. Each theme key here MUST match a
    // `[data-theme="<key>"]` selector in `themes.css` so the CSS rule
    // `[data-theme="X"] .shiki span { color: var(--shiki-X); }` can
    // pick the right per-token variable.
    //
    // Mapping rationale:
    //   - dark:             vitesse-dark    (FlowState's original mark)
    //   - light:            vitesse-light   (sibling palette, same family)
    //   - terminal:         solarized-dark  (warm-toned amber/green that
    //                                        reads on a CRT-style chassis)
    //   - tokyo-night:      tokyo-night     (canonical mapping)
    //   - catppuccin-mocha: catppuccin-mocha (canonical mapping)
    //   - dracula:          dracula         (canonical mapping)
    //   - nord:             nord            (canonical mapping)
    const [
      { createHighlighterCoreSync },
      { createJavaScriptRegexEngine },
      vitesseDark,
      vitesseLight,
      solarizedDark,
      tokyoNight,
      catppuccinMocha,
      dracula,
      nord,
      bash,
      javascript,
      typescript,
      markdown,
      powershell,
      zig,
      json,
      go,
      python,
    ] = await Promise.all([
      import('@shikijs/core'),
      import('@shikijs/engine-javascript'),
      import('@shikijs/themes/vitesse-dark'),
      import('@shikijs/themes/vitesse-light'),
      import('@shikijs/themes/solarized-dark'),
      import('@shikijs/themes/tokyo-night'),
      import('@shikijs/themes/catppuccin-mocha'),
      import('@shikijs/themes/dracula'),
      import('@shikijs/themes/nord'),
      import('@shikijs/langs/bash'),
      import('@shikijs/langs/javascript'),
      import('@shikijs/langs/typescript'),
      import('@shikijs/langs/markdown'),
      import('@shikijs/langs/powershell'),
      import('@shikijs/langs/zig'),
      import('@shikijs/langs/json'),
      import('@shikijs/langs/go'),
      import('@shikijs/langs/python'),
    ])

    // Re-name the underlying Shiki themes to match FlowState's
    // data-theme keys so the CSS variable shape is `--shiki-dark`,
    // `--shiki-light`, `--shiki-terminal`, etc. The original Shiki name
    // would otherwise leak through (`--shiki-vitesse-dark`) and the
    // CSS rules would have to mirror an external naming convention.
    const themed = [
      { ...(vitesseDark.default as Record<string, unknown>), name: 'dark' },
      { ...(vitesseLight.default as Record<string, unknown>), name: 'light' },
      { ...(solarizedDark.default as Record<string, unknown>), name: 'terminal' },
      { ...(tokyoNight.default as Record<string, unknown>), name: 'tokyo-night' },
      { ...(catppuccinMocha.default as Record<string, unknown>), name: 'catppuccin-mocha' },
      { ...(dracula.default as Record<string, unknown>), name: 'dracula' },
      { ...(nord.default as Record<string, unknown>), name: 'nord' },
    ]

    const created = createHighlighterCoreSync({
      themes: themed,
      langs: [
        bash.default,
        javascript.default,
        typescript.default,
        markdown.default,
        powershell.default,
        zig.default,
        json.default,
        go.default,
        python.default,
      ],
      engine: createJavaScriptRegexEngine(),
    })
    highlighter = created
    // Notify subscribers so they can re-render now that Shiki is
    // available. The listener Set is snapshot-iterated to avoid
    // mutation-during-iteration if a listener unsubscribes itself
    // synchronously.
    const snap = Array.from(readyListeners)
    snap.forEach((fn) => {
      try {
        fn()
      } catch {
        // Listener errors must not block other subscribers — swallow.
      }
    })
    return created
  })()

  return loadPromise
}

/**
 * Subscribe to highlighter-ready notifications. Returns a cleanup
 * function the caller must invoke on unmount. Listener fires once,
 * synchronously, when Shiki finishes loading. If the highlighter is
 * already loaded at subscription time, the listener fires on the
 * next microtask (caller-side reactivity stays consistent).
 */
export function onHighlighterReady(fn: () => void): () => void {
  if (highlighter !== null) {
    queueMicrotask(fn)
    return () => {
      /* nothing to clean — fired already */
    }
  }
  readyListeners.add(fn)
  return () => {
    readyListeners.delete(fn)
  }
}

/**
 * Tokenise `code` as `lang`, returning Shiki's `<pre class="shiki …">`
 * HTML. Returns `null` if:
 *
 *   - The highlighter has not finished loading yet (caller falls back
 *     to plain `<pre><code>`; once the loader resolves the caller is
 *     notified via `onHighlighterReady` and re-renders).
 *   - The language is not in the supported set.
 *   - Any Shiki tokenisation error occurs.
 */
export function highlightCode(code: string, lang: string): string | null {
  if (highlighter === null) return null
  const grammar = resolveLang(lang)
  if (grammar === null) return null
  try {
    // N3 — multi-theme mode. `defaultColor: false` tells Shiki NOT to
    // pick one theme as the inline `color:` value; instead each token
    // ships a bundle of `--shiki-<key>` CSS variables, and the active
    // value is resolved via `themes.css`. Theme keys here MUST match
    // the `data-theme` values FlowState ships.
    return highlighter.codeToHtml(code, {
      lang: grammar,
      themes: {
        dark: 'dark',
        light: 'light',
        terminal: 'terminal',
        'tokyo-night': 'tokyo-night',
        'catppuccin-mocha': 'catppuccin-mocha',
        dracula: 'dracula',
        nord: 'nord',
      },
      defaultColor: false,
    })
  } catch {
    return null
  }
}
