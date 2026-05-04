import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ToolBubble from './ToolBubble.vue'

describe('ToolBubble', () => {
  const defaultProps = {
    toolName: 'test-tool',
    title: 'Test Tool'
  }

  it('renders with correct data attributes', () => {
    const wrapper = mount(ToolBubble, {
      props: {
        ...defaultProps,
        status: 'running'
      }
    })

    const root = wrapper.find('[data-testid="tool-bubble"]')
    expect(root.exists()).toBe(true)
    expect(root.attributes('data-component')).toBe('tool')
    expect(root.attributes('data-tool')).toBe('test-tool')
    expect(root.attributes('data-status')).toBe('running')
    expect(root.attributes('data-open')).toBe('false')
  })

  it('defaults status to completed', () => {
    const wrapper = mount(ToolBubble, {
      props: defaultProps
    })
    expect(wrapper.find('[data-testid="tool-bubble"]').attributes('data-status')).toBe('completed')
  })

  it('renders title and subtitle', () => {
    const wrapper = mount(ToolBubble, {
      props: {
        ...defaultProps,
        subtitle: 'Processing data...'
      }
    })
    expect(wrapper.text()).toContain('Test Tool')
    expect(wrapper.text()).toContain('Processing data...')
  })

  it('toggles open state when clicking trigger', async () => {
    const wrapper = mount(ToolBubble, {
      props: defaultProps
    })
    const trigger = wrapper.find('.tool-bubble__trigger')
    const root = wrapper.find('[data-testid="tool-bubble"]')

    expect(root.attributes('data-open')).toBe('false')
    
    await trigger.trigger('click')
    expect(root.attributes('data-open')).toBe('true')
    
    await trigger.trigger('click')
    expect(root.attributes('data-open')).toBe('false')
  })

  it('starts expanded when defaultOpen is true', () => {
    const wrapper = mount(ToolBubble, {
      props: {
        ...defaultProps,
        defaultOpen: true
      }
    })
    expect(wrapper.find('[data-testid="tool-bubble"]').attributes('data-open')).toBe('true')
  })

  it('renders slot content inside body', () => {
    const wrapper = mount(ToolBubble, {
      props: {
        ...defaultProps,
        defaultOpen: true
      },
      slots: {
        default: '<div class="slot-content">Internal Tool Content</div>'
      }
    })
    expect(wrapper.find('.slot-content').exists()).toBe(true)
    expect(wrapper.text()).toContain('Internal Tool Content')
  })

  it('shows spinner when status is running', () => {
    const wrapper = mount(ToolBubble, {
      props: {
        ...defaultProps,
        status: 'running'
      }
    })
    expect(wrapper.find('.tool-bubble__status-icon').text()).toBe('⟳')
  })

  it('shows checkmark when status is completed', () => {
    const wrapper = mount(ToolBubble, {
      props: {
        ...defaultProps,
        status: 'completed'
      }
    })
    expect(wrapper.find('.tool-bubble__status-icon').text()).toBe('✓')
  })

  it('applies error status correctly', () => {
    const wrapper = mount(ToolBubble, {
      props: {
        ...defaultProps,
        status: 'error'
      }
    })
    expect(wrapper.find('[data-testid="tool-bubble"]').attributes('data-status')).toBe('error')
  })

  it('expanded body has no viewport-relative max-height cap so content expands freely', async () => {
    const wrapper = mount(ToolBubble, {
      props: {
        ...defaultProps,
        defaultOpen: true
      }
    })

    const body = wrapper.find('.tool-bubble__body')
    expect(body.exists()).toBe(true)
    expect((body.element as HTMLElement).style.maxHeight).toBe('none')
  })
})
