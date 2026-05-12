import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import GenericTool from './GenericTool.vue'

const CopyButton = {
  template: '<span data-testid="copy-btn" />',
}

const ToolBubble = {
  props: ['toolName', 'title', 'subtitle', 'status', 'defaultOpen'],
  template: `
    <div data-testid="tool-bubble" data-component="tool" :data-tool="toolName" :data-status="status" :data-default-open="defaultOpen ? 'true' : 'false'">
      <span data-testid="tool-title">{{ title }}</span>
      <span v-if="subtitle" data-testid="tool-subtitle">{{ subtitle }}</span>
      <slot />
    </div>
  `,
}

describe('GenericTool', () => {
  // I4: Unknown tool shapes — safer to start open so the user sees the
  // raw input/output rather than burying them.
  it('starts open by default (unknown shape — safer expanded)', () => {
    const wrapper = mount(GenericTool, {
      props: { toolName: 'webfetch', heading: 'http://x', body: 'body', status: 'completed' },
      global: { stubs: { CopyButton, ToolBubble } },
    })
    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-default-open')).toBe('true')
  })
  it('renders fallback content with the tool name and body', () => {
    const wrapper = mount(GenericTool, {
      props: {
        toolName: 'webfetch',
        heading: 'https://example.com',
        body: 'Fetched body',
        status: 'completed',
      },
      global: {
        stubs: {
          CopyButton,
          ToolBubble,
        },
      },
    })

    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-component')).toBe('tool')
    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-tool')).toBe('webfetch')
    expect(wrapper.get('[data-testid="tool-title"]').text()).toBe('webfetch')
    expect(wrapper.get('[data-testid="tool-subtitle"]').text()).toBe('https://example.com')
    expect(wrapper.get('[data-component="generic-tool"]').text()).toContain('Fetched body')
    expect(wrapper.find('[data-testid="copy-btn"]').exists()).toBe(true)
  })

  it('truncates tool input longer than two hundred characters', () => {
    const longInput = 'x'.repeat(240)
    const wrapper = mount(GenericTool, {
      props: {
        toolName: 'task',
        heading: 'delegate',
        body: 'Queued',
        toolInput: longInput,
      },
      global: {
        stubs: {
          CopyButton,
          ToolBubble,
        },
      },
    })

    const renderedInput = wrapper.get('[data-component="generic-tool-input"]')
    expect(renderedInput.text().length).toBe(203)
    expect(renderedInput.text().endsWith('...')).toBe(true)
  })
})
