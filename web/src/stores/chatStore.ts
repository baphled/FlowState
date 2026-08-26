import { defineStore } from 'pinia'
import type { Agent, Message, Model, Session, SessionSummary, Swarm } from '@/types'
import {
  compactSessionNow,
  createSession,
  deleteSession as apiDeleteSession,
  fetchAgents,
  fetchModels,
  fetchSessionMessages,
  fetchSessions,
  fetchSwarms,
  fetchTurn,
  sendSessionMessage,
  truncateSessionMessages,
  updateSessionAgent,
  updateSessionModel,
  updateSessionPermissionMode,
  type TurnState,
} from '@/api'
import { recordStreamEvent } from '@/lib/streamLog'
import { exhaustivenessGuard, parseSSEPayload, type SSEEvent } from '@/lib/sseEvent'
import { dismissToast, showToast, updateToast } from '@/composables/useToast'
import { useTodoStore } from './todoStore'
import { useQuotaStore } from './quotaStore'

const activeSessionStorageKey = 'chat.currentSessionId'
const activeAgentStorageKey = 'chat.agentId'
const activeModelStorageKey = 'chat.selectedModel'
const activeProviderStorageKey = 'chat.selectedProvider'

// Permission Modes (May 2026) — Slice 2 keys the chip's selection under
// `flowstate.permissionMode.<sessionId>` so each session keeps its own
// mode independently. Slice 3 will move the canonical persistence to
// the backend session sidecar; localStorage then becomes an offline
// fallback only.
const permissionModeStorageKeyPrefix = 'flowstate.permissionMode.'

/**
 * PermissionMode — closed vocabulary mirroring `internal/permissionmode`
 * (backend, Slice 1, commit 26f9d864). Underscored `accept_edits` keeps
 * the wire shape identical between the localStorage value and the
 * backend's `PermissionMode` field so Slice 3 can POST the same string.
 *
 * `ask` (ModeAskUser Extension, May 2026 — Slice 1) is the fifth mode:
 * a pathguard-interactive surface where denials publish a
 * `permission_required` event and the suspended tool call resumes once
 * the operator grants per-call / per-session / per-resource access. The
 * frontend wire value is `'ask'` (kebab-style singular, matching the
 * Go enum identifier). v1 chip surface only — engine pause-and-wait
 * lands in Slice 2.
 */
export type PermissionMode = 'plan' | 'default' | 'accept_edits' | 'ask' | 'yolo'

export const PERMISSION_MODES: readonly PermissionMode[] = [
  'plan',
  'default',
  'accept_edits',
  'ask',
  'yolo',
] as const

export const DEFAULT_PERMISSION_MODE: PermissionMode = 'default'

function isPermissionMode(value: unknown): value is PermissionMode {
  return typeof value === 'string'
    && (PERMISSION_MODES as readonly string[]).includes(value)
}

function permissionModeStorageKey(sessionId: string): string {
  return `${permissionModeStorageKeyPrefix}${sessionId}`
}

function getPersistedPermissionMode(sessionId: string): PermissionMode {
  if (typeof window === 'undefined') {
    return DEFAULT_PERMISSION_MODE
  }
  const raw = window.localStorage.getItem(permissionModeStorageKey(sessionId))
  if (raw && isPermissionMode(raw)) {
    return raw
  }
  return DEFAULT_PERMISSION_MODE
}

function persistPermissionMode(sessionId: string, mode: PermissionMode): void {
  if (typeof window === 'undefined') {
    return
  }
  window.localStorage.setItem(permissionModeStorageKey(sessionId), mode)
}

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

// TODO_TOOL_NAMES — the set of tool names whose tool_result content is the
// canonical todo-list JSON the todoStore consumes. Centralised here so the
// live-ingest gate (handleToolResultEvent) and the hydration filter
// (todoStore.hydrateFromMessages) share a single source of truth and a
// future tool rename (e.g. todo_update → todo_patch) is one-line touch
// across both seams. The agent contract today is one `todowrite` (initial
// list) + N × `todo_update` (per-status flip); both return the FULL
// post-patch list in the same {content,status,priority} shape.
//
// See investigation note "Todo Tools UI Render Gaps (May 2026)" for the
// TUI-parity counterpart at internal/tui/intents/chat/intent.go:4740-4748.
export const TODO_TOOL_NAMES: ReadonlySet<string> = new Set(['todowrite', 'todo_update'])

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

