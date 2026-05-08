import { defineStore } from 'pinia'
import type { Agent, Message, Model, Session, SessionSummary, Swarm } from '@/types'
import {
  createSession,
  fetchAgents,
  fetchModels,
  fetchSessionMessages,
  fetchSessions,
  fetchSwarms,
  sendSessionMessage,
  truncateSessionMessages,
  updateSessionAgent,
  updateSessionModel,
} from '@/api'
import { useSessionStream, type SessionStream } from '@/composables/useSessionStream'
import { recordStreamEvent } from '@/lib/streamLog'
import { exhaustivenessGuard, parseSSEPayload, type SSEEvent } from '@/lib/sseEvent'
import { dismissToast, showToast, updateToast } from '@/composables/useToast'
import { useTodoStore } from './todoStore'

const activeSessionStorageKey = 'chat.currentSessionId'
const activeAgentStorageKey = 'chat.agentId'
const activeModelStorageKey = 'chat.selectedModel'
const activeProviderStorageKey = 'chat.selectedProvider'

// default-assistant is the friendly general-purpose chat agent — it answers
// directly when it can and delegates to specialists when the request needs
// one. It is the right starting point for open-ended user requests, in
// preference to a sprint-coordinator orchestrator like Team-Lead which is
// optimised for multi-step delivery rather than conversational use.
//
// The id below MUST match the manifest's id field at
// internal/app/agents/default-assistant.md (canonical: lowercase, hyphenated).
// Backend default in internal/config/config.go is the same id, so no agent_id
// in the POST /sessions body still resolves to the same agent.
export const DEFAULT_AGENT_ID = 'default-assistant'

/**
 * TOOL_ACTIVITY_DISMISS_MS — how long after the LAST tool_call the rolling
 * activity toast lingers before auto-dismissing. Calibrated to feel "live"
 * (the user sees the tool indicator pulse during a multi-tool burst) without
 * sticking around past the burst end. 1.2 seconds is a balance — short
 * enough that the toast disappears quickly when the model is done invoking
 * tools, long enough that two tools fired 500ms apart feel like a single
 * burst rather than two flashes.
 */
export const TOOL_ACTIVITY_DISMISS_MS = 1200

/**
 * describeToolName maps a raw tool name (as reported by the SSE tool_call
 * event — usually the same string the provider uses) to plain-language
 * verb-style copy suitable for a non-technical user. The user explicitly
 * called out "tool: bash" as too technical; "Running command" is the
 * design target.
 *
 * Lookup is case-insensitive: the Anthropic SDK ships TitleCase tool names
 * (Bash, Read, Edit), the openaicompat / z.ai pipeline often lowercases
 * them, and the FlowState dispatcher emits both depending on the upstream
 * provider. A single map covers both shapes.
 *
 * Unknown tools fall back to "Running {raw-name}" rather than blanking —
 * the user still gets a recognisable signal even on a tool we haven't
 * mapped yet (a new MCP tool, a custom dispatcher entry, a future
 * provider extension). This is the deliberately permissive contract:
 * a notification is more useful than a missing one, even if the wording
 * is a literal tool id.
 */
export function describeToolName(rawName: string): string {
  const key = rawName.trim().toLowerCase()
  switch (key) {
    case 'bash':
    case 'shell':
    case 'terminal':
      return 'Running command'
    case 'read':
    case 'view':
      return 'Reading file'
    case 'edit':
    case 'multiedit':
    case 'str_replace_editor':
      return 'Editing file'
    case 'write':
    case 'create_file':
      return 'Writing file'
    case 'grep':
    case 'search':
      return 'Searching files'
    case 'glob':
    case 'find':
      return 'Finding files'
    case 'webfetch':
    case 'web_fetch':
    case 'fetch':
      return 'Fetching web page'
    case 'websearch':
    case 'web_search':
      return 'Searching the web'
    case 'task':
    case 'agent':
    case 'delegate':
      return 'Delegating to agent'
    case 'todowrite':
    case 'todo_write':
    case 'update_todos':
      return 'Updating to-dos'
    case 'notebookedit':
    case 'notebook_edit':
      return 'Editing notebook'
    default:
      // Keep raw form readable: replace underscores with spaces so an
      // unmapped tool like "fetch_models" reads as "fetch models" rather
      // than the underscore-joined token. Don't strip — the raw tool name
      // is still informative when we don't have a friendlier verb.
      return `Running ${rawName.replace(/_/g, ' ')}`
  }
}

/**
 * composeToolActivityMessage builds the message body for the rolling
 * tool-activity toast given the full in-order list of tool names that
 * have fired during the current burst. Single tool: just the friendly
 * verb. Two-or-more: "{first verb} + N more" so the user gets a sense
 * of scale without an unbounded growing list.
 *
 * Why not list every tool name (e.g. "Reading file, Searching files,
 * Running command"): on tool-heavy turns the message would balloon and
 * push other content out of the toast frame. The "+ N more" form keeps
 * the toast height fixed.
 */
export function composeToolActivityMessage(toolNames: string[]): string {
  if (toolNames.length === 0) return ''
  const firstLabel = describeToolName(toolNames[0])
  if (toolNames.length === 1) return firstLabel
  const more = toolNames.length - 1
  return `${firstLabel} + ${more} more`
}

/**
 * describeFailoverReason maps a failover-reason token (from
 * classifyFailoverReason in internal/plugin/failover/stream_hook.go) to
 * plain English suitable for a toast notification body. The wording is
 * deliberately user-facing — no jargon (no "429", no "HTTP", no
 * "ErrorType"). Any unrecognised token degrades to "unavailable" which
 * is true and non-alarming.
 */
function describeFailoverReason(reason: string): string {
  switch (reason) {
    case 'rate_limited':
      return 'rate-limited'
    case 'billing':
      return 'unavailable due to billing'
    case 'quota':
      return 'over its quota'
    case 'overload':
      return 'overloaded'
    case 'auth_failure':
      return 'unavailable (authentication failed)'
    case 'model_not_found':
      return 'no longer available'
    case 'unavailable':
      return 'unavailable'
    case 'timeout':
      return 'too slow to respond'
    default:
      return 'unavailable'
  }
}

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

