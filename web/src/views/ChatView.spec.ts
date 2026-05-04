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
    const messagePaneIdx = Array.from(children).findIndex(
      (el) => el instanceof HTMLElement && el.classList.contains('message-pane'),
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

  it('scrolls the message pane when the last streaming message content grows', async () => {
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

    chatStore.messages[0].content = 'hello world'
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

  it('resumes auto-scroll when the user returns within 100px of the bottom', async () => {
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
    scrollMetrics.scrollTop = 450
    await wrapper.find('[data-testid="chat-message-pane"]').trigger('scroll')

    const chatStore = useChatStore()
    chatStore.messages = [
      { id: 'assistant-1', role: 'assistant', content: 'hello', timestamp: new Date().toISOString() },
    ]

    await nextTick()
    await nextTick()

    expect(scrollToSpy).toHaveBeenCalledWith({ top: 1000, behavior: 'smooth' })
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
