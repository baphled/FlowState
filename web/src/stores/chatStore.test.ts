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
  fetchModels,
  fetchSessionMessages,
  fetchSessions,
  sendSessionMessage,
  subscribeSessionStream,
  updateSessionAgent,
  updateSessionModel,
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
      fn({ data: typeof data === 'string' ? data : JSON.stringify(data) } as MessageEvent)
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
  updateSessionModel: vi.fn((sessionId: string, modelId: string, providerId: string) => Promise.resolve({
    id: sessionId,
    agentId: 'agent-1',
    currentModelId: modelId,
    currentProviderId: providerId,
    messages: [],
    messageCount: 0,
    createdAt: '',
    updatedAt: '',
  })),
  fetchModels: vi.fn(() => Promise.resolve([
    { id: 'claude-opus', name: 'Claude Opus', providerId: 'anthropic' },
    { id: 'gpt-4o', name: 'GPT-4o', providerId: 'openai' },
  ])),
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

  it('prefers currentAgentId from the backend so the user\'s last-selected agent is restored', async () => {
    window.localStorage.setItem('chat.currentSessionId', 'session-1')
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'session-1',
        agentId: 'agent-1',
        currentAgentId: 'agent-2',
        title: 'Session 1',
        createdAt: '',
        updatedAt: '',
        messageCount: 0,
      },
    ])

    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(store.currentSessionId).toBe('session-1')
    expect(store.agentId).toBe('agent-2')
  })

  it('falls back to agentId when currentAgentId is absent', async () => {
    window.localStorage.setItem('chat.currentSessionId', 'session-1')

    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(store.agentId).toBe('agent-1')
  })
})

describe('chatStore - loadSessionMessages', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('switches to currentAgentId when loading a session whose last-selected agent differs from the active one', async () => {
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'session-1',
        agentId: 'agent-1',
        currentAgentId: 'agent-2',
        title: 'Session 1',
        createdAt: '',
        updatedAt: '',
        messageCount: 0,
      },
    ])

    const store = useChatStore()
    await store.loadSessions()
    store.agentId = 'agent-1'
    store.currentSessionId = 'other'

    await store.loadSessionMessages('session-1')

    expect(store.agentId).toBe('agent-2')
    expect(vi.mocked(updateSessionAgent)).toHaveBeenCalledWith('other', 'agent-2')
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
    es.fire('message', {
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

  it('pushes an optimistic user message to messages before the API responds', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    FakeEventSource.instances.length = 0

    let resolveSend: (value: any) => void = () => {}
    vi.mocked(sendSessionMessage).mockImplementationOnce(
      () =>
        new Promise<any>((resolve) => {
          resolveSend = resolve
        }),
    )

    const sendPromise = store.sendMessage('hello now')

    await Promise.resolve()
    await Promise.resolve()

    const optimistic = store.messages.find((m) => m.role === 'user' && m.content === 'hello now')
    expect(optimistic).toBeDefined()
    expect(optimistic!.id).toMatch(/^temp-/)

    resolveSend({
      id: 'session-1',
      agentId: 'agent-1',
      messages: [],
      messageCount: 0,
      createdAt: '',
      updatedAt: '',
    })
    await sendPromise
  })

  it('appends progressive content chunks from default SSE message events to the in-flight assistant message', async () => {
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

    const sendPromise = store.sendMessage('stream me')

    await Promise.resolve()
    await Promise.resolve()

    expect(FakeEventSource.instances.length).toBe(1)
    const es = FakeEventSource.instances[0]

    es.fire('message', { content: 'hel' })
    es.fire('message', { content: 'lo ' })
    es.fire('message', { content: 'world' })

    const target = store.messages.find((m) => m.status !== 'completed')
    expect(target?.content).toBe('hello world')

    resolveSend({
      id: 'session-1',
      agentId: 'agent-1',
      messages: [],
      messageCount: 0,
      createdAt: '',
      updatedAt: '',
    })
    await sendPromise
  })

  it('creates an assistant message on the first content chunk when none exists', () => {
    const store = useChatStore()

    store.applyContentEvent(JSON.stringify({ content: 'hello' }))

    expect(store.messages).toHaveLength(1)
    expect(store.messages[0].role).toBe('assistant')
    expect(store.messages[0].content).toBe('hello')
    expect(store.isStreaming).toBe(true)
  })

  it('appends content chunks to an existing in-flight assistant message', () => {
    const store = useChatStore()
    store.messages = [
      { id: 'assistant-1', role: 'assistant', content: 'hel', timestamp: new Date().toISOString() },
    ]

    store.applyContentEvent(JSON.stringify({ content: 'lo' }))
    store.applyContentEvent(JSON.stringify({ content: ' world' }))

    expect(store.messages).toHaveLength(1)
    expect(store.messages[0].content).toBe('hello world')
    expect(store.isStreaming).toBe(true)
  })

  it('creates a running tool_result message for tool_call events', () => {
    const store = useChatStore()

    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'bash', status: 'running' }))

    expect(store.messages).toHaveLength(1)
    expect(store.messages[0].role).toBe('tool_result')
    expect(store.messages[0].toolName).toBe('bash')
    expect(store.messages[0].status).toBe('running')
    expect(store.messages[0].content).toBe('')
  })

  it('updates the most recent running tool_result when a tool_result event arrives', () => {
    const store = useChatStore()
    store.messages = [
      {
        id: 'tool-1',
        role: 'tool_result',
        toolName: 'read',
        content: '',
        timestamp: new Date().toISOString(),
        status: 'completed',
      },
      {
        id: 'tool-2',
        role: 'tool_result',
        toolName: 'bash',
        content: '',
        timestamp: new Date().toISOString(),
        status: 'running',
      },
    ]

    store.applyContentEvent(JSON.stringify({ type: 'tool_result', content: 'done' }))

    expect(store.messages[1].content).toBe('done')
    expect(store.messages[1].status).toBe('completed')
  })

  it('creates a running tool_result message for skill_load events', () => {
    const store = useChatStore()

    store.applyContentEvent(JSON.stringify({ type: 'skill_load', name: 'bdd-workflow' }))

    expect(store.messages).toHaveLength(1)
    expect(store.messages[0].role).toBe('tool_result')
    expect(store.messages[0].toolName).toBe('bdd-workflow')
    expect(store.messages[0].status).toBe('running')
  })

  it('sets the store error when an SSE error event arrives', () => {
    const store = useChatStore()

    store.applyContentEvent(JSON.stringify({ error: 'backend exploded' }))

    expect(store.error).toBe('backend exploded')
  })

  it('marks streaming as finished when the DONE sentinel arrives', () => {
    const store = useChatStore()
    store.isStreaming = true

    store.applyContentEvent('[DONE]')

    expect(store.isStreaming).toBe(false)
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

describe('chatStore - model restoration on restoreStateFromBackend', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('adopts session.currentModelId and currentProviderId when present on the restored session', async () => {
    window.localStorage.setItem('chat.currentSessionId', 'session-1')
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'session-1',
        agentId: 'agent-1',
        currentModelId: 'claude-opus',
        currentProviderId: 'anthropic',
        title: 'Session 1',
        createdAt: '',
        updatedAt: '',
        messageCount: 0,
      },
    ])

    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(store.currentModelId).toBe('claude-opus')
    expect(store.currentProviderId).toBe('anthropic')
  })
})

