/**
 * ProviderQuotaDashboardView.spec.ts — vitest spec for the Provider
 * Quota and Spend Visibility plan (May 2026) PR5a dashboard view.
 *
 * Pins:
 *   - On mount, GET /api/v1/providers/quota is called.
 *   - Empty array → "No providers observed yet" empty state.
 *   - 501 → "not wired in this deployment" empty state (feature off).
 *   - Non-empty payload renders one row per (provider, account, model).
 *   - Row click opens ProviderQuotaPanel for that entry.
 *
 * Per memory feedback_response_ok_mock_gotcha — fetch mocks use real
 * Response objects so res.ok evaluates correctly.
 * Per memory feedback_pinia_onmounted_clobbers_seed — assertions
 * happen AFTER flushPromises so the onMounted load resolves before
 * the spec checks DOM state.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import { createRouter, createMemoryHistory, type Router } from "vue-router";
import ProviderQuotaDashboardView from "./ProviderQuotaDashboardView.vue";

function makeRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/", component: { template: "<div />" } },
      {
        path: "/providers/quota",
        component: ProviderQuotaDashboardView,
        name: "provider-quota-dashboard",
      },
    ],
  });
}

async function mountWithRouter() {
  const router = makeRouter();
  await router.push("/providers/quota");
  await router.isReady();
  const wrapper = mount(ProviderQuotaDashboardView, {
    global: { plugins: [router] },
  });
  return { wrapper, router };
}

// Reusable entry fixtures matching the api/quota_dashboard.go wire
// shape (snake_case fields the SPA normalises to camelCase).
const rateLimitRow = {
  provider: "anthropic",
  account_hash: "abc12345abc",
  model: "claude-opus-4-7",
  observed_at: "2026-05-13T12:00:00Z",
  stale: false,
  store_backend: "memory",
  pricing_source: "embedded",
  variant: "rate_limit",
  rate_limit: {
    requests: { limit: 100, remaining: 42, reset: "2026-05-13T12:05:00Z" },
    tokens: { limit: 100000, remaining: 12000, reset: "2026-05-13T12:05:00Z" },
    input: { limit: 50000, remaining: 7000, reset: "2026-05-13T12:05:00Z" },
    output: { limit: 50000, remaining: 5000, reset: "2026-05-13T12:05:00Z" },
    tightest_percent_remaining: 12,
    tightest_reset_at: "2026-05-13T12:05:00Z",
  },
  token_spend: null,
  not_configured: null,
};

const tokenSpendRow = {
  provider: "openai",
  account_hash: "def67890def",
  model: "gpt-4o",
  observed_at: "2026-05-13T12:00:00Z",
  stale: false,
  store_backend: "memory",
  pricing_source: "embedded",
  variant: "token_spend",
  rate_limit: null,
  token_spend: {
    spent_minor: 1234,
    spent_currency: "USD",
    spent_usd_minor: 1234,
    cap_minor: 10000,
    cap_currency: "USD",
    period: "month",
    period_start: "2026-05-01T00:00:00Z",
    period_end: "2026-06-01T00:00:00Z",
    threshold_amber: 80,
    threshold_red: 95,
  },
  not_configured: null,
};

const notConfiguredRow = {
  provider: "ollama",
  account_hash: "",
  model: "llama3:8b",
  observed_at: "2026-05-13T12:00:00Z",
  stale: false,
  store_backend: "",
  pricing_source: "",
  variant: "not_configured",
  rate_limit: null,
  token_spend: null,
  not_configured: { reason: "local provider exposes no quota signal" },
};

vi.mock("@/composables/useToast", () => ({
  showToast: vi.fn(),
}));

describe("ProviderQuotaDashboardView", () => {
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

  it("issues GET /api/v1/providers/quota with credentials: include on mount", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await mountWithRouter();
    await flushPromises();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toContain("/api/v1/providers/quota");
    expect((init as RequestInit).credentials).toBe("include");
  });

  it("renders the empty state when the aggregator returns []", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const { wrapper } = await mountWithRouter();
    await flushPromises();

    expect(
      wrapper.find('[data-testid="provider-quota-dashboard-empty"]').exists(),
    ).toBe(true);
    expect(
      wrapper.find('[data-testid="provider-quota-dashboard-table"]').exists(),
    ).toBe(false);
  });

  it("renders the feature-off banner when the server reports 501", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response("not_implemented\n", {
        status: 501,
        headers: { "Content-Type": "text/plain" },
      }),
    );

    const { wrapper } = await mountWithRouter();
    await flushPromises();

    expect(
      wrapper
        .find('[data-testid="provider-quota-dashboard-feature-off"]')
        .exists(),
    ).toBe(true);
  });

  it("surfaces a 401 from the backend as an error message (uniform B8 carry-through)", async () => {
    // The Auth Track middleware emits 401 with the literal
    // "unauthenticated" body before the handler runs; the SPA's
    // fetchProviderQuotas turns any non-2xx (other than 501) into a
    // thrown Error, which the view displays in the error region.
    fetchMock.mockResolvedValueOnce(
      new Response("unauthenticated\n", {
        status: 401,
        headers: { "Content-Type": "text/plain" },
      }),
    );

    const { wrapper } = await mountWithRouter();
    await flushPromises();

    expect(
      wrapper.find('[data-testid="provider-quota-dashboard-error"]').exists(),
    ).toBe(true);
  });

  it("renders one row per entry with provider, truncated account, and model", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(
        JSON.stringify([rateLimitRow, tokenSpendRow, notConfiguredRow]),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      ),
    );

    const { wrapper } = await mountWithRouter();
    await flushPromises();

    // 3 rows in the table body.
    const rows = wrapper.findAll(".quota-dashboard__row");
    expect(rows).toHaveLength(3);

    // Rate-limit row.
    const rateRow = wrapper.find(
      '[data-testid="provider-quota-row-anthropic-abc12345abc-claude-opus-4-7"]',
    );
    expect(rateRow.exists()).toBe(true);
    // Account hash truncated to first 8 chars.
    expect(
      rateRow.find('[data-testid="provider-quota-row-account"]').text(),
    ).toBe("abc12345");
    expect(
      rateRow.find('[data-testid="provider-quota-row-variant"]').text(),
    ).toBe("Rate-limit");
    expect(
      rateRow.find('[data-testid="provider-quota-row-summary"]').text(),
    ).toContain("12% remaining");

    // Token-spend row.
    const spendRow = wrapper.find(
      '[data-testid="provider-quota-row-openai-def67890def-gpt-4o"]',
    );
    expect(spendRow.exists()).toBe(true);
    expect(
      spendRow.find('[data-testid="provider-quota-row-summary"]').text(),
    ).toContain("12.34");

    // Not-configured row with empty account_hash falls back to "(default)".
    const naRow = wrapper.find(
      '[data-testid="provider-quota-row-ollama--llama3:8b"]',
    );
    expect(naRow.exists()).toBe(true);
    expect(
      naRow.find('[data-testid="provider-quota-row-account"]').text(),
    ).toBe("(default)");
    expect(
      naRow.find('[data-testid="provider-quota-row-summary"]').text(),
    ).toContain("local provider exposes no quota signal");
  });

  it("opens the ProviderQuotaPanel modal when a row is clicked", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify([tokenSpendRow]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const { wrapper } = await mountWithRouter();
    await flushPromises();

    // Panel not visible initially.
    expect(wrapper.find('[data-testid="provider-quota-panel"]').exists()).toBe(
      false,
    );

    const row = wrapper.find(
      '[data-testid="provider-quota-row-openai-def67890def-gpt-4o"]',
    );
    await row.trigger("click");
    await flushPromises();

    expect(wrapper.find('[data-testid="provider-quota-panel"]').exists()).toBe(
      true,
    );
    expect(
      wrapper.find('[data-testid="provider-quota-panel-model"]').text(),
    ).toBe("gpt-4o");
  });
});
