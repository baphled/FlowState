<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useChatStore } from '@/stores/chatStore'

/**
 * ContextUsageChip — toolbar affordance for the engine's
 * `context_usage` SSE event class.
 *
 * Phase 2 of the May 2026 context-window saturation fix (companion to
 * the proactive overflow gate that closes the glm-4.6 "thought into
 * the void" failure mode). The Go SSE pipeline emits a
 * `context_usage` chunk as the first artefact of every Stream that has
 * enough information to compute it; the chat store branches on this
 * in applyContentEvent and populates `currentContextUsage`. This chip
 * binds to that state.
 *
 * Phase 3 (May 2026 follow-up) — TUI-cadence parity. The chip is now
 * permanently visible whenever a model is selected, mirroring the
 * TUI's StatusBar (internal/tui/intents/chat/intent.go syncStatusBar)
 * which reads engine.LastContextResult on every redraw and reflects
 * current state at all times. Pre-Phase-3 the chip stayed hidden
 * until the first context_usage event landed; the user reopening a
 * session saw a blank toolbar until they started typing. The empty
 * state is a placeholder figure (`—/—`) with the neutral palette so
 * the affordance is present without misleading the user with
 * synthetic numeric figures.
 *
 * Mounted between the provider-label and the ModelPicker in the
 * ChatView toolbar so the user sees how close the request is to
 * saturating the model's window without scrolling away from their
 * message.
 *
 * Visual contract:
 *   - Renders whenever a model is selected (currentModelId !== '').
 *     Hides only on the rare bootstrap edge where no model is yet
 *     known.
 *   - Empty-state placeholder fires when currentContextUsage is null
 *     OR carries a degraded limit=0 payload — the chip shows `—/—`
 *     and `—%` with the neutral palette. Server-side emitters (SSE-
 *     on-load, post-turn, agent/model PATCH) hydrate the figure
 *     within milliseconds in production so the empty state is
 *     barely-perceptible.
 *   - Numeric formatter rounds to thousands: `12345 → 12K`. Keeps the
 *     chip compact in a toolbar with finite real estate.
 *   - Threshold colours mirror the CriticalErrorBanner palette so a
 *     user already conditioned to recognise the red severity in the
 *     banner sees the same severity escalating in the chip:
 *       <75%   → neutral (default toolbar text colour)
 *       >=75%  → warning (var(--warning) — amber severity per theme)
 *       >=90%  → danger  (var(--error) — red severity per theme,
 *                same theme variable as CriticalErrorBanner.vue)
 *   - role="status" + aria-live="polite" so screen readers announce
 *     the figure on update without interrupting the user's flow (the
 *     critical banner uses assertive; the chip is informational).
 */
defineOptions({ name: 'ContextUsageChip' })

const chatStore = useChatStore()

const usage = computed(() => chatStore.currentContextUsage)

/**
 * Visibility predicate — the chip renders whenever a model is
 * selected. Phase 3 removed the `currentContextUsage !== null` gate
 * because the TUI parity goal is "always visible, reflects current
 * state". Without a model the chip has no provider/limit reference
 * to display so we keep it hidden — the no-model bootstrap edge.
 */
const isVisible = computed(() => chatStore.currentModelId !== '')

/**
 * The chip's empty-state predicate. True when we have no usage
 * payload OR the payload's limit is zero (degraded). The figure
 * formatters branch on this so the placeholder copy is centralised
 * here.
 */
const isEmptyState = computed(() => {
  const u = usage.value
  return u === null || u.limit <= 0
})

const EMPTY_STATE_COUNT = '—'
const EMPTY_STATE_PERCENTAGE = '—'

/**
 * Formats a token count to the chip's compact representation. Values
 * below 1000 show verbatim; values above round to the nearest
 * thousand and append 'K'. The threshold matches the modal mental
 * model where users describe their context budget as "100K" or "1M",
 * not "100,000".
 */
function formatTokens(n: number): string {
  if (n < 1000) {
    return String(n)
  }
  return `${Math.round(n / 1000)}K`
}

const inputLabel = computed(() =>
  isEmptyState.value ? EMPTY_STATE_COUNT : formatTokens(usage.value?.inputTokens ?? 0),
)
const limitLabel = computed(() =>
  isEmptyState.value ? EMPTY_STATE_COUNT : formatTokens(usage.value?.limit ?? 0),
)
const percentageLabel = computed(() =>
  isEmptyState.value ? `${EMPTY_STATE_PERCENTAGE}%` : `${usage.value?.percentage ?? 0}%`,
)

