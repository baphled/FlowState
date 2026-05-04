<script setup lang="ts">
import CopyButton from './CopyButton.vue'
import ToolBubble from './ToolBubble.vue'
import type { ToolRendererProps } from './toolRendererProps'

const props = withDefaults(defineProps<ToolRendererProps>(), {
  status: 'completed',
})
</script>

<template>
  <ToolBubble
    :tool-name="props.toolName"
    :title="props.toolName"
    :status="props.status"
    :default-open="true"
  >
    <div class="tool-renderer" data-component="bash-tool">
      <section class="tool-section">
        <div class="tool-section__header">
          <span class="tool-section__label">Command</span>
          <CopyButton :text="props.heading" />
        </div>
        <pre class="tool-code tool-code--bash" data-component="bash-command"><code>{{ props.heading }}</code></pre>
      </section>

      <section v-if="props.body" class="tool-section">
        <div class="tool-section__header">
          <span class="tool-section__label">Output</span>
          <CopyButton :text="props.body" />
        </div>
        <pre class="tool-code tool-code--output" data-component="bash-output"><code>{{ props.body }}</code></pre>
      </section>
    </div>
  </ToolBubble>
</template>

<style scoped>
.tool-renderer {
  display: grid;
  gap: 0.85rem;
}

.tool-section {
  display: grid;
  gap: 0.45rem;
}

.tool-section__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}

.tool-section__label {
  color: var(--text-secondary, #a9b1d6);
  font-size: 0.78rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  text-transform: uppercase;
}

.tool-code {
  margin: 0;
  padding: 0.85rem 1rem;
  border: 1px solid var(--border, rgba(148, 163, 184, 0.25));
  border-radius: calc(var(--radius, 12px) - 4px);
  background: var(--surface-low, #1a1b26);
  color: var(--text-primary, #c0caf5);
  font-family: ui-monospace, SFMono-Regular, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
  font-size: 0.85rem;
  line-height: 1.5;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
}

.tool-code--output {
  background: var(--surface-hover, #16161e);
}
</style>
