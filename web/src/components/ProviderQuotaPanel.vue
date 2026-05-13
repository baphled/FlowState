<script setup lang="ts">
import { computed, ref } from 'vue'
import { resetProviderQuotaSpend, type ProviderQuotaEntry } from '@/api'
import { showToast } from '@/composables/useToast'

/**
 * ProviderQuotaPanel — modal deep-view of one provider/account/model
 * quota row (Provider Quota and Spend Visibility plan, May 2026, PR5a).
 *
 * Branches:
 *   - rate_limit:    shows the four windows (Requests/Tokens/Input/
 *                    Output), tightest summary, and reset times.
 *   - token_spend:   shows native + USD equivalent (OD-6) + pricing
 *                    source + observed-at + period start/end +
 *                    thresholds + cap + estimator-vs-actual drift
 *                    column (OD-7) + "Reset spend counter" button
 *                    (OD-8).
 *   - not_configured: shows the Reason verbatim + a tooltip explaining
 *                    the provider class.
 *
 * Click-outside / Escape key closes the modal via the `close` emit.
 *
 * Reset flow (token_spend only):
 *   1. User clicks "Reset spend counter".
 *   2. Confirm modal renders inline.
 *   3. On confirm → POST /api/v1/providers/quota/reset.
 *   4. Toast on success / error.
 *   5. Emit `reset` so the parent re-fetches the aggregator.
 *
 * Per memory feedback_response_ok_mock_gotcha — fetch mocks in the
 * spec must use real Response objects so `if (!response.ok)` resolves.
 */
defineOptions({ name: 'ProviderQuotaPanel' })

const props = defineProps<{
  entry: ProviderQuotaEntry
}>()

const emit = defineEmits<{
  close: []
  reset: []
}>()

const showResetConfirm = ref(false)
const resetting = ref(false)

/**
 * truncateAccountHash — clip the SHA-256-truncated hash to the first
 * eight characters for the header / first row of the table. Matches
 * the chip's truncation pattern.
 */
function truncateAccountHash(hash: string): string {
  if (!hash) return '(default)'
  return hash.length > 8 ? hash.slice(0, 8) : hash
}

/**
 * formatMoney — same idiom as QuotaChip.vue. Renders `$12.34` for
 * USD, `¥18.40` for CNY, `£7.50` for GBP, `€9.10` for EUR. Unknown
 * currencies fall back to `<code> <major>.<minor>` ("XYZ 12.34").
 */
function formatMoney(minor: number, currency: string): string {
  const major = (minor / 100).toFixed(2)
  switch (currency) {
    case 'USD':
      return `$${major}`
    case 'CNY':
      return `¥${major}`
    case 'GBP':
      return `£${major}`
    case 'EUR':
      return `€${major}`
    default:
      return `${currency} ${major}`
  }
}

/**
 * formatPercentRemaining — "—" for the -1 no-signal sentinel; "%"
 * suffix otherwise.
 */
function formatPercentRemaining(pct: number): string {
  if (pct < 0) return '—'
  return `${pct}%`
}

/**
 * formatTimestamp — render ISO-8601 as the locale's medium date+time
 * format. Empty input renders as "—" so a missing field doesn't show
 * "Invalid Date".
 */
function formatTimestamp(iso: string): string {
  if (!iso) return '—'
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return iso
  const d = new Date(t)
  return d.toLocaleString()
}

/**
 * OD-7 estimator-vs-actual drift — the panel exposes a delta column
 * because the SSE event family does not yet carry the estimator
 * figure on its own. v1 ships the column with "—" when the entry
 * does not include a measured estimator value; PR6 will thread the
 * actual estimator through and the column will populate. The header
 * is in place now so the visual contract is stable.
 */
const estimatorDelta = computed<string>(() => {
  return '—'
})

const tokenSpend = computed(() => props.entry.tokenSpend)
const rateLimit = computed(() => props.entry.rateLimit)
const notConfigured = computed(() => props.entry.notConfigured)

/**
 * isUncapped — token_spend with no cap configured (capMinor <= 0).
 * The "Reset spend counter" button is disabled in this state because
 * there is no denominator to anchor a reset to; the figure is purely
 * observational.
 */
