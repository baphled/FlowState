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
//   5. Live-streaming children are flagged via the SessionSummary's
//      backend-authoritative `activeTurnId` field (Child Session Turn
//      Registry Plumbing May 2026 §Item 3). Pre-PR3 this read from
//      `chatStore.streamingFor(id)`; PR3 flipped it because child
//      sessions are spawned by the engine, not by a user POST, and so
//      the FE-side optimistic UI gap that motivates `streamingFor`
//      never applies. See §R8 for the dual-source boundary.
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

  // Sibling-confusion fix (May 2026 bug-hunt round 7) — clicking a child
  // row now routes through chatStore.loadSessionForDelegation. The
  // resolver disambiguates by chainId when known (live SwarmEvent /
  // cold-reload backfill), uses the validated childSessionId hint
  // otherwise, and falls back to the agent-id heuristic. Pre-fix this
  // path called loadSessionMessages directly, bypassing the resolver
  // entirely — both other delegated-session click surfaces share the
  // same seam now (MessageBubble in-thread cards, DelegationPanel swarm
  // bus events) so the fix surface for the bug class is unified.
  it('navigates to the child session via chatStore.loadSessionForDelegation when a row is clicked', async () => {
    const chatStore = useChatStore()
    chatStore.sessions = [
      makeSession({ id: 'parent-1' }),
      makeSession({
        id: 'child-1',
        parentId: 'parent-1',
        chainId: 'chain-1',
        agentId: 'executor',
      }),
    ]
    chatStore.currentSessionId = 'parent-1'
    const delegationSpy = vi
      .spyOn(chatStore, 'loadSessionForDelegation')
      .mockResolvedValue(true)
    // The unguarded path MUST NOT be the click entry point anymore.
    const loadSpy = vi.spyOn(chatStore, 'loadSessionMessages').mockResolvedValue()

    const wrapper = mount(ChildSessionsPanel)
    await flushPromises()

    const row = wrapper.find('[data-testid="child-session-row-child-1"]')
    expect(row.exists()).toBe(true)
    await row.trigger('click')
    await flushPromises()

    expect(delegationSpy).toHaveBeenCalledWith({
      chainId: 'chain-1',
      childSessionId: 'child-1',
      agentId: 'executor',
    })
    expect(loadSpy).not.toHaveBeenCalled()
  })

  // SessionSummary may omit chainId (root re-entry or pre-chainId data).
  // The resolver still routes correctly via the validated childSessionId
  // hint; we pin that the panel calls the resolver shape that exercises
  // that path.
  it('passes only the childSessionId + agentId when the child has no chainId', async () => {
    const chatStore = useChatStore()
    chatStore.sessions = [
      makeSession({ id: 'parent-1' }),
      makeSession({
        id: 'child-no-chain',
        parentId: 'parent-1',
        agentId: 'executor',
      }),
    ]
    chatStore.currentSessionId = 'parent-1'
    const delegationSpy = vi
      .spyOn(chatStore, 'loadSessionForDelegation')
      .mockResolvedValue(true)

    const wrapper = mount(ChildSessionsPanel)
    await flushPromises()

    const row = wrapper.find('[data-testid="child-session-row-child-no-chain"]')
    await row.trigger('click')
    await flushPromises()

    expect(delegationSpy).toHaveBeenCalledWith({
      chainId: undefined,
      childSessionId: 'child-no-chain',
      agentId: 'executor',
    })
  })

  // currentAgentId wins over agentId — sessions whose agent was
  // re-routed mid-flight carry the live agent on currentAgentId. The
  // resolver's agent-id fallback consults the same precedence, so the
  // panel must pass the same one.
  it('prefers currentAgentId over agentId when both are set', async () => {
    const chatStore = useChatStore()
    chatStore.sessions = [
      makeSession({ id: 'parent-1' }),
      makeSession({
        id: 'child-rerouted',
        parentId: 'parent-1',
        agentId: 'initial-agent',
        currentAgentId: 'live-agent',
        chainId: 'chain-rerouted',
      }),
    ]
    chatStore.currentSessionId = 'parent-1'
    const delegationSpy = vi
      .spyOn(chatStore, 'loadSessionForDelegation')
      .mockResolvedValue(true)

    const wrapper = mount(ChildSessionsPanel)
    await flushPromises()

    const row = wrapper.find('[data-testid="child-session-row-child-rerouted"]')
    await row.trigger('click')
    await flushPromises()

    expect(delegationSpy).toHaveBeenCalledWith({
      chainId: 'chain-rerouted',
      childSessionId: 'child-rerouted',
      agentId: 'live-agent',
    })
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

  // S9.1 — backend-authoritative Live indicator. Pre-PR3 the panel read
  // chatStore.streamingFor(child.id); PR3 flipped this to consume the
  // SessionSummary.activeTurnId stamped by handleListV1Sessions from the
  // backend Turn registry. The spec now seeds the summary with a non-empty
  // activeTurnId and asserts the row carries the is-streaming class
  // without touching chatStore.sessionStreaming.
  it('marks a child row as streaming when the backend-stamped activeTurnId is non-empty', async () => {
    const chatStore = useChatStore()
    chatStore.sessions = [
      makeSession({ id: 'parent-1' }),
      makeSession({
        id: 'child-streaming',
        parentId: 'parent-1',
        activeTurnId: 'turn-abc-123',
      }),
      makeSession({ id: 'child-idle', parentId: 'parent-1' }),
    ]
    chatStore.currentSessionId = 'parent-1'

    const wrapper = mount(ChildSessionsPanel)
    await flushPromises()

    const streamingRow = wrapper.find('[data-testid="child-session-row-child-streaming"]')
    const idleRow = wrapper.find('[data-testid="child-session-row-child-idle"]')

    expect(streamingRow.classes()).toContain('is-streaming')
    expect(idleRow.classes()).not.toContain('is-streaming')
  })

  // S9.1 — empty-string activeTurnId behaves as idle. The backend serialises
  // the field as `""` (not omitted) when no Running Turn exists; the FE
  // must treat both empty-string and undefined identically.
  it('treats an empty-string activeTurnId as idle (not streaming)', async () => {
    const chatStore = useChatStore()
    chatStore.sessions = [
      makeSession({ id: 'parent-1' }),
      makeSession({ id: 'child-idle-empty', parentId: 'parent-1', activeTurnId: '' }),
    ]
    chatStore.currentSessionId = 'parent-1'

    const wrapper = mount(ChildSessionsPanel)
    await flushPromises()

    const row = wrapper.find('[data-testid="child-session-row-child-idle-empty"]')
    expect(row.classes()).not.toContain('is-streaming')
  })

  // S9.2 (regression pin) — backend-authoritative shift means the panel
  // MUST NOT consume chatStore.streamingFor for any child. Pre-PR3 the
  // panel consumed streamingFor(child.id); this spec asserts the FE-side
  // optimistic-UI slot is now irrelevant to the indicator. Seeding
  // streamingFor with isStreaming=true while leaving activeTurnId empty
  // must NOT light up the row. (The complementary static-text pin lives
  // in dualSourceBoundary.spec.ts, which asserts ChildSessionsPanel.vue
  // contains no `streamingFor` token at all — a code-shape contract.)
  it('does not surface a Live indicator when streamingFor reports streaming but activeTurnId is empty (R8 regression pin)', async () => {
    const chatStore = useChatStore()
    chatStore.sessions = [
      makeSession({ id: 'parent-1' }),
      makeSession({ id: 'child-1', parentId: 'parent-1' }),
    ]
    chatStore.currentSessionId = 'parent-1'
    // Seed streamingFor with isStreaming=true; the panel MUST ignore this
    // because activeTurnId is empty. Pre-PR3 this would have lit up the
    // child row erroneously.
    chatStore.sessionStreaming = {
      'child-1': { isLoading: false, isStreaming: true },
    }

    const wrapper = mount(ChildSessionsPanel)
    await flushPromises()

    const row = wrapper.find('[data-testid="child-session-row-child-1"]')
    expect(row.exists()).toBe(true)
    expect(row.classes()).not.toContain('is-streaming')
  })

  // UX consolidation (May 2026) — accessibility hardening for streaming
  // affordance. The pre-existing green border + green dot fail for users with
  // green colour-blindness; pair the visual cue with a redundant text label
  // so the affordance is robust to colour-vision deficiencies.
  it('renders a redundant "Live" text label on streaming rows for colour-blind users', async () => {
    const chatStore = useChatStore()
    chatStore.sessions = [
      makeSession({ id: 'parent-1' }),
      // PR3 — backend-authoritative: drive the "Live" label off
      // SessionSummary.activeTurnId instead of chatStore.sessionStreaming.
      makeSession({
        id: 'child-streaming',
        parentId: 'parent-1',
        activeTurnId: 'turn-xyz-456',
      }),
      makeSession({ id: 'child-idle', parentId: 'parent-1' }),
    ]
    chatStore.currentSessionId = 'parent-1'

    const wrapper = mount(ChildSessionsPanel)
    await flushPromises()

    const streamingLabel = wrapper.find('[data-testid="child-session-streaming-label-child-streaming"]')
    expect(streamingLabel.exists()).toBe(true)
    expect(streamingLabel.text()).toBe('Live')

    const idleLabel = wrapper.find('[data-testid="child-session-streaming-label-child-idle"]')
    expect(idleLabel.exists()).toBe(false)
  })

  // UX consolidation — surface the full title on hover when the summary text
  // is truncated by .panel-summary's ellipsis. Without this the only way to
  // see a long title was to navigate into the child session.
  it('exposes the full row title via a title attribute on the row entry for hover-revealed text', async () => {
    const chatStore = useChatStore()
    chatStore.sessions = [
      makeSession({ id: 'parent-1' }),
      makeSession({
        id: 'child-long-title',
        parentId: 'parent-1',
        title: 'This is a deliberately very long delegated-session title that the ellipsis will swallow',
      }),
    ]
    chatStore.currentSessionId = 'parent-1'

    const wrapper = mount(ChildSessionsPanel)
    await flushPromises()

    const row = wrapper.find('[data-testid="child-session-row-child-long-title"]')
    expect(row.exists()).toBe(true)
    expect(row.attributes('title')).toBe(
      'This is a deliberately very long delegated-session title that the ellipsis will swallow',
    )
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
