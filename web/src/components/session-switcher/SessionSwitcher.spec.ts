import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { nextTick } from 'vue'
import SessionSwitcher from './SessionSwitcher.vue'
import { useChatStore } from '@/stores/chatStore'
import type { SessionSummary } from '@/types'

vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return {
    ...actual,
    fetchSessions: vi.fn().mockResolvedValue([]),
    fetchAgents: vi.fn().mockResolvedValue([]),
    listModels: vi.fn().mockResolvedValue({ providers: [] }),
  }
})

function makeSession(overrides: Partial<SessionSummary> = {}): SessionSummary {
  return {
    id: 'session-x',
    agentId: 'planner',
    status: 'active',
    depth: 0,
    title: 'Untitled',
    createdAt: '2026-05-10T09:00:00Z',
    updatedAt: '2026-05-10T09:00:00Z',
    messageCount: 0,
    isStreaming: false,
    ...overrides,
  }
}

// UX consolidation (May 2026) — visibility of "any session is streaming" must
// be discoverable from the session list itself, not only from the active
// chat thread. The trigger button surfaces a compact background-activity
// hint when ANY non-current session is streaming; each row in the dropdown
// surfaces its own per-session live indicator.
//
// Test ordering note: SessionSwitcher.onMounted fires `loadSessions()` which
// resolves the mocked `fetchSessions` returning `[]` and clobbers any
// pre-mount `chatStore.sessions` assignment. We therefore seed the store
// AFTER `flushPromises()` has run the mocked load to completion.
describe('SessionSwitcher activity indicators', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows a per-row live indicator on streaming sessions when the dropdown is open', async () => {
    const chatStore = useChatStore()

    const wrapper = mount(SessionSwitcher)
    await flushPromises()

    // Open the dropdown FIRST — toggleDropdown internally calls
    // loadSessions() which would otherwise overwrite the seed below with
    // the mocked-empty result.
    await wrapper.find('[aria-haspopup="listbox"]').trigger('click')
    await flushPromises()

    chatStore.sessions = [
      makeSession({ id: 'parent-A', title: 'Alpha' }),
      makeSession({ id: 'parent-B', title: 'Beta' }),
    ]
    chatStore.currentSessionId = 'parent-A'
    chatStore.sessionStreaming = {
      'parent-B': { isLoading: false, isStreaming: true },
    }
    await nextTick()

    expect(wrapper.find('[data-testid="session-switcher-streaming-parent-B"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="session-switcher-streaming-parent-A"]').exists()).toBe(false)
  })

  it('hides per-row indicators on idle sessions', async () => {
    const chatStore = useChatStore()

    const wrapper = mount(SessionSwitcher)
    await flushPromises()

    await wrapper.find('[aria-haspopup="listbox"]').trigger('click')
    await flushPromises()

    chatStore.sessions = [
      makeSession({ id: 'parent-A', title: 'Alpha' }),
      makeSession({ id: 'parent-B', title: 'Beta' }),
    ]
    chatStore.currentSessionId = 'parent-A'
    await nextTick()

    expect(wrapper.find('[data-testid="session-switcher-streaming-parent-A"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="session-switcher-streaming-parent-B"]').exists()).toBe(false)
  })

  it('shows a background-activity hint on the trigger when any non-current session is streaming', async () => {
    const chatStore = useChatStore()

    const wrapper = mount(SessionSwitcher)
    await flushPromises()

    chatStore.sessions = [
      makeSession({ id: 'parent-A', title: 'Alpha' }),
      makeSession({ id: 'parent-B', title: 'Beta' }),
    ]
    chatStore.currentSessionId = 'parent-A'
    chatStore.sessionStreaming = {
      'parent-B': { isLoading: false, isStreaming: true },
    }
    await nextTick()

    expect(wrapper.find('[data-testid="session-switcher-background-activity"]').exists()).toBe(true)
  })

  it('does not show the background-activity hint when only the current session is streaming', async () => {
    const chatStore = useChatStore()

    const wrapper = mount(SessionSwitcher)
    await flushPromises()

    chatStore.sessions = [
      makeSession({ id: 'parent-A', title: 'Alpha' }),
      makeSession({ id: 'parent-B', title: 'Beta' }),
    ]
    chatStore.currentSessionId = 'parent-A'
    chatStore.sessionStreaming = {
      'parent-A': { isLoading: false, isStreaming: true },
    }
    await nextTick()

    expect(wrapper.find('[data-testid="session-switcher-background-activity"]').exists()).toBe(false)
  })

  it('does not show the background-activity hint when no session is streaming', async () => {
    const chatStore = useChatStore()

    const wrapper = mount(SessionSwitcher)
    await flushPromises()

    chatStore.sessions = [
      makeSession({ id: 'parent-A', title: 'Alpha' }),
      makeSession({ id: 'parent-B', title: 'Beta' }),
    ]
    chatStore.currentSessionId = 'parent-A'
    await nextTick()

    expect(wrapper.find('[data-testid="session-switcher-background-activity"]').exists()).toBe(false)
  })
})
