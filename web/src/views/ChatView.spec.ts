import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { nextTick } from 'vue'
import ChatView from './ChatView.vue'
import { useChatStore } from '@/stores/chatStore'

const localStorageMock = (() => {
  let store: Record<string, string> = {}
  return {
    getItem: vi.fn((key: string) => store[key] ?? null),
    setItem: vi.fn((key: string, value: string) => { store[key] = value }),
    removeItem: vi.fn((key: string) => { delete store[key] }),
    clear: vi.fn(() => { store = {} }),
  }
})()

Object.defineProperty(window, 'localStorage', { value: localStorageMock })
Object.defineProperty(HTMLElement.prototype, 'scrollTo', {
  value: vi.fn(),
  configurable: true,
  writable: true,
})

vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return {
    ...actual,
    fetchModels: vi.fn().mockResolvedValue([{ id: 'llama3.2', name: 'Llama 3.2', providerId: 'ollama' }]),
    listModels: vi.fn().mockResolvedValue({
      providers: [{ id: 'ollama', models: [{ id: 'llama3.2', name: 'Llama 3.2' }] }],
    }),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessions: vi.fn().mockResolvedValue([]),
    restoreSession: vi.fn().mockResolvedValue({ messages: [] }),
  }
})

vi.mock('@/stores/swarmStore', () => {
  const connect = vi.fn()
  const disconnect = vi.fn()
  return {
    useSwarmStore: () => ({
      connect,
      disconnect,
      events: [],
      delegationEvents: [],
      harnessEvents: [],
      planEvents: [],
      statusEvents: [],
      reviewEvents: [],
    }),
  }
})

describe('ChatView selector bar', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('renders AgentPicker in the input selector bar', async () => {
    const wrapper = mount(ChatView)
    await flushPromises()

    expect(wrapper.find('[data-testid="input-selector-bar"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="input-selector-bar"] [data-testid="agent-picker"]').exists()).toBe(true)
  })

  it('renders ModelPicker in the input selector bar', async () => {
    const wrapper = mount(ChatView)
    await flushPromises()

    expect(wrapper.find('[data-testid="input-selector-bar"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="input-selector-bar"] [data-testid="model-picker"]').exists()).toBe(true)
  })

  it('places the selector bar between the message pane and MessageInput', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const main = wrapper.find('.chat-main')
    const children = main.element.children
    // Pin updated for QW-9: the message pane is now wrapped in
    // `.message-pane-wrap` so a floating scroll-to-bottom button can be
    // absolutely positioned inside the same stacking context. The wrap is
    // the structural container that takes the message pane's previous
    // place in the .chat-main flow; the selector bar must still sit after
    // it and before MessageInput.
    const messagePaneIdx = Array.from(children).findIndex(
      (el) => el instanceof HTMLElement && el.classList.contains('message-pane-wrap'),
    )
    const selectorBarIdx = Array.from(children).findIndex(
      (el) => el instanceof HTMLElement && (el as HTMLElement).dataset.testid === 'input-selector-bar',
    )
    const messageInputIdx = Array.from(children).findIndex(
      (el) => el instanceof HTMLElement && (el as HTMLElement).dataset.testid === 'message-input-stub',
    )

    expect(messagePaneIdx).toBeGreaterThanOrEqual(0)
    expect(selectorBarIdx).toBeGreaterThan(messagePaneIdx)
    expect(messageInputIdx).toBeGreaterThan(selectorBarIdx)
  })
})

describe('ChatView loading pulse', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('shows a loading pulse element when the chat store isLoading flag is true and isStreaming is false', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    chatStore.isLoading = true
    chatStore.isStreaming = false
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="loading-pulse"]').exists()).toBe(true)
  })

  it('hides the loading pulse element when the chat store isLoading flag is false', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    chatStore.isLoading = false
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="loading-pulse"]').exists()).toBe(false)
  })

  it('hides the loading pulse element while streaming', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    chatStore.isLoading = true
    chatStore.isStreaming = true
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="loading-pulse"]').exists()).toBe(false)
  })
})

