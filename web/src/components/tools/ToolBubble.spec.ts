import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { nextTick } from 'vue'
import ToolBubble from './ToolBubble.vue'
import { useChatStore } from '@/stores/chatStore'

describe('ToolBubble', () => {
  const defaultProps = {
    toolName: 'test-tool',
    title: 'Test Tool'
  }

  // ToolBubble reads chatStore.toolCardOpenOverride (PR6 — Collapse all /
  // Expand all) so every mount needs an active Pinia instance. The default
  // 'auto' state preserves the original local-isOpen behaviour the legacy
  // tests pin.
  beforeEach(() => {
    setActivePinia(createPinia())
  })

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

  // UI Parity bug-fix bundle (May 2026). P0-3: ToolBubble's isOpen was
  // seeded ONCE at mount from defaultOpen, so a card mounted while
  // status='running' (defaultOpen=false) that later transitioned to
  // status='error' (defaultOpen=true via parent cardDefaultOpen) never
  // force-opened — the error stayed hidden behind a click. The fix
  // watches defaultOpen and force-opens when it flips to true, without
  // auto-closing (so a user's manual open of a default-closed card
  // survives a subsequent defaultOpen=false transition).
  it('force-opens when defaultOpen flips from false to true (P0-3)', async () => {
    const wrapper = mount(ToolBubble, {
      props: { ...defaultProps, defaultOpen: false, status: 'running' }
    })
    const root = wrapper.find('[data-testid="tool-bubble"]')
    expect(root.attributes('data-open')).toBe('false')

    // Parent flips defaultOpen to true (e.g. status transitioned to 'error'
    // so cardDefaultOpen in the parent re-evaluated true).
    await wrapper.setProps({ defaultOpen: true, status: 'error' })
    expect(root.attributes('data-open')).toBe('true')
  })

  it('does not auto-close when defaultOpen flips back to false (preserves manual interaction)', async () => {
    const wrapper = mount(ToolBubble, {
      props: { ...defaultProps, defaultOpen: true, status: 'error' }
    })
    const root = wrapper.find('[data-testid="tool-bubble"]')
    expect(root.attributes('data-open')).toBe('true')

    // The parent's defaultOpen prop transitions away from true — the card
    // must remain open because a previous force-open is sticky (and a
    // user may have explicitly opened it). Auto-closing would yank UX.
    await wrapper.setProps({ defaultOpen: false, status: 'completed' })
    expect(root.attributes('data-open')).toBe('true')
  })

  it('preserves user manual open through a defaultOpen=false → defaultOpen=true cycle', async () => {
    const wrapper = mount(ToolBubble, {
      props: { ...defaultProps, defaultOpen: false }
    })
    const trigger = wrapper.find('.tool-bubble__trigger')
    const root = wrapper.find('[data-testid="tool-bubble"]')

    // User opens manually.
    await trigger.trigger('click')
    expect(root.attributes('data-open')).toBe('true')

    // defaultOpen flips true — we are already open, no change.
    await wrapper.setProps({ defaultOpen: true })
    expect(root.attributes('data-open')).toBe('true')

    // User closes manually.
    await trigger.trigger('click')
    expect(root.attributes('data-open')).toBe('false')
  })
})

// UI Parity PR6 — chatStore.toolCardOpenOverride respected by ToolBubble.
//
// The override has three states: 'auto' (default), 'expanded', 'collapsed'.
// When 'auto' the local isOpen ref drives rendering. When 'expanded' or
// 'collapsed' every ToolBubble is forced to the matching state regardless of
// per-card history. Flipping back to 'auto' restores the local ref so a
// user can resume per-card control.
describe('ToolBubble respects chatStore.toolCardOpenOverride', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('forces open when override flips to "expanded"', async () => {
    const wrapper = mount(ToolBubble, { props: { toolName: 't', title: 'T', defaultOpen: false } })
    const root = wrapper.find('[data-testid="tool-bubble"]')
    expect(root.attributes('data-open')).toBe('false')

    const chatStore = useChatStore()
    chatStore.toolCardOpenOverride = 'expanded'
    await nextTick()

    expect(root.attributes('data-open')).toBe('true')
  })

  it('forces closed when override flips to "collapsed"', async () => {
    const wrapper = mount(ToolBubble, { props: { toolName: 't', title: 'T', defaultOpen: true } })
    const root = wrapper.find('[data-testid="tool-bubble"]')
    expect(root.attributes('data-open')).toBe('true')

    const chatStore = useChatStore()
    chatStore.toolCardOpenOverride = 'collapsed'
    await nextTick()

    expect(root.attributes('data-open')).toBe('false')
  })

  it('restores local state when override is set back to "auto"', async () => {
    const wrapper = mount(ToolBubble, { props: { toolName: 't', title: 'T', defaultOpen: true } })
    const root = wrapper.find('[data-testid="tool-bubble"]')
    expect(root.attributes('data-open')).toBe('true')

    const chatStore = useChatStore()
    chatStore.toolCardOpenOverride = 'collapsed'
    await nextTick()
    expect(root.attributes('data-open')).toBe('false')

    chatStore.toolCardOpenOverride = 'auto'
    await nextTick()
    // Local ref still says open (defaultOpen=true seeded it), so unsetting the
    // override restores the prior state.
    expect(root.attributes('data-open')).toBe('true')
  })
})
