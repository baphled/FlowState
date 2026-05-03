import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchSessions, fetchSwarmEvents } from './index'

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