describe('ChatView auto-scroll', () => {
  const scrollToSpy = vi.fn()
  const scrollMetrics = {
    scrollHeight: 1000,
    scrollTop: 0,
    clientHeight: 500,
  }

  beforeEach(() => {
    setActivePinia(createPinia())
    scrollToSpy.mockReset()
    Object.defineProperty(HTMLElement.prototype, 'scrollTo', {
      value: scrollToSpy,
      configurable: true,
      writable: true,
    })
    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
      configurable: true,
      get() {
        return scrollMetrics.scrollHeight
      },
    })
    Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
      configurable: true,
      get() {
        return scrollMetrics.scrollTop
      },
      set(value: number) {
        scrollMetrics.scrollTop = value
      },
    })
    Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
      configurable: true,
      get() {
        return scrollMetrics.clientHeight
      },
    })
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  it('scrolls the message pane when messages load initially', async () => {
    mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
        },
      },
    })
    await flushPromises()

    expect(scrollToSpy).toHaveBeenCalledWith({ top: 1000, behavior: 'smooth' })
  })

  it('scrolls the message pane when a streaming message is added', async () => {
    mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    scrollToSpy.mockClear()
    chatStore.isStreaming = true
    chatStore.messages = [
      { id: 'assistant-1', role: 'assistant', content: 'hello', timestamp: new Date().toISOString() },
    ]

    await nextTick()
    await nextTick()

    expect(scrollToSpy).toHaveBeenCalledWith({ top: 1000, behavior: 'smooth' })
  })

  it('scrolls the message pane with instant behavior when the last streaming message content grows', async () => {
    vi.useFakeTimers()
    mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    scrollToSpy.mockClear()
    chatStore.messages = [
      { id: 'assistant-1', role: 'assistant', content: 'hel', timestamp: new Date().toISOString() },
    ]
    chatStore.isStreaming = true
    await nextTick()
    scrollToSpy.mockClear()

    chatStore.messages[0].content = 'hello world'
    await nextTick()
    // Flush the requestAnimationFrame scheduled by scheduleInstantScroll
    vi.runAllTimers()
    await nextTick()

    expect(scrollToSpy).toHaveBeenCalledWith({ top: 1000, behavior: 'instant' })
    vi.useRealTimers()
  })

  it('scrolls the message pane with smooth behavior when a new message is added', async () => {
    mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    scrollToSpy.mockClear()
    chatStore.messages = [
      { id: 'user-1', role: 'user', content: 'hello', timestamp: new Date().toISOString() },
    ]

    await nextTick()
    await nextTick()

    expect(scrollToSpy).toHaveBeenCalledWith({ top: 1000, behavior: 'smooth' })
  })

  it('resets userScrolledUp to false when isLoading becomes true (submit re-engages auto-scroll)', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    // Simulate user scrolling up — sets userScrolledUp to true
    scrollMetrics.scrollTop = 0
    await wrapper.find('[data-testid="chat-message-pane"]').trigger('scroll')
    scrollToSpy.mockClear()

    // Simulate submit starting — isLoading goes true
    const chatStore = useChatStore()
    chatStore.isLoading = true
    await nextTick()

    // Now a new message arrives; userScrolledUp should be false so scroll fires
    chatStore.messages = [
      { id: 'user-1', role: 'user', content: 'new message', timestamp: new Date().toISOString() },
    ]
    await nextTick()
    await nextTick()

    expect(scrollToSpy).toHaveBeenCalledWith({ top: 1000, behavior: 'smooth' })
  })

  it('does not auto-scroll when the user has scrolled up', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    scrollToSpy.mockClear()
    scrollMetrics.scrollTop = 0
    await wrapper.find('[data-testid="chat-message-pane"]').trigger('scroll')

    const chatStore = useChatStore()
    chatStore.messages = [
      { id: 'assistant-1', role: 'assistant', content: 'hello', timestamp: new Date().toISOString() },
    ]

    await nextTick()
    await nextTick()

    expect(scrollToSpy).not.toHaveBeenCalled()
  })

  // UX consolidation (May 2026) — the at-bottom threshold tightened from
  // 100px to 24px so the scroll-to-bottom button becomes discoverable after
  // scrolling 1-2 messages worth of pixels rather than ~5. With
  // scrollHeight=1000, clientHeight=500, scrollTop=480 leaves 20px of
  // distance — within the 24px threshold so userScrolledUp stays false and
  // auto-scroll continues to fire on new messages.
  it('resumes auto-scroll when the user returns within 24px of the bottom', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    scrollToSpy.mockClear()
    scrollMetrics.scrollTop = 480
    await wrapper.find('[data-testid="chat-message-pane"]').trigger('scroll')

    const chatStore = useChatStore()
    chatStore.messages = [
      { id: 'assistant-1', role: 'assistant', content: 'hello', timestamp: new Date().toISOString() },
    ]

    await nextTick()
    await nextTick()

    expect(scrollToSpy).toHaveBeenCalledWith({ top: 1000, behavior: 'smooth' })
  })

  // Symmetry pin for the new threshold: when the user is 50px from the
  // bottom (well outside the 24px tolerance), userScrolledUp must latch
  // and auto-scroll must not fire. Pre-consolidation a 50px gap was
  // considered "at bottom" under the 100px threshold, so this test guards
  // against regression to the old, looser tolerance.
  it('treats a 50px gap from the bottom as "scrolled up" under the 24px threshold', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    scrollToSpy.mockClear()
    scrollMetrics.scrollTop = 450 // distance = 1000 - 450 - 500 = 50, > 24
    await wrapper.find('[data-testid="chat-message-pane"]').trigger('scroll')

    const chatStore = useChatStore()
    chatStore.messages = [
      { id: 'assistant-1', role: 'assistant', content: 'hello', timestamp: new Date().toISOString() },
    ]

    await nextTick()
    await nextTick()

    expect(scrollToSpy).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="scroll-to-bottom-btn"]').exists()).toBe(true)
  })
})

