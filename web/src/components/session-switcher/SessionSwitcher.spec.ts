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
// Child Session Turn Registry Plumbing (May 2026) PR3 — backend-authoritative
// Live indicator. Pre-PR3 the indicators gated on chatStore.streamingFor;
// PR3 flipped them to consume SessionSummary.activeTurnId (projected from
// the backend Turn registry by handleListV1Sessions). See plan §Item 3 +
// §R8 for the dual-source boundary documentation.
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

  // S9.4 — backend-authoritative Live indicator driven by activeTurnId.
  it('shows a per-row live indicator on sessions with a non-empty activeTurnId when the dropdown is open', async () => {
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
      makeSession({ id: 'parent-B', title: 'Beta', activeTurnId: 'turn-beta-001' }),
    ]
    chatStore.currentSessionId = 'parent-A'
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

  // S9.4 — background-activity hint also reads activeTurnId. The hint must
  // fire when any non-current session has a Running Turn.
  it('shows a background-activity hint on the trigger when any non-current session has an active turn', async () => {
    const chatStore = useChatStore()

    const wrapper = mount(SessionSwitcher)
    await flushPromises()

    chatStore.sessions = [
      makeSession({ id: 'parent-A', title: 'Alpha' }),
      makeSession({ id: 'parent-B', title: 'Beta', activeTurnId: 'turn-beta-bg' }),
    ]
    chatStore.currentSessionId = 'parent-A'
    await nextTick()

    expect(wrapper.find('[data-testid="session-switcher-background-activity"]').exists()).toBe(true)
  })

  it('does not show the background-activity hint when only the current session has an active turn', async () => {
    const chatStore = useChatStore()

    const wrapper = mount(SessionSwitcher)
    await flushPromises()

    chatStore.sessions = [
      makeSession({ id: 'parent-A', title: 'Alpha', activeTurnId: 'turn-A-self' }),
      makeSession({ id: 'parent-B', title: 'Beta' }),
    ]
    chatStore.currentSessionId = 'parent-A'
    await nextTick()

    expect(wrapper.find('[data-testid="session-switcher-background-activity"]').exists()).toBe(false)
  })

  it('does not show the background-activity hint when no session has an active turn', async () => {
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

  // S9.4 — regression pin per R8. Seeding streamingFor with isStreaming=true
  // but leaving activeTurnId empty must NOT light up the per-row indicator
  // OR the trigger's background-activity hint. The list surface is
  // backend-authoritative.
  it('ignores chatStore.streamingFor entries — list rendering is backend-authoritative (R8 regression pin)', async () => {
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
    // streamingFor slot reports isStreaming=true; the list surface must
    // ignore it because activeTurnId is empty for both.
    chatStore.sessionStreaming = {
      'parent-B': { isLoading: false, isStreaming: true },
    }
    await nextTick()

    expect(wrapper.find('[data-testid="session-switcher-streaming-parent-B"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="session-switcher-background-activity"]').exists()).toBe(false)
  })
})

// QW-11 — Per-row delete + ordering in the switcher dropdown.
describe('SessionSwitcher per-row delete and ordering', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders a per-row delete button next to each session row', async () => {
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

    expect(wrapper.find('[data-testid="session-switcher-delete-parent-A"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="session-switcher-delete-parent-B"]').exists()).toBe(true)
  })

  it('invokes chatStore.deleteSession with the row id when the user confirms via the inline strip', async () => {
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

    const spy = vi.spyOn(chatStore, 'deleteSession').mockResolvedValue(undefined)

    await wrapper.find('[data-testid="session-switcher-delete-parent-B"]').trigger('click')
    await wrapper.find('[data-testid="session-switcher-confirm-delete-parent-B"]').trigger('click')
    await flushPromises()

    expect(spy).toHaveBeenCalledTimes(1)
    expect(spy).toHaveBeenCalledWith('parent-B')
  })

  it('orders rows by orderedSessions (streaming first, then updatedAt desc)', async () => {
    const chatStore = useChatStore()
    const wrapper = mount(SessionSwitcher)
    await flushPromises()
    await wrapper.find('[aria-haspopup="listbox"]').trigger('click')
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

    const titles = wrapper.findAll('.option-title').map((el) => el.text())
    expect(titles).toEqual(['Streaming', 'Recent', 'Mid'])
  })
})

// UI Parity I3 (May 2026) — Cmd+K / Ctrl+K fuzzy session palette.
//
// The palette is a `FuzzySearchModal` consumer that surfaces all root
// sessions (children are still filtered out — they remain reachable via
// the ChildSessionsPanel under the chat thread). Cmd+K (mac) and Ctrl+K
// (everywhere else) both open it from a document-level keydown listener
// installed on mount. Selecting a row jumps to that session via
// chatStore.currentSessionId + loadSessionMessages — the same path the
// existing dropdown takes.
//
// The existing dropdown stays intact: the activity-indicator surface
// (background-activity dot, per-row streaming dot, inline-confirm delete)
// keeps living there. The palette is purely a fast switcher overlay.
describe('SessionSwitcher Cmd+K fuzzy palette (I3)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('opens the fuzzy palette on Cmd+K (metaKey)', async () => {
    const wrapper = mount(SessionSwitcher, { attachTo: document.body })
    await flushPromises()

    expect(wrapper.find('[data-testid="session-palette-modal"]').exists()).toBe(false)

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true }))
    await nextTick()

    expect(wrapper.find('[data-testid="session-palette-modal"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('opens the fuzzy palette on Ctrl+K (ctrlKey)', async () => {
    const wrapper = mount(SessionSwitcher, { attachTo: document.body })
    await flushPromises()

    expect(wrapper.find('[data-testid="session-palette-modal"]').exists()).toBe(false)

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true }))
    await nextTick()

    expect(wrapper.find('[data-testid="session-palette-modal"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('does not open the palette on bare K (no modifier)', async () => {
    const wrapper = mount(SessionSwitcher, { attachTo: document.body })
    await flushPromises()

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k' }))
    await nextTick()

    expect(wrapper.find('[data-testid="session-palette-modal"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('surfaces only root sessions in the palette (children filtered out)', async () => {
    const chatStore = useChatStore()
    const wrapper = mount(SessionSwitcher, { attachTo: document.body })
    await flushPromises()

    chatStore.sessions = [
      makeSession({ id: 'root-A', title: 'Alpha' }),
      makeSession({ id: 'child-A', title: 'Child of Alpha', parentId: 'root-A' }),
      makeSession({ id: 'root-B', title: 'Beta' }),
    ]
    await nextTick()

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true }))
    await nextTick()

    expect(wrapper.find('[data-testid="fuzzy-search-item-root-A"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="fuzzy-search-item-root-B"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="fuzzy-search-item-child-A"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('jumps to the chosen session and closes the palette on select', async () => {
    const chatStore = useChatStore()
    const wrapper = mount(SessionSwitcher, { attachTo: document.body })
    await flushPromises()

    chatStore.sessions = [
      makeSession({ id: 'root-A', title: 'Alpha' }),
      makeSession({ id: 'root-B', title: 'Beta' }),
    ]
    chatStore.currentSessionId = 'root-A'
    await nextTick()

    const loadSpy = vi.spyOn(chatStore, 'loadSessionMessages').mockResolvedValue(undefined)

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true }))
    await nextTick()

    await wrapper.find('[data-testid="fuzzy-search-item-root-B"]').trigger('click')
    await flushPromises()

    expect(chatStore.currentSessionId).toBe('root-B')
    expect(loadSpy).toHaveBeenCalledWith('root-B')
    expect(wrapper.find('[data-testid="session-palette-modal"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
