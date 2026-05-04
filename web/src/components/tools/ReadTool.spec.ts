import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ReadTool from './ReadTool.vue'

const CopyButton = {
  template: '<span data-testid="copy-btn" />',
}

const ToolBubble = {
  props: ['toolName', 'title', 'subtitle', 'status'],
  template: `
    <div data-testid="tool-bubble" data-component="tool" :data-tool="toolName" :data-status="status">
      <span data-testid="tool-title">{{ title }}</span>
      <span v-if="subtitle" data-testid="tool-subtitle">{{ subtitle }}</span>
      <slot />
    </div>
  `,
}

describe('ReadTool', () => {
  it('renders the tool bubble, subtitle, and file content', () => {
    const wrapper = mount(ReadTool, {
      props: {
        toolName: 'read',
        heading: '/tmp/example.txt',
        body: 'hello world',
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
    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-tool')).toBe('read')
    expect(wrapper.get('[data-testid="tool-subtitle"]').text()).toBe('/tmp/example.txt')
    expect(wrapper.get('[data-component="read-tool"]').text()).toContain('hello world')
    expect(wrapper.find('[data-testid="copy-btn"]').exists()).toBe(true)
  })
})
