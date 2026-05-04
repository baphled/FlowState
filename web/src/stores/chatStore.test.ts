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
  truncateSessionMessages,
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
  truncateSessionMessages: vi.fn((_sessionId: string, _messageId: string) => Promise.resolve()),
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

  it('seals all backend-loaded assistant messages to status completed so they cannot be streaming targets', async () => {
    // Backend history returns assistant rows with no status field. Without
    // sealing, handleContentChunk's status === 'running' guard would create
    // a new placeholder on every chunk — but before the fix, the old guard
    // (status !== 'completed') would adopt the unseen assistant instead.
    // Sealing to 'completed' here ensures no backend row is ever a target.
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'session-1',
        agentId: 'agent-1',
        title: 'Session 1',
        createdAt: '',
        updatedAt: '',
        messageCount: 2,
      },
    ])
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'u1', role: 'user', content: 'hello', timestamp: '' },
      { id: 'a1', role: 'assistant', content: 'hi', timestamp: '' },
    ])

    const store = useChatStore()
    await store.loadSessions()
    await store.loadSessionMessages('session-1')

    const assistant = store.messages.find((m) => m.id === 'a1')
    expect(assistant?.status).toBe('completed')
  })

  it('switches to currentAgentId when loading a session whose last-selected agent differs from the active one', async () => {
    // The PATCH MUST target the freshly-selected session, not the
    // previously-active one. Selecting Session B while Session A is
    // active should only ever update Session B's agent.
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
    expect(vi.mocked(updateSessionAgent)).toHaveBeenCalledWith('session-1', 'agent-2')
    expect(vi.mocked(updateSessionAgent)).not.toHaveBeenCalledWith('other', 'agent-2')
    expect(store.currentSessionId).toBe('session-1')
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
  })

  it('does NOT call fetchSessionMessages during sendMessage to prevent mid-stream message replacement', async () => {
    // Regression guard: fetching and replacing this.messages while SSE
    // is streaming injects backend-loaded assistant rows (status ===
    // undefined) that handleContentChunk then adopts as streaming
    // targets, causing the previous response to appear above the new
    // user prompt. The SSE stream is the source of truth during a send.
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

    const sendPromise = store.sendMessage('test no reload')

    await Promise.resolve()
    await Promise.resolve()

    resolveSend({
      id: 'session-1',
      agentId: 'agent-1',
      messages: [],
      messageCount: 0,
      createdAt: '',
      updatedAt: '',
    })
    await sendPromise

    expect(vi.mocked(fetchSessionMessages)).not.toHaveBeenCalled()
  })

  it('targets the just-selected session on subsequent sendMessage calls instead of forking a new session', async () => {
    // Reproduces the user-reported regression: after selecting an existing
    // session from the switcher dropdown, the next sendMessage should land
    // on that session — not fork a new one. The selection path runs
    // loadSessionMessages, which switches the agent via setAgent. setAgent
    // must not clear currentSessionId, and sendMessage must observe the
    // selected session id.
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'session-A',
        agentId: 'agent-1',
        title: 'Session A',
        createdAt: '',
        updatedAt: '',
        messageCount: 1,
      },
      {
        id: 'session-B',
        agentId: 'agent-2',
        title: 'Session B',
        createdAt: '',
        updatedAt: '',
        messageCount: 2,
      },
    ])

    const store = useChatStore()
    await store.loadSessions()

    // Mimic the SessionSwitcher pre-set then loadSessionMessages call
    // that selectSession performs.
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-A'
    await store.loadSessionMessages('session-B')

    vi.mocked(sendSessionMessage).mockClear()
    vi.mocked(createSession).mockClear()

    await store.sendMessage('after switch')

    expect(vi.mocked(createSession)).not.toHaveBeenCalled()
    expect(vi.mocked(sendSessionMessage)).toHaveBeenCalledWith('session-B', 'after switch')
    expect(store.currentSessionId).toBe('session-B')
  })

  it('PATCHes the newly-selected session\'s agent — not the previously active session — when loadSessionMessages switches agents', async () => {
    // The setAgent call inside loadSessionMessages used to read the stale
    // currentSessionId (still pointing at the previous session) and PATCH
    // the wrong session. Selecting a session must update only the
    // session that was selected.
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'session-target',
        agentId: 'agent-1',
        currentAgentId: 'agent-2',
        title: 'Target Session',
        createdAt: '',
        updatedAt: '',
        messageCount: 0,
      },
    ])

    const store = useChatStore()
    await store.loadSessions()
    store.agentId = 'agent-1'
    store.currentSessionId = 'previous-session'

    await store.loadSessionMessages('session-target')

    expect(vi.mocked(updateSessionAgent)).toHaveBeenCalledWith('session-target', 'agent-2')
    expect(vi.mocked(updateSessionAgent)).not.toHaveBeenCalledWith('previous-session', 'agent-2')
    expect(store.currentSessionId).toBe('session-target')
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
    // The in-flight streaming target is identified by status === 'running'.
    // Backend-loaded rows (status === undefined) must NOT be matched —
    // see "Session message upsert collision" bug-fix note.
    store.messages = [
      { id: 'msg-running', role: 'assistant', content: '', timestamp: '', status: 'running' },
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
    // The streaming target is identified by status === 'running'.
    store.messages = [
      { id: 'msg-running', role: 'assistant', content: '', timestamp: '', status: 'running' },
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

  it('appends content chunks to an existing in-flight assistant message (status === running)', () => {
    const store = useChatStore()
    // The in-flight target is explicitly status === 'running'. A bare
    // assistant row with no status (e.g. backend-canonical history) must
    // NOT be a target — see "Session message upsert collision" bug-fix
    // note for why this matters across multiple turns.
    store.messages = [
      {
        id: 'assistant-1',
        role: 'assistant',
        content: 'hel',
        timestamp: new Date().toISOString(),
        status: 'running',
      },
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

// Session hierarchy navigation — these getters back the keyboard nav layer
// (Up to parent, Left/Right siblings, Ctrl+X Down to last delegated child).
// Sibling order: ascending by createdAt.
// "Last delegated" = most-recent child of the *current* session, by createdAt.
describe('chatStore - hierarchy getters', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  function summary(id: string, parentId: string | undefined, createdAt: string) {
    return {
      id,
      agentId: 'agent-x',
      title: id,
      parentId,
      createdAt,
      updatedAt: createdAt,
      messageCount: 0,
    }
  }

  it('exposes currentSession derived from currentSessionId', () => {
    const store = useChatStore()
    store.sessions = [
      summary('parent-1', undefined, '2026-01-01T00:00:00Z'),
      summary('child-1', 'parent-1', '2026-01-01T00:01:00Z'),
    ]
    store.currentSessionId = 'child-1'

    expect(store.currentSession?.id).toBe('child-1')
    expect(store.currentSession?.parentId).toBe('parent-1')
  })

  it('returns undefined currentSession when no session is selected', () => {
    const store = useChatStore()
    store.sessions = [summary('parent-1', undefined, '2026-01-01T00:00:00Z')]
    store.currentSessionId = null

    expect(store.currentSession).toBeUndefined()
  })

  it('lastDelegatedSessionId returns the most-recent child of the active session', () => {
    const store = useChatStore()
    store.sessions = [
      summary('parent-1', undefined, '2026-01-01T00:00:00Z'),
      summary('child-a', 'parent-1', '2026-01-01T00:01:00Z'),
      summary('child-b', 'parent-1', '2026-01-01T00:03:00Z'),
      summary('child-c', 'parent-1', '2026-01-01T00:02:00Z'),
      summary('unrelated', 'parent-2', '2026-01-01T00:99:00Z'),
    ]
    store.currentSessionId = 'parent-1'

    expect(store.lastDelegatedSessionId).toBe('child-b')
  })

  it('lastDelegatedSessionId returns null when the current session has no children', () => {
    const store = useChatStore()
    store.sessions = [summary('lonely', undefined, '2026-01-01T00:00:00Z')]
    store.currentSessionId = 'lonely'

    expect(store.lastDelegatedSessionId).toBeNull()
  })

  it('lastDelegatedSessionId returns null when no session is active', () => {
    const store = useChatStore()
    store.sessions = [
      summary('parent-1', undefined, '2026-01-01T00:00:00Z'),
      summary('child-a', 'parent-1', '2026-01-01T00:01:00Z'),
    ]
    store.currentSessionId = null

    expect(store.lastDelegatedSessionId).toBeNull()
  })

  it('parentSessionId returns the parent of the active child session', () => {
    const store = useChatStore()
    store.sessions = [
      summary('parent-1', undefined, '2026-01-01T00:00:00Z'),
      summary('child-a', 'parent-1', '2026-01-01T00:01:00Z'),
    ]
    store.currentSessionId = 'child-a'

    expect(store.parentSessionId).toBe('parent-1')
  })

  it('parentSessionId returns null when the active session is a parent (no parentId)', () => {
    const store = useChatStore()
    store.sessions = [summary('parent-1', undefined, '2026-01-01T00:00:00Z')]
    store.currentSessionId = 'parent-1'

    expect(store.parentSessionId).toBeNull()
  })

  it('siblingSessionIds returns siblings of the current child session in createdAt order', () => {
    const store = useChatStore()
    store.sessions = [
      summary('parent-1', undefined, '2026-01-01T00:00:00Z'),
      summary('child-c', 'parent-1', '2026-01-01T00:03:00Z'),
      summary('child-a', 'parent-1', '2026-01-01T00:01:00Z'),
      summary('child-b', 'parent-1', '2026-01-01T00:02:00Z'),
      summary('unrelated', 'parent-2', '2026-01-01T00:00:00Z'),
    ]
    store.currentSessionId = 'child-b'

    expect(store.siblingSessionIds).toEqual(['child-a', 'child-b', 'child-c'])
  })

  it('siblingSessionIds is empty when the active session is a parent', () => {
    const store = useChatStore()
    store.sessions = [
      summary('parent-1', undefined, '2026-01-01T00:00:00Z'),
      summary('child-a', 'parent-1', '2026-01-01T00:01:00Z'),
    ]
    store.currentSessionId = 'parent-1'

    expect(store.siblingSessionIds).toEqual([])
  })

  it('previousSiblingSessionId returns the prior sibling and clamps at the start', () => {
    const store = useChatStore()
    store.sessions = [
      summary('parent-1', undefined, '2026-01-01T00:00:00Z'),
      summary('child-a', 'parent-1', '2026-01-01T00:01:00Z'),
      summary('child-b', 'parent-1', '2026-01-01T00:02:00Z'),
      summary('child-c', 'parent-1', '2026-01-01T00:03:00Z'),
    ]

    store.currentSessionId = 'child-b'
    expect(store.previousSiblingSessionId).toBe('child-a')

    store.currentSessionId = 'child-a'
    expect(store.previousSiblingSessionId).toBeNull()
  })

  it('nextSiblingSessionId returns the next sibling and clamps at the end', () => {
    const store = useChatStore()
    store.sessions = [
      summary('parent-1', undefined, '2026-01-01T00:00:00Z'),
      summary('child-a', 'parent-1', '2026-01-01T00:01:00Z'),
      summary('child-b', 'parent-1', '2026-01-01T00:02:00Z'),
      summary('child-c', 'parent-1', '2026-01-01T00:03:00Z'),
    ]

    store.currentSessionId = 'child-b'
    expect(store.nextSiblingSessionId).toBe('child-c')

    store.currentSessionId = 'child-c'
    expect(store.nextSiblingSessionId).toBeNull()
  })

  it('previous/next sibling getters are null when there is only one child', () => {
    const store = useChatStore()
    store.sessions = [
      summary('parent-1', undefined, '2026-01-01T00:00:00Z'),
      summary('only-child', 'parent-1', '2026-01-01T00:01:00Z'),
    ]
    store.currentSessionId = 'only-child'

    expect(store.previousSiblingSessionId).toBeNull()
    expect(store.nextSiblingSessionId).toBeNull()
  })
})

describe('chatStore - multi-turn streaming (regression: prior assistant must not absorb next turn\'s chunks)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  // The user-reported symptom: "the session keeps upserting the last agent
  // response, instead of updating with the new response to the last user
  // prompt. Reloading the page resolves this."
  //
  // Root cause: handleContentChunk treats any assistant message with
  // status !== 'completed' as the in-flight target. After turn N completes,
  // sendMessage replaces this.messages with backend-canonical history whose
  // assistant rows have status === undefined (the backend has no notion of a
  // streaming "running" state). Turn N+1's first chunk reverse-finds the
  // previous turn's assistant (undefined !== 'completed' → true) and
  // overwrites it. The user watches the prior response mutate into the new
  // one in real time. Reloading the page fetches canonical history afresh,
  // which restores both messages.
  //
  // The contract being pinned: backend-loaded history must NEVER be a
  // streaming target. Only an assistant message that was created by the
  // current SSE stream (status === 'running') is a valid target. Once a
  // chunk stream ends ([DONE] sentinel or sendMessage refetch), the prior
  // assistant must be sealed so subsequent chunks cannot land on it.

  it('does not append a second turn\'s chunks onto the previous turn\'s assistant message', () => {
    const store = useChatStore()

    // Simulate state at the start of turn 2: history has been refetched
    // from the backend after turn 1, so the prior assistant carries no
    // status (backend canonical shape).
    store.messages = [
      { id: 'user-1', role: 'user', content: 'first prompt', timestamp: '2026-05-04T00:00:00Z' },
      { id: 'assistant-1', role: 'assistant', content: 'first response', timestamp: '2026-05-04T00:00:01Z' },
      { id: 'user-2-optimistic', role: 'user', content: 'second prompt', timestamp: '2026-05-04T00:00:02Z' },
    ]

    // Turn 2's first chunk arrives. It must land on a NEW assistant
    // message, not splice into 'first response'.
    store.applyContentEvent(JSON.stringify({ content: 'second' }))
    store.applyContentEvent(JSON.stringify({ content: ' response' }))

    // The first response remains untouched.
    const first = store.messages.find((m) => m.id === 'assistant-1')
    expect(first?.content).toBe('first response')

    // A new assistant message exists for turn 2.
    const assistantMessages = store.messages.filter((m) => m.role === 'assistant')
    expect(assistantMessages).toHaveLength(2)
    expect(assistantMessages[1].content).toBe('second response')
    expect(assistantMessages[1].id).not.toBe('assistant-1')
  })

  it('seals the in-flight assistant on [DONE] so chunks from a subsequent turn create a new message', () => {
    const store = useChatStore()
    store.messages = []

    // Turn 1 streams.
    store.applyContentEvent(JSON.stringify({ content: 'turn one' }))
    store.applyContentEvent('[DONE]')

    // Turn 2 streams. First chunk must NOT append onto turn 1's message.
    store.applyContentEvent(JSON.stringify({ content: 'turn two' }))

    const assistants = store.messages.filter((m) => m.role === 'assistant')
    expect(assistants).toHaveLength(2)
    expect(assistants[0].content).toBe('turn one')
    expect(assistants[1].content).toBe('turn two')
  })

  it('preserves prior assistant content across two full sendMessage cycles with realistic chunked SSE', async () => {
    // Post-PR2: sendMessage's [DONE] handler now reconciles with the backend
    // (replacing the pre-fix gated refetch). The key regression still being
    // pinned: turn 2's chunks must land on a NEW assistant bubble, not on
    // the sealed turn 1 assistant. We program the backend mock to return
    // the canonical post-turn-1 history before turn 1's [DONE], and the
    // canonical post-turn-2 history before turn 2's [DONE]; the test
    // asserts the user-observable outcome (two distinct assistant bubbles)
    // rather than implementation calls.
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    FakeEventSource.instances.length = 0

    // Turn 1: stream chunks then [DONE] seals the assistant.
    let resolveSend1: (v: any) => void = () => {}
    vi.mocked(sendSessionMessage).mockImplementationOnce(
      () =>
        new Promise<any>((resolve) => {
          resolveSend1 = resolve
        }),
    )
    // After turn 1's [DONE] the reconcile reads canonical history with one
    // user + one (sealed) assistant.
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'srv-u1', role: 'user', content: 'first prompt', timestamp: '' },
      { id: 'srv-a1', role: 'assistant', content: 'first response', timestamp: '' },
    ])

    const send1 = store.sendMessage('first prompt')
    await Promise.resolve(); await Promise.resolve()
    const es1 = FakeEventSource.instances[0]
    es1.fire('message', { content: 'first ' })
    es1.fire('message', { content: 'response' })
    es1.fire('message', '[DONE]')
    resolveSend1({ id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0, createdAt: '', updatedAt: '' })
    await send1
    // Flush the reconcile microtask chain (await fetchSessionMessages).
    await Promise.resolve(); await Promise.resolve()

    // After turn 1, the store holds the canonical history (sealed assistant).
    const assistants1 = store.messages.filter((m) => m.role === 'assistant')
    expect(assistants1).toHaveLength(1)
    expect(assistants1[0].content).toBe('first response')
    expect(assistants1[0].status).toBe('completed')

    // Turn 2: a new user optimistic message is pushed, then chunks arrive.
    // The sealed turn-1 assistant must not absorb turn-2 chunks.
    let resolveSend2: (v: any) => void = () => {}
    vi.mocked(sendSessionMessage).mockImplementationOnce(
      () =>
        new Promise<any>((resolve) => {
          resolveSend2 = resolve
        }),
    )
    // After turn 2's [DONE] the reconcile reads canonical history with two
    // user + two (sealed) assistants.
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'srv-u1', role: 'user', content: 'first prompt', timestamp: '' },
      { id: 'srv-a1', role: 'assistant', content: 'first response', timestamp: '' },
      { id: 'srv-u2', role: 'user', content: 'second prompt', timestamp: '' },
      { id: 'srv-a2', role: 'assistant', content: 'second response', timestamp: '' },
    ])

    const send2 = store.sendMessage('second prompt')
    await Promise.resolve(); await Promise.resolve()
    const es2 = FakeEventSource.instances[1]

    es2.fire('message', { content: 'second ' })
    es2.fire('message', { content: 'response' })

    // Mid-stream snapshot: turn-1 assistant content is untouched.
    const firstAssistant = store.messages.filter((m) => m.role === 'assistant')[0]
    expect(firstAssistant.content).toBe('first response')

    // A distinct second assistant carries turn-2 chunks (still streaming
    // from SSE — reconcile hasn't fired yet because [DONE] hasn't arrived).
    const assistantsLive = store.messages.filter((m) => m.role === 'assistant')
    expect(assistantsLive).toHaveLength(2)
    expect(assistantsLive[1].content).toBe('second response')
    expect(assistantsLive[1].id).not.toBe(firstAssistant.id)

    es2.fire('message', '[DONE]')
    resolveSend2({ id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0, createdAt: '', updatedAt: '' })
    await send2
    await Promise.resolve(); await Promise.resolve()

    // Post-reconcile: the canonical history is two complete user+assistant pairs.
    const finalAssistants = store.messages.filter((m) => m.role === 'assistant')
    expect(finalAssistants).toHaveLength(2)
    expect(finalAssistants[0].content).toBe('first response')
    expect(finalAssistants[1].content).toBe('second response')
  })
})