// QW-9 — Scroll-to-bottom button. The chat pane's `userScrolledUp` flag (see
// ChatView.vue) suppresses auto-scroll while the user is reading earlier
// messages. Until QW-9 the only way back to the latest message was to scroll
// the pane manually. This block pins the new floating affordance: hidden when
// at the bottom, visible when scrolled up, click clears `userScrolledUp` and
// scrolls smoothly to the latest message, and re-arms auto-scroll for
// subsequent streaming chunks.
describe('ChatView scroll-to-bottom button (QW-9)', () => {
  const scrollToSpy = vi.fn()
  const scrollMetrics = {
    scrollHeight: 1000,
    scrollTop: 0,
    clientHeight: 500,
  }

  beforeEach(() => {
    setActivePinia(createPinia())
    scrollToSpy.mockReset()
    Object.defineProperty(HTMLElement.prototype, 'scrollTo', {
      value: scrollToSpy,
      configurable: true,
      writable: true,
    })
    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
      configurable: true,
      get() {
        return scrollMetrics.scrollHeight
      },
    })
    Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
      configurable: true,
      get() {
        return scrollMetrics.scrollTop
      },
      set(value: number) {
        scrollMetrics.scrollTop = value
      },
    })
    Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
      configurable: true,
      get() {
        return scrollMetrics.clientHeight
      },
    })
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  it('hides the scroll-to-bottom button when the message pane is at the bottom', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    expect(wrapper.find('[data-testid="scroll-to-bottom-btn"]').exists()).toBe(false)
  })

  it('shows the scroll-to-bottom button when the user scrolls up', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    scrollMetrics.scrollTop = 0
    await wrapper.find('[data-testid="chat-message-pane"]').trigger('scroll')

    expect(wrapper.find('[data-testid="scroll-to-bottom-btn"]').exists()).toBe(true)
  })

  it('scrolls the message pane to the bottom and re-arms auto-scroll when clicked', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    // Scroll up to reveal the button.
    scrollMetrics.scrollTop = 0
    await wrapper.find('[data-testid="chat-message-pane"]').trigger('scroll')
    scrollToSpy.mockClear()

    const button = wrapper.find('[data-testid="scroll-to-bottom-btn"]')
    expect(button.exists()).toBe(true)
    await button.trigger('click')
    await nextTick()

    // Click scrolls to the bottom smoothly.
    expect(scrollToSpy).toHaveBeenCalledWith({ top: 1000, behavior: 'smooth' })

    // Auto-scroll is re-armed: a subsequent new message fires scroll again
    // even though the pane's reported scrollTop hasn't moved (the smooth
    // animation is asynchronous and JSDOM doesn't run it).
    scrollToSpy.mockClear()
    const chatStore = useChatStore()
    chatStore.messages = [
      { id: 'assistant-1', role: 'assistant', content: 'hello', timestamp: new Date().toISOString() },
    ]
    await nextTick()
    await nextTick()

    expect(scrollToSpy).toHaveBeenCalledWith({ top: 1000, behavior: 'smooth' })
    expect(wrapper.find('[data-testid="scroll-to-bottom-btn"]').exists()).toBe(false)
  })

  it('keeps the button visible and does not auto-scroll when new content arrives while the user is scrolled up', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    scrollMetrics.scrollTop = 0
    await wrapper.find('[data-testid="chat-message-pane"]').trigger('scroll')
    scrollToSpy.mockClear()

    const chatStore = useChatStore()
    chatStore.messages = [
      { id: 'assistant-1', role: 'assistant', content: 'hello', timestamp: new Date().toISOString() },
    ]
    await nextTick()
    await nextTick()

    expect(scrollToSpy).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="scroll-to-bottom-btn"]').exists()).toBe(true)
  })

  it('exposes an aria-label on the scroll-to-bottom button', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    scrollMetrics.scrollTop = 0
    await wrapper.find('[data-testid="chat-message-pane"]').trigger('scroll')

    const button = wrapper.find('[data-testid="scroll-to-bottom-btn"]')
    expect(button.exists()).toBe(true)
    expect(button.attributes('aria-label')).toBe('Scroll to latest message')
  })
})

