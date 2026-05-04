import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import DelegationPanel from './DelegationPanel.vue'
import { useSwarmStore } from '@/stores/swarmStore'
import { useChatStore } from '@/stores/chatStore'
import type { SwarmEvent } from '@/types'

function makeDelegationEvent(overrides: Partial<SwarmEvent> = {}): SwarmEvent {
  return {
    id: 'evt-delegation-1',
    type: 'delegation',
    status: 'started',
    timestamp: new Date().toISOString(),
    agent_id: 'researcher',
    metadata: {
      source_agent: 'planner',
      target_agent: 'researcher',
      child_session_id: 'session-child-42',
    },
    ...overrides,
  }
}

describe('DelegationPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('navigates to the child session when a delegation card with child_session_id is clicked', async () => {
    const swarmStore = useSwarmStore()
    swarmStore.events = [makeDelegationEvent()] as SwarmEvent[]

    const chatStore = useChatStore()
    const loadSpy = vi.spyOn(chatStore, 'loadSessionMessages').mockResolvedValue()

    const wrapper = mount(DelegationPanel)
    await flushPromises()

    const card = wrapper.find('[data-testid="delegation-evt-delegation-1"]')
    expect(card.exists()).toBe(true)

    await card.trigger('click')
    await flushPromises()

    expect(loadSpy).toHaveBeenCalledWith('session-child-42')
  })

  it('renders cards with child_session_id as keyboard-activatable buttons so the click target is reachable', async () => {
    const swarmStore = useSwarmStore()
    swarmStore.events = [makeDelegationEvent()] as SwarmEvent[]

    const wrapper = mount(DelegationPanel)
    await flushPromises()

    const card = wrapper.find('[data-testid="delegation-evt-delegation-1"]')
    expect(card.exists()).toBe(true)
    // Cards that can navigate must surface that affordance to assistive
    // tech and keyboard users — role=button + tabindex makes the card
    // operable, mirroring the SessionSwitcher dropdown's options.
    expect(card.attributes('role')).toBe('button')
    expect(card.attributes('tabindex')).toBe('0')
  })

  it('does not navigate when a delegation card lacks a child_session_id', async () => {
    const swarmStore = useSwarmStore()
    swarmStore.events = [
      makeDelegationEvent({
        id: 'evt-no-session',
        metadata: { source_agent: 'planner', target_agent: 'researcher' },
      }),
    ] as SwarmEvent[]

    const chatStore = useChatStore()
    const loadSpy = vi.spyOn(chatStore, 'loadSessionMessages').mockResolvedValue()

    const wrapper = mount(DelegationPanel)
    await flushPromises()

    const card = wrapper.find('[data-testid="delegation-evt-no-session"]')
    expect(card.exists()).toBe(true)

    await card.trigger('click')
    await flushPromises()

    expect(loadSpy).not.toHaveBeenCalled()
  })
})
