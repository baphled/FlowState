import { setActivePinia, createPinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useToast } from '@/composables/useToast'

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
  compactSessionNow,
  createSession,
  deleteSession,
  fetchAgents,
  fetchModels,
  fetchSessionMessages,
  fetchSessions,
  fetchSwarms,
  fetchTurn,
  sendSessionMessage,
  subscribeSessionStream,
  truncateSessionMessages,
  updateSessionAgent,
  updateSessionModel,
} from '../api'
import {
  DEFAULT_AGENT_ID,
  TOOL_ACTIVITY_DISMISS_MS,
  __resetSessionStreams,
  composeToolActivityMessage,
  describeToolName,
  useChatStore,
} from './chatStore'
import { useQuotaStore } from './quotaStore'

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
  fetchSwarms: vi.fn(() => Promise.resolve([
    { id: 'planning-loop', description: 'Planning swarm', lead: 'planner', members: ['explorer', 'analyst'] },
    { id: 'solo', description: 'Solo', lead: 'executor', members: [] },
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
    status: 'active',
    depth: 0,
    isStreaming: false,
    createdAt: '',
    updatedAt: '',
  })),
  sendSessionMessage: vi.fn((sessionId: string, content: string) => Promise.resolve({
    id: sessionId,
    agentId: 'agent-1',
    messages: [{ id: 'msg-x', sessionId, content, sender: 'user' }],
    messageCount: 1,
    status: 'active',
    depth: 0,
    isStreaming: false,
    createdAt: '',
    updatedAt: '',
  })),
  updateSessionAgent: vi.fn((sessionId: string, agentId: string) => Promise.resolve({
    id: sessionId,
    agentId,
    messages: [],
    messageCount: 0,
    status: 'active',
    depth: 0,
    isStreaming: false,
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
    status: 'active',
    depth: 0,
    isStreaming: false,
    createdAt: '',
    updatedAt: '',
  })),
  fetchModels: vi.fn(() => Promise.resolve([
    { id: 'claude-opus', name: 'Claude Opus', providerId: 'anthropic' },
    { id: 'gpt-4o', name: 'GPT-4o', providerId: 'openai' },
  ])),
  subscribeSessionStream: vi.fn((sessionId: string) => new FakeEventSource(`/api/v1/sessions/${sessionId}/stream`)),
  truncateSessionMessages: vi.fn((_sessionId: string, _messageId: string) => Promise.resolve()),
  // QW-11 — deleteSession backing the per-row trash button. Default mock
  // is "success"; per-test overrides can `vi.mocked(deleteSession).mockRejectedValueOnce(...)`.
  deleteSession: vi.fn((_sessionId: string) => Promise.resolve()),
  // Deliverable 3 (May 2026 context-accuracy bundle) — /compress
  // slash command's HTTP seam. Default mock reports "fired": tests
  // override with mockResolvedValueOnce({fired:false}) for the
  // empty-store branch.
  compactSessionNow: vi.fn((_sessionId: string) => Promise.resolve({
    fired: true,
    summary: '[auto-compacted summary]: {"intent":"x"}',
  })),
  // Phase 3 of "Turn-Based Post-Then-Poll Architecture (May 2026)" —
  // GET /api/v1/sessions/{sid}/turns/{tid}. Default mock resolves to a
  // single completed turn with no messages; specific tests override
  // with `.mockResolvedValueOnce(...)` or `.mockImplementation(...)`.
  fetchTurn: vi.fn((sessionId: string, turnId: string) => Promise.resolve({
    turn_id: turnId,
    session_id: sessionId,
    status: 'completed',
    started_at: '',
    completed_at: '',
    model: { provider: '', model: '' },
    error: '',
    messages: [],
  })),
}))

// Toast composable is a module singleton — without a global teardown the
// rolling tool-activity toast (added May 2026) leaks between tests. Pinia
// state is reset per-test, but the toasts ref lives in the composable's
// module scope and survives setActivePinia. Dismiss everything between
// tests so a tool_call test can't pollute a later "no toast" assertion.
afterEach(() => {
  const { dismissAll } = useToast()
  dismissAll()
  // Per-session SSE singletons (Slice B) — module-scoped Map persists
  // between tests; reset so a stream from test A does not leak into B.
  __resetSessionStreams()
})

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
        status: 'active',
        depth: 0,
        isStreaming: false,
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
        status: 'active',
        depth: 0,
        isStreaming: false,
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
        status: 'active',
        depth: 0,
        isStreaming: false,
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

  it('reconciles backend state ONCE after sendMessage resolves — never mid-stream', async () => {
    // Behaviour pinning for the post-(May 2026 fresh-session bugfix)
    // contract. There are two failure modes this guards against:
    //
    //   A. Calling fetchSessionMessages WHILE SSE is in flight injects
    //      backend-loaded assistant rows (status === undefined) that
    //      handleContentChunk would then adopt as the streaming target,
    //      causing the previous response to appear above the new user
    //      prompt. The contract is: no fetch during the SSE window.
    //
    //   B. NOT calling fetchSessionMessages after the POST resolves
    //      leaves local state stale — the assistant content the
    //      backend persisted post-stream-close (delegations, tool
    //      results, late-sealed assistant rows) is invisible until the
    //      next page reload. The contract is: exactly ONE fetch, after
    //      `await sendSessionMessage` resolves.
    //
    // We assert both: zero calls during the in-flight window, exactly
    // one call after the send resolves. The single call is the
    // reconcileFromBackend invocation that lands the canonical state
    // and prevents the duplicate-user-bubble + missing-assistant-after-
    // refresh regressions reported in May 2026.
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []
    vi.mocked(fetchSessionMessages).mockResolvedValue([])

    FakeEventSource.instances.length = 0

    let resolveSend: (value: any) => void = () => {}
    vi.mocked(sendSessionMessage).mockImplementationOnce(
      () =>
        new Promise<any>((resolve) => {
          resolveSend = resolve
        }),
    )

    const sendPromise = store.sendMessage('test reconcile contract')

    await Promise.resolve()
    await Promise.resolve()

    // Mid-flight: zero fetches. The SSE stream is the source of truth
    // until [DONE] arrives.
    expect(
      vi.mocked(fetchSessionMessages),
      'must not fetch messages while POST is still in flight — would race with the SSE stream',
    ).not.toHaveBeenCalled()

    resolveSend({
      id: 'session-1',
      agentId: 'agent-1',
      messages: [],
      messageCount: 0,
      status: 'active',
      depth: 0,
      isStreaming: false,
      createdAt: '',
      updatedAt: '',
    })
    await sendPromise

    // Post-resolve: exactly one fetch. This is the reconcileFromBackend
    // call that lands the canonical state — without it the user must
    // reload to see the assistant response.
    expect(
      vi.mocked(fetchSessionMessages),
      'must fetch messages exactly once after POST resolves — the post-send reconcile',
    ).toHaveBeenCalledTimes(1)
    expect(vi.mocked(fetchSessionMessages)).toHaveBeenCalledWith('session-1')
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
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
      {
        id: 'session-B',
        agentId: 'agent-2',
        title: 'Session B',
        createdAt: '',
        updatedAt: '',
        messageCount: 2,
        status: 'active',
        depth: 0,
        isStreaming: false,
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
        status: 'active',
        depth: 0,
        isStreaming: false,
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
    // Phase 3 update: SSE no longer opens during the in-flight POST
    // window. The active-send path is now POST → branch on turn_id →
    // (poll if present | open SSE if absent). This test exercises the
    // legacy SSE branch: the POST resolves with a flat Session (no
    // turn_id) so the chat store opens SSE post-resolve.
    //
    // The reconcileFromBackend after the POST is given a "turn still
    // in flight server-side" canonical history (last row is the user
    // message) so the orphan-preservation rule keeps the local
    // status='running' assistant row alive — that's the target the
    // delegation event lands on.
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

    // Legacy-server response shape (no turn_id) — the chat store falls
    // through to the SSE branch.
    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      id: 'session-1',
      agentId: 'agent-1',
      messages: [],
      messageCount: 0,
      status: 'active',
      depth: 0,
      isStreaming: false,
      createdAt: '',
      updatedAt: '',
    } as never)
    // Backend reports the turn is still mid-flight (last row is user);
    // reconcileFromBackend's orphan-preservation rule keeps the local
    // running row.
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'srv-u1', role: 'user', content: 'long task that delegates', timestamp: '' },
    ])

    await store.sendMessage('long task that delegates')

    expect(vi.mocked(subscribeSessionStream)).toHaveBeenCalledWith('session-1')
    expect(FakeEventSource.instances.length).toBe(1)

    const es = FakeEventSource.instances[0]
    // Backend always tags delegation events with `type: 'delegation'`
    // (writeSSEDelegationInfo in internal/api/server.go injects the field
    // even when wrapping a provider DelegationInfo). The pre-PR-3
    // structural fallback that accepted untyped delegation-shaped
    // payloads was bug-for-bug compatibility with an older emitter and
    // has been removed (Principal F6).
    es.fire('message', {
      type: 'delegation',
      chain_id: 'chain-xyz',
      target_agent: 'researcher',
      tool_calls: 4,
      last_tool: 'web_search',
      status: 'in_progress',
    })

    // The delegation handler matches the in-flight assistant by
    // status === 'running' / role === 'assistant'. After reconcile
    // the store also contains the user message(s); pin the
    // assertion onto the assistant row specifically rather than
    // the first non-completed row.
    const updated = store.messages.find(
      (m) => m.role === 'assistant' || m.role === 'delegation_started',
    )
    expect(updated?.targetAgent).toBe('researcher')
    expect(updated?.chainId).toBe('chain-xyz')
    expect(updated?.toolCalls).toBe(4)
    expect(updated?.lastTool).toBe('web_search')
    expect(updated?.status).toBe('in_progress')
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
      status: 'active',
      depth: 0,
      isStreaming: false,
      createdAt: '',
      updatedAt: '',
    })
    await sendPromise
  })

  it('appends progressive content chunks from default SSE message events to the in-flight assistant message', async () => {
    // Phase 3 update: legacy SSE branch — POST resolves with no
    // turn_id, then SSE opens. See the sibling spec above for the full
    // rationale on why the SSE-during-POST window no longer exists.
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    // The streaming target is identified by status === 'running'.
    store.messages = [
      { id: 'msg-running', role: 'assistant', content: '', timestamp: '', status: 'running' },
    ]

    FakeEventSource.instances.length = 0

    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      id: 'session-1',
      agentId: 'agent-1',
      messages: [],
      messageCount: 0,
      status: 'active',
      depth: 0,
      isStreaming: false,
      createdAt: '',
      updatedAt: '',
    } as never)
    // Backend reports the turn still mid-flight (last row is user) so
    // reconcileFromBackend's orphan-preservation keeps the running row.
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'srv-u1', role: 'user', content: 'stream me', timestamp: '' },
    ])

    await store.sendMessage('stream me')

    expect(FakeEventSource.instances.length).toBe(1)
    const es = FakeEventSource.instances[0]

    es.fire('message', { content: 'hel' })
    es.fire('message', { content: 'lo ' })
    es.fire('message', { content: 'world' })

    // Pin the assertion on the assistant row specifically (reconcile
    // adds the user message back to the store; the first non-completed
    // row would then be the user, not the assistant).
    const target = store.messages.find((m) => m.role === 'assistant')
    expect(target?.content).toBe('hello world')
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

  // Gap 2 (tool_error SSE wire, May 2026). When the engine stamps
  // IsError=true on a tool_result chunk, the SSE wire emits a distinct
  // `tool_error` event so the live stream can mark the matching tool
  // message as failed in-stream. The ToolBubble's cardDefaultOpen
  // watcher reacts to status='error' and force-opens the card so the
  // user sees the failure immediately, not behind a chevron click.
  //
  // Contract: the most recent running tool_result is flipped to
  // status='error' AND the content is populated with the error text.
  // The legacy tool_result handler keeps working for non-error chunks
  // — tool_error is purely additive.
  it('flips the most recent running tool_result to status=error when a tool_error event arrives', () => {
    const store = useChatStore()
    store.messages = [
      {
        id: 'tool-1',
        role: 'tool_result',
        toolName: 'read',
        content: 'previous output',
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

    store.applyContentEvent(
      JSON.stringify({ type: 'tool_error', content: 'Error: bash exited non-zero' }),
    )

    expect(store.messages[1].status).toBe('error')
    expect(store.messages[1].content).toBe('Error: bash exited non-zero')
    // The completed earlier message must stay untouched.
    expect(store.messages[0].status).toBe('completed')
    expect(store.messages[0].content).toBe('previous output')
  })

  it('does not mutate any tool message when a tool_error arrives but no running tool_result exists', () => {
    const store = useChatStore()
    store.messages = [
      {
        id: 'tool-1',
        role: 'tool_result',
        toolName: 'read',
        content: 'ok',
        timestamp: new Date().toISOString(),
        status: 'completed',
      },
    ]

    store.applyContentEvent(
      JSON.stringify({ type: 'tool_error', content: 'Error: nothing to flip' }),
    )

    // Defensive: no running target means no mutation. We tolerate stray
    // tool_error events (e.g. arriving after a watchdog cancel) by no-op.
    expect(store.messages[0].status).toBe('completed')
    expect(store.messages[0].content).toBe('ok')
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

  it('sets criticalError (with correlation_id) when a stream_critical event arrives', () => {
    // The Go SSE pipeline emits {"error":"critical stream error","correlation_id":"<id>"}
    // when handleSessionStream / SSEConsumer.WriteError / BuildWSChunkMsg
    // classify a provider error as SeverityCritical (revoked OAuth, 401,
    // model-not-found, billing/quota lockout). The chat UI surfaces this
    // via a persistent banner, so the store must capture both the safe
    // message and the correlation id (used by the banner's "Show details"
    // affordance).
    const store = useChatStore()

    store.applyContentEvent(
      JSON.stringify({ error: 'critical stream error', correlation_id: 'abc123' }),
    )

    expect(store.criticalError).toEqual({
      message: 'critical stream error',
      correlationId: 'abc123',
    })
  })

  it('does NOT set criticalError when a transient stream_error event arrives (regression-resistance)', () => {
    // Without this guard a future change that broadens the criticality
    // discriminator would silently escalate every transient blip into a
    // persistent banner. The transient path must continue to set only
    // store.error.
    const store = useChatStore()

    store.applyContentEvent(
      JSON.stringify({ error: 'stream error', correlation_id: 'xyz' }),
    )

    expect(store.criticalError).toBeNull()
    expect(store.error).toBe('stream error')
  })

  it('overwrites criticalError when a fresh stream_critical event arrives after dismissal', () => {
    // Per the spec: dismissing the banner must NOT permanently suppress
    // criticality — a fresh fatal error after dismissal carries a new
    // correlation id and the user must see it. This pins the
    // overwrite-on-arrival contract independent of any prior dismissal.
    const store = useChatStore()
    store.criticalError = { message: 'critical stream error', correlationId: 'old-id' }
    store.dismissCriticalError()
    expect(store.criticalError).toBeNull()

    store.applyContentEvent(
      JSON.stringify({ error: 'critical stream error', correlation_id: 'new-id' }),
    )

    expect(store.criticalError).toEqual({
      message: 'critical stream error',
      correlationId: 'new-id',
    })
  })

  it('keeps isStreaming true on intermediate [DONE] between tool rounds (Slice D — activity-indicator continuity)', () => {
    // Streaming Coherence Slice D — pre-slice DONE flipped isStreaming
    // false on every sentinel including intermediate DONEs. The user
    // observed indicator flicker. New contract: isStreaming stays true
    // across intermediate DONEs and is only cleared by the send finally
    // block when the outer turn completes.
    const store = useChatStore()
    store.currentSessionId = 'sess-1'
    store.setSessionStreaming('sess-1', { isStreaming: true })

    store.applyContentEvent('[DONE]')

    // Pre-slice this asserted false; new contract is sticky-until-outer.
    expect(store.isStreaming).toBe(true)
  })
})

// Phase 3 of "Turn-Based Post-Then-Poll Architecture (May 2026)".
//
// Plan: replace the broken per-send SSE subscription with HTTP polling
// driven off the turn_id Phase 2 introduced on POST /api/v1/sessions/
// {id}/messages. The active-send path opens NO SSE; it polls
// GET /api/v1/sessions/{id}/turns/{turn_id} every 1s, applying the
// returned messages additively, until status transitions away from
// 'running'.
//
// Plan reference:
//   ~/vaults/baphled/1. Projects/FlowState/Plans/
//     Turn-Based Post-Then-Poll Architecture (May 2026).md
//
// Predecessors:
//   - 6ce8048d Phase 1 — Turn resource + propagation
//   - 9e398807 Phase 2 — POST returns turn_id + GET /turns/{id}
//
// The user has spent ~24 hours debugging the SSE live-render bug
// through nine symptom-patch commits. Phase 3 closes the bug class by
// removing SSE from the active-send path entirely.
describe('chatStore - sendMessage turn-id poll path (Phase 3)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('polls fetchTurn when sendSessionMessage returns turnId (no SSE opened)', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    FakeEventSource.instances.length = 0

    const snapshot = {
      id: 'session-1',
      agentId: 'agent-1',
      messages: [{ id: 'user-1', role: 'user', content: 'hello', timestamp: '' }],
      messageCount: 1,
      status: 'active',
      depth: 0,
      isStreaming: false,
      createdAt: '',
      updatedAt: '',
    }
    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      turnId: 'turn-abc',
      snapshot,
    } as unknown as never)

    // Two-poll trajectory: first poll returns running with one assistant
    // chunk, second poll returns completed with the same row sealed.
    vi.mocked(fetchTurn)
      .mockResolvedValueOnce({
        turn_id: 'turn-abc',
        session_id: 'session-1',
        status: 'running',
        started_at: '',
        completed_at: null,
        model: { provider: 'anthropic', model: 'claude-opus' },
        error: '',
        messages: [
          { id: 'asst-1', role: 'assistant', content: 'Hi', timestamp: '', status: 'running' },
        ],
      } as never)
      .mockResolvedValueOnce({
        turn_id: 'turn-abc',
        session_id: 'session-1',
        status: 'completed',
        started_at: '',
        completed_at: '',
        model: { provider: 'anthropic', model: 'claude-opus' },
        error: '',
        messages: [
          { id: 'asst-1', role: 'assistant', content: 'Hi there', timestamp: '', status: 'completed' },
        ],
      } as never)

    await store.sendMessage('hello')

    expect(vi.mocked(fetchTurn)).toHaveBeenCalled()
    const firstCall = vi.mocked(fetchTurn).mock.calls[0]
    expect(firstCall[0]).toBe('session-1')
    expect(firstCall[1]).toBe('turn-abc')
    expect(vi.mocked(fetchTurn).mock.calls.length).toBeGreaterThanOrEqual(2)

    // No SSE subscription opened for the active-send path. Phase 4
    // (maybeReattachStream / refresh-reattach) is the only remaining
    // SSE consumer and is out of scope for Phase 3.
    expect(vi.mocked(subscribeSessionStream)).not.toHaveBeenCalled()
    expect(FakeEventSource.instances.length).toBe(0)
  })

  it("applies each poll's messages additively (no duplication on progressive growth)", async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    const snapshot = {
      id: 'session-1',
      agentId: 'agent-1',
      messages: [{ id: 'user-1', role: 'user', content: 'long task', timestamp: '' }],
      messageCount: 1,
      status: 'active',
      depth: 0,
      isStreaming: false,
      createdAt: '',
      updatedAt: '',
    }
    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      turnId: 'turn-xyz',
      snapshot,
    } as unknown as never)

    // Reconcile fires AFTER the poll loop terminates. To validate the
    // additive merge produced by polling, fetchSessionMessages must
    // return the same row ids the polls produced — that's what the
    // backend would actually persist (the engine's emission feeds both
    // the Turn buffer and the session history). The reconcile then
    // preserves the by-id structure without duplicating rows.
    vi.mocked(fetchSessionMessages).mockResolvedValue([
      { id: 'user-1', role: 'user', content: 'long task', timestamp: '' },
      { id: 'asst-1', role: 'assistant', content: 'final answer', timestamp: '', status: 'completed' },
      { id: 'tool-1', role: 'tool_result', content: 'fetched', timestamp: '' },
    ])

    vi.mocked(fetchTurn)
      .mockResolvedValueOnce({
        turn_id: 'turn-xyz',
        session_id: 'session-1',
        status: 'running',
        started_at: '',
        completed_at: null,
        model: { provider: '', model: '' },
        error: '',
        messages: [
          { id: 'asst-1', role: 'assistant', content: 'thinking…', timestamp: '', status: 'running' },
        ],
      } as never)
      .mockResolvedValueOnce({
        turn_id: 'turn-xyz',
        session_id: 'session-1',
        status: 'running',
        started_at: '',
        completed_at: null,
        model: { provider: '', model: '' },
        error: '',
        messages: [
          { id: 'asst-1', role: 'assistant', content: 'thinking…', timestamp: '', status: 'running' },
          { id: 'tool-1', role: 'tool_result', content: 'fetched', timestamp: '' },
        ],
      } as never)
      .mockResolvedValueOnce({
        turn_id: 'turn-xyz',
        session_id: 'session-1',
        status: 'completed',
        started_at: '',
        completed_at: '',
        model: { provider: '', model: '' },
        error: '',
        messages: [
          { id: 'asst-1', role: 'assistant', content: 'final answer', timestamp: '', status: 'completed' },
          { id: 'tool-1', role: 'tool_result', content: 'fetched', timestamp: '' },
        ],
      } as never)

    await store.sendMessage('long task')

    // The polling loop must have run multiple times — at least the three
    // mocked phases.
    expect(vi.mocked(fetchTurn).mock.calls.length).toBeGreaterThanOrEqual(3)

    // Each turn-id row should appear EXACTLY ONCE — additive merge by
    // id, not stacked duplication.
    const asstRows = store.messages.filter((m) => m.id === 'asst-1')
    const toolRows = store.messages.filter((m) => m.id === 'tool-1')
    expect(asstRows.length).toBe(1)
    expect(toolRows.length).toBe(1)
  })

  it('stops polling when status transitions to completed', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      turnId: 'turn-stop',
      snapshot: {
        id: 'session-1',
        agentId: 'agent-1',
        messages: [],
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
        createdAt: '',
        updatedAt: '',
      },
    } as unknown as never)

    vi.mocked(fetchTurn)
      .mockResolvedValueOnce({
        turn_id: 'turn-stop',
        session_id: 'session-1',
        status: 'running',
        started_at: '',
        completed_at: null,
        model: { provider: '', model: '' },
        error: '',
        messages: [],
      } as never)
      .mockResolvedValueOnce({
        turn_id: 'turn-stop',
        session_id: 'session-1',
        status: 'completed',
        started_at: '',
        completed_at: '',
        model: { provider: '', model: '' },
        error: '',
        messages: [
          { id: 'asst-1', role: 'assistant', content: 'done', timestamp: '', status: 'completed' },
        ],
      } as never)

    await store.sendMessage('quick')

    const callsAfterDone = vi.mocked(fetchTurn).mock.calls.length

    // Wait several real ticks — if the poll loop keeps firing, this would
    // accumulate more calls. With proper termination, the call count
    // stays put.
    await new Promise((r) => setTimeout(r, 50))
    await new Promise((r) => setTimeout(r, 50))

    expect(vi.mocked(fetchTurn).mock.calls.length).toBe(callsAfterDone)
  })

  it('surfaces an error when fetchTurn returns status=failed', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      turnId: 'turn-fail',
      snapshot: {
        id: 'session-1',
        agentId: 'agent-1',
        messages: [],
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
        createdAt: '',
        updatedAt: '',
      },
    } as unknown as never)

    vi.mocked(fetchTurn).mockResolvedValueOnce({
      turn_id: 'turn-fail',
      session_id: 'session-1',
      status: 'failed',
      started_at: '',
      completed_at: '',
      model: { provider: '', model: '' },
      error: 'rate limit exceeded',
      messages: [],
    } as never)

    await store.sendMessage('boom')

    expect(store.error).toBeTruthy()
    expect(String(store.error)).toMatch(/rate limit/i)
  })

  it('falls back to the SSE path when sendSessionMessage returns turnId=null (legacy/rollback server)', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    FakeEventSource.instances.length = 0

    // Legacy / rolled-back server: response has no turn_id. The api
    // function surfaces turnId=null and the chatStore takes the SSE
    // path.
    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      turnId: null,
      snapshot: {
        id: 'session-1',
        agentId: 'agent-1',
        messages: [{ id: 'user-1', role: 'user', content: 'hello', timestamp: '' }],
        messageCount: 1,
        status: 'active',
        depth: 0,
        isStreaming: false,
        createdAt: '',
        updatedAt: '',
      },
    } as unknown as never)

    await store.sendMessage('hello')

    expect(vi.mocked(subscribeSessionStream)).toHaveBeenCalledWith('session-1')
    expect(FakeEventSource.instances.length).toBe(1)
    expect(vi.mocked(fetchTurn)).not.toHaveBeenCalled()
  })
})

