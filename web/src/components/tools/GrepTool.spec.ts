import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import GrepTool from './GrepTool.vue'

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

describe('GrepTool', () => {
  it('renders grep results and bubble metadata', () => {
    const wrapper = mount(GrepTool, {
      props: {
        toolName: 'grep',
        heading: 'TODO',
        body: 'src/a.ts:1:TODO found',
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
    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-tool')).toBe('grep')
    expect(wrapper.get('[data-testid="tool-subtitle"]').text()).toBe('TODO')
    expect(wrapper.get('[data-component="grep-tool"]').text()).toContain('src/a.ts:1:TODO found')
    expect(wrapper.find('[data-testid="copy-btn"]').exists()).toBe(true)
  })
})
