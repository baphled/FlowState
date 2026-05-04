import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import DelegationStrip from './DelegationStrip.vue'
import { useSwarmStore } from '@/stores/swarmStore'
import { useChatStore } from '@/stores/chatStore'
import type { SwarmEvent } from '@/types'

function makeDelegationEvent(overrides: Partial<SwarmEvent> = {}): SwarmEvent {
  return {
    id: 'evt-strip-1',
    type: 'delegation',
    status: 'started',
    timestamp: '2026-05-01T09:00:00Z',
    agent_id: 'researcher',
    metadata: {
      source_agent: 'planner',
      target_agent: 'researcher',
      child_session_id: 'session-child-99',
    },
    ...overrides,
  }
}

describe('DelegationStrip', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders one entry per delegation event in the swarm store', async () => {
    const swarmStore = useSwarmStore()
    swarmStore.events = [
      makeDelegationEvent(),
      makeDelegationEvent({
        id: 'evt-strip-2',
        metadata: {
          source_agent: 'planner',
          target_agent: 'reviewer',
          child_session_id: 'session-child-100',
        },
      }),
    ] as SwarmEvent[]

    const wrapper = mount(DelegationStrip)
    await flushPromises()

    const entries = wrapper.findAll('[data-testid^="delegation-entry-"]')
    expect(entries).toHaveLength(2)
  })

  it('navigates to the child session via chatStore.loadSessionMessages when an entry is clicked', async () => {
    const swarmStore = useSwarmStore()
    swarmStore.events = [makeDelegationEvent()] as SwarmEvent[]

    const chatStore = useChatStore()
    const loadSpy = vi.spyOn(chatStore, 'loadSessionMessages').mockResolvedValue()

    const wrapper = mount(DelegationStrip)
    await flushPromises()

    const entry = wrapper.find('[data-testid="delegation-entry-evt-strip-1"]')
    expect(entry.exists()).toBe(true)
    expect(entry.attributes('role')).toBe('button')
    expect(entry.attributes('tabindex')).toBe('0')

    await entry.trigger('click')
    await flushPromises()

    expect(loadSpy).toHaveBeenCalledWith('session-child-99')
  })

  it('is a no-op when the delegation event lacks a child_session_id', async () => {
    const swarmStore = useSwarmStore()
    swarmStore.events = [
      makeDelegationEvent({
        id: 'evt-no-child',
        metadata: { source_agent: 'planner', target_agent: 'researcher' },
      }),
    ] as SwarmEvent[]

    const chatStore = useChatStore()
    const loadSpy = vi.spyOn(chatStore, 'loadSessionMessages').mockResolvedValue()

    const wrapper = mount(DelegationStrip)
    await flushPromises()

    const entry = wrapper.find('[data-testid="delegation-entry-evt-no-child"]')
    expect(entry.exists()).toBe(true)
    // Lacks the keyboard-button affordance because it is not navigable.
    expect(entry.attributes('role')).not.toBe('button')

    await entry.trigger('click')
    await flushPromises()

    expect(loadSpy).not.toHaveBeenCalled()
  })

  it('renders nothing when there are no delegation events', async () => {
    const swarmStore = useSwarmStore()
    swarmStore.events = [] as SwarmEvent[]

    const wrapper = mount(DelegationStrip)
    await flushPromises()

    const entries = wrapper.findAll('[data-testid^="delegation-entry-"]')
    expect(entries).toHaveLength(0)
  })
})
