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
// ChildSessionsPanel's vocabulary) when a Running Turn exists for the
// session.
//
// Child Session Turn Registry Plumbing (May 2026) PR3 — backend-authoritative
// Live indicator. Pre-PR3 the indicator gated on chatStore.streamingFor;
// PR3 flipped this to consume SessionSummary.activeTurnId (projected by
// handleListV1Sessions from the Turn registry). The dual-source boundary
// is documented in plan §R8 — list-rendering surfaces (this component,
// SessionSwitcher, ChildSessionsPanel) read activeTurnId; current-session
// surfaces (ChatView, MessageInput) keep streamingFor for optimistic UI.
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

  // S9.3 — backend-authoritative Live indicator driven by activeTurnId.
  it('shows a per-row live indicator on sessions with a non-empty activeTurnId', async () => {
    const chatStore = useChatStore()

    const wrapper = mount(SessionBrowser)
    ;(wrapper.vm as unknown as { open: () => void }).open()
    await flushPromises()

    chatStore.sessions = [
      makeSession({ id: 'session-A', title: 'Alpha' }),
      makeSession({ id: 'session-B', title: 'Beta', activeTurnId: 'turn-beta-001' }),
    ]
    await nextTick()

    expect(wrapper.find('[data-testid="session-browser-streaming-session-B"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="session-browser-streaming-session-A"]').exists()).toBe(false)
  })

  it('does not render any indicator when no session has an active turn', async () => {
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

  // S9.3 — regression pin per R8. The session-list surface MUST consume
  // activeTurnId and ignore any chatStore.streamingFor entry for the same
  // session. Seeding streamingFor with isStreaming=true but leaving
  // activeTurnId empty must NOT light up the indicator.
  it('ignores chatStore.streamingFor entries — list rendering is backend-authoritative (R8 regression pin)', async () => {
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

    // Both rows must be idle: A has no activeTurnId, B's streamingFor slot
    // is irrelevant to the list-surface indicator.
    expect(wrapper.findAll('[data-testid^="session-browser-streaming-"]')).toHaveLength(0)
  })
})

