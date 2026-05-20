<script setup lang="ts">
/**
 * ThinkingPanel — B2 (Vue UI Parity vs OpenCode, May 2026).
 *
 * Pre-fix, MessageBubble rendered a bare `<p class="thinking">{{ content }}</p>`
 * for `role === 'thinking'` messages. The paragraph was italic and
 * dimmed but flat — markdown (especially fenced code, which models
 * routinely emit in reasoning steps) showed as literal source text.
 *
 * This component:
 *
 *   1. Wraps reasoning in a native `<details>` element — collapsible
 *      with zero JS, keyboard-/screen-reader accessible by default.
 *   2. Routes the content through `MarkdownRenderer` so embedded
 *      code blocks get the same Shiki highlighting as the visible
 *      reply (B1 — already wired into MarkdownRenderer).
 *   3. Collapsed by default — reasoning is opt-in disclosure.
 *
 * The component takes a single `content` prop. When a Message
 * carries a `thinkingBlocks[]` array (the better data source per
 * the brief), MessageBubble renders one ThinkingPanel per block.
 * Legacy thinking-role messages without `thinkingBlocks` get a
 * single ThinkingPanel over the joined content string.
 */
import MarkdownRenderer from "./MarkdownRenderer.vue";

defineOptions({ name: "ThinkingPanel" });

defineProps<{ content: string }>();
</script>

<template>
  <details class="thinking-panel" data-testid="thinking-panel">
    <summary class="thinking-panel__summary">
      <span class="thinking-panel__icon" aria-hidden="true">▸</span>
      <span class="thinking-panel__label">Thinking</span>
    </summary>
    <div class="thinking-panel__body">
      <MarkdownRenderer :content="content" />
    </div>
  </details>
</template>

<style scoped>
.thinking-panel {
  margin: 0.25rem 0;
  border: 1px solid var(--border, rgba(255, 255, 255, 0.1));
  border-left: 2px solid var(--text-muted, #565f89);
  border-radius: var(--radius, 6px);
  background: var(--bg-elevated, transparent);
  font-size: 0.85rem;
  color: var(--text-secondary, inherit);
}

.thinking-panel__summary {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.35rem 0.6rem;
  cursor: pointer;
  font-size: 0.75rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-muted, #565f89);
  user-select: none;
  list-style: none;
}

.thinking-panel__summary::-webkit-details-marker {
  display: none;
}

.thinking-panel__icon {
  display: inline-block;
  transition: transform 0.15s ease;
  font-size: 0.7rem;
}

.thinking-panel[open] > .thinking-panel__summary .thinking-panel__icon {
  transform: rotate(90deg);
}

.thinking-panel__label {
  font-weight: 600;
}

.thinking-panel__body {
  padding: 0.4rem 0.75rem 0.6rem;
  border-top: 1px solid var(--border, rgba(255, 255, 255, 0.08));
  font-style: italic;
  opacity: 0.85;
}
</style>