describe('ChatView side panel reorganisation', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('mounts the TodoListPanel inside the swarm pane', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const sidebar = wrapper.find('[data-testid="swarm-pane"]')
    expect(sidebar.exists()).toBe(true)
    expect(sidebar.find('[data-testid="todo-list-panel"]').exists()).toBe(true)
  })

  it('does not render the legacy delegation panel inside the swarm pane', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const sidebar = wrapper.find('[data-testid="swarm-pane"]')
    expect(sidebar.exists()).toBe(true)
    expect(sidebar.find('[data-testid="delegation-panel"]').exists()).toBe(false)
  })

  it('does not render the legacy tool-call panel or plan panel inside the swarm pane', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const sidebar = wrapper.find('[data-testid="swarm-pane"]')
    expect(sidebar.exists()).toBe(true)
    // Tool-calls and plan content are no longer surfaced in the side panel —
    // the side panel is reserved for todos. Their data flow continues through
    // the chat thread (tool messages) and DelegationStrip (delegation events).
    expect(sidebar.find('[data-testid="tool-call-panel"]').exists()).toBe(false)
    expect(sidebar.find('[data-testid="plan-panel"]').exists()).toBe(false)
  })

  // UX consolidation (May 2026) — DelegationStrip removed entirely. The
  // transient swarm-bus pulse strip never shipped as a useful affordance:
  // the persistent ChildSessionsPanel already surfaces every delegated
  // child, and DelegationPanel still shows raw swarm events in the swarm
  // pane. This pin guards the removal so a future refactor can't quietly
  // re-mount the legacy strip.
  it('does not mount the legacy DelegationStrip anywhere in the chat region', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    expect(wrapper.find('[data-testid="delegation-strip"]').exists()).toBe(false)
  })
})

describe('ChatView toolbar in parent sessions', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('renders the input-selector-bar when the active session has no parentId (parent session)', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    chatStore.sessions = [
      {
        id: 'parent-1',
        agentId: 'a',
        title: 'parent',
        createdAt: '',
        updatedAt: '',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
    ]
    chatStore.currentSessionId = 'parent-1'
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="input-selector-bar"]').exists()).toBe(true)
  })

  it('renders the AgentPicker as interactive (not readonly) in a parent session', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    chatStore.sessions = [
      {
        id: 'parent-1',
        agentId: 'a',
        title: 'parent',
        createdAt: '',
        updatedAt: '',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
    ]
    chatStore.currentSessionId = 'parent-1'
    await wrapper.vm.$nextTick()

    const agentPicker = wrapper.find('[data-testid="agent-picker"]')
    expect(agentPicker.exists()).toBe(true)
    expect(agentPicker.classes()).not.toContain('is-readonly')

    const modelPicker = wrapper.find('[data-testid="model-picker"]')
    expect(modelPicker.exists()).toBe(true)
    expect(modelPicker.classes()).not.toContain('is-readonly')
  })
})