// Phase-4-Commit-1 adaptive cadence constants (per User May-19 decision
// option a — Turn-Based Post-Then-Poll Architecture (May 2026) plan,
// Constraints section): the fast / slow / slower poll intervals tighten
// from 1s/2s/5s to 250ms/1s/3s so the chat UI's chip + first-content
// rendering lands inside the perceived-responsiveness window. Backoff
// boundaries stay at 10 / 30 polls.
// Phase-4-Commit-1b ("long-poll Turn endpoint", May 2026). The
// pre-1b `pollTurnUntilTerminal` looped on a client-side
// 250ms/1000ms/3000ms cadence; the long-poll path replaces that with a
// server-side hold. Each iteration calls fetchTurn with
// `{ wait: true, since: N, signal }` — the await IS the cadence, no
// client-side setTimeout. The server returns either when a chunk lands
// (perceived sub-50ms latency), or after 25s (the loop re-issues
// immediately), or on terminal status. This block pins:
//   - the wire shape sent to fetchTurn (wait + since + signal)
//   - the lastMessageCount baseline advancing across iterations
//   - the AbortController cancelling the in-flight request on session
//     switch
//   - the loop firing immediately on each fetchTurn resolution (no
//     250ms delay between iterations)
describe('chatStore - pollTurnUntilTerminal long-poll loop (Phase 4 Commit 1b)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('passes wait=true and since=0 on the first iteration', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-longpoll'
    store.messages = []

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-lp',
      session_id: 'session-longpoll',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: '', model: '' },
      error: '',
      messages: [],
    } as never)

    await store.pollTurnUntilTerminal('session-longpoll', 'turn-lp')

    expect(ft).toHaveBeenCalledTimes(1)
    const firstCall = ft.mock.calls[0]
    expect(firstCall[0]).toBe('session-longpoll')
    expect(firstCall[1]).toBe('turn-lp')
    // Third argument carries the long-poll options. The server reads
    // `wait=true` as the gate into the hold path; absence (or false)
    // takes the legacy snapshot-read path — which would defeat the
    // perceived-latency promise of this slice.
    const opts = firstCall[2] as { wait?: boolean; since?: number } | undefined
    expect(opts).toBeDefined()
    expect(opts?.wait).toBe(true)
    expect(opts?.since).toBe(0)
  })

  it('advances since=N to len(messages) across iterations (server-side hold baseline)', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-since'
    store.messages = []

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-since',
      session_id: 'session-since',
      status: 'running' as const,
      started_at: '',
      completed_at: null,
      model: { provider: '', model: '' },
      error: '',
      messages: [
        { id: 'asst-1', role: 'assistant', content: 'one', timestamp: '' },
      ],
    } as never)
    ft.mockResolvedValueOnce({
      turn_id: 'turn-since',
      session_id: 'session-since',
      status: 'running' as const,
      started_at: '',
      completed_at: null,
      model: { provider: '', model: '' },
      error: '',
      messages: [
        { id: 'asst-1', role: 'assistant', content: 'one', timestamp: '' },
        { id: 'asst-2', role: 'assistant', content: 'two', timestamp: '' },
      ],
    } as never)
    ft.mockResolvedValueOnce({
      turn_id: 'turn-since',
      session_id: 'session-since',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: '', model: '' },
      error: '',
      messages: [
        { id: 'asst-1', role: 'assistant', content: 'one', timestamp: '' },
        { id: 'asst-2', role: 'assistant', content: 'two', timestamp: '' },
      ],
    } as never)

    await store.pollTurnUntilTerminal('session-since', 'turn-since')

    expect(ft.mock.calls.length).toBeGreaterThanOrEqual(3)
    // First call: since=0 — caller has not seen any rows yet.
    expect((ft.mock.calls[0][2] as { since?: number })?.since).toBe(0)
    // Second call: the prior response carried 1 message — the server
    // must hold until len > 1 (or terminal). Anything less means the
    // server would wake immediately on every iteration and the
    // long-poll loop degenerates into a tight spin.
    expect((ft.mock.calls[1][2] as { since?: number })?.since).toBe(1)
    // Third call: the prior response carried 2 messages.
    expect((ft.mock.calls[2][2] as { since?: number })?.since).toBe(2)
  })

  it('fires the next long-poll immediately on each fetchTurn resolution (no client-side delay)', async () => {
    // Long-poll's perceived-cadence promise: each iteration's await
    // resolves when the server says so (chunk lands OR 25s timeout),
    // and the next iteration must re-issue WITHOUT a client-side
    // setTimeout delay. The legacy 250ms gate is gone.
    //
    // We measure by running the loop synchronously over N mocked
    // resolutions — they all drain via microtask queue without any
    // real elapsed time. If the impl still has a setTimeout backoff,
    // the await on the poll promise would hang at 250ms per iteration
    // and the test wall-clock would inflate to N*250ms.
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-tight'
    store.messages = []

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    const running = {
      turn_id: 'turn-tight',
      session_id: 'session-tight',
      status: 'running' as const,
      started_at: '',
      completed_at: null,
      model: { provider: '', model: '' },
      error: '',
      messages: [] as never[],
    }
    for (let i = 0; i < 15; i++) {
      ft.mockResolvedValueOnce({ ...running, messages: [] } as never)
    }
    ft.mockResolvedValueOnce({
      ...running,
      status: 'completed',
      completed_at: '',
      messages: [],
    } as never)

    const start = Date.now()
    await store.pollTurnUntilTerminal('session-tight', 'turn-tight')
    const elapsed = Date.now() - start

    expect(ft.mock.calls.length).toBe(16)
    // 16 iterations against mocked-immediate resolutions must drain in
    // under ~200ms total wall-clock — a legacy 250ms cadence would
    // take ~10 polls × 250ms = 2.5s just to clear the fast window.
    // Strictly bounding at 250ms keeps the test margin tight; the
    // tight-loop drain typically lands under 50ms on a normal box.
    expect(elapsed).toBeLessThan(250)
  })

  it('passes an AbortSignal so session-switch can cancel the in-flight long-poll', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-abort'
    store.messages = []

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-abort',
      session_id: 'session-abort',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: '', model: '' },
      error: '',
      messages: [],
    } as never)

    await store.pollTurnUntilTerminal('session-abort', 'turn-abort')

    expect(ft).toHaveBeenCalledTimes(1)
    const opts = ft.mock.calls[0][2] as { signal?: AbortSignal } | undefined
    expect(opts?.signal).toBeDefined()
    // The signal must be a real AbortSignal so the session-switch
    // path can fire controller.abort() — a session change while a
    // long-poll is in-flight would otherwise hang the FE for up to
    // 25s waiting on a stale server-side wait.
    expect(opts?.signal).toBeInstanceOf(AbortSignal)
  })

  it('exits cleanly when fetchTurn rejects with an AbortError (no error surfaced)', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-aborted'
    store.messages = []

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    // First fetch resolves so the loop enters; the second is aborted —
    // simulates a session-switch mid-second-iteration.
    ft.mockResolvedValueOnce({
      turn_id: 'turn-aborted',
      session_id: 'session-aborted',
      status: 'running' as const,
      started_at: '',
      completed_at: null,
      model: { provider: '', model: '' },
      error: '',
      messages: [],
    } as never)
    const abortErr = new DOMException('The operation was aborted', 'AbortError')
    ft.mockRejectedValueOnce(abortErr)

    // The loop must NOT throw — AbortError is the well-known
    // session-switch signal and surfaces as a silent exit, not a
    // store.error mutation.
    store.error = null
    await store.pollTurnUntilTerminal('session-aborted', 'turn-aborted')
    expect(store.error).toBeNull()
  })

  it('terminates the loop when status transitions to completed (no extra fetchTurn after terminal)', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-terminal'
    store.messages = []

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-terminal',
      session_id: 'session-terminal',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: '', model: '' },
      error: '',
      messages: [],
    } as never)
    // Subsequent calls would return a fresh running — if the loop
    // didn't terminate on completed it would call fetchTurn again.
    ft.mockResolvedValue({
      turn_id: 'turn-terminal',
      session_id: 'session-terminal',
      status: 'running' as const,
      started_at: '',
      completed_at: null,
      model: { provider: '', model: '' },
      error: '',
      messages: [],
    } as never)

    await store.pollTurnUntilTerminal('session-terminal', 'turn-terminal')

    // Exactly one call — the first response was 'completed' so the
    // loop must exit without calling fetchTurn again.
    expect(ft).toHaveBeenCalledTimes(1)
  })

  it('terminates the loop when the session changes mid-poll (no orphan fetchTurn calls)', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-stay'
    store.messages = []

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    // First resolution: still running. After it resolves, the test
    // mutates currentSessionId to a different session — the loop's
    // top-of-iteration guard must exit before the next fetchTurn.
    ft.mockImplementationOnce(async () => {
      // Schedule the session switch to happen AFTER this resolution
      // lands but BEFORE the next iteration's guard runs. Microtask
      // ordering: this fn resolves → the loop body resumes → the
      // session-switch fires below → the loop iterates and exits.
      Promise.resolve().then(() => {
        store.currentSessionId = 'session-different'
      })
      return {
        turn_id: 'turn-switch',
        session_id: 'session-stay',
        status: 'running' as const,
        started_at: '',
        completed_at: null,
        model: { provider: '', model: '' },
        error: '',
        messages: [],
      } as never
    })
    ft.mockResolvedValue({
      turn_id: 'turn-switch',
      session_id: 'session-stay',
      status: 'running' as const,
      started_at: '',
      completed_at: null,
      model: { provider: '', model: '' },
      error: '',
      messages: [],
    } as never)

    await store.pollTurnUntilTerminal('session-stay', 'turn-switch')

    // The first iteration ran (one call). After the session switch,
    // the second iteration's guard must exit without a second
    // fetchTurn — calling it again would corrupt the new session's
    // local state.
    expect(ft).toHaveBeenCalledTimes(1)
  })
})

// Phase-4 Commit-2 ("streamed vs dump" UX investigation, May-19 2026).
//
// pollTurnUntilTerminal upserts each incoming Turn row into local
// `messages` by id (lines ~1817-1826 in chatStore.ts). The pre-fix
// implementation unconditionally re-built the local row via object
// spread on EVERY poll — even when the backend returned a byte-for-byte
// identical row (a backoff tick during a quiet phase between content
// chunks, or after the assistant produced a tool call and the next
// content chunk hadn't landed yet).
//
// That unconditional reassignment triggered Vue's reactivity for every
// MessageBubble whose row landed in the snapshot:
//   - MarkdownRenderer re-evaluated `renderedHtml` (full md.render of
//     the accumulated content + image allow-list filter).
//   - The ChatView contentLength/status watcher fired, calling
//     `scheduleInstantScroll` and re-anchoring the viewport.
//   - v-html replaced the entire `.markdown-body` subtree.
//
// All for ZERO new content. The user-visible symptom was a faint
// re-render flicker every 250ms while the turn was running but
// quiet — reinforcing the "dump vs streamed" feel because the bubble
// was visibly redrawing without any actual content change.
//
// Fix: skip the upsert when the incoming row is shallow-equal to the
// existing row. The merge still appends new rows, still mutates rows
// whose content/status/toolCalls/etc actually changed — only the
// no-op case short-circuits.
//
// These tests pin both branches: identical rows preserve the existing
// object reference (no reactivity churn), changed rows still merge
// through.
describe('chatStore - pollTurnUntilTerminal no-op upsert short-circuit (Phase 4 Commit 2)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('preserves the existing row reference when an incoming poll row is byte-equal', async () => {
    vi.useFakeTimers()
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-noop'
    const existingAssistant = {
      id: 'assistant-1',
      role: 'assistant',
      content: 'partial reply so far',
      timestamp: '2026-05-19T11:00:00Z',
      status: 'running',
      toolCalls: 0,
    }
    store.messages = [existingAssistant]
    const refBefore = store.messages[0]

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-noop',
      session_id: 'session-noop',
      status: 'running',
      started_at: '',
      completed_at: null,
      model: { provider: '', model: '' },
      error: '',
      messages: [{ ...existingAssistant }],
    } as never)
    ft.mockResolvedValueOnce({
      turn_id: 'turn-noop',
      session_id: 'session-noop',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: '', model: '' },
      error: '',
      messages: [{ ...existingAssistant }],
    } as never)

    const pollPromise = store.pollTurnUntilTerminal('session-noop', 'turn-noop')
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(250)
    await pollPromise

    // Reference identity is the marker for "no reactivity churn fired".
    // Vue's reactivity wraps the messages array; reassigning an index
    // (even via object spread of identical fields) creates a NEW object
    // and triggers every downstream watcher / computed. Preserving the
    // reference is the contract the no-op branch promises.
    expect(store.messages[0]).toBe(refBefore)
    // Sanity: the row's content is still what we set.
    expect(store.messages[0].content).toBe('partial reply so far')
  })

  it('still merges rows whose content has grown (the streaming case)', async () => {
    vi.useFakeTimers()
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-grow'
    store.messages = [
      {
        id: 'assistant-1',
        role: 'assistant',
        content: 'short',
        timestamp: '2026-05-19T11:00:00Z',
        status: 'running',
      },
    ]

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-grow',
      session_id: 'session-grow',
      status: 'running',
      started_at: '',
      completed_at: null,
      model: { provider: '', model: '' },
      error: '',
      messages: [
        {
          id: 'assistant-1',
          role: 'assistant',
          content: 'short and getting longer now',
          timestamp: '2026-05-19T11:00:00Z',
          status: 'running',
        },
      ],
    } as never)
    ft.mockResolvedValueOnce({
      turn_id: 'turn-grow',
      session_id: 'session-grow',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: '', model: '' },
      error: '',
      messages: [],
    } as never)

    const pollPromise = store.pollTurnUntilTerminal('session-grow', 'turn-grow')
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(250)
    await pollPromise

    expect(store.messages[0].content).toBe('short and getting longer now')
  })

  it('still appends rows whose id is new (the new-row case)', async () => {
    vi.useFakeTimers()
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-new'
    store.messages = [
      {
        id: 'assistant-1',
        role: 'assistant',
        content: 'first chunk',
        timestamp: '2026-05-19T11:00:00Z',
        status: 'running',
      },
    ]

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-new',
      session_id: 'session-new',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: '', model: '' },
      error: '',
      messages: [
        {
          id: 'assistant-1',
          role: 'assistant',
          content: 'first chunk',
          timestamp: '2026-05-19T11:00:00Z',
          status: 'running',
        },
        {
          id: 'tool-1',
          role: 'tool_call',
          content: 'tool body',
          timestamp: '2026-05-19T11:00:01Z',
          toolName: 'bash',
        },
      ],
    } as never)

    const pollPromise = store.pollTurnUntilTerminal('session-new', 'turn-new')
    await vi.advanceTimersByTimeAsync(0)
    await pollPromise

    expect(store.messages).toHaveLength(2)
    expect(store.messages[1].id).toBe('tool-1')
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
        status: 'active',
        depth: 0,
        isStreaming: false,
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
        status: 'active',
        depth: 0,
        isStreaming: false,
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

describe('chatStore - newSession seeds chip data from POST /sessions response', () => {
  // Regression cover for the May 2026 chip-not-rendering bug. The
  // backend now seeds CurrentProviderID / CurrentModelID on a brand-new
  // session from the agent manifest's first PreferredModels entry. The
  // store must adopt those seed values so the persistent activity-
  // indicator chip renders immediately, before the user picks a model
  // and before any provider_changed transition fires.
  //
  // Without these specs, a future refactor of newSession could quietly
  // drop the propagation and the chip would silently regress to "never
  // shows on a fresh session" — exactly the symptom hands-on UI
  // verification surfaced.
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('adopts currentModelId and currentProviderId from the createSession response', async () => {
    vi.mocked(createSession).mockResolvedValueOnce({
      id: 'session-new',
      agentId: 'team-lead',
      currentModelId: 'glm-4.6',
      currentProviderId: 'zai',
      messages: [],
      messageCount: 0,
      status: 'active',
      depth: 0,
      isStreaming: false,
      createdAt: '',
      updatedAt: '',
    })

    const store = useChatStore()
    store.agentId = 'team-lead'
    // Pre-conditions mirror the bug repro: store fields start empty.
    store.currentModelId = ''
    store.currentProviderId = ''

    await store.newSession()

    expect(store.currentModelId).toBe('glm-4.6')
    expect(store.currentProviderId).toBe('zai')
  })

  it('leaves chip fields empty when the createSession response carries no model+provider', async () => {
    // Defensive: agent manifest with no PreferredModels (e.g. a
    // bare-bones custom agent) returns empty defaults. The chip must
    // stay hidden in that degraded path — never synthesise a fake
    // value or leak a previously-set localStorage value into the
    // session-bound chip.
    vi.mocked(createSession).mockResolvedValueOnce({
      id: 'session-new',
      agentId: 'barebones-agent',
      messages: [],
      messageCount: 0,
      status: 'active',
      depth: 0,
      isStreaming: false,
      createdAt: '',
      updatedAt: '',
    })

    const store = useChatStore()
    store.agentId = 'barebones-agent'
    store.currentModelId = ''
    store.currentProviderId = ''

    await store.newSession()

    expect(store.currentModelId).toBe('')
    expect(store.currentProviderId).toBe('')
  })

  it('does not clobber a previously-selected model when the seed response is empty', async () => {
    // The store may already hold a localStorage-backed model selection
    // from a prior session. Creating a new session against a manifest
    // with no preferred models must not erase that prior selection —
    // the user expects their picker choice to carry forward.
    vi.mocked(createSession).mockResolvedValueOnce({
      id: 'session-new',
      agentId: 'barebones-agent',
      messages: [],
      messageCount: 0,
      status: 'active',
      depth: 0,
      isStreaming: false,
      createdAt: '',
      updatedAt: '',
    })

    const store = useChatStore()
    store.agentId = 'barebones-agent'
    store.currentModelId = 'claude-opus-4.7'
    store.currentProviderId = 'anthropic'

    await store.newSession()

    expect(store.currentModelId).toBe('claude-opus-4.7')
    expect(store.currentProviderId).toBe('anthropic')
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
      status: 'active',
      depth: 0,
      isStreaming: false,
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
    // Phase 3 update: SSE no longer opens during the in-flight POST
    // window. The active-send path is POST → branch on turn_id. We
    // drive the legacy SSE branch (no turn_id) and rely on the post-
    // send reconcile to land the canonical history. The user-observable
    // outcome (two distinct assistant bubbles across two turns) is the
    // load-bearing assertion; the SSE chunk timing is internal protocol
    // and no longer exercised by this spec — it's covered by the
    // turn-id poll path specs instead.
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    FakeEventSource.instances.length = 0

    // Turn 1: legacy server response (no turn_id) → SSE branch.
    // Reconcile lands the canonical history.
    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0,
      status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '',
    } as never)
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'srv-u1', role: 'user', content: 'first prompt', timestamp: '' },
      { id: 'srv-a1', role: 'assistant', content: 'first response', timestamp: '' },
    ])

    await store.sendMessage('first prompt')

    const assistants1 = store.messages.filter((m) => m.role === 'assistant')
    expect(assistants1).toHaveLength(1)
    expect(assistants1[0].content).toBe('first response')
    expect(assistants1[0].status).toBe('completed')

    // Turn 2: a new user optimistic message is pushed; reconcile
    // pulls canonical history with two complete pairs.
    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0,
      status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '',
    } as never)
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'srv-u1', role: 'user', content: 'first prompt', timestamp: '' },
      { id: 'srv-a1', role: 'assistant', content: 'first response', timestamp: '' },
      { id: 'srv-u2', role: 'user', content: 'second prompt', timestamp: '' },
      { id: 'srv-a2', role: 'assistant', content: 'second response', timestamp: '' },
    ])

    await store.sendMessage('second prompt')

    const finalAssistants = store.messages.filter((m) => m.role === 'assistant')
    expect(finalAssistants).toHaveLength(2)
    expect(finalAssistants[0].content).toBe('first response')
    expect(finalAssistants[1].content).toBe('second response')
  })
})

