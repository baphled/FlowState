<script setup lang="ts">
import { computed, ref } from "vue";
import { useChatStore } from "@/stores/chatStore";
import type {
  PermissionGrantScope,
  TurnStatePermissionRequest,
} from "@/api";

/**
 * PermissionPrompt — inline ModeAskUser permission grant component.
 * Permission Mode ModeAskUser Extension plan (May 2026), Slice 3, §3.
 *
 * Renders an anchored card beneath the suspended tool_call bubble
 * carrying Tool / Resource / Agent / Reason rows and four scope
 * buttons (Allow once / This session / Forever / Deny). On click the
 * component routes through chatStore.grantPermission which POSTs to
 * /api/v1/sessions/{id}/permission-grant; the long-poll diff carries
 * the resulting status flip to all open tabs (R5 cross-tab guard per
 * plan §11 R5 + §17.1).
 *
 * Optimistic state: every button shows a "Granting…" label while the
 * request is in flight. The flag clears either when the long-poll
 * diff observes a non-pending status (success path — the entry leaves
 * pendingPermissionRequests so the component unmounts naturally) OR
 * when the POST throws (failure path — store action re-enables the
 * buttons for retry).
 *
 * The component is intentionally render-only beyond the click handler
 * — owning the wire shape via the request prop keeps the surface
 * narrow and lets MessageBubble.vue read pendingPermissionRequests
 * and pass exactly one entry in.
 *
 * Per plan §3 the entire chrome (icon, four rows, four buttons) is
 * styled to read as "interactive intervention, not relaxation" — the
 * purple ask-mode palette from PermissionModeChip carries through to
 * the icon + border tint here.
 *
 * Tests:
 *   - PermissionPrompt.spec.ts pins the four buttons + per-row labels +
 *     emit semantics + Granting… state.
 *   - MessageBubble.spec.ts pins the inline-render gate (when a pending
 *     entry keyed by the bubble's tool call exists).
 */
defineOptions({ name: "PermissionPrompt" });

const props = defineProps<{
  request: TurnStatePermissionRequest;
}>();

const chatStore = useChatStore();

// Local fallback for failure surfacing — the store's grantPermission
// re-throws on POST failure so we can surface a retry-friendly inline
// message without growing the global error slot. Cleared on a retry
// click so the user sees the new attempt's status, not the prior
// error.
const localError = ref<string>("");

const isGranting = computed(() =>
  chatStore.grantingPermissionRequests.has(props.request.request_id),
);

const buttonLabel = (idle: string): string =>
  isGranting.value ? "Granting…" : idle;

async function grant(scope: PermissionGrantScope): Promise<void> {
  // Already in flight — ignore double clicks. The store action's
  // try/finally is the canonical guard, but adding a render-side
  // gate here keeps the UI's affordance honest (the disabled
  // attribute on the buttons is the screen-reader signal; this is
  // the keyboard-navigation belt-and-braces).
  if (isGranting.value) return;
  localError.value = "";
  try {
    await chatStore.grantPermission(props.request.request_id, scope);
  } catch (err) {
    localError.value =
      err instanceof Error
        ? err.message
        : "Failed to send grant. Click a button to retry.";
  }
}
</script>

