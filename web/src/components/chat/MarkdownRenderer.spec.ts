import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import MarkdownRenderer from './MarkdownRenderer.vue'

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