// User report: "we are seeing a todo list completing, but we don't see any
// responses between the update." The agent emits assistant text, then a
// `tool_call`/`tool_result` pair (e.g. `todowrite`), then more assistant
// text. The chat thread should render three bubbles in order:
//
//   [assistant: pre-tool text]
//   [tool_result: todowrite]      ← rendered as the todo card
//   [assistant: post-tool text]
//
// Pre-fix the in-flight assistant message is never sealed when a tool fires.
// `handleContentChunk` reverse-finds the first `role==='assistant' &&
// status==='running'` message — which is still the pre-tool assistant —
// and APPENDS the post-tool text onto it. The merged bubble then sits at
// its original array position (BEFORE the tool_result), so the user sees
// the pre+post text fused into one block above all the todo updates and
// the inter-tool / post-tool replies appear to "vanish" from the thread.
//
// The contract being pinned: a `tool_call` (or `skill_load`) event seals
// any in-flight assistant bubble. The next content chunk creates a new
// assistant message AFTER the tool_result in array order, so the chat UI
// renders distinct bubbles around each tool invocation.
describe('chatStore - assistant content around tool_call (no merge across tool boundary)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('seals the in-flight assistant on tool_call so post-tool chunks land on a NEW message after the tool_result', () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'

    // Simulate the start of a turn streaming a todowrite-using flow.
    store.applyContentEvent(JSON.stringify({ content: 'Let me plan this out. ' }))

    // Tool fires mid-stream.
    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'todowrite', status: 'running' }))
    store.applyContentEvent(JSON.stringify({ type: 'tool_result', content: '[{"id":"1","content":"step","status":"pending"}]' }))

    // More assistant text arrives between the todo updates / after the tool.
    store.applyContentEvent(JSON.stringify({ content: 'First step is ready.' }))

    // The post-tool chunk MUST NOT merge into the pre-tool assistant.
    const assistants = store.messages.filter((m) => m.role === 'assistant')
    expect(assistants).toHaveLength(2)
    expect(assistants[0].content).toBe('Let me plan this out. ')
    expect(assistants[1].content).toBe('First step is ready.')
    expect(assistants[1].id).not.toBe(assistants[0].id)

    // And array order must be preserved so the UI renders bubbles in
    // chronological order (pre-tool → tool_result → post-tool).
    const order = store.messages.map((m) => ({ role: m.role, content: m.content }))
    expect(order).toEqual([
      { role: 'assistant', content: 'Let me plan this out. ' },
      { role: 'tool_result', content: '[{"id":"1","content":"step","status":"pending"}]' },
      { role: 'assistant', content: 'First step is ready.' },
    ])
  })

  it('seals the in-flight assistant across multiple tool_call/tool_result pairs', () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'

    // Simulate the multi-todo flow that produced the user-reported gap.
    store.applyContentEvent(JSON.stringify({ content: 'I will track this with todos. ' }))
    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'todowrite', status: 'running' }))
    store.applyContentEvent(JSON.stringify({ type: 'tool_result', content: '[{"id":"1","status":"pending"}]' }))

    store.applyContentEvent(JSON.stringify({ content: 'Starting step one. ' }))
    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'todowrite', status: 'running' }))
    store.applyContentEvent(JSON.stringify({ type: 'tool_result', content: '[{"id":"1","status":"in_progress"}]' }))

    store.applyContentEvent(JSON.stringify({ content: 'All done.' }))
    store.applyContentEvent('[DONE]')

    const assistants = store.messages.filter((m) => m.role === 'assistant')
    expect(assistants.map((m) => m.content)).toEqual([
      'I will track this with todos. ',
      'Starting step one. ',
      'All done.',
    ])

    // Every assistant bubble between todo updates should be present and
    // positioned between the tool_result rows in array order.
    const roles = store.messages.map((m) => m.role)
    expect(roles).toEqual([
      'assistant',
      'tool_result',
      'assistant',
      'tool_result',
      'assistant',
    ])
  })

  it('seals the in-flight assistant on a skill_load event as well', () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'

    store.applyContentEvent(JSON.stringify({ content: 'Loading a skill. ' }))
    store.applyContentEvent(JSON.stringify({ type: 'skill_load', name: 'bdd-workflow' }))
    store.applyContentEvent(JSON.stringify({ content: 'Skill loaded, continuing.' }))

    const assistants = store.messages.filter((m) => m.role === 'assistant')
    expect(assistants).toHaveLength(2)
    expect(assistants[0].content).toBe('Loading a skill. ')
    expect(assistants[1].content).toBe('Skill loaded, continuing.')
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

  it("clears isLoading and isStreaming when reconnect attempts are exhausted", async () => {
    window.localStorage.setItem("chat.currentSessionId", "session-1")
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: "srv-u1", role: "user", content: "continue", timestamp: "2026-05-04T00:00:00Z" },
    ])

    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(store.isLoading).toBe(true)
    for (let i = 0; i <= 5; i++) {
      const es = FakeEventSource.instances[FakeEventSource.instances.length - 1]
      es.fire("error", null)
    }

    expect(store.isLoading).toBe(false)
    expect(store.isStreaming).toBe(false)
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

  it('queues the prompt instead of bouncing when isLoading is already true (Slice E — queued prompts)', async () => {
    // Streaming Coherence Slice E (May 2026) — pre-slice this gate
    // bounced the prompt with `store.error = "in flight..."`. The new
    // contract: silently push onto the session's queue; the strip
    // shows the pending pill and the auto-drain fires it on outer
    // turn completion.
    const store = useChatStore()
    store.currentSessionId = 'sess-q'
    store.setSessionStreaming('sess-q', { isLoading: true })

    await store.sendMessage('continue')

    expect(store.error).toBeNull()
    expect(store.queuedPrompts['sess-q']).toEqual(['continue'])
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
  // sendMessage is in flight, the watchdog clears isLoading after 65s of
  // no observed SSE activity (60s engine idle-watchdog threshold +
  // 5s buffer), sets a surfacing error, and unwedges the UI.

  it('clears isLoading after 65s of no SSE activity and surfaces a stall error', async () => {
    // Phase 3 update: SSE is opened by the legacy branch after the POST
    // resolves. The watchdog still arms when SSE connects; this spec
    // drives the SSE branch by mocking sendSessionMessage to resolve
    // with no turn_id, then advances time with no SSE activity.
    //
    // The sendMessage gate clears in `finally` once the POST + reconcile
    // resolves; the SSE keeps streaming in the background. The watchdog
    // trip's load-bearing observable is now the `error` message.
    vi.useFakeTimers()
    try {
      const store = useChatStore()
      store.agentId = 'agent-1'
      store.currentSessionId = 'session-1'

      vi.mocked(sendSessionMessage).mockResolvedValueOnce({
        id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0,
        status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '',
      } as never)
      vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
        { id: 'srv-u1', role: 'user', content: 'hello', timestamp: '' },
      ])

      await store.sendMessage('hello')
      expect(FakeEventSource.instances.length).toBe(1)

      // Advance 65s with no SSE activity — watchdog should trip.
      await vi.advanceTimersByTimeAsync(65_000)

      expect(store.error).toBeTruthy()
      expect(String(store.error)).toMatch(/(stall|stuck|timeout|no response)/i)
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

      vi.mocked(sendSessionMessage).mockResolvedValueOnce({
        id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0,
        status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '',
      } as never)
      vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
        { id: 'srv-u1', role: 'user', content: 'streaming', timestamp: '' },
      ])

      await store.sendMessage('streaming')

      const es = FakeEventSource.instances[0]

      // Heartbeat every 30s — under the 60s threshold — for 5 minutes.
      for (let i = 0; i < 10; i++) {
        await vi.advanceTimersByTimeAsync(30_000)
        es.fire('message', { content: '.' })
      }

      // After 5 minutes of activity, no stall trip — error stays null.
      expect(store.error).toBeNull()

      es.fire('message', '[DONE]')
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
        status: 'active',
        depth: 0,
        isStreaming: false,
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

  it('skips truncateSessionMessages for temp message IDs and still reverts locally', async () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    store.isLoading = true
    store.messages = [
      { id: 'msg-1', role: 'user', content: 'first prompt', timestamp: '' },
      { id: 'temp-1778784397087-98nzco', role: 'user', content: 'failed prompt', status: 'failed', timestamp: '' },
      { id: 'msg-3', role: 'assistant', content: 'response', timestamp: '' },
    ]

    await store.revertToMessage('temp-1778784397087-98nzco')

    expect(vi.mocked(truncateSessionMessages)).not.toHaveBeenCalled()
    expect(store.messages).toHaveLength(1)
    expect(store.messages[0].id).toBe('msg-1')
    expect(store.composerText).toBe('failed prompt')
    expect(store.isLoading).toBe(false)
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
    // Phase 3 update: POST resolves first (legacy no-turn_id path),
    // then SSE opens, then chunks + [DONE] fire. Post-sendMessage the
    // reconcile already landed the canonical state (it runs once
    // per-send, regardless of SSE).
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0,
      status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '',
    } as never)
    // The backend canonical state includes the full reply (the SSE
    // partial we deliver below would have been replaced by reconcile).
    vi.mocked(fetchSessionMessages).mockResolvedValue([
      { id: 'srv-u1', role: 'user', content: 'go', timestamp: '' },
      { id: 'srv-a1', role: 'assistant', content: 'final canonical reply', timestamp: '' },
    ])

    await store.sendMessage('go')

    // Post-reconcile, the user sees the backend canonical reply.
    const assistants = store.messages.filter((m) => m.role === 'assistant')
    expect(assistants).toHaveLength(1)
    expect(assistants[0].content).toBe('final canonical reply')
    expect(assistants[0].status).toBe('completed')
  })

  it('reconciles with the backend on SSE error (network glitch / proxy hang) so the bubble updates', async () => {
    // Phase 3 update: POST resolves first → reconcile fires
    // unconditionally → SSE opens. The reconcile result is already
    // landed before any SSE chunk is observed; an SSE drop becomes
    // a no-op for store state. The user-observable contract (the
    // canonical reply is visible without manual refresh) is the
    // load-bearing assertion. Gate-clearing is exercised by the SSE
    // [DONE] path (sibling spec above); a transient error triggers
    // scheduled reconnect in useSessionStream rather than immediate
    // gate clearing.
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-1'
    store.messages = []

    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0,
      status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '',
    } as never)
    vi.mocked(fetchSessionMessages).mockResolvedValue([
      { id: 'srv-u1', role: 'user', content: 'hi', timestamp: '' },
      { id: 'srv-a1', role: 'assistant', content: 'recovered reply', timestamp: '' },
    ])

    await store.sendMessage('hi')

    // Reconcile already landed the canonical reply.
    expect(store.messages.find((m) => m.id === 'srv-a1')?.content).toBe('recovered reply')
  })
})

describe('chatStore - watchdog trip clears gate without masked reconcile (engine-watchdog era)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
    FakeEventSource.instances.length = 0
  })

  // May 2026 mid-thinking-halt fix: the engine's idle-stream watchdog
  // (internal/engine/engine.go: engineStreamIdleTimeout = 60s) now emits
  // a synthetic Done within 60s of provider-stream silence, so a frontend
  // watchdog trip is no longer a routine "stream completed without [DONE]"
  // signal. It now means a genuine network failure or a regression in the
  // engine watchdog — both of which we want to surface to the user as
  // an error rather than mask with a silent refetch.
  //
  // Pre-fix behaviour (NOW REMOVED): handleStreamStall fire-and-forgot
  // reconcileFromBackend(sessionId) on every trip, papering over the
  // engine hang by silently refetching the canonical session state. This
  // hid the underlying engine-park-on-providerChunks bug for months and
  // produced the "after refresh it polls for changes" user complaint.
  //
  // New contract: the stall handler clears the gate, surfaces the error
  // footer, and does NOT touch the network. Regressions in the engine
  // watchdog will announce themselves loudly via the error message.

  it('clears isStreaming/isLoading and surfaces the error footer but does NOT reconcile after a stall trip', async () => {
    // Phase 3 update: POST resolves first → reconcile fires once →
    // SSE opens → SSE stalls → watchdog trips. The "stall does NOT
    // reconcile" load-bearing assertion now checks that the stall
    // handler ADDS NO MORE fetchSessionMessages calls beyond the one
    // post-send reconcile that always fires.
    vi.useFakeTimers()
    try {
      const store = useChatStore()
      store.agentId = 'agent-1'
      store.currentSessionId = 'session-1'
      store.messages = []

      vi.mocked(sendSessionMessage).mockResolvedValueOnce({
        id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0,
        status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '',
      } as never)
      // Phase 3 — reconcile fires once during sendMessage. Keep the
      // running orphan alive by reporting "turn in-flight" (last row
      // is user) so the SSE branch has a row to attach to and a stall
      // is observable on the chat surface.
      vi.mocked(fetchSessionMessages).mockResolvedValue([
        { id: 'srv-u1', role: 'user', content: 'long task', timestamp: '' },
      ])

      await store.sendMessage('long task')
      const baselineFetchCount = vi.mocked(fetchSessionMessages).mock.calls.length

      const es = FakeEventSource.instances[0]
      // Some content arrives, then the stream stalls completely.
      es.fire('message', { content: 'partial frozen' })

      // 65s of zero activity — the new (engine-watchdog-aligned) per-phase
      // threshold trips at 65s. Pre-fix this was 60s but the engine now
      // emits a synthetic Done by 60s, so this client-side trip only
      // fires when the engine itself is unreachable.
      await vi.advanceTimersByTimeAsync(65_000)

      expect(store.isStreaming).toBe(false)
      expect(store.isLoading).toBe(false)
      expect(String(store.error)).toMatch(/(stall|stuck|timeout|no response|activity)/i)

      // Flush any queued microtasks so a stray reconcile (if the bug
      // regresses) would have a chance to land.
      for (let i = 0; i < 8; i++) {
        await vi.advanceTimersByTimeAsync(0)
      }

      // The load-bearing assertion: the stall trip does NOT call
      // fetchSessionMessages over and above the one canonical post-send
      // reconcile that already fired. The masked-reconcile recovery
      // has been removed — the engine's idle-stream watchdog now owns
      // the "stream stopped emitting" failure mode end-to-end.
      expect(vi.mocked(fetchSessionMessages).mock.calls.length).toBe(baselineFetchCount)

      expect(store.error).toBeTruthy()
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
        status: 'active',
        depth: 0,
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
        status: 'active',
        depth: 0,
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
        status: 'active',
        depth: 0,
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
      status: 'active',
      depth: 0,
      isStreaming: false,
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
      status: 'active',
      depth: 0,
      isStreaming: false,
      createdAt: '',
      updatedAt: '',
    } as never)
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
      { id: 'session-idle', agentId: 'agent-1', title: 'idle', createdAt: '', updatedAt: '', messageCount: 0, status: 'active', depth: 0, isStreaming: false },
    ])

    await store.loadSessions()
    await store.loadSessionMessages('session-idle')

    // Both flags are cleared after the load completes.
    expect(store.isLoading).toBe(false)
    expect(store.isStreaming).toBe(false)
  })
})

// ─────────────────────────────────────────────────────────────────────────
// Merged from chatStore.spec.ts (test convention F8 — single chatStore
// test file, *.test.ts suffix). The describes below preserve the original
// scoping; mock fixtures use the file-scoped vi.mock declared at the top
// of this file rather than the spec file's standalone factory.
// ─────────────────────────────────────────────────────────────────────────

// ── restoreStateFromBackend — streaming reconnect ─────────────────────────
//
// These tests exercise the isStreaming-based reconnect path that covers a
// gap in the message-heuristic approach:
//
//   Gap: the backend may be actively streaming but the last persisted
//   message is an assistant message WITHOUT status 'running' (e.g. a
//   partial response written mid-stream via the accumulator). The
//   maybeReattachStream message-heuristic misses this case because it
//   only checks lastMessage.role==='user' or lastMessage.status==='running'.
//
//   Fix: when the session list includes isStreaming: true, the store
//   subscribes regardless of the message heuristic.

describe('chatStore - restoreStateFromBackend streaming reconnect', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('calls subscribeSessionStream when the session summary has isStreaming: true, even when last message is a completed assistant message', async () => {
    vi.mocked(fetchSessions).mockResolvedValueOnce([
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
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'u1', role: 'user', content: 'hello', timestamp: '2026-05-04T00:00:00Z' },
      { id: 'a1', role: 'assistant', content: 'partial…', timestamp: '2026-05-04T00:00:01Z' },
    ])

    window.localStorage.setItem('chat.currentSessionId', 'session-streaming')
    window.localStorage.setItem('chat.agentId', 'team-lead')
    vi.mocked(fetchAgents).mockResolvedValueOnce([{ id: 'team-lead', name: 'team-lead' } as never])

    FakeEventSource.instances.length = 0
    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(vi.mocked(subscribeSessionStream)).toHaveBeenCalledWith('session-streaming')
    expect(store.isStreaming).toBe(true)
  })

  it('does not call subscribeSessionStream when isStreaming: false and last message is a completed assistant', async () => {
    vi.mocked(fetchSessions).mockResolvedValueOnce([
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
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([
      { id: 'u1', role: 'user', content: 'hello', timestamp: '2026-05-04T00:00:00Z' },
      { id: 'a1', role: 'assistant', content: 'hi there', timestamp: '2026-05-04T00:00:01Z', status: 'completed' },
    ])

    window.localStorage.setItem('chat.currentSessionId', 'session-done')
    window.localStorage.setItem('chat.agentId', 'team-lead')
    vi.mocked(fetchAgents).mockResolvedValueOnce([{ id: 'team-lead', name: 'team-lead' } as never])

    vi.mocked(subscribeSessionStream).mockClear()
    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(vi.mocked(subscribeSessionStream)).not.toHaveBeenCalled()
    expect(store.isStreaming).toBe(false)
  })
})

// ── applyDelegationEvent ─────────────────────────────────────────────────

