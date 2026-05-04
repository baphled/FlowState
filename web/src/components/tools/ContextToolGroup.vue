<script setup lang="ts">
import { ref, computed } from 'vue'
import type { Message } from '@/types'
import { buildToolRenderSpec } from '@/views/toolRenderSpec'

const props = defineProps<{
  messages: Message[]
  toolCounts: Record<string, number>
}>()

const isOpen = ref(false)

const toolLabels: Record<string, string> = {
  read: 'files read',
  glob: 'patterns matched',
  grep: 'searches',
  list: 'listings',
}

const summaryLabel = computed(() => {
  return Object.entries(props.toolCounts)
    .map(([name, count]) => `${count} ${toolLabels[name] || name}`)
    .join(', ')
})

function toggle() {
  isOpen.value = !isOpen.value
}

function truncateBody(body: string): string {
  const maxLen = 100
  if (body.length <= maxLen) return body
  return body.slice(0, maxLen) + '...'
}

const renderedMessages = computed(() => {
  return props.messages.map((msg) => {
    const spec = buildToolRenderSpec(msg)
    return {
      id: msg.id,
      heading: spec.heading,
      bodyPreview: truncateBody(spec.body),
    }
  })
})
</script>

<template>
  <div
    class="context-tool-group"
    data-component="context-tool-group"
    data-testid="context-tool-group"
    :data-open="isOpen"
  >
    <div class="group-header" @click="toggle">
      <span class="toggle-icon">{{ isOpen ? '▼' : '▶' }}</span>
      <span class="summary">{{ summaryLabel }}</span>
    </div>
    <div v-if="isOpen" class="group-content">
      <div v-for="msg in renderedMessages" :key="msg.id" class="entry">
        <div class="entry-heading">{{ msg.heading }}</div>
        <div class="entry-body">{{ msg.bodyPreview }}</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.context-tool-group {
  --group-border-color: var(--color-border, #30363d);
  --group-header-bg: var(--color-bg-subtle, #161b22);
  --group-text-color: var(--color-text-primary, #c9d1d9);
  --group-text-secondary: var(--color-text-secondary, #8b949e);

  border: 1px solid var(--group-border-color);
  border-radius: 6px;
  margin: 8px 0;
  overflow: hidden;
  background-color: var(--group-header-bg);
}

.group-header {
  padding: 8px 12px;
  cursor: pointer;
  display: flex;
  align-items: center;
  gap: 8px;
  user-select: none;
}

.toggle-icon {
  font-size: 10px;
  width: 12px;
  color: var(--group-text-secondary);
}

.summary {
  font-size: 13px;
  font-weight: 500;
  color: var(--group-text-color);
}

.group-content {
  border-top: 1px solid var(--group-border-color);
  background-color: var(--color-bg-default, #0d1117);
}

.entry {
  padding: 8px 12px;
  border-bottom: 1px solid var(--group-border-color);
}

.entry:last-child {
  border-bottom: none;
}

.entry-heading {
  font-size: 12px;
  font-weight: 600;
  color: var(--group-text-color);
  margin-bottom: 4px;
}

.entry-body {
  font-size: 11px;
  color: var(--group-text-secondary);
  white-space: pre-wrap;
  font-family: var(--font-family-mono, monospace);
}
</style>
