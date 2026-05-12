import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import EditTool from './EditTool.vue'

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

describe('EditTool', () => {
  // I4: Edit diffs ARE the value of the card — the diff is what the user
  // needs to see to verify the change. Open by default.
  it('starts open by default (diff is the value)', () => {
    const wrapper = mount(EditTool, {
      props: { toolName: 'edit', heading: '/a', body: '-x\n+y', status: 'completed' },
      global: { stubs: { CopyButton, ToolBubble } },
    })
    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-default-open')).toBe('true')
  })
  it('renders diff lines with added and removed styling', () => {
    const wrapper = mount(EditTool, {
      props: {
        toolName: 'edit',
        heading: '/tmp/edit.txt',
        body: '-before\n+after',
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
    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-tool')).toBe('edit')
    expect(wrapper.get('[data-testid="tool-subtitle"]').text()).toBe('/tmp/edit.txt')
    expect(wrapper.find('[data-component="edit-tool"]').exists()).toBe(true)
    expect(wrapper.find('[data-line-kind="removed"]').text()).toContain('-before')
    expect(wrapper.find('[data-line-kind="added"]').text()).toContain('+after')
    expect(wrapper.find('[data-testid="copy-btn"]').exists()).toBe(true)
  })

  it('falls back to plain content when no diff markers exist', () => {
    const wrapper = mount(EditTool, {
      props: {
        toolName: 'edit',
        heading: '/tmp/plain.txt',
        body: 'plain replacement text',
      },
      global: {
        stubs: {
          CopyButton,
          ToolBubble,
        },
      },
    })

    expect(wrapper.find('[data-line-kind="plain"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('plain replacement text')
  })
})