// Error-handling convention (Principal F7)
// =========================================
// The chat store splits actions into two error-handling families. Both are
// in-tree today and the split matches the call-site contract — do NOT
// "normalise" them without re-auditing every consumer.
//
// Pattern A — catch + assign-to-this.error (fire-and-forget actions):
//   Used when the action is invoked from the UI without an awaiting caller
//   or from an event handler that has no error channel of its own.
//   Examples: setAgent, setModel. The MessageBubble + chat-error footer
//   render `chatStore.error` directly so a try/catch in the action is the
//   minimum viable user-visible signal.
//
// Pattern B — propagate-and-let-caller-decide (initialisation + send):
//   Used when the caller has richer recovery context than the store can
//   reach. The two examples are restoreStateFromBackend (only ChatView
//   onMounted needs the user-facing toast — other callers may suppress)
//   and sendMessage (the optimistic-bubble failed-marker is set inside the
//   action, but downstream toast surfacing belongs to the caller).
//
// New actions choose A when there is no caller that benefits from the
// thrown error; B when there is. Don't introduce a third pattern.
export const useChatStore = defineStore('chat', {
  state: () => ({
    availableAgentDetails: [] as Agent[],
    availableAgents: [] as string[],
    // swarms backs the @-picker's swarm slice in MessageInput — populated
    // by loadSwarms() at bootstrap. The Vue web chat had this slice
    // stubbed empty pending backend wiring (Web Swarm Mention Parity,
    // May 2026); now it carries the real registry projection.
    swarms: [] as Swarm[],
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
    // criticalError carries the wire-level signal for fatal provider
    // errors (revoked OAuth, 401, model-not-found, billing/quota
    // lockout). Set when applyContentEvent sees an SSE event of
    // kind: 'stream_critical' (sniffed from the canonical
    // "critical stream error" safeMsg in the SSE/WS error JSON shape —
    // see web/src/lib/sseEvent.ts CRITICAL_STREAM_ERROR_MESSAGE). The
    // distinction from the existing transient `error` field above is
    // deliberate: the chat UI surfaces criticality via a persistent
    // banner (CriticalErrorBanner.vue) above the message list, while
    // transient errors fall through to the existing toast path.
    //
    // The session is unrecoverable until the operator re-authenticates,
    // fixes billing, or switches provider, so the banner persists
    // across user interactions until either (a) the user clicks
    // Dismiss, which calls `dismissCriticalError()`, or (b) the user
    // navigates to a different session, which resets state via the
    // shared session-change clear path.
    //
    // `correlationId` is the server-side log lookup token; the banner
    // exposes it via a "Show details" affordance so users can paste it
    // for support. The raw provider error never reaches the client —
    // only the canonical safeMsg + correlation id.
    criticalError: null as { message: string; correlationId: string } | null,
    // currentContextUsage carries the live figures the toolbar usage
    // chip renders. Populated by applyContentEvent on every
    // `context_usage` SSE event the engine prepends to a stream (Phase
    // 2 of the May 2026 context-window saturation fix — companion to
    // the proactive overflow gate). The chip displays
    // `{inputTokens}/{limit}` plus a `{percentage}%` label, with
    // threshold colours that match the CriticalErrorBanner palette
    // (≥75% warning, ≥90% danger).
    //
    // Why a structured slice rather than threading raw payload via
    // props: the chip lives at the toolbar level (web/src/views/
    // ChatView.vue between the provider-label and ModelPicker) while
    // the dispatch lives in the store. A central slice keeps one
    // source of truth and lets the chip render purely from store
    // state.
    //
    // Cleared on session change (loadSessionMessages) so a stale
    // figure from a prior session does not bleed into the new one. A
    // fresh stream on the new session repopulates it.
    //
    // Defensive empty-figure payloads (a future emitter that ships
    // only the type) MUST NOT clobber a healthy figure — handled in
    // the dispatch (mirror of the model_active guard).
    currentContextUsage: null as {
      inputTokens: number
      outputReserve: number
      limit: number
      percentage: number
    } | null,
    // ---- auto-compaction telemetry (Slice 6b — Phase 4 follow-up) -------
    //
    // The Go SSE pipeline emits a `context_compacted` event when the L2
    // auto-compactor publishes EventContextCompacted on the bus (Slice 6a
    // wired the bridge in internal/api/event_bridge.go +
    // writeSSEContextCompacted in internal/api/server.go). The store
    // routes it through handleContextCompactedEvent which:
    //   - Increments `compactionEventCount` (canary signal: non-zero ⇒
    //     at least one compaction has fired this session ⇒ tooltip is
    //     meaningful).
    //   - Records the most-recent compaction's payload onto
    //     `lastCompaction` so the ContextUsageChip can derive its
    //     tooltip copy ("Last compaction saved 45K tokens (50K → 5K)").
    //
    // Both fields reset on session change (loadSessionMessages) — a stale
    // compaction figure from a prior session must NOT bleed into the new
    // session's chip.
    //
    // Why a structured slice rather than threading raw payload via props:
    // the chip lives at the toolbar level (mounted once in ChatView.vue)
    // while the dispatch lives in the store. A central slice keeps one
    // source of truth and lets the chip render purely from store state,
    // mirroring `currentContextUsage` for `context_usage`.
    compactionEventCount: 0,
    lastCompaction: null as {
      originalTokens: number
      summaryTokens: number
      tokensSaved: number
      at: number
      // Phase-5 Slice δ — Trigger discriminant identifies the path
      // that fired compaction. Closed vocabulary: ratio |
      // gate_proximity | model_switch | tool_result_wave. Empty is
      // tolerated for forward-compatibility; the chip tooltip falls
      // back to the generic copy when unrecognised.
      trigger: string
    } | null,
    // ---- swarm gate-failure surface (Plans/Gate Bus Bridge) -----------
    //
    // The Go SSE pipeline emits a `gate_failed` event when the engine's
    // runSwarmGates / dispatchMemberGates halts on a *swarm.GateError.
    // applyContentEvent routes the parsed payload into this slice; the
    // GateFailureBanner.vue component reads it and renders a persistent
    // banner above the message pane.
    //
    // Why a structured slice rather than a transient toast: the banner
    // persists until the operator dismisses it (gate failures halt the
    // dispatch — auto-clear would leave a confusing "swarm finished
    // with no transcript" UX). The slice survives component re-mount;
    // dismiss + session-change clear it.
    //
    // The slice resets on session change (loadSessionMessages) so a
    // halt from a prior session does not bleed into the new one.
    lastGateFailure: null as {
      swarmId: string
      lifecycle: string
      memberId: string
      gateName: string
      gateKind: string
      reason: string
      cause: string
      coordStoreKeys: string[]
    } | null,
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
    // ---- tool-activity rolling-toast state (May 2026 notifications work) ----
    //
    // The user requested visible notifications when tools fire AND when the
    // provider/model pivots. A naive implementation toasts per tool_call,
    // which is unusable on tool-heavy turns (10+ tools/turn observed). We
    // aggregate instead: the FIRST tool_call of a quiet period spawns one
    // "loading"-variant toast that updates as subsequent tool_calls arrive,
    // and a rolling debounce auto-dismisses it 1.2 seconds after the last
    // tool_call. The fields below carry the bookkeeping for that flow.
    //
    //   toolActivityToastId    — id of the live aggregating toast, null when
    //                            no toast is currently showing for tools.
    //   toolActivityNames      — in-order list of tool names accumulated this
    //                            burst, used to compose the toast message.
    //                            Cleared when the toast auto-dismisses.
    //   toolActivityTimer      — opaque setTimeout handle for the rolling
    //                            auto-dismiss. Cleared and re-armed on every
    //                            new tool_call.
    //
    // Transient UI state — never persisted, never hydrated from the backend.
    toolActivityToastId: null as number | null,
    toolActivityNames: [] as string[],
    toolActivityTimer: null as ReturnType<typeof setTimeout> | null,
    // ---- provider/model change toast deduplication state ------------------
    //
    // The Go SSE pipeline emits BOTH provider_changed (failover transition)
    // and model_active (every-stream actual-model affordance). When a
    // failover happens, both events fire back-to-back targeting the same
    // (provider, model) pair. provider_changed already carries detailed
    // toast copy ("Switched to {model} — {prev} is rate-limited"); a
    // follow-up generic model_active toast is duplicate noise.
    //
    // lastProviderChangeKey snapshots the "<provider>+<model>" the most
    // recent provider_changed pivoted to. handleModelActiveEvent compares
    // against this and stays silent if it matches — letting the richer
    // failover toast stand alone.
    //
    // Cleared on session change (loadSessionMessages clears it via the
    // shared reset path) so a model_active on a fresh session is not
    // accidentally suppressed by a key from a prior session.
    lastProviderChangeKey: null as string | null,
    // ---- bootstrap singleton (App-level loading-overlay coordination) ----
    //
    // bootstrap() wraps restoreStateFromBackend so the App-level loading
    // overlay has one definitive "first hydration done" promise to await,
    // and so the documented loadAgents/restoreStateFromBackend race (eager
    // pickers racing the canonical agent resolution) is closed at the
    // source: the first call seeds this promise, every subsequent caller
    // (App.vue, ChatView.onMounted, any future picker that mounts before
    // restore completes) gets the same in-flight or already-settled
    // handle. The underlying restoreStateFromBackend therefore runs
    // exactly once per store instance per page-load.
    //
    // Transient — never persisted, never hydrated from the backend.
    bootstrapPromise: null as Promise<void> | null,
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
    // bootstrap: singleton wrapper around restoreStateFromBackend.
    //
    // App-level callers (the loading overlay in App.vue, ChatView's
    // onMounted handler, any future caller that needs a "first hydration
    // done" gate) await this rather than restoreStateFromBackend
    // directly. The first call invokes the underlying restore; concurrent
    // and subsequent calls reuse the same promise instance. Failures
    // propagate to every awaiter identically.
    //
    // Why a singleton: the existing call sites are already racy by design
    // (eager pickers fire loadAgents before ChatView awaits restore — see
    // the long history comment on loadAgents). Centralising "kick off the
    // canonical restore exactly once" here means the overlay can rely on
    // it and the legacy callers can keep their current shape without
    // double-fetching agents/models/sessions.
    bootstrap(): Promise<void> {
      if (this.bootstrapPromise) {
        return this.bootstrapPromise
      }
      this.bootstrapPromise = this.restoreStateFromBackend()
      return this.bootstrapPromise
    },

    async restoreStateFromBackend(): Promise<void> {
      await this.loadAgents()
      await this.loadSwarms()
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
      // loadAgents is responsible for populating the agent list — it is NOT
      // responsible for deciding which agent the user should be talking to.
      // restoreStateFromBackend owns the active-agent precedence (session
      // agent first, then persisted, then DEFAULT_AGENT_ID, then alphabetical
      // fallback), and setAgent owns the user-driven switch path.
      //
      // Pre-fix this method also seeded an active agent when none was set:
      //
      //     if (!this.agentId && agents.length > 0) {
      //       await this.setAgent(agents[0].id)   // agents[0] = alphabetical first
      //       return
      //     }
      //
      // That created two problems:
      //
      //   1. AgentPicker.onMounted fires `void chatStore.loadAgents()` (no
      //      await) BEFORE ChatView.onMounted runs `await chatStore.
      //      restoreStateFromBackend()`. The eager seed persisted agents[0]
      //      (alphabetically API-Engineer) to localStorage, where it then
      //      beat DEFAULT_AGENT_ID in restoreStateFromBackend's
      //      `sessionAgentId ?? persistedAgentId ?? defaultAgent` chain.
      //      Commit 5c596e8 changed DEFAULT_AGENT_ID to default-assistant
      //      but the live UX never saw it because of this pre-empt race.
      //
      //   2. Even after preferring DEFAULT_AGENT_ID over agents[0], the
      //      eager seed made the AgentPicker's "Default Assistant" label
      //      flip BEFORE restoreStateFromBackend had hydrated
      //      currentSessionId. Tests gating on the picker label as a proxy
      //      for "store fully restored" then proceeded to send a message
      //      while currentSessionId was still null, hitting sendMessage's
      //      lazy-create branch and creating a phantom session that
      //      restoreStateFromBackend's late completion subsequently
      //      clobbered (currentSessionId=null, messages=[]).
      //
      // The cleaner contract is: loadAgents fetches the list, period. The
      // store is left with `agentId === ''` until restoreStateFromBackend
      // resolves it from session > localStorage > default-assistant.
      // setAgent (user-driven) and restoreStateFromBackend (boot-time) are
      // the only two paths that mutate the active agent. AgentPicker,
      // MessageInput, and AgentSwitcher only need the list — they no
      // longer indirectly drive agent selection by mounting.
      const agents = await fetchAgents()
      this.availableAgentDetails = agents
      this.availableAgents = agents.map((agent) => agent.id)
    },

    /**
     * loadSwarms refreshes the registered-swarm list backing the
     * MessageInput's @-picker. Mirrors loadAgents — fetches the list
     * and populates state. The store is left to MessageInput / any
     * future swarm panel to read; loadSwarms does NOT touch agentId
     * or session state.
     *
     * No-throw on empty list — `[]` is a legitimate state when the
     * backend has no registered swarms (the bare-server test path).
     * Errors propagate so callers can surface them via the existing
     * top-level error path; today the only caller is bootstrap, which
     * runs alongside the other restore steps.
     */
    async loadSwarms(): Promise<void> {
      const swarms = await fetchSwarms()
      this.swarms = swarms
    },

    async setAgent(agentId: string): Promise<void> {
      const previousAgentId = this.agentId
      this.agentId = agentId
      persistAgentId(agentId)

      if (!agentId || !this.currentSessionId || agentId === previousAgentId) {
        return
      }

      try {
        const updated = await updateSessionAgent(this.currentSessionId, agentId)
        // Phase 3 — TUI-cadence parity. The PATCH response carries
        // the engine's fresh context_usage shape so the chip ticks
        // up to reflect the new agent's preferred model / context
        // limit without waiting for the next pre-send.
        this.applyContextUsageFromSession(updated)
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
        const updated = await updateSessionModel(this.currentSessionId, modelId, providerId)
        // Phase 3 — TUI-cadence parity. The PATCH response carries
        // the engine's fresh context_usage shape so the chip
        // pivots to the new limit immediately rather than waiting
        // for the next pre-send.
        this.applyContextUsageFromSession(updated)
      } catch (error) {
        this.error = error instanceof Error ? error.message : 'Failed to update session model'
      }
    },

    /**
     * Phase 3 helper — read the PATCH response's contextUsage field
     * (when present) and route it through handleContextUsageEvent so
     * the chip updates via the same code path as the SSE-streamed
     * event. Snake-case wire shape is mapped to the camelCase store
     * shape inline.
     *
     * No-op when the field is missing — degraded engines (no token
     * counter, no resolvable limit) suppress the field server-side.
     */
    applyContextUsageFromSession(session: { contextUsage?: Session['contextUsage'] }): void {
      const cu = session.contextUsage
      if (!cu) {
        return
      }
      this.handleContextUsageEvent({
        inputTokens: cu.input_tokens,
        outputReserve: cu.output_reserve,
        limit: cu.limit,
        percentage: cu.percentage,
      })
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
      // Propagate the (provider, model) pair the backend seeded onto the
      // session from the agent manifest's first PreferredModels entry. The
      // POST /sessions response now carries these fields populated when
      // the manifest declares any preferred model, so the persistent
      // activity-indicator chip can render `on <model> · <provider>` as
      // soon as the user issues a prompt — no waiting for a failover
      // transition or a manual model selection. Empty strings (manifest
      // had no PreferredModels) keep the chip hidden, matching the
      // legacy degraded-session behaviour.
      if (session.currentModelId) {
        this.currentModelId = session.currentModelId
      }
      if (session.currentProviderId) {
        this.currentProviderId = session.currentProviderId
      }
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
      // A critical-class banner from a prior session is no longer
      // relevant once the user switches contexts. The banner is bound
      // to the failing session — the new one starts clean. A fresh
      // critical event on the new session will repopulate this.
      this.criticalError = null
      // Same rationale for the usage chip — it tracks the live stream
      // on the prior session and is meaningless on a different one.
      // The next stream's first context_usage event repopulates it.
      this.currentContextUsage = null
      // Slice 6b — auto-compaction telemetry is per-session. A stale
      // "compacted ×3" counter or "saved 45K tokens" tooltip from a
      // prior session must NOT bleed into the new one's chip. The
      // next stream's first context_compacted event (if any)
      // repopulates these.
      this.compactionEventCount = 0
      this.lastCompaction = null
      // Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026):
      // a halt from a prior session is bound to that session's
      // dispatch context; carrying it onto the new session would
      // misattribute the failure. A fresh halt on the new session
      // repopulates the banner.
      this.lastGateFailure = null
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

      // Refresh the session-level model+provider from the most recent
      // assistant message. The backend's appendSessionMessage promotes
      // the engine-stamped (model, provider) onto the session whenever
      // an assistant turn lands, but the only way that update reaches
      // the chat-store today is via a full sessions-list refresh — and
      // post-send reconcile calls this method, NOT loadSessions. Reading
      // the most recent assistant message's modelName / providerName is
      // sufficient: the per-message stamp is the source of truth for
      // attribution, and the chip displays whatever the active session
      // has on the chat-store. This keeps the chip in sync after every
      // turn without an extra round-trip to GET /api/v1/sessions.
      for (let i = sealedBackend.length - 1; i >= 0; i--) {
        const m = sealedBackend[i]
        if (m && m.role === 'assistant' && (m.modelName || m.providerName)) {
          if (m.modelName) this.currentModelId = m.modelName
          if (m.providerName) this.currentProviderId = m.providerName
          break
        }
      }

      recordStreamEvent({
        kind: 'reconcile-result',
        sessionId,
        messageCount: this.messages.length,
      })
    },

    // loadSessionByAgentId resolves the in-thread delegation-card click —
    // MessageBubble.loadDelegatedSession passes `targetAgent` here because
    // the persisted `delegation` / `delegation_started` message carries
    // only the target agent name (the streaming.DelegationEvent wire shape
    // has no ChildSessionID; the load-bearing child_session_id is on the
    // separate SwarmEvent stream consumed by DelegationStrip, not on the
    // per-session SSE chat stream).
    //
    // Resolution order — load-bearing:
    //   1. The most-recent child of the active session whose agentId
    //      matches. This is the click's actual intent: "open the agent
    //      this parent just delegated to". Anchoring on parentId pins
    //      the click to the active parent's branch of the delegation
    //      tree.
    //   2. The most-recent session for that agent overall, falling back
    //      to oldest-first only if no createdAt is present. Used when no
    //      active parent exists or no child of the parent matches (e.g.
    //      the parent is itself the delegated agent — a swarm-bridge
    //      edge case).
    //
    // Pre-fix this picked sessions[0]-of-match against an oldest-first
    // backend list, so a long-running backend with a stale standalone
    // session for the same agent always loaded that stale session
    // instead of the just-delegated child. The user reported "we are no
    // longer able to click on the delegating card and view the
    // delegated agents session" — the click fired but landed on the
    // wrong session, defeating the affordance.
    async loadSessionByAgentId(agentId: string): Promise<boolean> {
      const matchesAgent = (s: SessionSummary) =>
        (s.currentAgentId ?? s.agentId) === agentId

      const sortByCreatedAtDesc = (a: SessionSummary, b: SessionSummary) =>
        b.createdAt.localeCompare(a.createdAt)

      // Step 1: prefer a child of the active session.
      let candidate: SessionSummary | undefined
      if (this.currentSessionId) {
        const childrenOfCurrent = this.sessions
          .filter((s) => s.parentId === this.currentSessionId && matchesAgent(s))
          .sort(sortByCreatedAtDesc)
        candidate = childrenOfCurrent[0]
      }

      // Step 2: fall back to the most-recent overall match. The
      // toSorted equivalent (spread + sort) is used so we don't mutate
      // the pinia state array.
      if (!candidate) {
        const overallMatches = this.sessions
          .filter(matchesAgent)
          .sort(sortByCreatedAtDesc)
        candidate = overallMatches[0]
      }

      if (!candidate) return false

      await this.loadSessionMessages(candidate.id)
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
      // Note on activity affordance: `isStreaming` is intentionally NOT
      // set true here — the SSE stream hasn't actually started yet, and
      // `isStreaming` retains its precise meaning ("SSE chunks are
      // arriving"). The user-facing "agent is working" indicator
      // surfaces while either flag is true (see ChatView.vue v-if), so
      // the affordance is continuously visible from this point through
      // to the post-send reconcile completing — a regression in either
      // gate would otherwise hide the indicator on backends that emit
      // no intermediate `content` events.
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
          // Mirror newSession: the lazy-create path on a brand-new chat
          // (user types into a session-less view and hits send) must
          // surface the seed defaults onto the chip too, otherwise the
          // first turn would still render with a blank chip until the
          // first assistant chunk lands and the engine-stamped pair
          // reaches restoreStateFromBackend on a refresh.
          if (session.currentModelId) {
            this.currentModelId = session.currentModelId
          }
          if (session.currentProviderId) {
            this.currentProviderId = session.currentProviderId
          }
        }

        // connect tears down any prior SSE, opens a new one, and arms the
        // stall watchdog so a stuck stream cannot leave isLoading locked.
        // The sessionId is captured in every callback closure so a
        // mid-stream session switch never lands chunks on the wrong session
        // and never reconciles against the wrong session's history.
        // (Compounding bugs C-3, C-6 from the PR-2 plan.)
        //
        // SSE end-of-stream handling here is intentionally minimal: we
        // close on [DONE]/error to prevent the browser auto-reconnecting
        // and registering a second broker subscriber on the next send,
        // but the canonical post-send state-sync runs unconditionally
        // AFTER `await sendSessionMessage` resolves (see the post-await
        // reconcile below). Pre-fix the [DONE] handler also fired
        // `reconcileFromBackend`; that ran BEFORE the POST resolved,
        // pulled an in-progress backend snapshot whose user message the
        // merge logic treated as new (the local `temp-*` was preserved
        // as an orphan), and produced a duplicate user bubble that
        // persisted until the optimistic-id swap collapsed it. The fix:
        // reconcile exactly once, post-POST, after the id swap has
        // already replaced the temp-* with the canonical id so the
        // merge sees zero orphans. See bug-fix note "Vue Chat
        // Fresh-Session Duplicate User Bubble + Missing Streaming
        // Affordance (May 2026)".
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
              // before the finally block runs. Reconcile is intentionally
              // NOT called here — see the post-await reconcile below.
              sessionStream.disconnect()
            }
          },
          onError: () => {
            // SSE connection dropped (stream ended or network error) —
            // close immediately to prevent auto-reconnect registering a
            // duplicate broker subscriber on the next send. Reconcile is
            // intentionally NOT called here either; the post-await
            // reconcile below covers the success path, and the
            // catch/finally below covers the failure path so a network
            // drop never triggers a reconcile that races with the still
            // pending POST resolution.
            sessionStream.disconnect()
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
        //
        // CRITICAL: this swap MUST run before reconcileFromBackend below,
        // otherwise the merge would treat the local `temp-*` row as an
        // orphan to preserve and the canonical-id row from the backend
        // history would be ADDITIONAL — producing a duplicate user
        // bubble. Order is load-bearing.
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

        // Canonical post-send sync. The POST response carries the
        // backend's authoritative session state, but we route through
        // reconcileFromBackend (which re-fetches via GET /messages) for
        // three reasons:
        //   1. reconcileFromBackend already implements the seal/merge
        //      semantics required to land tool_result, delegation, and
        //      sealed-assistant rows correctly. Inlining the same logic
        //      here would duplicate it and risk drift.
        //   2. The POST may have triggered child-session writes (a
        //      delegation, a swarm fan-out) that the response does not
        //      include. The GET fetches the canonical merged history.
        //   3. reconcileFromBackend re-checks currentSessionId before
        //      and after the await, so a session switch concurrent with
        //      this background sync is safe — re-implementing here would
        //      drop the guard.
        // The id swap above guarantees the local temp-* row has already
        // been renamed to the canonical id, so the merge's orphan-
        // preservation rule produces zero duplicates.
        await this.reconcileFromBackend(capturedSessionId)

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
        case 'stream_critical':
          // Fatal provider error — the engine has classified this as
          // SeverityCritical (revoked OAuth, 401, model-not-found,
          // billing/quota lockout) and the session is unrecoverable
          // until the operator intervenes. Surface a persistent banner
          // (CriticalErrorBanner.vue) instead of the transient toast
          // path used for `error` events. Always overwrite a prior
          // criticalError — a fresh fatal error must replace any
          // previously-dismissed banner with the new correlation id so
          // support can locate the latest server-side log entry.
          this.criticalError = {
            message: event.error,
            correlationId: event.correlationId,
          }
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
        case 'thinking':
          this.handleThinkingEvent({ content: event.content })
          return
        case 'provider_changed':
          this.handleProviderChangedEvent({
            from: event.from,
            to: event.to,
            reason: event.reason,
          })
          return
        case 'model_active':
          this.handleModelActiveEvent({
            provider: event.provider,
            model: event.model,
          })
          return
        case 'context_usage':
          this.handleContextUsageEvent({
            inputTokens: event.inputTokens,
            outputReserve: event.outputReserve,
            limit: event.limit,
            percentage: event.percentage,
          })
          return
        case 'context_compacted':
          this.handleContextCompactedEvent({
            sessionId: event.sessionId,
            originalTokens: event.originalTokens,
            summaryTokens: event.summaryTokens,
            // Phase-5 Slice δ — surface the Trigger discriminant onto
            // the chip tooltip. Empty default tolerates historical
            // events that pre-date the field.
            trigger: event.trigger,
          })
          return
        case 'gate_failed':
          // Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026):
          // a halt-class swarm-gate failure populates the
          // session-scoped lastGateFailure slice the
          // GateFailureBanner reads. Each fresh halt unconditionally
          // overwrites the prior payload — a new failure is a new
          // event the operator must see (mirrors CriticalErrorBanner's
          // overwrite policy). The session-scope guard is
          // defence-in-depth; the api server scopes the SSE wire to
          // the active session so in practice another session's halt
          // never reaches this dispatch.
          this.lastGateFailure = {
            swarmId: event.swarmId,
            lifecycle: event.lifecycle,
            memberId: event.memberId,
            gateName: event.gateName,
            gateKind: event.gateKind,
            reason: event.reason,
            cause: event.cause,
            coordStoreKeys: event.coordStoreKeys,
          }
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

    /**
     * dismissCriticalError clears the persistent critical-error banner
     * for the current session. The banner re-appears the moment a new
     * critical event lands on the stream — `criticalError` is overwritten
     * unconditionally in the dispatch above. This is intentional: a
     * fresh fatal error after a dismissal is a new failure with a new
     * correlation id and the user must see it.
     *
     * Dismissal is per-session by virtue of `loadSessionMessages`
     * resetting the field on session change; this action does not
     * persist any "user has dismissed N criticals" history.
     */
    dismissCriticalError(): void {
      this.criticalError = null
    },

    /**
     * clearGateFailure clears the persistent gate-failure banner for
     * the current session. The banner re-appears the moment a fresh
     * gate_failed event lands on the stream — `lastGateFailure` is
     * overwritten unconditionally in the dispatch above. Mirrors
     * dismissCriticalError's intent: a fresh halt after a dismissal
     * is a new failure with a new context the operator must see.
     *
     * Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026).
     */
    clearGateFailure(): void {
      this.lastGateFailure = null
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

    /**
     * Drop #2 — Thinking handler.
     *
     * Accumulates the model's private reasoning onto the in-flight
     * assistant message's `thinkingContent` field. MUST NOT mutate
     * `content` — the public assistant turn is reserved for the actual
     * reply, not the model's chain of thought. The UI affordance to
     * disclose this text on demand is Track B's work; until that lands,
     * this handler exists so:
     *
     *   1. The watchdog re-arms during the reasoning phase (any SSE
     *      event coming through applyContentEvent counts as liveness).
     *   2. An in-flight assistant placeholder exists for the eventual
     *      content delta to land on (mirrors handleContentChunk).
     *   3. The data is captured end-to-end so the renderer addition is
     *      purely additive when Track B layers UI on top.
     */
    handleThinkingEvent(info: { content?: unknown }): void {
      if (typeof info.content !== 'string' || info.content.length === 0) {
        return
      }

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

      target.thinkingContent = (target.thinkingContent ?? '') + info.content
      // Note: target.content is INTENTIONALLY untouched. The model's private
      // reasoning is not the assistant's reply.
      this.isStreaming = true
    },

    /**
     * Track B — failover transition handler.
     *
     * When the failover hook switches providers mid-request (anthropic 429
     * → zai/glm-4.6 takes over), the SSE wire delivers a
     * provider_changed event. The handler does two things:
     *
     *   1. Surfaces a transient toast notification telling the user that
     *      a different model is now answering. The user explicitly asked
     *      for this in Track B — fallback can change quality / style /
     *      format and they need to know.
     *   2. Updates currentProviderId / currentModelId so the persistent
     *      toolbar chip in ChatView reflects the new active model going
     *      forward — the user doesn't have to keep the toast in mind to
     *      know what model produced the next message.
     *
     * Format: `to` is "<provider>+<model>" (e.g. "zai+glm-4.6"). The
     * split is on the FIRST "+" so model ids that themselves contain "+"
     * (rare; openrouter sometimes uses multi-tag identifiers) survive
     * intact.
     *
     * Defensive: an empty `to` leaves currentProviderId/currentModelId
     * untouched — better to keep the previous chip than blank it out
     * mid-conversation. The toast still fires with generic copy
     * ("Switched to a different model") so the user gets the signal.
     */
    /**
     * model_active handler — May 2026 chip-shows-selection-not-actual fix.
     *
     * The user reported (May 2026) that the persistent toolbar chip
     * "shows what was selected, not what actually ran". The backend now
     * prepends a `model_active` SSE event to EVERY successful stream
     * (see internal/plugin/failover/stream_hook.go prependModelActiveChunk)
     * carrying the actual (provider, model) pair the failover hook
     * chose. This handler updates currentProviderId / currentModelId so
     * the chip pivots from the user's selection to the actual model the
     * moment streaming starts.
     *
     * Behaviour notes:
     *   - On the common case (selection matches actual) this is a no-op
     *     for the user — the chip stays at its optimistic selection.
     *   - On the divergent case (failover, agent override, manifest
     *     override), the chip snaps to the truth before the first
     *     user-visible token arrives.
     *   - When the actual differs from the prior selection, the picker
     *     (which reads currentModelId) will also reflect the actual.
     *     That is intentional: the user's understanding of "what model
     *     is producing the answer I'm watching" is the chip + picker.
     *     A subsequent user-driven setModel still wins, because setModel
     *     re-PATCHes the backend session and the next stream emits
     *     model_active anew.
     *   - Empty fields (defensive: malformed payload from a future
     *     emitter) leave the prior values untouched. Better to keep the
     *     optimistic selection visible than blank the chip out.
     */
    handleModelActiveEvent(info: { provider?: unknown; model?: unknown }): void {
      const provider = typeof info.provider === 'string' ? info.provider : ''
      const model = typeof info.model === 'string' ? info.model : ''

      // Capture the prior chip values BEFORE the pivot so we can decide
      // whether to surface a toast. We only toast when the actual model
      // differs from what the user thought they selected — the common
      // "selection matches actual" case stays silent (otherwise every
      // single turn of every conversation would pop a toast).
      const priorProvider = this.currentProviderId
      const priorModel = this.currentModelId

      if (provider) {
        this.currentProviderId = provider
      }
      if (model) {
        this.currentModelId = model
      }

      // Toasting policy (May 2026 user-facing-notifications work):
      //
      //   1. Both fields empty (defensive payload) — silent. Already
      //      caught by the early no-op above; the chip stays put.
      //   2. Actual matches prior chip — silent. The user's mental model
      //      "I selected X, X is answering" is unbroken; a toast would be
      //      noise.
      //   3. Actual differs from prior chip AND a provider_changed just
      //      pivoted to this same target — silent. provider_changed has
      //      already shown a richer "Switched to X — primary is Y" toast
      //      that strictly dominates a generic model_active toast for the
      //      same transition.
      //   4. Actual differs from prior chip AND no recent provider_changed
      //      established this target — toast. This covers agent-override
      //      (the chosen agent runs on a different model than the picker
      //      shows), manifest-override (a swarm member pinned a model),
      //      and fresh sessions where the seed didn't include the actual.
      //
      // Why a separate path from provider_changed: provider_changed knows
      // the failure reason ("rate-limited", "over its quota") and crafts
      // a transition-specific message; model_active only knows the
      // destination. Generic copy here is correct.
      if (!provider && !model) {
        return
      }

      const targetKey = `${provider}+${model}`
      const priorKey = `${priorProvider}+${priorModel}`

      if (targetKey === priorKey) {
        // Common case — selection matched the actual model. No toast.
        return
      }

      if (targetKey === this.lastProviderChangeKey) {
        // provider_changed already toasted this exact transition. Stay
        // silent rather than double-fire. Don't clear the key — a future
        // model_active back to the same target inside the same session
        // is still that transition; only a *new* provider_changed should
        // overwrite the dedup key.
        return
      }

      const modelLabel = model || provider || 'a different model'
      showToast({
        title: 'Model changed',
        message: `Now answering with ${modelLabel}.`,
        variant: 'default',
        duration: 5000,
      })
    },

    /**
     * context_usage handler — Phase 2 of the May 2026 context-window
     * saturation fix.
     *
     * The Go SSE pipeline emits a `context_usage` event as the first
     * artefact of every Stream that has enough information to compute
     * it (token counter wired AND resolved limit > 0). The handler
     * updates `currentContextUsage` so the toolbar usage chip can
     * render the live figure alongside the model picker.
     *
     * Behaviour:
     *   - All-zero / empty payload (defensive: a future emitter that
     *     ships only the `type` field) leaves the prior figure
     *     untouched. Better to keep the prior chip visible than blank
     *     it mid-conversation. Mirrors the model_active "empty fields
     *     leave prior values" guard.
     *   - The handler MUST NOT touch currentProviderId /
     *     currentModelId. The toolbar chip's pivot is exclusively
     *     model_active-driven so failover toasts (which gate on
     *     lastProviderChangeKey) cannot be surprised by a usage-event
     *     side-effect. The provider/model fields on the wire are for
     *     display alongside the figure and are not surfaced into the
     *     store here (the chip reads them from the chip-pivot state).
     */
    handleContextUsageEvent(info: {
      inputTokens: number
      outputReserve: number
      limit: number
      percentage: number
    }): void {
      // Defensive guard — an all-zero payload (limit=0 in particular
      // would render `1234/0` in the chip, which is meaningless). The
      // engine suppresses the chunk when limit<=0 so a zero-limit
      // figure should never reach this handler in practice; the guard
      // is a belt-and-braces defence against a future emitter regression.
      if (
        info.inputTokens === 0 &&
        info.outputReserve === 0 &&
        info.limit === 0 &&
        info.percentage === 0
      ) {
        return
      }

      this.currentContextUsage = {
        inputTokens: info.inputTokens,
        outputReserve: info.outputReserve,
        limit: info.limit,
        percentage: info.percentage,
      }
    },

    /**
     * context_compacted handler — Slice 6b of the May 2026
     * context-management Phase-4 follow-ups (companion to Slice 6a's
     * gate-proximity force-fire).
     *
     * The Go SSE pipeline emits a `context_compacted` event when the
     * engine's L2 auto-compactor publishes EventContextCompacted on
     * the bus and the api-side bridge routes it onto the wire. The
     * handler:
     *
     *   - Ignores events whose `sessionId` does not match the
     *     `currentSessionId`. The api server scopes the SSE wire to
     *     the active session so this is a defence-in-depth guard
     *     against a future SSE multiplexing change.
     *   - Increments `compactionEventCount`. Non-zero is the canary
     *     signal the chip uses to enable its tooltip — without at
     *     least one compaction this session, the tooltip would be
     *     misleading.
     *   - Records the most-recent compaction onto `lastCompaction`
     *     with `tokensSaved = originalTokens - summaryTokens` and
     *     `at = Date.now()`. The chip's tooltip copy is derived from
     *     this state.
     *   - Triggers a Pinia reactive update; the chip's flash watcher
     *     (in ContextUsageChip.vue) observes the `compactionEventCount`
     *     getter increment and runs a 2-second flash class toggle.
     *     Mirroring the chip-side state-driven pattern (rather than
     *     pushing into a transient toast) keeps the source of truth
     *     in the store and lets the flash survive component re-mount
     *     with the same event count.
     *
     * Both `compactionEventCount` and `lastCompaction` reset on
     * session change (loadSessionMessages) so a stale figure from
     * a prior session does not bleed into the new one.
     */
    handleContextCompactedEvent(info: {
      sessionId: string
      originalTokens: number
      summaryTokens: number
      /**
       * Phase-5 Slice δ — Trigger discriminant. Empty defaults to ''
       * so the call site that pre-dates the field still works; the
       * chip tooltip falls back to the generic copy in that case.
       */
      trigger?: string
    }): void {
      // Defence in depth: ignore events for inactive sessions. The
      // api server already scopes the SSE wire to the active session
      // so in practice this guard never trips — but a future
      // SSE-multiplexing change must not silently surface another
      // session's compaction on this chip.
      if (info.sessionId !== '' && this.currentSessionId !== info.sessionId) {
        return
      }

      this.compactionEventCount += 1
      this.lastCompaction = {
        originalTokens: info.originalTokens,
        summaryTokens: info.summaryTokens,
        tokensSaved: info.originalTokens - info.summaryTokens,
        at: Date.now(),
        trigger: info.trigger ?? '',
      }
    },

    handleProviderChangedEvent(info: { from?: unknown; to?: unknown; reason?: unknown }): void {
      const to = typeof info.to === 'string' ? info.to : ''
      const from = typeof info.from === 'string' ? info.from : ''
      const reason = typeof info.reason === 'string' ? info.reason : ''

      let newProvider = ''
      let newModel = ''
      if (to.length > 0) {
        const sep = to.indexOf('+')
        if (sep === -1) {
          newProvider = to
        } else {
          newProvider = to.slice(0, sep)
          newModel = to.slice(sep + 1)
        }
        this.currentProviderId = newProvider
        this.currentModelId = newModel
        // Record the transition target so a follow-up model_active event
        // for the same target stays silent. The Go failover hook fires
        // model_active immediately after provider_changed (both target the
        // new provider+model); without this dedup the user sees two
        // back-to-back toasts for one transition.
        this.lastProviderChangeKey = `${newProvider}+${newModel}`
      }

      // Toast copy — keeping the mapping client-side keeps Go releases
      // independent of toast wording. The reason vocabulary is the
      // closed set defined in classifyFailoverReason on the Go side
      // (rate_limited, billing, quota, overload, auth_failure,
      // model_not_found, unavailable, timeout, unknown).
      const newModelLabel = newModel || newProvider || 'a different model'
      const reasonLabel = describeFailoverReason(reason)
      const fromModelLabel = (() => {
        if (!from) return ''
        const sep = from.indexOf('+')
        return sep === -1 ? from : from.slice(sep + 1)
      })()
      const message = fromModelLabel
        ? `Switched to ${newModelLabel} — ${fromModelLabel} is ${reasonLabel}.`
        : `Switched to ${newModelLabel} — primary model is ${reasonLabel}.`

      showToast({
        title: 'Model changed',
        message,
        variant: 'default',
        duration: 6000,
      })
    },

    handleToolCallEvent(info: { name?: unknown; status?: unknown; type?: unknown; input?: unknown }): void {
      const toolName = String(info.name ?? info.type ?? 'unknown')
      const status = String(info.status ?? 'running')
      // Remember the tool name so the next tool_result event can be routed
      // appropriately — the SSE tool_result payload only carries content,
      // not the tool name. This is the seam the todowrite ingestion hooks
      // into below.
      this.lastToolName = toolName

      // ---- Rolling tool-activity toast (May 2026 notifications work) ----
      //
      // The user requested "trigger notifications when tools are triggered".
      // A naive "toast per tool_call" is unusable — multi-tool turns spam
      // the UI with 10+ stacked toasts. Instead we aggregate: the FIRST
      // tool_call of a quiet period spawns ONE persistent toast that
      // updates as subsequent tool_calls arrive, and a 1.2s rolling
      // debounce auto-dismisses it after the burst ends.
      //
      // Wire format note: the brief said "every tool call" — we honour
      // that signal-wise (every tool_call mutates the toast), the
      // aggregation is purely a presentation decision.
      this.recordToolActivity(toolName)

      // Seal any in-flight assistant bubble before recording the tool
      // invocation. Without this, post-tool content chunks reverse-find the
      // pre-tool assistant (still status === 'running') and APPEND, so the
      // user sees a single fused assistant block with all pre/inter/post
      // text collapsed together at the array position of the FIRST chunk —
      // before every tool_result row. The user-visible symptom: "we are
      // seeing a todo list completing, but we don't see any responses
      // between the update." The fix: seal here so the next content chunk
      // creates a new assistant message AFTER the tool_result, preserving
      // chronological order in the rendered thread.
      const inFlight = [...this.messages].reverse().find(
        (message) => message.role === 'assistant' && message.status === 'running',
      )
      if (inFlight) {
        inFlight.status = 'completed'
      }

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

    /**
     * recordToolActivity drives the rolling tool-activity toast. Called
     * from handleToolCallEvent for every tool_call (and skill_load,
     * which the dispatcher folds into the same handler).
     *
     * Behaviour:
     *
     *   - First call of a burst: spawn a "loading"-variant toast (zero
     *     auto-dismiss, accent border) saying "Running command" or the
     *     friendlier verb for the tool. Track the toast id.
     *   - Subsequent calls in the same burst: append the new tool name to
     *     the running list and update the live toast's message via
     *     updateToast — same id, same DOM position, no spawn-and-remove
     *     flicker.
     *   - 1.2s after the LAST call: auto-dismiss. Every new call cancels
     *     and re-arms the timer so the toast lingers as long as tools
     *     keep firing and disappears shortly after they stop.
     *
     * Defensive: if updateToast returns false (the toast was somehow
     * dismissed externally — user closed it via the X), spawn a fresh
     * toast with the accumulated names rather than silently dropping
     * the signal.
     */
    recordToolActivity(toolName: string): void {
      this.toolActivityNames.push(toolName)

      const message = composeToolActivityMessage(this.toolActivityNames)
      const title = 'Working'

      if (this.toolActivityToastId === null) {
        // First tool of a quiet period — spawn the rolling toast.
        // Duration 0 means persistent; we own dismissal via the timer
        // below.
        this.toolActivityToastId = showToast({
          title,
          message,
          variant: 'loading',
          duration: 0,
        })
      } else {
        // Live update — same toast id, replaced copy.
        const ok = updateToast(this.toolActivityToastId, { message })
        if (!ok) {
          // The toast was externally dismissed (user clicked X, or the
          // composable was reset). Recover by spawning a fresh one so
          // the user keeps seeing the activity signal.
          this.toolActivityToastId = showToast({
            title,
            message,
            variant: 'loading',
            duration: 0,
          })
        }
      }

      // Re-arm the rolling auto-dismiss. Every new tool_call resets the
      // 1.2s window so a steady stream of tools keeps the toast alive
      // and the toast disappears soon after the model stops invoking
      // tools — reflecting the real-world "tools are happening / tools
      // stopped" boundary.
      if (this.toolActivityTimer) {
        clearTimeout(this.toolActivityTimer)
      }
      this.toolActivityTimer = setTimeout(() => {
        this.dismissToolActivityToast()
      }, TOOL_ACTIVITY_DISMISS_MS)
    },

    /**
     * dismissToolActivityToast — clean up the rolling toast and reset
     * its state. Called from the rolling auto-dismiss timer and from
     * any teardown path that needs to reset the burst (e.g. session
     * switch, where lingering tool activity from a prior session
     * shouldn't cross the boundary).
     */
    dismissToolActivityToast(): void {
      if (this.toolActivityToastId !== null) {
        dismissToast(this.toolActivityToastId)
        this.toolActivityToastId = null
      }
      this.toolActivityNames = []
      if (this.toolActivityTimer) {
        clearTimeout(this.toolActivityTimer)
        this.toolActivityTimer = null
      }
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
