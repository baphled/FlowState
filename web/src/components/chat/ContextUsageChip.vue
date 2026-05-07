<script setup lang="ts">
import { computed } from 'vue'
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
 * Mounted between the provider-label and the ModelPicker in the
 * ChatView toolbar so the user sees how close the request is to
 * saturating the model's window without scrolling away from their
 * message.
 *
 * Visual contract:
 *   - Only renders when currentContextUsage is non-null AND limit > 0
 *     (a zero limit would render a meaningless `1234/0`).
 *   - Numeric formatter rounds to thousands: `12345 → 12K`. Keeps the
 *     chip compact in a toolbar with finite real estate.
 *   - Threshold colours mirror the CriticalErrorBanner palette so a
 *     user already conditioned to recognise the red severity in the
 *     banner sees the same severity escalating in the chip:
 *       <75%   → neutral (default toolbar text colour)
 *       >=75%  → warning (rgb(217, 119, 6) — amber severity)
 *       >=90%  → danger  (rgb(220, 38, 38) — red severity, same as
 *                CriticalErrorBanner.vue)
 *   - role="status" + aria-live="polite" so screen readers announce
 *     the figure on update without interrupting the user's flow (the
 *     critical banner uses assertive; the chip is informational).
 */
defineOptions({ name: 'ContextUsageChip' })

const chatStore = useChatStore()

const usage = computed(() => chatStore.currentContextUsage)

const isVisible = computed(() => {
  const u = usage.value
  return u !== null && u.limit > 0
})

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

const inputLabel = computed(() => formatTokens(usage.value?.inputTokens ?? 0))
const limitLabel = computed(() => formatTokens(usage.value?.limit ?? 0))
const percentageLabel = computed(() => `${usage.value?.percentage ?? 0}%`)

/**
 * Severity classification — matches the CriticalErrorBanner palette
 * thresholds so the visual escalation is consistent across the chat
 * surface. The threshold figures are tuned against the historical
 * saturation profile: most healthy turns sit below 50%; sustained
 * 75%+ usage indicates the conversation is approaching the gate;
 * 90%+ is "compact or fail next turn" territory.
 */
const severity = computed<'neutral' | 'warning' | 'danger'>(() => {
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
</script>

<template>
  <div
    v-if="isVisible"
    :class="chipClass"
    role="status"
    aria-live="polite"
    data-testid="context-usage-chip"
    :data-severity="severity"
  >
    <span class="context-usage-chip__counts" data-testid="context-usage-counts">
      {{ inputLabel }}/{{ limitLabel }}
    </span>
    <span class="context-usage-chip__percentage" data-testid="context-usage-percentage">
      {{ percentageLabel }}
    </span>
  </div>
</template>

<style scoped>
.context-usage-chip {
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

.context-usage-chip__counts {
  color: var(--text-primary, #f5f5f5);
}

.context-usage-chip__percentage {
  font-size: 0.72rem;
  opacity: 0.85;
}

/*
 * Severity palettes match CriticalErrorBanner.vue's red so the visual
 * escalation across the chat surface is consistent. The 75% warning
 * is amber (a colour the banner does not use, reserved for the
 * "approaching" state distinct from the "danger" red).
 */
.context-usage-chip--warning {
  background: rgba(217, 119, 6, 0.15);
  border-color: rgba(217, 119, 6, 0.5);
  color: rgb(217, 119, 6);
}

.context-usage-chip--warning .context-usage-chip__counts {
  color: rgb(217, 119, 6);
}

.context-usage-chip--danger {
  background: rgba(220, 38, 38, 0.18);
  border-color: rgba(220, 38, 38, 0.55);
  color: rgb(220, 38, 38);
}

.context-usage-chip--danger .context-usage-chip__counts {
  color: rgb(220, 38, 38);
  font-weight: 600;
}
</style>
