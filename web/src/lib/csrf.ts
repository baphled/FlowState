/**
 * csrf.ts — helpers for reading the gorilla/csrf-issued `_csrf` cookie
 * and injecting the `X-CSRF-Token` header on unsafe-method requests.
 *
 * Plan reference: FlowState API Auth Track (May 2026) §"Wire Protocol"
 * (CSRF token section, lines 419-431) — the Go server sets a non-
 * HttpOnly `_csrf` cookie with path `/api` so the SPA can read the
 * token via `document.cookie` and echo it back in the
 * `X-CSRF-Token` header on POST/PUT/PATCH/DELETE.
 *
 * gorilla/csrf's masked token is what lives in the cookie; gorilla/csrf
 * itself unmasks + validates server-side, and a separate Record-bound
 * layer (internal/auth/csrf.go:RequireCSRFRecordBound) checks the
 * unmasked token against the session's stored CSRFToken. Both layers
 * read the SAME header — `X-CSRF-Token`. The SPA just needs to send it.
 *
 * Why no Pinia store: this helper is stateless. The cookie IS the
 * source of truth — keeping a shadow copy in a store would only invite
 * staleness bugs after server-side rotation (login). Callers grab the
 * current value at request time.
 *
 * Behaviour:
 *   - Returns the cookie value (URL-decoded) when present.
 *   - Returns "" when the cookie is absent (the first unauthenticated
 *     request to a `registerLogin` endpoint sets it via Set-Cookie; the
 *     follow-up POST will then have a value to send).
 *   - Returns "" when `document` is undefined (SSR — not a runtime
 *     condition for this SPA, but defensive against test envs).
 */

const CSRF_COOKIE_NAME = '_csrf'
const CSRF_HEADER_NAME = 'X-CSRF-Token'

/**
 * getCsrfToken reads the `_csrf` cookie value from `document.cookie`.
 *
 * Returns the raw cookie value (URL-decoded). The caller MUST send
 * this verbatim — gorilla/csrf does its own unmask + validation on the
 * server side, and any pre-processing here would break the
 * mask/HMAC verification.
 */
export function getCsrfToken(): string {
  if (typeof document === 'undefined' || !document.cookie) {
    return ''
  }
  // document.cookie is "name=value; name2=value2; ..." — split on "; "
  // and search for the entry by name. Cookie names cannot contain "="
  // so the first "=" delimits name from value.
  const cookies = document.cookie.split('; ')
  for (const cookie of cookies) {
    const eq = cookie.indexOf('=')
    if (eq === -1) continue
    const name = cookie.slice(0, eq)
    if (name !== CSRF_COOKIE_NAME) continue
    const raw = cookie.slice(eq + 1)
    try {
      return decodeURIComponent(raw)
    } catch {
      // Malformed percent-encoding — return raw rather than throwing.
      // gorilla/csrf will reject an invalid token with 403; better than
      // surfacing a TypeError to the caller mid-request.
      return raw
    }
  }
  return ''
}

/**
 * withCsrfHeader merges the `X-CSRF-Token` header into an existing
 * headers object. Helper for fetch() call sites that pass
 * `headers: { 'Content-Type': 'application/json' }` etc.
 *
 * When the cookie is absent (first request, pre-login), returns
 * headers unchanged — the request will hit the gorilla/csrf gate and
 * 403, which is the correct "you need to log in first" signal. The
 * SPA's 401/403 redirect-to-login handler picks it up.
 *
 * NB: this helper ONLY adds the header. Callers MUST also set
 * `credentials: 'include'` on the fetch() options so the browser
 * sends the cookie. The two are an inseparable pair — the cookie
 * round-trips for server validation, the header proves the SPA read
 * the value (and not an off-origin attacker who can't read it via
 * SameSite=Lax).
 */
export function withCsrfHeader(
  headers: HeadersInit | undefined,
): HeadersInit {
  const token = getCsrfToken()
  if (!token) {
    return headers ?? {}
  }
  // Build the merged headers without mutating the input. Three shapes
  // to handle (per HeadersInit): Headers, string[][], Record.
  if (headers instanceof Headers) {
    const out = new Headers(headers)
    out.set(CSRF_HEADER_NAME, token)
    return out
  }
  if (Array.isArray(headers)) {
    return [...headers, [CSRF_HEADER_NAME, token]]
  }
  return { ...(headers ?? {}), [CSRF_HEADER_NAME]: token }
}
