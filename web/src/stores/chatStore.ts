import { defineStore } from 'pinia'
import type { Agent, Message, Model, SessionSummary } from '@/types'
import {
  createSession,
  fetchAgents,
  fetchModels,
  fetchSessionMessages,
  fetchSessions,
  sendSessionMessage,
  truncateSessionMessages,
  updateSessionAgent,
  updateSessionModel,
} from '@/api'
import { useSessionStream, type SessionStream } from '@/composables/useSessionStream'
import { recordStreamEvent } from '@/lib/streamLog'
import { exhaustivenessGuard, parseSSEPayload, type SSEEvent } from '@/lib/sseEvent'
import { useTodoStore } from './todoStore'

const activeSessionStorageKey = 'chat.currentSessionId'
const activeAgentStorageKey = 'chat.agentId'
const activeModelStorageKey = 'chat.selectedModel'
const activeProviderStorageKey = 'chat.selectedProvider'

// team-lead is the lead orchestrator — it can delegate to any agent or swarm
// and is the correct starting point for open-ended requests.
const DEFAULT_AGENT_ID = 'team-lead'

// Module-instantiated streaming lifecycle. The composable owns the EventSource
// and stall watchdog handles internally; the store treats it as an opaque
// dependency. Single-instance preserves the pre-extraction "one in-flight SSE
// per page" invariant — concurrent connect calls still tear down the prior
// connection. Per-test isolation continues to work via setActivePinia +
// FakeEventSource.instances reset (the composable consumes the same global
// EventSource constructor that the FakeEventSource mock swaps in).
const sessionStream: SessionStream = useSessionStream()

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

function getPersistedModelId(): string | null {
  if (typeof window === 'undefined') {
    return null
  }

  return window.localStorage.getItem(activeModelStorageKey)
}

function persistModelId(modelId: string | null): void {
  if (typeof window === 'undefined') {
    return
  }

  if (modelId) {
    window.localStorage.setItem(activeModelStorageKey, modelId)
    return
  }

  window.localStorage.removeItem(activeModelStorageKey)
}

function getPersistedProviderId(): string | null {
  if (typeof window === 'undefined') {
    return null
  }

  return window.localStorage.getItem(activeProviderStorageKey)
}

