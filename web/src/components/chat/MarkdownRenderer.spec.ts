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
})
