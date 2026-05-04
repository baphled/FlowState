<script setup lang="ts">
import { computed } from 'vue'
import CopyButton from './CopyButton.vue'
import ToolBubble from './ToolBubble.vue'
import type { ToolRendererProps } from './toolRendererProps'

type EditLineKind = 'added' | 'removed' | 'plain'

interface EditLine {
  text: string
  kind: EditLineKind
}

const props = withDefaults(defineProps<ToolRendererProps>(), {
  status: 'completed',
})

function isAddedLine(line: string): boolean {
  return line.startsWith('+') && !line.startsWith('+++')
}

function isRemovedLine(line: string): boolean {
  return line.startsWith('-') && !line.startsWith('---')
}

function resolveLineKind(line: string, useDiffFormatting: boolean): EditLineKind {
  if (!useDiffFormatting) {
    return 'plain'
  }

  if (isAddedLine(line)) {
    return 'added'
  }

  if (isRemovedLine(line)) {
    return 'removed'
  }

  return 'plain'
}

const lines = computed<EditLine[]>(() => {
  const splitLines = props.body.split('\n')
  const useDiffFormatting = splitLines.some((line) => isAddedLine(line) || isRemovedLine(line))

  return splitLines.map((line) => ({
    text: line,
    kind: resolveLineKind(line, useDiffFormatting),
  }))
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
    <div class="tool-renderer" data-component="edit-tool">
      <div class="tool-renderer__header">
        <span class="tool-renderer__label">Patch</span>
        <CopyButton :text="props.body" />
      </div>
      <pre class="tool-code tool-code--edit"><code>
<template v-for="(line, index) in lines" :key="`${index}-${line.text}`"><span class="tool-line" :class="`tool-line--${line.kind}`" :data-line-kind="line.kind">{{ line.text }}</span>
<br v-if="index < lines.length - 1" /></template></code></pre>
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

.tool-line--added {
  display: inline;
  color: #9ece6a;
}

.tool-line--removed {
  display: inline;
  color: var(--error, #f7768e);
}

.tool-line--plain {
  display: inline;
  color: var(--text-primary, #c0caf5);
}
</style>