// Phase-4-Commit-2 of "Turn-Based Post-Then-Poll Architecture
// (May 2026)" retired the per-session SSE infrastructure that used to
// live here (getSessionStream / disconnectSessionStream /
// disconnectAllSessionStreams + the closure-captured `streams` map +
// the `useSessionStream` composable). The FE now drives live state
// via long-poll on GET /v1/sessions/{id}/turns/{turn_id} — see the
// `pollTurnUntilTerminal` action below.
//
// `__resetSessionStreams` is retained as a no-op test seam so callers
// that imported it (chatStore.test.ts) don't break compilation. It
// can be safely deleted once those imports are pruned.
export function __resetSessionStreams(): void {
  // no-op — retained for backward compat; long-poll has no module-
  // scoped state to reset.
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

// rowsShallowEqual compares two Message objects field-by-field for the
// keys that actually drive UI re-render (content, status, tool counters,
// model attribution, thinking content). Returns true when the incoming
// row has nothing new to say about the existing row, so the caller can
// skip the object spread + array-slot reassignment that would otherwise
// trigger Vue's reactivity for zero observable change.
//
// Scoped to the union of fields the Turn endpoint emits + the fields
// the chat UI binds to. Conservative on the "different" side: any
// missing field on `incoming` that differs from `existing` (e.g.
// `incoming.toolCalls === undefined` vs `existing.toolCalls === 3`) is
// treated as "different" so we don't accidentally erase state via a
// short-circuit. The Turn endpoint's row payloads carry every field
// they care about, so this branch is largely a defence-in-depth.
//
// thinkingBlocks is compared by reference: the engine emits a NEW
// array when it grows, so reference equality is sufficient and avoids
// a deep walk on every poll.
function rowsShallowEqual(existing: Message, incoming: Partial<Message>): boolean {
  const keys: (keyof Message)[] = [
    'content',
    'status',
    'role',
    'toolCalls',
    'lastTool',
    'targetAgent',
    'chainId',
    'modelName',
    'providerName',
    'thinkingContent',
    'thinkingBlocks',
    'stopReason',
    'toolName',
    'toolInput',
    'agentId',
    'timestamp',
  ]
  for (const k of keys) {
    if (k in incoming && existing[k] !== incoming[k]) {
      return false
    }
  }
  return true
}

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
    // Permission Modes (May 2026) — Slice 2 (localStorage-only).
    //
    // The active session's permissioning dial, displayed by the
    // PermissionModeChip in the composer toolbar. Default mirrors the
    // backend constant `permissionmode.ModeDefault` (Slice 1) so the
    // chip's first render of a fresh session never disagrees with what
    // the engine would compute for an unset field.
    //
    // Persistence in this slice is localStorage only, keyed per session
    // under `flowstate.permissionMode.<sessionId>` — see
    // `permissionModeStorageKeyPrefix`. Slice 3 moves the canonical
    // persistence to the backend session sidecar and POSTs the value;
    // this slice deliberately stops at the wire.
    //
    // Hydration is wired through every session-change entry-point
    // (restoreStateFromBackend, loadSessionMessages, newSession) so the
    // chip never bleeds the previous session's value across the gap.
    permissionMode: DEFAULT_PERMISSION_MODE as PermissionMode,
    // chainSessions — Bug Hunt (May 2026) sibling-confusion fix for the
    // in-thread delegation card.
    //
    // The persisted `delegation` / `delegation_started` Message carries
    // `targetAgent` + `chainId` but NOT a child session id (see
    // session.Message in internal/session/manager.go — the wire shape
    // intentionally omits ChildSessionID because the DelegationEvent
    // payload was originally TUI-only). MessageBubble's "click the agent
    // name" affordance therefore could not resolve the click to a unique
    // session when a parent delegated to the same agent more than once:
    // both cards shared `targetAgent`, the resolver fell back to
    // "most-recent child", and the EARLIER card silently opened the
    // LATER sibling. Sibling confusion, click A see B.
    //
    // The SwarmEvent stream (consumed by swarmStore) does carry the
    // child session id alongside the chain id — every `delegation`
    // SwarmEvent fired by the engine has `metadata.child_session_id`
    // and `id === chainId`. swarmStore.ingestEventLine records the
    // (chainId → childSessionId) mapping into this map as events flow,
    // and loadSessionForDelegation prefers it over the agent-id
    // fallback. The resolver only falls back when the chainId is
    // unknown to the map (e.g. on a hard reload before the live swarm
    // stream has reconnected, since FlowState does not replay swarm
    // events on reconnect).
    //
    // Latent surface (flagged): the reload-before-swarm-reconnect
    // window still routes by agent-id and inherits the original
    // most-recent-wins behaviour. Closing that requires either
    // SwarmEvent replay on `/swarm/events` connect or carrying
    // `target_session_id` on the persisted `delegation` Message. Both
    // are backend changes; this commit closes the live-click path.
    chainSessions: {} as Record<string, string>,
    // Per-session streaming state (Slice A — Streaming Coherence May 2026).
    //
    // Pre-slice the store carried flat global `isLoading` / `isStreaming`
    // booleans. That conflated all sessions onto a single in-flight slot
    // — composing in session B was blocked while session A streamed,
    // because the composer's submit gate read the global flag. The fix:
    // a per-session record keyed by session id with an isolated slot for
    // each, and `streamingFor(sessionId)` / `setSessionStreaming` as the
    // canonical access surface.
    //
    // Legacy `isLoading` / `isStreaming` remain as state fields that
    // shadow the active session's slot (`setSessionStreaming` updates
    // both when sessionId === currentSessionId). This preserves
    // backwards-compatible writes (`store.isLoading = true` on test
    // setup) and reads while the per-session map carries the
    // multi-session truth. New consumers should prefer
    // `streamingFor(sessionId)` so per-session isolation is observable.
    sessionStreaming: {} as Record<string, { isLoading: boolean; isStreaming: boolean }>,
    // Per-session queued prompts (Slice E — Streaming Coherence May 2026).
    //
    // Pre-slice submit-while-streaming was rejected with a toast ("Send
    // blocked: an earlier message is still in flight"). The new contract:
    // submit-while-streaming pushes the prompt onto the session's queue;
    // when the outer turn completes (handleStreamDone equivalent in the
    // send finally block), the next queued prompt is auto-submitted.
    //
    // Per-session keying so cross-session composition does not interfere.
    // Pinia reactivity proxies the record so the QueuedPromptStrip watcher
    // re-renders when slots change.
    queuedPrompts: {} as Record<string, string[]>,
    // Per-session streaming phase (Slice F — Streaming Coherence May 2026).
    //
    // The engine emits `streaming.heartbeat` events carrying a `phase`
    // discriminant ("generating" | "thinking" | "tool_executing" |
    // "queued") that the watchdog reads to pick a per-phase threshold:
    //
    //   generating     →  45s
    //   thinking       → 120s
    //   tool_executing → 180s
    //   queued         → 300s
    //
    // Falls back to the legacy 60s flat threshold when the phase is
    // empty / unrecognised. Stored per-session so a stalled session A
    // does not borrow session B's longer threshold.
    streamingPhase: {} as Record<string, string>,
    // UI Parity PR5 — Live token counter (May 2026).
    //
    // tokenCountBySession records the in-flight turn's cumulative
    // output_tokens per session, projected onto the streaming chrome
    // as "1,247 tokens" next to the working-on label. Populated on
    // every streaming.heartbeat tick from event.tokenCount. Zero is
    // the legitimate pre-first-UsageDelta value; the ChatView counter
    // renderer gates on >0 so a fresh turn does not flash "0 tokens"
    // until the provider's first message_delta arrives.
    //
    // tokensPerSecondBySession holds the latest computed t/s rate
    // from the delta between consecutive heartbeats at the documented
    // 15s engine cadence. ChatView renders "· 42 t/s" trailing only
    // when the value is positive; the first tick of a turn has no
    // predecessor so the rate stays 0 and the trailing segment is
    // suppressed.
    //
    // lastHeartbeatAtBySession is the timestamp of the previous tick
    // (ms since epoch), used as the basis for the Δseconds computation.
    // Cleared on session-change to prevent a stale anchor from a prior
    // turn producing a misleading rate on the next tick.
    tokenCountBySession: {} as Record<string, number>,
    tokensPerSecondBySession: {} as Record<string, number>,
    lastHeartbeatAtBySession: {} as Record<string, number>,
    // Slice G — Escape-twice cancel cascade (Streaming Coherence May 2026).
    // Tracks escape press count and timeout for the 600ms chord window.
    escapePressCount: 0,
    escapeTimeoutId: null as ReturnType<typeof setTimeout> | null,
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
    // contextUsageBySession — Bug Hunt (May 2026) per-session context-
    // usage isolation. Pre-fix `currentContextUsage` was a single flat
    // slot; an SSE `context_usage` event landing for session A while
    // the user viewed B either bled A's figure onto B's chip (if the
    // C-3 chunk guard let it through) or was silently dropped, and
    // returning to A blanked the chip until the next emission.
    //
    // The map is keyed by the chunk's capturedSessionId so each
    // session keeps its own most-recent figure. applyContentEvent
    // routes context_usage into the map regardless of whether the
    // session is currently active (the figure is metadata bound to
    // its session, not the active view); loadSessionMessages reads
    // the map on session change so returning to a session shows its
    // last figure rather than `—/—`. `currentContextUsage` continues
    // to track the ACTIVE session's view for the chip to read
    // directly without subscribing to the map.
    contextUsageBySession: {} as Record<string, {
      inputTokens: number
      outputReserve: number
      limit: number
      percentage: number
    }>,
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
    // promptHistoryBySession backs up-arrow recall in MessageInput (UI Parity
    // PR2 B4, May 2026; per-session privacy fix May 2026 bug-fix bundle).
    // Each successful sendMessage pushes the user's text onto this session's
    // ring buffer; the composer's ArrowUp/ArrowDown handlers walk it when the
    // textarea is empty or the caret sits at the buffer's start/end edge so
    // the user can re-run / edit a recent prompt without re-typing.
    //
    // Privacy: pre-fix this was a flat singleton `string[]`. A prompt typed
    // in session A leaked into session B's ArrowUp recall — a real
    // privacy concern during screen-shares (e.g. "API key sk-..."). The
    // map shape isolates history per session id so switching sessions
    // hides the prior session's history without losing it.
    //
    // Capped at 50 entries PER SESSION — the TUI uses the same ceiling.
    // Newest entry is at the END of the array (push semantics); the
    // composer steps from end-to-start on ArrowUp.
    promptHistoryBySession: {} as Record<string, string[]>,
    // promptHistoryLegacy backs the legacy flat-shape contract for the
    // null-currentSessionId fast path (pre-session-create sends from the
    // App-level mount). The map-shape would be empty for any caller that
    // has not yet minted a session id; preserving a flat fallback means
    // those callers still get history-aware behaviour. Once a session id
    // is set this slot is no longer the source of truth — promptHistory
    // (the getter) resolves to `promptHistoryBySession[currentSessionId]`.
    promptHistoryLegacy: [] as string[],
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
    // lastContextUsageKey is Phase-5 §1c-β's idempotency gate for
    // handleContextUsageEvent. The transitional 1c-β state has TWO
    // callers for the same context_usage figure — the SSE branch at
    // chatStore.ts:2795-2806 and the new poll-diff caller in
    // pollTurnUntilTerminal. A double-fire for the same (inputTokens,
    // outputReserve, limit, percentage) tuple MUST NOT mutate state
    // twice (the chip's reactivity would re-render; the per-session
    // map would re-write with an identical figure causing a needless
    // proxy mutation Vue's `ref` watchers observe).
    //
    // The key is a `<inputTokens>:<outputReserve>:<limit>:<percentage>`
    // string serialised at handler entry. A matching key short-circuits
    // the mutation; a differing key proceeds and updates the gate. Same
    // pattern as `lastProviderChangeKey` above for the failover toast
    // dedup. Per-session keyed inside the contextUsageBySession map
    // would be heavier — the chip reads currentContextUsage off the
    // active session, so a single gate covers the FE's observable
    // semantic.
    //
    // Cleared on session change so a fresh session's first figure
    // is never suppressed by the prior session's last figure.
    lastContextUsageKey: null as string | null,
    // lastCompactionEventKey is Phase-5 §1c-γ's idempotency gate for
    // handleContextCompactedEvent. The transitional 1c-γ state has TWO
    // callers for the same compaction figure — the SSE branch at
    // chatStore.ts:~2954 and the new poll-diff caller in
    // pollTurnUntilTerminal. A double-fire for the same
    // (originalTokens, summaryTokens, latencyMs, trigger) tuple MUST
    // NOT increment compactionEventCount twice — the chip's flash
    // watcher observes count-deltas, so a duplicate fire would flash
    // twice for one event.
    //
    // Same pattern as `lastContextUsageKey` / `lastProviderChangeKey`.
    // Cleared on session change so a fresh session's first compaction
    // is never suppressed by the prior session's last figure.
    lastCompactionEventKey: null as string | null,
    // lastGateFailureKey is Phase-5 §1c-γ's idempotency gate for the
    // gate_failed dispatch. Same transitional double-caller story —
    // SSE branch + poll-diff both write `lastGateFailure`; the key
    // gates the second writer when it sees the same payload.
    //
    // Key shape: `<swarmId>:<gateName>:<reason>:<cause>` — the four
    // fields together uniquely identify a halt within a session.
    // Cleared on session change.
    lastGateFailureKey: null as string | null,
    // lastCriticalErrorCorrelationId is Phase-5 §1c-γ's idempotency
    // gate for the stream_critical / poll-diff critical_error
    // transition. Mirrors the brief's prescribed key shape: the
    // correlation_id IS the unique fingerprint of a critical error
    // (the dispatcher mints 8 random bytes hex-encoded per stamp; the
    // SSE writer's clientError uses the same shape). A re-fire with
    // the same correlation_id must NOT overwrite `criticalError`
    // (would re-show the banner the user dismissed); a different
    // correlation_id MUST proceed (a fresh fatal error must replace
    // the prior banner per the SSE handler's existing overwrite policy).
    //
    // Cleared on session change so a fresh session can re-render a
    // critical event with the SAME id as a prior session's last one
    // (extremely unlikely, but the dedup must be per-session scoped).
    lastCriticalErrorCorrelationId: null as string | null,
    // Sketch A — UI Delegation Chain Not Updating (May 2026) Issue B.A.
    //
    // Set of `delegation_started` message ids the active session's
    // pollTurnUntilTerminal loop has ALREADY observed and used to
    // trigger a `loadSessions()` refresh. Functions as a per-row debounce
    // so the same row, present across every subsequent poll tick while
    // the child runs, doesn't hammer `GET /api/v1/sessions`.
    //
    // Lifecycle:
    //   - Add: on the FIRST poll iteration that surfaces a row whose
    //     id is not yet present (and whose role === 'delegation_started').
    //   - Read: on every subsequent merge iteration to decide whether
    //     to skip the refresh fan-out.
    //   - Clear: on session change in `loadSessionMessages`. The seen
    //     set is per-session: the next session's poll loop starts with
    //     an empty set so its first delegation_started row fires a
    //     fresh refresh even if a prior session's row carried the
    //     same id (vanishingly unlikely in practice, but the dedup must
    //     be per-session scoped — mirrors the lastCompactionEventKey /
    //     lastGateFailureKey / lastCriticalErrorCorrelationId pattern
    //     at chatStore.ts:1520-1522).
    //
    // Why a Set<string> rather than a per-session Record: scope is the
    // active session for the duration of its poll. Cross-session
    // composition is supported by the per-session-singleton SSE / poll
    // architecture (Slice B), but the loadSessions refresh fans out the
    // full session list to all subscribers anyway, so a single
    // top-level Set is the lightest correct primitive.
    seenDelegationStartedIds: new Set<string>() as Set<string>,
    // Bug fix — Child session "Live" indicator goes stale (May 2026).
    //
    // Symmetric counterpart to `seenDelegationStartedIds`. The
    // `delegation_started` row above triggers a `loadSessions()`
    // refresh so the child entry APPEARS in ChildSessionsPanel; this
    // set drives the same refresh on the TERMINAL copy of the row
    // (role === 'delegation', emitted by the engine's accumulator
    // when the child finishes — accumulator.go:177-192 / turn.go:699
    // — and dispatched into the messages array by the same Turn
    // poll). Without it the panel's "Live" pulsing-green chip never
    // flips back to idle because the parent's session list is never
    // re-fetched to observe the child's status moving from
    // 'running' to 'completed' / 'errored'.
    //
    // Lifecycle / scope is identical to the started-set above:
    //   - Add: first poll iteration that surfaces a row whose id
    //     is not yet present and whose role === 'delegation'.
    //   - Read: every subsequent merge iteration, to skip the
    //     refresh fan-out (a snapshot can carry the same terminal
    //     row across every subsequent poll while the parent turn
    //     keeps running).
    //   - Clear: on session change in `loadSessionMessages` — the
    //     dedup is per-session, mirroring the started-set's clear
    //     at chatStore.ts:1573 and the wider per-session reset
    //     pattern (lastCompactionEventKey / lastGateFailureKey /
    //     lastCriticalErrorCorrelationId).
    //
    // Independent of `seenDelegationStartedIds`: a single poll that
    // carries both a NEW started row and a NEW terminal row fires
    // TWO refreshes (one each). That's intentional — they're
    // separate panel transitions (new chip appears; existing chip
    // flips to idle) and the panel needs the session list
    // reconciled for each.
    seenDelegationCompletedIds: new Set<string>() as Set<string>,
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
    // UI Parity PR6 — Collapse all / Expand all override (May 2026).
    //
    // Per-card open state lives in ToolBubble's local isOpen ref. The
    // override here lets a parent (e.g. the ChatView toolbar) bulk-flip
    // every ToolBubble without lifting per-card state into the store.
    //
    //   'auto'      — default; ToolBubble uses its local isOpen ref.
    //   'expanded'  — every ToolBubble forces open regardless of local state.
    //   'collapsed' — every ToolBubble forces closed regardless of local state.
    //
    // Flipping back to 'auto' restores per-card behaviour so the user can
    // resume granular control. Transient — never persisted (the override is
    // session-level UX, not session metadata).
    toolCardOpenOverride: 'auto' as 'auto' | 'expanded' | 'collapsed',
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

    // orderedSessions: the canonical list ordering used by every session-list
    // surface (SessionBrowser cards, SessionSwitcher dropdown rows). The
    // contract is two-tier:
    //   1. Actively-streaming sessions float to the top — the
    //      streamingFor(id).isStreaming slot is the source of truth (the
    //      same field every per-row "Live" affordance already reads).
    //   2. Within each tier, sort by updatedAt descending so the
    //      most-recent activity surfaces first.
    //
    // Returns a NEW array — never mutates state.sessions. Computed-style
    // derivation so per-row reactivity tracks both the membership and the
    // streaming map.
    orderedSessions(state): SessionSummary[] {
      const streamingMap = state.sessionStreaming
      const isStreaming = (id: string): boolean =>
        streamingMap[id]?.isStreaming === true
      return [...state.sessions].sort((a, b) => {
        const aStream = isStreaming(a.id) ? 1 : 0
        const bStream = isStreaming(b.id) ? 1 : 0
        if (aStream !== bStream) return bStream - aStream
        return new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime()
      })
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

    // streamingFor (Slice A — Streaming Coherence May 2026) — per-session
    // streaming-state lookup. Returns a fresh `{isLoading: false, isStreaming:
    // false}` for sessions with no slot so callers do not need to guard for
    // missing entries. Components that compose in session B while session A
    // is streaming MUST read this rather than the legacy `isLoading` flag —
    // pre-slice the flat flag conflated all sessions on a single in-flight
    // slot and bounced session B's send.
    streamingFor(state) {
      return (sessionId: string | null | undefined): { isLoading: boolean; isStreaming: boolean } => {
        if (!sessionId) return { isLoading: state.isLoading, isStreaming: state.isStreaming }
        const slot = state.sessionStreaming[sessionId]
        return slot ? { isLoading: slot.isLoading, isStreaming: slot.isStreaming } : { isLoading: false, isStreaming: false }
      }
    },

    // UI Parity bug-fix bundle (May 2026). P1-4: per-session prompt-history
    // getter. Resolves to the active session's history when a session is
    // current; falls back to the legacy flat list when no session id is set
    // (pre-session-create fast path). MessageInput reads this as
    // `store.promptHistory` so the component layer is unchanged.
    promptHistory(state): string[] {
      if (state.currentSessionId) {
        return state.promptHistoryBySession[state.currentSessionId] ?? []
      }
      return state.promptHistoryLegacy
    },
  },

  actions: {
    // setSessionStreaming (Slice A — Streaming Coherence May 2026) — the
    // canonical mutator for the per-session streaming-state record. Patches
    // the named session's slot, creating it if absent. Pinia's reactivity
    // proxies the record so component getters re-evaluate when a slot
    // changes.
    //
    // Mirroring contract: when sessionId matches currentSessionId (the
    // user-visible active session) the legacy flat `isLoading` /
    // `isStreaming` fields are also synced. Components that have not yet
    // been migrated to `streamingFor(sessionId)` keep working unchanged
    // for the active-session view. When sessionId differs from
    // currentSessionId (the cross-session non-blocking case), the flat
    // fields are intentionally left alone — that is the whole point of
    // the per-session record.
    //
    // A no-op when sessionId is empty (defensive — the pre-session-create
    // first-send branch passes null before the createSession round-trip
    // returns).
    setSessionStreaming(
      sessionId: string | null,
      patch: Partial<{ isLoading: boolean; isStreaming: boolean }>,
    ): void {
      if (!sessionId) {
        // No session yet — the pre-session-create fast path. Mirror to the
        // flat fields so the legacy gate / indicator behaviour is intact.
        if (patch.isLoading !== undefined) this.isLoading = patch.isLoading
        if (patch.isStreaming !== undefined) this.isStreaming = patch.isStreaming
        return
      }
      const prior = this.sessionStreaming[sessionId] ?? { isLoading: false, isStreaming: false }
      const next = {
        isLoading: patch.isLoading !== undefined ? patch.isLoading : prior.isLoading,
        isStreaming: patch.isStreaming !== undefined ? patch.isStreaming : prior.isStreaming,
      }
      this.sessionStreaming[sessionId] = next
      if (sessionId === this.currentSessionId) {
        this.isLoading = next.isLoading
        this.isStreaming = next.isStreaming
      }
    },

    // clearSessionStreaming — drop the slot entirely. Used when a session
    // is removed; otherwise prefer `setSessionStreaming(id, {isLoading: false,
    // isStreaming: false})` to retain the slot for late-arriving events.
    clearSessionStreaming(sessionId: string | null): void {
      if (!sessionId) return
      delete this.sessionStreaming[sessionId]
    },

    // queuePromptFor (Slice E — Streaming Coherence May 2026) — push a
    // prompt onto the named session's queue. The QueuedPromptStrip
    // watcher re-renders when the slot changes.
    queuePromptFor(sessionId: string | null, text: string): void {
      if (!sessionId || !text) return
      const existing = this.queuedPrompts[sessionId] ?? []
      this.queuedPrompts[sessionId] = [...existing, text]
    },

    // popQueuedPromptFor (Slice E) — remove a queued prompt at the
    // given index and return its text. Used by the strip's X click to
    // revert + edit-then-resend (mirrors revertToMessage's edit pattern).
    popQueuedPromptFor(sessionId: string | null, index: number): string | null {
      if (!sessionId) return null
      const existing = this.queuedPrompts[sessionId] ?? []
      if (index < 0 || index >= existing.length) return null
      const removed = existing[index]
      this.queuedPrompts[sessionId] = existing.filter((_, i) => i !== index)
      return removed
    },

    // shiftQueuedPromptFor (Slice E) — pop the head of the queue,
    // returning the prompt or null when empty. Called by the
    // post-stream-completion auto-submit path inside sendMessage's
    // finally block.
    shiftQueuedPromptFor(sessionId: string | null): string | null {
      if (!sessionId) return null
      const existing = this.queuedPrompts[sessionId] ?? []
      if (existing.length === 0) return null
      const head = existing[0]
      this.queuedPrompts[sessionId] = existing.slice(1)
      return head
    },

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
        // Permission Modes (May 2026) — Slice 3 hydration on the
        // alternate-agent branch of restoreStateFromBackend. Backend
        // value wins over localStorage per the canonical precedence
        // (see hydratePermissionMode JSDoc); when the summary omits
        // the field (legacy sessions persisted before Slice 1) the
        // helper falls back to localStorage and then "default".
        this.hydratePermissionMode(
          sessionForAgent.id,
          sessionForAgent.permissionMode,
        )
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
      // Permission Modes (May 2026) — Slice 3 hydration on the
      // session-matches-agent branch of restoreStateFromBackend.
      // Symmetric with the alternate-agent branch above — backend
      // value (when present on the summary) wins, with localStorage
      // as the offline-boot fall-back.
      this.hydratePermissionMode(session.id, session.permissionMode)
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

    // maybeReattachStream — Phase-4-Commit-2 successor to the retired
    // SSE-reattach path. When the user navigates back to a session
    // whose Turn is still Running (engine produced chunks while the
    // user was elsewhere), we hand the active turn_id to
    // pollTurnUntilTerminal so the long-poll surfaces the in-flight
    // chunks. When no turn is active OR the snapshot says the session
    // is idle, this is a no-op.
    //
    // The session summary's `activeTurnId` field (populated by
    // handleListV1Sessions when the Turn registry has a Running entry
    // for the session — see internal/api/server.go) is the canonical
    // signal. `backendStreaming` is the legacy `isStreaming` flag,
    // which post-Commit-2 mirrors `activeTurnId != ""`.
    maybeReattachStream(sessionId: string, backendStreaming = false): void {
      if (!sessionId) return

      // Look up activeTurnId for the session. The summaries[] list is
      // populated by loadSessions on app boot and refreshed by
      // reconcileFromBackend; the activeTurnId field lives on each
      // SessionSummary.
      const summary = this.sessions.find((s) => s.id === sessionId)
      const activeTurnId = summary?.activeTurnId ?? ''
      if (!activeTurnId) {
        // No active turn registered — nothing to reattach. The legacy
        // message-heuristic ("last message is user / status=running")
        // is no longer relevant because the Turn registry is the
        // canonical "is this session mid-stream?" signal.
        return
      }
      // Defence-in-depth: the backendStreaming flag is also derived
      // from activeTurnId server-side, so the two should agree. If
      // they disagree the activeTurnId wins.
      void backendStreaming

      this.setSessionStreaming(sessionId, { isLoading: true, isStreaming: true })
      // pollTurnUntilTerminal awaits internally; we fire-and-forget
      // because the action's own finally block restores the per-
      // session gate. A session switch concurrent with this poll is
      // safe — the action re-checks currentSessionId at each iteration.
      void this.pollTurnUntilTerminal(sessionId, activeTurnId).finally(() => {
        this.setSessionStreaming(sessionId, { isLoading: false, isStreaming: false })
        void this.reconcileFromBackend(sessionId)
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

    async setAgent(agentId: string, opts?: { sessionId?: string }): Promise<void> {
      const previousAgentId = this.agentId
      this.agentId = agentId
      persistAgentId(agentId)

      // Click-click race fix (May 2026 bug-hunt round 7) — snapshot
      // the target session at call-time. Pre-fix this read
      // `this.currentSessionId` synchronously at the updateSessionAgent
      // arg-evaluation step, which was correct for PATCH targeting
      // (JavaScript evaluates the property before the await yields)
      // BUT the post-await applyContextUsageFromSession ran against
      // `this.currentSessionId` at resolve-time — which a racing
      // second click had already mutated. Threading the explicit
      // sessionId closes that gap: we know which session this PATCH
      // was issued for and can gate the chip update on it.
      //
      // The optional shape preserves the existing call sites that
      // (legitimately) treat setAgent as "update the active
      // session's agent" — chatStore.ts:1885 (loadSessionMessages)
      // threads the resolved sessionId explicitly; user-driven calls
      // from the agent picker still fall back to currentSessionId.
      const targetSessionId = opts?.sessionId ?? this.currentSessionId

      if (!agentId || !targetSessionId || agentId === previousAgentId) {
        return
      }

      try {
        const updated = await updateSessionAgent(targetSessionId, agentId)
        // Phase 3 — TUI-cadence parity. The PATCH response carries
        // the engine's fresh context_usage shape so the chip ticks
        // up to reflect the new agent's preferred model / context
        // limit without waiting for the next pre-send.
        //
        // Click-click race fix — only apply the context_usage to the
        // active chip when the target session is still the active
        // one. A slow PATCH for session A resolving after the user
        // has navigated to session B must NOT smear A's tokens onto
        // B's chip. The figure still lands in contextUsageBySession
        // via the capturedSessionId path so returning to A re-hydrates
        // its chip correctly.
        this.applyContextUsageFromSession(updated, targetSessionId)
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
     * Permission Modes (May 2026) — Slice 3 setter.
     *
     * Writes the chip's selection to:
     *   1. In-memory store state — the chip reflects the change
     *      optimistically before the network round-trip.
     *   2. The backend POST `/api/v1/sessions/{id}/permission-mode`
     *      so the value survives a process restart.
     *   3. Per-session localStorage — the offline boot fall-back so
     *      a hard reload before the backend list endpoint responds
     *      still renders the chip in the user's last-chosen mode.
     *
     * Graceful degradation: a backend POST failure (4xx/5xx, network
     * outage, missing session id) does NOT block the optimistic local
     * update or the localStorage write. Rationale: the chip's job is
     * to feel responsive; a transient outage shouldn't lock the user
     * out of changing modes for the rest of the session, and the
     * next backend round-trip (or the next reload's hydration via
     * the session-list payload) will reconcile state. The error is
     * stamped on `this.error` so any error banner picks it up but no
     * rollback happens.
     *
     * The value is validated against the closed-vocabulary
     * `PERMISSION_MODES` tuple — an invalid string (manual edit,
     * schema drift, stray test input) is a no-op rather than
     * corrupting the slot.
     */
    async setPermissionMode(mode: PermissionMode): Promise<void> {
      if (!isPermissionMode(mode)) {
        return
      }
      this.permissionMode = mode
      const sessionId = this.currentSessionId
      if (!sessionId) {
        return
      }
      // localStorage write first — even if the backend POST fails the
      // offline-boot fall-back still reflects the user's selection on
      // the next reload, until the session-list payload reconciles it.
      persistPermissionMode(sessionId, mode)
      try {
        await updateSessionPermissionMode(sessionId, mode)
      } catch (error) {
        // Graceful degradation: log on the error slot but do NOT roll
        // back the optimistic local update or the localStorage write.
        // The next cold load will reconcile against the backend's
        // canonical value via restoreStateFromBackend.
        this.error =
          error instanceof Error
            ? error.message
            : 'Failed to update permission mode'
      }
    },

    /**
     * Permission Modes (May 2026) — Slice 3 hydration helper.
     *
     * Adopt the per-session mode using the canonical precedence:
     *   1. Backend payload (`session.permissionMode` from the session
     *      list / single-session GET / POST response).
     *   2. localStorage (offline boot fall-back).
     *   3. Default ("default") — both above absent.
     *
     * `backendValue` is optional so call sites that don't have the
     * backend payload in hand (legacy code path, transitional callers)
     * can still trigger localStorage-only hydration by passing
     * `undefined`. When `backendValue` is one of the four canonical
     * modes the backend always wins, even when localStorage holds a
     * different value — the backend is the canonical store and a
     * stale localStorage entry MUST NOT override a known-fresh
     * server-side value.
     *
     * Called from every session-change entry-point
     * (loadSessionMessages, newSession, restoreStateFromBackend) so
     * the same hydration rule fires regardless of which path the user
     * took to land on the session.
     */
    hydratePermissionMode(sessionId: string, backendValue?: string): void {
      if (backendValue && isPermissionMode(backendValue)) {
        this.permissionMode = backendValue
        // Reconcile localStorage so a subsequent offline boot reflects
        // the backend's view rather than a stale prior selection.
        persistPermissionMode(sessionId, backendValue)
        return
      }
      this.permissionMode = getPersistedPermissionMode(sessionId)
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
    applyContextUsageFromSession(
      session: { contextUsage?: Session['contextUsage'] },
      capturedSessionId?: string,
    ): void {
      const cu = session.contextUsage
      if (!cu) {
        return
      }
      // capturedSessionId — the session this PATCH was issued for at
      // call-time. handleContextUsageEvent uses it to decide whether
      // the figure may update the ACTIVE chip slot (current session
      // matches) or only the per-session map (cross-session figure
      // for a backgrounded session). Closes the click-click race
      // where a slow PATCH for session A resolving after navigation
      // to B used to overwrite B's chip with A's figure.
      this.handleContextUsageEvent({
        inputTokens: cu.input_tokens,
        outputReserve: cu.output_reserve,
        limit: cu.limit,
        percentage: cu.percentage,
      }, capturedSessionId)
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
        // Rebuild the (chainId → childSessionId) map from the persisted
        // session list so the inline-card click resolver works on cold
        // reload — without this the runtime map is empty (FlowState does
        // not replay swarm events on reconnect) and the sibling-confusion
        // bug a488b858 closed for live clicks re-appears on every page
        // reload. Each delegated session carries its chainId in the wire
        // shape (Summary.chainId, stamped on the backend by
        // CreateWithParentAndChain). We do NOT clear the existing map
        // first — live SwarmEvent ingestion may have populated entries
        // not yet present in the loaded list (e.g. a delegation that
        // hasn't been persisted yet), and clobbering them would re-open
        // the bug for the very window the backfill is supposed to
        // protect. Empty chainId is skipped (root sessions).
        for (const summary of this.sessions) {
          if (summary.chainId && summary.id) {
            this.chainSessions[summary.chainId] = summary.id
          }
        }
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

    /**
     * deleteSession removes a session via DELETE /api/v1/sessions/{id} and
     * applies an optimistic local-state update so the UI reflects the change
     * immediately. Backs SessionBrowser / SessionSwitcher trash buttons.
     *
     * Behaviour:
     *  - On success: drop the session from `sessions`, clear per-session
     *    streaming slot, prune queuedPrompts/streamingPhase entries.
     *  - When the deleted session is the current one: roll the active
     *    session forward to the most-recently-updated remaining session,
     *    or null when the list is empty (callers route to an empty state).
     *  - On HTTP failure: rethrow so the caller can show a toast / rewind
     *    its optimistic UI. We do NOT mutate local state until the server
     *    confirms — a stale 404 (concurrent delete from another tab) is
     *    handled by the caller falling through to a `loadSessions()`
     *    reconcile.
     *
     * Closes Quick-wins QW-11.
     */
    async deleteSession(sessionId: string): Promise<void> {
      await apiDeleteSession(sessionId)

      // Local prune AFTER the server confirms — avoids torn state on
      // network failure / 404.
      const wasCurrent = this.currentSessionId === sessionId

      // User bug (May 2026 — "delete cascade"): the backend cascades
      // delete to every delegated descendant. Mirror that cascade in the
      // local cache so stale child summaries don't linger in
      // `this.sessions` (where `lastDelegatedSessionId` /
      // `ChildSessionsPanel` would still find them). DFS walk over the
      // local parentId chain — bounded by `this.sessions.length`
      // iterations so a malformed parentId cycle is safe.
      const removed = new Set<string>([sessionId])
      let grew = true
      while (grew) {
        grew = false
        for (const s of this.sessions) {
          if (s.parentId && removed.has(s.parentId) && !removed.has(s.id)) {
            removed.add(s.id)
            grew = true
          }
        }
      }
      const removedIds = removed
      this.sessions = this.sessions.filter((s) => !removedIds.has(s.id))
      // Use Pinia-reactive patches so the QueuedPromptStrip watcher /
      // streaming watchers re-run when their per-session slots disappear.
      // Iterate over the full cascade set so descendant streaming /
      // queuedPrompts / phase slots are dropped too (matching the
      // backend cascade — otherwise a re-created sibling with the same
      // id would inherit stale phase state).
      const pruneMap = <T>(map: Record<string, T>): Record<string, T> => {
        let touched = false
        const next = { ...map }
        for (const id of removedIds) {
          if (id in next) {
            delete next[id]
            touched = true
          }
        }
        return touched ? next : map
      }
      this.sessionStreaming = pruneMap(this.sessionStreaming)
      this.queuedPrompts = pruneMap(this.queuedPrompts)
      this.streamingPhase = pruneMap(this.streamingPhase)
      // UI Parity PR5 (May 2026) — prune per-session token counter
      // state on session delete to match streamingPhase / sessionStreaming
      // pruning above. Without this, a deleted session's last
      // counter / rate stays in the map indefinitely.
      this.tokenCountBySession = pruneMap(this.tokenCountBySession)
      this.tokensPerSecondBySession = pruneMap(this.tokensPerSecondBySession)
      this.lastHeartbeatAtBySession = pruneMap(this.lastHeartbeatAtBySession)
      if (wasCurrent) {
        // Roll forward to the most-recently-updated remaining ROOT session
        // (children are unreachable without a parent in the UI). Empty
        // list → leave currentSessionId null; ChatView handles the empty
        // state.
        const candidates = this.sessions.filter((s) => !s.parentId)
        if (candidates.length === 0) {
          this.currentSessionId = null
        } else {
          const next = [...candidates].sort(
            (a, b) =>
              new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime(),
          )[0]
          this.currentSessionId = next.id
          // Best-effort hydrate of the new active session's messages —
          // fire-and-forget; the user is already looking at the empty
          // composer state by this point and the message pane fills in
          // as the request resolves.
          void this.loadSessionMessages(next.id)
        }
      }
    },

    // resetSessionScopedState — shared reset surface for every
    // session-change entry-point. Pre-fix (May 2026 live bleed) the reset
    // block lived inline in `loadSessionMessages` and `newSession`
    // duplicated none of it, leaving every session-scoped dedup gate,
    // counter, banner, and per-session heartbeat slot leaking from the
    // prior session into the new one when the user clicked
    // "New Session" in SessionSwitcher.vue. The architectural fix —
    // extract once, route both entry-points through it — guarantees
    // future session-scoped state additions cannot drift across the two
    // paths.
    //
    // Each slice cleared here has documentation adjacent to its
    // declaration stating "Cleared on session change"; this method is
    // that contract's single implementation. Order matches the original
    // loadSessionMessages reset block for review continuity.
    //
    // Caller responsibilities (NOT in this helper):
    //   - setSessionStreaming(sessionId, { isLoading: true }) — only
    //     loadSessionMessages needs the loading flash because newSession
    //     synchronously creates a fresh session-shell.
    //   - currentSessionId assignment + persistSessionId — caller-shape
    //     specific (newSession reads from createSession's response,
    //     loadSessionMessages reads from its argument after a setAgent
    //     handshake).
    //   - todoStore.setCurrentSession + hydrateFromMessages — caller
    //     decides whether to hydrate from fetched history or seed empty.
    //   - currentModelId / currentProviderId — newSession propagates
    //     from createSession response; loadSessionMessages reads from
    //     the SessionSummary.
    resetSessionScopedState(sessionId: string): void {
      this.error = null
      // Messages slice — newSession has no fetch so the prior session's
      // residue must be cleared explicitly here. loadSessionMessages
      // overwrites this with the fetched history a few lines later;
      // the brief empty-array intermediate state matches the
      // "user is looking at empty composer state" UX comment in the
      // session-switcher selectSession path.
      this.messages = []
      // A critical-class banner from a prior session is no longer
      // relevant once the user switches contexts. The banner is bound
      // to the failing session — the new one starts clean. A fresh
      // critical event on the new session will repopulate this.
      this.criticalError = null
      // Bug Hunt (May 2026) — per-session usage isolation. The chip
      // tracks the active session, so re-hydrate currentContextUsage
      // from the per-session map. A session with a prior emission
      // (its stream may be running in the background, or its summary
      // was populated by a previous emit) shows its last figure;
      // sessions with no record fall back to null and the chip
      // renders its empty state until the next emission.
      this.currentContextUsage = this.contextUsageBySession[sessionId] ?? null
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
      // Phase-5 §1c-γ — clear the dedup gates so a fresh session can
      // accept its first compaction / gate halt / critical error
      // without being suppressed by the prior session's last key.
      this.lastCompactionEventKey = null
      this.lastGateFailureKey = null
      this.lastCriticalErrorCorrelationId = null
      // Live session-bleed bug bundle (May 2026, Bug 1) — the context_usage
      // dedup gate (§1c-β) is GLOBAL and the JSDoc at chatStore.ts:711-712
      // explicitly states it must be cleared on session change so a fresh
      // session's first figure is never suppressed by the prior session's
      // last figure. Symmetric with the §1c-γ sibling resets above.
      this.lastContextUsageKey = null
      // Provider/model failover toast dedup — JSDoc at chatStore.ts:688-690
      // explicitly states cleared on session change "via the shared
      // reset path". Pre-helper-extraction the prose was aspirational —
      // the inline reset block in loadSessionMessages never actually
      // touched this field. Including it here closes that latent
      // adjacent surface while we are reworking the reset path.
      this.lastProviderChangeKey = null
      // Sketch A — UI Delegation Chain Not Updating (May 2026). The
      // per-row debounce for the mid-poll loadSessions refresh is
      // per-session — the new session's poll must be able to fire a
      // fresh refresh for ITS first delegation_started row even if a
      // (vanishingly unlikely) collision would otherwise suppress it.
      this.seenDelegationStartedIds.clear()
      // Bug fix — Child session "Live" indicator goes stale (May 2026).
      // Symmetric per-session reset for the terminal-row dedup set.
      this.seenDelegationCompletedIds.clear()
      // Streaming Coherence Slice F — clear stale per-session phase
      // for the target session so its watchdog starts at the legacy
      // 60s default until the next engine heartbeat updates the phase.
      delete this.streamingPhase[sessionId]
      // UI Parity PR5 (May 2026) — clear stale token counter state
      // for the target session so a returning user does not see a
      // counter from the prior turn flash up before the next
      // heartbeat re-seeds it. The next streaming.heartbeat tick
      // repopulates these.
      delete this.tokenCountBySession[sessionId]
      delete this.tokensPerSecondBySession[sessionId]
      delete this.lastHeartbeatAtBySession[sessionId]
      // Permission Modes (May 2026) — Slice 2. Rebind the chip's
      // selection to the new session's persisted value (or the safe
      // default when no record exists). Without this hop the chip
      // would carry the prior session's mode across the switch — a
      // YOLO chip silently following the operator into a fresh
      // session is exactly the foot-gun the per-session keying is
      // designed to prevent.
      this.hydratePermissionMode(sessionId)
    },

    async newSession(): Promise<void> {
      const session = await createSession(this.agentId)
      // Route through the shared reset surface BEFORE the
      // currentSessionId flip so the helper's resets are bound to the
      // new session id (the per-session keyed deletes target the new
      // id, which is harmless idempotent — but explicit). This is the
      // architectural close on the live bleed the user reproduced
      // post-15f30f6e: clicking "New Session" left the prior
      // session's context_usage chip, dedup gates, and per-session
      // heartbeat residue in place because newSession ran none of
      // loadSessionMessages's reset protocol.
      this.resetSessionScopedState(session.id)
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
      // Per-session SSE singleton (Slice B — Streaming Coherence May 2026):
      // do NOT disconnect the prior session's stream on switch. Each
      // session keeps its own EventSource so session A continues to
      // stream in the background while the user reads / composes in B.
      // The chunk handler's `currentSessionId !== capturedSessionId`
      // guard prevents A's chunks from landing on B's view; A's
      // canonical state is recovered via reconcileFromBackend when the
      // user returns to A (or when A's stream completes server-side).
      this.setSessionStreaming(sessionId, { isLoading: true })
      // Route through the shared reset surface — see
      // resetSessionScopedState for the full per-slice rationale and
      // the May 2026 live-bleed bundle. Pre-helper-extraction this
      // block was inline here and newSession (the "New Session" button
      // path) duplicated none of it, so every session-scoped dedup
      // gate, banner, counter, and per-session heartbeat slot leaked
      // into the new session.
      this.resetSessionScopedState(sessionId)
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
          // Click-click race fix (May 2026 bug-hunt round 7) — thread
          // the snapshot session id explicitly into setAgent so its
          // post-await applyContextUsageFromSession knows which
          // session the PATCH was issued for. A second loadSessionMessages
          // call landing between this assignment and the PATCH
          // resolving used to bleed the first PATCH's context_usage
          // onto the second session's chip. Explicit > magic capture.
          await this.setAgent(sessionAgentId, { sessionId })
        }

        if (session) {
          this.currentModelId = session.currentModelId ?? ''
          this.currentProviderId = session.currentProviderId ?? ''
        }

        // Permission Modes (May 2026) — Slice 3. resetSessionScopedState
        // above ran the localStorage-fall-back hydration before the
        // sessions[] summary was in hand. Override now with the backend
        // value when it's present so the canonical precedence (backend
        // > localStorage > default) holds even when localStorage carries
        // a stale value from a prior write the user later reverted on a
        // different device.
        if (session?.permissionMode) {
          this.hydratePermissionMode(sessionId, session.permissionMode)
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

        // Plans/Child Session Turn Registry Plumbing (May 2026) §S7 —
        // PR4 audit found that clicking a child row in
        // ChildSessionsPanel (or any session-switch path that lands
        // here) did NOT reattach the long-poll. restoreStateFromBackend
        // calls maybeReattachStream on initial mount (lines 1117/1150)
        // but the runtime switch path was missing the equivalent call.
        // Net effect pre-fix: the user lands on the child session and
        // sees its static history, but the Turn registry's MessagesAdded
        // slice never reaches the FE because pollTurnUntilTerminal was
        // never started for the child's activeTurnId. PR3's backend-
        // authoritative Live indicator on the LIST still lit up, but
        // the user couldn't actually SEE the live chunks once inside
        // the child. This is the canonical S7 contract — closing it
        // here makes the chain `selectChild` → `loadSessionMessages` →
        // `maybeReattachStream` → `pollTurnUntilTerminal` complete and
        // mirrors the restore path's behaviour. `session` is the
        // SessionSummary captured at the top of this try block; the
        // post-fetch summaries map is not re-read because the registry-
        // backed activeTurnId is what maybeReattachStream consults via
        // its own this.sessions.find lookup at chatStore.ts:1173.
        this.maybeReattachStream(sessionId, session?.isStreaming ?? false)
      } finally {
        // Per-session state — clear isLoading on the target session
        // (the message-history fetch is done). DO NOT clear isStreaming:
        // the target session may still have an active SSE stream
        // (Slice B per-session-singleton invariant) that landed earlier
        // chunks while the user was on a different session. Pre-Slice-B
        // this method tore down the prior session's stream and reset
        // both flags; that path is gone.
        this.setSessionStreaming(sessionId, { isLoading: false })
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

      // Bug Hunt (May 2026 — live-render race): preserve any in-flight
      // locally-generated rows (status === 'running') that the backend
      // has not yet sealed into history. sendMessage's post-POST
      // reconcile fires as soon as `await sendSessionMessage` resolves,
      // which on the new async POST path returns BEFORE the SSE stream
      // delivers all chunks (and well before [DONE] / the canonical
      // assistant persist). Pre-fix the wholesale replace at
      // `this.messages = [...sealedBackend, ...orphans]` therefore wiped
      // the streaming assistant placeholder (id `streaming-*`), the
      // in-flight thinking row (id `thinking-*`), and any in-flight
      // delegation card (id `delegation-*`) the SSE chunks were
      // actively building. The user saw no live response; only the
      // final canonical row appeared after a manual refresh, because
      // `handleContentChunk` / `handleThinkingEvent` re-created fresh
      // placeholders for subsequent chunks but the next reconcile
      // wiped those too in a tight enough race.
      //
      // The preservation is gated TWICE:
      //
      //   1. status === 'running' — backend history is sealed to
      //      'completed' on the `sealedBackend.map` above, so a
      //      'running' row in `this.messages` can only have come from
      //      the local streaming pipeline. When [DONE] arrives,
      //      `handleStreamDone` seals the row to 'completed' and this
      //      filter no longer matches.
      //
      //   2. The backend has NOT yet caught up with the assistant for
      //      this turn. We probe this by checking whether the
      //      canonical history's terminal row is the user message
      //      (turn still in-flight server-side) or an
      //      assistant/thinking/tool_result row (turn done, the
      //      backend has the final state). When the backend has
      //      caught up, the local in-flight rows are obsolete
      //      placeholders and the test `loadSessions detects
      //      was-streaming → not-streaming and reconciles` explicitly
      //      requires them to be wiped so the canonical reply
      //      surfaces. Without this gate the delegation_started card
      //      from the parent-watching-child scenario would persist
      //      alongside the canonical final assistant row, producing a
      //      duplicate.
      const lastBackendRow = sealedBackend[sealedBackend.length - 1]
      const backendTurnInFlight =
        !lastBackendRow || lastBackendRow.role === 'user'
      const inFlightLocalOrphans = backendTurnInFlight
        ? this.messages.filter(
            (m) => m.status === 'running' && !backendIds.has(m.id),
          )
        : []

      this.messages = [...sealedBackend, ...optimisticOrphans, ...inFlightLocalOrphans]

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

    /**
     * pollTurnUntilTerminal drives the Phase-4-Commit-1b long-poll path.
     * Each iteration issues GET /api/v1/sessions/{sessionId}/turns/{turnId}
     * with ?wait=true&since=N — the server holds the request until ANY
     * of (a) a new message accumulates past `lastMessageCount`,
     * (b) Phase / TokenCount move, (c) Status leaves Running, OR
     * (d) 25s elapses. The server hold REPLACES the prior client-side
     * setTimeout backoff — the await on fetchTurn IS the cadence.
     *
     * Net effect: each chunk lands in the FE within broadcast-latency
     * of the server-side Append (sub-50ms target on a quiet network),
     * not at the 250ms polling-window boundary the old loop pinned.
     *
     * Plan ref:
     *   ~/vaults/baphled/1. Projects/FlowState/Plans/
     *     Turn-Based Post-Then-Poll Architecture (May 2026).md §4d
     *     Commit 1b — long-poll endpoint.
     *
     * Backwards-compat fallback: if the FIRST long-poll request looks
     * like it hit a pre-1b server (no long-poll headers OR an error that
     * suggests the feature isn't wired), the loop falls back to the
     * legacy 250/1000/3000ms cadence. Detection is response-time-based:
     * a sub-30ms first response with status=running and an empty messages
     * array AGAINST a known-running turn likely means the server ignored
     * the ?wait param and returned the snapshot immediately. We use this
     * heuristic only to switch cadence — not to fail loudly.
     *
     * Each poll's `messages` array is merged into local state by id —
     * additive growth, no duplication. The orphan-preservation logic
     * from `7eb3cf47` (preserved in reconcileFromBackend) is reused
     * implicitly: we never wipe existing rows, just upsert by id.
     *
     * Terminal states:
     *   - 'completed' — silent return; the caller fires reconcile.
     *   - 'failed' — surface the engine's error message via store.error
     *     so the user sees what went wrong rather than a silent hang.
     *
     * Defence-in-depth:
     *   - 404 from fetchTurn (e.g. server restart drops the registry):
     *     stop polling and let reconcileFromBackend take over.
     *   - Session switch mid-poll: terminate. The local `messages`
     *     array now belongs to a different session.
     *   - AbortController: the in-flight long-poll is aborted when
     *     the session changes so the server-side wait wakes promptly
     *     via r.Context().Done().
     */
    async pollTurnUntilTerminal(sessionId: string, turnId: string): Promise<void> {
      // Legacy-fallback cadence — only used when the long-poll path is
      // detected as unsupported (see useLongPoll gate below).
      const POLL_INTERVAL_FAST_MS = 250
      const POLL_INTERVAL_SLOW_MS = 1000
      const POLL_INTERVAL_SLOWER_MS = 3000
      const POLL_BACKOFF_AFTER_FAST = 10
      const POLL_BACKOFF_AFTER_SLOW = 30

      // Long-poll mode is the default; flips to false on the first
      // response that smells like a pre-1b server (see below).
      let useLongPoll = true
      // The FE's view of len(messages) accumulated so far for this
      // turn. Each long-poll iteration sends this as ?since=N so the
      // server only wakes when the registry's count has grown past it.
      let lastMessageCount = 0
      let pollCount = 0
      // Phase-5 §1c-α — track the prior poll's (current_provider,
      // current_model) pair so the loop can diff each new snapshot
      // against the prior one and fire handleProviderChangedEvent on a
      // real transition. The SSE handler at chatStore.ts:2740-2756 also
      // calls handleProviderChangedEvent for the same transition during
      // the transitional 1c-α surface; the handler's lastProviderChange
      // Key dedup gate ensures only one toast fires per transition.
      //
      // Initial value: empty strings. The first poll that carries a
      // non-empty current_provider counts as a transition (empty → real)
      // — the chip pivots once the engine announces the live pair.
      let lastPollProvider = ''
      let lastPollModel = ''
      // Phase-5 §1c-β — track prior poll's context_usage figure for diff.
      // We serialise the four observable fields into a string key so the
      // diff is a cheap string compare and avoids per-field deep checks.
      // Empty initial value: the first poll that carries a context_usage
      // payload counts as a transition (empty → real) and routes through
      // handleContextUsageEvent.
      let lastPollContextUsageKey = ''
      // Phase-5 §1c-β — track prior poll's per-partition provider_quota
      // snapshots so the diff fires applyProviderQuotaEvent only on a
      // real change (new partition OR per-partition value move).
      // Initial empty map: first poll with any partitions counts as
      // transitions (empty → real) and routes through the quotaStore.
      const lastPollQuotaKeys: Record<string, string> = {}
      // Phase-5 §1c-γ — track prior poll's CompactionEvents + GateFailures
      // slice lengths so the diff can identify NEW entries (positional
      // indexing past the prior length). The wire shape is append-only;
      // each new entry routes through its corresponding handler.
      let lastPollCompactionLen = 0
      let lastPollGateFailuresLen = 0

      // Per-iteration AbortController so a session-switch can wake
      // the server-side wait promptly. Recreated each iteration; the
      // previous controller's signal is no-op'd by the resolved fetch.
      while (true) {
        // Session-switch guard. If the user navigated away, abort —
        // the running turn lives server-side, the user can resume on
        // return.
        if (this.currentSessionId !== sessionId) {
          return
        }

        const controller = new AbortController()
        // Race the controller against the session-switch watcher: if
        // the session changes mid-await, abort the in-flight request.
        // We rely on the loop's own currentSessionId check at the top
        // of each iteration to cover the steady-state case; the
        // explicit abort here is for the in-flight request only.

        let state: TurnState
        try {
          if (useLongPoll) {
            state = await fetchTurn(sessionId, turnId, {
              wait: true,
              since: lastMessageCount,
              signal: controller.signal,
            })
          } else {
            state = await fetchTurn(sessionId, turnId)
          }
        } catch (err) {
          // Aborted by session switch (or component teardown): exit
          // cleanly without surfacing an error. AbortError is the
          // typical shape; defensive .name check covers DOMException
          // (which is NOT always `instanceof Error` in jsdom and older
          // node runtimes — we must inspect the structural .name /
          // .message fields directly rather than rely on the Error
          // prototype chain).
          const errAsObj = err as { name?: unknown; message?: unknown } | null | undefined
          const errName = typeof errAsObj?.name === 'string' ? errAsObj.name : ''
          const errMessage = typeof errAsObj?.message === 'string' ? errAsObj.message : ''
          const aborted = errName === 'AbortError' || /abort/i.test(errMessage)
          if (aborted) {
            return
          }
          // M-01 (Vue Web Frontend Master Bug Report, May 2026): non-
          // abort errors (TypeError "Failed to fetch", 5xx wrapped in
          // Error, JSON parse failures) MUST surface to the user.
          // Pre-fix this branch silently returned, leaving the chat
          // gate cleared (the enclosing sendMessage finally / the
          // maybeReattachStream finally clear flags) but with no
          // user-visible signal — the user's message appeared then the
          // composer flipped back to Send with no assistant reply and
          // no explanation. Setting this.error lights up the existing
          // chat-error footer; reconcileFromBackend on the outer turn
          // path still runs to pull whatever canonical state the
          // server managed to persist.
          const surfaceMessage = errMessage || 'unknown error'
          this.error = `Streaming connection failed: ${surfaceMessage}. The chat is recoverable — send another message to retry.`
          return
        }
        pollCount++
        // Live session-bleed bug bundle (May 2026, Bug 2) — POST-await
        // session-switch guard. The pre-await guard at the top of the
        // loop (chatStore.ts:1942-1944) only covers the steady-state
        // case; the long-poll fetch above can suspend for up to 25s and
        // during that window the user can navigate to a different
        // session via loadSessionMessages. If we don't re-check here,
        // the merge below writes this poll's incoming rows (which belong
        // to the captured `sessionId`) into `this.messages` (which now
        // belongs to the NEW currentSessionId). Same intent as the
        // existing per-iteration AbortController + AbortError catch
        // above — that path covers an in-flight cancel, this one covers
        // the race where the fetch resolved cleanly with stale-session
        // data before the AbortController could fire (e.g. the response
        // was already in the network buffer at switch time).
        if (this.currentSessionId !== sessionId) {
          return
        }
        // Sync lastMessageCount AFTER the merge below — see comment
        // there for ordering. The actual update happens at the end of
        // the loop body.

        // Apply this poll's MessagesAdded delta. The Turn endpoint
        // returns the engine-emitted rows added during the turn
        // (assistant, thinking, tool_call, tool_result, delegation)
        // EXCLUDING the user message — that's already in local state
        // from the optimistic push.
        //
        // Merge semantics:
        //   - Match each incoming row by id. If we have a local row
        //     with the same id, replace its content/status/etc.
        //     (preserves position, avoids the duplication that a naive
        //     push would cause across multiple polls).
        //   - New ids append to the end. This matches the backend's
        //     emission order — assistant rows arrive in monotonic
        //     order during the turn.
        //   - Optimistic temp-* user rows are left untouched. The id
        //     swap in sendMessage already promoted them to canonical
        //     ids, and the Turn endpoint excludes user messages, so
        //     there is no collision risk here.
        const incoming = state.messages ?? []
        for (const row of incoming) {
          if (!row || !row.id) continue
          // Sketch A — UI Delegation Chain Not Updating (May 2026).
          // First time we see this delegation_started row id, fan out a
          // `loadSessions()` refresh so ChildSessionsPanel surfaces the
          // newly-spawned child within one poll-tick of its first
          // appearance in the Turn snapshot. The per-row debounce
          // (seenDelegationStartedIds) ensures this fires ONCE per row
          // — not on every subsequent poll that the row remains
          // present.
          //
          // Why fan out here rather than after the merge: ordering does
          // not matter for the refresh (loadSessions is fire-and-forget
          // and merges into chatStore.sessions independently of
          // this.messages), and fronting the check inside the row loop
          // means a snapshot carrying multiple new delegation_started
          // rows (the swarm-fan-out case where a coordinator spawns N
          // children in the same dispatch tick) fires N refreshes if
          // the rows arrive on the same poll — actually we only need
          // ONE refresh per poll-tick regardless of how many rows are
          // new, but each row adds itself to the seen set so subsequent
          // ticks won't re-fire for the same id. The N-in-one-tick
          // duplicate-refresh is harmless (fetchSessions is idempotent;
          // the second call lands a microsecond later with byte-equal
          // payload). If profiling shows it's actually expensive we can
          // coalesce later with a `didFire` flag in this loop iteration.
          //
          // Sibling track Sketch B is plumbing a child Turn through
          // DelegateTool so the child's own chat view long-polls live;
          // THIS path closes only the "panel updates within seconds"
          // gap as a no-architectural-change quick win.
          if (
            row.role === 'delegation_started' &&
            !this.seenDelegationStartedIds.has(row.id)
          ) {
            this.seenDelegationStartedIds.add(row.id)
            void this.loadSessions()
          }
          // Bug fix — Child session "Live" indicator goes stale (May 2026).
          //
          // Symmetric trigger for the TERMINAL copy of the delegation
          // row (role === 'delegation', emitted by the engine's
          // accumulator when the child finishes — accumulator.go:177-192
          // / turn.go:699). Same shape as the started block above:
          // per-row debounce via a sibling Set keyed by message id,
          // single `loadSessions()` fan-out on first sight. Closes the
          // "Live" chip going stale: without this trigger the parent's
          // session list never re-fetches when the child completes, so
          // ChildSessionsPanel's pulsing-green affordance keeps pulsing
          // forever until the user manually navigates or the next turn
          // happens to fire a fresh delegation_started.
          //
          // Independent of the started block: a snapshot carrying both
          // a new started row AND a new terminal row fires TWO
          // refreshes (one each). They're separate panel transitions
          // (new chip appears; existing chip flips to idle) and the
          // panel needs the session list reconciled for each.
          if (
            row.role === 'delegation' &&
            !this.seenDelegationCompletedIds.has(row.id)
          ) {
            this.seenDelegationCompletedIds.add(row.id)
            void this.loadSessions()
          }
          const idx = this.messages.findIndex((m) => m.id === row.id)
          if (idx >= 0) {
            // Upsert by id — preserve position, replace contents IF
            // anything actually changed.
            //
            // Phase-4 Commit-2 (May-19 2026, "streamed vs dump" UX
            // investigation). The pre-fix path unconditionally spread
            // `{ ...existing, ...row }` and reassigned the array slot
            // on every poll, including no-op ticks where the backend
            // returned a byte-equal row during a quiet phase (between
            // content chunks, during a tool call, mid-thinking-block,
            // or after the cadence backed off to 1s/3s while the turn
            // was still "running"). That triggered the full Vue
            // reactivity cascade — MarkdownRenderer re-parsed the
            // whole accumulated content via md.render and v-html
            // replaced the entire `.markdown-body` subtree — for zero
            // observable change.
            //
            // The shallow-equal check skips the spread when every
            // incoming-row field already matches what we have. Net
            // effect at 250ms cadence on a long-running turn: the
            // MessageBubble for the active assistant row only
            // re-renders when its content / status / tool counters
            // genuinely move.
            //
            // Limits of this fix:
            //   - This does NOT make polling feel like a token stream.
            //     Each poll still delivers a fully-accumulated content
            //     string and the bubble still grows in 250ms jumps when
            //     content does arrive — that's structural to snapshot
            //     polling and only a transport change can address it.
            //   - This only short-circuits the no-op case. Real growth
            //     ticks still pay the same md.render cost as before.
            const existing = this.messages[idx]
            if (!rowsShallowEqual(existing, row)) {
              this.messages[idx] = { ...existing, ...row }
            }
          } else {
            this.messages.push(row)
          }
        }

        // Phase-4-Commit-2 — write the heartbeat-projected phase +
        // token_count onto the per-session FE state every poll. The
        // adaptive watchdog (now retired alongside SSE) used to read
        // streamingPhase to pick a per-phase threshold; the live token
        // counter + t/s display still reads tokenCountBySession +
        // tokensPerSecondBySession. Phase B re-verification of the
        // Phase-4-Commit-1 heartbeat-on-turn subscriber confirmed the
        // wire is fed from the Go side (server.go:543); this is the
        // FE-side reader the brief asked to wire.
        if (typeof state.phase === 'string') {
          this.streamingPhase[sessionId] = state.phase || ''
        }
        if (typeof state.token_count === 'number') {
          const prevCount = this.tokenCountBySession[sessionId]
          const prevAt = this.lastHeartbeatAtBySession[sessionId]
          const nowMs = Date.now()
          this.tokenCountBySession[sessionId] = state.token_count
          if (typeof prevCount === 'number' && typeof prevAt === 'number') {
            const deltaTokens = state.token_count - prevCount
            const deltaSeconds = (nowMs - prevAt) / 1000
            if (deltaSeconds > 0 && deltaTokens > 0) {
              this.tokensPerSecondBySession[sessionId] = Math.round(deltaTokens / deltaSeconds)
            } else {
              this.tokensPerSecondBySession[sessionId] = 0
            }
          } else {
            this.tokensPerSecondBySession[sessionId] = 0
          }
          this.lastHeartbeatAtBySession[sessionId] = nowMs
        }

        // Phase-5 §1c-α — diff the (current_provider, current_model) pair
        // against the prior poll's snapshot. On a real transition, route
        // through handleProviderChangedEvent so the chip pivots and the
        // toast fires. Run BEFORE the status-check returns so a
        // completion-poll that also carries the final pair still fires
        // the handler.
        const incomingProvider =
          typeof state.current_provider === 'string' ? state.current_provider : ''
        const incomingModel =
          typeof state.current_model === 'string' ? state.current_model : ''
        if (
          (incomingProvider !== '' || incomingModel !== '') &&
          (incomingProvider !== lastPollProvider || incomingModel !== lastPollModel)
        ) {
          this.handleProviderChangedEvent({
            toProvider: incomingProvider,
            toModel: incomingModel,
          })
          lastPollProvider = incomingProvider
          lastPollModel = incomingModel
        }

        // Phase-5 §1c-β — diff the context_usage figure against the prior
        // poll. On change, route through handleContextUsageEvent — the same
        // handler the SSE branch at chatStore.ts:~2795 calls — so the chip
        // ticks up without an SSE side-channel. The handler's
        // `lastContextUsageKey` dedup gate (added in 1c-β) ensures the
        // transitional double-fire (poll + SSE for the same figure) only
        // mutates state once.
        const incomingCU = state.context_usage
        if (incomingCU) {
          const cuKey = `${incomingCU.input_tokens}:${incomingCU.output_reserve}:${incomingCU.limit}:${incomingCU.percentage}`
          if (cuKey !== lastPollContextUsageKey) {
            this.handleContextUsageEvent({
              inputTokens: incomingCU.input_tokens,
              outputReserve: incomingCU.output_reserve,
              limit: incomingCU.limit,
              percentage: incomingCU.percentage,
            }, sessionId)
            lastPollContextUsageKey = cuKey
          }
        }

        // Phase-5 §1c-β — diff each provider_quota partition. The poll
        // returns the full set; we compare each partition's snapshot
        // signature against the prior poll's view and route only the
        // changed partitions through quotaStore.applyProviderQuotaEvent.
        // The action's own structural-equal guard (Phase-5 §1c-β) means
        // a double-fire with the SSE branch is also safe — the second
        // call sees the existing snapshot and short-circuits.
        const incomingQuotas = state.provider_quotas
        if (Array.isArray(incomingQuotas)) {
          for (const snap of incomingQuotas) {
            const partitionKey = `${snap.provider}:${snap.account_hash}:${snap.model ?? ''}`
            // Signature = observedAt + variant's primary figure. Mirrors
            // the quotaStore.providerQuotaSnapshotEqual gate.
            let figure: string | number = 'na'
            if (snap.variant === 'token_spend') {
              figure = snap.token_spend?.spent_minor ?? -1
            } else if (snap.variant === 'rate_limit') {
              figure = snap.rate_limit?.tightest_percent_remaining ?? -1
            } else if (snap.variant === 'not_configured') {
              figure = snap.not_configured?.reason ?? ''
            }
            const sig = `${snap.observed_at}:${snap.variant}:${figure}`
            if (sig === lastPollQuotaKeys[partitionKey]) {
              continue
            }
            // Route through quotaStore via the existing SSE event shape.
            // The poll wire surfaces snake_case (matching the engine's
            // SSE chunk); the quotaStore action expects camelCase via
            // SSEProviderQuotaEvent. Translate variant payloads at this
            // seam — the field-rename matrix mirrors the Go-side
            // `json:"..."` tags on sseProviderQuota* structs.
            const rl = snap.rate_limit
            const ts = snap.token_spend
            const nc = snap.not_configured
            useQuotaStore().applyProviderQuotaEvent({
              kind: 'provider_quota',
              provider: snap.provider,
              accountHash: snap.account_hash,
              model: snap.model ?? '',
              observedAt: snap.observed_at,
              stale: snap.stale ?? false,
              storeBackend: snap.store_backend ?? '',
              pricingSource: snap.pricing_source ?? '',
              variant: snap.variant,
              rateLimit: rl
                ? {
                    requests: {
                      limit: rl.requests.limit,
                      remaining: rl.requests.remaining,
                      reset: rl.requests.reset ?? '',
                    },
                    tokens: {
                      limit: rl.tokens.limit,
                      remaining: rl.tokens.remaining,
                      reset: rl.tokens.reset ?? '',
                    },
                    input: {
                      limit: rl.input.limit,
                      remaining: rl.input.remaining,
                      reset: rl.input.reset ?? '',
                    },
                    output: {
                      limit: rl.output.limit,
                      remaining: rl.output.remaining,
                      reset: rl.output.reset ?? '',
                    },
                    tightestPercentRemaining: rl.tightest_percent_remaining,
                    tightestResetAt: rl.tightest_reset_at ?? '',
                  }
                : null,
              tokenSpend: ts
                ? {
                    spentMinor: ts.spent_minor,
                    spentCurrency: ts.spent_currency,
                    spentUsdMinor: ts.spent_usd_minor,
                    capMinor: ts.cap_minor ?? 0,
                    capCurrency: ts.cap_currency ?? '',
                    period: ts.period,
                    periodStart: ts.period_start,
                    periodEnd: ts.period_end,
                    thresholdAmber: ts.threshold_amber,
                    thresholdRed: ts.threshold_red,
                  }
                : null,
              notConfigured: nc ? { reason: nc.reason } : null,
            })
            lastPollQuotaKeys[partitionKey] = sig
          }
        }

        // Phase-5 §1c-γ — diff CompactionEvents by length. The poll
        // returns the cumulative slice; positional indexing past the
        // prior poll's length tells us which entries are NEW. Each new
        // entry routes through handleContextCompactedEvent — the same
        // handler the SSE branch at chatStore.ts:~2997 calls — so the
        // chip's flash + tooltip update without an SSE side-channel.
        // The handler's `lastCompactionEventKey` dedup (added in 1c-γ
        // for the SSE+poll transitional double-fire) ensures the chip
        // only flashes once per real event.
        const incomingCompactions = state.compaction_events
        if (Array.isArray(incomingCompactions) && incomingCompactions.length > lastPollCompactionLen) {
          for (let i = lastPollCompactionLen; i < incomingCompactions.length; i++) {
            const ev = incomingCompactions[i]
            this.handleContextCompactedEvent({
              sessionId: ev.session_id,
              originalTokens: ev.original_tokens,
              summaryTokens: ev.summary_tokens,
              trigger: ev.trigger,
            })
          }
          lastPollCompactionLen = incomingCompactions.length
        }

        // Phase-5 §1c-γ — diff GateFailures by length. The poll returns
        // the cumulative slice; new entries past the prior length are
        // routed onto `lastGateFailure` (the LATEST entry wins, matching
        // the SSE handler's overwrite-on-fresh-halt policy at
        // chatStore.ts:~2996). The dedup key gates the within-iteration
        // re-fire when the slice did not grow.
        const incomingGates = state.gate_failures
        if (Array.isArray(incomingGates) && incomingGates.length > lastPollGateFailuresLen) {
          // Take the LATEST new entry — the banner only shows one at a
          // time. If multiple halts landed since the prior poll, the
          // most recent is the operator's signal of the current
          // halt-class state.
          const latest = incomingGates[incomingGates.length - 1]
          const key = `${latest.swarm_id}:${latest.gate_name}:${latest.reason}:${latest.cause}`
          if (key !== this.lastGateFailureKey) {
            this.lastGateFailure = {
              swarmId: latest.swarm_id,
              lifecycle: latest.lifecycle,
              memberId: latest.member_id,
              gateName: latest.gate_name,
              gateKind: latest.gate_kind,
              reason: latest.reason,
              cause: latest.cause,
              coordStoreKeys: latest.coord_store_keys ?? [],
            }
            this.lastGateFailureKey = key
          }
          lastPollGateFailuresLen = incomingGates.length
        }

        // Phase-5 §1c-γ — diff CriticalError. The poll returns the
        // sanitised payload (or omits it when no critical error has
        // fired). nil→non-nil transitions populate the persistent
        // CriticalErrorBanner via `criticalError`; correlation_id is
        // the dedup key (matches the brief's prescription). Re-fire
        // with the same id is a no-op (the user-dismissed banner stays
        // dismissed); a fresh id is a new fatal error and MUST replace
        // the prior payload per the SSE handler's overwrite policy at
        // chatStore.ts:~2907.
        const incomingCE = state.critical_error
        if (incomingCE) {
          const cid = incomingCE.correlation_id ?? ''
          if (cid !== this.lastCriticalErrorCorrelationId) {
            this.criticalError = {
              message: incomingCE.message,
              correlationId: cid,
            }
            this.lastCriticalErrorCorrelationId = cid
          }
        }

        if (state.status === 'failed') {
          // Surface the engine's error so the user sees what happened.
          // The post-poll reconcile still fires from sendMessage's
          // outer code; it will render whatever the backend persisted.
          this.error = state.error || 'turn failed'
          return
        }
        if (state.status !== 'running') {
          // 'completed' — done. Caller fires reconcileFromBackend.
          return
        }

        // Update the long-poll baseline AFTER the merge so the next
        // iteration's ?since=N reflects the FE's latest view. The
        // server then only wakes when len > N OR a watched field moves.
        lastMessageCount = state.messages?.length ?? lastMessageCount

        if (useLongPoll) {
          // The server's hold IS the cadence — no client-side delay.
          // We yield to the event loop so any pending Vue reactivity /
          // session-switch / unsubscribe runs before the next request
          // fires. `Promise.resolve()` is the cheapest yield available;
          // no setTimeout, no jitter.
          await Promise.resolve()
          continue
        }

        // Legacy-fallback adaptive backoff (pre-1b server). The first 10
        // polls run at 250ms cadence to feel "live"; subsequent polls
        // slow to 1s, then 3s after 30 total polls. This caps the
        // polling overhead on long-running turns at ~20 polls/minute.
        let delay = POLL_INTERVAL_FAST_MS
        if (pollCount >= POLL_BACKOFF_AFTER_SLOW) {
          delay = POLL_INTERVAL_SLOWER_MS
        } else if (pollCount >= POLL_BACKOFF_AFTER_FAST) {
          delay = POLL_INTERVAL_SLOW_MS
        }
        await new Promise<void>((resolve) => setTimeout(resolve, delay))
      }
    },

    // loadSessionByAgentId resolves the in-thread delegation-card click —
    // MessageBubble.loadDelegatedSession passes `targetAgent` here because
    // the persisted `delegation` / `delegation_started` message carries
    // only the target agent name (the streaming.DelegationEvent wire shape
    // has no ChildSessionID; the load-bearing child_session_id is on the
    // separate SwarmEvent stream consumed by DelegationPanel, not on the
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

    // recordChainSession is the seam swarmStore.ingestEventLine writes
    // to when a `delegation` SwarmEvent arrives carrying both the chain
    // id (event.id) and the child session id (metadata.child_session_id).
    // The map is consumed by loadSessionForDelegation to route the
    // in-thread delegation card click to the correct sibling when a
    // parent delegated to the same agent multiple times. Idempotent —
    // re-recording the same pair is a no-op; the engine emits multiple
    // updates per chain (started, in-flight, completed) and they all
    // carry the same (chainId, childSessionId).
    recordChainSession(chainId: string, childSessionId: string): void {
      if (!chainId || !childSessionId) return
      this.chainSessions[chainId] = childSessionId
    },

    // loadSessionForDelegation is the seam EVERY delegated-session
    // click surface routes through — the in-thread MessageBubble
    // delegation card, the persistent ChildSessionsPanel rows, AND
    // the live swarm-bus DelegationPanel cards. Sharing one resolver
    // closes the bug class where each surface re-implemented routing
    // and bypassed the chainId disambiguation (prior six fixes —
    // 4607120b/b1d485eb/93bf40ed/a488b858/40ad53d2/21f0681e — each
    // protected only the MessageBubble path; the other two surfaces
    // kept calling loadSessionMessages with raw ids and silently
    // re-opened the bug for users who navigated via the panels).
    //
    // Resolution order:
    //   1. If chainId is set AND we have observed it (live SwarmEvent
    //      OR cold-reload backfill via loadSessions), jump directly
    //      to the recorded child session. chain ids are unique per
    //      delegation so this disambiguates sibling delegations to
    //      the same agent (the sibling-confusion bug class).
    //   2. If chainId is set but unknown to the map, refresh sessions
    //      ONCE and retry the map. The refresh path closes the race
    //      where the click fires before the just-persisted child
    //      session has been listed (cold-reload window, live click
    //      before the swarm-event-driven recordChainSession has
    //      reached the chunk processor). Bounded to one refresh so a
    //      genuinely-unknown chainId can't induce a polling loop.
    //   3. If childSessionId is set AND it appears in sessions[],
    //      use it directly. This is the DelegationPanel/
    //      ChildSessionsPanel hint path — they read the id off a
    //      SwarmEvent or SessionSummary, and we validate it against
    //      our local list before trusting it. An unvalidated hint is
    //      discarded (a stale or spoofed id should not silently
    //      navigate us to a session we have no record of).
    //   4. Fall back to loadSessionByAgentId(agentId), which uses
    //      the "most-recent child of the active parent" heuristic.
    //      Preserves correctness for legacy persisted messages
    //      without a chainId AND closes the swarm-bridge re-entry
    //      case (parent is itself the delegated agent).
    //
    // Returns true when a session was loaded, false when no resolver
    // produced a candidate.
    async loadSessionForDelegation(opts: {
      chainId?: string
      agentId?: string
      childSessionId?: string
    }): Promise<boolean> {
      const { chainId, agentId, childSessionId } = opts
      // Step 1 — chainId map lookup (hot path).
      if (chainId) {
        const recorded = this.chainSessions[chainId]
        if (recorded) {
          await this.loadSessionMessages(recorded)
          return true
        }
        // Step 2 — chainId miss → single refresh + retry. The
        // loadSessions() backfill rebuilds chainSessions from the
        // persisted session list (chatStore.ts:1587-1591). If the
        // backend has the child listed by now the retry lands it.
        // We swallow refresh failures: if the backend is unreachable
        // we still want the local resolvers to attempt a fallback
        // rather than hard-failing the click.
        try {
          await this.loadSessions()
        } catch {
          // Best-effort refresh; fall through to local-only resolvers.
        }
        const recordedAfterRefresh = this.chainSessions[chainId]
        if (recordedAfterRefresh) {
          await this.loadSessionMessages(recordedAfterRefresh)
          return true
        }
      }
      // Step 3 — validated childSessionId hint.
      if (childSessionId) {
        const hinted = this.sessions.find((s) => s.id === childSessionId)
        if (hinted) {
          await this.loadSessionMessages(hinted.id)
          return true
        }
      }
      // Step 4 — agent-id fallback. Requires agentId; without it (a
      // DelegationPanel event whose metadata has neither a known
      // chainId nor a present childSessionId AND no target_agent —
      // a degenerate payload) we return false rather than guess.
      if (!agentId) {
        return false
      }
      return this.loadSessionByAgentId(agentId)
    },

    // UI Parity PR2 B4 (May 2026) — push a prompt onto the history ring.
    // Capped at 50 entries; the oldest entry rolls off the front. Adjacent
    // duplicates are folded so re-running the same prompt twice does not
    // burn two slots.
    //
    // P1-4 (May 2026 bug-fix bundle): per-session storage. When a session
    // is current, the entry lands in `promptHistoryBySession[sessionId]`
    // so it does not bleed into other sessions' ArrowUp recall. The
    // null-session fast path still uses the legacy flat list so
    // pre-session-create sends remain history-aware.
    recordPromptHistory(text: string): void {
      const trimmed = text.trim()
      if (!trimmed) return
      const sessionId = this.currentSessionId
      let buf: string[]
      if (sessionId) {
        buf = this.promptHistoryBySession[sessionId]
        if (!buf) {
          buf = []
          this.promptHistoryBySession[sessionId] = buf
        }
      } else {
        buf = this.promptHistoryLegacy
      }
      if (buf.length > 0 && buf[buf.length - 1] === trimmed) {
        return
      }
      buf.push(trimmed)
      // Cap matches the TUI history depth so muscle memory carries between
      // surfaces.
      const PROMPT_HISTORY_CAP = 50
      if (buf.length > PROMPT_HISTORY_CAP) {
        buf.splice(0, buf.length - PROMPT_HISTORY_CAP)
      }
    },

    async sendMessage(content: string, options?: { attachmentIds?: string[] }): Promise<void> {
      const text = content.trim()
      if (!text) {
        return
      }
      const attachmentIds = options?.attachmentIds ?? []
      // UI Parity PR2 B4 (May 2026) — prompt history for ArrowUp recall.
      // Record every non-empty send (including /compact and queued
      // prompts) onto the ring buffer so the composer's history walk
      // reaches the canonical list of what the user actually submitted.
      // Dedup against the most recent entry — re-running the same prompt
      // twice in a row should not double-fill the buffer.
      this.recordPromptHistory(text)
      // Deliverable 3 (May 2026 context-accuracy bundle) — slash
      // commands that are handled entirely client-side must
      // short-circuit BEFORE the optimistic-bubble push so the
      // composer does not leak a "/compact" user message into the
      // transcript. Only /compact (renamed from /compress in the
      // May 2026 OpenCode-shape rename) currently uses this path;
      // future client-handled commands would join the if-chain here.
      if (text === '/compact') {
        await this.compactCurrentSession()
        return
      }
      // Pre-fix this branch silently early-returned when isLoading was true.
      // Combined with a stuck stream (no [DONE] from the backend), the user
      // saw the chat appear frozen with no surfacing of any kind. The gate
      // now sets this.error so the existing chat-error footer renders the
      // rejection. The MessageInput component additionally surfaces a toast
      // — the two surface independently because non-input call sites
      // (e.g. programmatic resends) still need a visible signal.
      //
      // Per-session state (Slice A) — read the gate from the active
      // session's slot, NOT the flat legacy field. Pre-slice the flat
      // gate bounced session B's send while session A was streaming;
      // now session B's gate is independent. Fall back to the flat
      // field when no current session exists (lazy-create branch
      // hasn't run yet) — that's the legacy-shape contract for the
      // pre-session-create gate.
      const gateState = this.currentSessionId
        ? this.streamingFor(this.currentSessionId)
        : { isLoading: this.isLoading, isStreaming: this.isStreaming }
      if (gateState.isLoading) {
        // Streaming Coherence Slice E (May 2026) — queued prompts.
        // Submit-while-streaming pushes onto the session's queue
        // instead of bouncing the prompt with a toast. The send
        // finally block auto-submits the queue head when the outer
        // turn completes.
        this.queuePromptFor(this.currentSessionId, text)
        return
      }

      this.error = null
      // Per-session state (Slice A) — gate the active session, not a flat
      // global. `isStreaming` is intentionally NOT set true here — the SSE
      // stream hasn't actually started yet, and `isStreaming` retains its
      // precise meaning ("SSE chunks are arriving"). The user-facing
      // "agent is working" indicator surfaces while either flag is true
      // (see ChatView.vue v-if), so the affordance is continuously
      // visible from this point through to the post-send reconcile
      // completing — a regression in either gate would otherwise hide
      // the indicator on backends that emit no intermediate `content`
      // events.
      const initialSessionId = this.currentSessionId
      this.setSessionStreaming(initialSessionId, { isLoading: true, isStreaming: false })

      // M-04 (Vue Web Frontend Master Bug Report, May 2026) — capture
      // the targeted session-id at function scope so the finally block
      // can clear flags on the session this send actually targeted,
      // independent of any concurrent navigation. Pre-fix the finally
      // read `this.currentSessionId ?? initialSessionId`, which:
      //   - Cleared the WRONG session when the user navigated to a
      //     different existing session mid-flight (`?? initialSessionId`
      //     does not engage because currentSessionId is non-null).
      //   - Missed the lazy-create branch's freshly-minted session-id
      //     when navigation away happened before finally fired.
      // The captured id is reassigned inside the try block if the
      // lazy-create branch synthesises a new session — that's the
      // single canonical target for both flag-clear and queued-prompt
      // drain.
      let targetedSessionId: string | null = initialSessionId

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
          // M-04 (Vue Web Frontend Master Bug Report, May 2026) — keep
          // the function-scoped targetedSessionId in lock-step with the
          // lazy-created session id so the finally block clears the
          // freshly-minted session, not whichever session
          // currentSessionId happens to point at after navigation.
          targetedSessionId = sessionId
          // Per-session state (Slice A) — the in-flight slot was attached
          // to the prior null id; transfer it to the freshly-created
          // session so the gate / indicator continue to read true.
          this.setSessionStreaming(sessionId, { isLoading: true, isStreaming: false })
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

        // Mirror the function-scoped capture: when the send entered with
        // a pre-existing session (the if !sessionId branch did not run),
        // targetedSessionId is still the initialSessionId — but if the
        // initialSessionId was null (typical lazy-create), the assignment
        // above covered it. This redundant assignment is the
        // belt-and-braces safety against future refactors that introduce
        // a third entry path.
        targetedSessionId = sessionId

        const capturedSessionId = sessionId

        // Phase 3 of "Turn-Based Post-Then-Poll Architecture (May 2026)":
        // POST first, then branch on turn_id. Pre-Phase-3 the SSE was
        // opened BEFORE the POST so the EventSource was ready when the
        // backend's broker started publishing chunks; that path produced
        // the 24-hour SSE live-render bug class (chunks raced the POST,
        // the FE missed chunks between session.create and first SSE
        // accept, and a session switch mid-stream stranded chunks on
        // the wrong session). Phase 3 replaces it with HTTP polling
        // off the backend-minted turn_id — deterministic, stateless,
        // and immune to broker subscription races.
        //
        // The legacy SSE path remains wired but ONLY runs when the
        // server response lacks turn_id (operator rollback / pre-Phase-2
        // server). This is the documented "defence-in-depth" fallback —
        // the FE keeps working if Phase 2 is rolled back server-side
        // without redeploying the FE.
        const sentResult =
          attachmentIds.length > 0
            ? await sendSessionMessage(sessionId, text, { attachmentIds })
            : await sendSessionMessage(sessionId, text)

        // Defensive unwrap. The api function returns
        // { turnId: string | null, snapshot: Session }. Pre-Phase-3
        // test mocks (which return a flat Session shape directly via
        // mockImplementationOnce) get normalised here so existing
        // suites that haven't been updated still find a usable shape
        // and fall through the legacy SSE path. New Phase-3 suites
        // supply the wrapper explicitly via
        // mockResolvedValueOnce({turnId, snapshot}).
        const sentResultAny = sentResult as unknown as {
          turnId?: string | null
          snapshot?: Session
        } & Session
        const turnId =
          typeof sentResultAny?.turnId === 'string' && sentResultAny.turnId.length > 0
            ? sentResultAny.turnId
            : null
        const sentSession: Session = (sentResultAny?.snapshot ?? sentResultAny) as Session

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

        if (turnId !== null) {
          // Phase 3/4 — TURN-ID POLL PATH (the sole live path post-
          // Commit-2). pollTurnUntilTerminal long-polls
          // GET /sessions/{id}/turns/{turn_id}?wait=true&since=N and
          // surfaces engine-emitted chunks via the per-turn poll-diff
          // branches (provider_changed, context_usage, context_compacted,
          // gate_failed, provider_quota, critical_error).
          await this.pollTurnUntilTerminal(capturedSessionId, turnId)
        } else {
          // Defence-in-depth: a server that returns no turn_id is
          // structurally broken post-Commit-2 (every POST mints a
          // Turn). Surface the error so the user retries / reloads.
          this.error =
            'Server returned no turn_id — long-poll unavailable. Reload the page or retry.'
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
        // Per-session state (Slice A) — clear flags on the session this
        // send targeted (resolved either at sendMessage entry or via the
        // lazy-create branch). If the user has navigated to a different
        // session in the meantime, that session's slot is unaffected.
        //
        // Phase-4-Commit-2: pollTurnUntilTerminal awaited above drove
        // the turn to a terminal status via long-poll; no SSE was
        // opened so there is nothing to tear down here.
        //
        // M-04 (Vue Web Frontend Master Bug Report, May 2026): use the
        // function-scoped targetedSessionId (captured at request-start
        // and reassigned across the lazy-create branch) rather than
        // this.currentSessionId — the live currentSessionId may have
        // moved to a different session due to user navigation between
        // send-start and finally, in which case the prior
        // `this.currentSessionId ?? initialSessionId` would clear the
        // wrong session's flags. The captured id is the canonical
        // recipient of the send and the canonical owner of the cleanup.
        const completedSessionId = targetedSessionId ?? initialSessionId
        this.setSessionStreaming(completedSessionId, { isLoading: false, isStreaming: false })
        // Streaming Coherence Slice E (May 2026) — queued-prompt drain.
        // After the outer turn completes, fire the next queued prompt
        // for THIS session (not the active session — the user may have
        // navigated). The recursion is bounded: each queued prompt
        // either succeeds (drain continues) or fails (queue retains
        // remaining prompts; user can retry). Microtask-scheduled so
        // the finally block resolves before the next send begins —
        // observers of the streaming flag transition see false before
        // it goes back to true.
        if (completedSessionId) {
          const nextPrompt = this.shiftQueuedPromptFor(completedSessionId)
          if (nextPrompt !== null) {
            void Promise.resolve().then(() => {
              // Re-check the session is still active before firing — a
              // mid-flight session deletion or navigation can leave the
              // queue stranded; the user picking the session up again
              // can re-trigger the drain.
              if (this.currentSessionId === completedSessionId) {
                void this.sendMessage(nextPrompt)
              } else {
                // Restore the prompt to the head of the queue so it is
                // not lost on background-session drain.
                const remaining = this.queuedPrompts[completedSessionId] ?? []
                this.queuedPrompts[completedSessionId] = [nextPrompt, ...remaining]
              }
            })
          }
        }
      }
    },

    // Slice G — Escape-twice cancel cascade (Streaming Coherence May 2026).
    // Handles escape key presses for cancelling in-flight streaming turns.
    // First press increments the count; second press within 600ms fires the
    // DELETE /api/v1/sessions/{id}/stream endpoint and closes the EventSource.
    async handleEscapeKey(): Promise<void> {
      const sessionId = this.currentSessionId
      if (!sessionId) return

      // Gate: cancel if the turn is in-flight at all — either loading
      // (POST resolved, long-poll waiting for the first chunk) OR
      // streaming (chunks arriving). M-05 (Vue Web Frontend Master Bug
      // Report, May 2026): pre-fix this gated exclusively on
      // `state.isStreaming`, which only flips true once the first
      // content chunk arrives (chatStore.ts applyContentEvent path).
      // The pre-stream loading window — `isLoading: true,
      // isStreaming: false` — silently dropped the gesture; the user
      // hit Escape-Escape and nothing happened. Widening the gate to
      // either flag covers the full in-flight lifecycle; the teardown
      // (setSessionStreaming false/false) is consistent across both
      // windows so behaviour after cancel is unchanged.
      const state = this.streamingFor(sessionId)
      if (!state.isStreaming && !state.isLoading) return

      this.escapePressCount++

      if (this.escapePressCount === 1) {
        // First press: set up a 600ms window for the second press
        if (this.escapeTimeoutId !== null) {
          clearTimeout(this.escapeTimeoutId)
        }
        this.escapeTimeoutId = setTimeout(() => {
          // Window closed without a second press; reset
          this.escapePressCount = 0
          this.escapeTimeoutId = null
        }, 600)
      } else if (this.escapePressCount >= 2) {
        // Second press within 600ms: cancel the turn
        if (this.escapeTimeoutId !== null) {
          clearTimeout(this.escapeTimeoutId)
          this.escapeTimeoutId = null
        }
        this.escapePressCount = 0

        // Phase-4-Commit-2 of "Turn-Based Post-Then-Poll Architecture
        // (May 2026)" retired the DELETE /api/v1/sessions/{id}/stream
        // cancel endpoint along with the SSE handler that owned it.
        // A server-side DELETE /api/v1/sessions/{id}/turns/{turn_id}
        // cancel endpoint is a follow-on (tracked separately). For
        // now Escape-Escape clears the per-session UI gate immediately
        // so the composer flips back to Send; the long-poll itself
        // continues until the engine drains, and any chunks that
        // arrive land on a now-cleared streaming slot (no user-visible
        // re-render). The Turn registry's terminal state still resolves
        // correctly when the engine completes.
        this.setSessionStreaming(sessionId, { isLoading: false, isStreaming: false })
      }
    },

    // Re-arm the stall watchdog whenever there is fresh streaming activity.
    // Called from applyContentEvent on every chunk to indicate liveness; the
    // initial arm happens implicitly inside sessionStream.connect. The 60s
    // window is intentionally generous — agents can sit thinking on a slow
    // tool call without producing chunks; we only want to trip on "actually
    // dead" streams, not "agent is busy".
    //
    // armStallWatchdog — retired in Phase-4-Commit-2 of "Turn-Based
    // Post-Then-Poll Architecture (May 2026)" alongside the SSE
    // EventSource it used to arm. The long-poll path has its own
    // stall behaviour built in (the server's 25s long-poll timeout
    // releases the FE, which immediately reissues — no client-side
    // watchdog needed). Retained as a no-op so existing test seam
    // call sites compile.
    armStallWatchdog(_sessionId?: string): void {
      // no-op
    },

    // Stall trip handler. Stream stalled — unsticky the input gate so the
    // user can recover without reloading the page. The error footer surfaces
    // the cause; if no chunks arrived at all the in-flight assistant bubble
    // (if any) stays in-place but is no longer locked.
    //
    // sessionId is the session whose SSE armed the watchdog. Pre-May-2026
    // this handler called reconcileFromBackend(sessionId) on every trip to
    // mask "stream completed server-side without [DONE]" failures by
    // refetching the canonical session state. That masked the underlying
    // engine-hang bug (May 2026 mid-thinking-halt: engine parked on
    // <-providerChunks with no Done emitted). The engine now emits a
    // synthetic Done inside 60s via engineStreamIdleTimeout
    // (internal/engine/engine.go), so a watchdog trip on the frontend
    // means either:
    //
    //   1. A genuinely dead network (rare; the user reloads anyway), OR
    //   2. A regression in the engine watchdog (we want this to surface
    //      loudly, not be masked by a silent backend refetch).
    //
    // Keeping the trip log + the error footer + the gate clear lets a
    // future regression announce itself. The reconcile call was removed
    // because it papered over the bug class the engine watchdog now
    // closes; see commits 3408c793 (engine watchdog) and the chat plan
    // in the FlowState vault.
    handleStreamStall(sessionId?: string): void {
      this.error = 'Response stalled — the stream produced no activity for 65 seconds. You can send another message.'
      // Per-session state (Slice A) — clear flags on the session that
      // armed the watchdog. Pre-slice this cleared the global flag.
      this.setSessionStreaming(sessionId ?? this.currentSessionId, { isLoading: false, isStreaming: false })
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

    // applyContentEvent — Phase-4-Commit-2 of "Turn-Based Post-Then-Poll
    // Architecture (May 2026)" retired the production SSE dispatch
    // pipeline that fed this switch. The function is preserved as a
    // TEST-ONLY seam so the existing handler-dispatch coverage in
    // chatStore.test.ts continues to exercise the handler functions
    // (handleContentChunk, handleStreamDone, handleProviderChangedEvent,
    // handleContextUsageEvent, handleContextCompactedEvent, etc.) via
    // a single payload-shaped entry point. Production code never calls
    // this — every poll-diff site routes through the handler functions
    // directly (see pollTurnUntilTerminal above).
    applyContentEvent(payload: string, capturedSessionId?: string): void {
      const event: SSEEvent = parseSSEPayload(payload)
      const targetSessionId = capturedSessionId ?? this.currentSessionId ?? undefined

      // Streaming Coherence Slice F (May 2026) — record the latest
      // engine heartbeat phase BEFORE arming the watchdog so the
      // adaptive threshold (45/120/180/300s) is in effect when the
      // arm runs below.
      if (event.kind === 'streaming_heartbeat' && targetSessionId) {
        this.streamingPhase[targetSessionId] = event.phase || ''
        // UI Parity PR5 (May 2026) — live token counter. Record
        // the engine's reported cumulative output_tokens for this
        // session AND compute the tokens-per-second rate from the
        // delta against the previous tick.
        const prevCount = this.tokenCountBySession[targetSessionId]
        const prevAt = this.lastHeartbeatAtBySession[targetSessionId]
        const nowMs = Date.now()
        this.tokenCountBySession[targetSessionId] = event.tokenCount
        if (typeof prevCount === 'number' && typeof prevAt === 'number') {
          const deltaTokens = event.tokenCount - prevCount
          const deltaSeconds = (nowMs - prevAt) / 1000
          if (deltaSeconds > 0 && deltaTokens > 0) {
            this.tokensPerSecondBySession[targetSessionId] = Math.round(deltaTokens / deltaSeconds)
          } else {
            this.tokensPerSecondBySession[targetSessionId] = 0
          }
        } else {
          this.tokensPerSecondBySession[targetSessionId] = 0
        }
        this.lastHeartbeatAtBySession[targetSessionId] = nowMs
      }

      this.armStallWatchdog(targetSessionId)

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
        case 'tool_error':
          this.handleToolErrorEvent({ content: event.content })
          return
        case 'delegation':
          this.applyDelegationEvent(event.raw)
          return
        case 'error':
          this.error = event.error
          return
        case 'stream_critical':
          this.criticalError = {
            message: event.error,
            correlationId: event.correlationId,
          }
          return
        case 'harness_retry':
        case 'harness_attempt_start':
        case 'harness_complete':
        case 'harness_critic_feedback':
          return
        case 'thinking':
          this.handleThinkingEvent({ content: event.content })
          return
        case 'provider_changed':
          this.handleProviderChangedEvent({
            from: event.from,
            to: event.to,
            fromProvider: event.fromProvider,
            fromModel: event.fromModel,
            toProvider: event.toProvider,
            toModel: event.toModel,
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
          }, targetSessionId)
          return
        case 'context_compacted':
          this.handleContextCompactedEvent({
            sessionId: event.sessionId,
            originalTokens: event.originalTokens,
            summaryTokens: event.summaryTokens,
            trigger: event.trigger,
          })
          return
        case 'provider_quota':
          useQuotaStore().applyProviderQuotaEvent(event)
          return
        case 'streaming_heartbeat':
          return
        case 'gate_failed':
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
      // Streaming Coherence Slice C (May 2026) — seal ALL running
      // assistant + delegation rows on [DONE], not just the last
      // reverse-find. Pre-slice the loop kept any but the most recent
      // running row in a permanent "running" state when an intermediate
      // tool round closed; the user observed delegation cards stuck on
      // an in-progress spinner long after the parent turn completed.
      let sealedAny = false
      for (const message of this.messages) {
        if (message.status === 'running' &&
            (message.role === 'assistant' || message.role === 'delegation_started')) {
          message.status = 'completed'
          sealedAny = true
        }
      }
      // Streaming Coherence Slice C — empty-turn placeholder. When
      // [DONE] arrives without any running assistant having been
      // created (engine-side `synthesizePlaceholderAssistant` did not
      // emit a placeholder for whatever reason — degraded provider,
      // legacy session, race), push an empty_turn placeholder so the
      // user sees the silence rather than waiting for the 60s
      // watchdog to trip. Bug fix #27 (May 11 2026) wired the matching
      // MessageBubble v-else-if branch (isEmptyTurn) so the placeholder
      // now surfaces the "Reply didn't come through" soft-error
      // affordance — pre-fix the placeholder was pushed but no render
      // branch consumed it, so true empty turns were silently swallowed.
      if (!sealedAny) {
        const lastMsg = this.messages[this.messages.length - 1]
        const lastIsUser = lastMsg && lastMsg.role === 'user'
        if (lastIsUser || this.messages.length === 0) {
          // No assistant artefact landed. Render an empty-turn
          // placeholder so the chat thread closes cleanly.
          this.messages.push({
            id: `empty-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
            role: 'assistant',
            content: '',
            timestamp: new Date().toISOString(),
            status: 'completed',
            stopReason: 'empty_turn',
          })
        }
      }
      // Streaming Coherence Slice D (May 2026) — activity-indicator
      // continuity. Pre-slice this method flipped isStreaming=false on
      // EVERY [DONE] sentinel, including the intermediate Done events
      // engine pipelines emit between tool rounds. The user observed
      // the activity indicator flickering off-and-on between rounds.
      //
      // The simpler shape from the Streaming Liveness ADR: keep
      // isStreaming true until the *outer* turn completes. The send
      // finally block clears both isLoading and isStreaming when the
      // post-await reconcile resolves; for intermediate DONEs we no
      // longer touch the streaming flag.
      //
      // Phase-4-Commit-2 retired the SSE EventSource and its
      // armed-watchdog — the long-poll path has no client-side
      // watchdog to clear.
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
      // Per-session state (Slice A) — set on the current session's slot.
      this.setSessionStreaming(this.currentSessionId, { isStreaming: true })
    },

    /**
     * Drop #2 — Thinking handler.
     *
     * Two parallel writes per chunk, anchored to the canonical backend
     * representation:
     *
     *   1. A `role: 'thinking'` running message accumulates the chunk
     *      content. This mirrors how the engine persists thinking
     *      end-of-turn — `appendSessionMessage` writes a separate
     *      `role: 'thinking'` row whose `content` carries the full
     *      reasoning text. MessageBubble's `isThinking` branch
     *      (`role === 'thinking'` + non-empty content) renders this via
     *      `ThinkingPanel`, so the live stream now surfaces reasoning
     *      tokens as they arrive instead of waiting for the post-stream
     *      reload to materialise the canonical thinking row.
     *
     *      Bug Hunt (May 2026) — pre-fix this handler ONLY wrote to
     *      `target.thinkingContent` on the assistant placeholder, which
     *      the `assistant`-role render gates explicitly skip ("MUST NOT
     *      be rendered as the visible reply" per the original Drop #2
     *      contract). For z.ai glm-4.6 / OpenAI o1 / any reasoning
     *      provider whose visible response IS the reasoning text, the
     *      user saw a frozen empty bubble through the entire turn —
     *      until a reload pulled in the canonical thinking row.
     *
     *   2. The legacy `target.thinkingContent` accumulation on the
     *      assistant placeholder is preserved unchanged so the Track B
     *      "disclose reasoning on demand" UI (when it ships) reads the
     *      same field. Future Track B work is purely additive: it can
     *      either keep reading `target.thinkingContent` OR pivot to the
     *      thinking row's `content`. Both carry the same string.
     *
     * The assistant placeholder is ALSO still created/refreshed here so
     * `handleContentChunk` has a `running` target to extend when
     * `"type":"content"` chunks arrive (mixed thinking+content turns).
     */
    handleThinkingEvent(info: { content?: unknown }): void {
      if (typeof info.content !== 'string' || info.content.length === 0) {
        return
      }

      // (1) Thinking row — the load-bearing live-render surface.
      // Find the most-recent running thinking row; create one if none
      // exists. Pre-fix this row was never created live, so the user
      // saw nothing on thinking-only models until a refresh hydrated
      // the canonical history. status='running' is the load-bearing
      // discriminator: reconcileFromBackend's in-flight preservation
      // filter keeps this row alive across the post-POST reconcile so
      // it isn't wiped mid-stream by the canonical history (which
      // doesn't have the row yet).
      let thinkingRow = [...this.messages].reverse().find(
        (m) => m.role === 'thinking' && m.status === 'running',
      )
      if (!thinkingRow) {
        thinkingRow = {
          id: `thinking-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
          role: 'thinking',
          content: '',
          timestamp: new Date().toISOString(),
          status: 'running',
        }
        this.messages.push(thinkingRow)
      }
      thinkingRow.content = (thinkingRow.content ?? '') + info.content

      // (2) Legacy assistant.thinkingContent accumulation — preserved
      // for Track B compatibility. Reuse / create the assistant
      // placeholder so handleContentChunk has a target for any
      // subsequent `"type":"content"` chunk (mixed thinking+content
      // turns like Anthropic with extended-thinking enabled).
      let assistantTarget = [...this.messages].reverse().find(
        (m) => m.role === 'assistant' && m.status === 'running',
      )
      if (!assistantTarget) {
        assistantTarget = {
          id: `streaming-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
          role: 'assistant',
          content: '',
          timestamp: new Date().toISOString(),
          status: 'running',
        }
        this.messages.push(assistantTarget)
      }
      assistantTarget.thinkingContent = (assistantTarget.thinkingContent ?? '') + info.content

      // Per-session state (Slice A) — set on the current session's slot.
      this.setSessionStreaming(this.currentSessionId, { isStreaming: true })
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
    }, capturedSessionId?: string): void {
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

      // Phase-5 §1c-β idempotency gate. The transitional state has two
      // callers for the same figure — the SSE branch at
      // chatStore.ts:2795-2806 and the new pollTurnUntilTerminal poll-
      // diff caller. Serialise the four-field tuple and short-circuit
      // when the prior call matches. Mirrors lastProviderChangeKey's
      // same-pair dedup pattern for failover toasts.
      //
      // The key reflects the observable figure only — capturedSessionId
      // is NOT included because a cross-session figure update is a
      // separate user-facing action (return to a stale session re-hydrates
      // the chip from contextUsageBySession, not via this handler).
      const key = `${info.inputTokens}:${info.outputReserve}:${info.limit}:${info.percentage}`
      if (key === this.lastContextUsageKey) {
        return
      }
      this.lastContextUsageKey = key

      const figure = {
        inputTokens: info.inputTokens,
        outputReserve: info.outputReserve,
        limit: info.limit,
        percentage: info.percentage,
      }

      // Bug Hunt (May 2026) — record under the chunk's captured
      // session id so cross-session navigation can read each session's
      // own most-recent figure. capturedSessionId is the canonical
      // key; fall back to currentSessionId for legacy / PATCH callers
      // (applyContextUsageFromSession) that don't thread the id.
      const sessionKey = capturedSessionId ?? this.currentSessionId
      if (sessionKey) {
        this.contextUsageBySession[sessionKey] = figure
      }

      // The active chip slot mirrors the figure only when this
      // emission is for the currently-viewed session. An emission for
      // an inactive session updates the per-session map (above) so
      // returning to that session re-hydrates the chip via
      // loadSessionMessages, but it does NOT clobber the active
      // session's chip with a foreign figure.
      const isForActiveSession = !capturedSessionId || capturedSessionId === this.currentSessionId
      if (isForActiveSession) {
        this.currentContextUsage = figure
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

      // Phase-5 §1c-γ idempotency gate. The transitional 1c-γ state has
      // two callers for the same compaction figure — the SSE branch at
      // chatStore.ts:~2997 and the new pollTurnUntilTerminal poll-diff
      // caller. Serialise the four-field tuple and short-circuit when
      // the prior call matches. Mirrors lastContextUsageKey's pattern
      // for the chip-flash dedup. Without this gate, the count would
      // increment twice (chip flashes twice) for one real event.
      //
      // The key includes sessionId only when it's non-empty — the SSE
      // wire always carries it; legacy callers without it fall back to
      // a fingerprint-only key.
      const compactionKey = `${info.sessionId ?? ''}:${info.originalTokens}:${info.summaryTokens}:${info.trigger ?? ''}`
      if (compactionKey === this.lastCompactionEventKey) {
        return
      }
      this.lastCompactionEventKey = compactionKey

      this.compactionEventCount += 1
      this.lastCompaction = {
        originalTokens: info.originalTokens,
        summaryTokens: info.summaryTokens,
        tokensSaved: info.originalTokens - info.summaryTokens,
        at: Date.now(),
        trigger: info.trigger ?? '',
      }
    },

    handleProviderChangedEvent(info: {
      from?: unknown
      to?: unknown
      fromProvider?: unknown
      fromModel?: unknown
      toProvider?: unknown
      toModel?: unknown
      reason?: unknown
    }): void {
      const to = typeof info.to === 'string' ? info.to : ''
      const from = typeof info.from === 'string' ? info.from : ''
      const fromProviderField =
        typeof info.fromProvider === 'string' ? info.fromProvider : ''
      const fromModelField = typeof info.fromModel === 'string' ? info.fromModel : ''
      const toProviderField = typeof info.toProvider === 'string' ? info.toProvider : ''
      const toModelField = typeof info.toModel === 'string' ? info.toModel : ''
      const reason = typeof info.reason === 'string' ? info.reason : ''

      // Phase-5 §1c-α — idempotency guard. The transitional 1c surface has
      // TWO callers for the same pair: the existing SSE handler at the
      // switch case (chatStore.ts ~line 2740) and the new poll-diff caller
      // in pollTurnUntilTerminal. When both fire for the SAME transition
      // (which they will during 1c-α), the second call must NOT re-toast
      // or re-mutate state — the user would otherwise see two back-to-
      // back "Model changed" toasts for one transition. The dedup key is
      // the target pair: if it matches lastProviderChangeKey we already
      // processed this transition and short-circuit.
      //
      // Computed BEFORE the parse/mutation block so the short-circuit
      // applies to both the split-field and the joined-`to` paths.
      const candidateKey = (() => {
        if (toProviderField !== '' || toModelField !== '') {
          return `${toProviderField}+${toModelField}`
        }
        if (to.length > 0) {
          const sep = to.indexOf('+')
          if (sep === -1) return `${to}+`
          return `${to.slice(0, sep)}+${to.slice(sep + 1)}`
        }
        return ''
      })()
      if (
        candidateKey !== '' &&
        candidateKey === this.lastProviderChangeKey
      ) {
        // We already processed this exact transition (SSE branch or a
        // prior poll-diff fire). Both branches stay live during 1c-α; the
        // dedup gate keeps the user-visible behaviour single-fire.
        return
      }

      // Prefer the split fields when the wire carries them — they skip
      // the "+" parse hop and the off-by-one bugs around model ids that
      // themselves contain "+" (rare; openrouter). Fall back to
      // splitting the joined `to` on "+" for legacy emitters that
      // haven't migrated yet.
      let newProvider = ''
      let newModel = ''
      if (toProviderField !== '' || toModelField !== '') {
        newProvider = toProviderField
        newModel = toModelField
        this.currentProviderId = newProvider
        this.currentModelId = newModel
        this.lastProviderChangeKey = `${newProvider}+${newModel}`
      } else if (to.length > 0) {
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
        // Prefer the split `fromModel` field; fall back to splitting
        // the joined `from` on "+" for legacy emitters.
        if (fromModelField !== '') return fromModelField
        if (fromProviderField !== '') return fromProviderField
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
      // Per-session SSE singleton (Slice B) — drop the specific session.
      // Phase-4-Commit-2 retired the SSE stream; revertToMessage no
      // longer needs to disconnect an EventSource. The per-session
      // streaming gate is cleared so the composer flips back to Send.
      this.setSessionStreaming(this.currentSessionId, { isLoading: false, isStreaming: false })
      if (!messageId.startsWith('temp-')) {
        await truncateSessionMessages(this.currentSessionId, messageId)
      }
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

      // Route todo-tool results into the todoStore. The agent emits the
      // full todo array on every `todowrite` (initial list) AND every
      // `todo_update` (per-status-flip) call, so the slice for the active
      // session is replaced rather than merged — matching the TUI which
      // re-renders the full list on every emission (see
      // internal/tui/intents/chat/intent.go:4740-4748 for the parity
      // counterpart). The two names share a single ingestion path because
      // the wire shape (and the backend Output contract — see
      // internal/tool/todo/update.go:148-152) is identical.
      if (this.lastToolName !== null && TODO_TOOL_NAMES.has(this.lastToolName) && this.currentSessionId) {
        const todoStore = useTodoStore()
        todoStore.ingestToolResult(this.currentSessionId, content)
      }
      // Clear the gate so a stray subsequent tool_result doesn't double-route.
      this.lastToolName = null
    },

    /**
     * handleToolErrorEvent — flips the most recent running tool_result
     * message to status='error' and populates the error content. Mirrors
     * handleToolResultEvent's "find most recent running" semantics so the
     * tool_call → tool_error pairing matches the wire-time ordering.
     *
     * Gap 2 (May 2026): added so the Go SSE pipeline's new `tool_error`
     * typed event can surface in-stream tool failures. Without this the
     * frontend's handleToolResultEvent always stamped status='completed',
     * which hid live errors behind the post-stream history reconcile.
     *
     * The status='error' transition is the load-bearing field — the
     * ToolBubble's cardDefaultOpen watcher reacts to it and force-opens
     * the matching card, so the user sees the failure immediately rather
     * than needing to click the chevron.
     *
     * Defensive: a stray tool_error with no running target is a no-op
     * rather than a throw. Stale events (e.g. arriving after a watchdog
     * cancel) must not break the chat thread.
     */
    handleToolErrorEvent(info: { content?: unknown }): void {
      const target = [...this.messages].reverse().find(
        (message) => message.role === 'tool_result' && message.status === 'running',
      )

      const content = String(info.content ?? '')

      if (target) {
        target.content = content
        target.status = 'error'
      }

      // Clear the gate so a subsequent tool_result/tool_error doesn't
      // double-route to the same target (mirrors handleToolResultEvent).
      this.lastToolName = null
    },

    /**
     * compactCurrentSession is the action behind the /compact slash
     * command (renamed from /compress in the May 2026 OpenCode-shape
     * rename — was Deliverable 3 of the May 2026 context-accuracy
     * bundle). Force-fires the engine's L2 auto-compactor against the
     * current session via POST /api/v1/sessions/{id}/compact.
     *
     * Branches on the server's `fired` discriminant for the toast
     * copy:
     *   - fired=true  → "Context compacted." (the chip's existing
     *     flash + tooltip path picks up the ContextCompactedEvent
     *     forwarded over SSE — no need to duplicate the saved-tokens
     *     figure in the toast).
     *   - fired=false → "Nothing to compact." (the session is empty,
     *     the layer is disabled, or the summariser declined; the
     *     toast keeps the slash command's outcome visible rather
     *     than appearing to do nothing).
     *
     * Errors are caught and surfaced as a toast so the slash command
     * never propagates a rejection back into the composer — a /compact
     * failure should not put the chat input into an error state.
     *
     * No-op when no session is currently active (the user has not
     * created or selected one yet); silent rather than toast-warning
     * because the precondition is obvious in the UI.
     */
    async compactCurrentSession(): Promise<void> {
      const sessionId = this.currentSessionId
      if (!sessionId) {
        return
      }
      try {
        const result = await compactSessionNow(sessionId)
        if (result.fired) {
          showToast({
            title: 'Compacted',
            message: 'Context compacted.',
            variant: 'default',
            duration: 4000,
          })
        } else {
          showToast({
            title: 'Nothing to compact',
            message: 'Nothing to compact yet — try again after more turns.',
            variant: 'default',
            duration: 4000,
          })
        }
      } catch (err) {
        showToast({
          title: 'Compaction failed',
          message: err instanceof Error ? err.message : 'Compact request failed.',
          variant: 'error',
          duration: 5000,
        })
      }
    },
  },
})
