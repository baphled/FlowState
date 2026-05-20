import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import MessageInput from './MessageInput.vue'
import { useChatStore } from '@/stores/chatStore'
import type { Agent, Swarm } from '@/types'

// Chat Attachments Backend PR1 (May 2026) — the composer's
// uploadPendingAttachments() now POSTs to the real backend via
// @/api uploadAttachments. Mock the import surface so the unit tests
// don't need a live backend. Specs that need to assert upload-failure
// semantics override the resolution per-test.
vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return {
    ...actual,
    uploadAttachments: vi.fn(async (_sid: string, files: File[]) =>
      files.map((f, i) => ({
        id: `att-${i}-${f.name}`,
        mediaType: f.type || 'image/png',
        sizeBytes: f.size,
        originalFilename: f.name,
      })),
    ),
  }
})

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

  // UI Parity bug-fix bundle (May 2026). P1-4: privacy. Pre-fix
  // promptHistory was a flat singleton — a prompt typed in session A
  // leaked into session B's ArrowUp recall (a real privacy concern
  // during screen-shares). The fix scopes history per session id.
  it('prompt history is per-session — switching sessions hides the prior session prompts (P1-4)', () => {
    const store = useChatStore()

    // Session A — record two prompts.
    store.currentSessionId = 'session-a'
    store.recordPromptHistory('A1 secret prompt')
    store.recordPromptHistory('A2 follow-up')
    expect(store.promptHistory).toEqual(['A1 secret prompt', 'A2 follow-up'])

    // Switch to session B — the active prompt history must NOT show
    // session A's entries.
    store.currentSessionId = 'session-b'
    expect(store.promptHistory).toEqual([])

    // Recording into session B keeps A's history intact and isolated.
    store.recordPromptHistory('B1 different prompt')
    expect(store.promptHistory).toEqual(['B1 different prompt'])

    // Switching back to session A restores A's history (per-session
    // memory, not a destructive switch).
    store.currentSessionId = 'session-a'
    expect(store.promptHistory).toEqual(['A1 secret prompt', 'A2 follow-up'])
  })

  it('prompt history per-session cap is still 50 entries (P1-4)', () => {
    const store = useChatStore()
    store.currentSessionId = 'session-x'
    for (let i = 0; i < 60; i++) {
      store.recordPromptHistory(`x${i}`)
    }
    expect(store.promptHistory).toHaveLength(50)
    expect(store.promptHistory[0]).toBe('x10')
    expect(store.promptHistory[49]).toBe('x59')
  })
})