describe('chatStore - todowrite wiring (live SSE)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('routes a todowrite tool_result event into the todoStore for the active session', async () => {
    // The web frontend already carries tool_call/tool_result over SSE for
    // every tool (see internal/api/server.go writeSSEToolCall /
    // writeSSEToolResult). The todowrite tool fires the same pair: a
    // tool_call with name="todowrite" then a tool_result whose content is
    // the raw JSON the agent emitted. chatStore must recognise that pair
    // and feed the JSON to todoStore so the side panel updates live.
    const { useTodoStore } = await import('./todoStore')
    const chat = useChatStore()
    chat.currentSessionId = 'session-live'

    const todoStore = useTodoStore()
    todoStore.setCurrentSession('session-live')

    chat.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'todowrite' }))
    chat.applyContentEvent(
      JSON.stringify({
        type: 'tool_result',
        content: JSON.stringify([
          { content: 'live emitted', status: 'pending', priority: 'high' },
        ]),
      }),
    )

    expect(todoStore.todos).toHaveLength(1)
    expect(todoStore.todos[0].content).toBe('live emitted')
  })

  it('does not feed non-todowrite tool_result content into the todoStore', async () => {
    const { useTodoStore } = await import('./todoStore')
    const chat = useChatStore()
    chat.currentSessionId = 'session-live'

    const todoStore = useTodoStore()
    todoStore.setCurrentSession('session-live')
    todoStore.ingestToolResult(
      'session-live',
      JSON.stringify([{ content: 'pre-existing', status: 'pending', priority: 'low' }]),
    )

    chat.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'bash' }))
    chat.applyContentEvent(
      JSON.stringify({ type: 'tool_result', content: 'ls -la output' }),
    )

    // Bash tool result must NOT clobber the todo slice. The todoStore still
    // holds whatever the last todowrite emission set.
    expect(todoStore.todos).toHaveLength(1)
    expect(todoStore.todos[0].content).toBe('pre-existing')
  })
})

