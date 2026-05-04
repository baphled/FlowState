/**
 * apiHostAllowlist — guard against arbitrary API host overrides written to
 * `flowstate-api-host` localStorage.
 *
 * Threat model (Security HIGH #1):
 *   The API host is read from localStorage with no validation
 *   (api/index.ts:13-24, settingsStore.ts:81-84). On any deployment that
 *   shares an origin with attacker-controlled JavaScript (XSS), or accepts
 *   a `?apiHost=` URL parameter, an attacker can point every subsequent
 *   API call at their own server and exfiltrate session tokens, message
 *   content, and credentials forwarded by sendSessionMessage / fetchAgents.
 *
 * Allowlist policy (default — see `defaultAllowlist`):
 *   1. Empty / null / undefined  → permitted (the BASE default '/api'
 *      kicks in, same-origin).
 *   2. Relative path starting with '/' (no scheme, no authority) → permitted
 *      (same-origin by definition).
 *   3. Absolute http://localhost:* / http://127.0.0.1:* → permitted
 *      (developer setup against a local FlowState server).
 *   4. Same-origin absolute URL — origin matches `window.location.origin`
 *      → permitted (production deployment serving frontend + API on the
 *      same hostname).
 *   5. Anything else (incl. https on a different host, javascript:,
 *      file:, data:, http: outside localhost) → REJECTED.
 *
 * On rejection the helper:
 *   - returns false from `isAllowedApiHost`,
 *   - logs a `console.warn` with the rejected value (no message-content),
 *   - the caller (settingsStore / api/index.ts) is responsible for
 *     clearing the offending localStorage key.
 *
 * The allowlist is open for future expansion (e.g. https://api.flowstate.app)
 * via the `extraAllowedOrigins` argument — the production deployment can
 * inject its expected API origin at build time without patching this file.
 */

export interface AllowlistOptions {
  /**
   * The current page origin used to evaluate same-origin URLs. Defaults to
   * `window.location.origin`. Tests pass a fixed value to make assertions
   * independent of the harness.
   */
  pageOrigin?: string
  /**
   * Extra origins the deployment trusts. Each entry is a full origin
   * `scheme://host[:port]` (no trailing slash). Production builds may
   * inject e.g. ['https://api.flowstate.app'].
   */
  extraAllowedOrigins?: readonly string[]
}

/** Localhost detection — covers dev tooling, including IPv4 loopback. */
function isLocalhostHost(host: string): boolean {
  return host === 'localhost' || host === '127.0.0.1' || host === '[::1]'
}

/**
 * Returns true when the given API host string is permitted by the allowlist.
 * Empty / null is permitted (it means "use the BASE default" inside
 * `getBaseURL`). Relative paths are permitted. Absolute URLs are checked
 * against the localhost-or-same-origin policy.
 */
export function isAllowedApiHost(value: string | null | undefined, opts: AllowlistOptions = {}): boolean {
  if (value === null || value === undefined || value === '') {
    return true
  }

  // Relative paths (no scheme + no authority) — same-origin by definition.
  // We accept '/api', '/api/v1', or any path that begins with a single '/'.
  // We deliberately reject '//' (protocol-relative) because the browser
  // will resolve it against the page protocol but the host is attacker-
  // controlled.
  if (value.startsWith('/') && !value.startsWith('//')) {
    return true
  }

  // Anything else must parse as an absolute URL with a permitted scheme.
  // URL constructor throws on garbage like 'javascript:alert(1)'? — no,
  // 'javascript:' is a valid URL scheme to the parser. The scheme check
  // below catches it.
  let url: URL
  try {
    url = new URL(value)
  } catch {
    return false
  }

  // Permit only http/https. javascript:, file:, data:, blob:, etc. are out.
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    return false
  }

  // Localhost gets the http: pass — developer setups commonly use plain http.
  if (isLocalhostHost(url.hostname)) {
    return true
  }

  // Non-localhost http: (no TLS) is always rejected.
  if (url.protocol === 'http:') {
    return false
  }

  // Same-origin or extra-allowed-origin check for https.
  const pageOrigin =
    opts.pageOrigin ?? (typeof window !== 'undefined' ? window.location.origin : '')
  const candidates = new Set<string>([pageOrigin, ...(opts.extraAllowedOrigins ?? [])])
  return candidates.has(url.origin)
}

/**
 * Validates a candidate API host. On failure logs a warning and returns
 * the safe fallback (an empty string, which routes the caller back to the
 * BASE default). On success returns the value unchanged.
 */
export function validateApiHost(
  value: string | null | undefined,
  opts: AllowlistOptions = {},
): string {
  if (isAllowedApiHost(value, opts)) {
    return value ?? ''
  }
  // No PII in the warning — the value is the user's input, not message
  // content; logging it is the only way for an operator to debug a
  // legitimate rejection of a custom-deployment host.
  // eslint-disable-next-line no-console
  console.warn(
    '[flowstate] rejected API host override (not in allowlist):',
    value,
    '- falling back to default',
  )
  return ''
}
