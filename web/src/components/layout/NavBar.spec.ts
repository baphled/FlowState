import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import NavBar from './NavBar.vue'
import * as api from '@/api'

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

  it('mounts both AgentSwitcher and ModelSwitcher in the navigation bar', async () => {
    const wrapper = mount(NavBar)
    await flushPromises()

    expect(wrapper.find('[data-testid="agent-switcher"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="model-switcher"]').exists()).toBe(true)
  })

  it('places ModelSwitcher in a distinct wrapper from AgentSwitcher', async () => {
    const wrapper = mount(NavBar)
    await flushPromises()

    const agentEl = wrapper.find('[data-testid="agent-switcher"]').element.parentElement
    const modelEl = wrapper.find('[data-testid="model-switcher"]').element.parentElement

    expect(agentEl).not.toBe(modelEl)
  })
})
