/**
 * quotaStore — Pinia store for the Provider Quota and Spend Visibility
 * feature (May 2026 plan PR4a).
 *
 * Subscribes to the SSE `provider_quota` event that the engine emits
 * inline before every reply and after every turn (see
 * internal/engine/engine.go:2519-2533 (Stream) +
 * internal/engine/provider_quota.go:buildProviderQuotaChunk). The
 * store keeps the most recent Snapshot per
 * `<provider>:<accountHash>:<model>` key and exposes a selector the
 * QuotaChip uses to render the matching variant.
 *
 * Discriminator contract: every snapshot in `snapshots` has
 * `variant === 'rate_limit' | 'token_spend' | 'not_configured'` and
 * the matching nested object populated. The TypeScript-side
 * discriminated union (SSEProviderQuotaEvent) enforces this at the
 * boundary; the store re-asserts via a render-time switch in the
 * chip component.
 *
 * Plan §"Vue integration" lines 326-336 + OD-9 thresholds (517-520).
 */

import { defineStore } from "pinia";
import type {
  SSEProviderQuotaEvent,
  SSEProviderQuotaRateLimit,
  SSEProviderQuotaTokenSpend,
  SSEProviderQuotaNotConfig,
} from "@/lib/sseEvent";

/**
 * ProviderQuotaSnapshot is the store-side projection of
 * SSEProviderQuotaEvent. Identical shape, distinct name to keep
 * "store value type" separate from "wire event type" in TS imports.
 */
export interface ProviderQuotaSnapshot {
  provider: string;
  accountHash: string;
  model: string;
  observedAt: string;
  stale: boolean;
  storeBackend: string;
  pricingSource: string;
  variant: "rate_limit" | "token_spend" | "not_configured";
  rateLimit: SSEProviderQuotaRateLimit | null;
  tokenSpend: SSEProviderQuotaTokenSpend | null;
  notConfigured: SSEProviderQuotaNotConfig | null;
}

/**
 * snapshotKey builds the partition key for the snapshots map.
 * Mirrors the Go-side store.Key shape (provider | account_hash |
 * model) — the engine writes one Snapshot per key and the chip
 * reads the same partition.
 */
export function snapshotKey(
  provider: string,
  accountHash: string,
  model: string,
): string {
  return `${provider}:${accountHash}:${model}`;
}

/**
 * providerQuotaSnapshotEqual reports structural equality across two
 * snapshots for the same partition. Used by applyProviderQuotaEvent's
 * idempotency gate (Phase-5 §1c-β): the SSE handler + poll-diff caller
 * both invoke the action with the same snapshot during the transitional
 * dual-surface phase, and a structural-equal short-circuit prevents Vue
 * reactivity from observing a fresh object reference as a state change.
 *
 * Field comparison walks the discriminator (`variant`) + observedAt +
 * the variant payload's primary figure (spent_minor / tightest_percent /
 * reason). The full nested structure (RateLimit windows etc.) is not
 * deep-compared field-by-field — observed_at moves on every refresh and
 * is the canonical "this is a new emission" signal; if observed_at is
 * identical, the upstream emitter produced the same value-set.
 */
export function providerQuotaSnapshotEqual(
  a: ProviderQuotaSnapshot,
  b: ProviderQuotaSnapshot,
): boolean {
  if (
    a.provider !== b.provider ||
    a.accountHash !== b.accountHash ||
    a.model !== b.model ||
    a.observedAt !== b.observedAt ||
    a.stale !== b.stale ||
    a.storeBackend !== b.storeBackend ||
    a.pricingSource !== b.pricingSource ||
    a.variant !== b.variant
  ) {
    return false;
  }
  // Variant-specific primary figure comparison. Each variant has
  // exactly one non-null payload (the discriminator guarantee).
  if (a.variant === "token_spend") {
    if (a.tokenSpend?.spentMinor !== b.tokenSpend?.spentMinor) return false;
  } else if (a.variant === "rate_limit") {
    if (
      a.rateLimit?.tightestPercentRemaining !==
      b.rateLimit?.tightestPercentRemaining
    )
      return false;
  } else if (a.variant === "not_configured") {
    if (a.notConfigured?.reason !== b.notConfigured?.reason) return false;
  }
  return true;
}

