import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import MessageInput from './MessageInput.vue'
import { useChatStore } from '@/stores/chatStore'
import type { Agent } from '@/types'

/**
 * Drives the textarea state in tests: pushes the value through Vue's
 * v-model via `setValue` (so `inputText.value` is updated), then sets
 * the DOM caret position before firing a fresh `input` event so the
 * trigger detector reads the synced value-and-caret pair.
 */
async function typeInto(
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  inputWrapper: any,
  value: string,
  caret: number,
): Promise<void> {
  await inputWrapper.setValue(value)
  const el = inputWrapper.element as HTMLTextAreaElement
  el.selectionStart = caret
  el.selectionEnd = caret
  await inputWrapper.trigger('input')
  await flushPromises()
}

function makeAgents(): Agent[] {
  return [
    { id: 'planner', name: 'Planner Agent', description: 'Plans tasks' },
    { id: 'coder', name: 'Coder Agent', description: 'Writes code' },
  ]
}

describe('MessageInput slash and mention triggers', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('opens the slash command picker when the user types "/" at the start of the buffer', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    await typeInto(wrapper.get('[data-testid="message-input"]'), '/', 1)

    expect(wrapper.find('[data-testid="fuzzy-search-backdrop"]').exists()).toBe(true)
    // Slash commands surface mirrors the TUI builtins; clear is first.
    expect(wrapper.find('[data-testid="fuzzy-search-item-clear"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('opens the agent mention picker when the user types "@" mid-message', async () => {
    const store = useChatStore()
    store.availableAgentDetails = makeAgents()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    await typeInto(wrapper.get('[data-testid="message-input"]'), 'hey @', 5)

    expect(wrapper.find('[data-testid="fuzzy-search-backdrop"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="fuzzy-search-item-planner"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="fuzzy-search-item-coder"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('does NOT open the slash picker mid-word (e.g. inside "src/foo")', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    await typeInto(wrapper.get('[data-testid="message-input"]'), 'src/foo', 7)

    expect(wrapper.find('[data-testid="fuzzy-search-backdrop"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('does NOT open the mention picker inside an email-like token', async () => {
    const store = useChatStore()
    store.availableAgentDetails = makeAgents()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    await typeInto(wrapper.get('[data-testid="message-input"]'), 'foo@bar', 7)

    expect(wrapper.find('[data-testid="fuzzy-search-backdrop"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('inserts the slash command token when the user picks an entry', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    await typeInto(wrapper.get('[data-testid="message-input"]'), '/cle', 4)

    await wrapper.get('[data-testid="fuzzy-search-item-clear"]').trigger('click')
    await flushPromises()

    expect((wrapper.get('[data-testid="message-input"]').element as HTMLTextAreaElement).value).toBe('/clear ')
    expect(wrapper.find('[data-testid="fuzzy-search-backdrop"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('inserts the agent mention token when the user picks an entry', async () => {
    const store = useChatStore()
    store.availableAgentDetails = makeAgents()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    await typeInto(wrapper.get('[data-testid="message-input"]'), 'hey @plan', 9)

    await wrapper.get('[data-testid="fuzzy-search-item-planner"]').trigger('click')
    await flushPromises()

    expect((wrapper.get('[data-testid="message-input"]').element as HTMLTextAreaElement).value).toBe('hey @planner ')
    expect(wrapper.find('[data-testid="fuzzy-search-backdrop"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('Enter submits the message when no picker is open', async () => {
    const store = useChatStore()
    const sendSpy = vi.spyOn(store, 'sendMessage').mockResolvedValue()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await typeInto(inputWrapper, 'hello', 5)

    await inputWrapper.trigger('keydown', { key: 'Enter' })
    await flushPromises()

    expect(sendSpy).toHaveBeenCalledWith('hello')
    wrapper.unmount()
  })

  it('Enter does NOT submit when the slash picker is open — it stays scoped to the picker', async () => {
    const store = useChatStore()
    const sendSpy = vi.spyOn(store, 'sendMessage').mockResolvedValue()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await typeInto(inputWrapper, '/', 1)

    expect(wrapper.find('[data-testid="fuzzy-search-backdrop"]').exists()).toBe(true)

    // The textarea's keydown handler must NOT call submit while a
    // picker is open — picker keys (Enter, Esc, Up, Down) are owned by
    // FuzzySearchModal's document-level handler.
    await inputWrapper.trigger('keydown', { key: 'Enter' })
    await flushPromises()

    expect(sendSpy).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('Escape closes an open picker without clearing the buffer', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    const textarea = inputWrapper.element as HTMLTextAreaElement

    await typeInto(inputWrapper, '/cle', 4)

    expect(wrapper.find('[data-testid="fuzzy-search-backdrop"]').exists()).toBe(true)

    await inputWrapper.trigger('keydown', { key: 'Escape' })
    await flushPromises()

    expect(wrapper.find('[data-testid="fuzzy-search-backdrop"]').exists()).toBe(false)
    expect(textarea.value).toBe('/cle')
    wrapper.unmount()
  })
})
