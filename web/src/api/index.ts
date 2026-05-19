import type {
  ChatRequest,
  SSEChunk,
  Agent,
  Session,
  SessionSummary,
  Message,
  Model,
  ModelsResponse,
  Swarm,
} from '@/types'
import { parseError } from '@/lib/parseError'
import { isAllowedApiHost } from '@/lib/apiHostAllowlist'
import { withCsrfHeader } from '@/lib/csrf'

// PR3 / C8 — auth coordinated change. Every fetch() in this module
// adds `credentials: 'include'` so the browser sends the
// `flowstate_session` cookie (and the `_csrf` cookie) cross-origin and
// same-origin alike. Unsafe methods (POST/PUT/PATCH/DELETE) additionally
// inject the `X-CSRF-Token` header from the _csrf cookie via
// withCsrfHeader().
//
// Plan reference: FlowState API Auth Track (May 2026) §"Migration Path"
// + §"Wire Protocol" CSRF section. Flag-gated server-side at PR3/C7 —
// when features.auth_v1 is false (PR2/PR3 ship state), the server's
// registerProtected helpers no-op so the extra header / cookie is
// harmless. When PR5 flips the flag, the same call sites work without
// further change — the load-bearing PR3 invariant.

// CREDENTIALS_INCLUDE is the shared RequestCredentials literal so the
// constant is referenced consistently across every fetch site.
const CREDENTIALS_INCLUDE: RequestCredentials = 'include'

const BASE = '/api'
const API_HOST_STORAGE_KEY = 'flowstate-api-host'

/**
 * getBaseURL returns the API base URL, validated against the host
 * allowlist. A localStorage value that fails the allowlist (e.g. injected
 * by an XSS vector or a malicious bookmarklet) is removed and the safe
 * BASE default is returned. See apiHostAllowlist.ts for the policy and
 * threat model.
 */
function getBaseURL(): string {
  let stored: string | null = null
  try {
    stored = localStorage.getItem(API_HOST_STORAGE_KEY)
  } catch {
    // localStorage unavailable (private mode, SSR) — fall through to default.
    return BASE
  }
  if (!stored) return BASE
  if (!isAllowedApiHost(stored)) {
    // Hostile or malformed override — clear it and warn (validateApiHost
    // would also warn, but we want to log AND remove). The next page load
    // sees the BASE default; in-flight requests in the same tick still
    // see BASE because we returned it below.
    try {
      localStorage.removeItem(API_HOST_STORAGE_KEY)
    } catch {
      // best effort — fall through
    }
    // eslint-disable-next-line no-console
    console.warn(
      '[flowstate] cleared API host override that failed allowlist policy:',
      stored,
    )
    return BASE
  }
  return stored
}

export function joinBaseURL(path: string): string {
  const base = getBaseURL().replace(/\/$/, '')
  return `${base}${path}`
}

export async function fetchAgents(): Promise<Agent[]> {
  const res = await fetch(joinBaseURL('/agents'), { credentials: CREDENTIALS_INCLUDE })
  if (!res.ok) {
    throw new Error(`Failed to fetch agents: ${res.statusText}`)
  }
  return res.json()
}

export async function fetchAgent(id: string): Promise<Agent> {
  const res = await fetch(joinBaseURL(`/agents/${encodeURIComponent(id)}`), {
    credentials: CREDENTIALS_INCLUDE,
  })
  if (!res.ok) {
    throw new Error(`Failed to fetch agent ${id}: ${res.statusText}`)
  }
  return res.json()
}

/**
 * fetchSwarms returns the registered swarms in stable id order. Used
 * by the chat store on bootstrap so the MessageInput's @-picker can
 * surface swarms alongside agents — same shape as fetchAgents, just a
 * different endpoint and entity.
 *
 * The backend coerces an unconfigured registry to `[]` so callers
 * never see `null`; the array form is contractual (see the Go-side
 * GET /api/swarms specs in internal/api/server_test.go).
 */
export async function fetchSwarms(): Promise<Swarm[]> {
  const res = await fetch(joinBaseURL('/swarms'), { credentials: CREDENTIALS_INCLUDE })
  if (!res.ok) {
    throw new Error(`Failed to fetch swarms: ${res.statusText}`)
  }
  return res.json()
}

export async function postChat(
  agentId: string,
  message: string,
  onChunk: (content: string) => void
): Promise<string> {
  const res = await fetch(joinBaseURL('/chat'), {
    method: 'POST',
    headers: withCsrfHeader({ 'Content-Type': 'application/json' }),
    credentials: CREDENTIALS_INCLUDE,
    body: JSON.stringify({ agent_id: agentId, message } as ChatRequest),
  })

  if (!res.ok) {
    throw new Error(await parseError(res))
  }

  if (!res.body) {
    throw new Error('Response body is null')
  }

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let content = ''

  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      const chunk = decoder.decode(value, { stream: true })
      const lines = chunk.split('\n')
      for (const line of lines) {
        if (!line.trim() || !line.startsWith('data: ')) continue
        const data = line.slice(6)
        if (data === '[DONE]') continue
        try {
          const parsed: SSEChunk = JSON.parse(data)
          if (parsed.content) {
            content += parsed.content
            onChunk(parsed.content)
          } else if (parsed.error) {
            throw new Error(parsed.error)
          }
        } catch {
          // ignore parse errors for non-JSON lines
        }
      }
    }
  } finally {
    reader.releaseLock()
  }

  return content
}

