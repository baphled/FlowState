import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useCsrfStore } from './csrfStore'

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('csrfStore', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    setActivePinia(createPinia())
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    localStorage.clear()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.clearAllMocks()
    localStorage.clear()
  })

  it('stores a token supplied by the login response', () => {
    const store = useCsrfStore()
    store.setToken('post-login-token')

    expect(store.tokenValue).toBe('post-login-token')
  })

  it('fetches /api/auth/csrf with credentials on cache miss', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'fresh-token' }))

    const store = useCsrfStore()
    const token = await store.ensureToken()

    expect(token).toBe('fresh-token')
    expect(store.tokenValue).toBe('fresh-token')
    expect(fetchMock).toHaveBeenCalledWith('/api/auth/csrf', {
      method: 'GET',
      credentials: 'include',
    })
  })

  it('uses the configured API host for the prefetch endpoint', async () => {
    localStorage.setItem('flowstate-api-host', 'http://localhost:8080/api')
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'fresh-token' }))

    const store = useCsrfStore()
    await store.ensureToken()

    expect(fetchMock).toHaveBeenCalledWith('http://localhost:8080/api/auth/csrf', {
      method: 'GET',
      credentials: 'include',
    })
  })

  it('returns the cached token without fetching', async () => {
    const store = useCsrfStore()
    store.setToken('cached-token')

    await expect(store.ensureToken()).resolves.toBe('cached-token')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('coalesces concurrent cache misses into one fetch', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'shared-token' }))

    const store = useCsrfStore()
    const [first, second] = await Promise.all([
      store.ensureToken(),
      store.ensureToken(),
    ])

    expect(first).toBe('shared-token')
    expect(second).toBe('shared-token')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('clears the in-flight request after a failed prefetch so a retry can succeed', async () => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse(500, { error: 'boom' }))
      .mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'retry-token' }))

    const store = useCsrfStore()
    await expect(store.ensureToken()).rejects.toThrow(/csrf/i)
    await expect(store.ensureToken()).resolves.toBe('retry-token')
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
})
