import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import MessageInput from './MessageInput.vue'
import { useChatStore } from '@/stores/chatStore'
import type { Agent, Swarm } from '@/types'

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

function makeSwarms(): Swarm[] {
  return [
    { id: 'a-team', description: 'Alpha codegen swarm', lead: 'planner', members: ['coder'] },
    { id: 'planning-loop', description: 'Planning loop', lead: 'planner', members: [] },
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

  it('forwards submit-while-streaming to the store (queueing handled there — Slice E)', async () => {
    // Streaming Coherence Slice E (May 2026) — pre-slice the composer
    // bounced submit-while-streaming with a toast. The new contract:
    // forward to store.sendMessage which routes the prompt onto the
    // session's queue. The QueuedPromptStrip surfaces the queued
    // pills below the thread; no toast fires.
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()
    const sendSpy = vi.spyOn(store, 'sendMessage').mockResolvedValue()

    const { useToast } = await import('@/composables/useToast')
    const { toasts, dismissAll } = useToast()
    dismissAll()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await typeInto(inputWrapper, 'continue', 8)
    await inputWrapper.trigger('keydown', { key: 'Enter' })
    await flushPromises()

    // Send IS invoked — the store handles the queue routing.
    expect(sendSpy).toHaveBeenCalledWith('continue')
    // No "Send blocked" toast — the strip is the user-facing affordance.
    const blockedToast = toasts.value.find((t) => t.title === 'Send blocked')
    expect(blockedToast).toBeUndefined()

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

  // Web Swarm Mention Parity (May 2026) — the @-picker surfaces both
  // agents (group "Agents") and swarms (group "Swarms"). The TUI has
  // had this since the Multi-Agent Chat UX work; this pin confirms the
  // web surface reaches parity. The picker pattern itself is reused
  // verbatim — the only delta is populating the previously-empty swarm
  // slice in mentionItems from the chat store.
  it('shows registered swarms in the @-picker alongside agents', async () => {
    const store = useChatStore()
    store.availableAgentDetails = makeAgents()
    store.swarms = makeSwarms()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    await typeInto(wrapper.get('[data-testid="message-input"]'), '@', 1)

    expect(wrapper.find('[data-testid="fuzzy-search-backdrop"]').exists()).toBe(true)
    // Swarms must appear with their id as the testid suffix, mirroring
    // the agent items the picker already renders.
    expect(wrapper.find('[data-testid="fuzzy-search-item-a-team"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="fuzzy-search-item-planning-loop"]').exists()).toBe(true)
    // Agents are still present — swarms add to the mentionItems list,
    // they don't replace it.
    expect(wrapper.find('[data-testid="fuzzy-search-item-planner"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('inserts a swarm @-mention token when a swarm picker entry is selected', async () => {
    const store = useChatStore()
    store.availableAgentDetails = makeAgents()
    store.swarms = makeSwarms()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    await typeInto(wrapper.get('[data-testid="message-input"]'), '@a-t', 4)

    await wrapper.get('[data-testid="fuzzy-search-item-a-team"]').trigger('click')
    await flushPromises()

    const textarea = wrapper.get('[data-testid="message-input"]').element as HTMLTextAreaElement
    // The token inserted carries the @ prefix so the orchestrator's
    // ScanMentions path resolves it to a swarm dispatch on send.
    expect(textarea.value).toBe('@a-team ')
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

// UI Parity PR2 B4 (May 2026) — Prompt history navigation via ArrowUp /
// ArrowDown. Verifies the composer walks store.promptHistory when the
// caret is at the buffer edge, preserves the live draft on first
// ArrowUp, and snaps back to "live" mode on ArrowDown past the newest
// entry. The chatStore action `recordPromptHistory` is the canonical
// recorder; sendMessage calls it implicitly but we exercise it directly
// from the test so we don't have to drive the full streaming surface.
describe('MessageInput — prompt history (B4)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('ArrowUp on empty buffer recalls the most-recent prompt', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()
    store.recordPromptHistory('first prompt')
    store.recordPromptHistory('second prompt')

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    const textarea = inputWrapper.element as HTMLTextAreaElement
    textarea.selectionStart = 0
    textarea.selectionEnd = 0
    await inputWrapper.trigger('keydown', { key: 'ArrowUp' })
    await flushPromises()

    expect(textarea.value).toBe('second prompt')
    wrapper.unmount()
  })

  it('repeated ArrowUp walks further back through history', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()
    store.recordPromptHistory('oldest')
    store.recordPromptHistory('middle')
    store.recordPromptHistory('newest')

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    const textarea = inputWrapper.element as HTMLTextAreaElement
    textarea.selectionStart = 0
    textarea.selectionEnd = 0

    await inputWrapper.trigger('keydown', { key: 'ArrowUp' })
    await flushPromises()
    expect(textarea.value).toBe('newest')

    textarea.selectionStart = 0
    textarea.selectionEnd = 0
    await inputWrapper.trigger('keydown', { key: 'ArrowUp' })
    await flushPromises()
    expect(textarea.value).toBe('middle')

    textarea.selectionStart = 0
    textarea.selectionEnd = 0
    await inputWrapper.trigger('keydown', { key: 'ArrowUp' })
    await flushPromises()
    expect(textarea.value).toBe('oldest')
    wrapper.unmount()
  })

  it('ArrowDown past the newest history entry restores the live draft', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()
    store.recordPromptHistory('history entry')

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    // Seed the textarea with a live draft the user is mid-typing.
    await typeInto(inputWrapper, 'mid-typed draft', 15)
    const textarea = inputWrapper.element as HTMLTextAreaElement

    // Move caret to start so ArrowUp triggers history.
    textarea.selectionStart = 0
    textarea.selectionEnd = 0
    await inputWrapper.trigger('keydown', { key: 'ArrowUp' })
    await flushPromises()
    expect(textarea.value).toBe('history entry')

    // Caret to end so ArrowDown triggers history walk back forward.
    const len = textarea.value.length
    textarea.selectionStart = len
    textarea.selectionEnd = len
    await inputWrapper.trigger('keydown', { key: 'ArrowDown' })
    await flushPromises()

    expect(textarea.value).toBe('mid-typed draft')
    wrapper.unmount()
  })

  it('ArrowUp in the middle of the buffer does NOT trigger history (native caret motion preserved)', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()
    store.recordPromptHistory('should-not-appear')

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await typeInto(inputWrapper, 'multi\nline content', 8)
    const textarea = inputWrapper.element as HTMLTextAreaElement

    await inputWrapper.trigger('keydown', { key: 'ArrowUp' })
    await flushPromises()

    expect(textarea.value).toBe('multi\nline content')
    expect(textarea.value).not.toBe('should-not-appear')
    wrapper.unmount()
  })

  it('recordPromptHistory dedups against the most-recent entry', () => {
    const store = useChatStore()
    // recordPromptHistory is the canonical recorder; sendMessage calls
    // it at the top. Testing it directly avoids exercising the full
    // streaming surface.
    store.recordPromptHistory('hello world')
    expect(store.promptHistory).toEqual(['hello world'])
    store.recordPromptHistory('hello world')
    expect(store.promptHistory).toEqual(['hello world']) // dedup against the last entry
    store.recordPromptHistory('next prompt')
    expect(store.promptHistory).toEqual(['hello world', 'next prompt'])
  })

  it('promptHistory caps at 50 entries (oldest rolls off)', () => {
    const store = useChatStore()
    for (let i = 0; i < 55; i++) {
      store.recordPromptHistory(`p${i}`)
    }
    expect(store.promptHistory).toHaveLength(50)
    // Oldest 5 rolled off: history starts at p5.
    expect(store.promptHistory[0]).toBe('p5')
    expect(store.promptHistory[49]).toBe('p54')
  })
})

// UI Parity PR2 B5 (May 2026) — Stop-generating button swap. Verifies
// the composer surfaces a red Stop button instead of Send while the
// active session is streaming, and that clicking it fires the cancel
// path twice (the chatStore.handleEscapeKey is an arm-then-confirm
// chord; two synchronous calls dispatch the DELETE).
describe('MessageInput — stop button (B5)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders Send button when the session is idle', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()
    // streamingFor is a getter; when currentSessionId is null it falls
    // back to the legacy flat fields. Leave both false → idle.
    store.currentSessionId = null
    store.isLoading = false
    store.isStreaming = false

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    expect(wrapper.find('[data-testid="send-button"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="stop-button"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('swaps to Stop button when the session is streaming', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()
    store.currentSessionId = null
    store.isLoading = false
    store.isStreaming = true

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    expect(wrapper.find('[data-testid="send-button"]').exists()).toBe(false)
    const stop = wrapper.find('[data-testid="stop-button"]')
    expect(stop.exists()).toBe(true)
    // Tooltip surfaces the Esc-Esc chord so keyboard users learn it.
    expect(stop.attributes('title')).toContain('Esc')
    wrapper.unmount()
  })

  it('also shows Stop when only isLoading is true (pre-stream send)', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()
    store.currentSessionId = null
    store.isLoading = true
    store.isStreaming = false

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    expect(wrapper.find('[data-testid="stop-button"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('clicking Stop calls handleEscapeKey twice (arm + confirm)', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()
    store.currentSessionId = null
    store.isLoading = false
    store.isStreaming = true
    const escapeSpy = vi.spyOn(store, 'handleEscapeKey').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    await wrapper.get('[data-testid="stop-button"]').trigger('click')
    await flushPromises()

    expect(escapeSpy).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })
})

// UI Parity PR2 B3 (May 2026) — Image / file attachment support. The
// backend chat-attachment endpoint does not yet exist on
// feature/vue-ui-rebase, so these specs verify the frontend half: the
// file input, paste handler, and drag overlay stage attachments into
// `pendingAttachments` and the composer renders thumbnails for them.
// Once the backend lands the upload-and-thread-refs path can be tested
// separately; today the staging surface is the contract.
describe('MessageInput — attachments (B3)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    // jsdom does not implement createObjectURL; stub it so the
    // previewUrl branch in stageFiles does not throw.
    if (!('createObjectURL' in URL)) {
      ;(URL as unknown as { createObjectURL: (f: File) => string }).createObjectURL = () =>
        'blob:mock-url'
    }
    if (!('revokeObjectURL' in URL)) {
      ;(URL as unknown as { revokeObjectURL: (u: string) => void }).revokeObjectURL = () => {}
    }
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders an attach button alongside the textarea', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    expect(wrapper.find('[data-testid="attach-button"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="file-input"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('stages a pasted image as a pending attachment', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const file = new File(['fake-png-bytes'], 'pasted.png', { type: 'image/png' })
    const dataTransfer = {
      items: [
        {
          kind: 'file' as const,
          type: 'image/png',
          getAsFile: () => file,
        },
      ],
    }

    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await inputWrapper.trigger('paste', { clipboardData: dataTransfer })
    await flushPromises()

    expect(wrapper.find('[data-testid="message-input-attachments"]').exists()).toBe(true)
    // One staged attachment renders one tile.
    const tiles = wrapper.findAll('[data-testid^="message-input-attachment-att-"]')
    expect(tiles.length).toBe(1)
    wrapper.unmount()
  })

  it('shows the drag overlay when files are dragged over the composer', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const wrap = wrapper.get('[data-testid="message-input-wrap"]')
    // Fake DataTransfer with the "Files" type token.
    await wrap.trigger('dragenter', {
      dataTransfer: { types: ['Files'] },
    })
    await flushPromises()

    expect(wrapper.find('[data-testid="message-input-drag-overlay"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('non-image pastes are ignored (no attachment staged)', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const textFile = new File(['hello'], 'note.txt', { type: 'text/plain' })
    const dataTransfer = {
      items: [
        {
          kind: 'file' as const,
          type: 'text/plain',
          getAsFile: () => textFile,
        },
      ],
    }

    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await inputWrapper.trigger('paste', { clipboardData: dataTransfer })
    await flushPromises()

    expect(wrapper.find('[data-testid="message-input-attachments"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('removing a staged attachment empties the attachments strip', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const file = new File(['x'], 'img.png', { type: 'image/png' })
    const dataTransfer = {
      items: [{ kind: 'file' as const, type: 'image/png', getAsFile: () => file }],
    }
    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await inputWrapper.trigger('paste', { clipboardData: dataTransfer })
    await flushPromises()

    const removeBtn = wrapper.find('[data-testid^="message-input-attachment-remove-att-"]')
    expect(removeBtn.exists()).toBe(true)
    await removeBtn.trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="message-input-attachments"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
