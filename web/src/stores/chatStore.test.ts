import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

function installLocalStorageStub(): void {
  const store = new Map<string, string>()
  const stub = {
    getItem: (key: string) => (store.has(key) ? store.get(key)! : null),
    setItem: (key: string, value: string) => {
      store.set(key, String(value))
    },
    removeItem: (key: string) => {
      store.delete(key)
    },
    clear: () => store.clear(),
    key: (i: number) => Array.from(store.keys())[i] ?? null,
    get length() {
      return store.size
    },
  }
  Object.defineProperty(window, 'localStorage', { value: stub, configurable: true })
}
import {
  createSession,
  fetchAgents,
  fetchSessionMessages,
  fetchSessions,
  sendSessionMessage,
  subscribeSessionStream,
  updateSessionAgent,
} from '../api'
import { useChatStore } from './chatStore'

class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  listeners: Record<string, (event: MessageEvent) => void> = {}
  closed = false

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }

  addEventListener(type: string, fn: (event: MessageEvent) => void): void {
    this.listeners[type] = fn
  }

  removeEventListener(type: string): void {
    delete this.listeners[type]
  }

  close(): void {
    this.closed = true
  }

  fire(type: string, data: unknown): void {
    const fn = this.listeners[type]
    if (fn) {
      fn({ data: JSON.stringify(data) } as MessageEvent)
    }
  }
}

vi.mock('../api', () => ({
  fetchAgents: vi.fn(() => Promise.resolve([
    { id: 'agent-1', name: 'Agent One' },
    { id: 'agent-2', name: 'Agent Two' },
  ])),
  fetchSessions: vi.fn(() => Promise.resolve([
    { id: 'session-1', agentId: 'agent-1', title: 'Session 1', createdAt: '', updatedAt: '', messageCount: 0 },
    { id: 'session-2', agentId: 'agent-2', title: 'Session 2', createdAt: '', updatedAt: '', messageCount: 0 },
  ])),
  fetchSessionMessages: vi.fn((sessionId: string) => Promise.resolve([
    { id: 'msg-1', sessionId, content: 'Hello', sender: 'user' },
    { id: 'msg-2', sessionId, content: 'Hi', sender: 'agent' },
  ])),
  createSession: vi.fn((agentId: string) => Promise.resolve({
    id: 'session-new',
    agentId,
    messages: [],
    messageCount: 0,
    createdAt: '',
    updatedAt: '',
  })),
  sendSessionMessage: vi.fn((sessionId: string, content: string) => Promise.resolve({
    id: sessionId,
    agentId: 'agent-1',
    messages: [{ id: 'msg-x', sessionId, content, sender: 'user' }],
    messageCount: 1,
    createdAt: '',
    updatedAt: '',
  })),
  updateSessionAgent: vi.fn((sessionId: string, agentId: string) => Promise.resolve({
    id: sessionId,
    agentId,
    messages: [],
    messageCount: 0,
    createdAt: '',
    updatedAt: '',
  })),
  subscribeSessionStream: vi.fn((sessionId: string) => new FakeEventSource(`/api/v1/sessions/${sessionId}/stream`)),
}))

describe('chatStore - restoreStateFromBackend', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('restores agent, session, and messages from backend', async () => {
    const store = useChatStore()
    await expect(store.restoreStateFromBackend()).resolves.not.toThrow()
    expect(vi.mocked(fetchAgents)).toHaveBeenCalledTimes(1)
    expect(vi.mocked(fetchSessions)).toHaveBeenCalledTimes(1)
    expect(vi.mocked(fetchSessionMessages)).toHaveBeenCalledWith('session-1')
    expect(store.availableAgents.length).toBeGreaterThan(0)
    expect(store.sessions.length).toBeGreaterThan(0)
    expect(store.currentSessionId).toBe(store.sessions[0].id)
    expect(store.agentId).toBe(store.sessions[0].agentId)
    expect(store.messages.map((message) => message.content)).toEqual(['Hello', 'Hi'])
  })
})

describe('chatStore - sendMessage', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('refreshes the session summary list after sending so messageCount stays current', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'

    await store.sendMessage('Hello world')

    expect(vi.mocked(sendSessionMessage)).toHaveBeenCalledWith('session-1', 'Hello world')
    expect(vi.mocked(fetchSessions)).toHaveBeenCalled()
    expect(vi.mocked(fetchSessionMessages)).toHaveBeenCalledWith('session-1')
  })

  it('creates a session, sends, and refreshes summaries when no session is active', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = null

    await store.sendMessage('Hi')

    expect(vi.mocked(createSession)).toHaveBeenCalledWith('agent-1')
    expect(store.currentSessionId).toBe('session-new')
    expect(vi.mocked(sendSessionMessage)).toHaveBeenCalledWith('session-new', 'Hi')
    expect(vi.mocked(fetchSessions)).toHaveBeenCalled()
  })

  it('subscribes to the session SSE stream and applies delegation events in-place to the in-flight assistant message', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = [
      { id: 'msg-pending', role: 'assistant', content: '', timestamp: '', status: 'pending' },
    ]

    FakeEventSource.instances.length = 0

    let resolveSend: (value: any) => void = () => {}
    vi.mocked(sendSessionMessage).mockImplementationOnce(
      () =>
        new Promise<any>((resolve) => {
          resolveSend = resolve
        }),
    )

    const sendPromise = store.sendMessage('long task that delegates')

    await Promise.resolve()
    await Promise.resolve()

    expect(vi.mocked(subscribeSessionStream)).toHaveBeenCalledWith('session-1')
    expect(FakeEventSource.instances.length).toBe(1)

    const es = FakeEventSource.instances[0]
    es.fire('delegation', {
      chain_id: 'chain-xyz',
      target_agent: 'researcher',
      tool_calls: 4,
      last_tool: 'web_search',
      status: 'in_progress',
    })

    const updated = store.messages.find((m) => m.status !== 'completed')
    expect(updated?.targetAgent).toBe('researcher')
    expect(updated?.chainId).toBe('chain-xyz')
    expect(updated?.toolCalls).toBe(4)
    expect(updated?.lastTool).toBe('web_search')
    expect(updated?.status).toBe('in_progress')

    resolveSend({
      id: 'session-1',
      agentId: 'agent-1',
      messages: [],
      messageCount: 0,
      createdAt: '',
      updatedAt: '',
    })
    await sendPromise

    expect(es.closed).toBe(true)
  })
})

describe('chatStore - setAgent', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('PATCHes the backend when an active session exists and the agent changes', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'

    await store.setAgent('agent-2')

    expect(vi.mocked(updateSessionAgent)).toHaveBeenCalledWith('session-1', 'agent-2')
    expect(store.agentId).toBe('agent-2')
  })

  it('does not PATCH when no session is active', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = null

    await store.setAgent('agent-2')

    expect(vi.mocked(updateSessionAgent)).not.toHaveBeenCalled()
    expect(store.agentId).toBe('agent-2')
  })

  it('does not PATCH when the agent is unchanged', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'

    await store.setAgent('agent-1')

    expect(vi.mocked(updateSessionAgent)).not.toHaveBeenCalled()
  })
})