// QW-11 — Per-row delete affordance, inline confirmation, ordering.
//
// Contract:
//   - Every row exposes a `[data-testid="session-browser-delete-<id>"]`
//     button. Clicking it reveals an inline confirmation strip (Cancel +
//     Delete) on that row only. Confirming calls `chatStore.deleteSession`
//     with the row id; cancelling restores the idle button. On failure the
//     row stays in place (no optimistic remove on error).
//   - Row order follows `chatStore.orderedSessions`: streaming first, then
//     updatedAt descending. SessionBrowser composes its own filter on top
//     (search + agent filter) but never mutates the store-ordered list.
describe('SessionBrowser per-row delete', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  async function openWithSessions(): Promise<{ wrapper: ReturnType<typeof mount>, chatStore: ReturnType<typeof useChatStore> }> {
    const chatStore = useChatStore()
    const wrapper = mount(SessionBrowser)
    ;(wrapper.vm as unknown as { open: () => void }).open()
    await flushPromises()
    chatStore.sessions = [
      makeSession({ id: 'session-A', title: 'Alpha', updatedAt: '2026-05-10T09:00:00Z' }),
      makeSession({ id: 'session-B', title: 'Beta', updatedAt: '2026-05-11T09:00:00Z' }),
    ]
    await nextTick()
    return { wrapper, chatStore }
  }

  it('renders a per-row delete button on every session card', async () => {
    const { wrapper } = await openWithSessions()

    expect(wrapper.find('[data-testid="session-browser-delete-session-A"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="session-browser-delete-session-B"]').exists()).toBe(true)
  })

  it('shows an inline confirmation on the row when the delete button is clicked', async () => {
    const { wrapper } = await openWithSessions()

    await wrapper.find('[data-testid="session-browser-delete-session-A"]').trigger('click')

    expect(wrapper.find('[data-testid="session-browser-delete-confirm-session-A"]').exists()).toBe(true)
    // Other rows are unaffected — only the clicked row enters confirm state.
    expect(wrapper.find('[data-testid="session-browser-delete-confirm-session-B"]').exists()).toBe(false)
  })

  it('cancels back to the idle delete button when the user clicks Cancel', async () => {
    const { wrapper } = await openWithSessions()

    await wrapper.find('[data-testid="session-browser-delete-session-A"]').trigger('click')
    await wrapper.find('[data-testid="session-browser-cancel-delete-session-A"]').trigger('click')

    expect(wrapper.find('[data-testid="session-browser-delete-confirm-session-A"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="session-browser-delete-session-A"]').exists()).toBe(true)
  })

  it('invokes chatStore.deleteSession with the row id when the user confirms', async () => {
    const { wrapper, chatStore } = await openWithSessions()
    const spy = vi.spyOn(chatStore, 'deleteSession').mockResolvedValue(undefined)

    await wrapper.find('[data-testid="session-browser-delete-session-A"]').trigger('click')
    await wrapper.find('[data-testid="session-browser-confirm-delete-session-A"]').trigger('click')
    await flushPromises()

    expect(spy).toHaveBeenCalledTimes(1)
    expect(spy).toHaveBeenCalledWith('session-A')
  })

  it('does not call chatStore.deleteSession when the card body is clicked (delete button click stays scoped)', async () => {
    const { wrapper, chatStore } = await openWithSessions()
    const spy = vi.spyOn(chatStore, 'deleteSession').mockResolvedValue(undefined)

    await wrapper.find('[data-testid="session-browser-delete-session-A"]').trigger('click')

    // Toggling confirm state must NOT have invoked delete yet.
    expect(spy).not.toHaveBeenCalled()
  })

  it('orders rows by chatStore.orderedSessions (streaming first, then updatedAt desc)', async () => {
    const chatStore = useChatStore()
    const wrapper = mount(SessionBrowser)
    ;(wrapper.vm as unknown as { open: () => void }).open()
    await flushPromises()
    chatStore.sessions = [
      makeSession({ id: 'idle-recent', title: 'Recent', updatedAt: '2026-05-11T10:00:00Z' }),
      makeSession({ id: 'streaming-old', title: 'Streaming', updatedAt: '2026-05-09T09:00:00Z' }),
      makeSession({ id: 'idle-mid', title: 'Mid', updatedAt: '2026-05-10T09:00:00Z' }),
    ]
    chatStore.sessionStreaming = {
      'streaming-old': { isLoading: false, isStreaming: true },
    }
    await nextTick()

    const titles = wrapper.findAll('.session-title').map((el) => el.text())
    expect(titles).toEqual(['Streaming', 'Recent', 'Mid'])
  })
})

// UI Parity PR6 I1 residual (May 2026) — the modal close button rendered a
// raw `✕` character instead of the Icon wrapper. Every chrome glyph elsewhere
// on this surface is the Icon wrapper (search, plus, trash, message), so the
// close affordance was the lone holdout. Pin the contract so it can't drift.
describe('SessionBrowser close button (I1 residual — ✕ → Icon)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('renders <Icon name="close" /> in the close button, not a raw ✕ glyph', async () => {
    const wrapper = mount(SessionBrowser)
    ;(wrapper.vm as unknown as { open: () => void }).open()
    await flushPromises()

    const closeBtn = wrapper.find('.close-button')
    expect(closeBtn.exists()).toBe(true)

    // Icon wrapper stamps data-icon-name on the rendered SVG.
    const closeIcon = closeBtn.find('[data-testid="icon"][data-icon-name="close"]')
    expect(closeIcon.exists()).toBe(true)

    // The raw ✕ glyph must not appear in the close button's text content.
    expect(closeBtn.text()).not.toContain('✕')
  })
})

