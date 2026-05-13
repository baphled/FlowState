<script setup lang="ts">
import { computed } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import { useQuotaStore, type ProviderQuotaSnapshot } from '@/stores/quotaStore'

/**
 * QuotaChip — toolbar affordance for the engine's `provider_quota`
 * SSE event class (Provider Quota and Spend Visibility plan, May
 * 2026, PR4a).
 *
 * Lives next to ContextUsageChip in the ChatView header. The two
 * chips render different facets of the same model+provider session:
 * ContextUsage shows "how full is the input window"; QuotaChip shows
 * either "how close to a rate-limit reset" (RateLimit variant) or
 * "how much have we spent" (TokenSpend variant) or "—" with a
 * tooltip explaining why (NotConfigured variant).
 *
 * Discriminator contract: every snapshot has `variant === 'rate_limit'
 * | 'token_spend' | 'not_configured'` and the matching nested object
 * populated. The template's v-if ladder discriminates on `variant`.
 *
 * Threshold colour palette (OD-9 — plan lines 517-520):
 *   - RateLimit: green >= 20% remaining, amber 5-20%, red < 5%
 *   - TokenSpend (capped): green < 80% used, amber 80-95%, red >= 95%
 *   - TokenSpend (uncapped): always green (no denominator)
 *   - NotConfigured: neutral
 */
defineOptions({ name: 'QuotaChip' })

const chatStore = useChatStore()
const quotaStore = useQuotaStore()

/**
 * Active snapshot — falls back to the any-account match when the
 * (provider, accountHash, model) tuple has no entry. v1
 * single-account-per-provider deployments rely on the any-account
 * path because the engine stamps a non-empty AccountHash that the
 * chip does not currently propagate from configuration.
 */
const snapshot = computed<ProviderQuotaSnapshot | null>(() => {
  const provider = chatStore.currentProviderId ?? ''
  const model = chatStore.currentModelId ?? ''
  if (provider === '' || model === '') {
    return null
  }
  return quotaStore.anyQuotaFor(provider, model)
})

/**
 * isVisible — hide the chip until the model is known AND a snapshot
 * has landed. The empty bootstrap edge (no model yet OR no SSE
 * event seen) renders nothing — distinct from ContextUsageChip's
 * always-visible empty state because the spend / rate-limit signal
 * is genuinely unknown until the first server tick, and a "—"
 * placeholder for both chips would be visually noisy in the
 * toolbar.
 */
const isVisible = computed(() => snapshot.value !== null)

/**
 * Severity classification per OD-9. Always 'neutral' for
 * NotConfigured; computed from tightest_percent_remaining for
 * RateLimit; computed from Spent/Cap for TokenSpend (capped path);
 * always 'neutral' (green) for TokenSpend uncapped.
 */
const severity = computed<'neutral' | 'warning' | 'danger'>(() => {
  const s = snapshot.value
  if (s === null) return 'neutral'
  if (s.variant === 'not_configured') return 'neutral'
  if (s.variant === 'rate_limit' && s.rateLimit !== null) {
    const pct = s.rateLimit.tightestPercentRemaining
    if (pct < 0) return 'neutral' // -1 sentinel — no signal
    if (pct < 5) return 'danger'
    if (pct < 20) return 'warning'
    return 'neutral'
  }
  if (s.variant === 'token_spend' && s.tokenSpend !== null) {
    const ts = s.tokenSpend
    if (ts.capMinor <= 0) return 'neutral' // uncapped — always green
    if (ts.thresholdAmber < 0 || ts.thresholdRed < 0) return 'neutral'
    const pct = (ts.spentMinor / ts.capMinor) * 100
    if (pct >= ts.thresholdRed) return 'danger'
    if (pct >= ts.thresholdAmber) return 'warning'
    return 'neutral'
  }
  return 'neutral'
})

const chipClass = computed(() => `quota-chip quota-chip--${severity.value}`)

