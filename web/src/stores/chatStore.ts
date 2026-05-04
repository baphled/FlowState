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
  updateSessionAgent,
  updateSessionModel,
} from '@/api'
import { useTodoStore } from './todoStore'

const activeSessionStorageKey = 'chat.currentSessionId'
const activeAgentStorageKey = 'chat.agentId'

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
        this.messages = await fetchSessionMessages(sessionForAgent.id)
        const todoStore = useTodoStore()
        todoStore.setCurrentSession(sessionForAgent.id)
        todoStore.hydrateFromMessages(sessionForAgent.id, this.messages)
        return
      }

      this.currentSessionId = session.id
      this.currentModelId = session.currentModelId ?? ''
      this.currentProviderId = session.currentProviderId ?? ''
      persistSessionId(session.id)
      this.messages = await fetchSessionMessages(session.id)
      const todoStore = useTodoStore()
      todoStore.setCurrentSession(session.id)
      todoStore.hydrateFromMessages(session.id, this.messages)
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

        this.messages = await fetchSessionMessages(sessionId)

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
      if (!text || this.isLoading) {
        return
      }

      this.error = null
      this.isLoading = true
      this.isStreaming = false

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

        this.messages = await fetchSessionMessages(sessionId)
        const seen = new Set<string>()
        this.messages = this.messages.filter((message) => {
          if (seen.has(message.id)) return false
          seen.add(message.id)
          return true
        })
        await this.loadSessions()
      } catch (error) {
        this.error = error instanceof Error ? error.message : 'Failed to send message'
      } finally {
        if (eventSource) {
          eventSource.close()
        }
        this.isLoading = false
        this.isStreaming = false
      }
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
