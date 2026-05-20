<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { fetchProviderQuotas, type ProviderQuotaEntry } from "@/api";
import ProviderQuotaPanel from "@/components/ProviderQuotaPanel.vue";

/**
 * ProviderQuotaDashboardView — `/providers/quota` route surface
 * (Provider Quota and Spend Visibility plan, May 2026, PR5a).
 *
 * Loads GET /api/v1/providers/quota on mount, renders one row per
 * (provider, account_hash, model) tuple in a table. Clicking a row
 * opens the ProviderQuotaPanel modal for the deep view.
 *
 * Auth: the underlying GET is gated server-side via registerProtected
 * (auth_routes.go) — a 401 on no-session surfaces as a thrown Error
 * which the catch block treats as "show empty state". The Auth Track
 * SPA-wide redirect pattern (catch-401 → push('/login')) is layered
 * separately and is not introduced here so the dashboard renders
 * cleanly when auth is off-by-default in the v2/v3 ship state.
 *
 * Per memory feedback_pinia_onmounted_clobbers_seed — onMounted loads
 * the snapshot after Pinia seeds (in the spec, seed AFTER
 * flushPromises so the post-mount fetch resolves first).
 */
defineOptions({ name: "ProviderQuotaDashboardView" });

const entries = ref<ProviderQuotaEntry[]>([]);
const featureWired = ref<boolean>(true);
const loading = ref<boolean>(true);
const errorMessage = ref<string>("");
const selectedEntry = ref<ProviderQuotaEntry | null>(null);

const isEmpty = computed(() => {
  return featureWired.value && !loading.value && entries.value.length === 0;
});

/**
 * truncateAccountHash — first 8 chars of the SHA-256-truncated hash,
 * or "(default)" when empty (single-account-per-provider v1 default).
 */
function truncateAccountHash(hash: string): string {
  if (!hash) return "(default)";
  return hash.length > 8 ? hash.slice(0, 8) : hash;
}

/**
 * variantLabel — human-readable variant for the table column. The
 * canonical discriminant stays the snake_case wire string; this is
 * presentation only.
 */
function variantLabel(variant: ProviderQuotaEntry["variant"]): string {
  switch (variant) {
    case "rate_limit":
      return "Rate-limit";
    case "token_spend":
      return "Token spend";
    case "not_configured":
      return "Not configured";
  }
}

/**
 * remainingOrSpent — one-line summary for the table cell. Per-variant
 * formatting: rate_limit shows "N% remaining" (— for no-signal);
 * token_spend shows the native+USD spend (with cap if configured);
 * not_configured shows the reason verbatim.
 */
function remainingOrSpent(entry: ProviderQuotaEntry): string {
  if (entry.variant === "rate_limit" && entry.rateLimit !== null) {
    const pct = entry.rateLimit.tightestPercentRemaining;
    return pct < 0 ? "—" : `${pct}% remaining`;
  }
  if (entry.variant === "token_spend" && entry.tokenSpend !== null) {
    const ts = entry.tokenSpend;
    const spent = `${(ts.spentMinor / 100).toFixed(2)} ${ts.spentCurrency}`;
    if (ts.capMinor <= 0) return spent;
    const cap = `${(ts.capMinor / 100).toFixed(2)} ${ts.capCurrency || ts.spentCurrency}`;
    return `${spent} / ${cap}`;
  }
  if (entry.variant === "not_configured" && entry.notConfigured !== null) {
    return entry.notConfigured.reason;
  }
  return "—";
}

async function loadEntries(): Promise<void> {
  loading.value = true;
  errorMessage.value = "";
  try {
    const res = await fetchProviderQuotas();
    if (res === null) {
      // 501 — feature not wired in this deployment. Render an
      // explanatory empty state rather than crashing the route.
      featureWired.value = false;
      entries.value = [];
    } else {
      featureWired.value = true;
      entries.value = res;
    }
  } catch (err) {
    errorMessage.value =
      err instanceof Error ? err.message : "Failed to load quotas";
    entries.value = [];
  } finally {
    loading.value = false;
  }
}

onMounted(loadEntries);

function openPanel(entry: ProviderQuotaEntry): void {
  selectedEntry.value = entry;
}

function closePanel(): void {
  selectedEntry.value = null;
}

