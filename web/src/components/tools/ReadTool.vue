<script setup lang="ts">
import { computed } from 'vue'
import CopyButton from './CopyButton.vue'
import ToolBubble from './ToolBubble.vue'
import type { ToolRendererProps } from './toolRendererProps'

const props = withDefaults(defineProps<ToolRendererProps>(), {
  status: 'completed',
})

function parseToolInput(raw: string | undefined): Record<string, unknown> {
  if (!raw) return {}
  try {
    const parsed: unknown = JSON.parse(raw)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>
    }
    return {}
  } catch {
    return {}
  }
}

const lineRange = computed(() => {
  const input = parseToolInput(props.toolInput)
  const limit = typeof input.limit === 'number' ? input.limit : undefined
  const offset = typeof input.offset === 'number' ? input.offset : undefined

  if (limit === undefined && offset === undefined) return null

  const start = (offset ?? 0) + 1
  const end = (offset ?? 0) + (limit ?? 0)
  return `lines ${start}–${end}`
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
    <div class="tool-renderer" data-component="read-tool">
      <div class="tool-renderer__header">
        <span class="tool-renderer__label">File contents</span>
        <span v-if="lineRange" class="tool-renderer__line-range" data-testid="line-range">[{{ lineRange }}]</span>
        <CopyButton :text="props.body" />
      </div>
      <pre class="tool-code tool-code--file" data-component="read-content"><code>{{ props.body }}</code></pre>
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
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
  font-size: 0.85rem;
  line-height: 1.5;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
}

.tool-code--file {
  max-height: 400px;
  overflow-y: auto;
}

.tool-renderer__line-range {
  font-size: 0.75rem;
  color: var(--text-muted, #565f89);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
}
</style>
