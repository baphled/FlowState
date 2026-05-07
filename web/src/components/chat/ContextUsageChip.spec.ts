import { beforeEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ContextUsageChip from './ContextUsageChip.vue'
import { useChatStore } from '@/stores/chatStore'

/**
 * ContextUsageChip component specs — pin BEHAVIOUR observable to a
 * user, not internal call signatures. The chip is the UI affordance
 * for the engine's `context_usage` SSE event class (see Bug Fixes /
 * glm Context-Window Saturation Detection in the vault). The chat
 * store owns the source-of-truth state (`currentContextUsage`); the
 * chip renders it.
 */
describe('ContextUsageChip', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('does not render when currentContextUsage is null', () => {
    const wrapper = mount(ContextUsageChip)

    expect(wrapper.find('[data-testid="context-usage-chip"]').exists()).toBe(false)
  })

  it('does not render when limit is zero (degraded payload guard)', () => {
    // A zero-limit figure would render `1234/0` which is meaningless.
    // The engine suppresses the chunk when limit<=0 so this should
    // never reach the chip in practice, but the chip guards against
    // a future emitter regression.
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 1234,
      outputReserve: 4096,
      limit: 0,
      percentage: 0,
    }

    const wrapper = mount(ContextUsageChip)

    expect(wrapper.find('[data-testid="context-usage-chip"]').exists()).toBe(false)
  })

  it('renders the chip with input/limit and percentage when currentContextUsage is set', () => {
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 12345,
      outputReserve: 4096,
      limit: 100000,
      percentage: 12,
    }

    const wrapper = mount(ContextUsageChip)

    const chip = wrapper.find('[data-testid="context-usage-chip"]')
    expect(chip.exists()).toBe(true)
    // 12345 → 12K, 100000 → 100K
    expect(wrapper.find('[data-testid="context-usage-counts"]').text()).toBe('12K/100K')
    expect(wrapper.find('[data-testid="context-usage-percentage"]').text()).toBe('12%')
  })

  it('formats sub-1000 token counts verbatim', () => {
    // The compact 'K' formatter applies only above 1000. A small
    // session shows `42/100K` rather than `0K/100K` which would
    // mis-suggest a saturated state.
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 42,
      outputReserve: 4096,
      limit: 100000,
      percentage: 0,
    }

    const wrapper = mount(ContextUsageChip)

    expect(wrapper.find('[data-testid="context-usage-counts"]').text()).toBe('42/100K')
  })

  it('exposes role="status" with aria-live="polite" for screen-reader announcement', () => {
    // Informational severity — the chip updates per turn and the
    // user reads it at their own pace. role="status" + polite is the
    // appropriate pairing (assertive is reserved for the
    // CriticalErrorBanner which announces an unrecoverable session
    // state).
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 1000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 1,
    }

    const wrapper = mount(ContextUsageChip)

    const chip = wrapper.find('[data-testid="context-usage-chip"]')
    expect(chip.attributes('role')).toBe('status')
    expect(chip.attributes('aria-live')).toBe('polite')
  })

  it('classes the chip as "neutral" below the 75% threshold', () => {
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 50000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 50,
    }

    const wrapper = mount(ContextUsageChip)

    const chip = wrapper.find('[data-testid="context-usage-chip"]')
    expect(chip.attributes('data-severity')).toBe('neutral')
    expect(chip.classes()).toContain('context-usage-chip--neutral')
  })

  it('classes the chip as "warning" between 75% and 89%', () => {
    // 75% is the threshold where the conversation enters
    // "approaching saturation" territory. The chip pivots to amber
    // so the user sees the warning before the danger threshold.
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 80000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 80,
    }

    const wrapper = mount(ContextUsageChip)

    const chip = wrapper.find('[data-testid="context-usage-chip"]')
    expect(chip.attributes('data-severity')).toBe('warning')
    expect(chip.classes()).toContain('context-usage-chip--warning')
  })

  it('classes the chip as "danger" at or above 90%', () => {
    // 90%+ is "compact or fail next turn" territory. The chip pivots
    // to the same red severity as the CriticalErrorBanner so the
    // visual escalation across the chat surface is consistent.
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 95000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 95,
    }

    const wrapper = mount(ContextUsageChip)

    const chip = wrapper.find('[data-testid="context-usage-chip"]')
    expect(chip.attributes('data-severity')).toBe('danger')
    expect(chip.classes()).toContain('context-usage-chip--danger')
  })

  it('reactively updates the rendered figure when the store slice changes', async () => {
    // The chip subscribes to the store's reactive slice so successive
    // turns update the figure in place. A new context_usage event for
    // the same session should show new figures without remount.
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 1000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 1,
    }

    const wrapper = mount(ContextUsageChip)
    expect(wrapper.find('[data-testid="context-usage-percentage"]').text()).toBe('1%')

    store.currentContextUsage = {
      inputTokens: 80000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 80,
    }
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="context-usage-percentage"]').text()).toBe('80%')
    expect(wrapper.find('[data-testid="context-usage-chip"]').attributes('data-severity')).toBe(
      'warning',
    )
  })
})