/**
 * Severity classification — matches the CriticalErrorBanner palette
 * thresholds so the visual escalation is consistent across the chat
 * surface. The threshold figures are tuned against the historical
 * saturation profile: most healthy turns sit below 50%; sustained
 * 75%+ usage indicates the conversation is approaching the gate;
 * 90%+ is "compact or fail next turn" territory.
 *
 * Empty-state always renders neutral so the toolbar reads as quiet
 * until real data lands.
 */
const severity = computed<'neutral' | 'warning' | 'danger'>(() => {
  if (isEmptyState.value) {
    return 'neutral'
  }
  const pct = usage.value?.percentage ?? 0
  if (pct >= 90) {
    return 'danger'
  }
  if (pct >= 75) {
    return 'warning'
  }
  return 'neutral'
})

const chipClass = computed(() => `context-usage-chip context-usage-chip--${severity.value}`)

/**
 * Slice 6b — auto-compaction flash + tooltip.
 *
 * The chat store dispatches a `context_compacted` SSE event into
 * `compactionEventCount` and `lastCompaction`. The chip observes the
 * counter and runs a 2-second flash on each new event so the user sees
 * the compactor fire (without it the auto-compactor's behaviour is
 * silent — operators have no feedback that the engine just bought
 * back budget). The tooltip surfaces the saved-tokens delta whenever
 * a compaction has fired this session so an operator can see the
 * compactor's aggressiveness on hover without leaving the chat.
 *
 * Why a watcher on `compactionEventCount` rather than a watcher on
 * `lastCompaction`: the count uniquely identifies each event so a
 * back-to-back compaction (rare, but possible if the gate-proximity
 * trigger fires twice on consecutive turns) re-flashes the chip even
 * if the saved-tokens figure happens to match. Watching the payload
 * object would conflate two distinct events with the same delta.
 */
const FLASH_MS = 2000
const compactionCount = computed(() => chatStore.compactionEventCount)
const lastCompaction = computed(() => chatStore.lastCompaction)
const flashing = ref(false)
let flashTimer: ReturnType<typeof setTimeout> | null = null

watch(compactionCount, (next, prev) => {
  // Only flash on positive increments. Resets to 0 on session change
  // (the store's loadSessionMessages clears the counter alongside
  // lastCompaction) must NOT trigger a flash on the new session's
  // empty chip.
  if (next <= (prev ?? 0)) {
    return
  }
  if (flashTimer !== null) {
    clearTimeout(flashTimer)
  }
  flashing.value = true
  flashTimer = setTimeout(() => {
    flashing.value = false
    flashTimer = null
  }, FLASH_MS)
})

onBeforeUnmount(() => {
  if (flashTimer !== null) {
    clearTimeout(flashTimer)
    flashTimer = null
  }
})

/**
 * Format a token count for the tooltip. Reuses the chip's compact
 * `K` formatter so the tooltip and the primary figure speak the same
 * language (50000 → 50K, not "50,000" or "50000").
 */
function formatTooltipTokens(n: number): string {
  if (n < 1000) {
    return String(n)
  }
  return `${Math.round(n / 1000)}K`
}

/**
 * Phase-5 Slice δ — human-readable copy for each compaction trigger.
 *
 * The closed-vocabulary discriminant from the SSE wire maps onto
 * tooltip copy that attributes the cause: each phrase reads as a
 * complete sentence fragment when concatenated with the saved-tokens
 * figure. Empty / unrecognised triggers return '' so the tooltip
 * falls back to the generic "Last compaction saved Ns tokens" line
 * without misattribution.
 */
function triggerPhrase(trigger: string): string {
  switch (trigger) {
    case 'ratio':
      return 'compacted on threshold'
    case 'gate_proximity':
      return 'compacted near limit'
    case 'model_switch':
      return 'compacted on model switch'
    case 'tool_result_wave':
      return 'compacted after tool result'
    default:
      return ''
  }
}

