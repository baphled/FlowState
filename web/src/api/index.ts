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

export async function sendSessionMessage(
  sessionId: string,
  content: string
): Promise<Session> {
  const res = await fetch(joinBaseURL(`/v1/sessions/${encodeURIComponent(sessionId)}/messages`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content }),
  })

  if (!res.ok) {
    throw new Error(await parseError(res))
  }

  const session = (await res.json()) as Session
  return session
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