export async function fetchSwarmEvents(): Promise<unknown[]> {
  const res = await fetch(joinBaseURL('/swarm/events'), { credentials: CREDENTIALS_INCLUDE })
  if (!res.ok) {
    throw new Error(`Failed to fetch swarm events: ${res.statusText}`)
  }
  return res.json()
}

export async function fetchSessions(): Promise<SessionSummary[]> {
  const res = await fetch(joinBaseURL('/v1/sessions'), { credentials: CREDENTIALS_INCLUDE })
  if (!res.ok) {
    throw new Error(`Failed to fetch sessions: ${res.statusText}`)
  }
  return res.json()
}

export async function createSession(agentId: string): Promise<Session> {
  const res = await fetch(joinBaseURL('/v1/sessions'), {
    method: 'POST',
    headers: withCsrfHeader({ 'Content-Type': 'application/json' }),
    credentials: CREDENTIALS_INCLUDE,
    body: JSON.stringify({ agent_id: agentId }),
  })
  if (!res.ok) {
    throw new Error(`Failed to create session: ${res.statusText}`)
  }
  return res.json()
}


export interface SendSessionMessageOptions {
  attachmentIds?: string[]
  signal?: AbortSignal
}

/**
 * SendSessionMessageResult is the Phase-3 wire-adapted shape returned by
 * sendSessionMessage. Phase 2 (server commit 9e398807) introduced two
 * additive fields on the POST /api/v1/sessions/{id}/messages response —
 * `turn_id` and `snapshot` — alongside the legacy flat Session shape.
 * Phase 3 (this commit) surfaces both to the chat store so the active-
 * send path can drive HTTP polling on `turn_id` instead of opening an
 * SSE stream that drops chunks in production.
 *
 * Fields:
 *   - turnId  — the freshly-minted Turn UUID, or null when the server
 *               response lacks turn_id (pre-Phase-2 server or operator
 *               rollback). The chat store branches on null to take the
 *               legacy SSE path, preserving the rollback contract.
 *   - snapshot — the synchronously-returned Session shape. The Phase 2
 *                server embeds the SessionResponse in the wire payload
 *                AND in `snapshot`, so reading either yields the same
 *                data; we keep the flat fields as the snapshot for
 *                pre-Phase-2 server compatibility.
 *
 * Plan reference:
 *   ~/vaults/baphled/1. Projects/FlowState/Plans/
 *     Turn-Based Post-Then-Poll Architecture (May 2026).md
 */
export interface SendSessionMessageResult {
  turnId: string | null
  snapshot: Session
}

