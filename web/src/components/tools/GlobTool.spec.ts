import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import GlobTool from './GlobTool.vue'

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

describe('GlobTool', () => {
  it('renders matched files and bubble metadata', () => {
    const wrapper = mount(GlobTool, {
      props: {
        toolName: 'glob',
        heading: 'src/**/*.ts',
        body: 'src/a.ts\nsrc/b.ts',
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
    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-tool')).toBe('glob')
    expect(wrapper.get('[data-testid="tool-subtitle"]').text()).toBe('src/**/*.ts')
    expect(wrapper.get('[data-component="glob-tool"]').text()).toContain('src/a.ts')
    expect(wrapper.find('[data-testid="copy-btn"]').exists()).toBe(true)
  })
})