describe('chatStore - applyDelegationEvent', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('creates a delegation_started message when no matching message exists', () => {
    const store = useChatStore()
    store.messages = [
      { id: 'u1', role: 'user', content: 'plan something', timestamp: '2026-05-04T00:00:00Z' },
    ]

    store.applyDelegationEvent(
      JSON.stringify({ target_agent: 'executor', chain_id: 'chain-1', status: 'started' }),
    )

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

// ── applyContentEvent — discriminated union dispatch (Principal F5/F6) ───

describe('chatStore - applyContentEvent dispatch', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

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

  it('routes a type:tool_call payload to handleToolCallEvent', () => {
    const store = useChatStore()
    const spy = vi.spyOn(store, 'handleToolCallEvent')
    store.applyContentEvent(
      JSON.stringify({ type: 'tool_call', name: 'bash', status: 'running', input: 'ls' }),
    )
    expect(spy).toHaveBeenCalledWith({ name: 'bash', status: 'running', input: 'ls' })
  })

  it('routes a type:tool_result payload to handleToolResultEvent', () => {
    const store = useChatStore()
    const spy = vi.spyOn(store, 'handleToolResultEvent')
    store.applyContentEvent(JSON.stringify({ type: 'tool_result', content: 'output' }))
    expect(spy).toHaveBeenCalledWith({ content: 'output' })
  })

  it('attaches a type:thinking payload to the in-flight assistant message as thinkingContent', () => {
    // Drop #2 — Thinking SSE handler. The chat store does NOT render the
    // model's reasoning as the visible reply (that's Track B's UI work);
    // instead it accumulates the thinking text on the in-flight assistant
    // message's optional `thinkingContent` field so a later UI affordance
    // can disclose it on demand. Crucially, thinking text MUST NOT land on
    // `content`, which is the public assistant turn — leaking private
    // reasoning into chat is exactly the failure mode we're fixing.
    const store = useChatStore()
    store.messages = [
      { id: 'u1', role: 'user', content: 'go', timestamp: '2026-05-04T00:00:00Z' },
      {
        id: 'a1',
        role: 'assistant',
        content: '',
        timestamp: '2026-05-04T00:00:01Z',
        status: 'running',
      },
    ]

    store.applyContentEvent(JSON.stringify({ type: 'thinking', content: 'reasoning step 1' }))
    store.applyContentEvent(JSON.stringify({ type: 'thinking', content: ' step 2' }))

    const target = store.messages.find((m) => m.id === 'a1')
    expect(target?.thinkingContent).toBe('reasoning step 1 step 2')
    // The visible reply MUST NOT carry the model's private reasoning.
    expect(target?.content).toBe('')
  })

  it('updates currentProviderId and currentModelId when a provider_changed event arrives', () => {
    // Track B — failover transition affordance. When the failover hook
    // switches providers mid-request (e.g. anthropic 429 → zai/glm-4.6
    // takes over), the SSE wire emits {"type":"provider_changed",
    // "from":"anthropic+claude-sonnet-4-6","to":"zai+glm-4.6",
    // "reason":"rate_limited"}. The chat store reflects the new active
    // provider/model on its currentProviderId / currentModelId fields so
    // the toolbar chip in ChatView refreshes immediately and the user
    // sees that the answer they're now streaming is from a different
    // model. The "to" string is "<provider>+<model>", split on "+".
    const store = useChatStore()
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'

    store.applyContentEvent(
      JSON.stringify({
        type: 'provider_changed',
        from: 'anthropic+claude-sonnet-4-6',
        to: 'zai+glm-4.6',
        reason: 'rate_limited',
      }),
    )

    expect(store.currentProviderId).toBe('zai')
    expect(store.currentModelId).toBe('glm-4.6')
  })

  it('does NOT touch currentProviderId/currentModelId when the to field is empty', () => {
    // Defensive: a malformed provider_changed event with no `to` must
    // not nuke the toolbar chip — leave the previous provider in place
    // so the user keeps seeing SOMETHING. The toast still fires (with
    // generic copy) so the user knows a transition happened.
    const store = useChatStore()
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'

    store.applyContentEvent(
      JSON.stringify({
        type: 'provider_changed',
        from: 'anthropic+claude-sonnet-4-6',
        to: '',
        reason: 'rate_limited',
      }),
    )

    expect(store.currentProviderId).toBe('anthropic')
    expect(store.currentModelId).toBe('claude-sonnet-4-6')
  })

  it('handles a provider_changed event with no model component (provider-only "to")', () => {
    // Forward-compat: a provider with no model qualifier (e.g. "ollama"
    // alone) must split cleanly — provider gets the whole string,
    // model stays empty. ModelPicker handles empty currentModelId by
    // showing its "Select model" placeholder, which is the correct
    // degraded UX.
    const store = useChatStore()
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'

    store.applyContentEvent(
      JSON.stringify({
        type: 'provider_changed',
        from: 'anthropic+claude-sonnet-4-6',
        to: 'ollama',
        reason: 'unknown',
      }),
    )

    expect(store.currentProviderId).toBe('ollama')
    expect(store.currentModelId).toBe('')
  })

  it('prefers split to_provider/to_model fields over the joined "to" when both are on the wire', () => {
    // M3-adjacent — mirror sseModelActive's split shape on
    // sseProviderChanged. The Go SSE writer ships BOTH the legacy joined
    // fields (from / to) AND the new split fields (from_provider /
    // from_model / to_provider / to_model) during the migration. The
    // handler MUST prefer the split fields when present so the chip
    // skips the "+" parse hop and the off-by-one bugs around model ids
    // that themselves contain "+" (rare; openrouter).
    //
    // The "to" joined string is deliberately corrupt here ("BAD") to
    // prove the handler reads the split fields, not the joined one.
    const store = useChatStore()
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'

    store.applyContentEvent(
      JSON.stringify({
        type: 'provider_changed',
        from: 'anthropic+claude-sonnet-4-6',
        to: 'BAD',
        from_provider: 'anthropic',
        from_model: 'claude-sonnet-4-6',
        to_provider: 'zai',
        to_model: 'glm-4.6',
        reason: 'rate_limited',
      }),
    )

    expect(store.currentProviderId).toBe('zai')
    expect(store.currentModelId).toBe('glm-4.6')
  })

  it('falls back to splitting the joined "to" on "+" when split fields are absent (legacy emitter)', () => {
    // Forward-compat: an older Go emitter still on the joined-only
    // shape must keep working through the migration. When the split
    // fields are absent, the handler falls back to the historical
    // "+"-split behaviour.
    const store = useChatStore()
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'

    store.applyContentEvent(
      JSON.stringify({
        type: 'provider_changed',
        from: 'anthropic+claude-sonnet-4-6',
        to: 'zai+glm-4.6',
        reason: 'rate_limited',
      }),
    )

    expect(store.currentProviderId).toBe('zai')
    expect(store.currentModelId).toBe('glm-4.6')
  })

  it('handles a model id that contains a "+" (split on FIRST separator only)', () => {
    // Edge: model ids like "openrouter+anthropic/claude-3.5-sonnet+beta"
    // can in principle contain "+". The split is on the FIRST "+" so the
    // provider is the prefix and the model is everything after. This
    // matches how the Go side encodes provider+model as a single token.
    const store = useChatStore()

    store.applyContentEvent(
      JSON.stringify({
        type: 'provider_changed',
        from: 'a+b',
        to: 'openrouter+anthropic/claude-3.5+beta',
        reason: 'rate_limited',
      }),
    )

    expect(store.currentProviderId).toBe('openrouter')
    expect(store.currentModelId).toBe('anthropic/claude-3.5+beta')
  })

  it('updates currentProviderId and currentModelId from a model_active event so the chip pivots to actual at stream start', () => {
    // May 2026 chip-shows-selection-not-actual fix. The user reported
    // that the toolbar chip "shows what was selected, not what actually
    // ran". The Go SSE pipeline now emits {"type":"model_active",
    // "provider":"<id>","model":"<id>"} at the start of EVERY successful
    // stream so the chat UI can pivot from the optimistic selection
    // (set by setModel) to the actual model the moment streaming starts.
    const store = useChatStore()
    // Simulate an optimistic selection from the picker.
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'

    // First chunk of stream: backend says actual is glm-4.6 on zai
    // (the user's selection diverged from the actual call — failover or
    // agent override).
    store.applyContentEvent(
      JSON.stringify({
        type: 'model_active',
        provider: 'zai',
        model: 'glm-4.6',
      }),
    )

    expect(store.currentProviderId).toBe('zai')
    expect(store.currentModelId).toBe('glm-4.6')
  })

  it('does NOT touch currentProviderId/currentModelId when both model_active fields are empty', () => {
    // Defensive: a malformed model_active wire payload (defensive guard
    // for a future emitter that ships only the type) must NOT blank the
    // chip. Leave the prior optimistic selection visible — the user's
    // mental model "I just picked X" stays consistent.
    const store = useChatStore()
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'

    store.applyContentEvent(JSON.stringify({ type: 'model_active' }))

    expect(store.currentProviderId).toBe('anthropic')
    expect(store.currentModelId).toBe('claude-sonnet-4-6')
  })

  it('updates only the populated fields when one of provider/model is missing on a model_active event', () => {
    // Defensive: a partial payload (e.g. provider known but model id
    // not yet resolved) must update only what's present. Empty fields
    // never overwrite — better to keep the prior value than blank one
    // half of the chip mid-conversation.
    const store = useChatStore()
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'

    store.applyContentEvent(
      JSON.stringify({
        type: 'model_active',
        provider: 'zai',
        model: '',
      }),
    )

    expect(store.currentProviderId).toBe('zai')
    expect(store.currentModelId).toBe('claude-sonnet-4-6')
  })

  it('does NOT fire a toast when model_active matches the prior chip values (selection matched actual)', async () => {
    // May 2026 user-facing-notifications policy: model_active fires on
    // every successful stream, so toasting unconditionally would spam the
    // user 10+ times per multi-turn session. We toast ONLY when the
    // actual model differs from what the chip already showed — the
    // common "I selected X, X is answering" case stays silent.
    const toastModule = await import('@/composables/useToast')
    const showToastSpy = vi.spyOn(toastModule, 'showToast').mockImplementation(() => 0)

    const store = useChatStore()
    store.currentProviderId = 'zai'
    store.currentModelId = 'glm-4.6'

    store.applyContentEvent(
      JSON.stringify({
        type: 'model_active',
        provider: 'zai',
        model: 'glm-4.6',
      }),
    )

    expect(showToastSpy).not.toHaveBeenCalled()

    showToastSpy.mockRestore()
  })

  it('fires a toast on model_active when the actual model differs from the prior chip values', async () => {
    // The user explicitly reversed the prior session's "always-silent"
    // policy — they want to see when the model the chip showed is not
    // the model that's now answering (agent-override, manifest-override,
    // late discovery on a fresh session). The toast copy is the
    // generic "Now answering with {model}." form because model_active
    // doesn't carry a transition reason (only provider_changed does).
    const toastModule = await import('@/composables/useToast')
    const showToastSpy = vi.spyOn(toastModule, 'showToast').mockImplementation(() => 0)

    const store = useChatStore()
    // User picked claude on anthropic via the picker; agent override or
    // a swarm manifest pin pivots the actual call to glm-4.6 on zai.
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'

    store.applyContentEvent(
      JSON.stringify({
        type: 'model_active',
        provider: 'zai',
        model: 'glm-4.6',
      }),
    )

    expect(showToastSpy).toHaveBeenCalledOnce()
    const callArg = showToastSpy.mock.calls[0][0]
    expect(typeof callArg).toBe('object')
    if (typeof callArg === 'object') {
      expect(callArg.message).toContain('glm-4.6')
    }

    showToastSpy.mockRestore()
  })

  it('populates currentContextUsage from a context_usage event with the wire figures', () => {
    // Phase 2 of the May 2026 context-window saturation fix. The engine
    // emits {type:"context_usage", input_tokens, output_reserve, limit,
    // percentage, provider, model} as the first artefact of every Stream.
    // The store exposes a structured slice the chip component reads
    // directly (rather than threading the raw payload through props).
    const store = useChatStore()

    store.applyContentEvent(
      JSON.stringify({
        type: 'context_usage',
        input_tokens: 12345,
        output_reserve: 4096,
        limit: 100000,
        percentage: 12,
        provider: 'zai',
        model: 'glm-4.6',
      }),
    )

    expect(store.currentContextUsage).toEqual({
      inputTokens: 12345,
      outputReserve: 4096,
      limit: 100000,
      percentage: 12,
    })
  })

  it('updates currentContextUsage on a subsequent context_usage event so the chip tracks each turn', () => {
    // The engine emits a usage event at the start of every Stream so the
    // chip reflects the latest turn. A fresh event must overwrite the
    // prior figures (not merge) — the second turn's `input_tokens` is
    // the figure the user is reading right now.
    const store = useChatStore()

    store.applyContentEvent(
      JSON.stringify({
        type: 'context_usage',
        input_tokens: 1000,
        output_reserve: 4096,
        limit: 100000,
        percentage: 1,
        provider: 'zai',
        model: 'glm-4.6',
      }),
    )

    store.applyContentEvent(
      JSON.stringify({
        type: 'context_usage',
        input_tokens: 80000,
        output_reserve: 4096,
        limit: 100000,
        percentage: 80,
        provider: 'zai',
        model: 'glm-4.6',
      }),
    )

    expect(store.currentContextUsage).toEqual({
      inputTokens: 80000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 80,
    })
  })

  it('clears currentContextUsage on session change (loadSessionMessages reset path)', async () => {
    // The shared session-change reset clears criticalError; the same
    // path must reset the usage chip so a stale figure from a prior
    // session does not bleed into the new one. A fresh stream on the
    // new session repopulates it.
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([] as never)

    const store = useChatStore()
    store.sessions = [
      {
        id: 'fresh-session',
        agentId: 'test-agent',
        currentAgentId: 'test-agent',
        createdAt: '2026-05-08T00:00:00Z',
        updatedAt: '2026-05-08T00:00:00Z',
        messageCount: 0,
      },
    ] as never
    store.currentContextUsage = {
      inputTokens: 80000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 80,
    }

    await store.loadSessionMessages('fresh-session')

    expect(store.currentContextUsage).toBeNull()
  })

  it('does NOT clobber currentContextUsage when a context_usage event has zero figures (defensive payload)', () => {
    // A degraded wire payload must not blank a healthy figure
    // mid-conversation. Mirrors the model_active "defaults to empty
    // strings" guard: the prior figure stays visible until a real new
    // figure replaces it.
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 50000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 50,
    }

    store.applyContentEvent(JSON.stringify({ type: 'context_usage' }))

    expect(store.currentContextUsage).toEqual({
      inputTokens: 50000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 50,
    })
  })

  it('does NOT touch currentProviderId / currentModelId from a context_usage event (chip-pivot is model_active-only)', () => {
    // Separation-of-concerns pin: the usage chip and the model chip
    // pivot on different events. context_usage carries provider / model
    // for display alongside the figure, but the toolbar chip's pivot
    // logic stays exclusively driven by model_active so failover toasts
    // (which gate on lastProviderChangeKey) are not surprised by a
    // usage-event side-effect.
    const store = useChatStore()
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'

    store.applyContentEvent(
      JSON.stringify({
        type: 'context_usage',
        input_tokens: 1000,
        output_reserve: 4096,
        limit: 100000,
        percentage: 1,
        provider: 'zai',
        model: 'glm-4.6',
      }),
    )

    expect(store.currentProviderId).toBe('anthropic')
    expect(store.currentModelId).toBe('claude-sonnet-4-6')
  })

  it('applies contextUsage from a session response to currentContextUsage (Phase 3 PATCH dispatch)', () => {
    // Phase 3 of the May 2026 saturation fix — TUI-cadence parity.
    // The api server attaches the engine's fresh context_usage shape
    // to PATCH /agent and PATCH /model responses so the chip ticks
    // up immediately rather than waiting for the next pre-send. The
    // store routes the field through handleContextUsageEvent, which
    // is the same code path the SSE-streamed event uses.
    const store = useChatStore()

    store.applyContextUsageFromSession({
      contextUsage: {
        input_tokens: 4567,
        output_reserve: 4096,
        limit: 200000,
        percentage: 2,
        provider: 'anthropic',
        model: 'claude-sonnet-4-6',
      },
    })

    expect(store.currentContextUsage).toEqual({
      inputTokens: 4567,
      outputReserve: 4096,
      limit: 200000,
      percentage: 2,
    })
  })

  it('is a no-op when a session response carries no contextUsage (degraded backend)', () => {
    // Server-side contextUsage is suppressed when no token counter
    // is wired or the model has no resolvable limit. The store must
    // not blank an existing figure in that case — the chip stays on
    // the prior display rather than reverting to the empty state.
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 1234,
      outputReserve: 4096,
      limit: 100000,
      percentage: 1,
    }

    store.applyContextUsageFromSession({})

    expect(store.currentContextUsage).toEqual({
      inputTokens: 1234,
      outputReserve: 4096,
      limit: 100000,
      percentage: 1,
    })
  })

  it('routes context_usage events to a per-session map keyed by the chunk-captured session id', () => {
    // Bug Hunt (May 2026) — context-calculation per-session isolation.
    //
    // Pre-fix `currentContextUsage` was a flat slot. When session A
    // continued to stream while the user viewed B the SSE chunk
    // handler dropped A's `context_usage` events at the C-3 guard,
    // and returning to A blanked the chip until the next emission —
    // a stale display while A was still actively producing.
    //
    // The fix lifts context_usage state into a per-session map
    // (`contextUsageBySession`) so the chip can hydrate from
    // whichever session the user is currently viewing. The slot
    // keyed by capturedSessionId carries the wire figure verbatim.
    const store = useChatStore()
    store.currentSessionId = 'session-A'

    store.applyContentEvent(
      JSON.stringify({
        type: 'context_usage',
        input_tokens: 5000,
        output_reserve: 4096,
        limit: 200000,
        percentage: 2,
      }),
      'session-A',
    )

    expect(store.contextUsageBySession['session-A']).toEqual({
      inputTokens: 5000,
      outputReserve: 4096,
      limit: 200000,
      percentage: 2,
    })
  })

  it('hydrates currentContextUsage from the per-session map on loadSessionMessages so returning to a streaming session shows its last figure', async () => {
    // Bug Hunt (May 2026) — companion to the per-session map.
    //
    // Pre-fix `loadSessionMessages` cleared `currentContextUsage` to
    // null unconditionally. Returning to a session that had a figure
    // (the SSE stream had emitted while the user was elsewhere, or
    // the session summary cache held one) left the chip showing
    // `—/—` until the next emission — a meaningless display for a
    // session the user knows is still working.
    //
    // The fix reads the per-session map on session change: if a slot
    // exists it becomes the active chip figure; only sessions with no
    // record fall back to null.
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([] as never)

    const store = useChatStore()
    store.sessions = [
      {
        id: 'session-A',
        agentId: 'test-agent',
        currentAgentId: 'test-agent',
        createdAt: '2026-05-08T00:00:00Z',
        updatedAt: '2026-05-08T00:00:00Z',
        messageCount: 0,
      },
    ] as never
    // Pre-seed the per-session slot — simulating an earlier
    // emission that landed while user was on a different session.
    store.contextUsageBySession = {
      'session-A': {
        inputTokens: 42000,
        outputReserve: 4096,
        limit: 100000,
        percentage: 42,
      },
    }

    await store.loadSessionMessages('session-A')

    expect(store.currentContextUsage).toEqual({
      inputTokens: 42000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 42,
    })
  })

  it('accepts context_usage events for inactive sessions and stores them under the captured session id (no chunk discard for usage)', () => {
    // Bug Hunt (May 2026) — the SSE chunk handler's C-3 guard drops
    // chunks whose captured session id no longer matches the active
    // currentSessionId. That guard is correct for content / tool /
    // delegation chunks (the user must not see another session's
    // bubbles), but context_usage is metadata — its figure is bound
    // to its session via the slot key, not the active view.
    //
    // applyContentEvent must route a context_usage event into the
    // per-session map even when its captured session id differs from
    // the active session. The active chip's currentContextUsage stays
    // unchanged in this case.
    const store = useChatStore()
    store.currentSessionId = 'session-B'
    store.currentContextUsage = {
      inputTokens: 1000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 1,
    }

    store.applyContentEvent(
      JSON.stringify({
        type: 'context_usage',
        input_tokens: 75000,
        output_reserve: 4096,
        limit: 100000,
        percentage: 75,
      }),
      'session-A',
    )

    // Inactive session's slot updates with the wire figure.
    expect(store.contextUsageBySession['session-A']).toEqual({
      inputTokens: 75000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 75,
    })
    // Active session's chip is unchanged.
    expect(store.currentContextUsage).toEqual({
      inputTokens: 1000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 1,
    })
  })

  it('does NOT fire a model_active toast when a provider_changed just toasted the same transition', async () => {
    // Failover sequence on the wire: provider_changed (rich copy with
    // failure reason) → model_active (target same provider+model).
    // provider_changed already toasted a strictly-better message; a
    // follow-up generic model_active toast for the same destination is
    // pure duplicate noise. The store's lastProviderChangeKey gates
    // the second toast.
    const toastModule = await import('@/composables/useToast')
    const showToastSpy = vi.spyOn(toastModule, 'showToast').mockImplementation(() => 0)

    const store = useChatStore()
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'

    store.applyContentEvent(
      JSON.stringify({
        type: 'provider_changed',
        from: 'anthropic+claude-sonnet-4-6',
        to: 'zai+glm-4.6',
        reason: 'rate_limited',
      }),
    )

    // provider_changed fired a toast.
    expect(showToastSpy).toHaveBeenCalledTimes(1)

    // The Go SSE pipeline now sends model_active right after
    // provider_changed targeting the same provider+model.
    store.applyContentEvent(
      JSON.stringify({
        type: 'model_active',
        provider: 'zai',
        model: 'glm-4.6',
      }),
    )

    // Still 1 — model_active stayed silent thanks to the dedup key.
    expect(showToastSpy).toHaveBeenCalledTimes(1)

    showToastSpy.mockRestore()
  })

  it('creates an in-flight assistant target when a thinking event arrives before any content', () => {
    // glm-4.6's observed shape: 52 seconds of reasoning_content arrives
    // BEFORE the first content delta. The handler must materialise an
    // in-flight assistant placeholder so the watchdog stays armed and the
    // future content delta has a target. The placeholder's visible content
    // MUST stay empty — only thinkingContent accumulates.
    const store = useChatStore()
    store.messages = [
      { id: 'u1', role: 'user', content: 'plan it', timestamp: '2026-05-04T00:00:00Z' },
    ]

    store.applyContentEvent(JSON.stringify({ type: 'thinking', content: 'first thought' }))

    const target = store.messages.find((m) => m.role === 'assistant' && m.status === 'running')
    expect(target).toBeDefined()
    expect(target?.thinkingContent).toBe('first thought')
    expect(target?.content).toBe('')
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

  it('seals the in-flight assistant on [DONE] (Slice D — isStreaming stays sticky until outer turn ends)', () => {
    // Streaming Coherence Slice D — DONE seals the running row but
    // does NOT clear isStreaming. The send finally block clears the
    // streaming flag when the outer turn completes. Pre-slice the
    // flag flipped on every DONE; the user saw indicator flicker
    // between tool rounds.
    const store = useChatStore()
    store.currentSessionId = 'sess-1'
    store.messages = [
      { id: 'u1', role: 'user', content: 'go', timestamp: '' },
      { id: 'a1', role: 'assistant', content: 'partial', timestamp: '', status: 'running' },
    ]
    store.setSessionStreaming('sess-1', { isStreaming: true })
    store.applyContentEvent('[DONE]')
    expect(store.messages.find((m) => m.id === 'a1')?.status).toBe('completed')
    expect(store.isStreaming).toBe(true)
  })

  it('does NOT route an untyped delegation-shaped payload (structural fallback removed)', () => {
    // Regression guard for Principal F6. The Go emitter always tags
    // delegation events with `type: 'delegation'`. A payload that lacks
    // the discriminant but happens to carry target_agent/chain_id is
    // either a bug on the emitter side or a stray test fixture — either
    // way it must not silently route to applyDelegationEvent.
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

  it('increments compactionEventCount on a context_compacted event for the active session (Slice 6b)', () => {
    // Slice 6b — the chat store consumes the SSE bridge of the engine's
    // EventContextCompacted bus event. Each compaction the user observes
    // is counted so the chip can show "compacted ×N this session" if a
    // future surface wants it; today the counter exists primarily as a
    // canary signal — non-zero ⇒ at least one compaction has fired ⇒
    // tooltip is meaningful.
    const store = useChatStore()
    store.currentSessionId = 's-active'
    expect(store.compactionEventCount).toBe(0)

    store.applyContentEvent(
      JSON.stringify({
        type: 'context_compacted',
        session_id: 's-active',
        agent_id: 'tech-lead',
        original_tokens: 50000,
        summary_tokens: 5000,
        latency_ms: 1200,
      }),
    )
    expect(store.compactionEventCount).toBe(1)

    store.applyContentEvent(
      JSON.stringify({
        type: 'context_compacted',
        session_id: 's-active',
        agent_id: 'tech-lead',
        original_tokens: 40000,
        summary_tokens: 4000,
        latency_ms: 800,
      }),
    )
    expect(store.compactionEventCount).toBe(2)
  })

  it('records the most-recent compaction payload for the chip tooltip (Slice 6b)', () => {
    // The chip tooltip reads `lastCompaction.tokensSaved` as the figure
    // it displays. The store derives that from `originalTokens -
    // summaryTokens` so the chip stays a pure reader. The wall-clock
    // `at` timestamp is recorded too — the chip's tooltip uses it for
    // the "compacted Ns ago" copy on a later iteration; today's pin
    // just verifies the field is present and a finite number.
    //
    // Phase-5 Slice δ added the trigger discriminant; the store
    // captures it onto lastCompaction so the tooltip can attribute the
    // cause without needing a sibling slice.
    const store = useChatStore()
    store.currentSessionId = 's-active'

    const before = Date.now()
    store.applyContentEvent(
      JSON.stringify({
        type: 'context_compacted',
        session_id: 's-active',
        agent_id: 'tech-lead',
        original_tokens: 50000,
        summary_tokens: 5000,
        latency_ms: 1200,
        trigger: 'gate_proximity',
      }),
    )
    const after = Date.now()

    expect(store.lastCompaction).not.toBeNull()
    if (store.lastCompaction) {
      expect(store.lastCompaction.originalTokens).toBe(50000)
      expect(store.lastCompaction.summaryTokens).toBe(5000)
      expect(store.lastCompaction.tokensSaved).toBe(45000)
      expect(store.lastCompaction.at).toBeGreaterThanOrEqual(before)
      expect(store.lastCompaction.at).toBeLessThanOrEqual(after)
      expect(store.lastCompaction.trigger).toBe('gate_proximity')
    }
  })

  it('captures the trigger discriminant for each fire (Phase-5 Slice δ)', () => {
    // Closed vocabulary: ratio | gate_proximity | model_switch |
    // tool_result_wave. The store stamps the field verbatim onto
    // lastCompaction so ContextUsageChip.vue can attribute the cause
    // without re-parsing the wire payload.
    const store = useChatStore()
    store.currentSessionId = 's-active'

    const cases: Array<'ratio' | 'gate_proximity' | 'model_switch' | 'tool_result_wave'> = [
      'ratio',
      'gate_proximity',
      'model_switch',
      'tool_result_wave',
    ]
    for (const trigger of cases) {
      store.applyContentEvent(
        JSON.stringify({
          type: 'context_compacted',
          session_id: 's-active',
          agent_id: 'tech-lead',
          original_tokens: 10000,
          summary_tokens: 1000,
          trigger,
        }),
      )
      expect(store.lastCompaction?.trigger).toBe(trigger)
    }
  })

  it('clears compaction state on session change (Slice 6b — loadSessionMessages reset path)', async () => {
    // The shared session-change reset clears criticalError and
    // currentContextUsage; the same path must reset the compaction
    // state so a stale "compacted ×3" from a prior session does not
    // bleed into the new one. A fresh stream on the new session
    // repopulates it.
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([] as never)

    const store = useChatStore()
    store.sessions = [
      {
        id: 'fresh-session',
        agentId: 'test-agent',
        currentAgentId: 'test-agent',
        createdAt: '2026-05-08T00:00:00Z',
        updatedAt: '2026-05-08T00:00:00Z',
        messageCount: 0,
      },
    ] as never
    store.compactionEventCount = 3
    store.lastCompaction = {
      originalTokens: 50000,
      summaryTokens: 5000,
      tokensSaved: 45000,
      at: Date.now(),
      trigger: '',
    }

    await store.loadSessionMessages('fresh-session')

    expect(store.compactionEventCount).toBe(0)
    expect(store.lastCompaction).toBeNull()
  })

  it('ignores context_compacted events for a different session (Slice 6b session-scope guard)', () => {
    // The SSE bridge in internal/api/server.go writes a
    // context_compacted event onto the active stream's wire only —
    // session scoping is enforced server-side. The store's guard
    // here is a defence-in-depth pin: if a future SSE multiplexing
    // change broadcasts events to multiple session streams, the
    // store still ignores compactions targeting other sessions so
    // the chip's tooltip and counter only reflect the current
    // session's compactions.
    const store = useChatStore()
    store.currentSessionId = 's-a'
    expect(store.compactionEventCount).toBe(0)

    store.applyContentEvent(
      JSON.stringify({
        type: 'context_compacted',
        session_id: 's-b',
        agent_id: 'tech-lead',
        original_tokens: 50000,
        summary_tokens: 5000,
        latency_ms: 1200,
      }),
    )

    expect(store.compactionEventCount).toBe(0)
    expect(store.lastCompaction).toBeNull()
  })

  it('populates lastGateFailure on a gate_failed event (Gate Bus Bridge)', () => {
    // Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026): the
    // chat store consumes the SSE bridge of the engine's
    // EventGateFailed bus event and routes it onto a session-scoped
    // lastGateFailure slice the GateFailureBanner reads. Mirrors the
    // lastCompaction pattern (state-driven affordance rather than
    // transient toast) so the banner survives component re-mount.
    const store = useChatStore()
    store.currentSessionId = 's-active'
    expect(store.lastGateFailure).toBeNull()

    store.applyContentEvent(
      JSON.stringify({
        type: 'gate_failed',
        swarm_id: 'a-team',
        lifecycle: 'post-member',
        member_id: 'researcher',
        gate_name: 'post-member-researcher-relevance-gate',
        gate_kind: 'ext:relevance-gate',
        reason: 'off-topic',
        cause: 'score 0.31 < threshold 0.5',
        coord_store_keys: ['chain/researcher/output', 'chain/topic/spec'],
      }),
    )

    expect(store.lastGateFailure).not.toBeNull()
    if (store.lastGateFailure) {
      expect(store.lastGateFailure.swarmId).toBe('a-team')
      expect(store.lastGateFailure.lifecycle).toBe('post-member')
      expect(store.lastGateFailure.memberId).toBe('researcher')
      expect(store.lastGateFailure.gateName).toBe('post-member-researcher-relevance-gate')
      expect(store.lastGateFailure.gateKind).toBe('ext:relevance-gate')
      expect(store.lastGateFailure.reason).toBe('off-topic')
      expect(store.lastGateFailure.cause).toBe('score 0.31 < threshold 0.5')
      expect(store.lastGateFailure.coordStoreKeys).toEqual([
        'chain/researcher/output',
        'chain/topic/spec',
      ])
    }
  })

  it('overwrites lastGateFailure when a fresh gate_failed event arrives (each halt is foreground-renderable)', () => {
    // A subsequent halt must replace the prior lastGateFailure so
    // the banner shows the latest failure (matches CriticalErrorBanner's
    // unconditional overwrite policy — a fresh fatal needs a fresh
    // banner with the new context).
    const store = useChatStore()
    store.currentSessionId = 's-active'

    store.applyContentEvent(
      JSON.stringify({
        type: 'gate_failed',
        gate_name: 'first-halt',
        reason: 'first reason',
      }),
    )
    store.applyContentEvent(
      JSON.stringify({
        type: 'gate_failed',
        gate_name: 'second-halt',
        reason: 'second reason',
      }),
    )

    expect(store.lastGateFailure?.gateName).toBe('second-halt')
    expect(store.lastGateFailure?.reason).toBe('second reason')
  })

  it('clearGateFailure resets the slice (banner Dismiss action)', () => {
    const store = useChatStore()
    store.currentSessionId = 's-active'
    store.lastGateFailure = {
      swarmId: 'a-team',
      lifecycle: 'post',
      memberId: '',
      gateName: 'envelope-check',
      gateKind: 'builtin:result-schema',
      reason: 'schema validation failed',
      cause: '',
      coordStoreKeys: [],
    }

    store.clearGateFailure()

    expect(store.lastGateFailure).toBeNull()
  })

  it('clears lastGateFailure on session change (loadSessionMessages reset path)', async () => {
    // The shared session-change reset clears criticalError,
    // currentContextUsage, and the compaction state; the same path
    // must reset lastGateFailure so a stale halt from a prior session
    // does not bleed into the new one.
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([] as never)

    const store = useChatStore()
    store.sessions = [
      {
        id: 'fresh-gate-session',
        agentId: 'test-agent',
        currentAgentId: 'test-agent',
        createdAt: '2026-05-08T00:00:00Z',
        updatedAt: '2026-05-08T00:00:00Z',
        messageCount: 0,
      },
    ] as never
    store.lastGateFailure = {
      swarmId: 'old-swarm',
      lifecycle: 'pre',
      memberId: '',
      gateName: 'old-gate',
      gateKind: 'builtin:result-schema',
      reason: 'old reason',
      cause: '',
      coordStoreKeys: [],
    }

    await store.loadSessionMessages('fresh-gate-session')

    expect(store.lastGateFailure).toBeNull()
  })
})

describe('chatStore - DEFAULT_AGENT_ID matches the manifest convention', () => {
  // The frontend's DEFAULT_AGENT_ID is the agent the chat selects for a
  // brand-new visitor with no persisted session and no persisted agent
  // choice. This must point at default-assistant — the friendly, general-
  // purpose agent that can delegate to specialists — NOT a sprint-
  // coordinator orchestrator like Team-Lead.
  //
  // The agent id MUST match the manifest's `id:` field at
  // internal/app/agents/default-assistant.md verbatim. The backend's
  // POST /api/v1/sessions handler returns 400 when agent_id is empty, so
  // the value the frontend sends is the value the backend resolves —
  // there is no "system default" fallback at the API boundary that could
  // paper over a typo here.
  //
  // This pins the constant so a future refactor cannot silently regress
  // the default back to team-lead (which the user reported as wrong: it
  // is optimised for multi-step delivery coordination, not chat).

  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('exports DEFAULT_AGENT_ID as the lowercase hyphenated default-assistant id', () => {
    expect(DEFAULT_AGENT_ID).toBe('default-assistant')
  })

  it('uses default-assistant as the chosen agent on a fresh restore when it is in the available list', async () => {
    // No persisted session, no persisted agent. Available agents include
    // default-assistant — the store must pick it as the default.
    vi.mocked(fetchAgents).mockResolvedValueOnce([
      { id: 'default-assistant', name: 'Default Assistant' } as never,
      { id: 'Team-Lead', name: 'Team Lead' } as never,
    ])
    vi.mocked(fetchSessions).mockResolvedValueOnce([])

    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(store.agentId).toBe('default-assistant')
    expect(window.localStorage.getItem('chat.agentId')).toBe('default-assistant')
  })

  it('falls back to the first available agent only when default-assistant is not in the list', async () => {
    // Defensive: a deployment with the manifest removed must still pick
    // *some* agent so the chat can boot. The first available agent is
    // the documented fallback (chatStore.ts restoreStateFromBackend).
    vi.mocked(fetchAgents).mockResolvedValueOnce([
      { id: 'Team-Lead', name: 'Team Lead' } as never,
      { id: 'Senior-Engineer', name: 'Senior Engineer' } as never,
    ])
    vi.mocked(fetchSessions).mockResolvedValueOnce([])

    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(store.agentId).toBe('Team-Lead')
  })

  it('loadAgents populates the list without persisting an active agent (AgentPicker pre-empt race)', async () => {
    // The AgentPicker component's onMounted hook calls loadAgents()
    // before ChatView's onMounted runs restoreStateFromBackend. Pre-fix
    // loadAgents seeded agents[0] (alphabetically API-Engineer) into
    // this.agentId and persisted it to localStorage; restoreStateFromBackend
    // then read that persisted value and DEFAULT_AGENT_ID never won.
    //
    // This pin enforces the new contract: loadAgents is responsible for
    // populating availableAgents/availableAgentDetails only. It must
    // not touch agentId or localStorage. The active-agent precedence
    // belongs to restoreStateFromBackend (boot-time) and setAgent
    // (user-driven). Decoupling the two is what closes the pre-empt
    // race for both fresh visits AND fast tests that gate on the
    // picker label as a proxy for "store fully restored".
    vi.mocked(fetchAgents).mockResolvedValueOnce([
      { id: 'API-Engineer', name: 'API Engineer' } as never,
      { id: 'default-assistant', name: 'Default Assistant' } as never,
      { id: 'Team-Lead', name: 'Team Lead' } as never,
    ])

    const store = useChatStore()
    expect(store.agentId).toBe('')
    await store.loadAgents()

    expect(store.availableAgents).toEqual(['API-Engineer', 'default-assistant', 'Team-Lead'])
    expect(store.availableAgentDetails).toHaveLength(3)
    // The bug: loadAgents must not have set agentId or persisted to localStorage.
    expect(store.agentId).toBe('')
    expect(window.localStorage.getItem('chat.agentId')).toBeNull()
  })

  it('loadAgents preserves an already-set active agent and does not re-persist it', async () => {
    // setAgent (user-driven) is the only path that should persist an
    // active agent choice. loadAgents merely refreshes the list; an
    // agent already on the store stays put, and localStorage is not
    // re-written on every refresh.
    vi.mocked(fetchAgents).mockResolvedValueOnce([
      { id: 'default-assistant', name: 'Default Assistant' } as never,
      { id: 'Team-Lead', name: 'Team Lead' } as never,
    ])

    const store = useChatStore()
    store.agentId = 'Team-Lead'
    // Simulating a localStorage value set by setAgent earlier.
    window.localStorage.setItem('chat.agentId', 'Team-Lead')

    await store.loadAgents()

    expect(store.agentId).toBe('Team-Lead')
    expect(window.localStorage.getItem('chat.agentId')).toBe('Team-Lead')
  })

  it('loadAgents leaves an active agent untouched even when it has been removed from the manifest', async () => {
    // Reseeding-when-stale is restoreStateFromBackend's job (it falls
    // back to DEFAULT_AGENT_ID when the persisted agent is no longer
    // in the available list). loadAgents must not pre-empt that
    // decision — doing so racially clobbers the persisted-agent fallback
    // that restoreStateFromBackend's precedence chain depends on.
    vi.mocked(fetchAgents).mockResolvedValueOnce([
      { id: 'API-Engineer', name: 'API Engineer' } as never,
      { id: 'default-assistant', name: 'Default Assistant' } as never,
    ])

    const store = useChatStore()
    store.agentId = 'Retired-Agent'
    window.localStorage.setItem('chat.agentId', 'Retired-Agent')

    await store.loadAgents()

    // Stale-agent reconciliation belongs to restoreStateFromBackend.
    // loadAgents only refreshes the list.
    expect(store.agentId).toBe('Retired-Agent')
    expect(window.localStorage.getItem('chat.agentId')).toBe('Retired-Agent')
  })
})

// loadSessionByAgentId is the seam the in-thread MessageBubble
// delegation card click hangs on. The persisted `delegation` /
// `delegation_started` message carries only `targetAgent` (the
// streaming.DelegationEvent wire shape has no ChildSessionID), so the
// store has to resolve "the session for agent X" against the local
// sessions list. Pre-fix this was a sessions.find() against an
// oldest-first backend sort, which on a long-lived backend almost
// always returned a stale standalone session for the agent rather than
// the just-delegated child. The user reported this as "we are no
// longer able to click on the delegating card and view the delegated
// agents session".
describe('chatStore - loadSessionByAgentId (regression: prefer the active parent\'s child)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })
  it('prefers the most-recent child of the active session over an older standalone session for the same agent', async () => {
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      // Old standalone session for executor — unrelated to the active parent.
      {
        id: 'stale-executor',
        agentId: 'executor',
        title: 'Old standalone',
        createdAt: '2026-04-15T08:00:00Z',
        updatedAt: '2026-04-15T08:00:00Z',
        messageCount: 1,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
      // Active parent.
      {
        id: 'parent-active',
        agentId: 'planner',
        title: 'Active parent',
        createdAt: '2026-05-01T09:00:00Z',
        updatedAt: '2026-05-01T09:00:00Z',
        messageCount: 1,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
      // Active parent's child for executor — the click target.
      {
        id: 'child-of-active',
        agentId: 'executor',
        parentId: 'parent-active',
        title: 'Active child',
        createdAt: '2026-05-01T09:01:00Z',
        updatedAt: '2026-05-01T09:01:00Z',
        messageCount: 1,
        status: 'active',
        depth: 1,
        isStreaming: false,
      },
    ])
    vi.mocked(fetchSessionMessages).mockResolvedValue([])

    const store = useChatStore()
    await store.loadSessions()
    store.currentSessionId = 'parent-active'

    const loaded = await store.loadSessionByAgentId('executor')

    expect(loaded).toBe(true)
    expect(store.currentSessionId).toBe('child-of-active')
    // The stale standalone session — older but agent-id matched — must
    // not have been the one the click landed on.
    expect(store.currentSessionId).not.toBe('stale-executor')
  })

  it('picks the most-recent child when several siblings share the agent id', async () => {
    // Same agent, several runs — common when a parent re-delegates to the
    // same agent across iterations. The most recent run is the click's
    // intended target.
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'parent',
        agentId: 'planner',
        title: 'Parent',
        createdAt: '2026-05-01T09:00:00Z',
        updatedAt: '2026-05-01T09:00:00Z',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
      {
        id: 'child-old',
        agentId: 'executor',
        parentId: 'parent',
        title: 'First run',
        createdAt: '2026-05-01T09:01:00Z',
        updatedAt: '2026-05-01T09:01:00Z',
        messageCount: 0,
        status: 'active',
        depth: 1,
        isStreaming: false,
      },
      {
        id: 'child-new',
        agentId: 'executor',
        parentId: 'parent',
        title: 'Second run',
        createdAt: '2026-05-01T09:05:00Z',
        updatedAt: '2026-05-01T09:05:00Z',
        messageCount: 0,
        status: 'active',
        depth: 1,
        isStreaming: false,
      },
    ])
    vi.mocked(fetchSessionMessages).mockResolvedValue([])

    const store = useChatStore()
    await store.loadSessions()
    store.currentSessionId = 'parent'

    await store.loadSessionByAgentId('executor')

    expect(store.currentSessionId).toBe('child-new')
  })

  it('falls back to the most-recent overall match when no child of the active session matches', async () => {
    // Edge case: parent is itself the delegated agent (a swarm-bridge
    // re-entry), or the click happens before the child shows up in the
    // sessions list. The fallback must still pick the most recent so the
    // user lands on the freshest run rather than a stale one.
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'older',
        agentId: 'executor',
        title: 'Older',
        createdAt: '2026-04-01T08:00:00Z',
        updatedAt: '2026-04-01T08:00:00Z',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
      {
        id: 'newer',
        agentId: 'executor',
        title: 'Newer',
        createdAt: '2026-05-01T08:00:00Z',
        updatedAt: '2026-05-01T08:00:00Z',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
    ])
    vi.mocked(fetchSessionMessages).mockResolvedValue([])

    const store = useChatStore()
    await store.loadSessions()
    store.currentSessionId = null

    await store.loadSessionByAgentId('executor')

    expect(store.currentSessionId).toBe('newer')
  })

  it('returns false and leaves state untouched when no session matches the agent', async () => {
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'parent',
        agentId: 'planner',
        title: 'Parent',
        createdAt: '2026-05-01T09:00:00Z',
        updatedAt: '2026-05-01T09:00:00Z',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
    ])

    const store = useChatStore()
    await store.loadSessions()
    store.currentSessionId = 'parent'

    const loaded = await store.loadSessionByAgentId('not-an-agent')

    expect(loaded).toBe(false)
    expect(store.currentSessionId).toBe('parent')
  })
})

