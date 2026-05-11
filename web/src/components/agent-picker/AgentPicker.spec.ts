import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import AgentPicker from './AgentPicker.vue'
import { useChatStore } from '@/stores/chatStore'
import type { Agent, Swarm } from '@/types'

function makeAgents(): Agent[] {
  return [
    {
      id: 'planner',
      name: 'Planner Agent',
      description: 'Plans tasks',
    },
    {
      id: 'coder',
      name: 'Coder Agent',
      description: 'Writes code',
    },
  ]
}

function makeSwarms(): Swarm[] {
  return [
    {
      id: 'npr-onboarding',
      description: 'NPR onboarding',
      lead: 'npr-onboarding-lead',
      members: ['npr-profile-synthesizer', 'npr-quality-reviewer'],
    },
  ]
}

describe('AgentPicker', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows "Select agent" when no agent is selected', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(AgentPicker)
    await flushPromises()

    expect(wrapper.find('[data-testid="agent-picker"]').text()).toContain('Select agent')
  })

  it('renders the current agent name', async () => {
    const store = useChatStore()
    store.availableAgentDetails = makeAgents()
    store.agentId = 'planner'

    const wrapper = mount(AgentPicker)
    await flushPromises()

    expect(wrapper.find('[data-testid="agent-picker"]').text()).toContain('Planner Agent')
  })

  it('renders the current swarm id when the active chat target is a swarm', async () => {
    const store = useChatStore()
    store.availableAgentDetails = makeAgents()
    store.swarms = makeSwarms()
    store.agentId = 'npr-onboarding'

    const wrapper = mount(AgentPicker)
    await flushPromises()

    expect(wrapper.find('[data-testid="agent-picker"]').text()).toContain('@npr-onboarding')
  })

  it('opens the fuzzy search modal on click', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(AgentPicker, { attachTo: document.body })
    await flushPromises()

    await wrapper.find('[data-testid="agent-picker"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="fuzzy-search-backdrop"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('calls setAgent when an item is selected from the modal', async () => {
    const store = useChatStore()
    store.availableAgentDetails = makeAgents()
    const setAgentSpy = vi.spyOn(store, 'setAgent').mockResolvedValue()

    const wrapper = mount(AgentPicker, { attachTo: document.body })
    await flushPromises()

    await wrapper.find('[data-testid="agent-picker"]').trigger('click')
    await flushPromises()

    const item = wrapper.find('[data-testid="fuzzy-search-item-planner"]')
    expect(item.exists()).toBe(true)
    await item.trigger('click')
    await flushPromises()

    expect(setAgentSpy).toHaveBeenCalledWith('planner')
    expect(wrapper.find('[data-testid="fuzzy-search-backdrop"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('calls loadAgents on mount if availableAgentDetails is empty', async () => {
    const store = useChatStore()
    const loadAgentsSpy = vi.spyOn(store, 'loadAgents').mockResolvedValue()

    mount(AgentPicker)
    await flushPromises()

    expect(loadAgentsSpy).toHaveBeenCalledTimes(1)
  })

  it('does not call loadAgents on mount if agents are already loaded', async () => {
    const store = useChatStore()
    store.availableAgentDetails = makeAgents()
    const loadAgentsSpy = vi.spyOn(store, 'loadAgents').mockResolvedValue()

    mount(AgentPicker)
    await flushPromises()

    expect(loadAgentsSpy).not.toHaveBeenCalled()
  })

  it('maps agent descriptions to fuzzy search item meta', async () => {
    const store = useChatStore()
    store.availableAgentDetails = makeAgents()
    vi.spyOn(store, 'setAgent').mockResolvedValue()

    const wrapper = mount(AgentPicker, { attachTo: document.body })
    await flushPromises()

    await wrapper.find('[data-testid="agent-picker"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="fuzzy-search-item-planner"]').text()).toContain('Plans tasks')
    expect(wrapper.find('[data-testid="fuzzy-search-item-coder"]').text()).toContain('Writes code')
    wrapper.unmount()
  })
})