describe('chatStore - SSE reconnect on restoreStateFromBackend (stuck-after-reload)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
    FakeEventSource.instances.length = 0
  })

  // The user-reported symptom: after reloading the page mid-stream, the UI
  // shows the prior in-flight assistant frozen and the next prompt appears
  // to do nothing. Backend logs confirm the next prompt never reached the
  // API. Two reinforcing causes; this block pins the SSE reconnect.
  //
  // Pre-fix: restoreStateFromBackend only fetches REST history. If the
  // backend was still streaming when the reload happened, every chunk
  // produced after the reload is dropped — the SSE consumer was never
  // re-attached. The frontend has no way to know the stream resumed.
  //
  // Detection heuristic (post a500958 sealing fix): all backend-loaded
  // assistant messages are sealed to status === 'completed', so searching
  // for status === 'running' never fires on reload. Instead: when the last
  // message in restored history has role === 'user', the user sent something
  // and no assistant reply arrived yet — reattach is needed.
  //
  // Contract: when the last message in restored history has role === 'user',
  // the store must re-subscribe to /api/v1/sessions/{id}/stream so any
  // chunks the backend is still producing arrive at the UI. If the backend
  // has already finished streaming the SSE connection closes cleanly.

  it('subscribes to the session stream when the last restored message has role === user (in-flight heuristic)', async () => {
    // The sealing fix (a500958) means backend-loaded assistant rows always
    // get status === 'completed'. The new in-flight signal is: last message
    // is a user message with no subsequent assistant reply.
    window.localStorage.setItem('chat.currentSessionId', 'session-1')
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'srv-u1', role: 'user', content: 'continue', timestamp: '2026-05-04T00:00:00Z' },
    ])

    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(vi.mocked(subscribeSessionStream)).toHaveBeenCalledWith('session-1')
    expect(FakeEventSource.instances.length).toBe(1)
    expect(store.isLoading).toBe(true)
    expect(store.isStreaming).toBe(true)
  })

  it('does NOT subscribe when the last restored message has role === assistant (reply already arrived)', async () => {
    // When the last message is an assistant reply the session completed
    // before the reload — nothing to reattach.
    window.localStorage.setItem('chat.currentSessionId', 'session-1')
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'srv-u1', role: 'user', content: 'hello', timestamp: '2026-05-04T00:00:00Z' },
      {
        id: 'srv-a1',
        role: 'assistant',
        content: 'all done',
        timestamp: '2026-05-04T00:00:01Z',
      },
    ])

    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(vi.mocked(subscribeSessionStream)).not.toHaveBeenCalled()
    expect(FakeEventSource.instances.length).toBe(0)
    expect(store.isLoading).toBe(false)
  })

  it('does NOT subscribe when the messages array is empty', async () => {
    window.localStorage.setItem('chat.currentSessionId', 'session-1')
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([])

    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(vi.mocked(subscribeSessionStream)).not.toHaveBeenCalled()
    expect(FakeEventSource.instances.length).toBe(0)
  })

  it('routes chunks from the reconnected SSE stream onto a new assistant message', async () => {
    // After reattach, incoming chunks must create a new assistant message
    // (since the sealed history has no running target). handleContentChunk
    // creates a fresh placeholder when no status === 'running' row exists.
    window.localStorage.setItem('chat.currentSessionId', 'session-1')
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'srv-u1', role: 'user', content: 'continue', timestamp: '2026-05-04T00:00:00Z' },
    ])

    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(FakeEventSource.instances.length).toBe(1)
    const es = FakeEventSource.instances[0]
    es.fire('message', { content: 'response ' })
    es.fire('message', { content: 'continued' })

    const inFlight = store.messages.find((m) => m.role === 'assistant' && m.status === 'running')
    expect(inFlight?.content).toBe('response continued')
  })

  it('clears isLoading and isStreaming when the reconnected stream emits [DONE]', async () => {
    window.localStorage.setItem('chat.currentSessionId', 'session-1')
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'srv-u1', role: 'user', content: 'continue', timestamp: '2026-05-04T00:00:00Z' },
    ])

    const store = useChatStore()
    await store.restoreStateFromBackend()

    const es = FakeEventSource.instances[0]
    es.fire('message', '[DONE]')

    expect(store.isLoading).toBe(false)
    expect(store.isStreaming).toBe(false)
    expect(es.closed).toBe(true)
  })

  it('clears isLoading and isStreaming when the reconnected stream fires an error', async () => {
    window.localStorage.setItem('chat.currentSessionId', 'session-1')
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'srv-u1', role: 'user', content: 'continue', timestamp: '2026-05-04T00:00:00Z' },
    ])

    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(store.isLoading).toBe(true)
    const es = FakeEventSource.instances[0]
    es.fire('error', null)

    expect(store.isLoading).toBe(false)
    expect(store.isStreaming).toBe(false)
    expect(es.closed).toBe(true)
  })
})

