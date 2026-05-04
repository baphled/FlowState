import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useChatStore } from './chatStore'

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

})
