import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import { defineComponent, h } from 'vue'
import DelegationStrip from './DelegationStrip.vue'
import { useSwarmStore } from '@/stores/swarmStore'
import { useChatStore } from '@/stores/chatStore'
import type { SwarmEvent } from '@/types'

function makeRouterForStripTests() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: defineComponent({ render: () => h('div') }) },
      { path: '/chat', name: 'chat', component: defineComponent({ render: () => h('div') }) },
      {
        path: '/agents/:id',
        name: 'agent-info',
        component: defineComponent({ render: () => h('div') }),
      },
    ],
  })
}

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

  // Regression cover for the delegation-card-navigates-to-AgentInfoView bug.
  // Even though DelegationStrip has never used <router-link> directly, the
  // user-facing contract is that clicking a delegation card loads the
  // delegated child session in chat WITHOUT routing to /agents/:id. This
  // pins that contract at the strip level so any future refactor that
  // re-introduces a router-link will be caught here, not in the live app.
  it('does not push a /agents/:id route when an entry is clicked', async () => {
    const router = makeRouterForStripTests()
    await router.push('/chat')
    await router.isReady()
    const pushSpy = vi.spyOn(router, 'push')

    const swarmStore = useSwarmStore()
    swarmStore.events = [makeDelegationEvent()] as SwarmEvent[]

    const chatStore = useChatStore()
    vi.spyOn(chatStore, 'loadSessionMessages').mockResolvedValue()

    const wrapper = mount(DelegationStrip, {
      global: { plugins: [router] },
    })
    await flushPromises()

    const entry = wrapper.find('[data-testid="delegation-entry-evt-strip-1"]')
    await entry.trigger('click')
    await flushPromises()

    const pushedToAgents = pushSpy.mock.calls.some((call) => {
      const target = call[0]
      if (typeof target === 'string') return target.startsWith('/agents/')
      if (target && typeof target === 'object' && 'path' in target) {
        return typeof target.path === 'string' && target.path.startsWith('/agents/')
      }
      return false
    })
    expect(pushedToAgents).toBe(false)
    expect(router.currentRoute.value.path).toBe('/chat')
  })
})
