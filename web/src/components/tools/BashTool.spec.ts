import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import BashTool from './BashTool.vue'

const CopyButton = {
  props: {
    text: {
      type: String,
      required: true,
    },
  },
  template: '<span data-testid="copy-btn">{{ text }}</span>',
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

describe('BashTool', () => {
  it('renders the bash command and output blocks', () => {
    const wrapper = mount(BashTool, {
      props: {
        toolName: 'bash',
        heading: 'ls -la',
        body: 'file-a\nfile-b',
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
    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-tool')).toBe('bash')
    expect(wrapper.find('[data-component="bash-tool"]').exists()).toBe(true)
    expect(wrapper.get('[data-component="bash-command"]').text()).toContain('ls -la')
    expect(wrapper.get('[data-component="bash-output"]').text()).toContain('file-a')
    expect(wrapper.findAll('[data-testid="copy-btn"]')).toHaveLength(2)
  })

  it('renders only the command block when output is empty', () => {
    const wrapper = mount(BashTool, {
      props: {
        toolName: 'bash',
        heading: 'pwd',
        body: '',
      },
      global: {
        stubs: {
          CopyButton,
          ToolBubble,
        },
      },
    })

    expect(wrapper.get('[data-component="bash-command"]').text()).toContain('pwd')
    expect(wrapper.find('[data-component="bash-output"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="copy-btn"]')).toHaveLength(1)
  })
})