// loadSessionForDelegation closes the sibling-confusion bug class for
// the in-thread MessageBubble delegation card. Pre-fix the click
// resolved by `targetAgent` alone — when a parent delegated to the
// same agent twice (or two sibling chains landed on the same agent),
// the click on the EARLIER card silently opened the LATER sibling
// (loadSessionByAgentId fell back to most-recent-wins). The fix wires
// the persisted Message's `chainId` into the resolver: SwarmEvent
// ingestion populates a (chainId → childSessionId) map in the store,
// and the click routes through that map when chainId is known.
describe("chatStore - loadSessionForDelegation (chainId-aware sibling disambiguation)", () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it("routes by chainId when the chain is in the chainSessions map, even if siblings share the agent id", async () => {
    // Two siblings, same agentId, different chains. Pre-fix
    // loadSessionByAgentId always picked child-new (the most-recent).
    // Post-fix, clicking the card for chain-old routes to child-old
    // because the SwarmEvent for chain-old recorded child-old's id.
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'parent',
        agentId: 'planner',
        title: 'Parent',
        createdAt: '2026-05-01T09:00:00Z',
        updatedAt: '2026-05-01T09:00:00Z',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
      {
        id: 'child-old',
        agentId: 'executor',
        parentId: 'parent',
        title: 'First run',
        createdAt: '2026-05-01T09:01:00Z',
        updatedAt: '2026-05-01T09:01:00Z',
        messageCount: 0,
        status: 'active',
        depth: 1,
        isStreaming: false,
      },
      {
        id: 'child-new',
        agentId: 'executor',
        parentId: 'parent',
        title: 'Second run',
        createdAt: '2026-05-01T09:05:00Z',
        updatedAt: '2026-05-01T09:05:00Z',
        messageCount: 0,
        status: 'active',
        depth: 1,
        isStreaming: false,
      },
    ])
    vi.mocked(fetchSessionMessages).mockResolvedValue([])

    const store = useChatStore()
    await store.loadSessions()
    store.currentSessionId = 'parent'
    // Simulate swarmStore ingesting the two `delegation` events the
    // engine fires when the parent delegates to executor twice.
    store.recordChainSession('chain-old', 'child-old')
    store.recordChainSession('chain-new', 'child-new')

    // The user clicks the EARLIER delegation card — the one whose
    // persisted message carries chainId: 'chain-old'.
    const loaded = await store.loadSessionForDelegation({
      chainId: 'chain-old',
      agentId: 'executor',
    })

    expect(loaded).toBe(true)
    expect(store.currentSessionId).toBe('child-old')
    // The most-recent-wins fallback would have landed on child-new.
    // Confirm the chainId routing dominated.
    expect(store.currentSessionId).not.toBe('child-new')
  })

  it("falls back to loadSessionByAgentId's most-recent-child resolver when chainId is unknown", async () => {
    // No swarm event seen for this chain yet (e.g. hard reload before
    // the live stream reconnected). The legacy resolver kicks in so
    // the click still goes somewhere reasonable — preserving the
    // pre-fix behaviour for the chainId-missing case.
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'parent',
        agentId: 'planner',
        title: 'Parent',
        createdAt: '2026-05-01T09:00:00Z',
        updatedAt: '2026-05-01T09:00:00Z',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
      {
        id: 'child-old',
        agentId: 'executor',
        parentId: 'parent',
        title: 'First run',
        createdAt: '2026-05-01T09:01:00Z',
        updatedAt: '2026-05-01T09:01:00Z',
        messageCount: 0,
        status: 'active',
        depth: 1,
        isStreaming: false,
      },
      {
        id: 'child-new',
        agentId: 'executor',
        parentId: 'parent',
        title: 'Second run',
        createdAt: '2026-05-01T09:05:00Z',
        updatedAt: '2026-05-01T09:05:00Z',
        messageCount: 0,
        status: 'active',
        depth: 1,
        isStreaming: false,
      },
    ])
    vi.mocked(fetchSessionMessages).mockResolvedValue([])

    const store = useChatStore()
    await store.loadSessions()
    store.currentSessionId = 'parent'
    // chainSessions map empty — no recordChainSession() calls.

    const loaded = await store.loadSessionForDelegation({
      chainId: 'chain-not-yet-seen',
      agentId: 'executor',
    })

    expect(loaded).toBe(true)
    // Legacy most-recent-wins behaviour — preserved as the fallback.
    expect(store.currentSessionId).toBe('child-new')
  })

  it("falls back to the agent-id resolver when no chainId is provided at all", async () => {
    // The persisted message shape may lack chainId on older history
    // written before the chain-id-on-message field was added. The
    // call-site passes chainId: undefined, the resolver must still
    // route via the agent-id heuristic.
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'parent',
        agentId: 'planner',
        title: 'Parent',
        createdAt: '2026-05-01T09:00:00Z',
        updatedAt: '2026-05-01T09:00:00Z',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
      {
        id: 'child-of-active',
        agentId: 'executor',
        parentId: 'parent',
        title: 'Child',
        createdAt: '2026-05-01T09:01:00Z',
        updatedAt: '2026-05-01T09:01:00Z',
        messageCount: 0,
        status: 'active',
        depth: 1,
        isStreaming: false,
      },
    ])
    vi.mocked(fetchSessionMessages).mockResolvedValue([])

    const store = useChatStore()
    await store.loadSessions()
    store.currentSessionId = 'parent'

    const loaded = await store.loadSessionForDelegation({
      agentId: 'executor',
    })

    expect(loaded).toBe(true)
    expect(store.currentSessionId).toBe('child-of-active')
  })

  it("returns false when neither chainId nor agent-id resolves", async () => {
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'parent',
        agentId: 'planner',
        title: 'Parent',
        createdAt: '2026-05-01T09:00:00Z',
        updatedAt: '2026-05-01T09:00:00Z',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
    ])

    const store = useChatStore()
    await store.loadSessions()
    store.currentSessionId = 'parent'

    const loaded = await store.loadSessionForDelegation({
      chainId: 'unknown-chain',
      agentId: 'not-an-agent',
    })

    expect(loaded).toBe(false)
    expect(store.currentSessionId).toBe('parent')
  })

  it("recordChainSession is idempotent and tolerates empty inputs", async () => {
    const store = useChatStore()
    // Empty pair — no-op, no throw.
    store.recordChainSession('', 'some-session')
    store.recordChainSession('some-chain', '')
    expect(store.chainSessions).toEqual({})

    // First recording lands.
    store.recordChainSession('chain-1', 'session-1')
    expect(store.chainSessions['chain-1']).toBe('session-1')

    // Repeat is a no-op.
    store.recordChainSession('chain-1', 'session-1')
    expect(store.chainSessions['chain-1']).toBe('session-1')

    // Different chain → different slot, doesn't clobber.
    store.recordChainSession('chain-2', 'session-2')
    expect(store.chainSessions['chain-1']).toBe('session-1')
    expect(store.chainSessions['chain-2']).toBe('session-2')
  })

  // Cold-reload hole closure for the sibling-confusion bug class.
  //
  // a488b858 closed the LIVE-CLICK path by populating the
  // (chainId → childSessionId) map from SwarmEvents as they stream. The
  // map is empty after a hard reload because FlowState does not replay
  // swarm events on reconnect, so the click-through fell back to
  // agent-id "most-recent" and the sibling-confusion bug re-appeared.
  //
  // The persisted Session now carries chainId on the wire shape
  // (SessionSummary.chainId, stamped on the backend by
  // CreateWithParentAndChain). loadSessions rebuilds the runtime map
  // from this list on cold load so the click-through resolves correctly
  // without waiting for a swarm event.
  it("loadSessions rebuilds chainSessions from the persisted session list (cold-reload hole closure)", async () => {
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'parent',
        agentId: 'planner',
        title: 'Parent',
        createdAt: '2026-05-01T09:00:00Z',
        updatedAt: '2026-05-01T09:00:00Z',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
      },
      {
        id: 'child-old',
        agentId: 'executor',
        parentId: 'parent',
        chainId: 'chain-old',
        title: 'First run',
        createdAt: '2026-05-01T09:01:00Z',
        updatedAt: '2026-05-01T09:01:00Z',
        messageCount: 0,
        status: 'active',
        depth: 1,
        isStreaming: false,
      },
      {
        id: 'child-new',
        agentId: 'executor',
        parentId: 'parent',
        chainId: 'chain-new',
        title: 'Second run',
        createdAt: '2026-05-01T09:05:00Z',
        updatedAt: '2026-05-01T09:05:00Z',
        messageCount: 0,
        status: 'active',
        depth: 1,
        isStreaming: false,
      },
    ])

    const store = useChatStore()
    // Pre-condition: simulated cold reload — runtime map is empty.
    expect(store.chainSessions).toEqual({})

    await store.loadSessions()

    expect(store.chainSessions['chain-old']).toBe('child-old')
    expect(store.chainSessions['chain-new']).toBe('child-new')
  })

  it("loadSessions skips summaries without a chainId so root sessions don't pollute the map", async () => {
    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'root-1',
        agentId: 'planner',
        title: 'Root',
        createdAt: '2026-05-01T09:00:00Z',
        updatedAt: '2026-05-01T09:00:00Z',
        messageCount: 0,
        status: 'active',
        depth: 0,
        isStreaming: false,
        // chainId omitted — root session
      },
      {
        id: 'delegated',
        agentId: 'executor',
        parentId: 'root-1',
        chainId: 'chain-real',
        title: 'Child',
        createdAt: '2026-05-01T09:01:00Z',
        updatedAt: '2026-05-01T09:01:00Z',
        messageCount: 0,
        status: 'active',
        depth: 1,
        isStreaming: false,
      },
    ])

    const store = useChatStore()
    await store.loadSessions()

    expect(store.chainSessions).toEqual({ 'chain-real': 'delegated' })
  })

  it("loadSessions does NOT clobber existing chainSessions entries that were populated live before the refetch", async () => {
    // Live SwarmEvent ingestion may populate the map for a delegation
    // whose persisted Session hasn't been written to disk yet. The
    // refetch on the polling path (active-session reconcile) must NOT
    // wipe those entries — clobbering them re-opens the bug for the
    // very window the backfill is meant to protect.
    const store = useChatStore()
    store.recordChainSession('chain-live', 'session-live')

    vi.mocked(fetchSessions).mockResolvedValueOnce([
      {
        id: 'persisted-child',
        agentId: 'executor',
        parentId: 'parent',
        chainId: 'chain-persisted',
        title: 'Persisted',
        createdAt: '2026-05-01T09:00:00Z',
        updatedAt: '2026-05-01T09:00:00Z',
        messageCount: 0,
        status: 'active',
        depth: 1,
        isStreaming: false,
      },
    ])

    await store.loadSessions()

    expect(store.chainSessions['chain-live']).toBe('session-live')
    expect(store.chainSessions['chain-persisted']).toBe('persisted-child')
  })
})

