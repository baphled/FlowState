/**
 * Default timeout (ms) for sendSessionMessage API call.
 */
export const SEND_MESSAGE_TIMEOUT_MS = 30000;

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

export async function sendSessionMessage(
  sessionId: string,
  content: string,
  options?: SendSessionMessageOptions
): Promise<Session> {
  const body: Record<string, unknown> = { content }
  if (options?.attachmentIds && options.attachmentIds.length > 0) {
    body.attachmentIds = options.attachmentIds
  }

  const controller = new AbortController()
  const timeoutId = setTimeout(() => controller.abort(), SEND_MESSAGE_TIMEOUT_MS)

  if (options?.signal?.aborted) {
    clearTimeout(timeoutId)
    throw new Error('Request aborted')
  }

  const onExternalAbort = () => controller.abort()
  options?.signal?.addEventListener('abort', onExternalAbort)

  try {
    const res = await fetch(joinBaseURL(`/v1/sessions/${encodeURIComponent(sessionId)}/messages`), {
      method: 'POST',
      headers: withCsrfHeader({ 'Content-Type': 'application/json' }),
      credentials: CREDENTIALS_INCLUDE,
      body: JSON.stringify(body),
      signal: controller.signal,
    })
    if (!res.ok) {
      throw new Error(await parseError(res))
    }
    return (await res.json()) as Session
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') {
      throw new Error(`Request timed out after ${SEND_MESSAGE_TIMEOUT_MS / 1000} seconds`)
    }
    throw err
  } finally {
    clearTimeout(timeoutId)
    options?.signal?.removeEventListener('abort', onExternalAbort)
  }
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

// subscribeSessionStream opens a same-origin EventSource for the given session.
//
// Same-origin assumption: this app is served from the same origin as the
// FlowState API (the Vite dev server proxies /api to the Go server, and the
// production build is served alongside the Go server). EventSource follows
// the page's CORS policy and does not send cookies cross-origin by default.
//
// Cross-origin support path: when the API moves to a different origin (e.g.
// api.flowstate.app while the SPA is at app.flowstate.app), constructing
// `new EventSource(url, { withCredentials: true })` is the minimum change —
// the Go server must additionally emit `Access-Control-Allow-Origin: <origin>`
// (NOT `*`, which is rejected when withCredentials is true) and
// `Access-Control-Allow-Credentials: true`. See MDN:
// https://developer.mozilla.org/en-US/docs/Web/API/EventSource/EventSource
export function subscribeSessionStream(sessionId: string): EventSource {
  // PR3/C8 — withCredentials: true so the EventSource sends the
  // `flowstate_session` cookie on the SSE handshake. Without it the
  // protected stream endpoint returns 401 once features.auth_v1 flips
  // on (PR5). Per MDN, the Go server must additionally emit
  // `Access-Control-Allow-Origin: <origin>` (NOT `*`) and
  // `Access-Control-Allow-Credentials: true` for cross-origin
  // deployments; same-origin SSE works without server-side CORS.
  return new EventSource(joinBaseURL(`/v1/sessions/${encodeURIComponent(sessionId)}/stream`), {
    withCredentials: true,
  })
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
