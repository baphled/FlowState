import { describe, expect, it, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import MarkdownRenderer from './MarkdownRenderer.vue'
import { ensureHighlighterLoaded } from '@/lib/markdownHighlighter'
import { useChatStore } from '@/stores/chatStore'

describe('MarkdownRenderer', () => {
  beforeEach(() => {
    // The image allow-list (N9 / task-08) reaches into the chat store
    // for `currentSessionId` to constrain `<img src="/api/v1/sessions/.../">`
    // URLs to the active session. Activate a fresh Pinia per test so the
    // store starts in its default null-currentSessionId shape and individual
    // It blocks can seed the field where the cross-session check matters.
    setActivePinia(createPinia())
  })

  it('renders plain text as a paragraph', () => {
    const wrapper = mount(MarkdownRenderer, {
      props: { content: 'Hello world' },
    })

    expect(wrapper.find('p').exists()).toBe(true)
    expect(wrapper.find('p').text()).toBe('Hello world')
  })

  it('renders markdown headings as <h1> through <h6> elements', () => {
    const wrapper = mount(MarkdownRenderer, {
      props: { content: '# Title\n## Subtitle\n### Section' },
    })

    expect(wrapper.find('h1').exists()).toBe(true)
    expect(wrapper.find('h1').text()).toBe('Title')
    expect(wrapper.find('h2').exists()).toBe(true)
    expect(wrapper.find('h2').text()).toBe('Subtitle')
    expect(wrapper.find('h3').exists()).toBe(true)
    expect(wrapper.find('h3').text()).toBe('Section')
  })

  it('renders inline code with a <code> element', () => {
    const wrapper = mount(MarkdownRenderer, {
      props: { content: 'Use `console.log()` for debugging' },
    })

    expect(wrapper.find('code').exists()).toBe(true)
    expect(wrapper.find('code').text()).toBe('console.log()')
  })

  it('renders fenced code blocks inside a <pre><code> wrapper', () => {
    const wrapper = mount(MarkdownRenderer, {
      props: { content: '```js\nconst x = 1;\n```' },
    })

    const pre = wrapper.find('pre')
    expect(pre.exists()).toBe(true)
    expect(pre.find('code').exists()).toBe(true)
    expect(pre.find('code').text()).toContain('const x = 1;')
  })

  // B1 (Vue UI Parity vs OpenCode, May 2026): syntax highlighting in
  // markdown code blocks. Pre-fix the MarkdownIt instance has no
  // `highlight` callback, so fenced code blocks render as a plain
  // `<pre><code class="language-…">…</code></pre>` with zero token
  // markup. OpenCode's TUI ships Shiki-tokenised output (per-token
  // <span style="color:…">). This contract pins the post-fix state at
  // the component-seam level: a fenced typescript block produces a
  // wrapper carrying a Shiki marker class AND at least one inline
  // colour token. The specific token text is intentionally not
  // asserted — Shiki's tokenisation can drift between grammar bumps
  // without changing the user-visible outcome.
  describe('syntax highlighting (B1)', () => {
    it('emits Shiki-tokenised spans for a fenced typescript block', async () => {
      // Shiki is lazy-loaded to keep the initial JS bundle under the
      // 300 KB cap (PR brief). The component triggers the load on
      // mount and re-renders when the highlighter resolves. The spec
      // awaits that resolution explicitly so it asserts the post-
      // load state, not the plain `<pre><code>` first paint.
      await ensureHighlighterLoaded()
      const wrapper = mount(MarkdownRenderer, {
        props: {
          content: '```typescript\nconst answer: number = 42;\n```',
        },
      })
      await flushPromises()

      const html = wrapper.html()
      // Shiki wraps its output in a `<pre class="shiki ...">` element.
      // The class marker survives transformer pipelines and is the
      // load-bearing signal that the highlight callback fired.
      expect(html).toContain('shiki')
      // At least one inline coloured token must appear. Shiki emits
      // each token as `<span style="color:#xxxxxx">…</span>` (CSS
      // variables theme switches the `--shiki-light` / `--shiki-dark`
      // CSS custom properties instead, also matched here).
      expect(html).toMatch(/<span[^>]*(?:color:|--shiki)/i)
      // The original content must survive — the token spans render
      // the same characters, just wrapped.
      expect(wrapper.text()).toContain('const')
      expect(wrapper.text()).toContain('42')
    })

    it('emits Shiki-tokenised spans for a fenced bash block', async () => {
      // OpenCode embeds bash among the six baseline languages; the
      // bash grammar is the most-touched on chat output, so a
      // standalone contract pins it.
      await ensureHighlighterLoaded()
      const wrapper = mount(MarkdownRenderer, {
        props: {
          content: '```bash\necho "hello world"\n```',
        },
      })
      await flushPromises()

      expect(wrapper.html()).toContain('shiki')
      expect(wrapper.html()).toMatch(/<span[^>]*(?:color:|--shiki)/i)
      expect(wrapper.text()).toContain('echo')
    })

    it('renders a fenced code block with an unknown language without crashing', () => {
      // Shiki throws on unknown grammars unless the highlight callback
      // catches and falls back. Brainfuck is not in the bundled set —
      // the highlight callback must degrade gracefully to plain
      // <pre><code> output rather than break the entire message.
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '```brainfuck\n+++>+++\n```' },
      })

      // The bubble itself must still render (graceful degradation).
      expect(wrapper.find('.markdown-body').exists()).toBe(true)
      expect(wrapper.text()).toContain('+++>+++')
    })

    it('renders a fence with no language as plain text without crashing', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '```\nuntagged content\n```' },
      })

      expect(wrapper.find('pre').exists()).toBe(true)
      expect(wrapper.text()).toContain('untagged content')
    })
  })

  // N3 (Vue UI Parity vs OpenCode, May 2026): Shiki theme parity with
  // FlowState themes. The highlighter MUST emit CSS-variable token
  // colours rather than hardcoded vitesse-dark hex so that
  // `themes.css` palettes can swap code-block colours by toggling
  // `data-theme` on <html>. Shiki's idiomatic shape for this is the
  // multi-theme mode with `defaultColor: false`, which produces
  // `<span style="--shiki-dark:#xxx;--shiki-light:#yyy;…">` tokens.
  describe('N3 — CSS-variable token colours', () => {
    it('emits --shiki-<themeKey> CSS variables on tokens, not raw hex colour', async () => {
      // Pin the post-fix shape explicitly: each token carries at least
      // one `--shiki-<key>:` declaration. The bare `color:#xxxxxx` form
      // is the pre-fix hardcoded-vitesse-dark contract — the spec must
      // FAIL when that form sneaks back in alongside, since CSS
      // specificity would let it win over our `--shiki-<key>` rule.
      await ensureHighlighterLoaded()
      const wrapper = mount(MarkdownRenderer, {
        props: {
          content: '```typescript\nconst answer: number = 42;\n```',
        },
      })
      await flushPromises()

      const html = wrapper.html()
      // CSS-variable form present.
      expect(html).toMatch(/--shiki-[a-zA-Z0-9_-]+:\s*#?[a-zA-Z0-9]+/)
      // The hardcoded "style=\"color:#xxxxxx\"" form must be absent —
      // multi-theme mode replaces it with --shiki-<key> declarations.
      // (We only forbid the per-token inline `color:#xxxxxx` form;
      // background/foreground on the wrapper <pre> uses a `color:`
      // declaration that references a `var(...)` and is fine.)
      expect(html).not.toMatch(/<span[^>]*style="[^"]*\bcolor:\s*#[0-9a-fA-F]/)
    })
  })

  // N4 (Vue UI Parity vs OpenCode, May 2026): per-code-block copy
  // affordance. The whole-message copy lives on MessageBubble — this
  // contract scopes the per-fence copy button to MarkdownRenderer
  // where each fenced block gets its own clipboard handle. Hover-
  // reveal styling is fine; the testid presence assertion does not
  // require visibility.
  describe('per-code-block copy buttons (N4)', () => {
    it('renders a copy button inside each fenced code block', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: {
          content:
            '```bash\necho one\n```\n\nsome text\n\n```ts\nconst two = 2;\n```',
        },
      })

      const buttons = wrapper.findAll(
        '[data-testid="markdown-code-copy-btn"]',
      )
      expect(buttons.length).toBe(2)
    })

    it('renders no per-block copy button when the message has no code', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: 'just a paragraph, no fences' },
      })

      expect(
        wrapper.find('[data-testid="markdown-code-copy-btn"]').exists(),
      ).toBe(false)
    })
  })

  it('renders tables with <table>, <thead>, <tbody>, <th>, and <td>', () => {
    const wrapper = mount(MarkdownRenderer, {
      props: {
        content: '| Name | Value |\n| --- | --- |\n| foo | 42 |',
      },
    })

    expect(wrapper.find('table').exists()).toBe(true)
    expect(wrapper.find('thead').exists()).toBe(true)
    expect(wrapper.find('tbody').exists()).toBe(true)
    expect(wrapper.find('th').exists()).toBe(true)
    expect(wrapper.find('td').exists()).toBe(true)
    const cells = wrapper.findAll('td')
    expect(cells.map((c) => c.text())).toContain('42')
  })

  it('renders bold and italic text correctly', () => {
    const wrapper = mount(MarkdownRenderer, {
      props: { content: '**bold** and *italic*' },
    })

    expect(wrapper.find('strong').exists()).toBe(true)
    expect(wrapper.find('strong').text()).toBe('bold')
    expect(wrapper.find('em').exists()).toBe(true)
    expect(wrapper.find('em').text()).toBe('italic')
  })

  it('renders unordered and ordered lists', () => {
    const wrapper = mount(MarkdownRenderer, {
      props: { content: '- item one\n- item two\n\n1. first\n2. second' },
    })

    expect(wrapper.find('ul').exists()).toBe(true)
    expect(wrapper.find('ol').exists()).toBe(true)
    expect(wrapper.findAll('li').length).toBe(4)
  })

  it('renders blockquotes', () => {
    const wrapper = mount(MarkdownRenderer, {
      props: { content: '> A wise quote' },
    })

    expect(wrapper.find('blockquote').exists()).toBe(true)
    expect(wrapper.find('blockquote').text()).toContain('A wise quote')
  })

  it('renders links as <a> elements', () => {
    const wrapper = mount(MarkdownRenderer, {
      props: { content: '[FlowState](https://example.com)' },
    })

    const link = wrapper.find('a')
    expect(link.exists()).toBe(true)
    expect(link.attributes('href')).toBe('https://example.com')
    expect(link.text()).toBe('FlowState')
  })

  it('renders horizontal rules', () => {
    const wrapper = mount(MarkdownRenderer, {
      props: { content: 'above\n\n---\n\nbelow' },
    })

    expect(wrapper.find('hr').exists()).toBe(true)
  })

  // N9 (Vue UI Parity vs OpenCode, May 2026) flipped the markdown-it
  // `html` option from `false` to `true` so the renderer can surface
  // a strict allow-list of `<img>` tags. Every OTHER raw-HTML tag
  // (script, iframe, object, …) still drops out of the rendered DOM
  // via the post-render allow-list filter — the threat model the
  // original `html: false` posture closed remains closed.
  //
  // Pre-N9 this test was titled "does not render raw HTML (html
  // option is false)"; the assertion (no `<script>` survives) was
  // load-bearing on the underlying `html: false` config. The flip
  // preserves the assertion at a different layer of the pipeline
  // (allow-list filter, not parser-level strip) — so the same
  // contract still holds.
  it('does not render raw HTML script tags (allow-list strips non-<img>)', () => {
    const wrapper = mount(MarkdownRenderer, {
      props: { content: '<script>alert("xss")</script>' },
    })

    expect(wrapper.find('script').exists()).toBe(false)
  })

  it('converts line breaks to <br> tags (breaks option is true)', () => {
    const wrapper = mount(MarkdownRenderer, {
      props: { content: 'line one\nline two' },
    })

    expect(wrapper.html()).toContain('<br>')
  })

  it('renders empty content as an empty container', () => {
    const wrapper = mount(MarkdownRenderer, {
      props: { content: '' },
    })

    expect(wrapper.find('.markdown-body').exists()).toBe(true)
    expect(wrapper.find('.markdown-body').text()).toBe('')
  })

  it('updates rendered output when the content prop changes', async () => {
    const wrapper = mount(MarkdownRenderer, {
      props: { content: '# Initial' },
    })

    expect(wrapper.find('h1').text()).toBe('Initial')

    await wrapper.setProps({ content: '# Updated' })

    expect(wrapper.find('h1').text()).toBe('Updated')
  })

  describe('XSS link sanitization (M6)', () => {
    // Bug Hunt May 2026 § M6: chat-message bodies are rendered via
    // v-html. markdown-it 14's default validateLink uses a regex on the
    // trimmed lowercased URL string and does NOT URL-decode first, so
    // `javascript%3Aalert(1)` slips past the gate, becomes
    // `href="javascript%3Aalert(1)"`, and the browser decodes it on
    // click — script executes in the chat origin. The fix is a strict
    // scheme allowlist (http/https/mailto/anchor/relative); blob/data/
    // ftp/scheme-relative are also kept off because they're not a
    // legitimate chat-message link target. Pin the threat model and
    // confirm benign schemes still render.

    it('strips javascript: scheme from link href', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[click](javascript:alert(1))' },
      })

      const link = wrapper.find('a')
      if (link.exists()) {
        const href = link.attributes('href') ?? ''
        expect(href.toLowerCase()).not.toContain('javascript:')
      }
      // Belt-and-braces: even if rendered as text, raw HTML must not
      // include a javascript: href.
      expect(wrapper.html().toLowerCase()).not.toContain('href="javascript:')
    })

    it('strips mixed-case JaVaScRiPt: scheme (case-insensitive)', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[click](JaVaScRiPt:alert(1))' },
      })

      expect(wrapper.html().toLowerCase()).not.toContain('href="javascript:')
    })

    it('strips data: URLs (defense in depth)', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: {
          content: '[click](data:text/html,<script>alert(1)</script>)',
        },
      })

      expect(wrapper.html().toLowerCase()).not.toContain('href="data:')
    })

    it('strips vbscript: scheme', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[click](vbscript:msgbox(1))' },
      })

      expect(wrapper.html().toLowerCase()).not.toContain('href="vbscript:')
    })

    it('strips URL-encoded javascript: bypass (javascript%3A...)', () => {
      // The actual bug: markdown-it's default validator regex-tests the
      // trimmed lowercased string but does not URL-decode, so this slips
      // through and renders as href="javascript%3Aalert(1)" — the
      // browser decodes %3A → ":" on navigation and executes.
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[click](javascript%3Aalert(1))' },
      })

      // No anchor must be produced; the source text is allowed to remain
      // visible (markdown-it falls back to literal rendering on a
      // rejected link).
      expect(wrapper.find('a').exists()).toBe(false)
      const html = wrapper.html().toLowerCase()
      expect(html).not.toContain('href="javascript')
      expect(html).not.toContain('href="javascript%3a')
    })

    it('strips blob: URLs', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[click](blob:https://x.com/abc)' },
      })

      expect(wrapper.html().toLowerCase()).not.toContain('href="blob:')
    })

    it('strips file: URLs', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[click](file:///etc/passwd)' },
      })

      expect(wrapper.html().toLowerCase()).not.toContain('href="file:')
    })

    it('strips ftp: URLs (not in chat-link allowlist)', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[click](ftp://example.com)' },
      })

      expect(wrapper.html().toLowerCase()).not.toContain('href="ftp:')
    })

    it('strips scheme-relative // URLs (protocol-inheriting)', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[click](//evil.com)' },
      })

      expect(wrapper.html()).not.toContain('href="//')
    })

    it('strips data:image/* URLs (link context, not <img>)', () => {
      // markdown-it permits data:image/* by default for <img> sources.
      // For chat link targets it's still unwanted — close it.
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[click](data:image/png;base64,AAAA)' },
      })

      expect(wrapper.html().toLowerCase()).not.toContain('href="data:')
    })

    it('preserves https:// links as live anchors', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[FlowState](https://example.com)' },
      })

      const link = wrapper.find('a')
      expect(link.exists()).toBe(true)
      expect(link.attributes('href')).toBe('https://example.com')
    })

    it('preserves http:// links as live anchors', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[insecure](http://example.com)' },
      })

      const link = wrapper.find('a')
      expect(link.exists()).toBe(true)
      expect(link.attributes('href')).toBe('http://example.com')
    })

    it('preserves mailto: links as live anchors', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[contact](mailto:hello@example.com)' },
      })

      const link = wrapper.find('a')
      expect(link.exists()).toBe(true)
      expect(link.attributes('href')).toBe('mailto:hello@example.com')
    })

    it('preserves anchor (#fragment) links as live anchors', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[jump](#section-2)' },
      })

      const link = wrapper.find('a')
      expect(link.exists()).toBe(true)
      expect(link.attributes('href')).toBe('#section-2')
    })

    it('preserves relative path links as live anchors', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[docs](./guide.md)' },
      })

      const link = wrapper.find('a')
      expect(link.exists()).toBe(true)
      expect(link.attributes('href')).toBe('./guide.md')
    })

    it('preserves root-relative links as live anchors', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '[home](/dashboard)' },
      })

      const link = wrapper.find('a')
      expect(link.exists()).toBe(true)
      expect(link.attributes('href')).toBe('/dashboard')
    })
  })

  // N9 (Vue UI Parity vs OpenCode, May 2026) — plan "Chat Attachments
  // Backend (May 2026)" §6 task-08. The markdown-it `html` option is
  // now `true` so the renderer can surface raw `<img>` tags that the
  // assistant emits, but a strict allow-list filter constrains the
  // `src` attribute to two shapes:
  //
  //   1. base64 data URLs in PNG / JPEG / GIF / WEBP (NOT svg+xml —
  //      AC-08-SVG-Excluded, SVG can carry inline <script>/event
  //      handlers and the four Anthropic-supported types are
  //      sufficient).
  //   2. Same-origin attachment URLs under the active session id only
  //      (cross-session injection defence — plan R9).
  //
  // Every other tag is stripped from the rendered DOM. Every other
  // `<img>` src shape (http(s), javascript:, file:, blob:, cross-session
  // attachment URL, …) is dropped.
  describe('image allow-list (N9 / task-08)', () => {
    const TINY_PNG_B64 =
      'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkAAIAAAoAAv/lxKUAAAAASUVORK5CYII='

    it('preserves <img src="data:image/png;base64,..."> in rendered output', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: `<img src="data:image/png;base64,${TINY_PNG_B64}" alt="cat">` },
      })

      const img = wrapper.find('img')
      expect(img.exists()).toBe(true)
      expect(img.attributes('src')).toBe(`data:image/png;base64,${TINY_PNG_B64}`)
    })

    it('preserves <img src="data:image/jpeg;base64,..."> in rendered output', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '<img src="data:image/jpeg;base64,AAAA">' },
      })

      expect(wrapper.find('img').exists()).toBe(true)
    })

    it('preserves <img src="data:image/gif;base64,..."> in rendered output', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '<img src="data:image/gif;base64,AAAA">' },
      })

      expect(wrapper.find('img').exists()).toBe(true)
    })

    it('preserves <img src="data:image/webp;base64,..."> in rendered output', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '<img src="data:image/webp;base64,AAAA">' },
      })

      expect(wrapper.find('img').exists()).toBe(true)
    })

    // AC-08-SVG-Excluded — load-bearing acceptance criterion. SVG must
    // be rejected because SVG can carry inline <script> tags and event
    // handlers (onload="…", onmouseover="…", …) that execute in the
    // page's origin. The four Anthropic-supported image types are
    // sufficient for the round-trip render path; SVG buys us no
    // additional surface and ships a known attack vector.
    it('drops <img src="data:image/svg+xml;base64,..."> (AC-08-SVG-Excluded)', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: {
          // Minimal SVG carrying an onload script handler.
          content:
            '<img src="data:image/svg+xml;base64,PHN2ZyBvbmxvYWQ9ImFsZXJ0KDEpIiB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciPjwvc3ZnPg==">',
        },
      })

      expect(wrapper.find('img').exists()).toBe(false)
      // Defence in depth: no `src="data:image/svg` anywhere in raw HTML.
      expect(wrapper.html().toLowerCase()).not.toContain('data:image/svg')
    })

    it('preserves <img> with a same-session attachment URL (active session match)', () => {
      const chat = useChatStore()
      chat.currentSessionId = 'session-abc'

      const wrapper = mount(MarkdownRenderer, {
        props: {
          content: '<img src="/api/v1/sessions/session-abc/attachments/aid-1">',
        },
      })

      const img = wrapper.find('img')
      expect(img.exists()).toBe(true)
      expect(img.attributes('src')).toBe(
        '/api/v1/sessions/session-abc/attachments/aid-1',
      )
    })

    // R9 — cross-session injection defence. An assistant-rendered
    // `<img>` referencing another session's attachment must be dropped
    // AND the block must surface an observable signal so a test (or an
    // operator listening on `window`) can confirm the defence fired.
    // The plan permits "console.warn or a typed event" — we choose a
    // typed `window` event because it survives jsdom, doesn't need a
    // console spy, and gives the test a precise hook.
    it('drops <img> pointing at another session AND fires attachment_blocked.cross_session (R9)', async () => {
      const chat = useChatStore()
      chat.currentSessionId = 'session-abc'

      const events: Event[] = []
      const listener = (e: Event): void => {
        events.push(e)
      }
      window.addEventListener('attachment_blocked.cross_session', listener)

      try {
        const wrapper = mount(MarkdownRenderer, {
          props: {
            content:
              '<img src="/api/v1/sessions/session-OTHER/attachments/aid-1">',
          },
        })
        await flushPromises()

        expect(wrapper.find('img').exists()).toBe(false)
        // Defence in depth: no cross-session URL remains anywhere in HTML.
        expect(wrapper.html()).not.toContain('session-OTHER')

        // Observable: one block event fired for the dropped <img>.
        expect(events.length).toBeGreaterThanOrEqual(1)
      } finally {
        window.removeEventListener('attachment_blocked.cross_session', listener)
      }
    })

    it('drops <img src="http://evil.example/x.png"> (external URL)', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '<img src="http://evil.example/x.png">' },
      })

      expect(wrapper.find('img').exists()).toBe(false)
    })

    it('drops <img src="https://evil.example/x.png"> (external URL, https)', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '<img src="https://evil.example/x.png">' },
      })

      expect(wrapper.find('img').exists()).toBe(false)
    })

    it('drops <img src="javascript:alert(1)">', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '<img src="javascript:alert(1)">' },
      })

      expect(wrapper.find('img').exists()).toBe(false)
      expect(wrapper.html().toLowerCase()).not.toContain('javascript:')
    })

    it('drops <img src="data:text/html,..."> (data: but not image)', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: {
          content:
            '<img src="data:text/html,<script>alert(1)</script>">',
        },
      })

      expect(wrapper.find('img').exists()).toBe(false)
    })

    it('drops <img> with no src attribute', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: { content: '<img alt="no src">' },
      })

      expect(wrapper.find('img').exists()).toBe(false)
    })

    it('still strips non-<img> raw HTML tags (script, iframe, object)', () => {
      const wrapper = mount(MarkdownRenderer, {
        props: {
          content:
            '<script>alert(1)</script><iframe src="https://x"></iframe><object data="x"></object>',
        },
      })

      expect(wrapper.find('script').exists()).toBe(false)
      expect(wrapper.find('iframe').exists()).toBe(false)
      expect(wrapper.find('object').exists()).toBe(false)
    })
  })
})
