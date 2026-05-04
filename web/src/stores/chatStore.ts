import { defineStore } from 'pinia'
import type { Agent, Message, Model, SessionSummary } from '@/types'
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
} from '@/api'
import { useTodoStore } from './todoStore'

const activeSessionStorageKey = 'chat.currentSessionId'
const activeAgentStorageKey = 'chat.agentId'

// 60s fail-safe — if no SSE activity arrives during a send, the store
// assumes the stream is dead and clears isLoading. Without this the
// submit gate stays locked forever after a network hiccup, presenting
// to the user as "the chat is stuck". Reset on every chunk; cancelled
// when the stream cleanly terminates.
const SSE_STALL_TIMEOUT_MS = 60_000

// Module-scoped watchdog handle. Module scope (not Pinia state) keeps it
// out of reactivity tracking; the timer is an implementation detail of
// the streaming lifecycle, never read by the UI.
let stallWatchdog: ReturnType<typeof setTimeout> | null = null

function clearStallWatchdog(): void {
  if (stallWatchdog !== null) {
    clearTimeout(stallWatchdog)
    stallWatchdog = null
  }
}

function getPersistedSessionId(): string | null {
  if (typeof window === 'undefined') {
    return null
  }

  return window.localStorage.getItem(activeSessionStorageKey)
}

function persistSessionId(sessionId: string | null): void {
  if (typeof window === 'undefined') {
    return
  }

  if (sessionId) {
    window.localStorage.setItem(activeSessionStorageKey, sessionId)
    return
  }

  window.localStorage.removeItem(activeSessionStorageKey)
}

function getPersistedAgentId(): string | null {
  if (typeof window === 'undefined') {
    return null
  }

  return window.localStorage.getItem(activeAgentStorageKey)
}

function persistAgentId(agentId: string | null): void {
  if (typeof window === 'undefined') {
    return
  }

  if (agentId) {
    window.localStorage.setItem(activeAgentStorageKey, agentId)
    return
  }

  window.localStorage.removeItem(activeAgentStorageKey)
}


