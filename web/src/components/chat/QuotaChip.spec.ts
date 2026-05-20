import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import QuotaChip from "./QuotaChip.vue";
import { useChatStore } from "@/stores/chatStore";
import { useQuotaStore } from "@/stores/quotaStore";
import type { SSEProviderQuotaEvent } from "@/lib/sseEvent";

/**
 * QuotaChip component specs — pin behaviour observable to a user.
 *
 * The chip discriminates on the `variant` field of the most-recent
 * SSE `provider_quota` event for the active (provider, model) pair:
 *
 *   - `rate_limit`     → "42% remaining · resets 3m"
 *   - `token_spend`    → "$2.41 / $50.00" + thin bar
 *   - `not_configured` → "—" with tooltip surfacing Reason
 *
 * Threshold colour palette (OD-9 — plan lines 517-520) pinned
 * separately per branch.
 *
 * Memory gotchas honoured:
 *   - `feedback_pinia_onmounted_clobbers_seed` — seed runs AFTER
 *     `flushPromises()` post-mount so any onMounted async load
 *     in the chip cannot clobber the seed.
 *   - `feedback_response_ok_mock_gotcha` — the chip does not call
 *     fetch directly so no `ok` getter mock is needed; this spec
 *     would add it if a future PR threads a fetch into the chip.
 */

const PROVIDER = "anthropic";
const MODEL = "claude-opus-4-7";
const ACCOUNT_HASH = "deadbeef1234";

function baseEvent(): SSEProviderQuotaEvent {
  return {
    kind: "provider_quota",
    provider: PROVIDER,
    accountHash: ACCOUNT_HASH,
    model: MODEL,
    observedAt: "2026-05-13T12:00:00Z",
    stale: false,
    storeBackend: "memory",
    pricingSource: "flowstate-default-v1",
    variant: "not_configured",
    rateLimit: null,
    tokenSpend: null,
    notConfigured: { reason: "awaiting-first-response" },
  };
}

async function mountWithEvent(event: SSEProviderQuotaEvent) {
  const chat = useChatStore();
  chat.currentProviderId = PROVIDER;
  chat.currentModelId = MODEL;

  const wrapper = mount(QuotaChip);

  // Post-mount seed per memory `feedback_pinia_onmounted_clobbers_seed`
  // — dispatch the SSE event AFTER the component's onMounted has
  // flushed so any async store load inside the chip cannot clobber
  // the seed with an empty fetch result.
  await flushPromises();
  useQuotaStore().applyProviderQuotaEvent(event);
  await flushPromises();
  return wrapper;
}