describe('chatStore - sendMessage surfacing when isLoading is already true (silent-drop fix)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
    FakeEventSource.instances.length = 0
  })

  // The user-reported symptom: typing into the input and pressing Enter
  // appears to do nothing. The submit gate silently early-returns when
  // isLoading is truthy — no toast, no error, no surfacing of any kind.
  //
  // Pre-fix: chatStore.sendMessage line 347-349 returns silently. The user
  // has no way to know their prompt was rejected. Combined with a stuck
  // isLoading from a prior stream that never produced [DONE], this presents
  // as "the chat is broken".
  //
  // Contract: when sendMessage is invoked while isLoading is true, the
  // store must surface the rejection. The error string is set so the
  // existing chat-error footer renders, AND a toast fires for an in-front
  // surfacing the user cannot miss.

  it('sets store.error so the chat-error footer surfaces the rejection (was silent return)', async () => {
    const store = useChatStore()
    store.isLoading = true

    await store.sendMessage('continue')

    expect(store.error).toBeTruthy()
    expect(String(store.error)).toMatch(/(in.flight|already|wait|reload)/i)
    // The send pipeline must NOT have been reached.
    expect(vi.mocked(sendSessionMessage)).not.toHaveBeenCalled()
  })
})

describe('chatStore - isLoading watchdog (60s fail-safe)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
    FakeEventSource.instances.length = 0
  })

  // Pre-fix: if the SSE stream stalls (no chunks, no [DONE], no error),
  // isLoading stays true forever and every subsequent submission is
  // silently dropped by the gate. Reload is the user's only escape.
  //
  // Contract: a watchdog observes the most recent streaming activity. When
  // sendMessage is in flight, the watchdog clears isLoading after 60s of
  // no observed SSE activity, sets a surfacing error, and unwedges the UI.

  it('clears isLoading after 60s of no SSE activity and surfaces a stall error', async () => {
    vi.useFakeTimers()
    try {
      const store = useChatStore()
      store.agentId = 'agent-1'
      store.currentSessionId = 'session-1'

      let resolveSend: (v: any) => void = () => {}
      vi.mocked(sendSessionMessage).mockImplementationOnce(
        () => new Promise<any>((resolve) => { resolveSend = resolve }),
      )

      const sendPromise = store.sendMessage('hello')

      // Allow microtasks so the eventSource is created.
      await Promise.resolve()
      await Promise.resolve()
      expect(store.isLoading).toBe(true)

      // Advance 60s with no SSE activity at all.
      await vi.advanceTimersByTimeAsync(60_000)

      expect(store.isLoading).toBe(false)
      expect(store.error).toBeTruthy()
      expect(String(store.error)).toMatch(/(stall|stuck|timeout|no response)/i)

      // Tidy: resolve the dangling send promise so the test exits cleanly.
      resolveSend({ id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0, createdAt: '', updatedAt: '' })
      await sendPromise
    } finally {
      vi.useRealTimers()
    }
  })

  it('does NOT trip the watchdog while SSE chunks keep arriving', async () => {
    vi.useFakeTimers()
    try {
      const store = useChatStore()
      store.agentId = 'agent-1'
      store.currentSessionId = 'session-1'

      let resolveSend: (v: any) => void = () => {}
      vi.mocked(sendSessionMessage).mockImplementationOnce(
        () => new Promise<any>((resolve) => { resolveSend = resolve }),
      )

      const sendPromise = store.sendMessage('streaming')
      await Promise.resolve()
      await Promise.resolve()

      const es = FakeEventSource.instances[0]

      // Heartbeat every 30s — under the 60s threshold — for 5 minutes.
      for (let i = 0; i < 10; i++) {
        await vi.advanceTimersByTimeAsync(30_000)
        es.fire('message', { content: '.' })
      }

      // After 5 minutes of activity isLoading is still true (no stall).
      expect(store.isLoading).toBe(true)
      expect(store.error).toBeNull()

      es.fire('message', '[DONE]')
      resolveSend({ id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0, createdAt: '', updatedAt: '' })
      await sendPromise
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('chatStore - todoStore session swap on session switch', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('switches the todoStore active session and hydrates from history when a session is loaded', async () => {
    const { useTodoStore } = await import('./todoStore')

    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'session-with-todos',
        agentId: 'agent-1',
        title: 'Session with todos',
        createdAt: '',
        updatedAt: '',
        messageCount: 3,
      },
    ])
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'm1', role: 'user', content: 'kick off', timestamp: '' },
      { id: 'm2', role: 'tool_call', toolName: 'todowrite', content: 'todowrite', timestamp: '' },
      {
        id: 'm3',
        role: 'tool_result',
        toolName: 'todowrite',
        content: JSON.stringify([
          { content: 'historical todo', status: 'pending', priority: 'low' },
        ]),
        timestamp: '',
      },
    ])

    const chat = useChatStore()
    await chat.loadSessions()
    await chat.loadSessionMessages('session-with-todos')

    const todoStore = useTodoStore()
    expect(todoStore.currentSessionId).toBe('session-with-todos')
    expect(todoStore.todos).toHaveLength(1)
    expect(todoStore.todos[0].content).toBe('historical todo')
  })
})

