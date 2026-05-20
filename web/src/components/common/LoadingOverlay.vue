<script setup lang="ts">
// LoadingOverlay — full-viewport opaque cover shown while the application
// is bootstrapping. Mounted by App.vue at the top of the component tree
// so it sits above the (not-yet-rendered) RouterView and blocks any
// glimpse of half-built page content.
//
// Visual treatment: matches the dark-theme app background (--bg-primary)
// with a pulsing FlowState wordmark and a subtle progress indicator,
// chosen to (a) feel like a brand-aware splash rather than a generic
// spinner, (b) reuse existing theme tokens so it adapts automatically
// when the user has switched themes, and (c) avoid loading any external
// font / asset that would itself FOUC.
//
// The HTML splash in index.html paints the same layout (same id-less
// markup, same background colour, same wordmark glyph) so the handover
// from server-rendered HTML to Vue-rendered overlay is seamless. App.vue
// removes the HTML splash on mount before this component takes over.

defineProps<{
  // Optional sub-line ("Connecting…", "Hydrating sessions…") shown under
  // the wordmark. Kept opt-in because the bootstrap path is short enough
  // (single health-check + one restoreStateFromBackend) that a static
  // splash is honest. Surfaced as a prop for future progress narration.
  message?: string;
}>();
</script>

<template>
  <div
    class="loading-overlay"
    data-testid="app-loading-overlay"
    role="status"
    aria-live="polite"
    aria-label="Loading FlowState"
  >
    <div class="loading-content">
      <div class="loading-wordmark" aria-hidden="true">FlowState</div>
      <div class="loading-spinner" aria-hidden="true">
        <span class="loading-dot loading-dot--1"></span>
        <span class="loading-dot loading-dot--2"></span>
        <span class="loading-dot loading-dot--3"></span>
      </div>
      <div v-if="message" class="loading-message">{{ message }}</div>
    </div>
  </div>
</template>

<style scoped>
.loading-overlay {
  position: fixed;
  inset: 0;
  z-index: 9999;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-primary);
  color: var(--text-primary);
  font-family: var(--font-sans);
}

.loading-content {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 1rem;
}

.loading-wordmark {
  font-size: 1.75rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  color: var(--accent);
  animation: loading-pulse 1.6s ease-in-out infinite;
}

.loading-spinner {
  display: flex;
  gap: 0.4rem;
}

.loading-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--accent);
  opacity: 0.4;
  animation: loading-bounce 1.2s ease-in-out infinite;
}

.loading-dot--1 {
  animation-delay: 0s;
}
.loading-dot--2 {
  animation-delay: 0.15s;
}
.loading-dot--3 {
  animation-delay: 0.3s;
}

.loading-message {
  font-size: 0.85rem;
  color: var(--text-muted);
  margin-top: 0.25rem;
}

@keyframes loading-pulse {
  0%,
  100% {
    opacity: 0.7;
  }
  50% {
    opacity: 1;
  }
}

@keyframes loading-bounce {
  0%,
  80%,
  100% {
    transform: translateY(0);
    opacity: 0.4;
  }
  40% {
    transform: translateY(-6px);
    opacity: 1;
  }
}
</style>
