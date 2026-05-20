/**
 * LoginView.spec.ts — Vitest component spec for the FlowState API
 * Auth Track PR3/C8 login surface.
 *
 * Pins:
 *   - The form renders username + password fields and a collapsible
 *     "deployment secret" details section (mode-agnostic shape — see
 *     LoginView.vue for the B8-discipline rationale).
 *   - Submit button is disabled until at least one credential shape
 *     is filled.
 *   - POST /auth/login is called with `credentials: 'include'` AND a
 *     JSON body shaped by whichever fields the user filled in. The
 *     fetch shape matches the server's parseCredentials expectations.
 *   - On 401 the toast surface shows "Invalid credentials" — uniform,
 *     no mode-fingerprint leak.
 *   - On 200 router.push('/chat') fires.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import { createRouter, createMemoryHistory } from "vue-router";
import { createPinia, setActivePinia } from "pinia";
import LoginView from "./LoginView.vue";
import { useCsrfStore } from "@/stores/csrfStore";

function makeRouter() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/", component: { template: "<div />" } },
      { path: "/login", component: LoginView, name: "login" },
      {
        path: "/chat",
        component: { template: "<div>chat</div>" },
        name: "chat",
      },
    ],
  });
  return router;
}

async function mountWithRouter() {
  const router = makeRouter();
  await router.push("/login");
  await router.isReady();
  // Pinia must be active for useCsrfStore() inside csrf.ts /
  // ensureCsrfToken() and the post-login setToken() call.
  setActivePinia(createPinia());
  const wrapper = mount(LoginView, {
    global: { plugins: [router] },
  });
  return { wrapper, router };
}

// Helper: build a Response with `ok` getter (memory
// feedback_response_ok_mock_gotcha — bare `{status: N}` literals make
// `response.ok` undefined and production code takes the wrong branch).
function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// Helper: seed the prefetch as the FIRST mocked fetch. LoginView now
// awaits ensureCsrfToken() before the login POST, so every spec that
// drives onSubmit needs a prefetch mock first.
function mockPrefetch(
  fetchMock: ReturnType<typeof vi.fn>,
  token = "prefetch-masked-token",
) {
  fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: token }));
}

// Toast mock — useToast.ts exports `showToast` as a module-level
// function. We mock the module so the spec can assert on the calls
// without driving Vue's reactivity.
vi.mock("@/composables/useToast", () => ({
  showToast: vi.fn(),
}));

describe("LoginView", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  it("renders username + password fields and a collapsible secret section", async () => {
    const { wrapper } = await mountWithRouter();
    expect(wrapper.find('[data-testid="login-username"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="login-password"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="login-secret-section"]').exists()).toBe(
      true,
    );
    expect(wrapper.find('[data-testid="login-secret"]').exists()).toBe(true);
  });

  it("disables the submit button when no fields are filled", async () => {
    const { wrapper } = await mountWithRouter();
    const btn = wrapper.find('[data-testid="login-submit"]');
    expect((btn.element as HTMLButtonElement).disabled).toBe(true);
  });

  it("enables submit once username + password are filled", async () => {
    const { wrapper } = await mountWithRouter();
    await wrapper.find('[data-testid="login-username"]').setValue("alice");
    await wrapper.find('[data-testid="login-password"]').setValue("secret");
    const btn = wrapper.find('[data-testid="login-submit"]');
    expect((btn.element as HTMLButtonElement).disabled).toBe(false);
  });

  it("enables submit once secret alone is filled", async () => {
    const { wrapper } = await mountWithRouter();
    await wrapper
      .find('[data-testid="login-secret"]')
      .setValue("shared-secret-value");
    const btn = wrapper.find('[data-testid="login-submit"]');
    expect((btn.element as HTMLButtonElement).disabled).toBe(false);
  });

  it("POSTs to /api/auth/login with credentials: include AND prefetched X-CSRF-Token on submit", async () => {
    // QA BUG-1/BUG-2 fix (May 2026). LoginView now prefetches the
    // masked CSRF token via GET /api/auth/csrf BEFORE the POST, then
    // sends the prefetched token in X-CSRF-Token on the login POST.
    // Mock both responses in order.
    mockPrefetch(fetchMock, "prefetch-token-abc");
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, { csrf_token: "rotated-token-xyz" }),
    );

    const { wrapper } = await mountWithRouter();
    await wrapper.find('[data-testid="login-username"]').setValue("alice");
    await wrapper.find('[data-testid="login-password"]').setValue("p4ss");
    await wrapper.find('[data-testid="login-form"]').trigger("submit");
    await flushPromises();

    // Prefetch GET + login POST = 2 calls.
    expect(fetchMock).toHaveBeenCalledTimes(2);

    // Call 1: prefetch GET /api/auth/csrf.
    const [prefetchUrl, prefetchInit] = fetchMock.mock.calls[0];
    expect(String(prefetchUrl)).toContain("/api/auth/csrf");
    expect((prefetchInit as RequestInit).method).toBe("GET");
    expect((prefetchInit as RequestInit).credentials).toBe("include");

    // Call 2: login POST /api/auth/login with the prefetched token.
    const [url, init] = fetchMock.mock.calls[1];
    expect(String(url)).toContain("/api/auth/login");
    expect((init as RequestInit).method).toBe("POST");
    expect((init as RequestInit).credentials).toBe("include");
    const headers = (init as RequestInit).headers as Record<string, string>;
    expect(headers["X-CSRF-Token"]).toBe("prefetch-token-abc");
    expect(headers["Content-Type"]).toBe("application/json");
    const body = JSON.parse(String((init as RequestInit).body));
    expect(body.username).toBe("alice");
    expect(body.password).toBe("p4ss");
  });

  it("captures the rotated csrf_token from the login response and updates the Pinia store", async () => {
    // The server rotates the CSRF token on session mint; the SPA must
    // read csrf_token from the login response and update the Pinia
    // store so subsequent authenticated requests use the post-mint
    // token (bound to the new session Record by
    // RequireCSRFRecordBound).
    mockPrefetch(fetchMock, "prefetch-token");
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, { csrf_token: "post-login-rotated-token" }),
    );

    const { wrapper } = await mountWithRouter();
    await wrapper.find('[data-testid="login-secret"]').setValue("shared");
    await wrapper.find('[data-testid="login-form"]').trigger("submit");
    await flushPromises();

    const store = useCsrfStore();
    expect(store.tokenValue).toBe("post-login-rotated-token");
  });

  it("routes to /chat on 200 login response", async () => {
    mockPrefetch(fetchMock);
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { csrf_token: "abc" }));

    const { wrapper, router } = await mountWithRouter();
    await wrapper
      .find('[data-testid="login-secret"]')
      .setValue("shared-secret");
    await wrapper.find('[data-testid="login-form"]').trigger("submit");
    await flushPromises();

    expect(router.currentRoute.value.path).toBe("/chat");
  });

  it('shows "Invalid credentials" toast on 401', async () => {
    const { showToast } = await import("@/composables/useToast");
    mockPrefetch(fetchMock);
    // ok=false comes from status>=400; mock must use real Response to
    // get the `ok` getter (memory feedback_response_ok_mock_gotcha).
    fetchMock.mockResolvedValueOnce(
      jsonResponse(401, { error: "invalid_credentials" }),
    );

    const { wrapper, router } = await mountWithRouter();
    await wrapper.find('[data-testid="login-secret"]').setValue("wrong");
    await wrapper.find('[data-testid="login-form"]').trigger("submit");
    await flushPromises();

    expect(showToast).toHaveBeenCalledWith(
      expect.objectContaining({
        message: "Invalid credentials",
        variant: "error",
      }),
    );
    // Stays on /login on failure
    expect(router.currentRoute.value.path).toBe("/login");
  });

  it("shows a generic error toast on network failure during login POST", async () => {
    const { showToast } = await import("@/composables/useToast");
    mockPrefetch(fetchMock);
    fetchMock.mockRejectedValueOnce(new Error("network down"));

    // Silence the expected console.error so the test output stays
    // clean.
    const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});

    const { wrapper } = await mountWithRouter();
    await wrapper.find('[data-testid="login-secret"]').setValue("any");
    await wrapper.find('[data-testid="login-form"]').trigger("submit");
    await flushPromises();

    expect(showToast).toHaveBeenCalledWith(
      expect.objectContaining({
        message: expect.stringContaining("Could not reach the server"),
        variant: "error",
      }),
    );
    consoleSpy.mockRestore();
  });

  // QA BUG-1/BUG-2 specs (May 2026). Pin the full prefetch flow and
  // its failure paths.
  describe("CSRF prefetch flow (QA BUG-1/BUG-2)", () => {
    it('surfaces a "could not reach the server" toast when prefetch fails and does NOT fire the login POST', async () => {
      const { showToast } = await import("@/composables/useToast");
      // Prefetch fails (network or 5xx) — login POST must NOT fire.
      fetchMock.mockRejectedValueOnce(new Error("prefetch network down"));

      const consoleSpy = vi
        .spyOn(console, "error")
        .mockImplementation(() => {});

      const { wrapper } = await mountWithRouter();
      await wrapper.find('[data-testid="login-secret"]').setValue("any");
      await wrapper.find('[data-testid="login-form"]').trigger("submit");
      await flushPromises();

      // Only the prefetch was attempted; no login POST.
      expect(fetchMock).toHaveBeenCalledTimes(1);
      expect(showToast).toHaveBeenCalledWith(
        expect.objectContaining({
          message: expect.stringContaining("Could not reach the server"),
          variant: "error",
        }),
      );
      consoleSpy.mockRestore();
    });

    it("reuses a Pinia-cached token on a second submit (no second prefetch)", async () => {
      // First submit: prefetch + login. Second submit (e.g. user
      // corrects credentials after a 401): login POST only — Pinia
      // cache satisfies ensureCsrfToken. Pins the cache-and-coalesce
      // behaviour so a future refactor that drops the cache doesn't
      // silently hammer the prefetch endpoint.
      const { showToast } = await import("@/composables/useToast");
      mockPrefetch(fetchMock, "token-1");
      fetchMock.mockResolvedValueOnce(
        jsonResponse(401, { error: "invalid_credentials" }),
      );
      fetchMock.mockResolvedValueOnce(
        jsonResponse(200, { csrf_token: "token-2" }),
      );

      const { wrapper } = await mountWithRouter();
      await wrapper.find('[data-testid="login-secret"]').setValue("first-try");
      await wrapper.find('[data-testid="login-form"]').trigger("submit");
      await flushPromises();

      // First attempt: prefetch + login = 2 calls.
      expect(fetchMock).toHaveBeenCalledTimes(2);
      expect(showToast).toHaveBeenCalledWith(
        expect.objectContaining({ message: "Invalid credentials" }),
      );

      await wrapper.find('[data-testid="login-secret"]').setValue("second-try");
      await wrapper.find('[data-testid="login-form"]').trigger("submit");
      await flushPromises();

      // Second attempt: cached token, so login POST only — total = 3.
      expect(fetchMock).toHaveBeenCalledTimes(3);
      const [, secondLoginInit] = fetchMock.mock.calls[2];
      const headers = (secondLoginInit as RequestInit).headers as Record<
        string,
        string
      >;
      expect(headers["X-CSRF-Token"]).toBe("token-1");
    });
  });
});