export async function sendSessionMessage(
  sessionId: string,
  content: string,
  options?: SendSessionMessageOptions
): Promise<SendSessionMessageResult> {
  if (options?.signal?.aborted) {
    throw new DOMException('This operation was aborted', 'AbortError')
  }

  const body: Record<string, unknown> = { content }
  if (options?.attachmentIds && options.attachmentIds.length > 0) {
    body.attachmentIds = options.attachmentIds
  }

  const res = await fetch(joinBaseURL(`/v1/sessions/${encodeURIComponent(sessionId)}/messages`), {
    method: 'POST',
    headers: withCsrfHeader({ 'Content-Type': 'application/json' }),
    credentials: CREDENTIALS_INCLUDE,
    body: JSON.stringify(body),
    signal: options?.signal,
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  // Phase 2 wire shape:
  //   {
  //     ...SessionResponse,        // legacy flat fields (id, agentId, messages, ...)
  //     turn_id: string,           // additive (empty string when feature off)
  //     snapshot: SessionResponse, // additive nested copy
  //   }
  // Pre-Phase-2 servers omit turn_id + snapshot; the body is just the
  // flat SessionResponse. We normalise both into the same client shape:
  //   - turn_id present and non-empty → turnId set; the chat store
  //     polls GET /turns/{turn_id}.
  //   - turn_id absent or empty       → turnId null; the chat store
  //     falls back to SSE.
  // snapshot is the body itself (flat) when no nested snapshot was
  // provided — the flat fields ARE the Session.
  const parsed = (await res.json()) as Session & {
    turn_id?: string
    snapshot?: Session
  }
  const rawTurnId = typeof parsed.turn_id === 'string' ? parsed.turn_id : ''
  const turnId = rawTurnId.length > 0 ? rawTurnId : null
  const snapshot = (parsed.snapshot as Session | undefined) ?? (parsed as Session)
  return { turnId, snapshot }
}

/**
 * TurnState is the wire shape returned by
 * GET /api/v1/sessions/{session_id}/turns/{turn_id}. Mirrors the Go
 * `turnResponse` struct in internal/api/server.go.
 *
 * Phase 3 reads:
 *   - status — terminal-state discriminant. The chat store polls until
 *              status transitions away from 'running'.
 *   - messages — engine-emitted rows persisted during the turn
 *                (assistant, thinking, tool_call, tool_result,
 *                delegation). Excludes the user message that triggered
 *                the turn — that lives in the POST response's snapshot.
 *   - error — non-empty on status='failed'; surfaced to the user.
 *
 * Plan reference:
 *   ~/vaults/baphled/1. Projects/FlowState/Plans/
 *     Turn-Based Post-Then-Poll Architecture (May 2026).md
 */
export interface TurnStateModel {
  provider: string
  model: string
}

export interface TurnState {
  turn_id: string
  session_id: string
  status: 'running' | 'completed' | 'failed'
  started_at: string
  completed_at: string | null
  model: TurnStateModel
  error: string
  messages: Message[]
  /**
   * phase surfaces the engine's most-recent streaming heartbeat phase
   * (Phase-4-Commit-1). Values: 'generating', 'thinking',
   * 'tool_executing', 'queued'. Empty pre-first-heartbeat.
   *
   * Phase-4-Commit-2 made this the canonical phase signal for the
   * chip's adaptive watchdog — the FE writes it onto
   * `chatStore.streamingPhase[sessionId]` on every poll-diff.
   */
  phase?: string
  /**
   * token_count is the engine's most-recent cumulative
   * output_tokens-so-far on this turn (Phase-4-Commit-1). Monotonically
   * non-decreasing within a turn; resets at the start of each new turn.
   * Powers the live token counter + tokens-per-second display.
   */
  token_count?: number
  /**
   * current_provider mirrors the provider id the engine is CURRENTLY
   * streaming under (Phase-5 §1c-α). Populated by the dispatcher's
   * wrapWithTurnLifecycle chunk-tap on `model_active` /
   * `provider_changed` events; surfaced on every poll so the chat-UI's
   * toolbar chip pivots without an SSE side-channel.
   *
   * Distinct from `model.provider` — `model` is the post-Complete frozen
   * snapshot; `current_provider` surfaces the live pair WHILE running.
   * Empty during the brief window between POST and the first
   * model_active chunk; pre-1c servers omit the field entirely (the
   * absent key is functionally equivalent to "").
   */
  current_provider?: string
  /**
   * current_model is the model id paired with current_provider. Same
   * lifecycle semantics — Phase-5 §1c-α adds this so the FE's poll loop
   * can diff against the prior snapshot and pivot the chip on a real
   * change without waiting for the SSE handler at chatStore.ts:2740.
   */
  current_model?: string
  /**
   * context_usage mirrors the engine's `context_usage` chunk payload —
   * the live context-window saturation figure the chat-UI's usage chip
   * pivots on (Phase-5 §1c-β). Populated by the dispatcher's
   * wrapWithTurnLifecycle chunk-tap on `context_usage` events via
   * registry.SetContextUsage; surfaced on every poll so the chip ticks
   * up without an SSE side-channel.
   *
   * Wire shape mirrors `sseContextUsage` at
   * internal/api/sse_writers.go:142-150 — same field names + JSON tags
   * so the same parser can deserialise SSE chunks and poll snapshots.
   *
   * Optional: pre-1c-β servers and pre-first-chunk Turn states omit the
   * field entirely. The FE's poll-diff treats absent === unchanged.
   */
  context_usage?: TurnStateContextUsage
  /**
   * provider_quotas mirrors the cumulative set of `provider_quota`
   * chunk payloads the engine emitted during this Turn (Phase-5 §1c-β),
   * partitioned by `provider:account_hash:model`. Multi-value because a
   * single stream can carry multiple partitions (anthropic + zai after
   * failover, anthropic + openai across @-mention swarm hops); each
   * partition's most-recent snapshot wins on the registry's upsert
   * semantics.
   *
   * Wire shape mirrors `sseProviderQuota` at
   * internal/api/sse_writers.go:176-189; ProviderQuotaEntry in this
   * module already mirrors the same shape for the REST aggregator —
   * we re-use the existing type here so the FE has one shape across
   * both surfaces.
   *
   * Optional: pre-1c-β servers and pre-first-chunk Turn states omit the
   * field entirely. The FE's poll-diff iterates per-partition; an
   * absent slice is treated as no-change.
   */
  provider_quotas?: TurnStateProviderQuotaSnapshot[]
  /**
   * compaction_events records each `context_compacted` bus event the
   * engine published during this Turn (Phase-5 §1c-γ). Populated by the
   * subscribeTurnContextCompacted bus subscriber in
   * internal/api/server.go via registry.AppendCompactionEvent.
   * Append-only — each compaction adds one entry; the FE poll-diff
   * iterates from `lastPollSnapshot.compaction_events.length` upward
   * and routes any new entries through handleContextCompactedEvent.
   *
   * Optional: pre-1c-γ servers and pre-first-event Turn states omit
   * the field entirely. The FE's poll-diff treats absent === unchanged.
   */
  compaction_events?: TurnStateCompactionEvent[]
  /**
   * gate_failures records each halt-class `gate_failed` bus event the
   * engine published during this Turn (Phase-5 §1c-γ). Populated by
   * subscribeTurnGateFailed. Append-only — the GateFailureBanner reads
   * only the latest entry but the slice preserves history.
   *
   * Optional: pre-1c-γ servers omit the field entirely.
   */
  gate_failures?: TurnStateGateFailure[]
  /**
   * critical_error surfaces a fatal provider-error stamp produced by
   * the dispatcher's wrapWithTurnLifecycle chunk-tap when chunk.Error
   * classifies as SeverityCritical (Phase-5 §1c-γ). The wire shape
   * matches the SSE `stream_critical` event's safeMsg + correlation_id
   * fields so a single FE sink (the persistent CriticalErrorBanner)
   * can hydrate from either transport.
   *
   * null when no critical error has been classified during this Turn
   * (the common case); non-null transitions populate the banner and
   * the correlation_id is the idempotency key the FE handler dedups on.
   */
  critical_error?: TurnStateCriticalError | null
}

/**
 * TurnStateCompactionEvent mirrors the Go `internal/turn.CompactionEvent`
 * JSON shape (turn.go field tags: session_id, agent_id, original_tokens,
 * summary_tokens, latency_ms, trigger). The FE's chatStore poll-diff
 * iterates new entries (length growth since the prior poll) and routes
 * each through handleContextCompactedEvent — the same handler the SSE
 * branch calls at chatStore.ts:~2954.
 *
 * Phase-5 §1c-γ.
 */
export interface TurnStateCompactionEvent {
  session_id: string
  agent_id: string
  original_tokens: number
  summary_tokens: number
  latency_ms: number
  trigger?: string
}

/**
 * TurnStateGateFailure mirrors the Go `internal/turn.GateFailure` JSON
 * shape. The FE's chatStore poll-diff iterates new entries and writes
 * the LATEST entry onto `lastGateFailure` — same shape the existing SSE
 * handler at chatStore.ts:~2996 writes.
 *
 * Phase-5 §1c-γ.
 */
export interface TurnStateGateFailure {
  swarm_id: string
  lifecycle: string
  member_id: string
  gate_name: string
  gate_kind: string
  reason: string
  cause: string
  coord_store_keys?: string[]
}

/**
 * TurnStateCriticalError mirrors the Go `internal/turn.TurnCriticalError`
 * JSON shape. The FE's chatStore poll-diff transitions nil→non-nil
 * populate `criticalError` — same shape the existing SSE handler at
 * chatStore.ts:~2907 writes (after field-name translation:
 * `correlation_id` → `correlationId`).
 *
 * Phase-5 §1c-γ.
 */
export interface TurnStateCriticalError {
  message: string
  correlation_id?: string
  severity?: string
}

/**
 * TurnStateContextUsage mirrors the Go `internal/turn.ContextUsage` JSON
 * shape (turn.go field tags: input_tokens, output_reserve, limit,
 * percentage, provider, model). The FE's chatStore poll-diff parses this
 * verbatim and routes through handleContextUsageEvent — the same handler
 * the SSE branch calls at chatStore.ts:2795-2806 — so a single sink in
 * the store covers both transports.
 *
 * Phase-5 §1c-β.
 */
export interface TurnStateContextUsage {
  input_tokens: number
  output_reserve: number
  limit: number
  percentage: number
  provider: string
  model: string
}

/**
 * TurnStateProviderQuotaSnapshot mirrors the Go
 * `internal/turn.ProviderQuotaSnapshot` JSON shape (snake_case field
 * tags matching sseProviderQuota at internal/api/sse_writers.go). The
 * variant discriminator selects which of {rate_limit, token_spend,
 * not_configured} carries the payload — exactly one is non-null per
 * snapshot.
 *
 * Distinct from ProviderQuotaEntry (the REST aggregator's shape) which
 * uses camelCase: the turn endpoint emits the snake_case Go-side shape
 * directly because Go's encoding/json honors the struct tags as-is.
 * The poll-diff in chatStore normalises this to the camelCase shape
 * applyProviderQuotaEvent expects.
 *
 * Phase-5 §1c-β.
 */
export interface TurnStateProviderQuotaSnapshot {
  provider: string
  account_hash: string
  model?: string
  observed_at: string
  stale?: boolean
  store_backend?: string
  pricing_source?: string
  variant: 'rate_limit' | 'token_spend' | 'not_configured'
  rate_limit?: TurnStateProviderQuotaRateLimit | null
  token_spend?: TurnStateProviderQuotaTokenSpend | null
  not_configured?: TurnStateProviderQuotaNotConfig | null
}

export interface TurnStateProviderQuotaRateLimit {
  requests: TurnStateProviderQuotaWindow
  tokens: TurnStateProviderQuotaWindow
  input: TurnStateProviderQuotaWindow
  output: TurnStateProviderQuotaWindow
  tightest_percent_remaining: number
  tightest_reset_at?: string
}

export interface TurnStateProviderQuotaWindow {
  limit: number
  remaining: number
  reset?: string
}

export interface TurnStateProviderQuotaTokenSpend {
  spent_minor: number
  spent_currency: string
  spent_usd_minor: number
  cap_minor?: number
  cap_currency?: string
  period: string
  period_start: string
  period_end: string
  threshold_amber: number
  threshold_red: number
}

export interface TurnStateProviderQuotaNotConfig {
  reason: string
}

/**
 * FetchTurnOptions enables the Phase-4-Commit-1b long-poll surface.
 * Default behaviour (no options) is the legacy snapshot-read fetch — a
 * single GET that returns the Turn's current state and resolves
 * immediately. Pass `{ wait: true, since: N }` to opt into the
 * server-side hold: the server holds the request until len(messages) > N
 * OR phase/token_count changes OR the Turn reaches a terminal state OR
 * 25s elapses, then returns a fresh snapshot.
 *
 * Fields:
 *   - wait — when true, ?wait=true is appended to the URL. The server's
 *     long-poll handler reads this query param and switches to the hold
 *     path. Default false (legacy snapshot).
 *   - since — caller's last-observed len(messages). Server waits until
 *     the registry's len > since. Default 0 (the first long-poll of a
 *     turn after POST). Ignored when wait=false.
 *   - signal — AbortController signal. The FE's long-poll loop wires
 *     this so a session-switch / page-nav cancels the in-flight
 *     request. The server's r.Context().Done() then fires and the
 *     handler aborts the wait without writing a stale body.
 */
export interface FetchTurnOptions {
  wait?: boolean
  since?: number
  signal?: AbortSignal
}

/**
 * fetchTurn GETs the current state of a Turn by its UUID. Two surfaces:
 *
 *   - Legacy snapshot (default): `fetchTurn(sessionId, turnId)` —
 *     single GET, returns immediately with the Turn's current state.
 *     Used by reconciliation paths and the pre-1b polling fallback.
 *
 *   - Long-poll (Phase-4-Commit-1b): `fetchTurn(sessionId, turnId,
 *     { wait: true, since: N, signal })` — server holds the request
 *     until a mutation lands OR 25s elapses. The FE's long-poll loop
 *     re-issues immediately on every return so each chunk surfaces
 *     within broadcast-latency of the server-side append.
 *
 * The long-poll variant overrides the legacy fetch's per-request
 * implicit timeout — the request must accommodate the server's 25s
 * hold plus a network buffer. We pass `signal` through unchanged so
 * the caller's AbortController controls cancellation; without a
 * signal, the request runs to completion of the server-side wait.
 *
 * Error contract (unchanged across surfaces):
 *   - 404 → throws (caller falls back to SSE / reconcile for
 *     defence-in-depth).
 *   - Any non-OK status → throws with status text.
 *   - signal aborted → throws (DOMException 'AbortError' from fetch).
 *
 * Per memory feedback_response_ok_mock_gotcha — fetch mocks in tests
 * MUST use real Response objects (or include `ok` getter explicitly)
 * so the `if (!res.ok)` branch resolves correctly.
 */
export async function fetchTurn(
  sessionId: string,
  turnId: string,
  opts: FetchTurnOptions = {},
): Promise<TurnState> {
  const basePath = `/v1/sessions/${encodeURIComponent(sessionId)}/turns/${encodeURIComponent(turnId)}`
  let url = joinBaseURL(basePath)
  if (opts.wait) {
    // The server reads `wait=true` as a string match — only this exact
    // value enables the long-poll branch. Pre-1b servers ignore the
    // query param and fall through to the legacy snapshot path
    // (acceptable degradation: every long-poll round-trip is a
    // 250ms-cadence poll on the server we have).
    const since = Math.max(0, Math.floor(opts.since ?? 0))
    url = `${url}?wait=true&since=${since}`
  }
  const init: RequestInit = { credentials: CREDENTIALS_INCLUDE }
  if (opts.signal) {
    init.signal = opts.signal
  }
  const res = await fetch(url, init)
  if (!res.ok) {
    throw new Error(`Failed to fetch turn: ${res.status} ${res.statusText}`)
  }
  return (await res.json()) as TurnState
}

/**
 * UploadedAttachment is the wire shape of a single attachment returned
 * by POST /api/v1/sessions/{id}/attachments. The `id` field is the
 * stable content-hash identifier the caller threads through to
 * sendSessionMessage as attachmentIds.
 *
 * Plan "Chat Attachments Backend (May 2026)" §6 task-03 / task-05.
 */
export interface UploadedAttachment {
  id: string
  mediaType: string
  sizeBytes: number
  originalFilename?: string
}

/**
 * uploadAttachments POSTs a multipart/form-data body to the per-session
 * attachments endpoint and returns the array of stored attachment
 * metadata. The caller is responsible for thread-safety against
 * concurrent uploads — the backend dedups on content hash within a
 * session so a re-upload of identical bytes returns the same id with
 * no error.
 *
 * Error shape: throws on non-2xx response with the backend's status
 * text in the error message. The composer (MessageInput.vue) catches
 * and surfaces a toast; the staged-attachments array is NOT cleared
 * on failure so the user can retry.
 */
export async function uploadAttachments(
  sessionId: string,
  files: File[]
): Promise<UploadedAttachment[]> {
  if (files.length === 0) {
    return []
  }
  const form = new FormData()
  for (const file of files) {
    form.append('files', file, file.name)
  }
  const res = await fetch(joinBaseURL(`/v1/sessions/${encodeURIComponent(sessionId)}/attachments`), {
    method: 'POST',
    headers: withCsrfHeader(undefined),
    credentials: CREDENTIALS_INCLUDE,
    body: form,
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const data = (await res.json()) as { attachments?: UploadedAttachment[] }
  return data.attachments ?? []
}

export async function fetchSessionMessages(sessionId: string): Promise<Message[]> {
  const res = await fetch(joinBaseURL(`/v1/sessions/${encodeURIComponent(sessionId)}/messages`), {
    credentials: CREDENTIALS_INCLUDE,
  })
  if (!res.ok) {
    throw new Error(`Failed to fetch session messages: ${res.statusText}`)
  }
  const data = (await res.json()) as Message[] | null
  return data ?? []
}

// Phase-4-Commit-2 of "Turn-Based Post-Then-Poll Architecture (May 2026)"
// retired the per-session SSE subscription endpoint. The FE now drives
// live state via long-poll on
// GET /v1/sessions/{id}/turns/{turn_id}?wait=true&since=N — see the
// pollTurnUntilTerminal action on the chatStore.
//
// subscribeSessionStream is retained as a deprecated test-only export so
// existing test mocks compile while the test suite migrates to long-
// poll fixtures. Calling it in production throws — the route is gone.
export function subscribeSessionStream(_sessionId: string): EventSource {
  throw new Error(
    'subscribeSessionStream retired in Phase-4-Commit-2 of Turn-Based Post-Then-Poll Architecture (May 2026). Use long-poll on GET /v1/sessions/{id}/turns/{turn_id} via pollTurnUntilTerminal.',
  )
}

export async function updateSessionAgent(sessionId: string, agentId: string): Promise<Session> {
  const res = await fetch(joinBaseURL(`/v1/sessions/${encodeURIComponent(sessionId)}/agent`), {
    method: 'PATCH',
    headers: withCsrfHeader({ 'Content-Type': 'application/json' }),
    credentials: CREDENTIALS_INCLUDE,
    body: JSON.stringify({ agentId }),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return (await res.json()) as Session
}

export async function updateSessionModel(
  sessionId: string,
  modelId: string,
  providerId: string,
): Promise<Session> {
  const res = await fetch(joinBaseURL(`/v1/sessions/${encodeURIComponent(sessionId)}/model`), {
    method: 'PATCH',
    headers: withCsrfHeader({ 'Content-Type': 'application/json' }),
    credentials: CREDENTIALS_INCLUDE,
    body: JSON.stringify({ modelId, providerId }),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return (await res.json()) as Session
}

export async function fetchModels(): Promise<Model[]> {
  const res = await fetch(joinBaseURL('/v1/models'), { credentials: CREDENTIALS_INCLUDE })
  if (!res.ok) {
    throw new Error(`Failed to fetch models: ${res.statusText}`)
  }
  const data = (await res.json()) as ModelsResponse | null
  const providers = data?.providers ?? []
  const models: Model[] = []
  for (const provider of providers) {
    for (const model of provider.models ?? []) {
      models.push({ id: model.id, name: model.name, providerId: provider.id })
    }
  }
  return models
}

/**
 * deleteSession removes a session entirely from the backend (in-memory map
 * + on-disk .meta.json sidecar + .events.jsonl WAL). Backs the per-row
 * trash button in SessionBrowser / SessionSwitcher. The backend returns
 * 204 on success and 404 for an unknown id; non-OK statuses surface as a
 * thrown Error so callers can show a toast and rewind their optimistic
 * remove.
 */
export async function deleteSession(sessionId: string): Promise<void> {
  const url = joinBaseURL(`/v1/sessions/${encodeURIComponent(sessionId)}`)
  const res = await fetch(url, {
    method: 'DELETE',
    headers: withCsrfHeader(undefined),
    credentials: CREDENTIALS_INCLUDE,
  })
  if (!res.ok) {
    throw new Error(`Failed to delete session: ${res.status} ${res.statusText}`)
  }
}

export async function truncateSessionMessages(sessionId: string, fromMessageId: string): Promise<void> {
  const url = joinBaseURL(
    `/v1/sessions/${encodeURIComponent(sessionId)}/messages/from/${encodeURIComponent(fromMessageId)}`,
  )
  const res = await fetch(url, {
    method: 'DELETE',
    headers: withCsrfHeader(undefined),
    credentials: CREDENTIALS_INCLUDE,
  })
  if (!res.ok) throw new Error(`truncate failed: ${res.status}`)
}

export async function listModels(): Promise<ModelsResponse> {
  const res = await fetch(joinBaseURL('/v1/models'), { credentials: CREDENTIALS_INCLUDE })
  if (!res.ok) {
    throw new Error(`Failed to list models: ${res.statusText}`)
  }
  const data = (await res.json()) as ModelsResponse | null
  return { providers: data?.providers ?? [] }
}

// Deliverable 2 of the May 2026 context-accuracy bundle —
// CompressionConfig is the wire shape returned by
// GET / PATCH /api/v1/config/compression. The SettingsView slider
// binds onto `threshold`.
export interface CompressionConfig {
  threshold: number
}

/**
 * fetchCompressionConfig reads the engine's current auto-compaction
 * soft-trigger threshold so the Settings slider can hydrate to the
 * actual configured value rather than guessing the default.
 *
 * Returns:
 *   - The current config on a 200 response.
 *   - null when the server reports 501 (no CompactionController
 *     wired — the feature is built but disabled in this deployment).
 *     The SettingsView treats null as "hide the slider" so operators
 *     don't see a control that won't function.
 *   - Throws on any other non-OK status.
 */
export async function fetchCompressionConfig(): Promise<CompressionConfig | null> {
  const res = await fetch(joinBaseURL('/v1/config/compression'), {
    credentials: CREDENTIALS_INCLUDE,
  })
  if (res.status === 501) {
    return null
  }
  if (!res.ok) {
    throw new Error(`Failed to fetch compression config: ${res.status} ${res.statusText}`)
  }
  const data = (await res.json()) as CompressionConfig
  return data
}

/**
 * updateCompressionThreshold PATCHes the engine's soft-trigger
 * threshold. Returns the post-mutation config so callers can update
 * their local copy from the source of truth rather than echoing the
 * input optimistically.
 *
 * Server-side validation rejects values outside (0.0, 1.0]; the
 * 400 response propagates as a thrown Error so the SettingsView
 * can surface it inline.
 */
export async function updateCompressionThreshold(threshold: number): Promise<CompressionConfig> {
  const res = await fetch(joinBaseURL('/v1/config/compression'), {
    method: 'PATCH',
    headers: withCsrfHeader({ 'Content-Type': 'application/json' }),
    credentials: CREDENTIALS_INCLUDE,
    body: JSON.stringify({ threshold }),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const data = (await res.json()) as CompressionConfig
  return data
}

// Deliverable 3 — CompactNowResult is the wire shape returned by
// POST /api/v1/sessions/{id}/compress. `fired` is the discriminant
// the /compress slash command branches on for its toast copy:
//   - fired=true  → "compacted (saved ~X tokens)"
//   - fired=false → "nothing to compact"
// Summary is the JSON-encoded summary text when fired=true; absent
// otherwise.
export interface CompactNowResult {
  fired: boolean
  summary?: string
}

/**
 * compactSessionNow forces the L2 auto-compactor to fire against the
 * given session, bypassing every soft / gate threshold. The engine
 * still respects the AutoCompaction.Enabled flag — an opt-out is
 * sticky and cannot be overridden by the slash command.
 */
export async function compactSessionNow(sessionId: string): Promise<CompactNowResult> {
  const url = joinBaseURL(`/v1/sessions/${encodeURIComponent(sessionId)}/compress`)
  const res = await fetch(url, {
    method: 'POST',
    headers: withCsrfHeader(undefined),
    credentials: CREDENTIALS_INCLUDE,
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const data = (await res.json()) as CompactNowResult
  return data
}

// Provider Quota and Spend Visibility plan (May 2026) — PR5 REST
// surface. GET /api/v1/providers/quota returns the aggregator view
// across every (provider, account_hash, model) tuple the engine has
// observed; POST .../reset zeros the spend counter for one row.
//
// Wire shape mirrors api/quota_dashboard.go's quotaDashboardEntry +
// internal/api/sse_writers.go's sseProviderQuota* shapes so the
// TypeScript discriminated-union types deserialise both the SSE chunk
// and the REST array element with the same parsers.

/**
 * ProviderQuotaWindow mirrors api/sse_writers.go's sseQuotaWindow —
 * one of four windows the rate_limit variant exposes.
 */
export interface ProviderQuotaWindow {
  limit: number
  remaining: number
  reset: string
}

/**
 * ProviderQuotaRateLimit mirrors api/sse_writers.go's
 * sseProviderQuotaRateLimit — the rate_limit variant payload.
 */
export interface ProviderQuotaRateLimit {
  requests: ProviderQuotaWindow
  tokens: ProviderQuotaWindow
  input: ProviderQuotaWindow
  output: ProviderQuotaWindow
  tightestPercentRemaining: number
  tightestResetAt: string
}

/**
 * ProviderQuotaTokenSpend mirrors api/sse_writers.go's
 * sseProviderQuotaTokenSpend — the token_spend variant payload.
 */
export interface ProviderQuotaTokenSpend {
  spentMinor: number
  spentCurrency: string
  spentUsdMinor: number
  capMinor: number
  capCurrency: string
  period: string
  periodStart: string
  periodEnd: string
  thresholdAmber: number
  thresholdRed: number
}

/**
 * ProviderQuotaNotConfigured mirrors api/sse_writers.go's
 * sseProviderQuotaNotConfig — the not_configured variant payload.
 */
export interface ProviderQuotaNotConfigured {
  reason: string
}

/**
 * ProviderQuotaEntry is one row of the dashboard aggregator response.
 * Field-for-field mirror of api/quota_dashboard.go's
 * quotaDashboardEntry. The `variant` discriminant matches exactly one
 * of the three nested payloads.
 */
export interface ProviderQuotaEntry {
  provider: string
  accountHash: string
  model: string
  observedAt: string
  stale: boolean
  storeBackend: string
  pricingSource: string
  variant: 'rate_limit' | 'token_spend' | 'not_configured'
  rateLimit: ProviderQuotaRateLimit | null
  tokenSpend: ProviderQuotaTokenSpend | null
  notConfigured: ProviderQuotaNotConfigured | null
}

/**
 * normaliseEntry maps the JSON-on-the-wire snake-case fields to the
 * camelCase TypeScript shape the SPA consumes. The Go side uses
 * `json:"..."` tags with snake_case identifiers; the SPA's
 * SSEProviderQuotaEvent already uses camelCase via parseSSEPayload —
 * keep both deserialisers symmetric so a future move to one shared
 * codec replaces this in one place.
 */
function normaliseQuotaEntry(raw: unknown): ProviderQuotaEntry | null {
  if (!raw || typeof raw !== 'object') return null
  const obj = raw as Record<string, unknown>
  const variant = obj['variant']
  if (variant !== 'rate_limit' && variant !== 'token_spend' && variant !== 'not_configured') {
    return null
  }
  return {
    provider: typeof obj['provider'] === 'string' ? (obj['provider'] as string) : '',
    accountHash: typeof obj['account_hash'] === 'string' ? (obj['account_hash'] as string) : '',
    model: typeof obj['model'] === 'string' ? (obj['model'] as string) : '',
    observedAt: typeof obj['observed_at'] === 'string' ? (obj['observed_at'] as string) : '',
    stale: obj['stale'] === true,
    storeBackend: typeof obj['store_backend'] === 'string' ? (obj['store_backend'] as string) : '',
    pricingSource: typeof obj['pricing_source'] === 'string' ? (obj['pricing_source'] as string) : '',
    variant,
    rateLimit: normaliseRateLimit(obj['rate_limit']),
    tokenSpend: normaliseTokenSpend(obj['token_spend']),
    notConfigured: normaliseNotConfigured(obj['not_configured']),
  }
}

function normaliseWindow(raw: unknown): ProviderQuotaWindow {
  if (!raw || typeof raw !== 'object') {
    return { limit: 0, remaining: 0, reset: '' }
  }
  const obj = raw as Record<string, unknown>
  return {
    limit: typeof obj['limit'] === 'number' ? (obj['limit'] as number) : 0,
    remaining: typeof obj['remaining'] === 'number' ? (obj['remaining'] as number) : 0,
    reset: typeof obj['reset'] === 'string' ? (obj['reset'] as string) : '',
  }
}

function normaliseRateLimit(raw: unknown): ProviderQuotaRateLimit | null {
  if (!raw || typeof raw !== 'object') return null
  const obj = raw as Record<string, unknown>
  return {
    requests: normaliseWindow(obj['requests']),
    tokens: normaliseWindow(obj['tokens']),
    input: normaliseWindow(obj['input']),
    output: normaliseWindow(obj['output']),
    tightestPercentRemaining:
      typeof obj['tightest_percent_remaining'] === 'number'
        ? (obj['tightest_percent_remaining'] as number)
        : -1,
    tightestResetAt:
      typeof obj['tightest_reset_at'] === 'string' ? (obj['tightest_reset_at'] as string) : '',
  }
}

function normaliseTokenSpend(raw: unknown): ProviderQuotaTokenSpend | null {
  if (!raw || typeof raw !== 'object') return null
  const obj = raw as Record<string, unknown>
  return {
    spentMinor: typeof obj['spent_minor'] === 'number' ? (obj['spent_minor'] as number) : 0,
    spentCurrency:
      typeof obj['spent_currency'] === 'string' ? (obj['spent_currency'] as string) : '',
    spentUsdMinor:
      typeof obj['spent_usd_minor'] === 'number' ? (obj['spent_usd_minor'] as number) : 0,
    capMinor: typeof obj['cap_minor'] === 'number' ? (obj['cap_minor'] as number) : 0,
    capCurrency:
      typeof obj['cap_currency'] === 'string' ? (obj['cap_currency'] as string) : '',
    period: typeof obj['period'] === 'string' ? (obj['period'] as string) : '',
    periodStart:
      typeof obj['period_start'] === 'string' ? (obj['period_start'] as string) : '',
    periodEnd: typeof obj['period_end'] === 'string' ? (obj['period_end'] as string) : '',
    thresholdAmber:
      typeof obj['threshold_amber'] === 'number' ? (obj['threshold_amber'] as number) : -1,
    thresholdRed:
      typeof obj['threshold_red'] === 'number' ? (obj['threshold_red'] as number) : -1,
  }
}

function normaliseNotConfigured(raw: unknown): ProviderQuotaNotConfigured | null {
  if (!raw || typeof raw !== 'object') return null
  const obj = raw as Record<string, unknown>
  return {
    reason: typeof obj['reason'] === 'string' ? (obj['reason'] as string) : '',
  }
}

/**
 * fetchProviderQuotas reads the dashboard aggregator. Returns the
 * array of per-tuple entries; an empty array means "no providers
 * observed yet" (200 OK + []). Returns null when the server reports
 * 501 (aggregator not wired — feature off in this deployment); the
 * view renders an explanatory empty state.
 *
 * Per memory feedback_response_ok_mock_gotcha — fetch mocks in tests
 * MUST use real Response objects (or include `ok` getter explicitly)
 * so the `if (!res.ok)` branch resolves correctly.
 */
export async function fetchProviderQuotas(): Promise<ProviderQuotaEntry[] | null> {
  const res = await fetch(joinBaseURL('/v1/providers/quota'), {
    headers: withCsrfHeader(undefined),
    credentials: CREDENTIALS_INCLUDE,
  })
  if (res.status === 501) {
    return null
  }
  if (!res.ok) {
    throw new Error(`Failed to fetch provider quotas: ${res.status} ${res.statusText}`)
  }
  const raw = (await res.json()) as unknown
  if (!Array.isArray(raw)) {
    return []
  }
  const out: ProviderQuotaEntry[] = []
  for (const r of raw) {
    const entry = normaliseQuotaEntry(r)
    if (entry !== null) out.push(entry)
  }
  return out
}

/**
 * resetProviderQuotaSpend posts a manual reset for one
 * (provider, account_hash, model) tuple. Returns true when the
 * Snapshot was reset, false when the server reported 404
 * (nothing to reset — silently treat as a no-op). Throws on any
 * other non-OK status.
 *
 * The Auth Track PR3 middleware chain rejects an unauthenticated
 * caller with 401 before the handler runs; missing-CSRF rejects
 * with 403. Both surface as a thrown Error here; the caller (the
 * panel modal) shows an error toast and stays open.
 */
export async function resetProviderQuotaSpend(
  provider: string,
  accountHash: string,
  model: string,
): Promise<boolean> {
  const res = await fetch(joinBaseURL('/v1/providers/quota/reset'), {
    method: 'POST',
    headers: withCsrfHeader({ 'Content-Type': 'application/json' }),
    credentials: CREDENTIALS_INCLUDE,
    body: JSON.stringify({
      provider,
      account_hash: accountHash,
      model,
    }),
  })
  if (res.status === 404) {
    return false
  }
  if (!res.ok) {
    throw new Error(`Failed to reset provider quota: ${res.status} ${res.statusText}`)
  }
  return true
}