// UI Parity I9 (May 2026) — agent filter swap from native <select> to
// FuzzySearchModal.
//
// Pre-fix the agent filter was a `<select>` (`SessionBrowser.vue:175-184`)
// which renders inconsistently across OS chrome and has no typeahead. The
// modal is replaced with a button → `FuzzySearchModal` consumer so the
// experience matches the slash/mention/agent/model pickers used everywhere
// else.
//
// Contract:
//   - The `<select.agent-filter>` element MUST NOT exist.
//   - A trigger button `[data-testid="agent-filter-trigger"]` is rendered.
//   - Clicking it opens a FuzzySearchModal listing one entry per agent
//     present in `chatStore.sessions`, plus an explicit "All Agents" entry.
//   - Selecting an agent sets the filter to that agent's id; the session
//     list re-filters accordingly. Selecting "All Agents" clears the filter.
describe('SessionBrowser agent filter — fuzzy modal (I9)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  async function openWithAgents(): Promise<{ wrapper: ReturnType<typeof mount>, chatStore: ReturnType<typeof useChatStore> }> {
    const chatStore = useChatStore()
    const wrapper = mount(SessionBrowser)
    ;(wrapper.vm as unknown as { open: () => void }).open()
    await flushPromises()
    chatStore.sessions = [
      makeSession({ id: 'session-A', title: 'Alpha', agentId: 'planner' }),
      makeSession({ id: 'session-B', title: 'Beta', agentId: 'researcher' }),
    ]
    chatStore.availableAgentDetails = [
      { id: 'planner', name: 'Planner', description: 'Plans things', model: 'sonnet-4-5', provider: 'anthropic' },
      { id: 'researcher', name: 'Researcher', description: 'Researches things', model: 'sonnet-4-5', provider: 'anthropic' },
    ]
    await nextTick()
    return { wrapper, chatStore }
  }

  it('does not render a native <select.agent-filter>', async () => {
    const { wrapper } = await openWithAgents()
    expect(wrapper.find('select.agent-filter').exists()).toBe(false)
  })

  it('renders an agent-filter trigger button', async () => {
    const { wrapper } = await openWithAgents()
    expect(wrapper.find('[data-testid="agent-filter-trigger"]').exists()).toBe(true)
  })

  it('opens a FuzzySearchModal listing each agent on trigger click', async () => {
    const { wrapper } = await openWithAgents()

    await wrapper.find('[data-testid="agent-filter-trigger"]').trigger('click')
    await nextTick()

    expect(wrapper.find('[data-testid="agent-filter-modal"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="fuzzy-search-item-all"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="fuzzy-search-item-planner"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="fuzzy-search-item-researcher"]').exists()).toBe(true)
  })

  it('filters the session list by the chosen agent id', async () => {
    const { wrapper } = await openWithAgents()

    await wrapper.find('[data-testid="agent-filter-trigger"]').trigger('click')
    await nextTick()
    await wrapper.find('[data-testid="fuzzy-search-item-planner"]').trigger('click')
    await nextTick()

    const titles = wrapper.findAll('.session-title').map((el) => el.text())
    expect(titles).toEqual(['Alpha'])
  })

  it('resets the filter back to all agents when the "All Agents" entry is chosen', async () => {
    const { wrapper } = await openWithAgents()

    await wrapper.find('[data-testid="agent-filter-trigger"]').trigger('click')
    await nextTick()
    await wrapper.find('[data-testid="fuzzy-search-item-planner"]').trigger('click')
    await nextTick()

    await wrapper.find('[data-testid="agent-filter-trigger"]').trigger('click')
    await nextTick()
    await wrapper.find('[data-testid="fuzzy-search-item-all"]').trigger('click')
    await nextTick()

    const titles = wrapper.findAll('.session-title').map((el) => el.text())
    expect(titles.sort()).toEqual(['Alpha', 'Beta'])
  })
})
