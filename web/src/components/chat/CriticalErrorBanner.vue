<script setup lang="ts">
import { computed, ref } from 'vue'
import { useChatStore } from '@/stores/chatStore'

/**
 * CriticalErrorBanner — persistent, accessible affordance for the
 * `stream_critical` SSE event class.
 *
 * The Go SSE pipeline classifies fatal provider errors (revoked OAuth,
 * 401, model-not-found, billing/quota lockout) as SeverityCritical and
 * emits them as `{"error":"critical stream error","correlation_id":"…"}`.
 * The chat store branches on this in applyContentEvent and populates
 * `criticalError` (see chatStore.ts state declaration). This banner
 * binds to that state.
 *
 * Visual contract (distinct from the existing transient-error toast at
 * the bottom-right of the viewport):
 *   - Anchored at the top of the chat surface so the user sees it
 *     before scrolling. ChatView mounts it above the message-pane.
 *   - Red severity palette (matches the toast's --error variant for
 *     consistency, distinct background fill).
 *   - role="alert" so screen readers announce arrival immediately.
 *   - Persists across user interactions until either a Dismiss click
 *     or a session change (the store handles the session-change clear).
 *
 * Affordances:
 *   - "Show details" toggles a disclosure that reveals the
 *     correlation_id so users can paste it for support.
 *   - "Dismiss" calls `chatStore.dismissCriticalError()`. A subsequent
 *     critical event will repopulate the banner (the dispatch
 *     overwrites unconditionally — see the dispatch comment in
 *     chatStore.ts).
 */
defineOptions({ name: 'CriticalErrorBanner' })

const chatStore = useChatStore()
const detailsOpen = ref(false)

const isVisible = computed(() => chatStore.criticalError !== null)
const message = computed(() => chatStore.criticalError?.message ?? '')
const correlationId = computed(() => chatStore.criticalError?.correlationId ?? '')

function toggleDetails(): void {
  detailsOpen.value = !detailsOpen.value
}

function dismiss(): void {
  detailsOpen.value = false
  chatStore.dismissCriticalError()
}
</script>

<template>
  <div
    v-if="isVisible"
    class="critical-error-banner"
    role="alert"
    aria-live="assertive"
    data-testid="critical-error-banner"
  >
    <span class="critical-error-icon" aria-hidden="true">!</span>
    <div class="critical-error-content">
      <span class="critical-error-title">Critical stream error</span>
      <span class="critical-error-message" data-testid="critical-error-message">
        {{ message }}
      </span>
      <button
        v-if="correlationId"
        type="button"
        class="critical-error-details-toggle"
        data-testid="critical-error-details-toggle"
        :aria-expanded="detailsOpen"
        @click="toggleDetails"
      >
        {{ detailsOpen ? 'Hide details' : 'Show details' }}
      </button>
      <p
        v-if="detailsOpen && correlationId"
        class="critical-error-details"
        data-testid="critical-error-details"
      >
        Reference this id when contacting support:
        <code class="critical-error-correlation-id" data-testid="critical-error-correlation-id">{{ correlationId }}</code>
      </p>
    </div>
    <button
      type="button"
      class="critical-error-dismiss"
      data-testid="critical-error-dismiss"
      aria-label="Dismiss critical error"
      @click="dismiss"
    >
      &times;
    </button>
  </div>
</template>

<style scoped>
.critical-error-banner {
  display: flex;
  align-items: flex-start;
  gap: 0.75rem;
  padding: 0.85rem 1rem;
  margin: 0.5rem 1rem 0;
  background: rgba(220, 38, 38, 0.18);
  border: 1px solid rgba(220, 38, 38, 0.55);
  border-left-width: 4px;
  border-radius: var(--radius, 6px);
  color: var(--text-primary, #f5f5f5);
  font-size: 0.9rem;
  box-shadow: 0 2px 8px rgba(220, 38, 38, 0.15);
  flex-shrink: 0;
}

.critical-error-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.5rem;
  height: 1.5rem;
  flex-shrink: 0;
  border-radius: 50%;
  background: rgba(220, 38, 38, 0.85);
  color: #fff;
  font-weight: 700;
  font-size: 1rem;
  line-height: 1;
}

.critical-error-content {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  flex: 1;
  min-width: 0;
}

.critical-error-title {
  font-weight: 600;
  color: rgba(248, 113, 113, 1);
  font-size: 0.95rem;
}

.critical-error-message {
  color: var(--text-primary, #f5f5f5);
  word-wrap: break-word;
}

.critical-error-details-toggle {
  align-self: flex-start;
  background: transparent;
  border: none;
  padding: 0;
  color: rgba(248, 113, 113, 1);
  font-size: 0.8rem;
  font-weight: 500;
  cursor: pointer;
  text-decoration: underline;
}

.critical-error-details-toggle:hover {
  color: rgba(254, 178, 178, 1);
}

.critical-error-details {
  margin: 0.25rem 0 0;
  color: var(--text-muted, #b0b0b0);
  font-size: 0.8rem;
}

.critical-error-correlation-id {
  display: inline-block;
  padding: 0.1rem 0.4rem;
  margin-left: 0.25rem;
  background: rgba(0, 0, 0, 0.3);
  border-radius: 3px;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.75rem;
  color: var(--text-primary, #f5f5f5);
  user-select: all;
}

.critical-error-dismiss {
  background: transparent;
  border: none;
  color: rgba(248, 113, 113, 0.85);
  font-size: 1.4rem;
  line-height: 1;
  cursor: pointer;
  padding: 0;
  flex-shrink: 0;
  align-self: flex-start;
}

.critical-error-dismiss:hover {
  color: rgba(254, 178, 178, 1);
}
</style>
