import { beforeEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import CriticalErrorBanner from './CriticalErrorBanner.vue'
import { useChatStore } from '@/stores/chatStore'

/**
 * CriticalErrorBanner component specs — pin BEHAVIOUR observable to a
 * user, not internal call signatures. The banner is the UI affordance
 * for the engine's `stream_critical` event class (see Bug Fixes /
 * Critical Stream Error Gating in the vault). The chat store owns the
 * source-of-truth state (`criticalError`); the banner renders it.
 */
describe('CriticalErrorBanner', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('does not render when no critical error is in store state', () => {
    const wrapper = mount(CriticalErrorBanner)

    expect(wrapper.find('[data-testid="critical-error-banner"]').exists()).toBe(false)
  })

  it('renders the banner with the wire-sanitized message when criticalError is set', () => {
    const store = useChatStore()
    store.criticalError = {
      message: 'critical stream error',
      correlationId: 'abc123',
    }

    const wrapper = mount(CriticalErrorBanner)

    const banner = wrapper.find('[data-testid="critical-error-banner"]')
    expect(banner.exists()).toBe(true)
    expect(wrapper.find('[data-testid="critical-error-message"]').text()).toContain(
      'critical stream error',
    )
  })

  it('exposes role="alert" for screen-reader announcement', () => {
    const store = useChatStore()
    store.criticalError = { message: 'critical stream error', correlationId: 'abc123' }

    const wrapper = mount(CriticalErrorBanner)

    expect(wrapper.find('[data-testid="critical-error-banner"]').attributes('role')).toBe(
      'alert',
    )
  })

  it('hides the correlation id by default and reveals it via the "Show details" affordance', async () => {
    const store = useChatStore()
    store.criticalError = { message: 'critical stream error', correlationId: 'abc123' }

    const wrapper = mount(CriticalErrorBanner)

    // Hidden by default — the user does not see the technical id until
    // they ask for it.
    expect(wrapper.find('[data-testid="critical-error-correlation-id"]').exists()).toBe(false)

    await wrapper.find('[data-testid="critical-error-details-toggle"]').trigger('click')

    const idEl = wrapper.find('[data-testid="critical-error-correlation-id"]')
    expect(idEl.exists()).toBe(true)
    expect(idEl.text()).toBe('abc123')
  })

  it('clears the banner from the DOM when the Dismiss button is clicked', async () => {
    const store = useChatStore()
    store.criticalError = { message: 'critical stream error', correlationId: 'abc123' }

    const wrapper = mount(CriticalErrorBanner)
    expect(wrapper.find('[data-testid="critical-error-banner"]').exists()).toBe(true)

    await wrapper.find('[data-testid="critical-error-dismiss"]').trigger('click')

    expect(wrapper.find('[data-testid="critical-error-banner"]').exists()).toBe(false)
    expect(store.criticalError).toBeNull()
  })

  it('re-shows the banner after a dismiss when a fresh critical error arrives', async () => {
    const store = useChatStore()
    store.criticalError = { message: 'critical stream error', correlationId: 'first-id' }

    const wrapper = mount(CriticalErrorBanner)
    await wrapper.find('[data-testid="critical-error-dismiss"]').trigger('click')
    expect(wrapper.find('[data-testid="critical-error-banner"]').exists()).toBe(false)

    // Simulate a fresh stream_critical event landing on the store. In
    // production this happens inside applyContentEvent; the banner just
    // needs to react to the state change.
    store.criticalError = { message: 'critical stream error', correlationId: 'second-id' }
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="critical-error-banner"]').exists()).toBe(true)

    // Reveal details — the new correlation id must be displayed (not a
    // stale cached one from before dismissal).
    await wrapper.find('[data-testid="critical-error-details-toggle"]').trigger('click')
    expect(wrapper.find('[data-testid="critical-error-correlation-id"]').text()).toBe(
      'second-id',
    )
  })

  it('omits the "Show details" affordance when no correlation id is provided', () => {
    // Defensive: a degraded wire payload (no correlation_id) still
    // surfaces the banner with the message, but the details toggle is
    // hidden because there is nothing to reveal.
    const store = useChatStore()
    store.criticalError = { message: 'critical stream error', correlationId: '' }

    const wrapper = mount(CriticalErrorBanner)

    expect(wrapper.find('[data-testid="critical-error-banner"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="critical-error-details-toggle"]').exists()).toBe(false)
  })
})