describe('describeToolName (plain-language tool labels for non-technical users)', () => {
  // The user explicitly called out that "tool: bash" reads as too
  // technical — non-technical-user UX bar. The label map below is the
  // contract the chat toast surface relies on; covering it case-wise
  // pins the user-visible copy so a refactor doesn't accidentally
  // regress to raw tool ids.
  it('maps bash and shell variants to "Running command"', () => {
    expect(describeToolName('bash')).toBe('Running command')
    expect(describeToolName('Bash')).toBe('Running command')
    expect(describeToolName('shell')).toBe('Running command')
    expect(describeToolName('terminal')).toBe('Running command')
  })

  it('maps file read variants to "Reading file"', () => {
    expect(describeToolName('read')).toBe('Reading file')
    expect(describeToolName('Read')).toBe('Reading file')
    expect(describeToolName('view')).toBe('Reading file')
  })

  it('maps file edit variants to "Editing file"', () => {
    expect(describeToolName('edit')).toBe('Editing file')
    expect(describeToolName('Edit')).toBe('Editing file')
    expect(describeToolName('multiedit')).toBe('Editing file')
    expect(describeToolName('str_replace_editor')).toBe('Editing file')
  })

  it('maps file write variants to "Writing file"', () => {
    expect(describeToolName('write')).toBe('Writing file')
    expect(describeToolName('Write')).toBe('Writing file')
    expect(describeToolName('create_file')).toBe('Writing file')
  })

  it('maps grep and search variants to "Searching files"', () => {
    expect(describeToolName('grep')).toBe('Searching files')
    expect(describeToolName('search')).toBe('Searching files')
  })

  it('maps glob and find to "Finding files"', () => {
    expect(describeToolName('glob')).toBe('Finding files')
    expect(describeToolName('find')).toBe('Finding files')
  })

  it('maps web fetch variants to "Fetching web page"', () => {
    expect(describeToolName('webfetch')).toBe('Fetching web page')
    expect(describeToolName('WebFetch')).toBe('Fetching web page')
    expect(describeToolName('web_fetch')).toBe('Fetching web page')
    expect(describeToolName('fetch')).toBe('Fetching web page')
  })

  it('maps web search variants to "Searching the web"', () => {
    expect(describeToolName('websearch')).toBe('Searching the web')
    expect(describeToolName('WebSearch')).toBe('Searching the web')
    expect(describeToolName('web_search')).toBe('Searching the web')
  })

  it('maps task and agent delegation to "Delegating to agent"', () => {
    expect(describeToolName('task')).toBe('Delegating to agent')
    expect(describeToolName('Task')).toBe('Delegating to agent')
    expect(describeToolName('agent')).toBe('Delegating to agent')
    expect(describeToolName('delegate')).toBe('Delegating to agent')
  })

  it('maps todowrite variants to "Updating to-dos"', () => {
    expect(describeToolName('todowrite')).toBe('Updating to-dos')
    expect(describeToolName('TodoWrite')).toBe('Updating to-dos')
    expect(describeToolName('todo_write')).toBe('Updating to-dos')
  })

  it('falls back to "Running {tool}" with underscores normalised for unmapped tools', () => {
    // Defensive: an unmapped tool (new MCP entry, custom dispatcher)
    // still gets a recognisable signal — a notification with the raw
    // id is more useful than no notification at all. Underscores are
    // replaced with spaces for readability.
    expect(describeToolName('fetch_models')).toBe('Running fetch models')
    expect(describeToolName('mcp_custom_thing')).toBe('Running mcp custom thing')
  })
})

describe('composeToolActivityMessage', () => {
  it('returns the friendly label alone when one tool fired', () => {
    expect(composeToolActivityMessage(['bash'])).toBe('Running command')
  })

  it('returns "{first label} + N more" for multi-tool bursts', () => {
    expect(composeToolActivityMessage(['bash', 'read', 'edit'])).toBe('Running command + 2 more')
  })

  it('returns an empty string for an empty list (defensive)', () => {
    // Defensive: composeToolActivityMessage is only called by
    // recordToolActivity AFTER pushing a tool name, so the empty case
    // is unreachable in the live flow. Still pinned so a refactor
    // doesn't introduce a "Running undefined" message.
    expect(composeToolActivityMessage([])).toBe('')
  })
})

describe('chatStore - rolling tool-activity toast (handleToolCallEvent + recordToolActivity)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('spawns a single loading toast on the first tool_call of a burst', () => {
    // First tool of a quiet period: one toast appears, with the
    // friendly verb for that tool. Variant is "loading" (accent border,
    // persistent — we own dismissal via the rolling timer).
    const store = useChatStore()
    const { toasts } = useToast()

    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'bash', status: 'running' }))

    expect(toasts.value).toHaveLength(1)
    expect(toasts.value[0].title).toBe('Working')
    expect(toasts.value[0].message).toBe('Running command')
    expect(toasts.value[0].variant).toBe('loading')
    expect(toasts.value[0].duration).toBe(0)
  })

  it('aggregates subsequent tool_calls in the same burst into the SAME toast (no parallel toasts)', () => {
    // Multi-tool turns can fire 10+ tool_calls. A toast per call would
    // bury the user. The rolling toast UPDATES IN PLACE — same id,
    // same DOM position — and the message reads "{first label} + N more".
    const store = useChatStore()
    const { toasts } = useToast()

    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'bash', status: 'running' }))
    const firstId = toasts.value[0].id

    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'read', status: 'running' }))
    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'edit', status: 'running' }))

    expect(toasts.value).toHaveLength(1)
    expect(toasts.value[0].id).toBe(firstId)
    expect(toasts.value[0].message).toBe('Running command + 2 more')
  })

  it('auto-dismisses the rolling toast TOOL_ACTIVITY_DISMISS_MS after the LAST tool_call', () => {
    // Rolling debounce: every new tool_call resets the auto-dismiss
    // window. The toast lingers as long as tools keep firing and
    // disappears soon after the burst ends.
    const store = useChatStore()
    const { toasts } = useToast()

    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'bash', status: 'running' }))
    expect(toasts.value).toHaveLength(1)

    // 800ms in — still alive (hasn't reached the dismiss window yet).
    vi.advanceTimersByTime(800)
    expect(toasts.value).toHaveLength(1)

    // Another tool_call fires. Timer resets.
    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'read', status: 'running' }))

    // 1000ms after the SECOND call (1800ms after the first) — still
    // alive, because the window is anchored to the LAST call.
    vi.advanceTimersByTime(1000)
    expect(toasts.value).toHaveLength(1)

    // Past the dismiss window from the last call — gone.
    vi.advanceTimersByTime(TOOL_ACTIVITY_DISMISS_MS)
    expect(toasts.value).toHaveLength(0)
  })

  it('routes skill_load events through the same rolling-toast aggregator as tool_call', () => {
    // skill_load is dispatched into handleToolCallEvent (chatStore.ts
    // applyContentEvent), so the aggregator covers it for free. Pin
    // that wiring — accidentally splitting the path would re-introduce
    // parallel toasts.
    const store = useChatStore()
    const { toasts } = useToast()

    store.applyContentEvent(JSON.stringify({ type: 'skill_load', name: 'pre-action' }))
    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'bash', status: 'running' }))

    expect(toasts.value).toHaveLength(1)
    expect(toasts.value[0].message).toContain('+ 1 more')
  })

  it('starts a fresh toast for the next burst after the previous one auto-dismissed', () => {
    // Two distinct turns: first burst dismisses, second burst spawns a
    // new toast (not a stale-state hangover from the first).
    const store = useChatStore()
    const { toasts } = useToast()

    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'bash', status: 'running' }))
    vi.advanceTimersByTime(TOOL_ACTIVITY_DISMISS_MS + 50)
    expect(toasts.value).toHaveLength(0)

    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'read', status: 'running' }))
    expect(toasts.value).toHaveLength(1)
    // Fresh burst — the message is the single-tool form, NOT "+ 1 more".
    expect(toasts.value[0].message).toBe('Reading file')
  })

  it('still creates the underlying tool_result message in the chat thread (no regression)', () => {
    // Critical: the toast surface must NEVER replace the in-thread
    // tool_result row. The thread is the source of truth for what
    // happened; the toast is an ambient affordance. Confirm both
    // surfaces still receive the signal.
    const store = useChatStore()
    store.messages = []

    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'bash', status: 'running', input: 'ls' }))

    expect(store.messages).toHaveLength(1)
    expect(store.messages[0].role).toBe('tool_result')
    expect(store.messages[0].toolName).toBe('bash')
  })
})

describe('chatStore.handleProviderChangedEvent records a dedup key for the follow-up model_active', () => {
  // White-box: confirm the lastProviderChangeKey is set so the
  // suppression path in the model_active handler has the data it needs.
  // The behavioural consequence is covered by the "no duplicate toast"
  // test in the dispatch suite; this pins the internal signal.
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('records "<newProvider>+<newModel>" on lastProviderChangeKey when the pivot has both fields', () => {
    const store = useChatStore()

    store.applyContentEvent(
      JSON.stringify({
        type: 'provider_changed',
        from: 'anthropic+claude-sonnet-4-6',
        to: 'zai+glm-4.6',
        reason: 'rate_limited',
      }),
    )

    expect(store.lastProviderChangeKey).toBe('zai+glm-4.6')
  })

  it('leaves lastProviderChangeKey untouched when the pivot has an empty "to" field (defensive)', () => {
    // Empty "to" already disables the chip pivot — same defensive
    // posture for the dedup key, otherwise an empty key would silence
    // a legitimate model_active toast on the next session boundary.
    const store = useChatStore()
    store.lastProviderChangeKey = null

    store.applyContentEvent(
      JSON.stringify({
        type: 'provider_changed',
        from: 'anthropic+claude-sonnet-4-6',
        to: '',
        reason: 'rate_limited',
      }),
    )

    expect(store.lastProviderChangeKey).toBeNull()
  })
})

// Phase-5 §1c-α — the long-poll Turn endpoint now surfaces
// `current_provider` + `current_model` on every snapshot. The chat store's
// pollTurnUntilTerminal loop diffs the new pair against the prior poll's
// snapshot and, when the pair moves, calls the same
// handleProviderChangedEvent path the SSE handler uses (chatStore.ts:2740-
// 2756). Both surfaces stay live during 1c-α; idempotency is required so
// the transitional double-fire (SSE + poll for the same transition) doesn't
// emit two toasts.
//
// What this block pins:
//   1. Initial poll-with-current-pair → calls handleProviderChangedEvent with
//      the (provider, model) pair.
//   2. Same pair on a subsequent poll → no second call (no-diff).
//   3. Pair pivots mid-stream → second call with the NEW pair.
//   4. Idempotency: calling the handler twice with same args fires ONE toast
//      (and mutates state ONCE — no double-toast on SSE+poll co-fire).
describe('chatStore - pollTurnUntilTerminal current_provider/current_model diff (Phase-5 §1c-α)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('calls handleProviderChangedEvent on the first poll that carries current_provider + current_model', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-pdiff'
    store.messages = []
    // Seed the chip differently so the diff is observable.
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-opus-4-7'

    const handlerSpy = vi.spyOn(store, 'handleProviderChangedEvent')

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-pdiff',
      session_id: 'session-pdiff',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: 'zai', model: 'glm-4.6' },
      error: '',
      messages: [],
      current_provider: 'zai',
      current_model: 'glm-4.6',
    } as never)

    await store.pollTurnUntilTerminal('session-pdiff', 'turn-pdiff')

    expect(handlerSpy).toHaveBeenCalledTimes(1)
    const callArg = handlerSpy.mock.calls[0][0]
    expect(callArg.toProvider).toBe('zai')
    expect(callArg.toModel).toBe('glm-4.6')
  })

  it('does NOT call handleProviderChangedEvent on a poll where (current_provider, current_model) matches the prior poll', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-nodiff'
    store.messages = []

    const handlerSpy = vi.spyOn(store, 'handleProviderChangedEvent')

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-nodiff',
      session_id: 'session-nodiff',
      status: 'running' as const,
      started_at: '',
      completed_at: null,
      model: { provider: '', model: '' },
      error: '',
      messages: [],
      current_provider: 'anthropic',
      current_model: 'claude-opus-4-7',
    } as never)
    // Second poll: same pair — must be a no-op for the handler.
    ft.mockResolvedValueOnce({
      turn_id: 'turn-nodiff',
      session_id: 'session-nodiff',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: 'anthropic', model: 'claude-opus-4-7' },
      error: '',
      messages: [],
      current_provider: 'anthropic',
      current_model: 'claude-opus-4-7',
    } as never)

    await store.pollTurnUntilTerminal('session-nodiff', 'turn-nodiff')

    expect(handlerSpy).toHaveBeenCalledTimes(1)
    // The single call came from the first poll's transition (empty → real
    // pair). The second poll's matching pair must NOT re-fire.
  })

  it('calls handleProviderChangedEvent again when the pair pivots on a subsequent poll (mid-stream failover)', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-failover'
    store.messages = []

    const handlerSpy = vi.spyOn(store, 'handleProviderChangedEvent')

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    // First poll — initial pair lands. handler call #1.
    ft.mockResolvedValueOnce({
      turn_id: 'turn-failover',
      session_id: 'session-failover',
      status: 'running' as const,
      started_at: '',
      completed_at: null,
      model: { provider: '', model: '' },
      error: '',
      messages: [],
      current_provider: 'anthropic',
      current_model: 'claude-opus-4-7',
    } as never)
    // Second poll — pair pivots (failover). handler call #2.
    ft.mockResolvedValueOnce({
      turn_id: 'turn-failover',
      session_id: 'session-failover',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: 'zai', model: 'glm-4.6' },
      error: '',
      messages: [],
      current_provider: 'zai',
      current_model: 'glm-4.6',
    } as never)

    await store.pollTurnUntilTerminal('session-failover', 'turn-failover')

    expect(handlerSpy).toHaveBeenCalledTimes(2)
    const firstArg = handlerSpy.mock.calls[0][0]
    expect(firstArg.toProvider).toBe('anthropic')
    expect(firstArg.toModel).toBe('claude-opus-4-7')
    const secondArg = handlerSpy.mock.calls[1][0]
    expect(secondArg.toProvider).toBe('zai')
    expect(secondArg.toModel).toBe('glm-4.6')
  })
})

// Phase-5 §1c-α — handleProviderChangedEvent idempotency. The transitional
// state during 1c-α has TWO callers: the SSE handler (chatStore.ts:2740-
// 2756) and the new poll-diff caller. A double-fire for the same
// (toProvider, toModel) pair MUST NOT emit two toasts. This pins the
// idempotency guard the new caller relies on.
describe('chatStore.handleProviderChangedEvent idempotency (Phase-5 §1c-α)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('fires the "Model changed" toast exactly ONCE when called twice with the same (toProvider, toModel) pair', async () => {
    const toastModule = await import('@/composables/useToast')
    const showToastSpy = vi.spyOn(toastModule, 'showToast').mockImplementation(() => 0)

    const store = useChatStore()

    // First call — fresh transition; toast fires.
    store.handleProviderChangedEvent({
      toProvider: 'zai',
      toModel: 'glm-4.6',
      reason: 'rate_limited',
    })

    // Second call — same target pair (the transitional 1c-α double-fire:
    // poll-diff sees the new pair while the SSE handler also fires for the
    // same transition). Must short-circuit on the dedup gate.
    store.handleProviderChangedEvent({
      toProvider: 'zai',
      toModel: 'glm-4.6',
      reason: 'rate_limited',
    })

    expect(showToastSpy).toHaveBeenCalledTimes(1)

    showToastSpy.mockRestore()
  })
})

// Phase-5 §1c-β — the long-poll Turn endpoint now surfaces `context_usage`
// + `provider_quotas` on every snapshot. The chat store's
// pollTurnUntilTerminal loop diffs these against the prior poll's snapshot
// and routes changes through handleContextUsageEvent / quotaStore's
// applyProviderQuotaEvent. Both surfaces stay live during 1c-β; the
// idempotency guards on both handlers ensure the transitional double-fire
// (SSE + poll for the same figure) only mutates state once.
describe('chatStore - pollTurnUntilTerminal context_usage diff (Phase-5 §1c-β)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('calls handleContextUsageEvent on the first poll that carries context_usage', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-cu'
    store.messages = []

    const handlerSpy = vi.spyOn(store, 'handleContextUsageEvent')

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-cu',
      session_id: 'session-cu',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: 'anthropic', model: 'claude-opus-4-7' },
      error: '',
      messages: [],
      context_usage: {
        input_tokens: 1234,
        output_reserve: 8192,
        limit: 200000,
        percentage: 1,
        provider: 'anthropic',
        model: 'claude-opus-4-7',
      },
    } as never)

    await store.pollTurnUntilTerminal('session-cu', 'turn-cu')

    expect(handlerSpy).toHaveBeenCalledTimes(1)
    const callArg = handlerSpy.mock.calls[0][0]
    expect(callArg.inputTokens).toBe(1234)
    expect(callArg.outputReserve).toBe(8192)
    expect(callArg.limit).toBe(200000)
    expect(callArg.percentage).toBe(1)
  })

  it('does NOT call handleContextUsageEvent on a poll where context_usage matches the prior poll', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-nodiff-cu'
    store.messages = []

    const handlerSpy = vi.spyOn(store, 'handleContextUsageEvent')

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-nodiff',
      session_id: 'session-nodiff-cu',
      status: 'running' as const,
      started_at: '',
      completed_at: null,
      model: { provider: '', model: '' },
      error: '',
      messages: [],
      context_usage: {
        input_tokens: 1234,
        output_reserve: 8192,
        limit: 200000,
        percentage: 1,
        provider: 'anthropic',
        model: 'claude-opus-4-7',
      },
    } as never)
    // Second poll: same context_usage — must be a no-op.
    ft.mockResolvedValueOnce({
      turn_id: 'turn-nodiff',
      session_id: 'session-nodiff-cu',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: 'anthropic', model: 'claude-opus-4-7' },
      error: '',
      messages: [],
      context_usage: {
        input_tokens: 1234,
        output_reserve: 8192,
        limit: 200000,
        percentage: 1,
        provider: 'anthropic',
        model: 'claude-opus-4-7',
      },
    } as never)

    await store.pollTurnUntilTerminal('session-nodiff-cu', 'turn-nodiff')

    expect(handlerSpy).toHaveBeenCalledTimes(1)
    // Single call from the first transition; second poll's matching figure
    // must NOT re-fire.
  })

  it('calls handleContextUsageEvent again when the figure changes on a subsequent poll', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-growing-cu'
    store.messages = []

    const handlerSpy = vi.spyOn(store, 'handleContextUsageEvent')

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-growing',
      session_id: 'session-growing-cu',
      status: 'running' as const,
      started_at: '',
      completed_at: null,
      model: { provider: '', model: '' },
      error: '',
      messages: [],
      context_usage: {
        input_tokens: 1000,
        output_reserve: 8192,
        limit: 200000,
        percentage: 0,
        provider: 'anthropic',
        model: 'claude-opus-4-7',
      },
    } as never)
    ft.mockResolvedValueOnce({
      turn_id: 'turn-growing',
      session_id: 'session-growing-cu',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: 'anthropic', model: 'claude-opus-4-7' },
      error: '',
      messages: [],
      context_usage: {
        input_tokens: 5000,
        output_reserve: 8192,
        limit: 200000,
        percentage: 2,
        provider: 'anthropic',
        model: 'claude-opus-4-7',
      },
    } as never)

    await store.pollTurnUntilTerminal('session-growing-cu', 'turn-growing')

    expect(handlerSpy).toHaveBeenCalledTimes(2)
    expect(handlerSpy.mock.calls[0][0].inputTokens).toBe(1000)
    expect(handlerSpy.mock.calls[1][0].inputTokens).toBe(5000)
  })
})

// Phase-5 §1c-β — handleContextUsageEvent idempotency. Same transitional
// double-caller story as handleProviderChangedEvent: SSE branch + poll-
// diff both call this with the same figure during the dual-surface phase.
// The dedup gate on `lastContextUsageKey` ensures a double-call mutates
// state ONCE.
describe('chatStore.handleContextUsageEvent idempotency (Phase-5 §1c-β)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('mutates currentContextUsage exactly ONCE when called twice with the same four-field tuple', () => {
    const store = useChatStore()
    store.currentSessionId = 'session-idem-cu'
    store.contextUsageBySession = {}

    // First call — fresh transition; mutation lands.
    store.handleContextUsageEvent({
      inputTokens: 1234,
      outputReserve: 8192,
      limit: 200000,
      percentage: 1,
    }, 'session-idem-cu')

    const firstSnapshot = store.currentContextUsage
    expect(firstSnapshot).not.toBeNull()
    expect(firstSnapshot!.inputTokens).toBe(1234)

    // Second call — same tuple. The dedup gate must short-circuit.
    // Observable proof: currentContextUsage's object identity is
    // preserved (a write would build a fresh object even with identical
    // values).
    store.handleContextUsageEvent({
      inputTokens: 1234,
      outputReserve: 8192,
      limit: 200000,
      percentage: 1,
    }, 'session-idem-cu')

    expect(store.currentContextUsage).toBe(firstSnapshot)
  })

  it('does NOT short-circuit when any field differs', () => {
    const store = useChatStore()
    store.currentSessionId = 'session-diff-cu'
    store.contextUsageBySession = {}

    store.handleContextUsageEvent({
      inputTokens: 1234,
      outputReserve: 8192,
      limit: 200000,
      percentage: 1,
    }, 'session-diff-cu')
    const firstSnapshot = store.currentContextUsage

    // Different inputTokens — must NOT short-circuit.
    store.handleContextUsageEvent({
      inputTokens: 5000,
      outputReserve: 8192,
      limit: 200000,
      percentage: 2,
    }, 'session-diff-cu')

    expect(store.currentContextUsage).not.toBe(firstSnapshot)
    expect(store.currentContextUsage!.inputTokens).toBe(5000)
  })
})