<template>
  <div
    class="permission-prompt"
    role="region"
    aria-label="Permission required"
    data-testid="permission-prompt"
    :data-request-id="props.request.request_id"
  >
    <div class="permission-prompt__header">
      <span class="permission-prompt__icon" aria-hidden="true">🔒</span>
      <span class="permission-prompt__title">Permission required</span>
    </div>
    <dl class="permission-prompt__rows">
      <div class="permission-prompt__row">
        <dt>Tool</dt>
        <dd data-testid="permission-prompt-tool">{{ props.request.tool_name }}</dd>
      </div>
      <div v-if="props.request.resource" class="permission-prompt__row">
        <dt>Resource</dt>
        <dd
          class="permission-prompt__resource"
          data-testid="permission-prompt-resource"
        >
          {{ props.request.resource }}
        </dd>
      </div>
      <div v-if="props.request.agent_name" class="permission-prompt__row">
        <dt>Agent</dt>
        <dd data-testid="permission-prompt-agent">{{ props.request.agent_name }}</dd>
      </div>
      <div v-if="props.request.denial_reason" class="permission-prompt__row">
        <dt>Reason</dt>
        <dd
          class="permission-prompt__reason"
          data-testid="permission-prompt-reason"
        >
          {{ props.request.denial_reason }}
        </dd>
      </div>
    </dl>
    <div class="permission-prompt__buttons" role="group" aria-label="Grant scope">
      <button
        type="button"
        class="permission-prompt__btn permission-prompt__btn--once"
        data-testid="permission-prompt-allow-once"
        :disabled="isGranting"
        @click="grant('once')"
      >
        {{ buttonLabel("Allow once") }}
      </button>
      <button
        type="button"
        class="permission-prompt__btn permission-prompt__btn--session"
        data-testid="permission-prompt-allow-session"
        :disabled="isGranting"
        @click="grant('session')"
      >
        {{ buttonLabel("This session") }}
      </button>
      <button
        type="button"
        class="permission-prompt__btn permission-prompt__btn--forever"
        data-testid="permission-prompt-allow-forever"
        :disabled="isGranting"
        @click="grant('forever')"
      >
        {{ buttonLabel("Forever") }}
      </button>
      <button
        type="button"
        class="permission-prompt__btn permission-prompt__btn--deny"
        data-testid="permission-prompt-deny"
        :disabled="isGranting"
        @click="grant('deny')"
      >
        {{ buttonLabel("Deny") }}
      </button>
    </div>
    <div
      v-if="localError"
      class="permission-prompt__error"
      role="alert"
      data-testid="permission-prompt-error"
    >
      {{ localError }}
    </div>
  </div>
</template>

<style scoped>
.permission-prompt {
  /* Inline anchored card. Sits beneath the tool-invocation chrome
     (MessageBubble.vue renders us inside the same .tool-invocation
     wrapper). Purple ask-mode tint mirrors PermissionModeChip's
     `--severity-ask` palette so the operator reads "interactive
     intervention, not relaxation" (plan §3). */
  margin: 0.5rem 0;
  padding: 0.75rem 1rem;
  border: 1px solid var(--color-permission-ask, #b88dff);
  border-radius: 0.5rem;
  background: var(--color-permission-ask-bg, rgba(184, 141, 255, 0.08));
  font-size: 0.875rem;
}
.permission-prompt__header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-weight: 600;
  margin-bottom: 0.5rem;
}
.permission-prompt__icon {
  font-size: 1rem;
}
.permission-prompt__rows {
  margin: 0 0 0.75rem;
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.25rem 0.75rem;
}
.permission-prompt__row {
  display: contents;
}
.permission-prompt__row dt {
  font-weight: 500;
  color: var(--color-text-muted, #666);
}
.permission-prompt__row dd {
  margin: 0;
  word-break: break-all;
}
.permission-prompt__resource,
.permission-prompt__reason {
  font-family: var(--font-monospace, ui-monospace, "SF Mono", monospace);
  font-size: 0.8125rem;
}
.permission-prompt__buttons {
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
}
.permission-prompt__btn {
  padding: 0.375rem 0.75rem;
  border: 1px solid currentColor;
  border-radius: 0.375rem;
  background: transparent;
  font-size: 0.8125rem;
  cursor: pointer;
}
.permission-prompt__btn:disabled {
  opacity: 0.6;
  cursor: progress;
}
.permission-prompt__btn--deny {
  color: var(--color-permission-danger, #c53030);
}
.permission-prompt__error {
  margin-top: 0.5rem;
  color: var(--color-permission-danger, #c53030);
  font-size: 0.8125rem;
}
</style>
