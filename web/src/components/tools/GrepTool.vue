<script setup lang="ts">
import { computed } from "vue";
import CopyButton from "./CopyButton.vue";
import ToolBubble from "./ToolBubble.vue";
import type { ToolRendererProps } from "./toolRendererProps";

const props = withDefaults(defineProps<ToolRendererProps>(), {
  status: "completed",
});

// UI Parity I4 (May 2026): grep results are long match lists. Start
// collapsed; subtitle surfaces the pattern. Force open on error.
const cardDefaultOpen = computed(() => props.status === "error");
</script>

<template>
  <ToolBubble
    :tool-name="props.toolName"
    :title="props.toolName"
    :subtitle="props.heading"
    :status="props.status"
    :default-open="cardDefaultOpen"
  >
    <div class="tool-renderer" data-component="grep-tool">
      <div class="tool-renderer__header">
        <span class="tool-renderer__label">Matches</span>
        <CopyButton :text="props.body" />
      </div>
      <pre
        class="tool-code tool-code--grep"
        data-component="grep-content"
      ><code>{{ props.body }}</code></pre>
    </div>
  </ToolBubble>
</template>

<style scoped>
.tool-renderer {
  display: grid;
  gap: 0.45rem;
}

.tool-renderer__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}

.tool-renderer__label {
  color: var(--text-secondary, #a9b1d6);
  font-size: 0.78rem;
  font-weight: 600;
  text-transform: uppercase;
}

.tool-code {
  margin: 0;
  padding: 0.85rem 1rem;
  border: 1px solid var(--border, rgba(148, 163, 184, 0.25));
  border-radius: calc(var(--radius, 12px) - 4px);
  background: var(--surface-low, #1a1b26);
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