async function onReset(): Promise<void> {
  // After a successful reset, re-fetch so the aggregator shows the
  // zeroed snapshot. The panel emits `close` after `reset` so the
  // selectedEntry is already nullified by then.
  await loadEntries();
}
</script>

<template>
  <div class="quota-dashboard" data-testid="provider-quota-dashboard">
    <header class="quota-dashboard__header">
      <h1>Provider Quota &amp; Spend</h1>
      <p class="quota-dashboard__subtitle">
        Per-(provider, account, model) tuple snapshot the FlowState engine has
        observed. Click a row for the full breakdown.
      </p>
    </header>

    <div
      v-if="loading"
      class="quota-dashboard__loading"
      data-testid="provider-quota-dashboard-loading"
    >
      Loading provider quotas&hellip;
    </div>

    <div
      v-else-if="errorMessage"
      class="quota-dashboard__error"
      data-testid="provider-quota-dashboard-error"
    >
      {{ errorMessage }}
    </div>

    <div
      v-else-if="!featureWired"
      class="quota-dashboard__empty"
      data-testid="provider-quota-dashboard-feature-off"
    >
      Provider quota tracking is not wired in this deployment.
    </div>

    <div
      v-else-if="isEmpty"
      class="quota-dashboard__empty"
      data-testid="provider-quota-dashboard-empty"
    >
      No providers observed yet. Send a message in a chat session to populate
      this view.
    </div>

    <table
      v-else
      class="quota-dashboard__table"
      data-testid="provider-quota-dashboard-table"
    >
      <thead>
        <tr>
          <th>Provider</th>
          <th>Account</th>
          <th>Model</th>
          <th>Variant</th>
          <th>Remaining / Spent</th>
          <th>Store</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="entry in entries"
          :key="`${entry.provider}:${entry.accountHash}:${entry.model}`"
          :data-testid="`provider-quota-row-${entry.provider}-${entry.accountHash}-${entry.model}`"
          class="quota-dashboard__row"
          @click="openPanel(entry)"
        >
          <td data-testid="provider-quota-row-provider">
            {{ entry.provider }}
          </td>
          <td data-testid="provider-quota-row-account">
            {{ truncateAccountHash(entry.accountHash) }}
          </td>
          <td data-testid="provider-quota-row-model">{{ entry.model }}</td>
          <td data-testid="provider-quota-row-variant">
            {{ variantLabel(entry.variant) }}
          </td>
          <td data-testid="provider-quota-row-summary">
            {{ remainingOrSpent(entry) }}
          </td>
          <td data-testid="provider-quota-row-store">
            {{ entry.storeBackend || "—" }}
          </td>
        </tr>
      </tbody>
    </table>

    <ProviderQuotaPanel
      v-if="selectedEntry !== null"
      :entry="selectedEntry"
      @close="closePanel"
      @reset="onReset"
    />
  </div>
</template>

<style scoped>
.quota-dashboard {
  padding: 1.5rem;
  max-width: 960px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.quota-dashboard__header h1 {
  font-size: 1.35rem;
  font-weight: 600;
  color: var(--text-primary, #f5f5f5);
  margin: 0 0 0.4rem;
}

.quota-dashboard__subtitle {
  font-size: 0.9rem;
  color: var(--text-secondary, #d0d0d0);
  margin: 0;
}

.quota-dashboard__loading,
.quota-dashboard__empty,
.quota-dashboard__error {
  padding: 1.5rem;
  border: 1px dashed var(--border, rgba(255, 255, 255, 0.12));
  border-radius: var(--radius, 6px);
  text-align: center;
  color: var(--text-secondary, #d0d0d0);
  font-size: 0.9rem;
}

.quota-dashboard__error {
  border-color: var(--error, #dc2626);
  color: var(--error, #dc2626);
}

.quota-dashboard__table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.88rem;
}

.quota-dashboard__table th,
.quota-dashboard__table td {
  padding: 0.55rem 0.6rem;
  text-align: left;
  border-bottom: 1px solid var(--border, rgba(255, 255, 255, 0.08));
}

.quota-dashboard__table th {
  font-weight: 500;
  color: var(--text-secondary, #d0d0d0);
  font-size: 0.78rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.quota-dashboard__row {
  cursor: pointer;
}

.quota-dashboard__row:hover {
  background: rgba(255, 255, 255, 0.03);
}
</style>