describe('ChatView read-only toolbar in child sessions', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  function mountWithChildSession() {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    return wrapper
  }

  async function activateChildSession() {
    const chatStore = useChatStore()
    chatStore.sessions = [
      {
        id: 'parent-1',
        agentId: 'planner',
        title: 'parent',
        createdAt: '',
        updatedAt: '',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
      {
        id: 'child-a',
        agentId: 'executor',
        title: 'child',
        parentId: 'parent-1',
        createdAt: '',
        updatedAt: '',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
        currentModelId: 'claude-sonnet-4-6',
        currentProviderId: 'anthropic',
      },
    ]
    chatStore.currentSessionId = 'child-a'
    chatStore.currentModelId = 'claude-sonnet-4-6'
    chatStore.currentProviderId = 'anthropic'
  }

  it('still renders the input-selector-bar in a child session (not hidden)', async () => {
    const wrapper = mountWithChildSession()
    await flushPromises()
    await activateChildSession()
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="input-selector-bar"]').exists()).toBe(true)
  })

  it('renders the AgentPicker in readonly mode in a child session', async () => {
    const wrapper = mountWithChildSession()
    await flushPromises()
    await activateChildSession()
    await wrapper.vm.$nextTick()

    const agentPicker = wrapper.find('[data-testid="agent-picker"]')
    expect(agentPicker.exists()).toBe(true)
    expect(agentPicker.classes()).toContain('is-readonly')
  })

  it('renders the ModelPicker in readonly mode in a child session', async () => {
    const wrapper = mountWithChildSession()
    await flushPromises()
    await activateChildSession()
    await wrapper.vm.$nextTick()

    const modelPicker = wrapper.find('[data-testid="model-picker"]')
    expect(modelPicker.exists()).toBe(true)
    expect(modelPicker.classes()).toContain('is-readonly')
  })

  it('renders the provider label populated from the active session', async () => {
    const wrapper = mountWithChildSession()
    await flushPromises()
    await activateChildSession()
    await wrapper.vm.$nextTick()

    const providerLabel = wrapper.find('[data-testid="toolbar-provider-label"]')
    expect(providerLabel.exists()).toBe(true)
    expect(providerLabel.text()).toContain('anthropic')
  })

  it('renders the model label populated from the active session', async () => {
    const wrapper = mountWithChildSession()
    await flushPromises()
    await activateChildSession()
    await wrapper.vm.$nextTick()

    const modelPicker = wrapper.find('[data-testid="model-picker"]')
    expect(modelPicker.text()).toContain('claude-sonnet-4-6')
  })

  // QW-11 — Delegated sessions are read-only. MessageInput is hidden and a
  // slim banner takes its place so the user knows the session was spawned
  // by another agent and is replaying its work, not awaiting a prompt.
  it('hides MessageInput when the active session has a parentId (delegated child)', async () => {
    const wrapper = mountWithChildSession()
    await flushPromises()
    await activateChildSession()
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="message-input-stub"]').exists()).toBe(false)
  })

  it('renders the read-only banner with a parent backlink when the active session is delegated', async () => {
    const wrapper = mountWithChildSession()
    await flushPromises()
    await activateChildSession()
    await wrapper.vm.$nextTick()

    const banner = wrapper.find('[data-testid="child-session-readonly-banner"]')
    expect(banner.exists()).toBe(true)
    // Banner copy references the parent title so the user knows where they
    // came from, and exposes a backlink button to return.
    expect(banner.text()).toContain('parent')
    expect(banner.text().toLowerCase()).toContain('read-only')
    expect(wrapper.find('[data-testid="child-session-readonly-parent-link"]').exists()).toBe(true)
  })
})

// QW-11 — Parent sessions remain composable: MessageInput is mounted and
// the read-only banner is absent.
describe('ChatView composer in parent sessions', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('mounts MessageInput when the active session has no parentId', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    chatStore.sessions = [
      {
        id: 'parent-1',
        agentId: 'a',
        title: 'parent',
        createdAt: '',
        updatedAt: '',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
    ]
    chatStore.currentSessionId = 'parent-1'
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="message-input-stub"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="child-session-readonly-banner"]').exists()).toBe(false)
  })
})

