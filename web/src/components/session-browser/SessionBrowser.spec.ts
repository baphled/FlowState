import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { nextTick } from 'vue'
import SessionBrowser from './SessionBrowser.vue'
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

// UX consolidation (May 2026) — every place that lists sessions must reveal
// active streams so users notice background work. SessionBrowser is the
// modal-style picker; rows must surface a compact live indicator (matching
// ChildSessionsPanel's vocabulary) when the per-session streamingFor slot
// reports isStreaming.
//
// Test ordering note: SessionBrowser.onMounted + open() both call
// `chatStore.loadSessions()` which resolves the mocked `fetchSessions`
// returning `[]`. Seed the store AFTER the mocked load completes so the
// test data isn't clobbered.
describe('SessionBrowser activity indicators', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows a per-row live indicator on streaming sessions', async () => {
    const chatStore = useChatStore()

    const wrapper = mount(SessionBrowser)
    ;(wrapper.vm as unknown as { open: () => void }).open()
    await flushPromises()

    chatStore.sessions = [
      makeSession({ id: 'session-A', title: 'Alpha' }),
      makeSession({ id: 'session-B', title: 'Beta' }),
    ]
    chatStore.sessionStreaming = {
      'session-B': { isLoading: false, isStreaming: true },
    }
    await nextTick()

    expect(wrapper.find('[data-testid="session-browser-streaming-session-B"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="session-browser-streaming-session-A"]').exists()).toBe(false)
  })

  it('does not render any indicator when no session is streaming', async () => {
    const chatStore = useChatStore()

    const wrapper = mount(SessionBrowser)
    ;(wrapper.vm as unknown as { open: () => void }).open()
    await flushPromises()

    chatStore.sessions = [
      makeSession({ id: 'session-A', title: 'Alpha' }),
      makeSession({ id: 'session-B', title: 'Beta' }),
    ]
    await nextTick()

    expect(wrapper.findAll('[data-testid^="session-browser-streaming-"]')).toHaveLength(0)
  })
})