const isUncapped = computed(() => {
  const ts = tokenSpend.value
  if (ts === null) return true
  return ts.capMinor <= 0
})

function onBackdropClick(): void {
  if (showResetConfirm.value) return
  emit('close')
}

function onEscape(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    if (showResetConfirm.value) {
      showResetConfirm.value = false
      return
    }
    emit('close')
  }
}

function openResetConfirm(): void {
  if (isUncapped.value || resetting.value) return
  showResetConfirm.value = true
}

function cancelReset(): void {
  showResetConfirm.value = false
}

async function confirmReset(): Promise<void> {
  if (resetting.value) return
  resetting.value = true
  try {
    const ok = await resetProviderQuotaSpend(
      props.entry.provider,
      props.entry.accountHash,
      props.entry.model,
    )
    if (ok) {
      showToast({
        message: `Spend counter reset for ${props.entry.provider} / ${props.entry.model}`,
        variant: 'success',
      })
      emit('reset')
    } else {
      // 404 — nothing to reset. Surface as info-level toast so the
      // user knows the click was acknowledged but had no effect.
      showToast({
        message: 'Nothing to reset — no spend snapshot recorded.',
        variant: 'default',
      })
    }
    showResetConfirm.value = false
    emit('close')
  } catch (err) {
    showToast({
      message: err instanceof Error ? err.message : 'Failed to reset spend counter',
      variant: 'error',
    })
  } finally {
    resetting.value = false
  }
}
</script>