describe('ChatView agent-activity indicator', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('shows agent-activity-indicator when isStreaming is true', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    chatStore.isStreaming = true
    chatStore.agentId = 'planner'
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="agent-activity-indicator"]').exists()).toBe(true)
  })

  it('hides agent-activity-indicator when isStreaming is false', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    chatStore.isStreaming = false
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="agent-activity-indicator"]').exists()).toBe(false)
  })

  it('shows the agent name in agent-activity-indicator when isStreaming is true', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    chatStore.isStreaming = true
    chatStore.agentId = 'planner'
    await wrapper.vm.$nextTick()

    const indicator = wrapper.find('[data-testid="agent-activity-indicator"]')
    expect(indicator.exists()).toBe(true)
    expect(indicator.text()).toContain('planner')
  })

  it('shows the active model and provider in the agent-activity-indicator during streaming', async () => {
    // Track B — model+provider visibility. The user explicitly asked
    // ("we need to see which model the request is using") to be able
    // to tell at a glance what model is producing the streaming
    // answer. After a failover the chatStore updates
    // currentProviderId/currentModelId and this label refreshes
    // immediately. Format is "agent is working… on <model> · <provider>"
    // — the model goes first because it's the user's mental anchor
    // (they pick a model), provider is the secondary qualifier.
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    chatStore.isStreaming = true
    chatStore.agentId = 'team-lead'
    chatStore.currentModelId = 'glm-4.6'
    chatStore.currentProviderId = 'zai'
    await wrapper.vm.$nextTick()

    const modelChip = wrapper.find('[data-testid="agent-activity-model"]')
    expect(modelChip.exists()).toBe(true)
    expect(modelChip.text()).toContain('glm-4.6')
    expect(modelChip.text()).toContain('zai')
  })

  it('omits the model chip when neither currentModelId nor currentProviderId is set', async () => {
    // Defensive: a session that never had a model selected (degraded
    // state) must not render a stray "on " label. The chip must hide
    // entirely until at least one of model/provider is present.
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    chatStore.isStreaming = true
    chatStore.agentId = 'team-lead'
    chatStore.currentModelId = ''
    chatStore.currentProviderId = ''
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="agent-activity-model"]').exists()).toBe(false)
  })
})

describe('ChatView message grouping', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('renders ContextToolGroup when messages contain context tools', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    // 'read' is a context tool by default
    chatStore.messages = [
      { id: '1', role: 'tool_result', toolName: 'read', content: '...', timestamp: new Date().toISOString() },
      { id: '2', role: 'tool_result', toolName: 'read', content: '...', timestamp: new Date().toISOString() },
    ]
    
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    const contextGroup = wrapper.find('[data-testid="context-tool-group-stub"]')
    expect(contextGroup.exists()).toBe(true)
  })

  it('renders individual MessageBubbles for non-context messages', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
          ContextToolGroup: { template: '<div data-testid="context-tool-group-stub"></div>' },
          MessageBubble: { template: '<div data-testid="message-bubble-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    chatStore.messages = [
      { id: '1', role: 'user', content: 'hello', timestamp: new Date().toISOString() },
    ]
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="message-bubble-stub"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="context-tool-group-stub"]').exists()).toBe(false)
  })
})

