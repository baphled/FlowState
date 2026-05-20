/**
 * csrf.ts — helpers for reading the masked CSRF token the SPA must
 * echo back via X-CSRF-Token on unsafe-method requests.
 *
 * Plan reference: FlowState API Auth Track (May 2026) §"Wire Protocol"
 * (CSRF token section, lines 419-431).
 *
 * QA BUG-1/BUG-2 fix (May 2026): the prior implementation read the
 * `_csrf` cookie value directly. But gorilla/csrf stores its own
 * securecookie-encrypted blob in that cookie; the value the SPA must
 * send is the MASKED token produced by `csrf.Token(r)` server-side.
 * Three different token values were in play:
 *
 *   - the securecookie blob (cookie value, gorilla-private),
 *   - the masked token (header value, what gorilla validates),
 *   - the unmasked Record-bound CSRFToken (login response body).
 *
 * The SPA could only read the cookie, so every unsafe-method request
 * was rejected with 403 before the handler ran. The fix routes the
 * masked token through a Pinia store (`@/stores/csrfStore`) populated
 * from two sources:
 *
 *   - the GET /api/auth/csrf prefetch response (pre-login), and
 *   - the POST /api/auth/login response body (post-login rotation).
 *
 * The synchronous `getCsrfToken()` reads the cached token; the async
 * `ensureCsrfToken()` is the bootstrap helper LoginView uses before
 * the first POST.
 *
 * Cookie-fallback: when Pinia has no token (e.g. a non-Vue caller),
 * `getCsrfToken()` falls through to the cookie reader for backwards-
 * compat with the prior shape. The cookie value is still the gorilla
 * securecookie blob and will not satisfy gorilla's check — but the
 * fallback exists so an existing call site that fires before the
 * prefetch happens degrades to a 403 (recoverable) rather than a
 * runtime error.
 */

import { useCsrfStore } from "@/stores/csrfStore";

const CSRF_COOKIE_NAME = "_csrf";
const CSRF_HEADER_NAME = "X-CSRF-Token";

/**
 * cookieFallback reads the `_csrf` cookie value from document.cookie.
 * Defence-in-depth path — called only when the Pinia cache is empty.
 *
 * The value here is NOT the masked token gorilla/csrf wants; it's the
 * securecookie-encrypted blob. Sending it produces a 403 csrf_invalid
 * server-side, which the SPA's 401/403 redirect handler picks up. Better
 * than returning empty and letting the request fire with no header
 * (same 403, but with a less-greppable log signature).
 */
function cookieFallback(): string {
  if (typeof document === "undefined" || !document.cookie) {
    return "";
  }
  const cookies = document.cookie.split("; ");
  for (const cookie of cookies) {
    const eq = cookie.indexOf("=");
    if (eq === -1) continue;
    const name = cookie.slice(0, eq);
    if (name !== CSRF_COOKIE_NAME) continue;
    const raw = cookie.slice(eq + 1);
    try {
      return decodeURIComponent(raw);
    } catch {
      return raw;
    }
  }
  return "";
}

/**
 * getCsrfToken returns the masked CSRF token the SPA should echo back
 * via X-CSRF-Token. Reads from the Pinia csrfStore first (the
 * authoritative source — populated by the prefetch and the login
 * response). Falls through to the cookie reader when the Pinia cache
 * is empty (defensive — see file header).
 *
 * Synchronous: the existing `withCsrfHeader` call sites are synchronous
 * and we don't want to refactor 13 call sites to async. The async
 * bootstrap is `ensureCsrfToken()` below — LoginView awaits that BEFORE
 * the first POST so the Pinia cache is populated by the time any
 * unsafe-method API call fires.
 */
export function getCsrfToken(): string {
  // Pinia access guarded for SSR / pre-pinia-install environments
  // (e.g. a unit test that imports this module without setActivePinia).
  // The try/catch lets the fallback path serve those callers without
  // throwing.
  try {
    const store = useCsrfStore();
    const cached = store.tokenValue;
    if (cached) {
      return cached;
    }
  } catch {
    // Pinia not active; fall through to cookie.
  }
  return cookieFallback();
}

/**
 * ensureCsrfToken triggers an async prefetch when the Pinia cache is
 * empty. LoginView awaits this BEFORE submitting the login form so the
 * synchronous getCsrfToken() call inside the request build can read a
 * valid masked token from the cache.
 *
 * Resolves to the token string on success; throws on network failure
 * or non-200 response (caller surfaces a "could not reach the server"
 * toast).
 *
 * Idempotent: concurrent calls share the same in-flight fetch via the
 * csrfStore's `fetchInFlight` coalescing.
 */
export async function ensureCsrfToken(): Promise<string> {
  const store = useCsrfStore();
  return store.ensureToken();
}

/**
 * withCsrfHeader merges the `X-CSRF-Token` header into an existing
 * headers object. Helper for fetch() call sites that pass
 * `headers: { 'Content-Type': 'application/json' }` etc.
 *
 * When no token is cached (and the cookie fallback is empty), returns
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
export function withCsrfHeader(headers: HeadersInit | undefined): HeadersInit {
  const token = getCsrfToken();
  if (!token) {
    return headers ?? {};
  }
  // Build the merged headers without mutating the input. Three shapes
  // to handle (per HeadersInit): Headers, string[][], Record.
  if (headers instanceof Headers) {
    const out = new Headers(headers);
    out.set(CSRF_HEADER_NAME, token);
    return out;
  }
  if (Array.isArray(headers)) {
    return [...headers, [CSRF_HEADER_NAME, token]];
  }
  return { ...(headers ?? {}), [CSRF_HEADER_NAME]: token };
}
