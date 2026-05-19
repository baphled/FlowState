import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { sendSessionMessage, fetchTurn } from './index'

describe('sendSessionMessage', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify({}), {
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

  it('rejects immediately if signal is already aborted (does not call fetch)', async () => {
    const controller = new AbortController()
    controller.abort()
    let error: unknown
    try {
      await sendSessionMessage('sess-1', 'hello', { signal: controller.signal })
    } catch (e) {
      error = e
    }
    expect(fetchMock).not.toHaveBeenCalled()
    expect(error).toBeDefined()
    expect(
      error instanceof DOMException
        ? error.name
        : (error as Error).message
    ).toMatch(/abort/i)
  })

  // Phase 3 of "Turn-Based Post-Then-Poll Architecture (May 2026)".
  // Phase 2 (server, commit 9e398807) made POST /api/v1/sessions/{id}/messages
  // return the Session shape (legacy flat fields) PLUS two new keys:
  //   - turn_id    — the freshly-minted Turn UUID
  //   - snapshot   — a nested copy of the same Session
  // Phase 3 (FE) requires sendSessionMessage to surface both turn_id and
  // a Session snapshot so the chat-store can drive GET /turns/{turn_id}.
  it('returns {turnId, snapshot} when the server response carries turn_id (Phase 3)', async () => {
    const sessionPayload = {
      id: 'sess-1',
      agentId: 'agent-1',
      messages: [{ id: 'msg-x', role: 'user', content: 'hello', timestamp: '' }],
      messageCount: 1,
      status: 'active',
      depth: 0,
      isStreaming: false,
      createdAt: '',
      updatedAt: '',
    }
    fetchMock.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          ...sessionPayload,
          turn_id: 'turn-abc',
          snapshot: sessionPayload,
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    const result = await sendSessionMessage('sess-1', 'hello')

    expect(result).toMatchObject({ turnId: 'turn-abc' })
    expect(result.snapshot.id).toBe('sess-1')
    expect(result.snapshot.messages.length).toBe(1)
  })

  it('returns {turnId: null, snapshot} when the server response lacks turn_id (legacy rollback)', async () => {
    // Defence-in-depth: if the server is older than Phase 2 (or operators
    // roll the server back), turn_id is absent. The FE falls back to the
    // SSE path. sendSessionMessage must surface turnId=null so the caller
    // can branch deterministically.
    const sessionPayload = {
      id: 'sess-1',
      agentId: 'agent-1',
      messages: [{ id: 'msg-x', role: 'user', content: 'hello', timestamp: '' }],
      messageCount: 1,
      status: 'active',
      depth: 0,
      isStreaming: false,
      createdAt: '',
      updatedAt: '',
    }
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify(sessionPayload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const result = await sendSessionMessage('sess-1', 'hello')

    expect(result.turnId).toBeNull()
    expect(result.snapshot.id).toBe('sess-1')
  })

  it('treats an empty string turn_id as null (defensive)', async () => {
    const sessionPayload = {
      id: 'sess-1',
      agentId: 'agent-1',
      messages: [],
      messageCount: 0,
      status: 'active',
      depth: 0,
      isStreaming: false,
      createdAt: '',
      updatedAt: '',
    }
    fetchMock.mockResolvedValueOnce(
      new Response(
        JSON.stringify({ ...sessionPayload, turn_id: '', snapshot: sessionPayload }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    const result = await sendSessionMessage('sess-1', 'hello')

    expect(result.turnId).toBeNull()
  })
})

describe('fetchTurn', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('GETs /api/v1/sessions/{sid}/turns/{tid} with credentials and returns the parsed TurnState', async () => {
    const body = {
      turn_id: 'turn-abc',
      session_id: 'sess-1',
      status: 'running',
      started_at: '2026-05-19T10:00:00Z',
      completed_at: null,
      model: { provider: 'anthropic', model: 'claude-opus' },
      error: '',
      messages: [{ id: 'asst-1', role: 'assistant', content: 'hi', timestamp: '' }],
    }
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const result = await fetchTurn('sess-1', 'turn-abc')

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0]
    expect(String(url)).toMatch(/\/v1\/sessions\/sess-1\/turns\/turn-abc$/)
    expect((init as RequestInit | undefined)?.credentials).toBe('include')
    expect(result.turn_id).toBe('turn-abc')
    expect(result.status).toBe('running')
    expect(result.messages.length).toBe(1)
  })

  it('throws on 404 so the caller can fall back to the SSE path (defence-in-depth)', async () => {
    fetchMock.mockResolvedValueOnce(
      new Response('turn not found', { status: 404 }),
    )

    let err: unknown
    try {
      await fetchTurn('sess-1', 'unknown-turn')
    } catch (e) {
      err = e
    }
    expect(err).toBeInstanceOf(Error)
    expect(String((err as Error).message)).toMatch(/404|not found/i)
  })

  it('throws on non-OK non-404 status (e.g. 500)', async () => {
    fetchMock.mockResolvedValueOnce(
      new Response('internal error', { status: 500 }),
    )

    let err: unknown
    try {
      await fetchTurn('sess-1', 'turn-abc')
    } catch (e) {
      err = e
    }
    expect(err).toBeInstanceOf(Error)
  })
})
