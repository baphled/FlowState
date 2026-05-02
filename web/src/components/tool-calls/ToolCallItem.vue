<script setup lang="ts">
import type { ToolCallEvent } from '@/stores/toolStore'

defineOptions({ name: 'ToolCallItem' })

const props = defineProps<{ event: ToolCallEvent }>()

function formatDuration(start: string, end?: string): string {
  const startMs = new Date(start).getTime()
  const endMs = end ? new Date(end).getTime() : Date.now()
  const diff = endMs - startMs
  if (diff < 1000) return `${diff}ms`
  return `${(diff / 1000).toFixed(1)}s`
}

function truncateResult(result?: string, max = 120): string {
  if (!result) return '—'
  return result.length > max ? result.slice(0, max) + '…' : result
}

const statusIcon: Record<ToolCallEvent['status'], string> = {
  pending: '◷',
  running: '◐',
  completed: '✓',
  error: '✕',
}
</script>

<template>
  <div
    class="tool-call-item"
    :class="`tool-status-${props.event.status}`"
    :data-testid="`tool-call-${props.event.id}`"
  >
    <div class="tool-header">
      <span class="tool-status-icon">{{ statusIcon[props.event.status] }}</span>
      <span class="tool-name">{{ props.event.toolName }}</span>
      <span class="tool-duration">{{ formatDuration(props.event.startedAt, props.event.completedAt) }}</span>
    </div>
    <div v-if="props.event.result" class="tool-result">
      {{ truncateResult(props.event.result) }}
    </div>
    <div v-else-if="props.event.arguments && Object.keys(props.event.arguments).length > 0" class="tool-args">
      {{ truncateResult(JSON.stringify(props.event.arguments), 80) }}
    </div>
  </div>
</template>

<style scoped>
.tool-call-item {
  padding: 0.45rem 0.6rem;
  border-left: 2px solid var(--border);
  background: var(--bg-elevated);
  border-radius: 0 var(--radius) var(--radius) 0;
  margin-bottom: 0.35rem;
  font-size: 0.78rem;
}

.tool-status-pending { border-left-color: var(--text-muted); }
.tool-status-running { border-left-color: var(--accent); }
.tool-status-completed { border-left-color: #22c55e; }
.tool-status-error { border-left-color: #ef4444; }

.tool-header {
  display: flex;
  gap: 0.4rem;
  align-items: center;
}

.tool-status-icon {
  font-size: 0.7rem;
  width: 1rem;
}

.tool-status-running .tool-status-icon {
  animation: spin 1s linear infinite;
}

.tool-name {
  font-family: var(--font-mono);
  font-weight: 600;
  color: var(--text-primary);
  font-size: 0.75rem;
}

.tool-duration {
  font-size: 0.68rem;
  color: var(--text-muted);
  margin-left: auto;
  font-family: var(--font-mono);
}

.tool-result,
.tool-args {
  margin-top: 0.25rem;
  padding-left: 1.4rem;
  font-size: 0.7rem;
  color: var(--text-muted);
  font-family: var(--font-mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 280px;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>