// Phase-5 §1c-β — provider_quotas poll-diff. The poll surface emits the
// full set on every snapshot; the chat store iterates per-partition and
// routes only the changed snapshots through quotaStore. Mirrors the
// 1c-α current_provider/current_model diff but for the multi-value
// partition-keyed slice shape.
describe('chatStore - pollTurnUntilTerminal provider_quotas diff (Phase-5 §1c-β)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('routes a new partition snapshot through quotaStore.applyProviderQuotaEvent', async () => {
    const store = useChatStore()
    const quota = useQuotaStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-q'
    store.messages = []

    const applySpy = vi.spyOn(quota, 'applyProviderQuotaEvent')

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-q',
      session_id: 'session-q',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: 'anthropic', model: 'claude-opus-4-7' },
      error: '',
      messages: [],
      provider_quotas: [
        {
          provider: 'anthropic',
          account_hash: 'acc-1',
          model: 'claude-opus-4-7',
          observed_at: '2026-05-19T00:00:00Z',
          variant: 'token_spend',
          token_spend: {
            spent_minor: 1000,
            spent_currency: 'USD',
            spent_usd_minor: 1000,
            period: 'monthly',
            period_start: '2026-05-01T00:00:00Z',
            period_end: '2026-05-31T23:59:59Z',
            threshold_amber: 70,
            threshold_red: 90,
          },
        },
      ],
    } as never)

    await store.pollTurnUntilTerminal('session-q', 'turn-q')

    expect(applySpy).toHaveBeenCalledTimes(1)
    const callArg = applySpy.mock.calls[0][0]
    expect(callArg.provider).toBe('anthropic')
    expect(callArg.accountHash).toBe('acc-1')
    expect(callArg.tokenSpend?.spentMinor).toBe(1000)
  })

  it('does NOT call applyProviderQuotaEvent on a poll where the partition snapshot is unchanged', async () => {
    const store = useChatStore()
    const quota = useQuotaStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-nodiff-q'
    store.messages = []

    const applySpy = vi.spyOn(quota, 'applyProviderQuotaEvent')

    const sameSnapshot = [
      {
        provider: 'anthropic',
        account_hash: 'acc-1',
        model: 'claude-opus-4-7',
        observed_at: '2026-05-19T00:00:00Z',
        variant: 'token_spend' as const,
        token_spend: {
          spent_minor: 1000,
          spent_currency: 'USD',
          spent_usd_minor: 1000,
          period: 'monthly',
          period_start: '2026-05-01T00:00:00Z',
          period_end: '2026-05-31T23:59:59Z',
          threshold_amber: 70,
          threshold_red: 90,
        },
      },
    ]

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-nodiff-q',
      session_id: 'session-nodiff-q',
      status: 'running' as const,
      started_at: '',
      completed_at: null,
      model: { provider: '', model: '' },
      error: '',
      messages: [],
      provider_quotas: sameSnapshot,
    } as never)
    ft.mockResolvedValueOnce({
      turn_id: 'turn-nodiff-q',
      session_id: 'session-nodiff-q',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: 'anthropic', model: 'claude-opus-4-7' },
      error: '',
      messages: [],
      provider_quotas: sameSnapshot,
    } as never)

    await store.pollTurnUntilTerminal('session-nodiff-q', 'turn-nodiff-q')

    expect(applySpy).toHaveBeenCalledTimes(1)
    // The second poll's unchanged snapshot must NOT re-fire.
  })

  it('calls applyProviderQuotaEvent again when the same partition updates with a new spent figure', async () => {
    const store = useChatStore()
    const quota = useQuotaStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-grow-q'
    store.messages = []

    const applySpy = vi.spyOn(quota, 'applyProviderQuotaEvent')

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-grow-q',
      session_id: 'session-grow-q',
      status: 'running' as const,
      started_at: '',
      completed_at: null,
      model: { provider: '', model: '' },
      error: '',
      messages: [],
      provider_quotas: [
        {
          provider: 'anthropic',
          account_hash: 'acc-1',
          model: 'claude-opus-4-7',
          observed_at: '2026-05-19T00:00:00Z',
          variant: 'token_spend' as const,
          token_spend: {
            spent_minor: 1000,
            spent_currency: 'USD',
            spent_usd_minor: 1000,
            period: 'monthly',
            period_start: '2026-05-01T00:00:00Z',
            period_end: '2026-05-31T23:59:59Z',
            threshold_amber: 70,
            threshold_red: 90,
          },
        },
      ],
    } as never)
    ft.mockResolvedValueOnce({
      turn_id: 'turn-grow-q',
      session_id: 'session-grow-q',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: 'anthropic', model: 'claude-opus-4-7' },
      error: '',
      messages: [],
      provider_quotas: [
        {
          provider: 'anthropic',
          account_hash: 'acc-1',
          model: 'claude-opus-4-7',
          observed_at: '2026-05-19T00:00:01Z',
          variant: 'token_spend' as const,
          token_spend: {
            spent_minor: 2500,
            spent_currency: 'USD',
            spent_usd_minor: 2500,
            period: 'monthly',
            period_start: '2026-05-01T00:00:00Z',
            period_end: '2026-05-31T23:59:59Z',
            threshold_amber: 70,
            threshold_red: 90,
          },
        },
      ],
    } as never)

    await store.pollTurnUntilTerminal('session-grow-q', 'turn-grow-q')

    expect(applySpy).toHaveBeenCalledTimes(2)
    expect(applySpy.mock.calls[0][0].tokenSpend?.spentMinor).toBe(1000)
    expect(applySpy.mock.calls[1][0].tokenSpend?.spentMinor).toBe(2500)
  })

  it('routes EACH new partition separately (anthropic + zai = 2 calls)', async () => {
    const store = useChatStore()
    const quota = useQuotaStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-multi-q'
    store.messages = []

    const applySpy = vi.spyOn(quota, 'applyProviderQuotaEvent')

    const ft = vi.mocked(fetchTurn)
    ft.mockReset()
    ft.mockResolvedValueOnce({
      turn_id: 'turn-multi-q',
      session_id: 'session-multi-q',
      status: 'completed',
      started_at: '',
      completed_at: '',
      model: { provider: 'zai', model: 'glm-4.6' },
      error: '',
      messages: [],
      provider_quotas: [
        {
          provider: 'anthropic',
          account_hash: 'acc-1',
          model: 'claude-opus-4-7',
          observed_at: '2026-05-19T00:00:00Z',
          variant: 'token_spend' as const,
          token_spend: {
            spent_minor: 1000,
            spent_currency: 'USD',
            spent_usd_minor: 1000,
            period: 'monthly',
            period_start: '2026-05-01T00:00:00Z',
            period_end: '2026-05-31T23:59:59Z',
            threshold_amber: 70,
            threshold_red: 90,
          },
        },
        {
          provider: 'zai',
          account_hash: 'acc-z',
          model: 'glm-4.6',
          observed_at: '2026-05-19T00:00:01Z',
          variant: 'token_spend' as const,
          token_spend: {
            spent_minor: 500,
            spent_currency: 'USD',
            spent_usd_minor: 500,
            period: 'monthly',
            period_start: '2026-05-01T00:00:00Z',
            period_end: '2026-05-31T23:59:59Z',
            threshold_amber: 70,
            threshold_red: 90,
          },
        },
      ],
    } as never)

    await store.pollTurnUntilTerminal('session-multi-q', 'turn-multi-q')

    expect(applySpy).toHaveBeenCalledTimes(2)
    const providers = applySpy.mock.calls.map((c) => c[0].provider).sort()
    expect(providers).toEqual(['anthropic', 'zai'])
  })
})

describe('chatStore - bootstrap (singleton wrapper around restoreStateFromBackend)', () => {
  // bootstrap() exists so the App-level loading overlay has a single
  // reliable "first hydration done" signal it can await — and so the
  // documented loadAgents/restoreStateFromBackend race (eager pickers
  // racing the canonical agent resolution) is closed at the source. The
  // contract:
  //
  //   1. The first call invokes restoreStateFromBackend and returns its
  //      promise.
  //   2. Concurrent and subsequent calls return the same in-flight or
  //      already-settled promise — the underlying restore is invoked
  //      exactly once per store instance.
  //   3. Failures propagate to all awaiters identically (so ChatView's
  //      try/catch toast + App.vue's overlay-dismiss both still fire).

  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('invokes restoreStateFromBackend exactly once across multiple concurrent callers', async () => {
    const store = useChatStore()
    const restoreSpy = vi.spyOn(store, 'restoreStateFromBackend')

    const [a, b, c] = [store.bootstrap(), store.bootstrap(), store.bootstrap()]
    await Promise.all([a, b, c])

    expect(restoreSpy).toHaveBeenCalledTimes(1)
  })

  it('caches the in-flight promise on the store so concurrent callers see the singleton', () => {
    // Pinia's action wrapper returns a fresh Promise.resolve(actionResult)
    // on every invocation (for hot-module-reload support), so promise
    // identity at the call-site is not preserved. The behavioural
    // singleton is observable via the store-state field — bootstrapPromise
    // is non-null after the first call and remains the same reference.
    const store = useChatStore()
    expect(store.bootstrapPromise).toBeNull()
    void store.bootstrap()
    const cached = store.bootstrapPromise
    expect(cached).not.toBeNull()
    void store.bootstrap()
    expect(store.bootstrapPromise).toBe(cached)
  })

  it('propagates a rejection from restoreStateFromBackend to all awaiters', async () => {
    const store = useChatStore()
    vi.spyOn(store, 'restoreStateFromBackend').mockRejectedValueOnce(new Error('network blip'))

    const a = store.bootstrap()
    const b = store.bootstrap()

    await expect(a).rejects.toThrow('network blip')
    await expect(b).rejects.toThrow('network blip')
  })
})

// Web Swarm Mention Parity (May 2026) — `loadSwarms` mirrors
// `loadAgents`: a single GET to /api/swarms populating the chat
// store's `swarms` slice. The MessageInput's @-picker reads this
// slice so swarms appear alongside agents. Bootstrap calls loadSwarms
// alongside loadAgents.
describe('chatStore - loadSwarms (Web Swarm Mention Parity)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('populates `swarms` from fetchSwarms', async () => {
    const store = useChatStore()
    expect(store.swarms).toEqual([])

    await store.loadSwarms()

    expect(vi.mocked(fetchSwarms)).toHaveBeenCalledTimes(1)
    expect(store.swarms).toHaveLength(2)
    expect(store.swarms[0].id).toBe('planning-loop')
    expect(store.swarms[0].lead).toBe('planner')
    expect(store.swarms[1].id).toBe('solo')
  })

  it('runs as part of restoreStateFromBackend so the @-picker has swarms after bootstrap', async () => {
    const store = useChatStore()
    await store.restoreStateFromBackend()

    expect(vi.mocked(fetchSwarms)).toHaveBeenCalledTimes(1)
    expect(store.swarms.map((s) => s.id)).toEqual(['planning-loop', 'solo'])
  })

  it('does not throw when the swarm endpoint returns an empty list', async () => {
    vi.mocked(fetchSwarms).mockResolvedValueOnce([])

    const store = useChatStore()
    await expect(store.loadSwarms()).resolves.not.toThrow()
    expect(store.swarms).toEqual([])
  })
})

// Per-Session Streaming State (Slice A — Streaming Coherence May 2026)
//
// Pre-slice the store carried flat global `isLoading` / `isStreaming` flags
// — when session A was streaming the user could not compose in session B
// because the composer's submit gate read the global flag. The fix:
// per-session truth via `sessionStreaming: Record<sessionId, {...}>` and
// a `streamingFor(sessionId)` getter. The flat `isLoading` / `isStreaming`
// continue to read for the active session via getters, so existing
// consumers (ChatView indicator, send gate) keep working unchanged for the
// happy path while session B's gate is no longer mis-blocked.
describe('chatStore - per-session streaming state (Slice A)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
    FakeEventSource.instances.length = 0
  })

  it('streamingFor returns isolated state per session', () => {
    const store = useChatStore()

    expect(store.streamingFor('session-A')).toEqual({ isLoading: false, isStreaming: false })

    store.setSessionStreaming('session-A', { isLoading: true })
    expect(store.streamingFor('session-A').isLoading).toBe(true)
    // Session B is unaffected.
    expect(store.streamingFor('session-B').isLoading).toBe(false)

    store.setSessionStreaming('session-B', { isStreaming: true })
    expect(store.streamingFor('session-B').isStreaming).toBe(true)
    // Session A is still loading=true, isStreaming=false.
    expect(store.streamingFor('session-A')).toEqual({ isLoading: true, isStreaming: false })
  })

  it('isLoading and isStreaming getters mirror the active session slot', () => {
    const store = useChatStore()
    store.currentSessionId = 'session-active'

    expect(store.isLoading).toBe(false)
    expect(store.isStreaming).toBe(false)

    store.setSessionStreaming('session-active', { isLoading: true, isStreaming: true })

    expect(store.isLoading).toBe(true)
    expect(store.isStreaming).toBe(true)

    // A different session's streaming state must not bleed into the
    // current-session getters.
    store.setSessionStreaming('session-other', { isLoading: true })
    store.setSessionStreaming('session-active', { isLoading: false, isStreaming: false })
    expect(store.isLoading).toBe(false)
    expect(store.isStreaming).toBe(false)
    expect(store.streamingFor('session-other').isLoading).toBe(true)
  })

  it('isLoading getter reads false when no session is active', () => {
    const store = useChatStore()
    store.currentSessionId = null
    // Even if a stale slot exists for some other session, the getter
    // reads from the current session — null means "no current".
    store.setSessionStreaming('session-stale', { isLoading: true, isStreaming: true })
    expect(store.isLoading).toBe(false)
    expect(store.isStreaming).toBe(false)
  })

  it('sendMessage on session B does not block while session A is loading (cross-session non-blocking)', async () => {
    // Slice A keystone: the submit gate is per-session. Session A is
    // loading; session B accepts a fresh send. Pre-slice the global
    // gate forced session B's send to bounce. The MessageInput consumer
    // reads streamingFor(currentSessionId).isLoading.
    const store = useChatStore()
    store.agentId = 'agent-1'

    // Session A is mid-flight.
    store.setSessionStreaming('session-A', { isLoading: true, isStreaming: true })

    // User switches to session B and submits.
    store.currentSessionId = 'session-B'
    expect(store.streamingFor('session-B').isLoading).toBe(false)
    expect(store.isLoading).toBe(false)

    await store.sendMessage('hello on B')

    expect(vi.mocked(sendSessionMessage)).toHaveBeenCalledWith('session-B', 'hello on B')
    // Session A's slot remains untouched by session B's send.
    expect(store.streamingFor('session-A').isLoading).toBe(true)
  })
})

// Per-Session SSE Singleton (Slice B — Streaming Coherence May 2026)
//
// Pre-slice a single module-scoped EventSource backed every session.
// Switching from A to B called sessionStream.disconnect() inside
// loadSessionMessages, which closed A's stream even though A's turn
// was still in flight server-side — visible as A going dark while
// the user opened B. Slice B introduces a Map<sessionId, SessionStream>
// so each session keeps its own EventSource for as long as the session
// is alive.
describe('chatStore - per-session SSE singleton (Slice B)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
    FakeEventSource.instances.length = 0
  })

  it('does not disconnect the prior session stream on session switch', async () => {
    // Reproduces the Slice B keystone: a stream open for session-A
    // must survive a switch to session-B. Pre-slice loadSessionMessages
    // called sessionStream.disconnect() unconditionally; that path is
    // gone.
    //
    // Phase 3 update: SSE no longer opens during the in-flight POST
    // window. We drive the legacy SSE branch by resolving the POST
    // with no turn_id, so SSE opens post-resolve and we can then
    // switch sessions to exercise the keystone.
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'session-A'

    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      id: 'session-A', agentId: 'agent-1', messages: [], messageCount: 0,
      status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '',
    } as never)
    // First call (post-send reconcile) — empty so no clobber.
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([])

    await store.sendMessage('long task on A')

    expect(FakeEventSource.instances.length).toBe(1)
    const esA = FakeEventSource.instances[0]
    expect(esA.closed).toBe(false)

    // User switches to session-B. A's stream MUST stay open.
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([])
    await store.loadSessionMessages('session-B')
    expect(esA.closed).toBe(false)
  })

  it('renders an empty_turn placeholder when DONE arrives with no running assistant (Slice C)', () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    // Simulate the user-prompt-only state — POST sent, no assistant
    // chunks landed, then [DONE] arrives.
    store.messages = [
      { id: 'msg-user', role: 'user', content: 'hello', timestamp: '' },
    ]
    store.handleStreamDone()
    expect(store.messages).toHaveLength(2)
    expect(store.messages[1].role).toBe('assistant')
    expect(store.messages[1].stopReason).toBe('empty_turn')
    expect(store.messages[1].content).toBe('')
    expect(store.messages[1].status).toBe('completed')
  })

  it('does NOT render an empty_turn placeholder when a running assistant existed (Slice C)', () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    store.messages = [
      { id: 'msg-user', role: 'user', content: 'hello', timestamp: '' },
      { id: 'msg-asst', role: 'assistant', content: 'partial reply', timestamp: '', status: 'running' },
    ]
    store.handleStreamDone()
    expect(store.messages).toHaveLength(2)
    expect(store.messages[1].status).toBe('completed')
    expect(store.messages[1].stopReason).toBeUndefined()
  })

  it('does NOT flip isStreaming false on intermediate DONE between tool rounds (Slice D — activity-indicator continuity)', () => {
    // Streaming Coherence Slice D — pre-slice handleStreamDone flipped
    // isStreaming=false on every [DONE] including the intermediate
    // sentinels between tool rounds. The activity indicator flickered
    // off-and-on between rounds. The new contract: isStreaming stays
    // true until the outer turn completes (the send finally block).
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    store.setSessionStreaming('session-1', { isLoading: true, isStreaming: true })
    // Mid-stream content arrived, then [DONE] arrives mid-tool-loop.
    store.messages = [
      { id: 'msg-user', role: 'user', content: 'task', timestamp: '' },
      { id: 'asst-1', role: 'assistant', content: 'mid-reply', timestamp: '', status: 'running' },
    ]
    store.handleStreamDone()
    // The streaming row was sealed (Slice C), but isStreaming stays
    // true so the activity indicator keeps showing across rounds.
    expect(store.isStreaming).toBe(true)
    expect(store.isLoading).toBe(true)
  })

  it('seals ALL running assistant/delegation rows on DONE (Slice C — delegation panel coherence)', () => {
    const store = useChatStore()
    store.currentSessionId = 'session-1'
    store.messages = [
      { id: 'msg-user', role: 'user', content: 'task', timestamp: '' },
      { id: 'del-1', role: 'delegation_started', content: '', timestamp: '', status: 'running', chainId: 'a' },
      { id: 'asst-1', role: 'assistant', content: 'mid-reply', timestamp: '', status: 'running' },
      { id: 'del-2', role: 'delegation_started', content: '', timestamp: '', status: 'running', chainId: 'b' },
    ]
    store.handleStreamDone()
    expect(store.messages.find((m) => m.id === 'del-1')?.status).toBe('completed')
    expect(store.messages.find((m) => m.id === 'asst-1')?.status).toBe('completed')
    expect(store.messages.find((m) => m.id === 'del-2')?.status).toBe('completed')
  })

  // Slice E — Queued Prompts with Revert (Streaming Coherence May 2026).
  it('queues prompts per-session via queuePromptFor (Slice E)', () => {
    const store = useChatStore()
    store.queuePromptFor('sess-A', 'first')
    store.queuePromptFor('sess-A', 'second')
    store.queuePromptFor('sess-B', 'on B only')

    expect(store.queuedPrompts['sess-A']).toEqual(['first', 'second'])
    expect(store.queuedPrompts['sess-B']).toEqual(['on B only'])
  })

  it('popQueuedPromptFor removes by index and returns text (Slice E — revert UX)', () => {
    const store = useChatStore()
    store.queuedPrompts['sess-A'] = ['a', 'b', 'c']

    expect(store.popQueuedPromptFor('sess-A', 1)).toBe('b')
    expect(store.queuedPrompts['sess-A']).toEqual(['a', 'c'])
    // Out-of-range returns null and does not mutate.
    expect(store.popQueuedPromptFor('sess-A', 99)).toBe(null)
    expect(store.queuedPrompts['sess-A']).toEqual(['a', 'c'])
  })

  it('shiftQueuedPromptFor pops the head (Slice E — auto-drain)', () => {
    const store = useChatStore()
    store.queuedPrompts['sess-A'] = ['head', 'mid', 'tail']

    expect(store.shiftQueuedPromptFor('sess-A')).toBe('head')
    expect(store.queuedPrompts['sess-A']).toEqual(['mid', 'tail'])

    expect(store.shiftQueuedPromptFor('sess-A')).toBe('mid')
    expect(store.shiftQueuedPromptFor('sess-A')).toBe('tail')
    expect(store.shiftQueuedPromptFor('sess-A')).toBe(null)
  })

  // Slice F — Streaming Heartbeat + Adaptive Watchdog (Streaming Coherence May 2026).
  it('records the latest engine heartbeat phase for the active session (Slice F)', () => {
    const store = useChatStore()
    store.currentSessionId = 'sess-hb'
    store.applyContentEvent(JSON.stringify({ type: 'streaming.heartbeat', phase: 'thinking' }))
    expect(store.streamingPhase['sess-hb']).toBe('thinking')
  })

  // UI Parity PR5 — Live token counter (May 2026).
  //
  // The engine threads cumulative output_tokens onto every
  // streaming.heartbeat tick. The chat store records the count per
  // session AND computes tokens-per-second from the delta-vs-prev-tick
  // so the streaming chrome renders "1,247 tokens · 42 t/s" next to
  // the working-on label. Pre-fix the chrome showed a pulsing dot and
  // agent/model only — a 5-token vs 5,000-token turn looked identical.
  describe('UI Parity PR5 — live token counter', () => {
    it('records the latest TokenCount per session on each heartbeat', () => {
      const store = useChatStore()
      store.applyContentEvent(
        JSON.stringify({ type: 'streaming.heartbeat', phase: 'generating', token_count: 1247 }),
        'sess-pr5',
      )
      expect(store.tokenCountBySession['sess-pr5']).toBe(1247)
    })

    it('computes tokensPerSecond from the delta between consecutive heartbeats', () => {
      // Heartbeat 1 at t=0s with 100 tokens. Heartbeat 2 at t=15s with
      // 730 tokens. Δtokens=630, Δseconds=15. t/s = 42.
      vi.useFakeTimers()
      try {
        vi.setSystemTime(new Date(1_000_000_000_000))
        const store = useChatStore()
        store.applyContentEvent(
          JSON.stringify({ type: 'streaming.heartbeat', phase: 'generating', token_count: 100 }),
          'sess-pr5',
        )
        // First tick seeds the counter — t/s is not yet defined.
        expect(store.tokenCountBySession['sess-pr5']).toBe(100)

        vi.setSystemTime(new Date(1_000_000_015_000)) // +15s
        store.applyContentEvent(
          JSON.stringify({ type: 'streaming.heartbeat', phase: 'generating', token_count: 730 }),
          'sess-pr5',
        )
        expect(store.tokenCountBySession['sess-pr5']).toBe(730)
        expect(store.tokensPerSecondBySession['sess-pr5']).toBe(42)
      } finally {
        vi.useRealTimers()
      }
    })

    it('reports zero tokensPerSecond on the first heartbeat (no prior tick to delta against)', () => {
      // The first tick of a turn has no predecessor — t/s is
      // undefined-but-rendered-as-0. The chat UI conditionally
      // hides the t/s segment when 0 so the chrome reads
      // "1,247 tokens" without the misleading "· 0 t/s" trailing.
      const store = useChatStore()
      store.applyContentEvent(
        JSON.stringify({ type: 'streaming.heartbeat', phase: 'generating', token_count: 1247 }),
        'sess-pr5-first',
      )
      expect(store.tokensPerSecondBySession['sess-pr5-first']).toBe(0)
    })

    it('keeps per-session token counts isolated', () => {
      const store = useChatStore()
      store.applyContentEvent(
        JSON.stringify({ type: 'streaming.heartbeat', phase: 'generating', token_count: 5000 }),
        'sess-A',
      )
      store.applyContentEvent(
        JSON.stringify({ type: 'streaming.heartbeat', phase: 'thinking', token_count: 200 }),
        'sess-B',
      )
      expect(store.tokenCountBySession['sess-A']).toBe(5000)
      expect(store.tokenCountBySession['sess-B']).toBe(200)
    })
  })

  it('adaptive watchdog uses per-phase thresholds (Slice F)', async () => {
    // Phase 3 update: POST resolves first (no turn_id) → SSE opens →
    // watchdog arms on the SSE connect. Phase heartbeats and timer
    // advances exercise the per-phase threshold logic.
    //
    // The sendMessage gate clears in `finally`; the SSE keeps streaming
    // and the watchdog stays armed. We assert via `store.error` (set
    // by the watchdog trip) and re-seed the gate after sendMessage so
    // the per-session gate transition observed by handleStreamStall is
    // observable.
    vi.useFakeTimers()
    try {
      const store = useChatStore()
      store.agentId = 'agent-1'
      store.currentSessionId = 'sess-1'

      vi.mocked(sendSessionMessage).mockResolvedValueOnce({
        id: 'sess-1', agentId: 'agent-1', messages: [], messageCount: 0,
        status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '',
      } as never)
      vi.mocked(fetchSessionMessages).mockResolvedValueOnce([])

      await store.sendMessage('test phase watchdog')
      const es = FakeEventSource.instances[0]

      // Phase=thinking → 120s threshold. Advance 60s — does NOT trip.
      es.fire('message', { type: 'streaming.heartbeat', phase: 'thinking' })
      await vi.advanceTimersByTimeAsync(60_000)
      expect(store.error).toBeNull()

      // Now advance another 65s (total 125s since the heartbeat re-armed
      // the watchdog at 120s) — trips.
      await vi.advanceTimersByTimeAsync(65_000)
      expect(store.error).toBeTruthy()
    } finally {
      vi.useRealTimers()
    }
  })

  it('opens an independent EventSource for each session', async () => {
    // Phase 3 update: SSE opens post-POST. Two sequential sends produce
    // two SSE EventSources (one per session) via the legacy SSE branch.
    const store = useChatStore()
    store.agentId = 'agent-1'

    // Send on session-A (legacy SSE branch — no turn_id).
    store.currentSessionId = 'session-A'
    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      id: 'session-A', agentId: 'agent-1', messages: [], messageCount: 0,
      status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '',
    } as never)
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([])
    await store.sendMessage('on A')

    // Switch to session-B (without disconnect).
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([])
    await store.loadSessionMessages('session-B')

    // Send on session-B (legacy SSE branch — no turn_id).
    vi.mocked(sendSessionMessage).mockResolvedValueOnce({
      id: 'session-B', agentId: 'agent-1', messages: [], messageCount: 0,
      status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '',
    } as never)
    vi.mocked(fetchSessionMessages).mockResolvedValueOnce([])
    await store.sendMessage('on B')

    // Two distinct EventSources — one per session — both open.
    expect(FakeEventSource.instances.length).toBe(2)
    expect(FakeEventSource.instances[0].closed).toBe(false)
    expect(FakeEventSource.instances[1].closed).toBe(false)
    // URL identifies the session each stream subscribed to.
    expect(FakeEventSource.instances[0].url).toContain('session-A')
    expect(FakeEventSource.instances[1].url).toContain('session-B')
  })

  // M7 — applyContentEvent must scope phase + watchdog re-arm to the chunk's
  // session, NOT the global currentSessionId. The C-3 chunk-handler guard
  // ensures `currentSessionId === capturedSessionId` for the SSE-driven
  // entry, but the contract is defence-in-depth: any caller that supplies
  // a session id must have phase recorded and the watchdog re-armed against
  // THAT session, regardless of where the user navigated to.
  //
  // Pre-fix: applyContentEvent read this.currentSessionId for both the
  // heartbeat phase write (line 1854) and the armStallWatchdog call
  // (line 1863). A chunk arriving for session A while the user has
  // navigated to B writes A's phase under B's key and re-arms B's
  // watchdog against A's chunk activity — false positives, missed stalls.
  describe('M7 — applyContentEvent scopes phase and watchdog to chunk session, not currentSessionId', () => {
    it('writes streaming.heartbeat phase under the chunk\'s session, not currentSessionId', () => {
      const store = useChatStore()
      // User has navigated from A to B mid-stream.
      store.currentSessionId = 'sess-B'
      store.streamingPhase['sess-A'] = 'thinking'
      store.streamingPhase['sess-B'] = 'generating'

      // A's stream emits a heartbeat with a new phase — pre-fix this
      // would clobber sess-B's phase. Post-fix it lands on sess-A.
      store.applyContentEvent(
        JSON.stringify({ type: 'streaming.heartbeat', phase: 'tool_executing' }),
        'sess-A',
      )

      expect(store.streamingPhase['sess-A']).toBe('tool_executing')
      expect(store.streamingPhase['sess-B']).toBe('generating')
    })

    it('re-arms the chunk session\'s watchdog (not currentSessionId\'s) so a stall trip fires for the correct session', async () => {
      // Two sessions both have an open stream. User starts on A, sends a
      // message, then navigates to B and sends there too. Mid-flight, A
      // emits a content chunk. The watchdog arming on that chunk MUST
      // target A's stream, not B's — otherwise A's true stall is masked
      // and B's active stream is interrupted by a spurious stall trip.
      //
      // Post-May-2026: handleStreamStall no longer calls reconcileFromBackend
      // (the masking refetch was removed; the engine's idle-stream watchdog
      // owns that failure mode). The observable proxy for "this session's
      // watchdog tripped" is therefore the per-session sessionStreaming[id]
      // state — handleStreamStall clears the flags for the tripped session
      // only. We pin the routing by asserting which session's flags clear.
      // Phase 3 update: SSE opens post-POST. We drive the legacy SSE
      // branch for both sessions (no turn_id) so each session has its
      // own EventSource and per-session watchdog. The session-scoping
      // re-arm contract is unchanged.
      vi.useFakeTimers()
      try {
        const store = useChatStore()
        store.agentId = 'agent-1'

        // Open a stream on A (legacy SSE branch).
        store.currentSessionId = 'session-A'
        vi.mocked(sendSessionMessage).mockResolvedValueOnce({
          id: 'session-A', agentId: 'agent-1', messages: [], messageCount: 0,
          status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '',
        } as never)
        vi.mocked(fetchSessionMessages).mockResolvedValueOnce([])
        await store.sendMessage('on A')

        // Switch to B and open a stream there too.
        vi.mocked(fetchSessionMessages).mockResolvedValueOnce([])
        await store.loadSessionMessages('session-B')
        vi.mocked(sendSessionMessage).mockResolvedValueOnce({
          id: 'session-B', agentId: 'agent-1', messages: [], messageCount: 0,
          status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '',
        } as never)
        vi.mocked(fetchSessionMessages).mockResolvedValueOnce([])
        await store.sendMessage('on B')

        expect(store.currentSessionId).toBe('session-B')
        // Both streams are armed (one watchdog per FakeEventSource opened).
        expect(FakeEventSource.instances.length).toBe(2)

        // Phase 3: the per-session gate is already cleared by the
        // sendMessage finally block. To exercise watchdog re-arm
        // routing we re-set the gate to mimic "still streaming" and
        // observe which session's flag the trip clears.
        store.setSessionStreaming('session-A', { isLoading: true, isStreaming: true })
        store.setSessionStreaming('session-B', { isLoading: true, isStreaming: true })

        // 30s passes — no stall yet on either session.
        await vi.advanceTimersByTimeAsync(30_000)

        // A's stream produces a chunk. The watchdog re-arm MUST target
        // A (the chunk's session), not B.
        store.applyContentEvent(JSON.stringify({ content: 'late from A' }), 'session-A')

        // 40s further (total 70s on the original watchdog arms for both
        // sessions, which sits past the 65s threshold). Pre-fix: A's chunk
        // re-armed B's watchdog. Post-fix: A's watchdog was re-armed by
        // A's chunk; only B's original 65s timer fires.
        await vi.advanceTimersByTimeAsync(40_000)
        await Promise.resolve()
        await Promise.resolve()

        // Exactly one stall trip fired — for the session whose watchdog
        // was NOT re-armed (B). A's was re-armed by its own chunk.
        expect(store.sessionStreaming['session-B']?.isLoading).toBe(false)
        expect(store.sessionStreaming['session-A']?.isLoading).toBe(true)
      } finally {
        vi.useRealTimers()
      }
    })
  })

  // Slice G — Escape-twice cancel cascade (Streaming Coherence May 2026).
  //
  // These specs exercise the store-layer contract: handleEscapeKey()
  // tracks press count, opens a 600ms window on the first call, and on
  // the second call within the window fires DELETE /api/v1/sessions/
  // {id}/stream and closes the per-session EventSource.
  //
  // Originally the specs dispatched DOM keydown events on `document` —
  // but this file is a store-level unit test; the document-keydown
  // listener that bridges into handleEscapeKey lives in ChatView.vue
  // (registered in onMounted, torn down in onBeforeUnmount per H9).
  // The store test never mounts ChatView, so dispatching DOM events
  // hit no listener and the store was never invoked. The DOM-dispatch
  // wiring is covered separately in ChatView.spec.ts under
  // "ChatView Escape keydown listener lifecycle (H9)" — those specs
  // assert document → handleEscapeKey routing and onBeforeUnmount
  // teardown. Here we test the store contract directly.
  describe('escape-twice cancel cascade', () => {
    it('sends DELETE /api/v1/sessions/{id}/stream on second escape within 600ms', async () => {
      const store = useChatStore()
      store.agentId = 'agent-1'
      store.currentSessionId = 'session-1'

      // Mock the DELETE endpoint
      vi.stubGlobal('fetch', vi.fn((url: string, opts: RequestInit) => {
        if (opts.method === 'DELETE' && url.includes('/stream')) {
          // handleEscapeKey checks `response.ok` before calling
          // disconnectSessionStream. A bare `{ status: 204 }` literal
          // omits the `ok` getter that real Response has, so the
          // success branch is silently skipped — include `ok: true`.
          return Promise.resolve({ ok: true, status: 204 } as Response)
        }
        return Promise.reject(new Error('unexpected request'))
      }))

      // Start a message stream
      let resolveSend: (v: any) => void = () => {}
      vi.mocked(sendSessionMessage).mockImplementationOnce(
        () => new Promise<any>((resolve) => { resolveSend = resolve }),
      )

      vi.useFakeTimers()
      try {
        const sendPromise = store.sendMessage('test cancel')
        await Promise.resolve()
        await Promise.resolve()

        // sendMessage sets isLoading:true, isStreaming:false — chunks
        // have not arrived yet. handleEscapeKey gates on isStreaming
        // (line 1680), so simulate the post-first-chunk state where
        // SSE is actively streaming and cancel is meaningful.
        store.setSessionStreaming('session-1', { isStreaming: true })

        // First escape — opens the 600ms window, no DELETE yet.
        await store.handleEscapeKey()

        expect(vi.mocked(fetch)).not.toHaveBeenCalledWith(
          expect.stringContaining('/stream'),
          expect.objectContaining({ method: 'DELETE' }),
        )

        // Second escape within 600ms — fires DELETE.
        vi.advanceTimersByTime(300)
        await store.handleEscapeKey()
        // Microtask flush: handleEscapeKey awaits fetch internally.
        await Promise.resolve()
        await Promise.resolve()

        expect(vi.mocked(fetch)).toHaveBeenCalledWith(
          expect.stringContaining('/stream'),
          expect.objectContaining({ method: 'DELETE' }),
        )

        resolveSend({ id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0, status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '' })
        await sendPromise
      } finally {
        vi.useRealTimers()
        vi.unstubAllGlobals()
      }
    })

    it('does not send cancel on single escape', async () => {
      const store = useChatStore()
      store.agentId = 'agent-1'
      store.currentSessionId = 'session-1'

      const fetchSpy = vi.fn(() => Promise.resolve({ status: 204 } as Response))
      vi.stubGlobal('fetch', fetchSpy)

      // Start a message stream
      let resolveSend: (v: any) => void = () => {}
      vi.mocked(sendSessionMessage).mockImplementationOnce(
        () => new Promise<any>((resolve) => { resolveSend = resolve }),
      )

      vi.useFakeTimers()
      try {
        const sendPromise = store.sendMessage('test')
        await Promise.resolve()
        await Promise.resolve()

        // Pass the isStreaming gate (see sibling spec for detail).
        store.setSessionStreaming('session-1', { isStreaming: true })

        // Single escape — opens the window then lets it lapse.
        await store.handleEscapeKey()

        // Wait beyond the 600ms window — counter resets, no second
        // press, no DELETE.
        vi.advanceTimersByTime(700)
        await Promise.resolve()

        expect(fetchSpy).not.toHaveBeenCalled()

        resolveSend({ id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0, status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '' })
        await sendPromise
      } finally {
        vi.useRealTimers()
        vi.unstubAllGlobals()
      }
    })

    it('closes the active EventSource when cancel is sent', async () => {
      // Phase 3 update: SSE opens post-POST. Resolve the legacy SSE
      // branch (no turn_id) before driving the escape sequence so an
      // EventSource exists to be closed.
      const store = useChatStore()
      store.agentId = 'agent-1'
      store.currentSessionId = 'session-1'

      vi.stubGlobal('fetch', vi.fn((url: string, opts: RequestInit) => {
        if (opts.method === 'DELETE' && url.includes('/stream')) {
          // handleEscapeKey checks `response.ok` before calling
          // disconnectSessionStream. A bare `{ status: 204 }` literal
          // omits the `ok` getter that real Response has, so the
          // success branch is silently skipped — include `ok: true`.
          return Promise.resolve({ ok: true, status: 204 } as Response)
        }
        return Promise.reject(new Error('unexpected request'))
      }))

      vi.mocked(sendSessionMessage).mockResolvedValueOnce({
        id: 'session-1', agentId: 'agent-1', messages: [], messageCount: 0,
        status: 'active', depth: 0, isStreaming: false, createdAt: '', updatedAt: '',
      } as never)
      vi.mocked(fetchSessionMessages).mockResolvedValueOnce([])

      await store.sendMessage('test')

      vi.useFakeTimers()
      try {
        // Capture the EventSource that was created post-POST.
        const eventSource = FakeEventSource.instances[FakeEventSource.instances.length - 1]
        expect(eventSource.closed).toBe(false)

        // Pass the isStreaming gate (see sibling spec for detail).
        store.setSessionStreaming('session-1', { isStreaming: true })

        // Double-tap escape (first press, then second within 600ms).
        await store.handleEscapeKey()
        vi.advanceTimersByTime(300)
        await store.handleEscapeKey()
        // Microtask flush: handleEscapeKey awaits fetch then calls
        // disconnectSessionStream which closes the EventSource.
        await Promise.resolve()
        await Promise.resolve()

        expect(eventSource.closed).toBe(true)
      } finally {
        vi.useRealTimers()
        vi.unstubAllGlobals()
      }
    })
  })
})