describe('chatStore - revertToMessage', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('calls truncateSessionMessages with the session and message IDs', async () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    store.messages = [
      { id: 'msg-1', role: 'user', content: 'first prompt', timestamp: '' },
      { id: 'msg-2', role: 'assistant', content: 'response one', timestamp: '' },
      { id: 'msg-3', role: 'user', content: 'second prompt', timestamp: '' },
      { id: 'msg-4', role: 'assistant', content: 'response two', timestamp: '' },
    ]

    await store.revertToMessage('msg-3')

    expect(vi.mocked(truncateSessionMessages)).toHaveBeenCalledWith('session-1', 'msg-3')
  })

  it('truncates local messages at the reverted message index (exclusive)', async () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    store.messages = [
      { id: 'msg-1', role: 'user', content: 'first prompt', timestamp: '' },
      { id: 'msg-2', role: 'assistant', content: 'response one', timestamp: '' },
      { id: 'msg-3', role: 'user', content: 'second prompt', timestamp: '' },
      { id: 'msg-4', role: 'assistant', content: 'response two', timestamp: '' },
    ]

    await store.revertToMessage('msg-3')

    expect(store.messages).toHaveLength(2)
    expect(store.messages[0].id).toBe('msg-1')
    expect(store.messages[1].id).toBe('msg-2')
  })

  it('sets composerText to the reverted message content', async () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    store.messages = [
      { id: 'msg-1', role: 'user', content: 'first prompt', timestamp: '' },
      { id: 'msg-2', role: 'assistant', content: 'response one', timestamp: '' },
      { id: 'msg-3', role: 'user', content: 'second prompt', timestamp: '' },
    ]

    await store.revertToMessage('msg-3')

    expect(store.composerText).toBe('second prompt')
  })

  it('clears isLoading after reverting', async () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    store.isLoading = true
    store.messages = [
      { id: 'msg-1', role: 'user', content: 'prompt', timestamp: '' },
    ]

    await store.revertToMessage('msg-1')

    expect(store.isLoading).toBe(false)
  })

  it('does nothing when the message ID is not found', async () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    store.messages = [
      { id: 'msg-1', role: 'user', content: 'prompt', timestamp: '' },
    ]

    await store.revertToMessage('nonexistent-id')

    expect(vi.mocked(truncateSessionMessages)).not.toHaveBeenCalled()
    expect(store.messages).toHaveLength(1)
    expect(store.composerText).toBe('')
  })

  it('does nothing when there is no active session', async () => {
    const store = useChatStore()
    store.currentSessionId = null
    store.messages = [
      { id: 'msg-1', role: 'user', content: 'prompt', timestamp: '' },
    ]

    await store.revertToMessage('msg-1')

    expect(vi.mocked(truncateSessionMessages)).not.toHaveBeenCalled()
  })
})

describe('chatStore - localStorage persistence for agent and model selection', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('persists agentId to localStorage under chat.agentId when setAgent is called', async () => {
    const store = useChatStore()
    store.currentSessionId = null

    await store.setAgent('agent-2')

    expect(window.localStorage.getItem('chat.agentId')).toBe('agent-2')
  })

  it('persists modelId to localStorage under chat.selectedModel when setModel is called', async () => {
    const store = useChatStore()
    store.currentSessionId = null

    await store.setModel('gpt-4o', 'openai')

    expect(window.localStorage.getItem('chat.selectedModel')).toBe('gpt-4o')
  })

  it('persists providerId to localStorage under chat.selectedProvider when setModel is called', async () => {
    const store = useChatStore()
    store.currentSessionId = null

    await store.setModel('gpt-4o', 'openai')

    expect(window.localStorage.getItem('chat.selectedProvider')).toBe('openai')
  })

  it('setAgent writes agentId to localStorage so it survives a page reload', async () => {
    // The agent persistence key is written by setAgent on every agent
    // selection. This pins the contract: after setAgent('agent-2') the
    // stored value is 'agent-2', ready to be read back by
    // restoreStateFromBackend on the next page load.
    const store = useChatStore()
    store.currentSessionId = null

    await store.setAgent('agent-2')

    // Verify the key is present and correct — this is the signal that
    // the selection survives a reload.
    expect(window.localStorage.getItem('chat.agentId')).toBe('agent-2')
  })

  it('restores model and provider from localStorage when no session exists on restoreStateFromBackend', async () => {
    window.localStorage.setItem('chat.selectedModel', 'gpt-4o')
    window.localStorage.setItem('chat.selectedProvider', 'openai')
    // Return no sessions so the "no sessionForAgent" path is exercised.
    vi.mocked(fetchSessions).mockResolvedValueOnce([])
    vi.mocked(fetchModels).mockResolvedValueOnce([
      { id: 'gpt-4o', name: 'GPT-4o', providerId: 'openai' },
    ])

    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(store.currentModelId).toBe('gpt-4o')
    expect(store.currentProviderId).toBe('openai')
  })

  it('falls back to empty model when the stored model is not in the available models list', async () => {
    window.localStorage.setItem('chat.selectedModel', 'obsolete-model')
    window.localStorage.setItem('chat.selectedProvider', 'unknown-provider')
    vi.mocked(fetchSessions).mockResolvedValueOnce([])
    vi.mocked(fetchModels).mockResolvedValueOnce([
      { id: 'gpt-4o', name: 'GPT-4o', providerId: 'openai' },
    ])

    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(store.currentModelId).toBe('')
    expect(store.currentProviderId).toBe('')
  })
})