describe('ChatView Escape keydown listener lifecycle (H9)', () => {
  // H9 — Bug Hunt Findings (May 2026). Pre-fix the Slice G escape-twice
  // handler was attached as an inline anonymous arrow inside onMounted
  // with no matching removeEventListener in onBeforeUnmount. After N
  // route round-trips (Chat → other → Chat → other → Chat) N copies of
  // the handler were live, so a single Escape press fanned out into N
  // calls into chatStore.handleEscapeKey — and after a double-tap, N
  // concurrent DELETE /v1/sessions/{id}/stream requests against the
  // API. The fix lifts the handler into a named const at setup scope
  // and tears it down in onBeforeUnmount with the same identity. These
  // specs pin the lifecycle directly so the fix can't silently regress.

  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('removes its keydown listener on unmount', async () => {
    const addSpy = vi.spyOn(document, 'addEventListener')
    const removeSpy = vi.spyOn(document, 'removeEventListener')

    const wrapper = mount(ChatView)
    await flushPromises()

    // Find the keydown listener registration that ChatView's onMounted
    // installed (there's one in installSessionHierarchyNav too — accept
    // both, we just need ChatView's to be torn down).
    const addCalls = addSpy.mock.calls.filter(([type]) => type === 'keydown')
    expect(addCalls.length).toBeGreaterThanOrEqual(1)

    wrapper.unmount()
    await flushPromises()

    const removeCalls = removeSpy.mock.calls.filter(([type]) => type === 'keydown')
    // Every keydown listener that was added during mount must be removed
    // on unmount — same count, and ideally same handler references. The
    // identity check is what catches the leak: an anonymous arrow added
    // in onMounted but not captured at setup scope cannot be removed.
    expect(removeCalls.length).toBeGreaterThanOrEqual(addCalls.length)

    const addedHandlers = addCalls.map(([, fn]) => fn)
    const removedHandlers = removeCalls.map(([, fn]) => fn)
    for (const handler of addedHandlers) {
      expect(removedHandlers).toContain(handler)
    }

    addSpy.mockRestore()
    removeSpy.mockRestore()
  })

  it('does not leak the Escape handler across re-mounts (single Escape, single dispatch)', async () => {
    // Simulate the Chat → other route → Chat round-trip three times,
    // then send a single Escape. Pre-fix the store would observe three
    // keydown events for one user keypress; post-fix it observes one.
    const wrapper1 = mount(ChatView)
    await flushPromises()
    wrapper1.unmount()
    await flushPromises()

    const wrapper2 = mount(ChatView)
    await flushPromises()
    wrapper2.unmount()
    await flushPromises()

    const wrapper3 = mount(ChatView)
    await flushPromises()

    const chatStore = useChatStore()
    const handleEscapeSpy = vi.spyOn(chatStore, 'handleEscapeKey').mockResolvedValue()

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()

    // Only the still-mounted ChatView's listener should fire. Pre-fix
    // there were three live handlers (one per mount), so three calls.
    expect(handleEscapeSpy).toHaveBeenCalledTimes(1)

    handleEscapeSpy.mockRestore()
    wrapper3.unmount()
  })

  it('stops dispatching to handleEscapeKey after unmount', async () => {
    const wrapper = mount(ChatView)
    await flushPromises()

    const chatStore = useChatStore()
    const handleEscapeSpy = vi.spyOn(chatStore, 'handleEscapeKey').mockResolvedValue()

    wrapper.unmount()
    await flushPromises()

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()

    // No live ChatView → no listener → no store call. Pre-fix the
    // anonymous handler outlived the component and continued firing.
    expect(handleEscapeSpy).not.toHaveBeenCalled()

    handleEscapeSpy.mockRestore()
  })
})

describe('ChatView mount-time restore failure (Principal F7)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  // The failure path: a network blip during initial hydration must surface
  // a toast and assign chatStore.error so the user sees something
  // actionable. Pre-fix the onMounted handler called restoreStateFromBackend
  // without a try/catch; a rejection would leave the user staring at a
  // blank chat with no signal of what went wrong.

  it('shows an error toast and assigns chatStore.error when restoreStateFromBackend rejects', async () => {
    const { showToast } = await import('@/composables/useToast')
    const toastSpy = vi.spyOn({ showToast }, 'showToast')
    const useToastMod = await import('@/composables/useToast')
    const realToast = vi.spyOn(useToastMod, 'showToast').mockImplementation(() => 0)

    const chatStore = useChatStore()
    vi.spyOn(chatStore, 'restoreStateFromBackend').mockRejectedValueOnce(
      new Error('boom: network down'),
    )

    mount(ChatView)
    await flushPromises()

    expect(chatStore.error).toBe('boom: network down')
    expect(realToast).toHaveBeenCalled()
    const toastArgs = realToast.mock.calls[0][0]
    if (typeof toastArgs === 'object') {
      expect(toastArgs.variant).toBe('error')
      expect(toastArgs.message).toBe('boom: network down')
    }

    realToast.mockRestore()
    toastSpy.mockRestore()
  })

  it('does NOT toast on a successful restore', async () => {
    const useToastMod = await import('@/composables/useToast')
    const realToast = vi.spyOn(useToastMod, 'showToast').mockImplementation(() => 0)

    const chatStore = useChatStore()
    vi.spyOn(chatStore, 'restoreStateFromBackend').mockResolvedValueOnce(undefined)

    mount(ChatView)
    await flushPromises()

    expect(realToast).not.toHaveBeenCalled()
    realToast.mockRestore()
  })
})
