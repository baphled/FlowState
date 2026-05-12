/**
 * Shiki-based syntax highlighter for MarkdownRenderer fenced code blocks.
 *
 * B1 (Vue UI Parity vs OpenCode, May 2026). Pre-fix the MarkdownIt
 * instance had no `highlight` callback. This module supplies one
 * backed by Shiki, lazy-loaded so the initial JS bundle stays under
 * the 300 KB cap from the PR brief.
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
    const [
      { createHighlighterCoreSync },
      { createJavaScriptRegexEngine },
      vitesseDark,
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

    const created = createHighlighterCoreSync({
      themes: [vitesseDark.default],
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
    return highlighter.codeToHtml(code, {
      lang: grammar,
      theme: 'vitesse-dark',
    })
  } catch {
    return null
  }
}