describe('chatStore - reconcileFromBackend (post-stream merge with backend canonical state)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
    FakeEventSource.instances.length = 0
  })

  // The user-visible bug: after a stream closes (or watchdog trips on a stall
  // whose backend already finished), the UI is frozen on the last visible
  // chunk. Manual page refresh fixes it because restoreStateFromBackend reads
  // the canonical history. reconcileFromBackend is the action that does the
  // same thing on stream-end without requiring the user to reload.
  //
  // Contract:
  //   - Idempotent: safe to call any number of times.
  //   - Re-checks currentSessionId before AND after the await — the user can
  //     navigate during the network round-trip; landing the result on the
  //     wrong session would corrupt the new session's view.
  //   - Merge semantics, NOT replace:
  //       * backend canonical history is the base, with assistant rows sealed
  //         to status='completed' (matching the seal rule used elsewhere)
  //       * any local optimistic user message (id starts with 'temp-') that
  //         the backend does not yet have is preserved and appended
  //       * any 'running' assistant placeholder the store created from SSE is
  //         dropped if the backend now has its persisted equivalent
  //   - Catches fetch failures silently — does not poison the UI; the
  //     watchdog/error path surfaces the user-facing message.

  it('replaces local messages with backend canonical history, sealing assistant rows to completed', async () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    // The store currently shows a stale partial.
    store.messages = [
      { id: 'srv-u1', role: 'user', content: 'hi', timestamp: '' },
      { id: 'streaming-x', role: 'assistant', content: 'partial', timestamp: '', status: 'running' },
    ]

    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'srv-u1', role: 'user', content: 'hi', timestamp: '' },
      // Backend assistant — no status, must be sealed to completed.
      { id: 'srv-a1', role: 'assistant', content: 'hello world', timestamp: '' },
    ])

    await store.reconcileFromBackend('session-1')

    expect(store.messages).toHaveLength(2)
    const assistant = store.messages.find((m) => m.id === 'srv-a1')
    expect(assistant?.content).toBe('hello world')
    expect(assistant?.status).toBe('completed')
    // The stale running placeholder must be gone — backend now has the
    // persisted equivalent.
    expect(store.messages.find((m) => m.id === 'streaming-x')).toBeUndefined()
  })

  it('preserves a temp-* optimistic user message that the backend response does not yet contain', async () => {
    // The user clicks send. sendSessionMessage is in flight. A reconcile
    // races with the still-pending append; the backend response is missing
    // the just-sent message. Without the merge, the optimistic bubble
    // disappears and the user thinks their click did nothing.
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    store.messages = [
      { id: 'srv-u1', role: 'user', content: 'first', timestamp: '' },
      { id: 'srv-a1', role: 'assistant', content: 'first reply', timestamp: '', status: 'completed' },
      // The optimistic message that the backend has not seen yet.
      { id: 'temp-12345', role: 'user', content: 'in flight', timestamp: '' },
    ]

    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'srv-u1', role: 'user', content: 'first', timestamp: '' },
      { id: 'srv-a1', role: 'assistant', content: 'first reply', timestamp: '' },
    ])

    await store.reconcileFromBackend('session-1')

    // Backend canonical first; optimistic in-flight appended at the end.
    expect(store.messages.map((m) => m.id)).toEqual(['srv-u1', 'srv-a1', 'temp-12345'])
    const optimistic = store.messages[2]
    expect(optimistic.content).toBe('in flight')
  })

  it('is a no-op when currentSessionId changed BEFORE the call (called for a stale session id)', async () => {
    const store = useChatStore()
    store.currentSessionId = 'session-current'
    store.messages = [{ id: 'msg-current', role: 'user', content: 'current', timestamp: '' }]

    // Caller asks to reconcile session-other — but the user is on session-current.
    await store.reconcileFromBackend('session-other')

    // No fetch was made and messages were not touched.
    expect(vi.mocked(fetchSessionMessages)).not.toHaveBeenCalled()
    expect(store.messages.map((m) => m.id)).toEqual(['msg-current'])
  })

  it('discards the result when currentSessionId changes DURING the await', async () => {
    // The user navigates while the network round-trip is in flight. The
    // result must land on session-A's history, not corrupt session-B.
    const store = useChatStore()
    store.currentSessionId = 'session-A'

    let resolveFetch: (value: any) => void = () => {}
    vi.mocked(fetchSessionMessages).mockImplementationOnce(
      () =>
        new Promise<any>((resolve) => {
          resolveFetch = resolve
        }),
    )

    const reconcilePromise = store.reconcileFromBackend('session-A')

    // Mid-flight: user switches to session-B and seeds new local state.
    store.currentSessionId = 'session-B'
    store.messages = [{ id: 'sb-msg', role: 'user', content: 'B content', timestamp: '' }]

    // The original fetch (for A) finally resolves — but currentSessionId
    // is now B, so the result must be discarded.
    resolveFetch([
      { id: 'sa-msg-1', role: 'user', content: 'A content', timestamp: '' },
      { id: 'sa-msg-2', role: 'assistant', content: 'A reply', timestamp: '' },
    ])
    await reconcilePromise

    // Session-B's local state is untouched.
    expect(store.currentSessionId).toBe('session-B')
    expect(store.messages.map((m) => m.id)).toEqual(['sb-msg'])
  })

  it('is idempotent — calling twice with the same backend state yields identical messages', async () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    store.messages = []

    vi.mocked(fetchSessionMessages).mockResolvedValue([
      { id: 'srv-u1', role: 'user', content: 'hi', timestamp: '' },
      { id: 'srv-a1', role: 'assistant', content: 'hello', timestamp: '' },
    ])

    await store.reconcileFromBackend('session-1')
    const after1 = store.messages.map((m) => ({ id: m.id, content: m.content, status: m.status }))

    await store.reconcileFromBackend('session-1')
    const after2 = store.messages.map((m) => ({ id: m.id, content: m.content, status: m.status }))

    expect(after2).toEqual(after1)
  })

  it('catches fetch failures silently and leaves messages untouched', async () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    store.messages = [{ id: 'local', role: 'user', content: 'preserve me', timestamp: '' }]

    vi.mocked(fetchSessionMessages).mockRejectedValueOnce(new Error('network down'))

    // Must not throw — the watchdog/error path surfaces user-facing errors.
    await expect(store.reconcileFromBackend('session-1')).resolves.not.toThrow()
    expect(store.messages.map((m) => m.id)).toEqual(['local'])
  })
})

