<script setup lang="ts">
import { ref } from "vue";
import CopyButton from "./CopyButton.vue";
import type { ToolRendererProps } from "./toolRendererProps";

const props =
  defineProps<Pick<ToolRendererProps, "toolName" | "heading" | "body">>();

const isOpen = ref(false);

function toggleDetails(): void {
  isOpen.value = !isOpen.value;
}
</script>

<template>
  <div
    class="tool-error-card"
    data-component="tool-error-card"
    data-tool="error"
  >
    <div class="tool-error-card__header">
      <div class="tool-error-card__summary">
        <span class="tool-error-card__icon" aria-hidden="true">✕</span>
        <div class="tool-error-card__text">
          <strong class="tool-error-card__tool">{{ props.toolName }}</strong>
          <span class="tool-error-card__heading">{{ props.heading }}</span>
        </div>
      </div>
      <button
        class="tool-error-card__toggle"
        data-testid="tool-error-toggle"
        type="button"
        @click="toggleDetails"
      >
        {{ isOpen ? "Hide details" : "Show details" }}
      </button>
    </div>

    <div
      v-if="isOpen"
      class="tool-error-card__details"
      data-component="tool-error-details"
    >
      <div class="tool-error-card__actions">
        <CopyButton :text="props.body" />
      </div>
      <pre class="tool-error-card__message"><code>{{ props.body }}</code></pre>
    </div>
  </div>
</template>

<style scoped>
.tool-error-card {
  border: 1px solid color-mix(in srgb, var(--error, #f7768e) 35%, transparent);
  border-left: 4px solid var(--error, #f7768e);
  border-radius: var(--radius, 12px);
  background: color-mix(
    in srgb,
    var(--error, #f7768e) 10%,
    var(--surface-low, #1a1b26)
  );
  color: var(--text-primary, #c0caf5);
  overflow: hidden;
}

.tool-error-card__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.9rem 1rem;
}

.tool-error-card__summary {
  display: flex;
  gap: 0.75rem;
  align-items: flex-start;
}

.tool-error-card__icon {
  color: var(--error, #f7768e);
  font-size: 1rem;
  line-height: 1.2;
}

.tool-error-card__text {
  display: grid;
  gap: 0.15rem;
}

.tool-error-card__tool {
  color: var(--text-primary, #c0caf5);
}

.tool-error-card__heading {
  color: var(--text-secondary, #a9b1d6);
  font-size: 0.85rem;
}

.tool-error-card__toggle {
  border: 1px solid color-mix(in srgb, var(--error, #f7768e) 35%, transparent);
  border-radius: calc(var(--radius, 12px) - 4px);
  background: transparent;
  color: var(--text-primary, #c0caf5);
  cursor: pointer;
  font-size: 0.8rem;
  padding: 0.35rem 0.6rem;
}

.tool-error-card__details {
  display: grid;
  gap: 0.5rem;
  padding: 0 1rem 1rem;
}

.tool-error-card__actions {
  display: flex;
  justify-content: flex-end;
}

.tool-error-card__message {
  margin: 0;
  padding: 0.85rem 1rem;
  border: 1px solid color-mix(in srgb, var(--error, #f7768e) 30%, transparent);
  border-radius: calc(var(--radius, 12px) - 4px);
  background: color-mix(
    in srgb,
    var(--error, #f7768e) 7%,
    var(--surface-low, #1a1b26)
  );
  color: var(--text-primary, #c0caf5);
  font-family:
    ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono",
    "Courier New", monospace;
  font-size: 0.85rem;
  line-height: 1.5;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
