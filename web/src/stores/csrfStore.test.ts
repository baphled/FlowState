/**
 * csrfStore.test.ts — vitest specs for the Pinia store backing the
 * masked CSRF token cache (QA BUG-1/BUG-2 fix, May 2026).
 *
 * Pins:
 *   - ensureToken() fetches GET /api/auth/csrf when the cache is empty
 *     and caches the response token in Pinia state.
 *   - ensureToken() is idempotent: a second call with a populated
 *     cache returns the cached token without re-fetching.
 *   - Concurrent ensureToken() calls share a single in-flight fetch
 *     (no second GET).
 *   - setToken() trims whitespace and rejects empty/whitespace tokens.
 *   - clearToken() drops the cached token AND clears any in-flight
 *     fetch (so logout immediately followed by re-login behaves
 *     correctly).
 *   - ensureToken() throws on non-200 response and on missing
 *     csrf_token in the body — caller (LoginView) surfaces a toast.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import { useCsrfStore } from "./csrfStore";

describe("csrfStore", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    setActivePinia(createPinia());
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  it("starts with no cached token", () => {
    const store = useCsrfStore();
    expect(store.tokenValue).toBe("");
    expect(store.token).toBeNull();
  });

  it("setToken caches a trimmed token", () => {
    const store = useCsrfStore();
    store.setToken("  abc-token  ");
    expect(store.tokenValue).toBe("abc-token");
  });

  it("setToken rejects empty / whitespace tokens", () => {
    const store = useCsrfStore();
    store.setToken("");
    expect(store.token).toBeNull();
    store.setToken("   ");
    expect(store.token).toBeNull();
  });

  it("clearToken drops the cached token", () => {
    const store = useCsrfStore();
    store.setToken("cached");
    expect(store.tokenValue).toBe("cached");
    store.clearToken();
    expect(store.tokenValue).toBe("");
  });

  it("ensureToken fetches /api/auth/csrf and caches the response token", async () => {
    // memory feedback_response_ok_mock_gotcha — Response object so
    // `res.ok` reflects status, not a literal-undefined.
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ csrf_token: "fresh-token" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const store = useCsrfStore();
    const got = await store.ensureToken();
    expect(got).toBe("fresh-token");
    expect(store.tokenValue).toBe("fresh-token");
    expect(fetchMock).toHaveBeenCalledTimes(1);

    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toContain("/api/auth/csrf");
    expect((init as RequestInit).method).toBe("GET");
    expect((init as RequestInit).credentials).toBe("include");
  });

  it("ensureToken returns the cached token without re-fetching when populated", async () => {
    const store = useCsrfStore();
    store.setToken("already-cached");

    const got = await store.ensureToken();
    expect(got).toBe("already-cached");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("concurrent ensureToken calls share a single in-flight fetch", async () => {
    // Resolves only when we say so — drives the coalescing assertion.
    let resolve!: (response: Response) => void;
    const pending = new Promise<Response>((r) => {
      resolve = r;
    });
    fetchMock.mockReturnValueOnce(pending);

    const store = useCsrfStore();
    const first = store.ensureToken();
    const second = store.ensureToken();

    // Only ONE fetch issued despite two callers.
    expect(fetchMock).toHaveBeenCalledTimes(1);

    resolve(
      new Response(JSON.stringify({ csrf_token: "shared-token" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const [a, b] = await Promise.all([first, second]);
    expect(a).toBe("shared-token");
    expect(b).toBe("shared-token");
  });

  it("ensureToken throws on non-200 response", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "misconfigured" }), {
        status: 500,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const store = useCsrfStore();
    await expect(store.ensureToken()).rejects.toThrow(/csrf prefetch failed/);
    // Cache stays empty so the next call retries.
    expect(store.tokenValue).toBe("");
  });

  it("ensureToken throws on missing csrf_token in the response body", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({}), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const store = useCsrfStore();
    await expect(store.ensureToken()).rejects.toThrow(/empty token/);
    expect(store.tokenValue).toBe("");
  });

  it("ensureToken re-fetches after clearToken", async () => {
    // Round 1: fetch + cache.
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ csrf_token: "round-1" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const store = useCsrfStore();
    expect(await store.ensureToken()).toBe("round-1");

    // Clear: simulates logout.
    store.clearToken();
    expect(store.tokenValue).toBe("");

    // Round 2: prefetch is hit again.
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ csrf_token: "round-2" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    expect(await store.ensureToken()).toBe("round-2");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
