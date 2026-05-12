import { describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import MarkdownRenderer from './MarkdownRenderer.vue'
import { ensureHighlighterLoaded } from '@/lib/markdownHighlighter'

describe('MarkdownRenderer', () => {
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

  it('does not render raw HTML (html option is false)', () => {
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
})
