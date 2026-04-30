<script setup lang="ts">
import type { SwarmEvent } from '@/types'

defineOptions({ name: 'EventCard' })

const props = defineProps<{ event: SwarmEvent }>()

function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString()
}

function payloadSummary(payload: Record<string, unknown>): string {
  const entries = Object.entries(payload).slice(0, 2)
  return entries.map(([k, v]) => `${k}: ${String(v)}`).join(' · ')
}
</script>

<template>
  <div
    class="event-card"
    :class="`event-type-${props.event.type.replace('_', '-')}`"
    :data-testid="`event-card-${props.event.id}`"
    :data-event-type="props.event.type"
  >
    <div class="event-header">
      <span class="event-type-badge">{{ props.event.type.replace('_', ' ') }}</span>
      <span class="event-agent">{{ props.event.agentName }}</span>
      <span class="event-time">{{ formatTime(props.event.timestamp) }}</span>
    </div>
    <p class="event-payload">{{ payloadSummary(props.event.payload) }}</p>
  </div>
</template>

<style scoped>
.event-card {
  padding: 0.6rem 0.8rem;
  border-left: 3px solid var(--border);
  background: var(--bg-elevated);
  border-radius: 0 var(--radius) var(--radius) 0;
  margin-bottom: 0.5rem;
}

.event-type-delegation { border-left-color: var(--event-delegation); }
.event-type-tool-call   { border-left-color: var(--event-tool-call); }
.event-type-plan        { border-left-color: var(--event-plan); }
.event-type-review      { border-left-color: var(--event-review); }

.event-header {
  display: flex;
  gap: 0.5rem;
  align-items: center;
  margin-bottom: 0.25rem;
}

.event-type-badge {
  font-size: 0.68rem;
  font-family: var(--font-mono);
  text-transform: uppercase;
  letter-spacing: 0.08em;
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
  background: var(--bg-secondary);
  color: var(--text-secondary);
}

.event-type-delegation .event-type-badge { color: var(--event-delegation); }
.event-type-tool-call .event-type-badge  { color: var(--event-tool-call); }
.event-type-plan .event-type-badge       { color: var(--event-plan); }
.event-type-review .event-type-badge     { color: var(--event-review); }

.event-agent {
  font-weight: 600;
  font-size: 0.82rem;
  color: var(--text-primary);
}

.event-time {
  font-size: 0.72rem;
  color: var(--text-muted);
  margin-left: auto;
  font-family: var(--font-mono);
}

.event-payload {
  font-size: 0.78rem;
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
