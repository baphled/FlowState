import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
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

vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return {
    ...actual,
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

  it('renders AgentSwitcher in the input selector bar', async () => {
    const wrapper = mount(ChatView)
    await flushPromises()

    expect(wrapper.find('[data-testid="input-selector-bar"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="input-selector-bar"] [data-testid="agent-switcher"]').exists()).toBe(true)
  })

  it('renders ModelSwitcher in the input selector bar', async () => {
    const wrapper = mount(ChatView)
    await flushPromises()

    expect(wrapper.find('[data-testid="input-selector-bar"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="input-selector-bar"] [data-testid="model-switcher"]').exists()).toBe(true)
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

  it('shows a loading pulse element when the chat store isLoading flag is true', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    chatStore.isLoading = true
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="loading-pulse"]').exists()).toBe(true)
  })

  it('hides the loading pulse element when the chat store isLoading flag is false', async () => {
    const wrapper = mount(ChatView, {
      global: {
        stubs: {
          MessageInput: { template: '<div data-testid="message-input-stub"></div>' },
        },
      },
    })
    await flushPromises()

    const chatStore = useChatStore()
    chatStore.isLoading = false
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="loading-pulse"]').exists()).toBe(false)
  })
})
