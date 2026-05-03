import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ModelPicker from './ModelPicker.vue'
import { useChatStore } from '@/stores/chatStore'
import type { Model } from '@/types'

function makeModels(): Model[] {
  return [
    { id: 'claude-opus-4', name: 'Claude Opus 4', providerId: 'anthropic' },
    { id: 'claude-sonnet-4', name: 'Claude Sonnet 4', providerId: 'anthropic' },
    { id: 'gpt-4o', name: 'GPT-4o', providerId: 'openai' },
  ]
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
})