<template>
  <div
    class="quota-panel-backdrop"
    data-testid="provider-quota-panel-backdrop"
    role="dialog"
    aria-modal="true"
    aria-labelledby="quota-panel-title"
    @click.self="onBackdropClick"
    @keydown="onEscape"
    tabindex="-1"
  >
    <div class="quota-panel" data-testid="provider-quota-panel">
      <header class="quota-panel__header">
        <h2 id="quota-panel-title" class="quota-panel__title">
          <span data-testid="provider-quota-panel-provider">{{ entry.provider }}</span>
          <span class="quota-panel__sep">·</span>
          <span data-testid="provider-quota-panel-account">{{
            truncateAccountHash(entry.accountHash)
          }}</span>
          <span class="quota-panel__sep">·</span>
          <span data-testid="provider-quota-panel-model">{{ entry.model }}</span>
        </h2>
        <button
          type="button"
          class="quota-panel__close"
          data-testid="provider-quota-panel-close"
          aria-label="Close panel"
          @click="emit('close')"
        >
          &times;
        </button>
      </header>

      <!-- rate_limit branch -->
      <section
        v-if="entry.variant === 'rate_limit' && rateLimit !== null"
        class="quota-panel__section"
        data-testid="provider-quota-panel-rate-limit"
      >
        <h3>Rate-limit windows</h3>
        <table class="quota-panel__table">
          <thead>
            <tr>
              <th>Window</th>
              <th>Limit</th>
              <th>Remaining</th>
              <th>Resets at</th>
            </tr>
          </thead>
          <tbody>
            <tr data-testid="provider-quota-panel-window-requests">
              <td>Requests</td>
              <td>{{ rateLimit.requests.limit }}</td>
              <td>{{ rateLimit.requests.remaining }}</td>
              <td>{{ formatTimestamp(rateLimit.requests.reset) }}</td>
            </tr>
            <tr data-testid="provider-quota-panel-window-tokens">
              <td>Tokens</td>
              <td>{{ rateLimit.tokens.limit }}</td>
              <td>{{ rateLimit.tokens.remaining }}</td>
              <td>{{ formatTimestamp(rateLimit.tokens.reset) }}</td>
            </tr>
            <tr data-testid="provider-quota-panel-window-input">
              <td>Input</td>
              <td>{{ rateLimit.input.limit }}</td>
              <td>{{ rateLimit.input.remaining }}</td>
              <td>{{ formatTimestamp(rateLimit.input.reset) }}</td>
            </tr>
            <tr data-testid="provider-quota-panel-window-output">
              <td>Output</td>
              <td>{{ rateLimit.output.limit }}</td>
              <td>{{ rateLimit.output.remaining }}</td>
              <td>{{ formatTimestamp(rateLimit.output.reset) }}</td>
            </tr>
          </tbody>
        </table>
        <dl class="quota-panel__summary">
          <dt>Tightest window</dt>
          <dd data-testid="provider-quota-panel-tightest">
            {{ formatPercentRemaining(rateLimit.tightestPercentRemaining) }}
          </dd>
          <dt>Tightest reset</dt>
          <dd data-testid="provider-quota-panel-tightest-reset">
            {{ formatTimestamp(rateLimit.tightestResetAt) }}
          </dd>
        </dl>
      </section>

      <!-- token_spend branch -->
      <section
        v-else-if="entry.variant === 'token_spend' && tokenSpend !== null"
        class="quota-panel__section"
        data-testid="provider-quota-panel-token-spend"
      >
        <h3>Token spend</h3>
        <dl class="quota-panel__summary">
          <dt>Spent (native)</dt>
          <dd data-testid="provider-quota-panel-spent-native">
            {{ formatMoney(tokenSpend.spentMinor, tokenSpend.spentCurrency) }}
          </dd>
          <!-- OD-6 — USD equivalent always visible -->
          <dt>USD equivalent</dt>
          <dd data-testid="provider-quota-panel-spent-usd">
            {{ formatMoney(tokenSpend.spentUsdMinor, 'USD') }}
          </dd>
          <dt>Cap</dt>
          <dd data-testid="provider-quota-panel-cap">
            <template v-if="isUncapped">uncapped</template>
            <template v-else>{{
              formatMoney(
                tokenSpend.capMinor,
                tokenSpend.capCurrency || tokenSpend.spentCurrency,
              )
            }}</template>
          </dd>
          <dt>Period</dt>
          <dd data-testid="provider-quota-panel-period">{{ tokenSpend.period }}</dd>
          <dt>Period start</dt>
          <dd data-testid="provider-quota-panel-period-start">
            {{ formatTimestamp(tokenSpend.periodStart) }}
          </dd>
          <dt>Period end</dt>
          <dd data-testid="provider-quota-panel-period-end">
            {{ formatTimestamp(tokenSpend.periodEnd) }}
          </dd>
          <dt>Threshold amber</dt>
          <dd data-testid="provider-quota-panel-threshold-amber">
            {{ tokenSpend.thresholdAmber }}%
          </dd>
          <dt>Threshold red</dt>
          <dd data-testid="provider-quota-panel-threshold-red">
            {{ tokenSpend.thresholdRed }}%
          </dd>
          <dt>Observed at</dt>
          <dd data-testid="provider-quota-panel-observed-at">
            {{ formatTimestamp(entry.observedAt) }}
          </dd>
          <dt>Pricing source</dt>
          <dd data-testid="provider-quota-panel-pricing-source">
            {{ entry.pricingSource || '—' }}
          </dd>
          <!-- OD-7 — estimator-vs-actual drift column. Always
               present (visual contract stable); populates when the
               event family carries the measured estimator value. -->
          <dt>Estimator drift</dt>
          <dd data-testid="provider-quota-panel-estimator-drift">
            {{ estimatorDelta }}
          </dd>
        </dl>
        <button
          type="button"
          class="quota-panel__reset"
          data-testid="provider-quota-panel-reset"
          :disabled="isUncapped || resetting"
          @click="openResetConfirm"
        >
          Reset spend counter
        </button>
      </section>

      <!-- not_configured branch -->
      <section
        v-else-if="entry.variant === 'not_configured' && notConfigured !== null"
        class="quota-panel__section"
        data-testid="provider-quota-panel-not-configured"
      >
        <h3>No quota signal</h3>
        <p
          class="quota-panel__reason"
          data-testid="provider-quota-panel-reason"
          :title="
            'Some provider classes do not expose a usable quota signal — e.g. local Ollama (no API) or providers that do not surface remaining-budget headers.'
          "
        >
          {{ notConfigured.reason }}
        </p>
      </section>

      <!-- Inline reset-confirm modal -->
      <div
        v-if="showResetConfirm"
        class="quota-panel__confirm"
        data-testid="provider-quota-panel-reset-confirm"
        role="dialog"
        aria-modal="true"
        aria-labelledby="quota-panel-confirm-title"
      >
        <h3 id="quota-panel-confirm-title">Reset spend counter?</h3>
        <p>
          This will zero the spend snapshot for
          <strong>{{ entry.provider }} / {{ entry.model }}</strong> and start
          the counter from zero on the next response. This is a manual
          operator action — the spend will still accumulate normally
          afterwards.
        </p>
        <div class="quota-panel__confirm-buttons">
          <button
            type="button"
            data-testid="provider-quota-panel-reset-cancel"
            @click="cancelReset"
            :disabled="resetting"
          >
            Cancel
          </button>
          <button
            type="button"
            class="quota-panel__reset"
            data-testid="provider-quota-panel-reset-confirm-button"
            @click="confirmReset"
            :disabled="resetting"
          >
            Reset
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.quota-panel-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
}

