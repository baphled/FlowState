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

  it('Shift+Enter does NOT submit the message — allows inserting a newline', async () => {
    const store = useChatStore()
    const sendSpy = vi.spyOn(store, 'sendMessage').mockResolvedValue()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await typeInto(inputWrapper, 'hello', 5)

    await inputWrapper.trigger('keydown', { key: 'Enter', shiftKey: true })
    await flushPromises()

    expect(sendSpy).not.toHaveBeenCalled()
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

  it('surfaces a rejection toast when submit is attempted while isLoading is true (was silent drop)', async () => {
    // Pre-fix: MessageInput.submit early-returns silently when
    // store.isLoading is true. The user types "continue", presses Enter,
    // and sees nothing happen — leading them to conclude the chat is
    // stuck. The fix surfaces the rejection through a toast.
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()
    const sendSpy = vi.spyOn(store, 'sendMessage').mockResolvedValue()
    store.isLoading = true

    const { showToast } = await import('@/composables/useToast')
    const toastSpy = vi.spyOn({ showToast }, 'showToast')
    // Re-import so the spy can capture the call. vi.spyOn on a module
    // namespace requires the actual call site to read through the same
    // object — we rely on the production code calling the bare exported
    // showToast. A simpler approach: assert against the toasts ref.
    toastSpy.mockClear()

    const { useToast } = await import('@/composables/useToast')
    const { toasts, dismissAll } = useToast()
    dismissAll()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await typeInto(inputWrapper, 'continue', 8)
    await inputWrapper.trigger('keydown', { key: 'Enter' })
    await flushPromises()

    // Send must NOT have been invoked — the gate still rejects.
    expect(sendSpy).not.toHaveBeenCalled()

    // But the rejection MUST be surfaced — a toast fires explaining why.
    expect(toasts.value.length).toBeGreaterThanOrEqual(1)
    const lastToast = toasts.value[toasts.value.length - 1]
    expect(lastToast.message).toMatch(/(in.flight|already|wait|reload)/i)

    dismissAll()
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

  it('@agent selection inserts agent token, not a slash command', async () => {
    // Regression guard for the cross-picker contamination bug: when the
    // mention trigger is active, applySelection must only accept items
    // whose label starts with "@". A slash-command item fired by the hidden
    // command picker must not be inserted at the mention trigger's position.
    const store = useChatStore()
    store.availableAgentDetails = makeAgents()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    // Open the mention picker by typing "@".
    await typeInto(wrapper.get('[data-testid="message-input"]'), '@', 1)
    await flushPromises()

    // Click the planner agent item from the mention picker.
    await wrapper.get('[data-testid="fuzzy-search-item-planner"]').trigger('click')
    await flushPromises()

    const textarea = wrapper.get('[data-testid="message-input"]').element as HTMLTextAreaElement
    expect(textarea.value).toContain('@planner')
    expect(textarea.value).not.toContain('/clear')
    wrapper.unmount()
  })

  it('cross-contamination rejected — slash item ignored when mention trigger is active', async () => {
    // When the mention trigger is active and applySelection is called with a
    // slash-command item (e.g. from the hidden command picker firing first),
    // the insertion must be silently rejected — inputText stays unchanged.
    const store = useChatStore()
    store.availableAgentDetails = makeAgents()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    // Open the mention picker by typing "@".
    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await typeInto(inputWrapper, '@', 1)
    await flushPromises()

    // Simulate the hidden slash picker firing @select with /clear before the
    // mention picker fires. Access the component's vm to call applySelection
    // directly with the contaminating item.
    const vm = wrapper.getComponent(MessageInput).vm as unknown as {
      applySelection: (item: { id: string; label: string; meta: string }) => Promise<void>
    }
    await vm.applySelection({ id: 'clear', label: '/clear', meta: 'Clear chat' })
    await flushPromises()

    const textarea = inputWrapper.element as HTMLTextAreaElement
    // The slash item must have been rejected — only the "@" trigger character remains.
    expect(textarea.value).not.toContain('/clear')
    wrapper.unmount()
  })

  it('cross-contamination rejected — mention item ignored when slash trigger is active', async () => {
    // When the slash trigger is active and applySelection is called with a
    // mention item (e.g. "@planner"), the insertion must be silently rejected.
    const store = useChatStore()
    store.availableAgentDetails = makeAgents()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    // Open the slash picker by typing "/".
    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await typeInto(inputWrapper, '/', 1)
    await flushPromises()

    const vm = wrapper.getComponent(MessageInput).vm as unknown as {
      applySelection: (item: { id: string; label: string; meta: string }) => Promise<void>
    }
    await vm.applySelection({ id: 'planner', label: '@planner', meta: 'Planner Agent' })
    await flushPromises()

    const textarea = inputWrapper.element as HTMLTextAreaElement
    // The mention item must have been rejected — only the "/" trigger remains.
    expect(textarea.value).not.toContain('@planner')
    wrapper.unmount()
  })
})