/**
 * Format an ISO-8601 timestamp as a "resets in Nm" / "in Nh" /
 * "in Ns" string for the RateLimit chip. Returns '' when the input
 * is empty / unparseable / in the past (the chip will then omit the
 * "· resets" suffix).
 */
function formatReset(iso: string): string {
  if (!iso) return ''
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ''
  const diffMs = t - Date.now()
  if (diffMs <= 0) return ''
  const totalSeconds = Math.round(diffMs / 1000)
  if (totalSeconds < 60) return `${totalSeconds}s`
  const totalMinutes = Math.round(totalSeconds / 60)
  if (totalMinutes < 60) return `${totalMinutes}m`
  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  if (minutes === 0) return `${hours}h`
  return `${hours}h${minutes.toString().padStart(2, '0')}`
}

/**
 * Money formatter for the TokenSpend variant. Renders `$12.34` for
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

const rateLimitLabel = computed(() => {
  const s = snapshot.value
  if (s === null || s.variant !== 'rate_limit' || s.rateLimit === null) return ''
  const pct = s.rateLimit.tightestPercentRemaining
  const pctLabel = pct < 0 ? '—' : `${pct}%`
  const reset = formatReset(s.rateLimit.tightestResetAt)
  if (reset === '') return `${pctLabel} remaining`
  return `${pctLabel} remaining · resets ${reset}`
})

const tokenSpendLabel = computed(() => {
  const s = snapshot.value
  if (s === null || s.variant !== 'token_spend' || s.tokenSpend === null) return ''
  const ts = s.tokenSpend
  const spent = formatMoney(ts.spentMinor, ts.spentCurrency)
  if (ts.capMinor <= 0) return spent
  const cap = formatMoney(ts.capMinor, ts.capCurrency || ts.spentCurrency)
  return `${spent} / ${cap}`
})

/**
 * TokenSpend bar fill (0-100). Returns 0 for uncapped so the bar
 * stays empty; 100 caps the visual at the right edge for
 * over-cap spend.
 */
const tokenSpendBarPct = computed(() => {
  const s = snapshot.value
  if (s === null || s.variant !== 'token_spend' || s.tokenSpend === null) return 0
  const ts = s.tokenSpend
  if (ts.capMinor <= 0) return 0
  const pct = (ts.spentMinor / ts.capMinor) * 100
  if (pct < 0) return 0
  if (pct > 100) return 100
  return pct
})

const notConfiguredReason = computed(() => {
  const s = snapshot.value
  if (s === null || s.variant !== 'not_configured' || s.notConfigured === null) return ''
  return s.notConfigured.reason
})

/**
 * Tooltip text. Variant-specific copy explaining the figure + the
 * store-backend disclosure (B3 — single-instance scope when
 * StoreBackend=memory).
 */
const tooltipTitle = computed(() => {
  const s = snapshot.value
  if (s === null) return ''
  const parts: string[] = []
  if (s.variant === 'token_spend' && s.tokenSpend !== null) {
    const usd = formatMoney(s.tokenSpend.spentUsdMinor, 'USD')
    if (s.tokenSpend.spentCurrency !== 'USD' && s.tokenSpend.spentUsdMinor > 0) {
      parts.push(`USD equivalent: ${usd}`)
    }
    if (s.pricingSource) {
      parts.push(`Pricing source: ${s.pricingSource}`)
    }
  }
  if (s.variant === 'not_configured' && s.notConfigured !== null) {
    parts.push(`No quota signal: ${s.notConfigured.reason}`)
  }
  if (s.storeBackend === 'memory') {
    parts.push(
      'Single-instance scope. If you run multiple FlowState instances against this provider key, this figure is FlowState-observed only and not the full account spend.',
    )
  } else if (s.storeBackend === 'redis' || s.storeBackend === 'postgres') {
    parts.push('Shared across all FlowState instances using this provider key.')
  }
  if (s.stale) {
    parts.push('Stale: the underlying window has reset; awaiting next response.')
  }
  return parts.join(' · ')
})