describe("QuotaChip", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-05-13T12:00:00Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe("visibility gating", () => {
    it("does not render when no provider/model is selected", async () => {
      const wrapper = mount(QuotaChip);
      await flushPromises();
      expect(wrapper.find('[data-testid="provider-quota-chip"]').exists()).toBe(
        false,
      );
    });

    it("does not render before any provider_quota event has arrived", async () => {
      const chat = useChatStore();
      chat.currentProviderId = PROVIDER;
      chat.currentModelId = MODEL;
      const wrapper = mount(QuotaChip);
      await flushPromises();
      expect(wrapper.find('[data-testid="provider-quota-chip"]').exists()).toBe(
        false,
      );
    });

    it("renders the chip after the first provider_quota event arrives", async () => {
      const wrapper = await mountWithEvent(baseEvent());
      expect(wrapper.find('[data-testid="provider-quota-chip"]').exists()).toBe(
        true,
      );
    });
  });

  describe("RateLimit variant", () => {
    function rateLimitEvent(
      percentRemaining: number,
      resetIso: string,
    ): SSEProviderQuotaEvent {
      return {
        ...baseEvent(),
        variant: "rate_limit",
        rateLimit: {
          requests: { limit: 1000, remaining: 750, reset: resetIso },
          tokens: { limit: 100000, remaining: 75000, reset: resetIso },
          input: { limit: -1, remaining: -1, reset: "" },
          output: { limit: -1, remaining: -1, reset: "" },
          tightestPercentRemaining: percentRemaining,
          tightestResetAt: resetIso,
        },
        tokenSpend: null,
        notConfigured: null,
      };
    }

    it('renders "{N}% remaining · resets Nm" with minute-formatted reset', async () => {
      const wrapper = await mountWithEvent(
        rateLimitEvent(42, "2026-05-13T12:03:00Z"), // 3 minutes from now
      );
      const label = wrapper.find(
        '[data-testid="provider-quota-rate-limit-label"]',
      );
      expect(label.exists()).toBe(true);
      expect(label.text()).toBe("42% remaining · resets 3m");
    });

    it("formats short resets in seconds when under 60s", async () => {
      const wrapper = await mountWithEvent(
        rateLimitEvent(42, "2026-05-13T12:00:30Z"), // 30s from now
      );
      expect(
        wrapper.find('[data-testid="provider-quota-rate-limit-label"]').text(),
      ).toBe("42% remaining · resets 30s");
    });

    it('formats long resets in "Nh{Mm}" form when over an hour', async () => {
      const wrapper = await mountWithEvent(
        rateLimitEvent(42, "2026-05-13T13:30:00Z"), // 1h30m from now
      );
      expect(
        wrapper.find('[data-testid="provider-quota-rate-limit-label"]').text(),
      ).toBe("42% remaining · resets 1h30");
    });

    it("omits the reset suffix when the reset wall-clock is empty", async () => {
      const wrapper = await mountWithEvent(rateLimitEvent(42, ""));
      expect(
        wrapper.find('[data-testid="provider-quota-rate-limit-label"]').text(),
      ).toBe("42% remaining");
    });

    it("applies neutral severity at 42% remaining (>= 20%)", async () => {
      const wrapper = await mountWithEvent(
        rateLimitEvent(42, "2026-05-13T12:03:00Z"),
      );
      expect(
        wrapper
          .find('[data-testid="provider-quota-chip"]')
          .attributes("data-severity"),
      ).toBe("neutral");
    });

    it("applies warning severity at 10% remaining (5-20% band)", async () => {
      const wrapper = await mountWithEvent(
        rateLimitEvent(10, "2026-05-13T12:03:00Z"),
      );
      expect(
        wrapper
          .find('[data-testid="provider-quota-chip"]')
          .attributes("data-severity"),
      ).toBe("warning");
    });

    it("applies danger severity at 2% remaining (< 5%)", async () => {
      const wrapper = await mountWithEvent(
        rateLimitEvent(2, "2026-05-13T12:03:00Z"),
      );
      expect(
        wrapper
          .find('[data-testid="provider-quota-chip"]')
          .attributes("data-severity"),
      ).toBe("danger");
    });

    it("applies neutral severity at -1 sentinel (no signal)", async () => {
      const wrapper = await mountWithEvent(rateLimitEvent(-1, ""));
      expect(
        wrapper
          .find('[data-testid="provider-quota-chip"]')
          .attributes("data-severity"),
      ).toBe("neutral");
    });
  });

  describe("TokenSpend variant", () => {
    function tokenSpendEvent(
      spentMinor: number,
      capMinor: number,
      thresholdAmber = 80,
      thresholdRed = 95,
    ): SSEProviderQuotaEvent {
      return {
        ...baseEvent(),
        variant: "token_spend",
        rateLimit: null,
        notConfigured: null,
        tokenSpend: {
          spentMinor,
          spentCurrency: "USD",
          spentUsdMinor: spentMinor,
          capMinor,
          capCurrency: "USD",
          period: "monthly",
          periodStart: "2026-05-01T00:00:00Z",
          periodEnd: "2026-06-01T00:00:00Z",
          thresholdAmber,
          thresholdRed,
        },
      };
    }

    it('renders "$X.XX / $Y.YY" for the capped USD case', async () => {
      const wrapper = await mountWithEvent(tokenSpendEvent(241, 5000));
      expect(
        wrapper.find('[data-testid="provider-quota-token-spend-label"]').text(),
      ).toBe("$2.41 / $50.00");
    });

    it("renders just the spent figure for the uncapped case (capMinor=0)", async () => {
      const wrapper = await mountWithEvent(tokenSpendEvent(241, 0));
      expect(
        wrapper.find('[data-testid="provider-quota-token-spend-label"]').text(),
      ).toBe("$2.41");
    });

    it("renders the thin bar when capped", async () => {
      const wrapper = await mountWithEvent(tokenSpendEvent(2500, 5000)); // 50%
      const bar = wrapper.find(
        '[data-testid="provider-quota-token-spend-bar"]',
      );
      expect(bar.exists()).toBe(true);
      // The bar's inner fill carries the width inline style — assert
      // it lands at 50% so the bar visually matches the figure.
      const fill = bar.find("span.quota-chip__bar-fill");
      expect(fill.exists()).toBe(true);
      expect(fill.attributes("style")).toContain("width: 50%");
    });

    it("omits the bar when uncapped", async () => {
      const wrapper = await mountWithEvent(tokenSpendEvent(241, 0));
      expect(
        wrapper.find('[data-testid="provider-quota-token-spend-bar"]').exists(),
      ).toBe(false);
    });

    it("renders CNY native currency with ¥ glyph", async () => {
      const wrapper = await mountWithEvent({
        ...tokenSpendEvent(1840, 50000),
        tokenSpend: {
          spentMinor: 1840,
          spentCurrency: "CNY",
          spentUsdMinor: 255,
          capMinor: 50000,
          capCurrency: "CNY",
          period: "monthly",
          periodStart: "2026-05-01T00:00:00Z",
          periodEnd: "2026-06-01T00:00:00Z",
          thresholdAmber: 80,
          thresholdRed: 95,
        },
      });
      expect(
        wrapper.find('[data-testid="provider-quota-token-spend-label"]').text(),
      ).toBe("¥18.40 / ¥500.00");
    });

    it("applies neutral severity at 50% used (< amber 80%)", async () => {
      const wrapper = await mountWithEvent(tokenSpendEvent(2500, 5000));
      expect(
        wrapper
          .find('[data-testid="provider-quota-chip"]')
          .attributes("data-severity"),
      ).toBe("neutral");
    });

    it("applies warning severity at 85% used (amber 80-95%)", async () => {
      const wrapper = await mountWithEvent(tokenSpendEvent(4250, 5000));
      expect(
        wrapper
          .find('[data-testid="provider-quota-chip"]')
          .attributes("data-severity"),
      ).toBe("warning");
    });

    it("applies danger severity at 97% used (>= red 95%)", async () => {
      const wrapper = await mountWithEvent(tokenSpendEvent(4850, 5000));
      expect(
        wrapper
          .find('[data-testid="provider-quota-chip"]')
          .attributes("data-severity"),
      ).toBe("danger");
    });

    it("honours operator-supplied thresholds (60% amber / 90% red)", async () => {
      const wrapper = await mountWithEvent(tokenSpendEvent(3000, 5000, 60, 90)); // 60% used
      // 60% is exactly the amber threshold per >= semantics
      expect(
        wrapper
          .find('[data-testid="provider-quota-chip"]')
          .attributes("data-severity"),
      ).toBe("warning");
    });

    it('emits an "open" event when the chip is clicked (token_spend variant)', async () => {
      const wrapper = await mountWithEvent(tokenSpendEvent(241, 5000));
      await wrapper
        .find('[data-testid="provider-quota-chip"]')
        .trigger("click");
      expect(wrapper.emitted("open")).toHaveLength(1);
      expect(wrapper.emitted("open")?.[0]?.[0]).toMatchObject({
        variant: "token_spend",
        provider: PROVIDER,
        model: MODEL,
      });
    });
  });

  describe("NotConfigured variant", () => {
    function notConfiguredEvent(reason: string): SSEProviderQuotaEvent {
      return {
        ...baseEvent(),
        variant: "not_configured",
        rateLimit: null,
        tokenSpend: null,
        notConfigured: { reason },
      };
    }

    it('renders "—" placeholder with neutral severity', async () => {
      const wrapper = await mountWithEvent(
        notConfiguredEvent("subscription-only"),
      );
      const label = wrapper.find(
        '[data-testid="provider-quota-not-configured-label"]',
      );
      expect(label.exists()).toBe(true);
      expect(label.text()).toBe("—");
      expect(
        wrapper
          .find('[data-testid="provider-quota-chip"]')
          .attributes("data-severity"),
      ).toBe("neutral");
    });

    it("surfaces the Reason verbatim on the data-reason attribute for assistive tech", async () => {
      const wrapper = await mountWithEvent(
        notConfiguredEvent("unknown-model:claude-experimental"),
      );
      const label = wrapper.find(
        '[data-testid="provider-quota-not-configured-label"]',
      );
      expect(label.attributes("data-reason")).toBe(
        "unknown-model:claude-experimental",
      );
    });

    it('does not emit "open" on click (only token_spend opens the panel)', async () => {
      const wrapper = await mountWithEvent(
        notConfiguredEvent("subscription-only"),
      );
      await wrapper
        .find('[data-testid="provider-quota-chip"]')
        .trigger("click");
      expect(wrapper.emitted("open")).toBeUndefined();
    });
  });

  describe("tooltip", () => {
    it("surfaces single-instance scope disclosure when StoreBackend=memory", async () => {
      const wrapper = await mountWithEvent({
        ...baseEvent(),
        storeBackend: "memory",
        variant: "token_spend",
        notConfigured: null,
        tokenSpend: {
          spentMinor: 241,
          spentCurrency: "USD",
          spentUsdMinor: 241,
          capMinor: 5000,
          capCurrency: "USD",
          period: "monthly",
          periodStart: "2026-05-01T00:00:00Z",
          periodEnd: "2026-06-01T00:00:00Z",
          thresholdAmber: 80,
          thresholdRed: 95,
        },
      });
      const chip = wrapper.find('[data-testid="provider-quota-chip"]');
      expect(chip.attributes("title")).toContain("Single-instance scope");
    });

    it('surfaces "shared across instances" copy when StoreBackend=redis', async () => {
      const wrapper = await mountWithEvent({
        ...baseEvent(),
        storeBackend: "redis",
      });
      const chip = wrapper.find('[data-testid="provider-quota-chip"]');
      expect(chip.attributes("title")).toContain(
        "Shared across all FlowState instances",
      );
    });

    it("includes USD equivalent in tooltip for a non-USD spend", async () => {
      const wrapper = await mountWithEvent({
        ...baseEvent(),
        storeBackend: "memory",
        variant: "token_spend",
        notConfigured: null,
        tokenSpend: {
          spentMinor: 1840,
          spentCurrency: "CNY",
          spentUsdMinor: 255,
          capMinor: 50000,
          capCurrency: "CNY",
          period: "monthly",
          periodStart: "2026-05-01T00:00:00Z",
          periodEnd: "2026-06-01T00:00:00Z",
          thresholdAmber: 80,
          thresholdRed: 95,
        },
      });
      const chip = wrapper.find('[data-testid="provider-quota-chip"]');
      expect(chip.attributes("title")).toContain("USD equivalent: $2.55");
    });

    it("surfaces stale signal in tooltip", async () => {
      const wrapper = await mountWithEvent({ ...baseEvent(), stale: true });
      const chip = wrapper.find('[data-testid="provider-quota-chip"]');
      expect(chip.attributes("title")).toContain("Stale");
    });
  });
});