interface QuotaStoreState {
  /**
   * snapshots — most recent ProviderQuotaSnapshot per
   * `<provider>:<accountHash>:<model>` key. Populated by
   * applyProviderQuotaEvent on every SSE `provider_quota` chunk;
   * cleared by reset() on session change (the chat store fires this
   * from loadSessionMessages so a stale prior-session figure does
   * not bleed into the new session's empty chip).
   */
  snapshots: Record<string, ProviderQuotaSnapshot>;
}

export const useQuotaStore = defineStore("quota", {
  state: (): QuotaStoreState => ({
    snapshots: {},
  }),

  getters: {
    /**
     * currentQuotaFor returns the most recent snapshot for the
     * (provider, accountHash, model) tuple, or null when none has
     * been seen on the SSE wire yet. The chip uses this to gate the
     * render — null → hide (the no-snapshot bootstrap edge); non-null
     * → render the matching variant.
     */
    currentQuotaFor: (state) => {
      return (
        provider: string,
        accountHash: string,
        model: string,
      ): ProviderQuotaSnapshot | null => {
        const key = snapshotKey(provider, accountHash, model);
        return state.snapshots[key] ?? null;
      };
    },

    /**
     * anyQuotaFor returns the most recent snapshot matching
     * (provider, model) for ANY accountHash. The chip falls back to
     * this when it does not know the active account hash (the
     * bootstrap edge where the engine has not yet stamped one).
     * Returns the first match by iteration order — for v1
     * single-account-per-provider deployments there is exactly one
     * matching key so the iteration order is moot.
     */
    anyQuotaFor: (state) => {
      return (
        provider: string,
        model: string,
      ): ProviderQuotaSnapshot | null => {
        for (const snap of Object.values(state.snapshots)) {
          if (snap.provider === provider && snap.model === model) {
            return snap;
          }
        }
        return null;
      };
    },
  },

  actions: {
    /**
     * applyProviderQuotaEvent ingests a parsed SSE provider_quota
     * event and updates the snapshots map.
     *
     * The chat store routes the typed event through this action from
     * its `applyContentEvent` dispatcher (see
     * `web/src/stores/chatStore.ts` `applyContentEvent` switch on
     * `event.kind === 'provider_quota'`). The action is also the
     * test-injection seam — vitest specs construct an event and
     * call this directly without standing up an EventSource.
     */
    applyProviderQuotaEvent(event: SSEProviderQuotaEvent): void {
      const snap: ProviderQuotaSnapshot = {
        provider: event.provider,
        accountHash: event.accountHash,
        model: event.model,
        observedAt: event.observedAt,
        stale: event.stale,
        storeBackend: event.storeBackend,
        pricingSource: event.pricingSource,
        variant: event.variant,
        rateLimit: event.rateLimit,
        tokenSpend: event.tokenSpend,
        notConfigured: event.notConfigured,
      };
      const key = snapshotKey(event.provider, event.accountHash, event.model);
      // Phase-5 §1c-β idempotency gate. The transitional state has two
      // callers for the same partition's snapshot — the SSE branch at
      // chatStore.ts:2818-2828 and the new pollTurnUntilTerminal poll-
      // diff caller. Without this gate every poll's matching snapshot
      // would re-write the `snapshots` map with `{ ...this.snapshots,
      // [key]: snap }` — a fresh object reference Vue's reactivity
      // observes as a state change even when the snapshot value is
      // identical, triggering needless chip re-renders.
      //
      // Compare against the existing snapshot for the partition key
      // by structural equality on the four primitives that move per
      // emission (observedAt, stale + the variant payload's primary
      // figure). Identical → short-circuit; differing → write.
      const existing = this.snapshots[key];
      if (
        existing !== undefined &&
        providerQuotaSnapshotEqual(existing, snap)
      ) {
        return;
      }
      this.snapshots = { ...this.snapshots, [key]: snap };
    },

    /**
     * reset clears all in-memory snapshots. Fired on session change
     * from the chat store's loadSessionMessages so the chip starts
     * blank in the new session and re-hydrates from the SSE
     * re-attach. Per memory `feedback_response_ok_mock_gotcha` and
     * the Pinia post-mount seed gotcha — the chip's onMounted hook
     * should NOT call reset (it would clobber a seed dispatched
     * pre-mount); reset is a session-change action only.
     */
    reset(): void {
      this.snapshots = {};
    },
  },
});
