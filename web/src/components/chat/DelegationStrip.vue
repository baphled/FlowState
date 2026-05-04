<script setup lang="ts">
import { computed } from 'vue'
import { useSwarmStore } from '@/stores/swarmStore'
import { useChatStore } from '@/stores/chatStore'
import type { SwarmEvent } from '@/types'

defineOptions({ name: 'DelegationStrip' })

// DelegationStrip is the in-thread relocation of the side-panel delegation
// cards. It preserves the navigation contract from `4607120`:
//   - reads `metadata.child_session_id` from each SwarmEvent
//   - on click/keyboard activation calls `chatStore.loadSessionMessages(id)`
//   - is a no-op when the event lacks a child_session_id
// The side panel is now reserved for the todo list; this strip lives in the
// chat-main region so delegation activity stays reachable from the thread
// without duplicating side-panel surface area.
const swarmStore = useSwarmStore()
const chatStore = useChatStore()

const delegationEvents = computed(() => swarmStore.delegationEvents)

function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString()
}

function delegationSummary(event: SwarmEvent): string {
  const meta = event.metadata
  if (!meta) return '—'
  const from = meta.source_agent || meta.from || meta.from_agent || '?'
  const to = meta.target_agent || meta.to || meta.to_agent || '?'
  const status = meta.status || meta.delegation_status || ''
  return `${from} → ${to}${status ? ` (${status})` : ''}`
}

function childSessionIdFor(event: SwarmEvent): string | null {
  const candidate = event.metadata?.child_session_id
  if (typeof candidate === 'string' && candidate.length > 0) {
    return candidate
  }
  return null
}

function isClickable(event: SwarmEvent): boolean {
  return childSessionIdFor(event) !== null
}

async function selectDelegationSession(event: SwarmEvent): Promise<void> {
  const sessionId = childSessionIdFor(event)
  if (!sessionId) {
    return
  }
  await chatStore.loadSessionMessages(sessionId)
}
</script>

<template>
  <section
    class="delegation-strip"
    :class="{ 'is-empty': delegationEvents.length === 0 }"
    data-testid="delegation-strip"
  >
    <template v-if="delegationEvents.length > 0">
      <header class="strip-header">
        <span class="strip-title">Delegations</span>
        <span class="strip-counter">{{ delegationEvents.length }}</span>
      </header>
      <ul class="strip-list">
        <li
          v-for="event in delegationEvents"
          :key="event.id"
          class="strip-entry"
          :class="{ clickable: isClickable(event) }"
          :data-testid="`delegation-entry-${event.id}`"
          :role="isClickable(event) ? 'button' : undefined"
          :tabindex="isClickable(event) ? 0 : undefined"
          @click="selectDelegationSession(event)"
          @keydown.enter.prevent="selectDelegationSession(event)"
          @keydown.space.prevent="selectDelegationSession(event)"
        >
          <span class="strip-icon" aria-hidden="true">↗</span>
          <span class="strip-summary">{{ delegationSummary(event) }}</span>
          <span class="strip-time">{{ formatTime(event.timestamp) }}</span>
        </li>
      </ul>
    </template>
  </section>
</template>

<style scoped>
.delegation-strip {
  flex-shrink: 0;
  padding: 0.4rem 1rem;
  background: var(--bg-secondary);
  border-top: 1px solid var(--border);
  font-family: var(--font-mono);
}

.delegation-strip.is-empty {
  display: none;
}

.strip-header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-muted);
  margin-bottom: 0.3rem;
}

.strip-title {
  flex: 1;
}

.strip-counter {
  background: var(--event-delegation, var(--accent));
  color: #fff;
  font-size: 0.65rem;
  padding: 0.1rem 0.35rem;
  border-radius: 10px;
}

.strip-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  max-height: 110px;
  overflow-y: auto;
}

.strip-entry {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.25rem 0.5rem;
  font-size: 0.75rem;
  color: var(--text-secondary);
  border: 1px dashed var(--border);
  border-left: 2px solid var(--event-delegation, var(--accent));
  border-radius: 0 var(--radius) var(--radius) 0;
  background: transparent;
}

.strip-entry.clickable {
  cursor: pointer;
  transition: background 0.15s;
}

.strip-entry.clickable:hover,
.strip-entry.clickable:focus-visible {
  background: var(--bg-elevated);
  outline: none;
}

.strip-entry.clickable:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}

.strip-icon {
  color: var(--accent);
  font-weight: 700;
}

.strip-summary {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.strip-time {
  font-size: 0.68rem;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}
</style>
