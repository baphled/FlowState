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

  it('mounts the DelegationStrip in the chat-main region (not in the side panel)', async () => {
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

    const main = wrapper.find('.chat-main')
    expect(main.find('[data-testid="delegation-strip"]').exists()).toBe(true)

    const sidebar = wrapper.find('[data-testid="swarm-pane"]')
    expect(sidebar.find('[data-testid="delegation-strip"]').exists()).toBe(false)
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
    const realToast = vi.spyOn(useToastMod, 'showToast').mockImplementation(() => {})

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
    const realToast = vi.spyOn(useToastMod, 'showToast').mockImplementation(() => {})

    const chatStore = useChatStore()
    vi.spyOn(chatStore, 'restoreStateFromBackend').mockResolvedValueOnce(undefined)

    mount(ChatView)
    await flushPromises()

    expect(realToast).not.toHaveBeenCalled()
    realToast.mockRestore()
  })
})
