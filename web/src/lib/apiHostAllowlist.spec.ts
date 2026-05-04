import { describe, it, expect, vi } from 'vitest'
import { isAllowedApiHost, validateApiHost } from './apiHostAllowlist'

/**
 * apiHostAllowlist defends against arbitrary localStorage overrides of
 * the API base URL. The threat is a malicious script (XSS, ?apiHost=
 * URL parameter) writing a hostile URL to `flowstate-api-host` so every
 * subsequent fetch / EventSource exfiltrates the user's data to an
 * attacker.
 *
 * The default allowlist permits:
 *   - empty/null (use built-in default)
 *   - relative paths starting with '/'
 *   - http://localhost:* and 127.0.0.1
 *   - same-origin absolute URLs (compared against pageOrigin)
 *   - operator-injected extra origins
 *
 * Everything else is rejected.
 */
describe('isAllowedApiHost — default policy', () => {
  const opts = { pageOrigin: 'https://app.flowstate.example' }

  it('accepts empty / null / undefined (caller uses default base)', () => {
    expect(isAllowedApiHost('', opts)).toBe(true)
    expect(isAllowedApiHost(null, opts)).toBe(true)
    expect(isAllowedApiHost(undefined, opts)).toBe(true)
  })

  it('accepts relative paths starting with single /', () => {
    expect(isAllowedApiHost('/api', opts)).toBe(true)
    expect(isAllowedApiHost('/api/v1', opts)).toBe(true)
    expect(isAllowedApiHost('/', opts)).toBe(true)
  })

  it('rejects protocol-relative URLs (//host) — host is attacker-controlled', () => {
    expect(isAllowedApiHost('//evil.example/api', opts)).toBe(false)
  })

  it('accepts http://localhost:* and 127.0.0.1 — dev setups', () => {
    expect(isAllowedApiHost('http://localhost:8080', opts)).toBe(true)
    expect(isAllowedApiHost('http://127.0.0.1:9000/api', opts)).toBe(true)
    expect(isAllowedApiHost('http://[::1]:8080', opts)).toBe(true)
  })

  it('rejects http:// non-localhost (no TLS = no go)', () => {
    expect(isAllowedApiHost('http://evil.example', opts)).toBe(false)
    expect(isAllowedApiHost('http://internal-svc:8080', opts)).toBe(false)
  })

  it('accepts https:// same-origin URLs', () => {
    expect(isAllowedApiHost('https://app.flowstate.example/api', opts)).toBe(true)
  })

  it('rejects https:// different-origin URLs by default', () => {
    expect(isAllowedApiHost('https://api.flowstate.example/v1', opts)).toBe(false)
    expect(isAllowedApiHost('https://evil.example', opts)).toBe(false)
  })

  it('accepts extra allowed origins when supplied (operator opt-in)', () => {
    const trusting = {
      pageOrigin: 'https://app.flowstate.example',
      extraAllowedOrigins: ['https://api.flowstate.example'] as const,
    }
    expect(isAllowedApiHost('https://api.flowstate.example/v1', trusting)).toBe(true)
    // still rejects unrelated hosts
    expect(isAllowedApiHost('https://evil.example', trusting)).toBe(false)
  })

  it('rejects javascript: URIs (XSS vector)', () => {
    expect(isAllowedApiHost('javascript:alert(1)', opts)).toBe(false)
  })

  it('rejects file:// and data: URIs', () => {
    expect(isAllowedApiHost('file:///etc/passwd', opts)).toBe(false)
    expect(isAllowedApiHost('data:text/html,<script>', opts)).toBe(false)
  })

  it('rejects ftp:// (any non-http(s) scheme)', () => {
    expect(isAllowedApiHost('ftp://archive.example', opts)).toBe(false)
  })

  it('rejects malformed URLs that fail to parse', () => {
    expect(isAllowedApiHost('http://[invalid', opts)).toBe(false)
    expect(isAllowedApiHost('not a url', opts)).toBe(false)
  })
})

describe('validateApiHost', () => {
  const opts = { pageOrigin: 'https://app.flowstate.example' }

  it('returns the value unchanged when permitted', () => {
    expect(validateApiHost('/api', opts)).toBe('/api')
    expect(validateApiHost('http://localhost:8080', opts)).toBe('http://localhost:8080')
  })

  it('returns empty string and warns when value is rejected', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(validateApiHost('http://evil.example', opts)).toBe('')
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })

  it('returns empty string for null/undefined without warning', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(validateApiHost(null, opts)).toBe('')
    expect(validateApiHost(undefined, opts)).toBe('')
    expect(warn).not.toHaveBeenCalled()
    warn.mockRestore()
  })
})