describe('chatStore - sendMessage stream-end reconciles with backend (replaces over-gated refetch)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
    FakeEventSource.instances.length = 0
  })

  // The headline bug for PR 2: pre-fix, sendMessage's stream-close path did
  // NOT refetch the canonical history. The user saw the partial response
  // from SSE and that was it — anything the backend persisted after [DONE]
  // (for example: a tool_result that finished after the assistant content
  // chunk stopped) was invisible until the user reloaded the page.
  //
  // Post-fix: every stream-end path (DONE, error, watchdog-trip) reconciles
  // with the backend so the user-visible state matches the canonical state.

  it('reconciles with the backend after [DONE] so the bubble shows the canonical assistant content', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    let resolveSend: (v: any) => void = () => {}
    vi.mocked(sendSessionMessage).mockImplementationOnce(
      () => new Promise<any>((resolve) => { resolveSend = resolve }),
    )
    // The backend canonical state includes a tool_result the SSE didn't
    // surface to this consumer (e.g. arrived during the brief gap between
    // [DONE] and reconcile call).
    vi.mocked(fetchSessionMessages).mockResolvedValue([
      { id: 'srv-u1', role: 'user', content: 'go', timestamp: '' },
      { id: 'srv-a1', role: 'assistant', content: 'final canonical reply', timestamp: '' },
    ])

    const sendPromise = store.sendMessage('go')
    await Promise.resolve(); await Promise.resolve()
    const es = FakeEventSource.instances[0]

    // SSE delivers a partial — backend will hold the canonical full text.
    es.fire('message', { content: 'partial' })
    es.fire('message', '[DONE]')
    resolveSend({ id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0, createdAt: '', updatedAt: '' })
    await sendPromise

    // Wait for the reconcile microtasks to flush.
    await Promise.resolve(); await Promise.resolve()

    // Post-reconcile, the user sees the backend canonical reply rather than
    // the partial SSE chunk that arrived first.
    const assistants = store.messages.filter((m) => m.role === 'assistant')
    expect(assistants).toHaveLength(1)
    expect(assistants[0].content).toBe('final canonical reply')
    expect(assistants[0].status).toBe('completed')
  })

  it('reconciles with the backend on SSE error (network glitch / proxy hang) so the bubble updates', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    let resolveSend: (v: any) => void = () => {}
    vi.mocked(sendSessionMessage).mockImplementationOnce(
      () => new Promise<any>((resolve) => { resolveSend = resolve }),
    )
    // Backend completed despite the SSE drop.
    vi.mocked(fetchSessionMessages).mockResolvedValue([
      { id: 'srv-u1', role: 'user', content: 'hi', timestamp: '' },
      { id: 'srv-a1', role: 'assistant', content: 'recovered reply', timestamp: '' },
    ])

    const sendPromise = store.sendMessage('hi')
    await Promise.resolve(); await Promise.resolve()
    const es = FakeEventSource.instances[0]

    // SSE pipe drops mid-stream.
    es.fire('error', null)
    resolveSend({ id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0, createdAt: '', updatedAt: '' })
    await sendPromise
    await Promise.resolve(); await Promise.resolve()

    // The user sees the recovered reply rather than a frozen partial.
    expect(store.messages.find((m) => m.id === 'srv-a1')?.content).toBe('recovered reply')
    expect(store.isStreaming).toBe(false)
    expect(store.isLoading).toBe(false)
  })
})

describe('chatStore - watchdog trip recovers via reconcile (stall recovery)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
    FakeEventSource.instances.length = 0
  })

  // The compounding bug: a stalled SSE (proxy hang or network glitch with
  // server-side completion) tripped the watchdog at 60s, but pre-fix the
  // watchdog only cleared isLoading/isStreaming — it never refetched. The
  // user was left looking at the partial chunk frozen on screen and had to
  // reload to see the actual completed response.

  it('reconciles with the backend after the watchdog trips so the partial bubble updates to canonical', async () => {
    vi.useFakeTimers()
    try {
      const store = useChatStore()
      store.agentId = 'agent-1'
      store.currentSessionId = 'session-1'
      store.messages = []

      let resolveSend: (v: any) => void = () => {}
      vi.mocked(sendSessionMessage).mockImplementationOnce(
        () => new Promise<any>((resolve) => { resolveSend = resolve }),
      )
      // Backend completed; SSE just stopped delivering.
      vi.mocked(fetchSessionMessages).mockResolvedValue([
        { id: 'srv-u1', role: 'user', content: 'long task', timestamp: '' },
        { id: 'srv-a1', role: 'assistant', content: 'completed despite stall', timestamp: '' },
      ])

      const sendPromise = store.sendMessage('long task')
      await Promise.resolve(); await Promise.resolve()
      const es = FakeEventSource.instances[0]

      // Some content arrives, then the stream stalls completely.
      es.fire('message', { content: 'partial frozen' })

      // 60s of zero activity — watchdog must trip.
      await vi.advanceTimersByTimeAsync(60_000)

      expect(store.isStreaming).toBe(false)
      expect(store.isLoading).toBe(false)

      // The watchdog fires handleStreamStall(sessionId) which fire-and-forgets
      // reconcileFromBackend. Flush the queued microtask chain so the
      // mocked fetchSessionMessages resolves and updates this.messages.
      // advanceTimersByTimeAsync already runs queued microtasks, so a few
      // explicit awaits here are sufficient to settle the await chain
      // inside reconcileFromBackend.
      for (let i = 0; i < 8; i++) {
        await vi.advanceTimersByTimeAsync(0)
      }

      // The user-observable outcome: the bubble now reflects the canonical
      // backend state, not the frozen partial.
      const assistant = store.messages.find((m) => m.id === 'srv-a1')
      expect(assistant?.content).toBe('completed despite stall')

      // Tidy.
      resolveSend({ id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0, createdAt: '', updatedAt: '' })
      await sendPromise
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('chatStore - loadSessions detects was-streaming → not-streaming and reconciles (parent watching child)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
    FakeEventSource.instances.length = 0
  })

  // The compounding bug C-4: the parent session shows a delegation card
  // while a child agent is doing the work. When the child finishes the
  // backend marks the parent session as no-longer-streaming, but the parent
  // UI never reconciles — the user has to reload to see the final reply.
  //
  // Contract: after each loadSessions poll, if the active session's
  // isStreaming flag transitioned from true to false since the previous
  // poll, the store reconciles with the backend so the child's final
  // payload is visible without manual refresh.

  it('reconciles when the active session transitions from isStreaming=true to false between two loadSessions calls', async () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    store.messages = [
      { id: 'srv-u1', role: 'user', content: 'delegate this', timestamp: '' },
      // The store still shows a 'running' delegation card from earlier SSE.
      { id: 'streaming-del', role: 'delegation_started', content: '', timestamp: '', status: 'running' },
    ]

    // First poll: backend reports active stream — no reconcile expected.
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'session-1',
        agentId: 'agent-1',
        title: 's',
        createdAt: '',
        updatedAt: '',
        messageCount: 1,
        isStreaming: true,
      },
    ])

    await store.loadSessions()
    // No reconcile while still streaming.
    expect(vi.mocked(fetchSessionMessages)).not.toHaveBeenCalled()

    // Second poll: backend now reports stream finished — reconcile expected.
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'session-1',
        agentId: 'agent-1',
        title: 's',
        createdAt: '',
        updatedAt: '',
        messageCount: 3,
        isStreaming: false,
      },
    ])
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'srv-u1', role: 'user', content: 'delegate this', timestamp: '' },
      { id: 'srv-a1', role: 'assistant', content: 'final after delegation', timestamp: '' },
    ])

    await store.loadSessions()
    // Allow the queued reconcile microtasks to flush.
    await Promise.resolve(); await Promise.resolve()

    expect(vi.mocked(fetchSessionMessages)).toHaveBeenCalledWith('session-1')
    // The store now shows the canonical history; the stale delegation card is gone.
    expect(store.messages.find((m) => m.id === 'srv-a1')?.content).toBe('final after delegation')
    expect(store.messages.find((m) => m.id === 'streaming-del')).toBeUndefined()
  })

  it('does not reconcile when the active session was not previously streaming (no-op transition)', async () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'

    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'session-1',
        agentId: 'agent-1',
        title: 's',
        createdAt: '',
        updatedAt: '',
        messageCount: 0,
        isStreaming: false,
      },
    ])

    await store.loadSessions()
    expect(vi.mocked(fetchSessionMessages)).not.toHaveBeenCalled()
  })
})