export const useChatStore = defineStore('chat', {
  state: () => ({
    availableAgentDetails: [] as Agent[],
    availableAgents: [] as string[],
    availableModels: [] as Model[],
    agentId: '',
    currentModelId: '',
    currentProviderId: '',
    currentSessionId: null as string | null,
    sessions: [] as SessionSummary[],
    messages: [] as Message[],
    isLoading: false,
    isStreaming: false,
    isLoadingSessions: false,
    error: null as string | null,
    // lastToolName tracks the tool whose result is expected next over the
    // SSE stream. The server emits `tool_call` then `tool_result` as a pair
    // (see internal/api/sse_consumer.go WriteToolCall/WriteToolResult), but
    // tool_result events do not echo the tool name — so we have to remember
    // the most recent tool_call to know whether the upcoming tool_result is
    // a todowrite emission and therefore routable into the todoStore.
    lastToolName: null as string | null,
    // composerText is set by revertToMessage to pre-populate the MessageInput
    // composer with the content of a reverted user message. MessageInput
    // watches this field and consumes it (resetting to '') on next tick.
    composerText: '',
  }),

  getters: {
    // Session hierarchy — these getters back the keyboard navigation layer
    // (Up to parent, Left/Right siblings, Ctrl+X Down to last delegated child)
    // and the toolbar visibility check in ChatView.
    //
    // currentSession: looked up by id from the sessions list. Pure derivation —
    // there is no `loaded session` cache.
    currentSession(state): SessionSummary | undefined {
      if (!state.currentSessionId) return undefined
      return state.sessions.find((s) => s.id === state.currentSessionId)
    },

    // parentSessionId: parent of the active *child* session, or null when the
    // active session has no parentId or no session is active.
    parentSessionId(state): string | null {
      if (!state.currentSessionId) return null
      const current = state.sessions.find((s) => s.id === state.currentSessionId)
      return current?.parentId ?? null
    },

    // siblingSessionIds: ids of all sessions that share the *current* session's
    // parentId, ordered ascending by createdAt. Empty when the active session
    // is a parent (i.e. has no parentId itself). Includes the current session
    // so callers can compute previous/next by index.
    siblingSessionIds(state): string[] {
      if (!state.currentSessionId) return []
      const current = state.sessions.find((s) => s.id === state.currentSessionId)
      if (!current?.parentId) return []
      const parentId = current.parentId
      return [...state.sessions]
        .filter((s) => s.parentId === parentId)
        .sort((a, b) => a.createdAt.localeCompare(b.createdAt))
        .map((s) => s.id)
    },

    // previousSiblingSessionId / nextSiblingSessionId: clamped at the ends —
    // returns null at the first/last sibling, and null when there is only one
    // sibling (so Left/Right do nothing on a single-child page).
    previousSiblingSessionId(): string | null {
      const siblings = (this as unknown as {
        siblingSessionIds: string[]
        currentSessionId: string | null
      }).siblingSessionIds
      const id = (this as unknown as { currentSessionId: string | null }).currentSessionId
      if (!id || siblings.length < 2) return null
      const idx = siblings.indexOf(id)
      if (idx <= 0) return null
      return siblings[idx - 1]
    },

    nextSiblingSessionId(): string | null {
      const siblings = (this as unknown as {
        siblingSessionIds: string[]
        currentSessionId: string | null
      }).siblingSessionIds
      const id = (this as unknown as { currentSessionId: string | null }).currentSessionId
      if (!id || siblings.length < 2) return null
      const idx = siblings.indexOf(id)
      if (idx < 0 || idx >= siblings.length - 1) return null
      return siblings[idx + 1]
    },

    // lastDelegatedSessionId: most-recent child of the active session by
    // createdAt. Used by the Ctrl+X Down chord. Returns null when the active
    // session has no children, or when no session is active.
    lastDelegatedSessionId(state): string | null {
      if (!state.currentSessionId) return null
      const children = state.sessions.filter((s) => s.parentId === state.currentSessionId)
      if (children.length === 0) return null
      const sorted = [...children].sort((a, b) => b.createdAt.localeCompare(a.createdAt))
      return sorted[0].id
    },
  },

  actions: {
    async restoreStateFromBackend(): Promise<void> {
      await this.loadAgents()
      await this.loadSessions()

      const persistedAgentId = getPersistedAgentId()
      const persistedSessionId = getPersistedSessionId()
      const session = this.sessions.find((item) => item.id === persistedSessionId)
      const sessionAgentId = session?.currentAgentId ?? session?.agentId
      const agentId = sessionAgentId ?? persistedAgentId ?? this.availableAgents[0] ?? ''

      this.agentId = agentId
      persistAgentId(agentId || null)

      if (!session || sessionAgentId !== agentId) {
        const sessionForAgent = this.sessions.find(
          (item) => (item.currentAgentId ?? item.agentId) === agentId,
        )

        if (!sessionForAgent) {
          this.currentSessionId = null
          this.messages = []
          this.currentModelId = ''
          this.currentProviderId = ''
          persistSessionId(null)
          // Clear the todoStore's active session — there's nothing to show.
          useTodoStore().setCurrentSession(null)
          return
        }

        this.currentSessionId = sessionForAgent.id
        this.currentModelId = sessionForAgent.currentModelId ?? ''
        this.currentProviderId = sessionForAgent.currentProviderId ?? ''
        persistSessionId(sessionForAgent.id)
        const loadedForAgent = await fetchSessionMessages(sessionForAgent.id)
        this.messages = loadedForAgent.map((m) =>
          m.role === 'assistant' && !m.status ? { ...m, status: 'completed' } : m,
        )
        const todoStore = useTodoStore()
        todoStore.setCurrentSession(sessionForAgent.id)
        todoStore.hydrateFromMessages(sessionForAgent.id, this.messages)
        this.maybeReattachStream(sessionForAgent.id)
        return
      }

      this.currentSessionId = session.id
      this.currentModelId = session.currentModelId ?? ''
      this.currentProviderId = session.currentProviderId ?? ''
      persistSessionId(session.id)
      const loadedForSession = await fetchSessionMessages(session.id)
      this.messages = loadedForSession.map((m) =>
        m.role === 'assistant' && !m.status ? { ...m, status: 'completed' } : m,
      )
      const todoStore = useTodoStore()
      todoStore.setCurrentSession(session.id)
      todoStore.hydrateFromMessages(session.id, this.messages)
      this.maybeReattachStream(session.id)
    },

    // Re-attach a live SSE consumer when restored history shows an
    // in-flight assistant (status === 'running'). Pre-fix the user could
    // reload mid-stream and the frontend would never reconnect — every
    // chunk produced after the reload was dropped silently and the chat
    // looked frozen. This bridges that gap: if the backend was still
    // streaming when the reload happened, the consumer attaches and
    // chunks land on the in-flight bubble; if the backend has already
    // finished the EventSource closes cleanly without ever firing.
    //
    // isLoading is set to true so the submit gate keeps blocking new
    // sends until [DONE] (or the watchdog) clears it — preventing a
    // racy interleave where the user fires another prompt before the
    // resumed stream settles.
    maybeReattachStream(sessionId: string): void {
      const lastAssistant = [...this.messages].reverse().find((m) => m.role === 'assistant')
      if (!lastAssistant || lastAssistant.status !== 'running') {
        return
      }

      this.isLoading = true
      this.isStreaming = true
      this.armStallWatchdog()

      const eventSource = subscribeSessionStream(sessionId)
      const close = (): void => {
        eventSource.close()
        clearStallWatchdog()
        this.isLoading = false
        this.isStreaming = false
      }

      eventSource.addEventListener('message', (event) => {
        const payload = (event as MessageEvent).data as string
        this.applyContentEvent(payload)
        if (payload === '[DONE]') {
          close()
        }
      })
      eventSource.addEventListener('error', () => {
        // Backend closed or proxy timed out — stop pretending we're still
        // streaming so the input gate unsticks. The user can fire a new
        // prompt to resume the conversation.
        close()
      })
    },

    async loadAgents(): Promise<void> {
      const agents = await fetchAgents()
      this.availableAgentDetails = agents
      this.availableAgents = agents.map((agent) => agent.id)

      if (!this.agentId && agents.length > 0) {
        await this.setAgent(agents[0].id)
        return
      }

      if (this.agentId && !this.availableAgents.includes(this.agentId) && agents.length > 0) {
        await this.setAgent(agents[0].id)
      }
    },

    async setAgent(agentId: string): Promise<void> {
      const previousAgentId = this.agentId
      this.agentId = agentId
      persistAgentId(agentId)

      if (!agentId || !this.currentSessionId || agentId === previousAgentId) {
        return
      }

      try {
        await updateSessionAgent(this.currentSessionId, agentId)
      } catch (error) {
        this.error = error instanceof Error ? error.message : 'Failed to update session agent'
      }
    },

    async setModel(modelId: string, providerId: string): Promise<void> {
      const previousModelId = this.currentModelId
      const previousProviderId = this.currentProviderId
      this.currentModelId = modelId
      this.currentProviderId = providerId

      if (!this.currentSessionId) {
        return
      }

      if (modelId === previousModelId && providerId === previousProviderId) {
        return
      }

      try {
        await updateSessionModel(this.currentSessionId, modelId, providerId)
      } catch (error) {
        this.error = error instanceof Error ? error.message : 'Failed to update session model'
      }
    },

    async loadModels(): Promise<void> {
      this.availableModels = await fetchModels()
    },

    getSelectedAgent(): Agent | undefined {
      return this.availableAgentDetails.find((agent) => agent.id === this.agentId)
    },

    clearMessages(): void {
      this.messages = []
      this.error = null
    },

    async loadSessions(): Promise<void> {
      this.isLoadingSessions = true
      try {
        this.sessions = await fetchSessions()
      } finally {
        this.isLoadingSessions = false
      }
    },

    async newSession(): Promise<void> {
      const session = await createSession(this.agentId)
      this.currentSessionId = session.id
      persistSessionId(session.id)
      // A new session has no history yet, so the todoStore slice should be
      // empty for the panel to render the "No todos in this session yet"
      // empty state until the agent emits its first todowrite.
      const todoStore = useTodoStore()
      todoStore.setCurrentSession(session.id)
      todoStore.hydrateFromMessages(session.id, [])
    },

    async loadSessionMessages(sessionId: string): Promise<void> {
      this.isLoading = true
      this.error = null
      try {
        const session = this.sessions.find((item) => item.id === sessionId)
        const sessionAgentId = session?.currentAgentId ?? session?.agentId

        // Switch the active session id BEFORE delegating to setAgent.
        // setAgent reads currentSessionId to decide which session to PATCH;
        // running it before this assignment caused the previously-active
        // session's agent to be updated instead of the one just selected,
        // leaving the UI and backend out of sync after every switch.
        this.currentSessionId = sessionId
        persistSessionId(sessionId)

        if (sessionAgentId && sessionAgentId !== this.agentId) {
          await this.setAgent(sessionAgentId)
        }

        if (session) {
          this.currentModelId = session.currentModelId ?? ''
          this.currentProviderId = session.currentProviderId ?? ''
        }

        const loaded = await fetchSessionMessages(sessionId)
        // Seal all backend-loaded assistant messages as 'completed' so
        // they can never be confused with an in-flight streaming target.
        // Backend history has no notion of a 'running' state; leaving
        // status === undefined allows handleContentChunk to wrongly adopt
        // a prior turn's assistant as the chunk target on the next send.
        this.messages = loaded.map((m) =>
          m.role === 'assistant' && !m.status ? { ...m, status: 'completed' } : m,
        )

        // Sync the todoStore: switch its active session and rebuild the
        // slice from the freshly-loaded history. The latest todowrite
        // tool_result message is the canonical state — see todoStore
        // hydrateFromMessages for the derivation rule.
        const todoStore = useTodoStore()
        todoStore.setCurrentSession(sessionId)
        todoStore.hydrateFromMessages(sessionId, this.messages)
      } finally {
        this.isLoading = false
      }
    },

    async loadSessionByAgentId(agentId: string): Promise<boolean> {
      const session = this.sessions.find(
        (s) => (s.currentAgentId ?? s.agentId) === agentId,
      )
      if (!session) return false

      await this.loadSessionMessages(session.id)
      return true
    },

    async sendMessage(content: string): Promise<void> {
      const text = content.trim()
      if (!text) {
        return
      }
      // Pre-fix this branch silently early-returned when isLoading was true.
      // Combined with a stuck stream (no [DONE] from the backend), the user
      // saw the chat appear frozen with no surfacing of any kind. The gate
      // now sets this.error so the existing chat-error footer renders the
      // rejection. The MessageInput component additionally surfaces a toast
      // — the two surface independently because non-input call sites
      // (e.g. programmatic resends) still need a visible signal.
      if (this.isLoading) {
        this.error = 'An earlier message is still in flight. Wait for it to finish or reload the page.'
        return
      }

      this.error = null
      this.isLoading = true
      this.isStreaming = false
      this.armStallWatchdog()

      const optimisticMessage: Message = {
        id: `temp-${Date.now()}`,
        role: 'user',
        content: text,
        timestamp: new Date().toISOString(),
      }
      this.messages.push(optimisticMessage)

      let eventSource: EventSource | null = null
      try {
        let sessionId = this.currentSessionId
        if (!sessionId) {
          const session = await createSession(this.agentId)
          sessionId = session.id
          this.currentSessionId = sessionId
          persistSessionId(sessionId)
        }

        eventSource = subscribeSessionStream(sessionId)
        eventSource.addEventListener('message', (event) => {
          this.applyContentEvent((event as MessageEvent).data as string)
        })

        await sendSessionMessage(sessionId, text)

        // Do NOT reload messages from the backend here. The SSE stream
        // delivers the assistant response in real-time; replacing
        // this.messages mid-stream causes the previous backend-loaded
        // assistant (status === undefined) to appear above the new user
        // prompt, producing the response-replay bug. Trust the stream.
        await this.loadSessions()
      } catch (error) {
        this.error = error instanceof Error ? error.message : 'Failed to send message'
      } finally {
        if (eventSource) {
          eventSource.close()
        }
        clearStallWatchdog()
        this.isLoading = false
        this.isStreaming = false
      }
    },

    // Re-arm the watchdog whenever there is fresh streaming activity.
    // Called from sendMessage on send-start, from applyContentEvent on
    // every chunk, and from maybeReattachStream on reconnect. The 60s
    // window is intentionally generous — agents can sit thinking on a
    // slow tool call without producing chunks; we only want to trip on
    // "actually dead" streams, not "agent is busy".
    armStallWatchdog(): void {
      clearStallWatchdog()
      stallWatchdog = setTimeout(() => {
        // Stream stalled — unsticky the input gate so the user can
        // recover without reloading the page. The error footer surfaces
        // the cause; if no chunks arrived at all the in-flight assistant
        // bubble (if any) stays in-place but is no longer locked.
        this.error = 'Response stalled — the stream produced no activity for 60 seconds. You can send another message.'
        this.isLoading = false
        this.isStreaming = false
        stallWatchdog = null
      }, SSE_STALL_TIMEOUT_MS)
    },

    applyDelegationEvent(payload: string): void {
      let info: {
        chain_id?: string
        target_agent?: string
        tool_calls?: number
        last_tool?: string
        status?: string
      }
      try {
        info = JSON.parse(payload)
      } catch {
        return
      }

      // Prefer matching by chain_id or target_agent — those identify a
      // specific in-flight delegation. Fall back to the in-flight
      // streaming assistant (status === 'running'), NOT any non-completed
      // assistant — backend-loaded messages have status === undefined and
      // would otherwise spuriously absorb a later turn's delegation
      // metadata. See bug-fix note "Session message upsert collision".
      const target =
        this.messages.find(
          (message) =>
            message.status !== 'completed' &&
            info.chain_id !== undefined &&
            message.chainId === info.chain_id,
        ) ??
        this.messages.find(
          (message) =>
            message.status !== 'completed' &&
            info.target_agent !== undefined &&
            message.targetAgent === info.target_agent,
        ) ??
        this.messages.find((message) => message.status === 'running' && message.role === 'assistant')

      if (!target) {
        return
      }

      if (info.target_agent !== undefined) {
        target.targetAgent = info.target_agent
      }
      if (info.chain_id !== undefined) {
        target.chainId = info.chain_id
      }
      if (info.tool_calls !== undefined) {
        target.toolCalls = info.tool_calls
      }
      if (info.last_tool !== undefined) {
        target.lastTool = info.last_tool
      }
      if (info.status !== undefined) {
        target.status = info.status
      }
    },

    applyContentEvent(payload: string): void {
      // Any SSE event counts as "the stream is alive" — re-arm the
      // watchdog so a slow but progressing stream is never killed.
      // The watchdog only trips on dead streams.
      this.armStallWatchdog()

      if (payload === '[DONE]') {
        // Seal any in-flight assistant message so a later turn's chunks
        // cannot land on it. Without this, chatStore treats backend-loaded
        // assistant rows (status === undefined) as valid streaming targets
        // and turn N+1's chunks overwrite turn N's response in-place. See
        // bug-fix note "Session message upsert collision" — pre-fix the
        // user observed the previous response mutating into the new one.
        const inFlight = [...this.messages].reverse().find(
          (message) => message.role === 'assistant' && message.status === 'running',
        )
        if (inFlight) {
          inFlight.status = 'completed'
        }
        this.isStreaming = false
        // Stream is done — cancel the watchdog. isLoading is cleared by
        // the sendMessage finally block (or by maybeReattachStream's
        // close handler if this came from a reconnected stream).
        clearStallWatchdog()
        return
      }

      let info: Record<string, unknown>
      try {
        info = JSON.parse(payload)
      } catch {
        return
      }

      if (info.type === 'tool_call') {
        this.handleToolCallEvent(info)
        return
      }

      if (info.type === 'tool_result') {
        this.handleToolResultEvent(info)
        return
      }

      if (info.type === 'skill_load') {
        this.handleToolCallEvent({ ...info, name: info.name, status: 'running' })
        return
      }

      if (
        info.target_agent !== undefined ||
        info.chain_id !== undefined ||
        info.tool_calls !== undefined ||
        info.last_tool !== undefined
      ) {
        this.applyDelegationEvent(payload)
        return
      }

      if (info.content !== undefined) {
        this.handleContentChunk(info)
        return
      }

      if (info.error !== undefined) {
        this.error = String(info.error)
      }
    },

    handleContentChunk(info: { content?: unknown }): void {
      if (typeof info.content !== 'string' || info.content.length === 0) {
        return
      }

      // Only an assistant message currently being streamed is a valid
      // target. The previous condition `status !== 'completed'` admitted
      // backend-loaded rows (status === undefined) and caused turn N+1's
      // chunks to land on turn N's message. The contract is now: a
      // chunk-stream target MUST have been created by this store with
      // status === 'running'. Backend-canonical history can never be a
      // target. See bug-fix note "Session message upsert collision".
      let target = [...this.messages].reverse().find(
        (message) => message.role === 'assistant' && message.status === 'running',
      )

      if (!target) {
        target = {
          id: `streaming-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
          role: 'assistant',
          content: '',
          timestamp: new Date().toISOString(),
          status: 'running',
        }
        this.messages.push(target)
      }

      target.content = (target.content ?? '') + info.content
      target.status = 'running'
      this.isStreaming = true
    },

    handleToolCallEvent(info: { name?: unknown; status?: unknown; type?: unknown }): void {
      const toolName = String(info.name ?? info.type ?? 'unknown')
      const status = String(info.status ?? 'running')
      // Remember the tool name so the next tool_result event can be routed
      // appropriately — the SSE tool_result payload only carries content,
      // not the tool name. This is the seam the todowrite ingestion hooks
      // into below.
      this.lastToolName = toolName

      const toolMessage: Message = {
        id: `tool-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
        role: 'tool_result',
        toolName,
        content: '',
        timestamp: new Date().toISOString(),
        status,
      }

      this.messages.push(toolMessage)
    },

    // revertToMessage truncates the session at the given user message, removes
    // it and all subsequent messages from the local store, and pre-populates
    // the composer with the reverted message's content so the user can edit
    // and re-send without manual copy/paste.
    //
    // Expected:
    //   - messageId identifies a message whose role === 'user'.
    //   - currentSessionId is set.
    //
    // Side effects:
    //   - Calls DELETE /api/v1/sessions/{id}/messages/from/{messageId}.
    //   - Slices this.messages at the revert index.
    //   - Sets this.composerText to the reverted message's content.
    //   - Clears any in-flight loading state.
    async revertToMessage(messageId: string): Promise<void> {
      const idx = this.messages.findIndex((m) => m.id === messageId)
      if (idx < 0 || !this.currentSessionId) {
        return
      }
      const content = this.messages[idx].content
      await truncateSessionMessages(this.currentSessionId, messageId)
      this.messages = this.messages.slice(0, idx)
      this.composerText = content
      this.isLoading = false
    },

    handleToolResultEvent(info: { content?: unknown }): void {
      const target = [...this.messages].reverse().find(
        (message) => message.role === 'tool_result' && message.status === 'running',
      )

      const content = String(info.content ?? '')

      if (target) {
        target.content = content
        target.status = 'completed'
      }

      // Route todowrite results into the todoStore. The agent emits the full
      // todo array on every todowrite call, so the slice for the active
      // session is replaced rather than merged — matching the TUI which
      // re-renders the full list on every todo_update message.
      if (this.lastToolName === 'todowrite' && this.currentSessionId) {
        const todoStore = useTodoStore()
        todoStore.ingestToolResult(this.currentSessionId, content)
      }
      // Clear the gate so a stray subsequent tool_result doesn't double-route.
      this.lastToolName = null
    },
  },
})