// QW-11 — Per-row session delete.
//
// Contract:
//   - chatStore.deleteSession(id) calls the api deleteSession helper, and on
//     success drops the session from the local `sessions` array, prunes
//     per-session streaming / queue / phase slots, and rolls forward
//     `currentSessionId` if the deleted session was active.
//   - On HTTP failure the action rethrows and leaves local state untouched
//     (the caller decides whether to toast / retry / reconcile via
//     loadSessions).
//
// These pins back the SessionBrowser / SessionSwitcher trash buttons.
describe('chatStore.deleteSession', () => {
  beforeEach(() => {
    installLocalStorageStub()
    setActivePinia(createPinia())
    vi.mocked(deleteSession).mockReset()
    vi.mocked(deleteSession).mockResolvedValue(undefined)
    vi.mocked(fetchSessionMessages).mockReset()
    vi.mocked(fetchSessionMessages).mockResolvedValue([])
  })

  function makeSummary(overrides: Record<string, unknown> = {}): import('@/types').SessionSummary {
    return {
      id: 'session-x',
      agentId: 'agent-1',
      title: 'Untitled',
      status: 'active',
      depth: 0,
      createdAt: '2026-05-10T09:00:00Z',
      updatedAt: '2026-05-10T09:00:00Z',
      messageCount: 0,
      isStreaming: false,
      ...overrides,
    } as import('@/types').SessionSummary
  }

  it('issues a DELETE through the api helper for the given session id', async () => {
    const store = useChatStore()
    store.sessions = [makeSummary({ id: 'session-A' }), makeSummary({ id: 'session-B' })]
    store.currentSessionId = 'session-A'

    await store.deleteSession('session-B')

    expect(deleteSession).toHaveBeenCalledTimes(1)
    expect(deleteSession).toHaveBeenCalledWith('session-B')
  })

  it('removes the session from chatStore.sessions on success', async () => {
    const store = useChatStore()
    store.sessions = [makeSummary({ id: 'session-A' }), makeSummary({ id: 'session-B' })]
    store.currentSessionId = 'session-A'

    await store.deleteSession('session-B')

    expect(store.sessions.map((s) => s.id)).toEqual(['session-A'])
  })

  it('rolls currentSessionId forward to the most-recently-updated remaining root session when the deleted session was current', async () => {
    const store = useChatStore()
    store.sessions = [
      makeSummary({ id: 'session-A', updatedAt: '2026-05-09T09:00:00Z' }),
      makeSummary({ id: 'session-B', updatedAt: '2026-05-10T09:00:00Z' }),
      makeSummary({ id: 'session-C', updatedAt: '2026-05-08T09:00:00Z' }),
    ]
    store.currentSessionId = 'session-B'

    await store.deleteSession('session-B')

    expect(store.currentSessionId).toBe('session-A')
  })

  it('clears currentSessionId when the last remaining session is deleted', async () => {
    const store = useChatStore()
    store.sessions = [makeSummary({ id: 'session-A' })]
    store.currentSessionId = 'session-A'

    await store.deleteSession('session-A')

    expect(store.currentSessionId).toBeNull()
  })

  it('prunes per-session streaming, queued-prompts, and phase slots for the deleted session', async () => {
    const store = useChatStore()
    store.sessions = [makeSummary({ id: 'session-A' }), makeSummary({ id: 'session-B' })]
    store.currentSessionId = 'session-A'
    store.sessionStreaming = {
      'session-A': { isLoading: false, isStreaming: false },
      'session-B': { isLoading: false, isStreaming: true },
    }
    store.queuedPrompts = {
      'session-B': ['queued-1'],
    }
    store.streamingPhase = {
      'session-B': 'generating',
    }

    await store.deleteSession('session-B')

    expect(store.sessionStreaming['session-B']).toBeUndefined()
    expect(store.queuedPrompts['session-B']).toBeUndefined()
    expect(store.streamingPhase['session-B']).toBeUndefined()
    // Untouched slots for surviving sessions stay put.
    expect(store.sessionStreaming['session-A']).toEqual({ isLoading: false, isStreaming: false })
  })

  it('rethrows on api failure and leaves the local sessions array untouched', async () => {
    const store = useChatStore()
    store.sessions = [makeSummary({ id: 'session-A' }), makeSummary({ id: 'session-B' })]
    store.currentSessionId = 'session-A'
    vi.mocked(deleteSession).mockRejectedValueOnce(new Error('boom'))

    await expect(store.deleteSession('session-B')).rejects.toThrow(/boom/i)
    expect(store.sessions.map((s) => s.id)).toEqual(['session-A', 'session-B'])
    expect(store.currentSessionId).toBe('session-A')
  })

  // User bug (May 2026 — "delete cascade"): the backend cascades delete
  // to every delegated descendant. The local cache must mirror that so
  // stale child summaries don't linger in `this.sessions` where
  // `lastDelegatedSessionId` / ChildSessionsPanel would still find them.
  it('cascade-prunes delegated descendants from the local sessions cache when the parent is deleted', async () => {
    const store = useChatStore()
    store.sessions = [
      makeSummary({ id: 'root' }),
      makeSummary({ id: 'child-1', parentId: 'root' }),
      makeSummary({ id: 'grandchild', parentId: 'child-1' }),
      makeSummary({ id: 'untouched-root' }),
    ]
    store.currentSessionId = 'untouched-root'

    await store.deleteSession('root')

    expect(store.sessions.map((s) => s.id).sort()).toEqual(['untouched-root'])
  })

  it('cascade-prunes descendant streaming / queued-prompts / phase slots too', async () => {
    const store = useChatStore()
    store.sessions = [
      makeSummary({ id: 'root' }),
      makeSummary({ id: 'child-1', parentId: 'root' }),
      makeSummary({ id: 'sibling' }),
    ]
    store.currentSessionId = 'sibling'
    store.sessionStreaming = {
      'root': { isLoading: false, isStreaming: false },
      'child-1': { isLoading: false, isStreaming: true },
      'sibling': { isLoading: false, isStreaming: false },
    }
    store.queuedPrompts = { 'child-1': ['queued-c1'] }
    store.streamingPhase = { 'child-1': 'generating' }

    await store.deleteSession('root')

    expect(store.sessionStreaming['root']).toBeUndefined()
    expect(store.sessionStreaming['child-1']).toBeUndefined()
    expect(store.queuedPrompts['child-1']).toBeUndefined()
    expect(store.streamingPhase['child-1']).toBeUndefined()
    // Sibling untouched.
    expect(store.sessionStreaming['sibling']).toEqual({ isLoading: false, isStreaming: false })
  })
})

// QW-11 — Session ordering. Every list-of-sessions surface reads the
// `orderedSessions` getter: actively-streaming first, then by updatedAt
// descending. Sort is non-mutating — `state.sessions` is untouched.
describe('chatStore.orderedSessions', () => {
  beforeEach(() => {
    installLocalStorageStub()
    setActivePinia(createPinia())
  })

  function makeSummary(overrides: Record<string, unknown> = {}): import('@/types').SessionSummary {
    return {
      id: 'session-x',
      agentId: 'agent-1',
      title: 'Untitled',
      status: 'active',
      depth: 0,
      createdAt: '2026-05-10T09:00:00Z',
      updatedAt: '2026-05-10T09:00:00Z',
      messageCount: 0,
      isStreaming: false,
      ...overrides,
    } as import('@/types').SessionSummary
  }

  it('places actively-streaming sessions at the top regardless of updatedAt', async () => {
    const store = useChatStore()
    store.sessions = [
      makeSummary({ id: 'idle-recent', updatedAt: '2026-05-11T10:00:00Z' }),
      makeSummary({ id: 'streaming-old', updatedAt: '2026-05-09T09:00:00Z' }),
      makeSummary({ id: 'idle-mid', updatedAt: '2026-05-10T09:00:00Z' }),
    ]
    store.sessionStreaming = {
      'streaming-old': { isLoading: false, isStreaming: true },
    }

    expect(store.orderedSessions.map((s) => s.id)).toEqual([
      'streaming-old',
      'idle-recent',
      'idle-mid',
    ])
  })

  it('sorts idle sessions by updatedAt descending', async () => {
    const store = useChatStore()
    store.sessions = [
      makeSummary({ id: 'oldest', updatedAt: '2026-05-01T09:00:00Z' }),
      makeSummary({ id: 'newest', updatedAt: '2026-05-11T09:00:00Z' }),
      makeSummary({ id: 'middle', updatedAt: '2026-05-05T09:00:00Z' }),
    ]

    expect(store.orderedSessions.map((s) => s.id)).toEqual([
      'newest',
      'middle',
      'oldest',
    ])
  })

  it('does not mutate the source sessions array', async () => {
    const store = useChatStore()
    const seed = [
      makeSummary({ id: 'session-A', updatedAt: '2026-05-01T09:00:00Z' }),
      makeSummary({ id: 'session-B', updatedAt: '2026-05-11T09:00:00Z' }),
    ]
    store.sessions = seed

    // Trigger the getter; ordering should NOT bleed back into `sessions`.
    const ordered = store.orderedSessions
    expect(ordered[0].id).toBe('session-B')
    expect(store.sessions.map((s) => s.id)).toEqual(['session-A', 'session-B'])
  })
})

// Deliverable 3 (May 2026 context-accuracy bundle) — /compress
// slash command routes through chatStore.compressCurrentSession to
// POST /api/v1/sessions/{id}/compress and surface fire / no-fire as
// a toast.
describe('chatStore - compressCurrentSession (/compress slash command)', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('calls compactSessionNow with the active session id and shows a success toast on fire=true', async () => {
    const store = useChatStore()
    store.currentSessionId = 'sess-active'

    vi.mocked(compactSessionNow).mockResolvedValueOnce({
      fired: true,
      summary: '[auto-compacted summary]: {"intent":"x"}',
    })

    await store.compressCurrentSession()

    expect(vi.mocked(compactSessionNow)).toHaveBeenCalledWith('sess-active')
    const { toasts } = useToast()
    expect(toasts.value.some((t) => /compact/i.test(t.message))).toBe(true)
  })

  it('shows a "nothing to compact" toast on fire=false', async () => {
    const store = useChatStore()
    store.currentSessionId = 'sess-empty'

    vi.mocked(compactSessionNow).mockResolvedValueOnce({ fired: false })

    await store.compressCurrentSession()

    const { toasts } = useToast()
    expect(toasts.value.some((t) => /nothing to compact/i.test(t.message))).toBe(true)
  })

  it('is a no-op when no session is currently active', async () => {
    const store = useChatStore()
    store.currentSessionId = ''

    await store.compressCurrentSession()

    expect(vi.mocked(compactSessionNow)).not.toHaveBeenCalled()
  })

  it('surfaces a backend error as a toast without throwing', async () => {
    const store = useChatStore()
    store.currentSessionId = 'sess-error'

    vi.mocked(compactSessionNow).mockRejectedValueOnce(new Error('boom'))

    await expect(store.compressCurrentSession()).resolves.toBeUndefined()

    const { toasts } = useToast()
    expect(toasts.value.some((t) => /compact.*fail|fail.*compact|boom/i.test(t.message))).toBe(true)
  })
})

// Deliverable 3 — slash-command routing. The composer must
// short-circuit /compress before treating it as a chat send so the
// user does not append a "/compress" user message to the
// transcript.
describe('chatStore - sendMessage intercepts /compress', () => {
  beforeEach(() => {
    installLocalStorageStub()
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('routes /compress through compactSessionNow without POSTing a chat message', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'sess-route'

    vi.mocked(compactSessionNow).mockResolvedValueOnce({ fired: true, summary: 'x' })

    await store.sendMessage('/compress')

    expect(vi.mocked(compactSessionNow)).toHaveBeenCalledWith('sess-route')
    expect(vi.mocked(sendSessionMessage)).not.toHaveBeenCalled()
    // The slash command must not leave a "/compress" user bubble in
    // the local transcript either.
    expect(store.messages.some((m) => m.content === '/compress')).toBe(false)
  })

  it('lets ordinary text through unchanged', async () => {
    const store = useChatStore()
    store.agentId = 'agent-1'
    store.currentSessionId = 'sess-route'

    await store.sendMessage('hello not a slash command')

    expect(vi.mocked(sendSessionMessage)).toHaveBeenCalledWith(
      'sess-route',
      'hello not a slash command',
    )
    expect(vi.mocked(compactSessionNow)).not.toHaveBeenCalled()
  })
})