/**
 * Click emit — PR4a wires the click to a parent-handled event;
 * PR4b will add the ProviderQuotaPanel modal. For PR4a the parent
 * gets the event but no panel renders yet (out of PR4 scope).
 */
const emit = defineEmits<{
  open: [snapshot: ProviderQuotaSnapshot]
}>()

function handleClick(): void {
  const s = snapshot.value
  if (s === null) return
  // Only TokenSpend opens the panel — RateLimit and NotConfigured
  // have nothing additional to drill into in PR4a.
  if (s.variant === 'token_spend') {
    emit('open', s)
  }
}
</script>

<template>
  <div
    v-if="isVisible && snapshot !== null"
    :class="chipClass"
    role="status"
    aria-live="polite"
    data-testid="provider-quota-chip"
    :data-severity="severity"
    :data-variant="snapshot.variant"
    :title="tooltipTitle"
    @click="handleClick"
  >
    <template v-if="snapshot.variant === 'rate_limit'">
      <span class="quota-chip__label" data-testid="provider-quota-rate-limit-label">
        {{ rateLimitLabel }}
      </span>
    </template>

    <template v-else-if="snapshot.variant === 'token_spend'">
      <span class="quota-chip__label" data-testid="provider-quota-token-spend-label">
        {{ tokenSpendLabel }}
      </span>
      <span
        v-if="tokenSpendBarPct > 0"
        class="quota-chip__bar"
        aria-hidden="true"
        data-testid="provider-quota-token-spend-bar"
      >
        <span
          class="quota-chip__bar-fill"
          :style="{ width: `${tokenSpendBarPct}%` }"
        />
      </span>
    </template>

    <template v-else-if="snapshot.variant === 'not_configured'">
      <span
        class="quota-chip__label quota-chip__label--placeholder"
        data-testid="provider-quota-not-configured-label"
        :data-reason="notConfiguredReason"
      >
        —
      </span>
    </template>
  </div>
</template>

<style scoped>
.quota-chip {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.15rem 0.5rem;
  border-radius: var(--radius, 6px);
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.08);
  font-size: 0.78rem;
  font-family: var(--font-mono, ui-monospace, monospace);
  color: var(--text-muted, #b0b0b0);
  flex-shrink: 0;
  cursor: default;
}

.quota-chip[data-variant='token_spend'] {
  /* TokenSpend is the only variant the click-handler emits 'open' for; the
   * cursor disambiguates affordance from the RateLimit / NotConfigured
   * branches which are read-only. PR4b adds the actual panel. */
  cursor: pointer;
}

.quota-chip__label {
  color: var(--text-primary, #f5f5f5);
}

.quota-chip__label--placeholder {
  color: var(--text-muted, #b0b0b0);
  font-weight: 600;
}

.quota-chip__bar {
  position: relative;
  width: 38px;
  height: 4px;
  border-radius: 2px;
  background: rgba(255, 255, 255, 0.1);
  overflow: hidden;
}

.quota-chip__bar-fill {
  position: absolute;
  inset: 0;
  width: var(--width, 0%);
  background: currentColor;
  transition: width 0.2s ease;
}

/* Severity palettes — mirror ContextUsageChip's color-mix idiom so the
 * dark→light→tokyo-night theme swap repaints both chips in lockstep. */
.quota-chip--warning {
  background: color-mix(in srgb, var(--warning) 15%, transparent);
  border-color: color-mix(in srgb, var(--warning) 50%, transparent);
  color: var(--warning);
}

.quota-chip--warning .quota-chip__label {
  color: var(--warning);
}

.quota-chip--danger {
  background: color-mix(in srgb, var(--error) 18%, transparent);
  border-color: color-mix(in srgb, var(--error) 55%, transparent);
  color: var(--error);
}

.quota-chip--danger .quota-chip__label {
  color: var(--error);
  font-weight: 600;
}
</style>
