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

  // load_skills surfacing (May 2026): when an agent delegates with
  // `load_skills: ["memory-keeper", "knowledge-base"]`, the recipient
  // gets those skills injected into its system prompt. The user wants
  // to see WHICH skills were passed on the delegation card so they
  // can audit the knowledge context the child received. The data is
  // read from event.metadata.load_skills (string[]); it is OK for the
  // backend to populate this lazily — the card renders gracefully when
  // the field is absent.
  describe('load_skills surfacing', () => {
    it('renders each loaded skill as a chip when load_skills is non-empty', async () => {
      const swarmStore = useSwarmStore()
      swarmStore.events = [
        makeDelegationEvent({
          id: 'evt-with-skills',
          metadata: {
            source_agent: 'planner',
            target_agent: 'knowledge-base-curator',
            child_session_id: 'session-child-99',
            load_skills: ['memory-keeper', 'knowledge-base', 'obsidian-structure'],
          },
        }),
      ] as SwarmEvent[]

      const wrapper = mount(DelegationPanel)
      await flushPromises()

      const skills = wrapper.findAll('[data-testid="delegation-skill-chip"]')
      expect(skills).toHaveLength(3)
      const texts = skills.map(s => s.text())
      expect(texts).toEqual(['memory-keeper', 'knowledge-base', 'obsidian-structure'])
    })

    it('does not render a skills row when load_skills is missing', async () => {
      const swarmStore = useSwarmStore()
      swarmStore.events = [
        makeDelegationEvent({
          id: 'evt-no-skills',
          metadata: {
            source_agent: 'planner',
            target_agent: 'researcher',
            child_session_id: 'session-child-42',
          },
        }),
      ] as SwarmEvent[]

      const wrapper = mount(DelegationPanel)
      await flushPromises()

      expect(wrapper.find('[data-testid="delegation-skills-row"]').exists()).toBe(false)
    })

    it('does not render a skills row when load_skills is an empty array', async () => {
      const swarmStore = useSwarmStore()
      swarmStore.events = [
        makeDelegationEvent({
          id: 'evt-empty-skills',
          metadata: {
            source_agent: 'planner',
            target_agent: 'researcher',
            child_session_id: 'session-child-43',
            load_skills: [],
          },
        }),
      ] as SwarmEvent[]

      const wrapper = mount(DelegationPanel)
      await flushPromises()

      expect(wrapper.find('[data-testid="delegation-skills-row"]').exists()).toBe(false)
    })
  })
})
