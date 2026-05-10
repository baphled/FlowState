import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { nextTick } from 'vue'
import ChildSessionsPanel from './ChildSessionsPanel.vue'
import { useChatStore } from '@/stores/chatStore'
import type { SessionSummary } from '@/types'

// ChildSessionsPanel is the persistent sibling to DelegationStrip.
//
// DelegationStrip renders transient swarm-bus delegation events that vanish
// after a page reload. ChildSessionsPanel reads the persistent session graph
// from chatStore and surfaces every child of the current session, regardless
// of whether the originating swarm event is still in memory.
//
// Contracts pinned here:
//   1. Renders zero rows when the active session has no children.
//   2. Renders one row per child of the active session.
//   3. Clicking a row calls chatStore.loadSessionMessages(childId).
//   4. Re-derives when chatStore.currentSessionId changes (Vue reactivity).
//   5. Live-streaming children are flagged via chatStore.streamingFor(id).
function makeSession(overrides: Partial<SessionSummary> = {}): SessionSummary {
  return {
    id: 'session-x',
    agentId: 'planner',
    status: 'active',
    depth: 0,
    title: 'Untitled session',
    createdAt: '2026-05-10T09:00:00Z',
    updatedAt: '2026-05-10T09:00:00Z',
    messageCount: 0,
    isStreaming: false,
    ...overrides,
  }
}

describe('ChildSessionsPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders no rows when the current session has no children', async () => {
    const chatStore = useChatStore()
    chatStore.sessions = [makeSession({ id: 'parent-1' })]
    chatStore.currentSessionId = 'parent-1'

    const wrapper = mount(ChildSessionsPanel)
    await flushPromises()

    const rows = wrapper.findAll('[data-testid^="child-session-row-"]')
    expect(rows).toHaveLength(0)
  })

  it('renders one row per child of the current session', async () => {
    const chatStore = useChatStore()
    chatStore.sessions = [
      makeSession({ id: 'parent-1' }),
      makeSession({
        id: 'child-1',
        parentId: 'parent-1',
        agentId: 'researcher',
        title: 'Research foo',
        createdAt: '2026-05-10T09:01:00Z',
      }),
      makeSession({
        id: 'child-2',
        parentId: 'parent-1',
        agentId: 'reviewer',
        title: 'Review bar',
        createdAt: '2026-05-10T09:02:00Z',
      }),
      // Sibling under a different parent — must not appear.
      makeSession({ id: 'unrelated', parentId: 'parent-2' }),
    ]
    chatStore.currentSessionId = 'parent-1'

    const wrapper = mount(ChildSessionsPanel)
    await flushPromises()

    expect(wrapper.find('[data-testid="child-session-row-child-1"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="child-session-row-child-2"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="child-session-row-unrelated"]').exists()).toBe(false)
  })

  it('navigates to the child session via chatStore.loadSessionMessages when a row is clicked', async () => {
    const chatStore = useChatStore()
    chatStore.sessions = [
      makeSession({ id: 'parent-1' }),
      makeSession({ id: 'child-1', parentId: 'parent-1' }),
    ]
    chatStore.currentSessionId = 'parent-1'
    const loadSpy = vi.spyOn(chatStore, 'loadSessionMessages').mockResolvedValue()

    const wrapper = mount(ChildSessionsPanel)
    await flushPromises()

    const row = wrapper.find('[data-testid="child-session-row-child-1"]')
    expect(row.exists()).toBe(true)
    await row.trigger('click')
    await flushPromises()

    expect(loadSpy).toHaveBeenCalledWith('child-1')
  })

  it('re-derives when chatStore.currentSessionId changes', async () => {
    const chatStore = useChatStore()
    chatStore.sessions = [
      makeSession({ id: 'parent-1' }),
      makeSession({ id: 'parent-2' }),
      makeSession({ id: 'child-of-1', parentId: 'parent-1' }),
      makeSession({ id: 'child-of-2', parentId: 'parent-2' }),
    ]
    chatStore.currentSessionId = 'parent-1'

    const wrapper = mount(ChildSessionsPanel)
    await flushPromises()

    expect(wrapper.find('[data-testid="child-session-row-child-of-1"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="child-session-row-child-of-2"]').exists()).toBe(false)

    chatStore.currentSessionId = 'parent-2'
    await nextTick()
    await flushPromises()

    expect(wrapper.find('[data-testid="child-session-row-child-of-1"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="child-session-row-child-of-2"]').exists()).toBe(true)
  })

  it('marks a child row as streaming when chatStore.streamingFor reports an active stream', async () => {
    const chatStore = useChatStore()
    chatStore.sessions = [
      makeSession({ id: 'parent-1' }),
      makeSession({ id: 'child-streaming', parentId: 'parent-1' }),
      makeSession({ id: 'child-idle', parentId: 'parent-1' }),
    ]
    chatStore.currentSessionId = 'parent-1'
    chatStore.sessionStreaming = {
      'child-streaming': { isLoading: false, isStreaming: true },
    }

    const wrapper = mount(ChildSessionsPanel)
    await flushPromises()

    const streamingRow = wrapper.find('[data-testid="child-session-row-child-streaming"]')
    const idleRow = wrapper.find('[data-testid="child-session-row-child-idle"]')

    expect(streamingRow.classes()).toContain('is-streaming')
    expect(idleRow.classes()).not.toContain('is-streaming')
  })

  it('hides the panel container when there are no children', async () => {
    const chatStore = useChatStore()
    chatStore.sessions = [makeSession({ id: 'parent-1' })]
    chatStore.currentSessionId = 'parent-1'

    const wrapper = mount(ChildSessionsPanel)
    await flushPromises()

    const root = wrapper.find('[data-testid="child-sessions-panel"]')
    expect(root.exists()).toBe(true)
    expect(root.classes()).toContain('is-empty')
  })
})
