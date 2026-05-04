<script setup lang="ts">
import { computed } from 'vue'
import CopyButton from './CopyButton.vue'
import ToolBubble from './ToolBubble.vue'
import type { ToolRendererProps } from './toolRendererProps'

const maxToolInputLength = 200

const props = withDefaults(defineProps<ToolRendererProps>(), {
  status: 'completed',
})

const truncatedToolInput = computed(() => {
  if (!props.toolInput) {
    return ''
  }

  if (props.toolInput.length <= maxToolInputLength) {
    return props.toolInput
  }

  return `${props.toolInput.slice(0, maxToolInputLength)}...`
})
</script>

<template>
  <ToolBubble
    :tool-name="props.toolName"
    :title="props.toolName"
    :subtitle="props.heading"
    :status="props.status"
    :default-open="true"
  >
    <div class="tool-renderer" data-component="generic-tool">
      <section v-if="truncatedToolInput" class="tool-section">
        <span class="tool-section__label">Input</span>
        <pre class="tool-code tool-code--input"><code data-component="generic-tool-input">{{ truncatedToolInput }}</code></pre>
      </section>

      <section class="tool-section">
        <div class="tool-section__header">
          <span class="tool-section__label">Output</span>
          <CopyButton :text="props.body" />
        </div>
        <pre class="tool-code tool-code--output" data-component="generic-tool-output"><code>{{ props.body }}</code></pre>
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
  text-transform: uppercase;
}

.tool-code {
  margin: 0;
  padding: 0.85rem 1rem;
  border: 1px solid var(--border, rgba(148, 163, 184, 0.25));
  border-radius: calc(var(--radius, 12px) - 4px);
  background: var(--surface-low, #1a1b26);
  color: var(--text-primary, #c0caf5);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
  font-size: 0.85rem;
  line-height: 1.5;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
}

.tool-code--input {
  background: var(--surface-hover, #16161e);
}
</style>