// UI Parity bug-fix bundle (May 2026). P1-5: stop button stuck.
// Clicking Stop should fully clear the per-session streaming state once
// the cancel cascade fires, not wait for the outer POST to drain. The
// fix calls setSessionStreaming(sessionId, {isLoading: false, isStreaming: false})
// right after disconnectSessionStream in handleEscapeKey.
describe('chatStore.handleEscapeKey — clears streaming state after cancel (P1-5)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('clears isStreaming/isLoading on confirmed escape-twice cancel', async () => {
    // Phase-4-Commit-2 of "Turn-Based Post-Then-Poll Architecture
    // (May 2026)" retired the DELETE /api/v1/sessions/{id}/stream
    // cancel endpoint along with the SSE handler. Escape-Escape now
    // clears the per-session UI gate immediately so the composer
    // flips back to Send; the long-poll itself continues to drain
    // on its own without a server-side cancel hop.
    const store = useChatStore()
    store.currentSessionId = 'sess-1'
    store.setSessionStreaming('sess-1', { isLoading: true, isStreaming: true })
    expect(store.streamingFor('sess-1')).toEqual({ isLoading: true, isStreaming: true })

    // First press arms the chord.
    await store.handleEscapeKey()
    // Second press within window fires the cancel cascade — local UI
    // state clears synchronously.
    await store.handleEscapeKey()

    // The streaming slot must have been cleared so the composer's
    // isStreamingNow gate flips back to Send and queued prompts stop
    // accumulating.
    expect(store.streamingFor('sess-1')).toEqual({ isLoading: false, isStreaming: false })
  })

  it('clears streaming state without requiring a backend cancel', async () => {
    // Phase-4-Commit-2 — the DELETE cancel endpoint is gone; the FE
    // never fires a network request on Escape-Escape. Local UI state
    // clears unconditionally so the user can resume composing.
    const store = useChatStore()
    store.currentSessionId = 'sess-2'
    store.setSessionStreaming('sess-2', { isLoading: true, isStreaming: true })

    await store.handleEscapeKey()
    await store.handleEscapeKey()

    expect(store.streamingFor('sess-2')).toEqual({ isLoading: false, isStreaming: false })
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

  // S9.5 — current-session optimistic UI dual-source pin.
  //
  // Child Session Turn Registry Plumbing (May 2026) PR3 — backend-
  // authoritative Live indicator flipped child-session LIST surfaces
  // (ChildSessionsPanel, SessionBrowser, SessionSwitcher) to consume
  // SessionSummary.activeTurnId. MessageInput intentionally STAYS on
  // chatStore.streamingFor — the Send/Stop swap must flip the instant
  // the user clicks Send (optimistic), without waiting for the backend
  // round-trip to populate the Turn registry. See plan §R8.
  //
  // This spec is a regression pin: it proves the Stop button surfaces
  // when streamingFor reports streaming, even when SessionSummary's
  // activeTurnId is empty. A future "fix" that flips MessageInput to
  // activeTurnId would fail this spec.
  it('keeps the Stop button on streamingFor even when SessionSummary.activeTurnId is empty (S9.5 dual-source pin)', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()
    store.currentSessionId = 'session-current'
    store.sessions = [
      {
        id: 'session-current',
        agentId: 'planner',
        status: 'active',
        depth: 0,
        title: 'Current',
        createdAt: '2026-05-20T09:00:00Z',
        updatedAt: '2026-05-20T09:00:00Z',
        messageCount: 0,
        isStreaming: false,
        activeTurnId: '',
      },
    ]
    // streamingFor reports streaming for the current session, but the
    // backend's SessionSummary.activeTurnId is still empty. The composer
    // must flip Send → Stop nonetheless (current-session optimistic UI).
    store.sessionStreaming = {
      'session-current': { isLoading: false, isStreaming: true },
    }
    store.isStreaming = false
    store.isLoading = false

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    expect(wrapper.find('[data-testid="send-button"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="stop-button"]').exists()).toBe(true)
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
    // jsdom + node-canvas may provide a real createObjectURL that returns
    // a blob:nodedata:... URL; we always overwrite both with predictable
    // stubs so the spec assertions are stable across environments.
    ;(URL as unknown as { createObjectURL: (f: File) => string }).createObjectURL = () =>
      'blob:mock-url'
    ;(URL as unknown as { revokeObjectURL: (u: string) => void }).revokeObjectURL = () => {}
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

  // UI Parity bug-fix bundle (May 2026). P0-1: send-with-attachments-but-no-text
  // previously fell through submit() → uploadPendingAttachments() → empty
  // sendMessage('') silently dropped the staged files. The fix surfaces a
  // toast and refuses the submit so attachments stay staged.
  it('refuses submit when attachments are staged but no text (no silent drop)', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()
    const sendSpy = vi.spyOn(store, 'sendMessage').mockResolvedValue()

    const { useToast } = await import('@/composables/useToast')
    const { toasts, dismissAll } = useToast()
    dismissAll()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const file = new File(['x'], 'img.png', { type: 'image/png' })
    const dataTransfer = {
      items: [{ kind: 'file' as const, type: 'image/png', getAsFile: () => file }],
    }
    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await inputWrapper.trigger('paste', { clipboardData: dataTransfer })
    await flushPromises()

    // Empty text — fire the submit path via the component VM. The send
    // button is disabled in this state, but submit() can also be reached
    // via Enter on the textarea with a staged attachment and empty input.
    const vm = wrapper.getComponent(MessageInput).vm as unknown as {
      submit: () => Promise<void>
    }
    await vm.submit()
    await flushPromises()

    // sendMessage MUST NOT have been called — silently dropping the
    // attachment is the failure mode the fix closes.
    expect(sendSpy).not.toHaveBeenCalled()
    // Attachments stay staged so the user can still send them with text.
    expect(wrapper.find('[data-testid="message-input-attachments"]').exists()).toBe(true)
    // Toast surfaces the affordance.
    const toast = toasts.value.find((t) => /attachment/i.test(t.message ?? ''))
    expect(toast).toBeTruthy()
    dismissAll()
    wrapper.unmount()
  })

  // UI Parity bug-fix bundle (May 2026). P0-2: URL.createObjectURL leaks.
  // Pre-fix the submit-then-clear path nulled the pendingAttachments array
  // without revoking the blob URLs; repeated send cycles leaked. The fix
  // revokes every preview URL before clearing the array.
  it('revokes blob preview URLs on send (P0-2)', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()
    const sendSpy = vi.spyOn(store, 'sendMessage').mockResolvedValue()

    // Spy on URL.revokeObjectURL — the production code must call it for
    // every staged attachment that carried a previewUrl.
    const revokeSpy = vi.spyOn(URL, 'revokeObjectURL')

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()
    // Chat Attachments Backend PR1 (May 2026) — uploadPendingAttachments
    // requires an active session id. Seed AFTER mount + flushPromises per
    // memory feedback_pinia_onmounted_clobbers_seed so the chatStore
    // onMounted load doesn't clobber the seed.
    store.currentSessionId = 'sess-attach-p02'

    // Stage an image via paste so previewUrl gets seeded (jsdom stub above
    // makes createObjectURL return 'blob:mock-url').
    const file = new File(['x'], 'img.png', { type: 'image/png' })
    const dataTransfer = {
      items: [{ kind: 'file' as const, type: 'image/png', getAsFile: () => file }],
    }
    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await inputWrapper.trigger('paste', { clipboardData: dataTransfer })
    await flushPromises()

    // Type some text so submit() takes the happy path (with attachments + text).
    await inputWrapper.setValue('here is the image')
    await flushPromises()

    const vm = wrapper.getComponent(MessageInput).vm as unknown as {
      submit: () => Promise<void>
    }
    await vm.submit()
    await flushPromises()

    expect(sendSpy).toHaveBeenCalled()
    // Pre-fix this assertion would be 0.
    expect(revokeSpy).toHaveBeenCalledWith('blob:mock-url')
    wrapper.unmount()
  })

  // Chat Attachments Backend PR4 (May 2026) task-15 — PDF document
  // attachment support. The composer's file-input accept attribute
  // covers PDFs; the staged-attachment chip branches on kind so
  // PDFs render a filename + size + file-icon badge instead of a
  // thumbnail. Active-model awareness in the chip itself is NOT in
  // scope here — server 415 rejection on send is the safety net.
  it('file picker accept attribute covers both image/* and application/pdf (PR4 task-15)', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const fileInput = wrapper.find('[data-testid="file-input"]')
    expect(fileInput.exists()).toBe(true)
    expect(fileInput.attributes('accept')).toBe('image/*,application/pdf')
    wrapper.unmount()
  })

  it('stages a PDF as a document-kind pending attachment with no thumbnail (PR4 task-15)', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const file = new File([new Uint8Array([0x25, 0x50, 0x44, 0x46])], 'doc.pdf', {
      type: 'application/pdf',
    })
    const dataTransfer = {
      items: [{ kind: 'file' as const, type: 'application/pdf', getAsFile: () => file }],
    }
    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await inputWrapper.trigger('paste', { clipboardData: dataTransfer })
    await flushPromises()

    // PDF chip renders.
    expect(wrapper.find('[data-testid="message-input-attachments"]').exists()).toBe(true)
    // File-icon badge present.
    expect(wrapper.find('[data-testid="message-input-attachment-doc-icon"]').exists()).toBe(true)
    // No <img> thumbnail.
    expect(wrapper.find('img.message-input-attachment-thumb').exists()).toBe(false)
    // Filename rendered.
    const chip = wrapper.find('[data-testid^="message-input-attachment-att-"]')
    expect(chip.text()).toContain('doc.pdf')
    wrapper.unmount()
  })

  it('PDF chip renders a human-readable size next to the filename (PR4 task-15)', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    // Approx 2.3 MB of bytes — exact size assertion is the size
    // helper's job; the chip just has to render the formatted string.
    const bytes = new Uint8Array(2_400_000)
    bytes[0] = 0x25 // %
    bytes[1] = 0x50 // P
    bytes[2] = 0x44 // D
    bytes[3] = 0x46 // F
    const file = new File([bytes], 'paper.pdf', { type: 'application/pdf' })
    const dataTransfer = {
      items: [{ kind: 'file' as const, type: 'application/pdf', getAsFile: () => file }],
    }
    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await inputWrapper.trigger('paste', { clipboardData: dataTransfer })
    await flushPromises()

    const sizeNode = wrapper.find('[data-testid="message-input-attachment-size"]')
    expect(sizeNode.exists()).toBe(true)
    // Reports MB shorthand for files ≥ 1 MB.
    expect(sizeNode.text()).toMatch(/MB$/)
    wrapper.unmount()
  })

  it('mixed image + PDF: both chips render with their kind-appropriate visuals (PR4 task-15)', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const png = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], 'cat.png', {
      type: 'image/png',
    })
    const pdf = new File([new Uint8Array([0x25, 0x50, 0x44, 0x46])], 'doc.pdf', {
      type: 'application/pdf',
    })
    const dataTransfer = {
      items: [
        { kind: 'file' as const, type: 'image/png', getAsFile: () => png },
        { kind: 'file' as const, type: 'application/pdf', getAsFile: () => pdf },
      ],
    }
    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await inputWrapper.trigger('paste', { clipboardData: dataTransfer })
    await flushPromises()

    const chips = wrapper.findAll('[data-testid^="message-input-attachment-att-"]')
    expect(chips.length).toBe(2)
    // PNG chip shows a thumbnail.
    expect(wrapper.find('img.message-input-attachment-thumb').exists()).toBe(true)
    // PDF chip shows the file-icon badge.
    expect(wrapper.find('[data-testid="message-input-attachment-doc-icon"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('non-image non-PDF files are silently skipped on stage (PR4 task-15)', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const docx = new File(['fake-docx'], 'note.docx', {
      type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
    })
    const dataTransfer = {
      items: [
        {
          kind: 'file' as const,
          type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
          getAsFile: () => docx,
        },
      ],
    }
    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await inputWrapper.trigger('paste', { clipboardData: dataTransfer })
    await flushPromises()

    // Nothing staged — chip strip remains hidden.
    expect(wrapper.find('[data-testid="message-input-attachments"]').exists()).toBe(false)
    wrapper.unmount()
  })

  // UI Parity bug-fix bundle (May 2026). P0-2: cleanup on component unmount.
  // Attachments staged but not sent (user closes tab / nav away) must also
  // revoke their blob URLs so memory does not leak across sessions.
  it('revokes outstanding blob preview URLs on unmount (P0-2)', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const revokeSpy = vi.spyOn(URL, 'revokeObjectURL')

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const file = new File(['x'], 'img.png', { type: 'image/png' })
    const dataTransfer = {
      items: [{ kind: 'file' as const, type: 'image/png', getAsFile: () => file }],
    }
    const inputWrapper = wrapper.get('[data-testid="message-input"]')
    await inputWrapper.trigger('paste', { clipboardData: dataTransfer })
    await flushPromises()

    revokeSpy.mockClear()
    wrapper.unmount()
    // After unmount the onBeforeUnmount hook should have revoked the staged
    // attachment's preview URL.
    expect(revokeSpy).toHaveBeenCalledWith('blob:mock-url')
  })
})

