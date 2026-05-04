import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useChatStore } from './chatStore'
import * as api from '@/api'

// Minimal mock of @/api — only the seams exercised by the actions under test.
vi.mock('@/api', () => ({
  fetchAgents: vi.fn().mockResolvedValue([]),
  fetchSessions: vi.fn().mockResolvedValue([]),
  fetchSessionMessages: vi.fn().mockResolvedValue([]),
  sendSessionMessage: vi.fn().mockResolvedValue(undefined),
  createSession: vi.fn().mockResolvedValue({ id: 'session-1' }),
  subscribeSessionStream: vi.fn().mockReturnValue({
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    close: vi.fn(),
    readyState: 1,
  }),
  fetchModels: vi.fn().mockResolvedValue([]),
  truncateSessionMessages: vi.fn().mockResolvedValue(undefined),
  updateSessionAgent: vi.fn().mockResolvedValue(undefined),
  updateSessionModel: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('./todoStore', () => ({
  useTodoStore: () => ({
    setCurrentSession: vi.fn(),
    hydrateFromMessages: vi.fn(),
  }),
}))

Object.defineProperty(window, 'localStorage', {
  value: {
    getItem: vi.fn(() => null),
    setItem: vi.fn(),
    removeItem: vi.fn(),
    clear: vi.fn(),
  },
  writable: true,
  configurable: true,
})

describe('chatStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  // ── restoreStateFromBackend — streaming reconnect ─────────────────────────
  //
  // These tests exercise the NEW isStreaming-based reconnect path introduced
  // to cover a gap in the existing message-heuristic approach:
  //
  //   Gap: the backend may be actively streaming but the last persisted message
  //   is an assistant message WITHOUT status 'running' (e.g. a partial response
  //   written mid-stream via the accumulator). The existing maybeReattachStream
  //   heuristic misses this case because it only checks lastMessage.role===user
  //   or lastMessage.status==='running'.
  //
  //   Fix: when the session list includes isStreaming: true, the store must
  //   subscribe regardless of the message heuristic.

  describe('restoreStateFromBackend — streaming reconnect', () => {
    it('calls subscribeSessionStream when the session summary has isStreaming: true, even when last message is a completed assistant message', async () => {
      // Key: last message is assistant WITHOUT status 'running' — the
      // existing heuristic would NOT reconnect. isStreaming: true on the
      // summary is the only signal that should trigger reconnect here.
      const mockSubscribe = vi.mocked(api.subscribeSessionStream)

      vi.mocked(api.fetchSessions).mockResolvedValue([
        {
          id: 'session-streaming',
          agentId: 'team-lead',
          title: 'In-progress',
          createdAt: '2026-05-04T00:00:00Z',
          updatedAt: '2026-05-04T00:00:01Z',
          messageCount: 2,
          status: 'active',
          depth: 0,
          isStreaming: true,
        },
      ])
      // Last message is assistant with no status — heuristic would skip reconnect.
      vi.mocked(api.fetchSessionMessages).mockResolvedValue([
        { id: 'u1', role: 'user', content: 'hello', timestamp: '2026-05-04T00:00:00Z' },
        { id: 'a1', role: 'assistant', content: 'partial…', timestamp: '2026-05-04T00:00:01Z' },
      ])

      window.localStorage.getItem = vi.fn().mockImplementation((key: string) => {
        if (key === 'chat.currentSessionId') return 'session-streaming'
        if (key === 'chat.agentId') return 'team-lead'
        return null
      })

      const store = useChatStore()
      await store.restoreStateFromBackend()

      expect(mockSubscribe).toHaveBeenCalledWith('session-streaming')
      expect(store.isStreaming).toBe(true)
    })

    it('does not call subscribeSessionStream when the session summary has isStreaming: false and last message is a completed assistant', async () => {
      const mockSubscribe = vi.mocked(api.subscribeSessionStream)

      vi.mocked(api.fetchSessions).mockResolvedValue([
        {
          id: 'session-done',
          agentId: 'team-lead',
          title: 'Completed',
          createdAt: '2026-05-04T00:00:00Z',
          updatedAt: '2026-05-04T00:00:01Z',
          messageCount: 2,
          status: 'active',
          depth: 0,
          isStreaming: false,
        },
      ])
      vi.mocked(api.fetchSessionMessages).mockResolvedValue([
        { id: 'u1', role: 'user', content: 'hello', timestamp: '2026-05-04T00:00:00Z' },
        { id: 'a1', role: 'assistant', content: 'hi there', timestamp: '2026-05-04T00:00:01Z', status: 'completed' },
      ])

      window.localStorage.getItem = vi.fn().mockImplementation((key: string) => {
        if (key === 'chat.currentSessionId') return 'session-done'
        if (key === 'chat.agentId') return 'team-lead'
        return null
      })

      const store = useChatStore()
      await store.restoreStateFromBackend()

      expect(mockSubscribe).not.toHaveBeenCalled()
      expect(store.isStreaming).toBe(false)
    })
  })

  // ── applyDelegationEvent ──────────────────────────────────────────────────

  describe('applyDelegationEvent', () => {
    it('creates a delegation_started message when no matching message exists', () => {
      const store = useChatStore()
      store.messages = [
        { id: 'u1', role: 'user', content: 'plan something', timestamp: '2026-05-04T00:00:00Z' },
      ]

      store.applyDelegationEvent(
        JSON.stringify({ target_agent: 'executor', chain_id: 'chain-1', status: 'started' }),
      )

      // The delegation message must use role:'delegation_started' so
      // MessageBubble renders delegation chrome rather than a plain bubble.
      const delegation = store.messages.find((m) => m.role === 'delegation_started')
      expect(delegation).toBeDefined()
      expect(delegation?.targetAgent).toBe('executor')
      expect(delegation?.chainId).toBe('chain-1')
    })

    it('updates an existing delegation_started message matched by chain_id without duplicating', () => {
      const store = useChatStore()
      store.messages = [
        { id: 'u1', role: 'user', content: 'plan something', timestamp: '2026-05-04T00:00:00Z' },
        {
          id: 'd1',
          role: 'delegation_started',
          content: '',
          timestamp: '2026-05-04T00:00:01Z',
          chainId: 'chain-1',
          targetAgent: 'executor',
          status: 'running',
        },
      ]

      store.applyDelegationEvent(
        JSON.stringify({ chain_id: 'chain-1', tool_calls: 3, last_tool: 'bash' }),
      )

      const delegations = store.messages.filter((m) => m.role === 'delegation_started')
      expect(delegations).toHaveLength(1)
      expect(delegations[0].toolCalls).toBe(3)
      expect(delegations[0].lastTool).toBe('bash')
    })
  })

  // ── applyContentEvent — delegation routing ────────────────────────────────

  describe('applyContentEvent — delegation routing', () => {
    it('routes a type:delegation payload to applyDelegationEvent', () => {
      const store = useChatStore()
      const spy = vi.spyOn(store, 'applyDelegationEvent')
      store.messages = []

      store.applyContentEvent(
        JSON.stringify({
          type: 'delegation',
          target_agent: 'executor',
          chain_id: 'chain-1',
          status: 'started',
        }),
      )

      expect(spy).toHaveBeenCalledOnce()
    })

    it('creates a delegation_started message in the message list when a type:delegation event arrives', () => {
      const store = useChatStore()
      store.messages = [
        { id: 'u1', role: 'user', content: 'plan something', timestamp: '2026-05-04T00:00:00Z' },
      ]

      store.applyContentEvent(
        JSON.stringify({
          type: 'delegation',
          target_agent: 'executor',
          chain_id: 'chain-1',
          status: 'started',
        }),
      )

      const delegation = store.messages.find((m) => m.role === 'delegation_started')
      expect(delegation).toBeDefined()
      expect(delegation?.targetAgent).toBe('executor')
    })
  })

  // ── applyContentEvent — discriminated union dispatch (Principal F5/F6) ───
  //
  // Pre-this-PR the dispatch was an untyped `Record<string, unknown>`
  // switch with a structural-fallback for delegation events that lacked
  // the `type` discriminant — bug-for-bug compatibility with an older
  // emitter. The Go side now ALWAYS tags delegation events with
  // `type: 'delegation'` (writeSSEDelegationInfo injects the field even
  // when wrapping a provider DelegationInfo), so the structural fallback
  // is dead code. The new dispatch uses the SSEEvent discriminated union
  // and an exhaustive switch — adding a new event type without a handler
  // fails compile.

  describe('applyContentEvent — discriminated union dispatch', () => {
    it('routes a type:tool_call payload to handleToolCallEvent', () => {
      const store = useChatStore()
      const spy = vi.spyOn(store, 'handleToolCallEvent')
      store.applyContentEvent(
        JSON.stringify({ type: 'tool_call', name: 'bash', status: 'running', input: 'ls' }),
      )
      expect(spy).toHaveBeenCalledOnce()
      expect(spy).toHaveBeenCalledWith({ name: 'bash', status: 'running', input: 'ls' })
    })

    it('routes a type:tool_result payload to handleToolResultEvent', () => {
      const store = useChatStore()
      const spy = vi.spyOn(store, 'handleToolResultEvent')
      store.applyContentEvent(JSON.stringify({ type: 'tool_result', content: 'output' }))
      expect(spy).toHaveBeenCalledOnce()
      expect(spy).toHaveBeenCalledWith({ content: 'output' })
    })

    it('routes a type:skill_load payload to handleToolCallEvent with status running', () => {
      const store = useChatStore()
      const spy = vi.spyOn(store, 'handleToolCallEvent')
      store.applyContentEvent(JSON.stringify({ type: 'skill_load', name: 'pre-action' }))
      expect(spy).toHaveBeenCalledWith({ name: 'pre-action', status: 'running' })
    })

    it('routes an untyped content chunk to handleContentChunk', () => {
      const store = useChatStore()
      const spy = vi.spyOn(store, 'handleContentChunk')
      store.applyContentEvent(JSON.stringify({ content: 'hello' }))
      expect(spy).toHaveBeenCalledWith({ content: 'hello' })
    })

    it('sets store.error when an untyped error event arrives', () => {
      const store = useChatStore()
      store.applyContentEvent(JSON.stringify({ error: 'something broke' }))
      expect(store.error).toBe('something broke')
    })

    it('seals the in-flight assistant and clears isStreaming on [DONE]', () => {
      const store = useChatStore()
      store.messages = [
        { id: 'u1', role: 'user', content: 'go', timestamp: '' },
        { id: 'a1', role: 'assistant', content: 'partial', timestamp: '', status: 'running' },
      ]
      store.isStreaming = true
      store.applyContentEvent('[DONE]')
      expect(store.messages.find((m) => m.id === 'a1')?.status).toBe('completed')
      expect(store.isStreaming).toBe(false)
    })

    it('does NOT route an untyped delegation-shaped payload (structural fallback removed)', () => {
      // Regression guard for Principal F6. The Go emitter always tags
      // delegation events with `type: 'delegation'`. A payload that lacks
      // the discriminant but happens to carry target_agent/chain_id is
      // either a bug on the emitter side or a stray test fixture — either
      // way it must not silently route to applyDelegationEvent. This spec
      // pins the removal of the bug-for-bug fallback at chatStore.ts:945.
      const store = useChatStore()
      const spy = vi.spyOn(store, 'applyDelegationEvent')
      store.applyContentEvent(
        JSON.stringify({ target_agent: 'executor', chain_id: 'chain-1', status: 'started' }),
      )
      expect(spy).not.toHaveBeenCalled()
    })

    it('silently logs unknown events to streamLog without affecting state', () => {
      const store = useChatStore()
      const before = store.messages.length
      // Payload has no recognised type and no content/error — must not
      // touch messages or error.
      store.applyContentEvent(JSON.stringify({ foo: 'bar' }))
      expect(store.messages.length).toBe(before)
      expect(store.error).toBeNull()
    })

    it('silently logs malformed payloads (non-JSON) to streamLog without throwing', () => {
      const store = useChatStore()
      const before = store.messages.length
      expect(() => store.applyContentEvent('not json {')).not.toThrow()
      expect(store.messages.length).toBe(before)
      expect(store.error).toBeNull()
    })
  })

})
