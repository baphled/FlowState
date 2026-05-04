import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import WriteTool from './WriteTool.vue'

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

describe('WriteTool', () => {
  it('renders written content inside the tool bubble', () => {
    const wrapper = mount(WriteTool, {
      props: {
        toolName: 'write',
        heading: '/tmp/output.txt',
        body: 'saved content',
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
    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-tool')).toBe('write')
    expect(wrapper.get('[data-testid="tool-subtitle"]').text()).toBe('/tmp/output.txt')
    expect(wrapper.get('[data-component="write-tool"]').text()).toContain('saved content')
    expect(wrapper.find('[data-testid="copy-btn"]').exists()).toBe(true)
  })
})