describe('chatStore - model restoration on loadSessionMessages', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('adopts session.currentModelId and currentProviderId when loading a session', async () => {
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'session-1',
        agentId: 'agent-1',
        currentModelId: 'gpt-4o',
        currentProviderId: 'openai',
        title: 'Session 1',
        createdAt: '',
        updatedAt: '',
        messageCount: 0,
      },
    ])

    const store = useChatStore()
    await store.loadSessions()
    store.currentModelId = ''
    store.currentProviderId = ''

    await store.loadSessionMessages('session-1')

    expect(store.currentModelId).toBe('gpt-4o')
    expect(store.currentProviderId).toBe('openai')
  })
})

describe('chatStore - setModel', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('PATCHes the backend with modelId and providerId when an active session exists and the model changes', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.currentModelId = 'claude-opus'
    store.currentProviderId = 'anthropic'

    await store.setModel('gpt-4o', 'openai')

    expect(vi.mocked(updateSessionModel)).toHaveBeenCalledWith('session-1', 'gpt-4o', 'openai')
    expect(store.currentModelId).toBe('gpt-4o')
    expect(store.currentProviderId).toBe('openai')
  })

  it('does not PATCH when no session is active', async () => {
    const store = useChatStore()
    store.currentSessionId = null

    await store.setModel('gpt-4o', 'openai')

    expect(vi.mocked(updateSessionModel)).not.toHaveBeenCalled()
    expect(store.currentModelId).toBe('gpt-4o')
    expect(store.currentProviderId).toBe('openai')
  })

  it('does not PATCH when the model and provider are unchanged', async () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    store.currentModelId = 'claude-opus'
    store.currentProviderId = 'anthropic'

    await store.setModel('claude-opus', 'anthropic')

    expect(vi.mocked(updateSessionModel)).not.toHaveBeenCalled()
  })
})

describe('chatStore - loadModels', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('populates availableModels via fetchModels', async () => {
    const store = useChatStore()

    await store.loadModels()

    expect(vi.mocked(fetchModels)).toHaveBeenCalledTimes(1)
    expect(store.availableModels).toEqual([
      { id: 'claude-opus', name: 'Claude Opus', providerId: 'anthropic' },
      { id: 'gpt-4o', name: 'GPT-4o', providerId: 'openai' },
    ])
  })
})
