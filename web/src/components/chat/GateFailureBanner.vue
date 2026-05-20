<script setup lang="ts">
import { computed, ref } from "vue";
import { useChatStore } from "@/stores/chatStore";

/**
 * GateFailureBanner — persistent, accessible affordance for the
 * `gate_failed` SSE event class.
 *
 * Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026). The Go
 * SSE pipeline emits a typed gate_failed event when the engine's
 * runSwarmGates / dispatchMemberGates halts on a *swarm.GateError.
 * The chat store routes the parsed payload into a session-scoped
 * `lastGateFailure` slice; this banner binds to that state.
 *
 * Visual contract (mirrors CriticalErrorBanner.vue's persistent shape):
 *   - Anchored above the message pane so the user sees the halt
 *     before scrolling.
 *   - Severity palette matching the existing critical banner.
 *   - role="alert" + aria-live="assertive" so screen readers announce
 *     on arrival.
 *   - Persists across user interactions until either Dismiss click
 *     or session change (the store handles the session-change clear).
 *
 * Affordances:
 *   - Title: "Swarm gate halted: <gate_name>" — names the failing
 *     gate so the operator knows what halted.
 *   - Body: <reason> + <cause> when present — the typed
 *     *swarm.GateError fields the engine destructured.
 *   - Subtitle: "<lifecycle> gate on <member_id> in swarm <swarm_id>"
 *     — distinguishes a swarm-boundary halt from a per-member halt.
 *   - "What was checked?" expander: surfaces coord_store_keys when
 *     the gate declared Inputs (Multi-Key Gate Inputs plan); hidden
 *     for legacy single-key gates.
 *   - Dismiss button: calls chatStore.clearGateFailure. A subsequent
 *     halt repopulates the banner unconditionally — the dispatch
 *     overwrites in chatStore.applyContentEvent.
 *
 * No auto-clear: gate failures halt the dispatch and require operator
 * acknowledgement (deliberately distinct from the auto-clearing
 * compaction-flash chip pattern).
 */
defineOptions({ name: "GateFailureBanner" });

const chatStore = useChatStore();
const detailsOpen = ref(false);

const isVisible = computed(() => chatStore.lastGateFailure !== null);
const failure = computed(() => chatStore.lastGateFailure);

const title = computed(() => {
  const name = failure.value?.gateName ?? "";
  if (name.length === 0) {
    return "Swarm gate halted";
  }
  return `Swarm gate halted: ${name}`;
});

const message = computed(() => failure.value?.reason ?? "");
const cause = computed(() => failure.value?.cause ?? "");
const coordStoreKeys = computed(() => failure.value?.coordStoreKeys ?? []);

const subtitle = computed(() => {
  const f = failure.value;
  if (!f) return "";
  const parts: string[] = [];
  if (f.lifecycle) {
    parts.push(`${f.lifecycle} gate`);
  }
  if (f.memberId) {
    parts.push(`on ${f.memberId}`);
  }
  if (f.swarmId) {
    parts.push(`in swarm ${f.swarmId}`);
  }
  return parts.join(" ");
});

function toggleDetails(): void {
  detailsOpen.value = !detailsOpen.value;
}

function dismiss(): void {
  detailsOpen.value = false;
  chatStore.clearGateFailure();
}
</script>

<template>
  <div
    v-if="isVisible"
    class="gate-failure-banner"
    role="alert"
    aria-live="assertive"
    data-testid="gate-failure-banner"
  >
    <span class="gate-failure-icon" aria-hidden="true">!</span>
    <div class="gate-failure-content">
      <span class="gate-failure-title" data-testid="gate-failure-title">{{
        title
      }}</span>
      <span class="gate-failure-message" data-testid="gate-failure-message">
        {{ message }}
      </span>
      <span
        v-if="cause"
        class="gate-failure-cause"
        data-testid="gate-failure-cause"
      >
        {{ cause }}
      </span>
      <span
        v-if="subtitle"
        class="gate-failure-subtitle"
        data-testid="gate-failure-subtitle"
      >
        {{ subtitle }}
      </span>
      <button
        v-if="coordStoreKeys.length > 0"
        type="button"
        class="gate-failure-details-toggle"
        data-testid="gate-failure-details-toggle"
        :aria-expanded="detailsOpen"
        @click="toggleDetails"
      >
        {{ detailsOpen ? "Hide checked inputs" : "What was checked?" }}
      </button>
      <ul
        v-if="detailsOpen && coordStoreKeys.length > 0"
        class="gate-failure-details"
        data-testid="gate-failure-details"
      >
        <li
          v-for="key in coordStoreKeys"
          :key="key"
          class="gate-failure-coord-key"
        >
          <code>{{ key }}</code>
        </li>
      </ul>
    </div>
    <button
      type="button"
      class="gate-failure-dismiss"
      data-testid="gate-failure-dismiss"
      aria-label="Dismiss gate failure"
      @click="dismiss"
    >
      &times;
    </button>
  </div>
</template>

<style scoped>
.gate-failure-banner {
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

.gate-failure-icon {
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

.gate-failure-content {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  flex: 1;
  min-width: 0;
}

.gate-failure-title {
  font-weight: 600;
  color: rgba(248, 113, 113, 1);
  font-size: 0.95rem;
}

.gate-failure-message {
  color: var(--text-primary, #f5f5f5);
  word-wrap: break-word;
}

.gate-failure-cause {
  color: var(--text-muted, #b0b0b0);
  font-size: 0.85rem;
  word-wrap: break-word;
}

.gate-failure-subtitle {
  color: var(--text-muted, #b0b0b0);
  font-size: 0.8rem;
}

.gate-failure-details-toggle {
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

.gate-failure-details-toggle:hover {
  color: rgba(254, 178, 178, 1);
}

.gate-failure-details {
  margin: 0.25rem 0 0;
  padding-left: 1rem;
  color: var(--text-muted, #b0b0b0);
  font-size: 0.8rem;
}

.gate-failure-coord-key code {
  display: inline-block;
  padding: 0.05rem 0.3rem;
  background: rgba(0, 0, 0, 0.3);
  border-radius: 3px;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.75rem;
  color: var(--text-primary, #f5f5f5);
  user-select: all;
}

.gate-failure-dismiss {
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

.gate-failure-dismiss:hover {
  color: rgba(254, 178, 178, 1);
}
</style>
