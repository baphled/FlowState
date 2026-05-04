import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ModelPicker from './ModelPicker.vue'
import { useChatStore } from '@/stores/chatStore'
import type { Agent, Model } from '@/types'

function makeModels(): Model[] {
  return [
    { id: 'claude-opus-4', name: 'Claude Opus 4', providerId: 'anthropic' },
    { id: 'claude-sonnet-4', name: 'Claude Sonnet 4', providerId: 'anthropic' },
    { id: 'gpt-4o', name: 'GPT-4o', providerId: 'openai' },
  ]
}

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'senior',
    name: 'Senior Engineer',
    description: 'Implements features',
    ...overrides,
  }
}

describe('ModelPicker', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders the current model name', async () => {
    const store = useChatStore()
    store.currentModelId = 'claude-opus-4'
    store.currentProviderId = 'anthropic'
    store.availableModels = makeModels()

    const wrapper = mount(ModelPicker)
    await flushPromises()

    expect(wrapper.find('[data-testid="model-picker"]').text()).toBe('claude-opus-4')
  })

  it('shows "Select model" when no model is selected', async () => {
    const store = useChatStore()
    store.availableModels = makeModels()

    const wrapper = mount(ModelPicker)
    await flushPromises()

    expect(wrapper.find('[data-testid="model-picker"]').text()).toBe('Select model')
  })

  it('opens the fuzzy search modal on click', async () => {
    const store = useChatStore()
    store.availableModels = makeModels()

    const wrapper = mount(ModelPicker)
    await flushPromises()

    expect(wrapper.findComponent({ name: 'FuzzySearchModal' }).props('open')).toBe(false)

    await wrapper.find('[data-testid="model-picker"]').trigger('click')

    expect(wrapper.findComponent({ name: 'FuzzySearchModal' }).props('open')).toBe(true)
  })

  it('calls setModel with split provider and model ids on select', async () => {
    const store = useChatStore()
    store.availableModels = makeModels()
    const setModelSpy = vi.spyOn(store, 'setModel').mockResolvedValue()

    const wrapper = mount(ModelPicker)
    await flushPromises()

    await wrapper.find('[data-testid="model-picker"]').trigger('click')
    await flushPromises()

    const modal = wrapper.findComponent({ name: 'FuzzySearchModal' })
    modal.vm.$emit('select', { id: 'anthropic:claude-opus-4', label: 'Claude Opus 4', group: 'anthropic' })
    await flushPromises()

    expect(setModelSpy).toHaveBeenCalledWith('claude-opus-4', 'anthropic')
  })

  it('maps availableModels to FuzzySearchItems grouped by provider', async () => {
    const store = useChatStore()
    store.availableModels = makeModels()

    const wrapper = mount(ModelPicker)
    await flushPromises()

    await wrapper.find('[data-testid="model-picker"]').trigger('click')
    await flushPromises()

    const modal = wrapper.findComponent({ name: 'FuzzySearchModal' })
    const items = modal.props('items') as Array<{ id: string; label: string; group?: string }>

    expect(items).toHaveLength(3)
    expect(items[0]).toEqual({ id: 'anthropic:claude-opus-4', label: 'Claude Opus 4', group: 'anthropic' })
    expect(items[1]).toEqual({ id: 'anthropic:claude-sonnet-4', label: 'Claude Sonnet 4', group: 'anthropic' })
    expect(items[2]).toEqual({ id: 'openai:gpt-4o', label: 'GPT-4o', group: 'openai' })
  })

  it('calls loadModels on mount when availableModels is empty', async () => {
    const store = useChatStore()
    const loadModelsSpy = vi.spyOn(store, 'loadModels').mockResolvedValue()

    mount(ModelPicker)
    await flushPromises()

    expect(loadModelsSpy).toHaveBeenCalledTimes(1)
  })

  it('does not call loadModels on mount when availableModels is already populated', async () => {
    const store = useChatStore()
    store.availableModels = makeModels()
    const loadModelsSpy = vi.spyOn(store, 'loadModels').mockResolvedValue()

    mount(ModelPicker)
    await flushPromises()

    expect(loadModelsSpy).not.toHaveBeenCalled()
  })

  it('closes the modal when close event is emitted', async () => {
    const store = useChatStore()
    store.availableModels = makeModels()

    const wrapper = mount(ModelPicker)
    await flushPromises()

    await wrapper.find('[data-testid="model-picker"]').trigger('click')
    await flushPromises()
    expect(wrapper.findComponent({ name: 'FuzzySearchModal' }).props('open')).toBe(true)

    const modal = wrapper.findComponent({ name: 'FuzzySearchModal' })
    modal.vm.$emit('close')
    await flushPromises()

    expect(wrapper.findComponent({ name: 'FuzzySearchModal' }).props('open')).toBe(false)
  })

  describe('agent preferred-model awareness', () => {
    it('renders only the agent\'s preferred models under a strict policy', async () => {
      const store = useChatStore()
      store.availableModels = makeModels()
      store.availableAgentDetails = [
        makeAgent({
          id: 'junior',
          name: 'Junior',
          model_policy: 'strict',
          preferred_models: [
            { provider: 'anthropic', model: 'claude-haiku-4' },
            { provider: 'anthropic', model: 'claude-sonnet-4' },
          ],
        }),
      ]
      store.agentId = 'junior'
      // Add the haiku to the catalogue so the strict filter has at
      // least one model matching the agent's preference. The opus and
      // gpt-4o entries from makeModels must be filtered out.
      store.availableModels = [
        ...makeModels(),
        { id: 'claude-haiku-4', name: 'Claude Haiku 4', providerId: 'anthropic' },
      ]

      const wrapper = mount(ModelPicker)
      await flushPromises()

      await wrapper.find('[data-testid="model-picker"]').trigger('click')
      await flushPromises()

      const modal = wrapper.findComponent({ name: 'FuzzySearchModal' })
      const items = modal.props('items') as Array<{ id: string; label: string }>

      expect(items.map((i) => i.id)).toEqual([
        'anthropic:claude-haiku-4',
        'anthropic:claude-sonnet-4',
      ])
    })

    it('shows every model under a permissive policy', async () => {
      const store = useChatStore()
      store.availableAgentDetails = [
        makeAgent({
          id: 'senior',
          name: 'Senior',
          model_policy: 'permissive',
          preferred_models: [
            { provider: 'anthropic', model: 'claude-opus-4' },
          ],
        }),
      ]
      store.agentId = 'senior'
      store.availableModels = makeModels()

      const wrapper = mount(ModelPicker)
      await flushPromises()

      await wrapper.find('[data-testid="model-picker"]').trigger('click')
      await flushPromises()

      const modal = wrapper.findComponent({ name: 'FuzzySearchModal' })
      const items = modal.props('items') as Array<{ id: string; label: string }>

      expect(items).toHaveLength(3)
    })

    it('orders preferred models first under a permissive policy', async () => {
      const store = useChatStore()
      store.availableAgentDetails = [
        makeAgent({
          id: 'senior',
          name: 'Senior',
          model_policy: 'permissive',
          preferred_models: [
            { provider: 'openai', model: 'gpt-4o' },
          ],
        }),
      ]
      store.agentId = 'senior'
      store.availableModels = makeModels()

      const wrapper = mount(ModelPicker)
      await flushPromises()

      await wrapper.find('[data-testid="model-picker"]').trigger('click')
      await flushPromises()

      const modal = wrapper.findComponent({ name: 'FuzzySearchModal' })
      const items = modal.props('items') as Array<{ id: string; label: string; meta?: string }>

      // Preferred entry must lead the list; the rest preserve their
      // original order so the picker remains stable for non-preferred
      // models.
      expect(items[0].id).toBe('openai:gpt-4o')
      expect(items[0].meta).toContain('Preferred')
    })

    it('treats no-agent or no-policy as fully permissive', async () => {
      const store = useChatStore()
      store.availableAgentDetails = []
      store.agentId = ''
      store.availableModels = makeModels()

      const wrapper = mount(ModelPicker)
      await flushPromises()

      await wrapper.find('[data-testid="model-picker"]').trigger('click')
      await flushPromises()

      const modal = wrapper.findComponent({ name: 'FuzzySearchModal' })
      const items = modal.props('items') as Array<{ id: string }>

      expect(items).toHaveLength(3)
    })

    it('degrades a strict policy with empty preferred list to permissive', async () => {
      const store = useChatStore()
      store.availableAgentDetails = [
        makeAgent({
          id: 'misconfigured',
          name: 'Misconfigured',
          model_policy: 'strict',
          preferred_models: [],
        }),
      ]
      store.agentId = 'misconfigured'
      store.availableModels = makeModels()

      const wrapper = mount(ModelPicker)
      await flushPromises()

      await wrapper.find('[data-testid="model-picker"]').trigger('click')
      await flushPromises()

      const modal = wrapper.findComponent({ name: 'FuzzySearchModal' })
      const items = modal.props('items') as Array<{ id: string }>

      // strict + empty list is meaningless; must not lock the
      // operator out of every model.
      expect(items).toHaveLength(3)
    })

    it('re-evaluates available models when the active agent changes', async () => {
      const store = useChatStore()
      store.availableAgentDetails = [
        makeAgent({
          id: 'junior',
          name: 'Junior',
          model_policy: 'strict',
          preferred_models: [
            { provider: 'anthropic', model: 'claude-sonnet-4' },
          ],
        }),
        makeAgent({
          id: 'senior',
          name: 'Senior',
          model_policy: 'permissive',
        }),
      ]
      store.agentId = 'junior'
      store.availableModels = makeModels()

      const wrapper = mount(ModelPicker)
      await flushPromises()

      await wrapper.find('[data-testid="model-picker"]').trigger('click')
      await flushPromises()

      const modal = wrapper.findComponent({ name: 'FuzzySearchModal' })
      let items = modal.props('items') as Array<{ id: string }>
      expect(items).toHaveLength(1)
      expect(items[0].id).toBe('anthropic:claude-sonnet-4')

      // Switch agent — the picker must reflect the new policy.
      store.agentId = 'senior'
      await flushPromises()

      items = modal.props('items') as Array<{ id: string }>
      expect(items).toHaveLength(3)
    })
  })
})