describe('chatStore - optimistic user message reconciliation (C-1, C-2)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
    FakeEventSource.instances.length = 0
  })

  // The compounding bug C-1: the temp-${Date.now()} optimistic id was never
  // reconciled with the server-assigned id from the POST response. After
  // the next backend refetch (now via reconcileFromBackend) the canonical
  // history landed alongside the still-present optimistic, producing a
  // duplicate user bubble.
  //
  // The compounding bug C-2: when sendSessionMessage rejected (backend
  // refused, network error), the optimistic bubble stayed in the thread
  // with no failed/retry indicator. The user couldn't tell their message
  // had failed; only the toast surfaced anything.
  //
  // Contract:
  //   - On sendSessionMessage resolve, the temp-* id is replaced with the
  //     server-assigned id taken from the response payload's matching
  //     user message (matched by content).
  //   - On sendSessionMessage reject, the optimistic message is marked
  //     `status: 'failed'` so MessageBubble can render a small marker.
  //     The bubble stays in place so the user can see what they sent.

  it('replaces the temp-* id with the server-assigned id when sendSessionMessage resolves', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    let resolveSend: (v: any) => void = () => {}
    vi.mocked(sendSessionMessage).mockImplementationOnce(
      () => new Promise<any>((resolve) => { resolveSend = resolve }),
    )
    vi.mocked(fetchSessionMessages).mockResolvedValue([
      { id: 'srv-u-actual', role: 'user', content: 'reconcile me', timestamp: '' },
      { id: 'srv-a1', role: 'assistant', content: 'reply', timestamp: '' },
    ])

    const sendPromise = store.sendMessage('reconcile me')
    await Promise.resolve(); await Promise.resolve()

    // Optimistic bubble exists with a temp-* id at this point.
    const optimisticInitial = store.messages.find((m) => m.role === 'user' && m.content === 'reconcile me')
    expect(optimisticInitial?.id).toMatch(/^temp-/)

    // Backend POST returns the canonical session including the persisted
    // user message — the response's user-message id is what should land on
    // the optimistic bubble.
    resolveSend({
      id: 'session-1',
      agentId: 'agent-1',
      messages: [
        { id: 'srv-u-actual', role: 'user', content: 'reconcile me', timestamp: '' },
      ],
      messageCount: 1,
      createdAt: '',
      updatedAt: '',
    })
    await sendPromise
    await Promise.resolve(); await Promise.resolve()

    // Post-resolve: the bubble carries the server-assigned id, not temp-*.
    // (Either the in-place id swap took effect before reconcile, or the
    // reconcile picked up the server id and the optimistic was matched and
    // dropped from the orphans list — either way the user-observable shape
    // is "exactly one user bubble with content 'reconcile me' and a
    // non-temp id".)
    const userBubbles = store.messages.filter((m) => m.role === 'user' && m.content === 'reconcile me')
    expect(userBubbles).toHaveLength(1)
    expect(userBubbles[0].id).toBe('srv-u-actual')
    expect(userBubbles[0].id).not.toMatch(/^temp-/)
  })

  it('marks the optimistic message status=failed and surfaces an error when sendSessionMessage rejects', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    vi.mocked(sendSessionMessage).mockRejectedValueOnce(new Error('backend rejected'))

    await store.sendMessage('this will fail')

    // The optimistic bubble stays in the thread (so the user can see what
    // they tried to send) but is marked failed so MessageBubble can render
    // a visible marker on it.
    const failed = store.messages.find((m) => m.role === 'user' && m.content === 'this will fail')
    expect(failed).toBeDefined()
    expect(failed?.status).toBe('failed')
    expect(failed?.id).toMatch(/^temp-/)

    // Error string is set so the existing chat-error footer renders the cause.
    expect(store.error).toContain('backend rejected')
  })

  it('does NOT mark the optimistic message failed when sendSessionMessage resolves (happy path)', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      id: 'session-1',
      agentId: 'agent-1',
      messages: [
        { id: 'srv-u1', role: 'user', content: 'happy path', timestamp: '' },
      ],
      messageCount: 1,
      createdAt: '',
      updatedAt: '',
    })
    vi.mocked(fetchSessionMessages).mockResolvedValue([
      { id: 'srv-u1', role: 'user', content: 'happy path', timestamp: '' },
    ])

    await store.sendMessage('happy path')
    await Promise.resolve(); await Promise.resolve()

    const userMsg = store.messages.find((m) => m.role === 'user' && m.content === 'happy path')
    expect(userMsg).toBeDefined()
    expect(userMsg?.status).not.toBe('failed')
  })
})

describe('chatStore - loadSessionMessages clears isStreaming alongside isLoading (C-7)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  // The compounding bug C-7: switching to an idle session while
  // isStreaming was true left the activity indicator pulsing on a session
  // that has nothing in flight. The user reads "agent is working" forever.

  it('clears isStreaming in the finally block of loadSessionMessages', async () => {
    const store = useChatStore()
    // Simulate switching from a session that was streaming.
    store.isStreaming = true
    store.isLoading = false

    vi.mocked(fetchSessions).mockResolvedValueOnce([
      { id: 'session-idle', agentId: 'agent-1', title: 'idle', createdAt: '', updatedAt: '', messageCount: 0 },
    ])

    await store.loadSessions()
    await store.loadSessionMessages('session-idle')

    // Both flags are cleared after the load completes.
    expect(store.isLoading).toBe(false)
    expect(store.isStreaming).toBe(false)
  })
})