.quota-panel {
  background: var(--bg-secondary, #1a1b26);
  color: var(--text-primary, #f5f5f5);
  border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
  border-radius: var(--radius, 8px);
  padding: 1.5rem;
  width: min(560px, 90vw);
  max-height: 85vh;
  overflow-y: auto;
  position: relative;
}

.quota-panel__header {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 0.5rem;
  margin-bottom: 1rem;
}

.quota-panel__title {
  margin: 0;
  font-size: 1rem;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-weight: 600;
}

.quota-panel__sep {
  color: var(--text-muted, #b0b0b0);
  margin: 0 0.4rem;
}

.quota-panel__close {
  background: transparent;
  border: none;
  color: var(--text-muted, #b0b0b0);
  font-size: 1.4rem;
  cursor: pointer;
  padding: 0 0.4rem;
  line-height: 1;
}

.quota-panel__close:hover {
  color: var(--text-primary, #f5f5f5);
}

.quota-panel__section h3 {
  margin: 0 0 0.75rem;
  font-size: 0.85rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-secondary, #d0d0d0);
}

.quota-panel__table {
  width: 100%;
  border-collapse: collapse;
  margin-bottom: 1rem;
  font-size: 0.85rem;
}

.quota-panel__table th,
.quota-panel__table td {
  text-align: left;
  padding: 0.4rem 0.5rem;
  border-bottom: 1px solid var(--border, rgba(255, 255, 255, 0.08));
}

.quota-panel__table th {
  font-weight: 500;
  color: var(--text-secondary, #d0d0d0);
}

.quota-panel__summary {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.4rem 1rem;
  margin: 0 0 1rem;
  font-size: 0.85rem;
}

.quota-panel__summary dt {
  color: var(--text-secondary, #d0d0d0);
}

.quota-panel__summary dd {
  margin: 0;
  font-family: var(--font-mono, ui-monospace, monospace);
}

.quota-panel__reset {
  display: inline-block;
  padding: 0.4rem 0.9rem;
  border: 1px solid var(--error, #dc2626);
  background: color-mix(in srgb, var(--error, #dc2626) 12%, transparent);
  color: var(--error, #dc2626);
  font-size: 0.85rem;
  border-radius: var(--radius, 6px);
  cursor: pointer;
}

.quota-panel__reset:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.quota-panel__reason {
  margin: 0;
  font-size: 0.9rem;
  color: var(--text-primary, #f5f5f5);
  cursor: help;
}

.quota-panel__confirm {
  margin-top: 1rem;
  padding: 1rem;
  border: 1px solid var(--warning, #d97706);
  border-radius: var(--radius, 6px);
  background: color-mix(in srgb, var(--warning, #d97706) 8%, transparent);
}

.quota-panel__confirm h3 {
  margin: 0 0 0.5rem;
  font-size: 0.9rem;
  text-transform: none;
  letter-spacing: 0;
  color: var(--text-primary, #f5f5f5);
}

.quota-panel__confirm p {
  margin: 0 0 0.75rem;
  font-size: 0.85rem;
  color: var(--text-primary, #f5f5f5);
}

.quota-panel__confirm-buttons {
  display: flex;
  gap: 0.5rem;
  justify-content: flex-end;
}

.quota-panel__confirm-buttons button {
  padding: 0.35rem 0.8rem;
  border-radius: var(--radius, 6px);
  font-size: 0.85rem;
  cursor: pointer;
  background: var(--bg-secondary, #1a1b26);
  color: var(--text-primary, #f5f5f5);
  border: 1px solid var(--border, rgba(255, 255, 255, 0.12));
}
</style>
