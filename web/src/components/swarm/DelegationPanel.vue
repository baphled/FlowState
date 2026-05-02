<script setup lang="ts">
import { computed } from 'vue'
import { useSwarmStore } from '@/stores/swarmStore'

defineOptions({ name: 'DelegationPanel' })

const swarmStore = useSwarmStore()
const delegationEvents = computed(() => swarmStore.delegationEvents)
const harnessEvents = computed(() => swarmStore.harnessEvents)

const emit = defineEmits<{
  close: []
}>()

function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString()
}

function delegationSummary(event: { metadata?: Record<string, unknown> }): string {
  if (!event.metadata) return '—'
  const meta = event.metadata
  const from = meta.source_agent || meta.from || meta.from_agent || '?'
  const to = meta.target_agent || meta.to || meta.to_agent || '?'
  const status = meta.status || meta.delegation_status || ''
  return `${from} → ${to}${status ? ` (${status})` : ''}`
}
</script>

<template>
  <aside class="delegation-panel" data-testid="delegation-panel">
    <header class="panel-header">
      <span class="panel-title">Delegation</span>
      <span v-if="delegationEvents.length > 0" class="delegation-badge">
        {{ delegationEvents.length }}
      </span>
      <button class="close-btn" data-testid="close-delegation-panel" @click="emit('close')">
        ✕
      </button>
    </header>
    <div class="delegation-list">
      <div
        v-for="event in delegationEvents"
        :key="event.id"
        class="delegation-card"
        :data-testid="`delegation-${event.id}`"
      >
        <div class="delegation-header">
          <span class="delegation-agent">{{ event.agent_id }}</span>
          <span class="delegation-time">{{ formatTime(event.timestamp) }}</span>
        </div>
        <p class="delegation-summary">{{ delegationSummary(event) }}</p>
      </div>

      <div
        v-for="event in harnessEvents"
        :key="event.id"
        class="harness-card"
        :data-testid="`harness-${event.id}`"
      >
        <div class="harness-header">
          <span class="harness-type-badge">{{ event.type.replace('harness_', '') }}</span>
          <span class="harness-time">{{ formatTime(event.timestamp) }}</span>
        </div>
        <p class="harness-payload">{{ event.metadata?.message || '—' }}</p>
      </div>

      <p v-if="delegationEvents.length === 0 && harnessEvents.length === 0" class="delegation-empty">
        No delegation or harness events yet
      </p>
    </div>
  </aside>
</template>

<style scoped>
.delegation-panel {
  display: flex;
  flex-direction: column;
  background: var(--bg-secondary);
  border-left: 1px solid var(--border);
}

.panel-header {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.5rem 0.6rem;
  border-bottom: 1px solid var(--border);
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.08em;
}

.panel-title {
  flex: 1;
}

.delegation-badge {
  background: var(--event-delegation, var(--accent));
  color: #fff;
  font-size: 0.65rem;
  padding: 0.1rem 0.35rem;
  border-radius: 10px;
}

.close-btn {
  background: none;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  font-size: 0.7rem;
  padding: 0.1rem 0.25rem;
}

.close-btn:hover {
  color: var(--text-primary);
}

.delegation-list {
  flex: 1;
  overflow-y: auto;
  padding: 0.4rem;
}

.delegation-card,
.harness-card {
  padding: 0.5rem 0.6rem;
  border-left: 3px solid var(--event-delegation, var(--accent));
  background: var(--bg-elevated);
  border-radius: 0 var(--radius) var(--radius) 0;
  margin-bottom: 0.4rem;
}

.harness-card {
  border-left-color: var(--event-plan, #9b59b6);
}

.delegation-header,
.harness-header {
  display: flex;
  gap: 0.5rem;
  align-items: center;
  margin-bottom: 0.2rem;
  font-size: 0.72rem;
}

.delegation-agent {
  font-weight: 600;
  color: var(--text-primary);
}

.delegation-time,
.harness-time {
  font-size: 0.68rem;
  color: var(--text-muted);
  margin-left: auto;
  font-family: var(--font-mono);
}

.delegation-summary {
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.harness-type-badge {
  font-size: 0.62rem;
  font-family: var(--font-mono);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  padding: 0.08rem 0.3rem;
  border-radius: 2px;
  background: var(--bg-secondary);
  color: var(--event-plan, #9b59b6);
}

.harness-payload {
  font-size: 0.72rem;
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.delegation-empty {
  color: var(--text-muted);
  font-size: 0.8rem;
  text-align: center;
  padding: 1rem 0;
}
</style>