// UI Parity bug-fix bundle (May 2026). P1-6: drag overlay sticky-state.
// When the user drags out of the browser window and releases the drag
// there, Chrome on Linux/Win misses the final dragleave so the
// dragCounter stays at 1 and the overlay never goes away. Window-level
// dragend / drop listeners reset the counter defensively.
describe('MessageInput — drag overlay defensive reset (P1-6)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('window-level dragend resets the drag overlay state', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    // Simulate a dragenter to set the overlay.
    const wrap = wrapper.get('[data-testid="message-input-wrap"]')
    await wrap.trigger('dragenter', { dataTransfer: { types: ['Files'] } })
    await flushPromises()
    expect(wrapper.find('[data-testid="message-input-drag-overlay"]').exists()).toBe(true)

    // Window-level dragend — the user released the drag outside the window.
    window.dispatchEvent(new Event('dragend'))
    await flushPromises()

    // Pre-fix this would be true (overlay sticks). Fix sets isDragging=false.
    expect(wrapper.find('[data-testid="message-input-drag-overlay"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('window-level drop also resets the drag overlay state', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'loadAgents').mockResolvedValue()

    const wrapper = mount(MessageInput, { attachTo: document.body })
    await flushPromises()

    const wrap = wrapper.get('[data-testid="message-input-wrap"]')
    await wrap.trigger('dragenter', { dataTransfer: { types: ['Files'] } })
    await flushPromises()
    expect(wrapper.find('[data-testid="message-input-drag-overlay"]').exists()).toBe(true)

    // A drop somewhere outside the composer — overlay must still clear.
    window.dispatchEvent(new Event('drop'))
    await flushPromises()

    expect(wrapper.find('[data-testid="message-input-drag-overlay"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
