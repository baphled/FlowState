import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import RecallSearchTool from './RecallSearchTool.vue'

const ToolBubble = {
  props: ['toolName', 'title', 'subtitle', 'status', 'defaultOpen'],
  template: `
    <div data-testid="tool-bubble" data-component="tool" :data-tool="toolName" :data-status="status" :data-default-open="defaultOpen ? 'true' : 'false'">
      <slot />
    </div>
  `,
}

describe('RecallSearchTool', () => {
  // I4: Recall search results are long. Start collapsed; the subtitle
  // already surfaces the result count so the user can decide to open.
  it('starts collapsed by default (search-results category)', () => {
    const wrapper = mount(RecallSearchTool, {
      props: { toolName: 'search_context', heading: 'q', body: '', status: 'completed' },
      global: { stubs: { ToolBubble } },
    })
    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-default-open')).toBe('false')
  })

  it('forces open when status is error', () => {
    const wrapper = mount(RecallSearchTool, {
      props: { toolName: 'search_context', heading: 'q', body: '', status: 'error' },
      global: { stubs: { ToolBubble } },
    })
    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-default-open')).toBe('true')
  })
  it('renders the query and parsed result entries', () => {
    const body = [
      'user: how do I fix the bubble nesting?',
      'assistant: remove the outer wrapper',
      'user: thanks',
    ].join('\n---\n')

    const wrapper = mount(RecallSearchTool, {
      props: {
        toolName: 'search_context',
        heading: 'how do I fix the bubble nesting?',
        body,
        status: 'completed',
        toolInput: JSON.stringify({ query: 'how do I fix the bubble nesting?' }),
      },
      global: { stubs: { ToolBubble } },
    })

    expect(wrapper.find('[data-component="recall-search-tool"]').exists()).toBe(true)
    const query = wrapper.find('[data-testid="recall-query"]')
    expect(query.exists()).toBe(true)
    expect(query.text()).toContain('how do I fix the bubble nesting?')

    const results = wrapper.findAll('[data-testid="recall-result"]')
    expect(results).toHaveLength(3)
    expect(results[0].text()).toContain('user')
    expect(results[0].text()).toContain('how do I fix the bubble nesting?')
    expect(results[1].text()).toContain('assistant')
    expect(results[1].text()).toContain('remove the outer wrapper')
  })

  it('limits visible results to a sensible cap and shows an overflow hint', () => {
    const body = Array.from({ length: 12 }, (_, i) => `user: result ${i + 1}`).join('\n---\n')

    const wrapper = mount(RecallSearchTool, {
      props: {
        toolName: 'search_context',
        heading: 'big query',
        body,
        status: 'completed',
      },
      global: { stubs: { ToolBubble } },
    })

    const results = wrapper.findAll('[data-testid="recall-result"]')
    expect(results.length).toBeLessThanOrEqual(10)
    expect(results.length).toBeGreaterThanOrEqual(5)

    const overflow = wrapper.find('[data-testid="recall-overflow"]')
    expect(overflow.exists()).toBe(true)
    expect(overflow.text()).toMatch(/and \d+ more/)
  })

  it('renders a no-results state when the body is empty', () => {
    const wrapper = mount(RecallSearchTool, {
      props: {
        toolName: 'search_context',
        heading: 'no hits',
        body: '',
        status: 'completed',
      },
      global: { stubs: { ToolBubble } },
    })

    expect(wrapper.find('[data-component="recall-search-tool"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="recall-empty"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-testid="recall-result"]')).toHaveLength(0)
  })
})
