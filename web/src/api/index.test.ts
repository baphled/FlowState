import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  deleteSession,
  fetchSessionMessages,
  fetchSessions,
  fetchSwarmEvents,
  fetchSwarms,
  updateSessionAgent,
} from './index'

function installLocalStorageStub() {
  const store = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => store.set(k, v),
    removeItem: (k: string) => store.delete(k),
    clear: () => store.clear(),
    key: () => null,
    length: 0,
  })
}

describe('fetchSwarmEvents', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    installLocalStorageStub()
    fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify([{ id: 'evt-1', type: 'tool_call' }]), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      )
    )
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('issues a GET to /api/swarm/events', async () => {
    await fetchSwarmEvents()

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const calledUrl = fetchMock.mock.calls[0][0] as string
    expect(calledUrl).toContain('/api/swarm/events')
  })

  it('returns the parsed JSON payload from the backend', async () => {
    const events = await fetchSwarmEvents()

    expect(events).toEqual([{ id: 'evt-1', type: 'tool_call' }])
  })
})

// Web Swarm Mention Parity (May 2026) — `fetchSwarms` mirrors
// `fetchAgents`: a single GET to /api/swarms returning the list of
// registered swarm manifests. The chat store calls it on bootstrap so
// the @-picker has swarms to surface alongside agents.
describe('fetchSwarms', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    installLocalStorageStub()
    fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify([
            { id: 'planning-loop', description: 'Planner orchestration', lead: 'planner', members: ['explorer'] },
            { id: 'solo', description: 'Single-member', lead: 'executor', members: [] },
          ]),
          { status: 200, headers: { 'Content-Type': 'application/json' } }
        )
      )
    )
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('issues a GET to /api/swarms', async () => {
    await fetchSwarms()

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const calledUrl = fetchMock.mock.calls[0][0] as string
    expect(calledUrl).toContain('/api/swarms')
  })

  it('returns the parsed array of swarm manifests', async () => {
    const swarms = await fetchSwarms()

    expect(swarms).toHaveLength(2)
    expect(swarms[0].id).toBe('planning-loop')
    expect(swarms[0].lead).toBe('planner')
    expect(swarms[1].id).toBe('solo')
  })

  it('throws when the backend responds with a non-OK status', async () => {
    fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response('boom', { status: 500, statusText: 'Internal Server Error' })
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(fetchSwarms()).rejects.toThrow(/swarms/i)
  })
})

describe('fetchSessions', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    installLocalStorageStub()
    fetchMock = vi.fn()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('throws when the backend responds with a non-OK status', async () => {
    fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response('boom', { status: 500, statusText: 'Internal Server Error' })
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(fetchSessions()).rejects.toThrow(/sessions/i)
  })

  it('returns the parsed array on success', async () => {
    fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify([
            { id: 'sess-1', agentId: 'a', title: 't', updatedAt: '2026-01-01T00:00:00Z', messageCount: 0 },
          ]),
          { status: 200, headers: { 'Content-Type': 'application/json' } }
        )
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    const sessions = await fetchSessions()
    expect(sessions).toHaveLength(1)
    expect(sessions[0].id).toBe('sess-1')
  })
})

describe('fetchSessionMessages', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    installLocalStorageStub()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('returns the parsed array when the backend responds with []', async () => {
    fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    const messages = await fetchSessionMessages('sess-1')
    expect(messages).toEqual([])
  })

  it('coerces a null body to [] so callers never see null', async () => {
    fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response('null', { status: 200, headers: { 'Content-Type': 'application/json' } })
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    const messages = await fetchSessionMessages('sess-1')
    expect(messages).not.toBeNull()
    expect(messages).toEqual([])
  })

  it('throws when the backend responds with a non-OK status', async () => {
    fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response('boom', { status: 500, statusText: 'Internal Server Error' })
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(fetchSessionMessages('sess-1')).rejects.toThrow(/messages/i)
  })
})

describe('updateSessionAgent', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    installLocalStorageStub()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('issues a PATCH to /api/v1/sessions/{id}/agent with agentId in the body', async () => {
    fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({ id: 'sess-1', agentId: 'plan-writer', title: 't', updatedAt: '2026-01-01T00:00:00Z', messages: [] }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }
        )
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    await updateSessionAgent('sess-1', 'plan-writer')

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [calledUrl, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(calledUrl).toContain('/api/v1/sessions/sess-1/agent')
    expect(init.method).toBe('PATCH')
    expect(init.body).toBe(JSON.stringify({ agentId: 'plan-writer' }))
  })

  it('returns the parsed Session on success', async () => {
    fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({ id: 'sess-1', agentId: 'plan-writer', title: 't', updatedAt: '2026-01-01T00:00:00Z', messages: [] }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }
        )
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    const sess = await updateSessionAgent('sess-1', 'plan-writer')
    expect(sess.id).toBe('sess-1')
    expect(sess.agentId).toBe('plan-writer')
  })

  it('throws when the backend responds with a non-OK status', async () => {
    fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify({ error: 'session not found' }), {
          status: 404,
          headers: { 'Content-Type': 'application/json' },
        })
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(updateSessionAgent('missing', 'plan-writer')).rejects.toThrow(/session not found/i)
  })
})

// QW-11 — Per-row session delete. The Vue UI's SessionBrowser /
// SessionSwitcher trash buttons issue a DELETE to /api/v1/sessions/{id};
// the backend returns 204 on success, 404 for an unknown id. The helper
// returns void on success and throws on non-OK.
describe('deleteSession', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    installLocalStorageStub()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('issues a DELETE to /api/v1/sessions/{id}', async () => {
    fetchMock = vi.fn(() =>
      Promise.resolve(new Response(null, { status: 204 }))
    )
    vi.stubGlobal('fetch', fetchMock)

    await deleteSession('sess-1')

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [calledUrl, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(calledUrl).toContain('/api/v1/sessions/sess-1')
    expect(init.method).toBe('DELETE')
  })

  it('throws when the backend responds with a non-OK status', async () => {
    fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify({ error: 'session not found' }), {
          status: 404,
          headers: { 'Content-Type': 'application/json' },
        })
      )
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(deleteSession('missing')).rejects.toThrow(/delete|not found/i)
  })

  it('url-encodes the session id', async () => {
    fetchMock = vi.fn(() =>
      Promise.resolve(new Response(null, { status: 204 }))
    )
    vi.stubGlobal('fetch', fetchMock)

    await deleteSession('a/b c')

    const calledUrl = fetchMock.mock.calls[0][0] as string
    expect(calledUrl).toContain('a%2Fb%20c')
  })
})
