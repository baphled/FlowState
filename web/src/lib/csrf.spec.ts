/**
 * csrf.spec.ts — Vitest specs for the gorilla/csrf cookie reader and
 * the X-CSRF-Token header merger.
 *
 * Plan: FlowState API Auth Track (May 2026) §"Wire Protocol" CSRF section.
 */
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { getCsrfToken, withCsrfHeader } from './csrf'

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
