import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import NavBar from './NavBar.vue'
import * as api from '@/api'
import { useChatStore } from '@/stores/chatStore'

vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return {
    ...actual,
    listModels: vi.fn().mockResolvedValue({
      providers: [{ id: 'ollama', models: [{ id: 'llama3.2', name: 'Llama 3.2' }] }],
    }),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessions: vi.fn().mockResolvedValue([]),
  }
})

const mockPush = vi.fn()
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: mockPush }),
  useRoute: () => ({ path: '/chat' }),
}))

describe('NavBar', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(api.listModels).mockResolvedValue({
      providers: [{ id: 'ollama', models: [{ id: 'llama3.2', name: 'Llama 3.2' }] }],
    })
  })

  it('does not render AgentSwitcher in the navigation bar', async () => {
    const wrapper = mount(NavBar)
    await flushPromises()

    expect(wrapper.find('[data-testid="agent-switcher"]').exists()).toBe(false)
  })

  it('does not render ModelSwitcher in the navigation bar', async () => {
    const wrapper = mount(NavBar)
    await flushPromises()

    expect(wrapper.find('[data-testid="model-switcher"]').exists()).toBe(false)
  })
})

describe('NavBar visibility for child sessions', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(api.listModels).mockResolvedValue({
      providers: [{ id: 'ollama', models: [{ id: 'llama3.2', name: 'Llama 3.2' }] }],
    })
  })

  it('renders the nav-bar when the active session has no parentId (parent session)', async () => {
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

    const wrapper = mount(NavBar)
    await flushPromises()

    expect(wrapper.find('[data-testid="nav-bar"]').exists()).toBe(true)
  })

  it('hides the nav-bar entirely in a child session (no chat/swarm/session-selection chrome)', async () => {
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
      {
        id: 'child-a',
        agentId: 'b',
        title: 'child',
        parentId: 'parent-1',
        createdAt: '',
        updatedAt: '',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
    ]
    chatStore.currentSessionId = 'child-a'

    const wrapper = mount(NavBar)
    await flushPromises()

    expect(wrapper.find('[data-testid="nav-bar"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="session-switcher"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="nav-chat"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="nav-swarm"]').exists()).toBe(false)
  })
})
