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
  const res = await fetch(joinBaseURL('/agents'))
  if (!res.ok) {
    throw new Error(`Failed to fetch agents: ${res.statusText}`)
  }
  return res.json()
}

export async function fetchAgent(id: string): Promise<Agent> {
  const res = await fetch(joinBaseURL(`/agents/${encodeURIComponent(id)}`))
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
  const res = await fetch(joinBaseURL('/swarms'))
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
    headers: { 'Content-Type': 'application/json' },
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
  const res = await fetch(joinBaseURL('/swarm/events'))
  if (!res.ok) {
    throw new Error(`Failed to fetch swarm events: ${res.statusText}`)
  }
  return res.json()
}

export async function fetchSessions(): Promise<SessionSummary[]> {
  const res = await fetch(joinBaseURL('/v1/sessions'))
  if (!res.ok) {
    throw new Error(`Failed to fetch sessions: ${res.statusText}`)
  }
  return res.json()
}

export async function createSession(agentId: string): Promise<Session> {
  const res = await fetch(joinBaseURL('/v1/sessions'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ agent_id: agentId }),
  })
  if (!res.ok) {
    throw new Error(`Failed to create session: ${res.statusText}`)
  }
  return res.json()
}

export interface SendSessionMessageOptions {
  /**
   * attachmentIds is the optional list of attachment ids returned by a
   * prior uploadAttachments() call. The backend resolves these against
   * the session's attachment store and threads them onto the user
   * message as native image content blocks (Anthropic PR1; OpenAI /
   * Copilot in PR3). Unknown ids surface as 400 from the backend.
   */
  attachmentIds?: string[]
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
  const res = await fetch(joinBaseURL(`/v1/sessions/${encodeURIComponent(sessionId)}/messages`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })

  if (!res.ok) {
    throw new Error(await parseError(res))
  }

  const session = (await res.json()) as Session
  return session
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
    body: form,
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const data = (await res.json()) as { attachments?: UploadedAttachment[] }
  return data.attachments ?? []
}

export async function fetchSessionMessages(sessionId: string): Promise<Message[]> {
  const res = await fetch(joinBaseURL(`/v1/sessions/${encodeURIComponent(sessionId)}/messages`))
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
  return new EventSource(joinBaseURL(`/v1/sessions/${encodeURIComponent(sessionId)}/stream`))
}

export async function updateSessionAgent(sessionId: string, agentId: string): Promise<Session> {
  const res = await fetch(joinBaseURL(`/v1/sessions/${encodeURIComponent(sessionId)}/agent`), {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
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
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ modelId, providerId }),
  })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  return (await res.json()) as Session
}

export async function fetchModels(): Promise<Model[]> {
  const res = await fetch(joinBaseURL('/v1/models'))
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
  const res = await fetch(url, { method: 'DELETE' })
  if (!res.ok) {
    throw new Error(`Failed to delete session: ${res.status} ${res.statusText}`)
  }
}

export async function truncateSessionMessages(sessionId: string, fromMessageId: string): Promise<void> {
  const url = joinBaseURL(
    `/v1/sessions/${encodeURIComponent(sessionId)}/messages/from/${encodeURIComponent(fromMessageId)}`,
  )
  const res = await fetch(url, { method: 'DELETE' })
  if (!res.ok) throw new Error(`truncate failed: ${res.status}`)
}

export async function listModels(): Promise<ModelsResponse> {
  const res = await fetch(joinBaseURL('/v1/models'))
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
  const res = await fetch(joinBaseURL('/v1/config/compression'))
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
    headers: { 'Content-Type': 'application/json' },
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
  const res = await fetch(url, { method: 'POST' })
  if (!res.ok) {
    throw new Error(await parseError(res))
  }
  const data = (await res.json()) as CompactNowResult
  return data
}