const tooltipTitle = computed(() => {
  const lc = lastCompaction.value
  if (lc === null || compactionCount.value === 0) {
    return ''
  }
  const saved = formatTooltipTokens(lc.tokensSaved)
  const before = formatTooltipTokens(lc.originalTokens)
  const after = formatTooltipTokens(lc.summaryTokens)
  const phrase = triggerPhrase(lc.trigger)
  if (phrase !== '') {
    return `Last compaction saved ${saved} tokens (${before} → ${after}) — ${phrase}`
  }
  return `Last compaction saved ${saved} tokens (${before} → ${after})`
})
</script>

<template>
  <div
    v-if="isVisible"
    :class="chipClass"
    role="status"
    aria-live="polite"
    data-testid="context-usage-chip"
    :data-severity="severity"
    :title="tooltipTitle"
  >
    <span class="context-usage-chip__counts" data-testid="context-usage-counts">
      {{ inputLabel }}/{{ limitLabel }}
    </span>
    <span class="context-usage-chip__percentage" data-testid="context-usage-percentage">
      {{ percentageLabel }}
    </span>
    <!--
      Slice 6b — auto-compaction flash overlay. Renders as an absolute
      sibling spanning the chip; CSS keyframes drive a brief opacity
      pulse so the existing severity palette stays visible underneath
      (the underlying figure is what the operator should be reading;
      the flash is a transient acknowledgement, not a replacement).
    -->
    <span
      v-if="flashing"
      class="context-usage-chip__flash"
      data-component="context-compacted-flash"
      aria-hidden="true"
    />
  </div>
</template>

<style scoped>
.context-usage-chip {
  position: relative;
  display: inline-flex;
  align-items: baseline;
  gap: 0.35rem;
  padding: 0.15rem 0.5rem;
  border-radius: var(--radius, 6px);
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.08);
  font-size: 0.78rem;
  font-family: var(--font-mono, ui-monospace, monospace);
  color: var(--text-muted, #b0b0b0);
  flex-shrink: 0;
}

/*
 * Slice 6b — auto-compaction flash overlay. Sits absolute on top of
 * the chip and pulses opacity for 2 seconds. The colour is the same
 * neutral palette as the chip's idle border so the flash reads as an
 * acknowledgement rather than a severity escalation; the existing
 * severity classes (warning / danger) keep painting the chip frame
 * underneath so the live usage signal is not masked.
 *
 * pointer-events: none keeps the overlay from swallowing hover
 * events so the chip's `title` tooltip still fires under the user's
 * cursor.
 */
.context-usage-chip__flash {
  position: absolute;
  inset: 0;
  border-radius: inherit;
  pointer-events: none;
  background: rgba(255, 255, 255, 0.18);
  border: 1px solid rgba(255, 255, 255, 0.35);
  animation: context-usage-chip-flash 2s ease-out forwards;
}

@keyframes context-usage-chip-flash {
  0% {
    opacity: 0;
  }
  15% {
    opacity: 1;
  }
  100% {
    opacity: 0;
  }
}

.context-usage-chip__counts {
  color: var(--text-primary, #f5f5f5);
}

.context-usage-chip__percentage {
  font-size: 0.72rem;
  opacity: 0.85;
}

/*
 * Severity palettes ride on the theme variables defined in themes.css
 * (--warning, --error). Each theme owns its own concrete colour so a
 * dark→light→tokyo-night switch repaints the chip without touching
 * this stylesheet. Background and border use a partially-opaque hue
 * via `color-mix(in srgb, …)` so the same single variable drives the
 * full chip palette — see CriticalErrorBanner.vue for the same idiom.
 *
 * N8 (Vue UI Parity vs OpenCode, May 2026) — previously the chip used
 * a hardcoded rgb() literal for warning (amber) and danger (red)
 * inline, which prevented the colours from re-skinning under a
 * `data-theme` swap on <html>.
 */
.context-usage-chip--warning {
  background: color-mix(in srgb, var(--warning) 15%, transparent);
  border-color: color-mix(in srgb, var(--warning) 50%, transparent);
  color: var(--warning);
}

.context-usage-chip--warning .context-usage-chip__counts {
  color: var(--warning);
}

.context-usage-chip--danger {
  background: color-mix(in srgb, var(--error) 18%, transparent);
  border-color: color-mix(in srgb, var(--error) 55%, transparent);
  color: var(--error);
}

.context-usage-chip--danger .context-usage-chip__counts {
  color: var(--error);
  font-weight: 600;
}
</style>