function persistProviderId(providerId: string | null): void {
  if (typeof window === 'undefined') {
    return
  }

  if (providerId) {
    window.localStorage.setItem(activeProviderStorageKey, providerId)
    return
  }

  window.localStorage.removeItem(activeProviderStorageKey)
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
      await this.loadModels()

      const persistedAgentId = getPersistedAgentId()
      const persistedSessionId = getPersistedSessionId()
      const session = this.sessions.find((item) => item.id === persistedSessionId)
      const sessionAgentId = session?.currentAgentId ?? session?.agentId
      const defaultAgent = this.availableAgents.includes(DEFAULT_AGENT_ID)
        ? DEFAULT_AGENT_ID
        : (this.availableAgents[0] ?? '')
      const agentId = sessionAgentId ?? persistedAgentId ?? defaultAgent

      this.agentId = agentId
      persistAgentId(agentId || null)

      if (!session || sessionAgentId !== agentId) {
        const sessionForAgent = this.sessions.find(
          (item) => (item.currentAgentId ?? item.agentId) === agentId,
        )

        if (!sessionForAgent) {
          this.currentSessionId = null
          this.messages = []
          // Restore model/provider from localStorage when there is no session
          // to derive them from. Validate the stored model still exists in the
          // available models list; fall back to empty string if it has been
          // removed so the picker shows its "Select model" placeholder.
          const persistedModelId = getPersistedModelId()
          const persistedProviderId = getPersistedProviderId()
          const modelIsAvailable =
            !!persistedModelId &&
            this.availableModels.some(
              (m) => m.id === persistedModelId && m.providerId === persistedProviderId,
            )
          this.currentModelId = modelIsAvailable ? persistedModelId! : ''
          this.currentProviderId = modelIsAvailable ? (persistedProviderId ?? '') : ''
          persistSessionId(null)
          // Clear the todoStore's active session — there's nothing to show.
          useTodoStore().setCurrentSession(null)
          return
        }

        this.currentSessionId = sessionForAgent.id
        // Prefer the session's own model; fall back to a validated localStorage
        // value when the session has never had a model set.
        {
          const sessionModelId = sessionForAgent.currentModelId ?? ''
          const sessionProviderId = sessionForAgent.currentProviderId ?? ''
          if (sessionModelId) {
            this.currentModelId = sessionModelId
            this.currentProviderId = sessionProviderId
          } else {
            const persistedModelId = getPersistedModelId()
            const persistedProviderId = getPersistedProviderId()
            const modelIsAvailable =
              !!persistedModelId &&
              this.availableModels.some(
                (m) => m.id === persistedModelId && m.providerId === persistedProviderId,
              )
            this.currentModelId = modelIsAvailable ? persistedModelId! : ''
            this.currentProviderId = modelIsAvailable ? (persistedProviderId ?? '') : ''
          }
        }
        persistSessionId(sessionForAgent.id)
        const loadedForAgent = await fetchSessionMessages(sessionForAgent.id)
        this.messages = loadedForAgent.map((m) =>
          m.role === 'assistant' && !m.status ? { ...m, status: 'completed' } : m,
        )
        const todoStore = useTodoStore()
        todoStore.setCurrentSession(sessionForAgent.id)
        todoStore.hydrateFromMessages(sessionForAgent.id, this.messages)
        this.maybeReattachStream(sessionForAgent.id, sessionForAgent.isStreaming ?? false)
        return
      }

      this.currentSessionId = session.id
      // Prefer the session's own model; fall back to a validated localStorage
      // value when the session has never had a model set.
      {
        const sessionModelId = session.currentModelId ?? ''
        const sessionProviderId = session.currentProviderId ?? ''
        if (sessionModelId) {
          this.currentModelId = sessionModelId
          this.currentProviderId = sessionProviderId
        } else {
          const persistedModelId = getPersistedModelId()
          const persistedProviderId = getPersistedProviderId()
          const modelIsAvailable =
            !!persistedModelId &&
            this.availableModels.some(
              (m) => m.id === persistedModelId && m.providerId === persistedProviderId,
            )
          this.currentModelId = modelIsAvailable ? persistedModelId! : ''
          this.currentProviderId = modelIsAvailable ? (persistedProviderId ?? '') : ''
        }
      }
      persistSessionId(session.id)
      const loadedForSession = await fetchSessionMessages(session.id)
      this.messages = loadedForSession.map((m) =>
        m.role === 'assistant' && !m.status ? { ...m, status: 'completed' } : m,
      )
      const todoStore = useTodoStore()
      todoStore.setCurrentSession(session.id)
      todoStore.hydrateFromMessages(session.id, this.messages)
      this.maybeReattachStream(session.id, session.isStreaming ?? false)
    },

    // Re-attach a live SSE consumer when restored history shows the session
    // was in-flight at reload time. Pre-fix the user could reload mid-stream
    // and the frontend would never reconnect — every chunk produced after the
    // reload was dropped silently and the chat looked frozen. This bridges
    // that gap: if the backend was still streaming when the reload happened,
    // the consumer attaches and chunks arrive at the UI; if the backend has
    // already finished the EventSource closes cleanly without ever firing.
    //
    // Detection: two complementary signals are checked in order:
    //   1. backendStreaming (from session summary isStreaming field) — the
    //      broker reports an active publish; reconnect regardless of message
    //      state. Covers the gap where the backend is streaming but the last
    //      persisted message is an assistant entry with no 'running' status
    //      (e.g. a partial response written mid-stream by the accumulator).
    //   2. Message heuristic — last message is the user turn, or is an
    //      assistant with status 'running'. Covers cases where the session
    //      summary was fetched without an isStreaming flag (e.g. legacy API).
    //
    // In all cases, the consumer subscribes; if the backend already finished
    // (fast-path [DONE] from handleSessionStream), the EventSource closes
    // cleanly and the fallback fetch fills in the completed response.
    //
    // isLoading is set to true so the submit gate keeps blocking new sends
    // until [DONE] (or the watchdog) clears it.
    maybeReattachStream(sessionId: string, backendStreaming = false): void {
      if (!sessionId) return

      // Prefer the authoritative backend signal: if the broker reports an
      // active publish, subscribe unconditionally.
      if (!backendStreaming) {
        if (!this.messages.length) return
        const lastMessage = this.messages[this.messages.length - 1]
        const needsReattach =
          lastMessage.role === 'user' ||
          (lastMessage.role === 'assistant' && lastMessage.status === 'running')
        if (!needsReattach) return
      }

      this.isLoading = true
      this.isStreaming = true

      const close = (): void => {
        sessionStream.disconnect()
        this.isLoading = false
        this.isStreaming = false

        // Reconcile unconditionally — the pre-fix `lastMsg?.role === 'user'`
        // gate dropped the more common case where chunks had arrived but the
        // backend had follow-up state SSE didn't surface before close (a
        // sealed assistant content, a tool_result, a delegation completion).
        // reconcileFromBackend re-checks currentSessionId before and after
        // its await so a session switch concurrent with this call is safe.
        void this.reconcileFromBackend(sessionId)
      }

      // connect tears down any prior SSE, opens a new one, and arms the stall
      // watchdog. The watchdog onTrip handler is the same store action used
      // for sendMessage so user-visible recovery behaviour is identical.
      // sessionId is captured in every callback closure so a mid-stream
      // session switch never lands chunks on the wrong session.
      // (Compounding bugs C-3, C-6 from the PR-2 plan.)
      const capturedSessionId = sessionId
      sessionStream.connect(capturedSessionId, {
        onMessage: (payload) => {
          // C-3: discard chunks if the user navigated away while this
          // stream was still alive.
          if (this.currentSessionId !== capturedSessionId) return
          this.applyContentEvent(payload)
          if (payload === '[DONE]') {
            close()
          }
        },
        // Backend closed or proxy timed out — stop pretending we're still
        // streaming so the input gate unsticks. The user can fire a new
        // prompt to resume the conversation.
        onError: () => {
          close()
        },
        onStall: () => this.handleStreamStall(capturedSessionId),
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
      persistModelId(modelId || null)
      persistProviderId(providerId || null)

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
      // Snapshot the prior streaming flag for the active session BEFORE the
      // refetch so we can detect a was-streaming → not-streaming transition.
      // The transition is the signal that a child agent (or the active
      // session itself) just finished and we should reconcile so the
      // user-visible bubble updates without a manual refresh. Compounding
      // bug C-4 from the PR-2 plan.
      const activeId = this.currentSessionId
      // SessionSummary.isStreaming is required on the wire (no omitempty in
      // session_response.go). The boolean read is safe; ?? false defends
      // against the session having been removed between renders.
      const wasStreaming =
        activeId !== null
          ? (this.sessions.find((s) => s.id === activeId)?.isStreaming ?? false)
          : false
      try {
        this.sessions = await fetchSessions()
      } finally {
        this.isLoadingSessions = false
      }
      if (activeId !== null && wasStreaming) {
        const nowStreaming =
          this.sessions.find((s) => s.id === activeId)?.isStreaming ?? false
        if (!nowStreaming) {
          // Fire-and-forget: reconcileFromBackend re-checks currentSessionId
          // before and after its await, so a session switch concurrent with
          // this background reconcile is safe.
          void this.reconcileFromBackend(activeId)
        }
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
      // Close any in-progress SSE from a prior session. Without this, the
      // stale SSE's close() callback can fire after the session switch and
      // overwrite the new session's messages with the old session's content.
      sessionStream.disconnect()
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
        // Compounding bug C-7: switching to an idle session while
        // isStreaming was true (left over from a prior session's SSE) leaves
        // the activity indicator pulsing on a session that has nothing in
        // flight. Clear both flags here.
        this.isStreaming = false
      }
    },

    // reconcileFromBackend re-fetches the canonical session history and
    // merges it into local state. It is the post-stream-end recovery
    // primitive that replaces the pre-fix `if lastMsg?.role === 'user'`
    // gated refetch — that gate dropped the more common case where chunks
    // had arrived but the backend had follow-up state (a tool_result, a
    // delegation completion, a sealed assistant) that SSE didn't surface
    // before the close.
    //
    // Contract:
    //   - Idempotent. Safe to call any number of times.
    //   - Re-checks currentSessionId BEFORE the call (no-op for stale
    //     session ids) and AFTER the await (discards the result if the
    //     user navigated during the network round-trip).
    //   - Merge semantics, not replace:
    //       * backend canonical history is the base, with assistant rows
    //         sealed to status='completed' (matching the seal rule used in
    //         restoreStateFromBackend at line 290 and loadSessionMessages
    //         at line 539).
    //       * any local 'temp-*' optimistic user message that the backend
    //         response does not yet contain is preserved and appended,
    //         so a reconcile that races with a still-pending POST does not
    //         visually swallow the user's just-sent bubble.
    //   - Catches fetch failures silently. The watchdog/error path surfaces
    //     user-facing messages — reconcile is best-effort recovery and must
    //     not poison the UI on a transient network blip.
    async reconcileFromBackend(sessionId: string): Promise<void> {
      // Pre-await guard: caller may pass a stale sessionId (e.g. fired from
      // a watchdog whose session the user has since navigated away from).
      if (this.currentSessionId !== sessionId) return
      recordStreamEvent({ kind: 'reconcile-call', sessionId })
      let loaded
      try {
        loaded = await fetchSessionMessages(sessionId)
      } catch {
        // Silent — see contract docstring above. The watchdog/error path
        // already informs the user something went wrong; double-surfacing
        // would just be noise.
        return
      }
      // Post-await guard: the user may have navigated away while we were
      // waiting on the network. Landing this result on a different session
      // would corrupt that session's view.
      if (this.currentSessionId !== sessionId) return

      // Seal backend-loaded assistant rows to 'completed' so they cannot be
      // confused with an in-flight streaming target by a subsequent chunk.
      // Mirrors the seal rule used at lines 290 and 539.
      const sealedBackend: Message[] = loaded.map((m) =>
        m.role === 'assistant' && !m.status ? { ...m, status: 'completed' } : m,
      )

      // Preserve any 'temp-*' optimistic user message the backend response
      // does not yet have. Compounding bug C-5: the pre-fix wholesale
      // replace dropped the in-flight bubble whenever a reconcile raced
      // ahead of the POST settling. Match by id only — content equality is
      // not safe (the user could send the same content twice) and the
      // backend never reuses a 'temp-*' id.
      const backendIds = new Set(sealedBackend.map((m) => m.id))
      const optimisticOrphans = this.messages.filter(
        (m) => m.id.startsWith('temp-') && !backendIds.has(m.id),
      )

      this.messages = [...sealedBackend, ...optimisticOrphans]
      recordStreamEvent({
        kind: 'reconcile-result',
        sessionId,
        messageCount: this.messages.length,
      })
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

      // Optimistic id is `temp-${Date.now()}-${rand}` rather than just
      // `temp-${Date.now()}` so concurrent sends within the same millisecond
      // (test harness, fast click) get distinct ids — otherwise the
      // reconcile-by-id swap below would collide.
      const optimisticMessage: Message = {
        id: `temp-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
        role: 'user',
        content: text,
        timestamp: new Date().toISOString(),
      }
      this.messages.push(optimisticMessage)

      try {
        let sessionId = this.currentSessionId
        if (!sessionId) {
          const session = await createSession(this.agentId)
          sessionId = session.id
          this.currentSessionId = sessionId
          persistSessionId(sessionId)
        }

        // connect tears down any prior SSE, opens a new one, and arms the
        // stall watchdog so a stuck stream cannot leave isLoading locked.
        // The sessionId is captured in every callback closure so a
        // mid-stream session switch never lands chunks on the wrong session
        // and never reconciles against the wrong session's history.
        // (Compounding bugs C-3, C-6 from the PR-2 plan.)
        const capturedSessionId = sessionId
        sessionStream.connect(capturedSessionId, {
          onMessage: (payload) => {
            // C-3: discard chunks if the user navigated away while this
            // stream was still alive — they belong to capturedSessionId,
            // not the now-active session.
            if (this.currentSessionId !== capturedSessionId) return
            this.applyContentEvent(payload)
            if (payload === '[DONE]') {
              // Close immediately on stream end so the browser cannot
              // auto-reconnect and register a second broker subscriber
              // before the finally block runs.
              sessionStream.disconnect()
              // Reconcile so any backend state SSE didn't surface (a
              // tool_result, a delegation completion, a sealed assistant
              // content) is visible without the user reloading.
              void this.reconcileFromBackend(capturedSessionId)
            }
          },
          onError: () => {
            // SSE connection dropped (stream ended or network error) —
            // close immediately to prevent auto-reconnect registering a
            // duplicate broker subscriber on the next send.
            sessionStream.disconnect()
            // Reconcile in case the backend did finish despite the SSE
            // drop. Silent on fetch failure — see reconcileFromBackend
            // contract.
            void this.reconcileFromBackend(capturedSessionId)
          },
          onStall: () => this.handleStreamStall(capturedSessionId),
        })

        const sentSession = await sendSessionMessage(sessionId, text)

        // Reconcile the optimistic temp-* id with the server-assigned id
        // from the response so subsequent renders carry the canonical id
        // (compounding bug C-1). Match by content among user messages in
        // the response — the backend persisted the just-sent message and
        // returns it in the messages array. We pick the LAST user message
        // with the matching content to pin the most recent send.
        const responseMessages = sentSession?.messages ?? []
        const serverUserMessage = [...responseMessages]
          .reverse()
          .find((m) => m.role === 'user' && m.content === text)
        if (serverUserMessage && serverUserMessage.id) {
          const local = this.messages.find((m) => m.id === optimisticMessage.id)
          if (local) {
            local.id = serverUserMessage.id
          }
        }

        // SSE is the source of truth during a send. Never replace this.messages
        // with a backend refetch here — that would inject backend-loaded assistant
        // rows (status === undefined) that handleContentChunk adopts as targets,
        // causing the prior response to appear to mutate into the new one. The
        // [DONE] sentinel from SSE, the error handler, and the stall watchdog all
        // handle the end-of-stream and failure paths.

        await this.loadSessions()
      } catch (error) {
        this.error = error instanceof Error ? error.message : 'Failed to send message'
        // Mark the optimistic bubble as failed so the user sees their
        // attempt didn't go through (compounding bug C-2). The bubble stays
        // in place — content is preserved so the user can retry by
        // reverting and re-sending.
        const local = this.messages.find((m) => m.id === optimisticMessage.id)
        if (local) {
          local.status = 'failed'
        }
      } finally {
        sessionStream.disconnect()
        this.isLoading = false
        this.isStreaming = false
      }
    },

    // Re-arm the stall watchdog whenever there is fresh streaming activity.
    // Called from applyContentEvent on every chunk to indicate liveness; the
    // initial arm happens implicitly inside sessionStream.connect. The 60s
    // window is intentionally generous — agents can sit thinking on a slow
    // tool call without producing chunks; we only want to trip on "actually
    // dead" streams, not "agent is busy".
    //
    // sessionId tracks which session armed this watchdog so a trip can
    // reconcile against the right session (compounding bug C-6 from the
    // PR-2 plan: a watchdog from session A must not act on session B after
    // a navigation). When omitted, reconcile is skipped on trip — legacy
    // call sites still get the gate-clearing behaviour.
    armStallWatchdog(sessionId?: string): void {
      sessionStream.armWatchdog(() => this.handleStreamStall(sessionId))
    },

    // Stall trip handler. Stream stalled — unsticky the input gate so the
    // user can recover without reloading the page. The error footer surfaces
    // the cause; if no chunks arrived at all the in-flight assistant bubble
    // (if any) stays in-place but is no longer locked.
    //
    // sessionId is the session whose SSE armed the watchdog. When provided
    // (every PR-2 caller does), reconcile so a stream that completed
    // server-side without [DONE] (proxy hang, network glitch) is recovered:
    // the bubble updates from the partial chunk to the canonical backend
    // state without the user having to reload. Without the sessionId
    // argument, the call site is legacy and reconcile is skipped — the
    // gate-clearing behaviour remains unchanged.
    handleStreamStall(sessionId?: string): void {
      this.error = 'Response stalled — the stream produced no activity for 60 seconds. You can send another message.'
      this.isLoading = false
      this.isStreaming = false
      if (sessionId) {
        void this.reconcileFromBackend(sessionId)
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
      let target =
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
        // No existing delegation or running assistant — create a delegation_started
        // card so in-flight delegations are visible immediately in the message thread
        // rather than appearing only after the full session history reloads.
        const newDelegation: Message = {
          id: `delegation-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
          role: 'delegation_started',
          content: '',
          timestamp: new Date().toISOString(),
          status: 'running',
          targetAgent: info.target_agent,
          chainId: info.chain_id,
        }
        this.messages.push(newDelegation)
        target = newDelegation
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
      // The watchdog only trips on dead streams. Pass currentSessionId so a
      // trip can reconcile against the right session (the C-3 chunk-handler
      // guard ensures applyContentEvent only runs while currentSessionId
      // still matches the streaming session).
      this.armStallWatchdog(this.currentSessionId ?? undefined)

      // Classify into the discriminated union — see web/src/lib/sseEvent.ts
      // for the source-of-truth list of event variants tracked from the Go
      // emitter. The exhaustive switch below means a new event type added
      // server-side without a frontend handler fails compile rather than
      // being silently swallowed.
      //
      // Pre-this-PR the dispatch was a `Record<string, unknown>` switch
      // with a structural-fallback for delegation events that lacked the
      // type discriminant. The Go side now ALWAYS tags delegation events
      // with `type: 'delegation'` (writeSSEDelegationInfo injects the
      // field even when wrapping a provider DelegationInfo), so the
      // structural fallback was dead code.
      const event: SSEEvent = parseSSEPayload(payload)
      switch (event.kind) {
        case 'done':
          this.handleStreamDone()
          return
        case 'content':
          this.handleContentChunk({ content: event.content })
          return
        case 'tool_call':
          this.handleToolCallEvent({ name: event.name, status: event.status, input: event.input })
          return
        case 'skill_load':
          this.handleToolCallEvent({ name: event.name, status: 'running' })
          return
        case 'tool_result':
          this.handleToolResultEvent({ content: event.content })
          return
        case 'delegation':
          this.applyDelegationEvent(event.raw)
          return
        case 'error':
          this.error = event.error
          return
        case 'harness_retry':
        case 'harness_attempt_start':
        case 'harness_complete':
        case 'harness_critic_feedback':
          // Harness events are surfaced by the TUI but the Vue chat thread
          // does not yet render them as bubbles — silently ignored here.
          // Adding rendering is a future change; the dispatch path is
          // typed so a renderer addition is a simple new case.
          return
        case 'unknown':
        case 'malformed':
          // Defensive: log structural-only metadata (no chunk content) so a
          // future emitter mismatch is visible in window.__flowstateStreamLog
          // without leaking user data. The event.kind is the only payload
          // we record — never event.raw, which may carry user secrets.
          recordStreamEvent({
            kind: 'event-dropped',
            sessionId: this.currentSessionId ?? '',
            reason: event.kind,
          })
          return
        default:
          exhaustivenessGuard(event)
      }
    },

    // handleStreamDone owns the [DONE] sentinel side effects: seal any
    // in-flight assistant bubble so a later turn's chunks cannot land on
    // it (see "Session message upsert collision" bug-fix note), clear the
    // streaming flag, and cancel the stall watchdog. isLoading is cleared
    // by sendMessage's finally block or by maybeReattachStream's close
    // handler — both already in place.
    handleStreamDone(): void {
      const inFlight = [...this.messages].reverse().find(
        (message) => message.role === 'assistant' && message.status === 'running',
      )
      if (inFlight) {
        inFlight.status = 'completed'
      }
      this.isStreaming = false
      sessionStream.clearWatchdog()
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

    handleToolCallEvent(info: { name?: unknown; status?: unknown; type?: unknown; input?: unknown }): void {
      const toolName = String(info.name ?? info.type ?? 'unknown')
      const status = String(info.status ?? 'running')
      // Remember the tool name so the next tool_result event can be routed
      // appropriately — the SSE tool_result payload only carries content,
      // not the tool name. This is the seam the todowrite ingestion hooks
      // into below.
      this.lastToolName = toolName

      // `input` carries the JSON-encoded arguments string emitted by the
      // server. Store it as toolInput so toolRenderSpec can build the heading
      // from the primary argument (e.g. "bash cat /home/user/foobar.md").
      const toolInput = typeof info.input === 'string' && info.input ? info.input : undefined

      const toolMessage: Message = {
        id: `tool-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
        role: 'tool_result',
        toolName,
        toolInput,
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
      // Kill any in-flight stream before truncating — without this, chunks
      // arriving after the slice would re-insert content that was just removed.
      sessionStream.disconnect()
      this.isLoading = false
      this.isStreaming = false
      await truncateSessionMessages(this.currentSessionId, messageId)
      this.messages = this.messages.slice(0, idx)
      this.composerText = content
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
