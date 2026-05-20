/**
 * csrfStore — Pinia store caching the masked CSRF token the SPA echoes
 * back via X-CSRF-Token on unsafe-method requests.
 *
 * Why this exists (QA BUG-1/BUG-2 fix, May 2026): the prior code read
 * the `_csrf` cookie value directly and sent it in X-CSRF-Token. But
 * gorilla/csrf stores its own securecookie-encrypted blob in the
 * cookie; the value the SPA must send is the MASKED token produced by
 * `csrf.Token(r)` server-side. Three different token values were in
 * play —
 *
 *   - the securecookie blob (cookie),
 *   - the masked token (header),
 *   - the unmasked Record-bound CSRFToken (login response),
 *
 * — and the SPA could only read the cookie, so every unsafe-method
 * request was rejected with 403 before the handler ran.
 *
 * Architecture: this store caches the masked token from one of two
 * sources, with the same precedence the SPA's request lifecycle uses:
 *
 *   1. The login response body's `csrf_token` field. The server rotates
 *      the token on session mint; the SPA captures it post-login so
 *      subsequent authenticated requests carry the post-mint token.
 *   2. The GET /api/auth/csrf prefetch endpoint's `csrf_token` field.
 *      Used pre-login when the SPA has no session yet but needs to
 *      satisfy gorilla/csrf on the login POST.
 *
 * On logout the store clears the token so the next pre-login flow
 * re-prefetches fresh.
 *
 * Synchronous reads: `tokenValue` is a plain getter the existing
 * `withCsrfHeader` call sites can consume without rewriting to async.
 * The async fetch is exposed separately via `ensureToken()` for the
 * login flow.
 */

import { defineStore } from "pinia";
import { joinBaseURL } from "@/api";

interface CSRFPrefetchResponse {
  csrf_token: string;
}

interface CSRFStoreState {
  token: string | null;
  /**
   * fetchInFlight — single in-flight promise for concurrent ensureToken
   * callers. Without this, two near-simultaneous prefetch calls would
   * issue two GET /api/auth/csrf requests and the second would clobber
   * the first's cookie. Coalesce so the second caller awaits the same
   * promise.
   */
  fetchInFlight: Promise<string> | null;
}

export const useCsrfStore = defineStore("csrf", {
  state: (): CSRFStoreState => ({
    token: null,
    fetchInFlight: null,
  }),
  getters: {
    /**
     * tokenValue returns the cached masked token, or empty string when
     * unknown. Synchronous — callers that want fetch-on-miss use
     * ensureToken().
     */
    tokenValue: (state) => state.token ?? "",
  },
  actions: {
    /**
     * setToken caches a token from any authoritative source (login
     * response, prefetch response). Trims defensive whitespace so a
     * trailing newline from a misconfigured upstream doesn't break the
     * comparison gorilla/csrf does server-side.
     */
    setToken(token: string): void {
      const trimmed = token?.trim();
      if (!trimmed) return;
      this.token = trimmed;
    },
    /**
     * clearToken drops the cached token. Called from the logout flow
     * so the next pre-login attempt re-prefetches a fresh token bound
     * to a new gorilla session.
     */
    clearToken(): void {
      this.token = null;
      this.fetchInFlight = null;
    },
    /**
     * ensureToken returns a non-empty token, prefetching from
     * GET /api/auth/csrf when the cache is empty. Concurrent callers
     * share the same in-flight fetch so the SPA issues at most one
     * prefetch per pre-login flow.
     *
     * Throws on network failure or non-200 response — the LoginView
     * surfaces a "could not reach the server" toast rather than letting
     * the POST fire with an empty header (which would 403 anyway).
     */
    async ensureToken(): Promise<string> {
      if (this.token) {
        return this.token;
      }
      if (this.fetchInFlight) {
        return this.fetchInFlight;
      }
      const promise = (async () => {
        const res = await fetch(joinBaseURL("/auth/csrf"), {
          method: "GET",
          credentials: "include",
          headers: { Accept: "application/json" },
        });
        if (!res.ok) {
          throw new Error(`csrf prefetch failed: ${res.status}`);
        }
        const body = (await res.json()) as CSRFPrefetchResponse;
        if (!body?.csrf_token) {
          throw new Error("csrf prefetch: empty token in response");
        }
        this.setToken(body.csrf_token);
        return body.csrf_token;
      })();
      this.fetchInFlight = promise;
      try {
        return await promise;
      } finally {
        this.fetchInFlight = null;
      }
    },
  },
});
