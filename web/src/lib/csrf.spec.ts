/**
 * csrf.spec.ts — Vitest specs for the gorilla/csrf cookie reader and
 * the X-CSRF-Token header merger.
 *
 * Plan: FlowState API Auth Track (May 2026) §"Wire Protocol" CSRF section.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { getCsrfToken, withCsrfHeader, ensureCsrfToken } from './csrf'
import { useCsrfStore } from '@/stores/csrfStore'

function setCookie(raw: string) {
  // Vitest happydom resets document.cookie between describes via the
  // beforeEach below; here we just append the test fixture.
  document.cookie = raw
}

function clearAllCookies() {
  const cookies = document.cookie.split('; ').filter((c) => c.length > 0)
  for (const cookie of cookies) {
    const eq = cookie.indexOf('=')
    if (eq === -1) continue
    const name = cookie.slice(0, eq)
    document.cookie = `${name}=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/`
  }
}

describe('getCsrfToken', () => {
  beforeEach(() => {
    clearAllCookies()
  })
  afterEach(() => {
    clearAllCookies()
  })

  it('reads the _csrf cookie value when present', () => {
    setCookie('_csrf=abc123xyz')
    expect(getCsrfToken()).toBe('abc123xyz')
  })

  it('returns empty string when the cookie is absent', () => {
    expect(getCsrfToken()).toBe('')
  })

  it('ignores cookies of other names', () => {
    setCookie('flowstate_session=session-token')
    setCookie('other=foo')
    expect(getCsrfToken()).toBe('')
  })

  it('returns the _csrf cookie when interleaved with others', () => {
    setCookie('flowstate_session=session-token')
    setCookie('_csrf=actual-csrf-value')
    setCookie('other=foo')
    expect(getCsrfToken()).toBe('actual-csrf-value')
  })

  it('URL-decodes percent-encoded cookie values', () => {
    setCookie('_csrf=' + encodeURIComponent('csrf+with/special=chars'))
    expect(getCsrfToken()).toBe('csrf+with/special=chars')
  })
})

describe('withCsrfHeader', () => {
  beforeEach(() => {
    clearAllCookies()
  })

  it('returns headers unchanged when no _csrf cookie is set', () => {
    const headers = { 'Content-Type': 'application/json' }
    expect(withCsrfHeader(headers)).toEqual({
      'Content-Type': 'application/json',
    })
  })

  it('adds X-CSRF-Token header to a plain Record when cookie is set', () => {
    setCookie('_csrf=t0ken')
    const headers = { 'Content-Type': 'application/json' }
    const result = withCsrfHeader(headers) as Record<string, string>
    expect(result).toEqual({
      'Content-Type': 'application/json',
      'X-CSRF-Token': 't0ken',
    })
  })

  it('adds X-CSRF-Token to a Headers instance', () => {
    setCookie('_csrf=t0ken')
    const headers = new Headers({ 'Content-Type': 'application/json' })
    const result = withCsrfHeader(headers) as Headers
    expect(result instanceof Headers).toBe(true)
    expect(result.get('X-CSRF-Token')).toBe('t0ken')
    expect(result.get('Content-Type')).toBe('application/json')
  })

  it('adds X-CSRF-Token to a string[][] header pair list', () => {
    setCookie('_csrf=t0ken')
    const headers: [string, string][] = [['Content-Type', 'application/json']]
    const result = withCsrfHeader(headers) as [string, string][]
    expect(result).toContainEqual(['X-CSRF-Token', 't0ken'])
    expect(result).toContainEqual(['Content-Type', 'application/json'])
  })

  it('handles undefined headers input', () => {
    setCookie('_csrf=t0ken')
    const result = withCsrfHeader(undefined) as Record<string, string>
    expect(result['X-CSRF-Token']).toBe('t0ken')
  })

  it('does NOT mutate the original headers object', () => {
    setCookie('_csrf=t0ken')
    const headers = { 'Content-Type': 'application/json' }
    withCsrfHeader(headers)
    expect(headers).toEqual({ 'Content-Type': 'application/json' })
  })
})

// QA BUG-1/BUG-2 fix (May 2026). getCsrfToken prefers the Pinia
// csrfStore (the masked-token source) over the cookie reader (which
// returns the gorilla securecookie blob and would 403). The cookie
// fallback exists for callers that fire before the Pinia bootstrap.
describe('getCsrfToken — Pinia-first precedence', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    clearAllCookies()
  })
  afterEach(() => {
    clearAllCookies()
  })

  it('returns the Pinia-cached token in preference to the cookie value', () => {
    setCookie('_csrf=cookie-blob-value')
    const store = useCsrfStore()
    store.setToken('pinia-masked-token')

    expect(getCsrfToken()).toBe('pinia-masked-token')
  })

  it('falls through to the cookie when the Pinia cache is empty (defence-in-depth)', () => {
    setCookie('_csrf=cookie-fallback')
    // No setToken — cache is empty.
    expect(getCsrfToken()).toBe('cookie-fallback')
  })

  it('returns empty string when both Pinia and cookie are empty', () => {
    expect(getCsrfToken()).toBe('')
  })

  it('withCsrfHeader uses the Pinia token over the cookie', () => {
    setCookie('_csrf=cookie-blob')
    const store = useCsrfStore()
    store.setToken('pinia-token')

    const headers = withCsrfHeader({ 'Content-Type': 'application/json' }) as Record<string, string>
    expect(headers['X-CSRF-Token']).toBe('pinia-token')
  })
})

describe('ensureCsrfToken', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    setActivePinia(createPinia())
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    clearAllCookies()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.clearAllMocks()
    clearAllCookies()
  })

  it('delegates to csrfStore.ensureToken — fetches /api/auth/csrf on cache miss', async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ csrf_token: 'fresh' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const got = await ensureCsrfToken()
    expect(got).toBe('fresh')
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/auth/csrf'),
      expect.objectContaining({ credentials: 'include' }),
    )
  })

  it('returns the cached token without fetching when csrfStore has one', async () => {
    const store = useCsrfStore()
    store.setToken('warm-cache')

    const got = await ensureCsrfToken()
    expect(got).toBe('warm-cache')
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
