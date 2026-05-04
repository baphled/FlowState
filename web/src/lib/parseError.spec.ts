import { describe, it, expect } from 'vitest'
import { parseError } from './parseError'

/**
 * parseError is the small defensive wrapper that replaces the
 * `as { error: string }` casts at api/index.ts:54,135,163,180. Behaviour
 * pinned here:
 *
 *   - reads `error`, `message`, or `detail` string fields off any JSON object
 *     body in that priority order, returning the first non-empty match.
 *   - accepts a top-level JSON string body verbatim.
 *   - falls back to `${statusText} (HTTP ${status})` when no string is
 *     available, when the body is not JSON, or when only non-string fields
 *     exist.
 *   - never throws — the return type is always a non-empty string suitable
 *     for `throw new Error(parseError(res))`.
 */
describe('parseError', () => {
  function jsonResponse(body: unknown, init: { status?: number; statusText?: string } = {}): Response {
    return new Response(JSON.stringify(body), {
      status: init.status ?? 500,
      statusText: init.statusText ?? 'Internal Server Error',
      headers: { 'Content-Type': 'application/json' },
    })
  }

  function plainResponse(text: string, init: { status?: number; statusText?: string } = {}): Response {
    return new Response(text, {
      status: init.status ?? 500,
      statusText: init.statusText ?? 'Internal Server Error',
    })
  }

  it('returns the `error` field when present (FlowState convention)', async () => {
    const res = jsonResponse({ error: 'session not found' }, { status: 404, statusText: 'Not Found' })
    expect(await parseError(res)).toBe('session not found')
  })

  it('returns the `message` field when no `error` field is present (express convention)', async () => {
    const res = jsonResponse({ message: 'rate limit exceeded' })
    expect(await parseError(res)).toBe('rate limit exceeded')
  })

  it('returns the `detail` field when no `error` or `message` (FastAPI convention)', async () => {
    const res = jsonResponse({ detail: 'validation failed' })
    expect(await parseError(res)).toBe('validation failed')
  })

  it('prefers `error` over `message` and `detail` when multiple are present', async () => {
    const res = jsonResponse({ error: 'A', message: 'B', detail: 'C' })
    expect(await parseError(res)).toBe('A')
  })

  it('returns a top-level JSON string body verbatim', async () => {
    const res = jsonResponse('plain string error')
    expect(await parseError(res)).toBe('plain string error')
  })

  it('falls back to statusText + status when JSON has no recognised string field', async () => {
    const res = jsonResponse({ unrelated: 42 }, { status: 502, statusText: 'Bad Gateway' })
    expect(await parseError(res)).toBe('Bad Gateway (HTTP 502)')
  })

  it('falls back to statusText + status when body is not JSON', async () => {
    const res = plainResponse('<html>oops</html>', { status: 500, statusText: 'Internal Server Error' })
    expect(await parseError(res)).toBe('Internal Server Error (HTTP 500)')
  })

  it('falls back to bare HTTP status when statusText is empty', async () => {
    const res = plainResponse('', { status: 418, statusText: '' })
    expect(await parseError(res)).toBe('HTTP 418')
  })

  it('ignores empty string `error` and falls back', async () => {
    const res = jsonResponse({ error: '' }, { status: 500, statusText: 'Internal Server Error' })
    expect(await parseError(res)).toBe('Internal Server Error (HTTP 500)')
  })

  it('ignores non-string `error` and falls back', async () => {
    const res = jsonResponse({ error: 123 }, { status: 500, statusText: 'Internal Server Error' })
    expect(await parseError(res)).toBe('Internal Server Error (HTTP 500)')
  })
})
