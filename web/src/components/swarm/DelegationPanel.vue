<script setup lang="ts">
import { computed } from 'vue'
import { useSwarmStore } from '@/stores/swarmStore'
import { useChatStore } from '@/stores/chatStore'
import type { SwarmEvent } from '@/types'

defineOptions({ name: 'DelegationPanel' })

const swarmStore = useSwarmStore()
const chatStore = useChatStore()
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

// childSessionIdFor extracts the delegated agent's session identifier
// from the SwarmEvent metadata. Mirrors the TUI delegation contract:
// the engine emits `child_session_id` on delegation events that spawn a
// new session so consumers can navigate to the delegated work without
// reconstructing the parent → child mapping client-side.
function childSessionIdFor(event: SwarmEvent): string | null {
  const candidate = event.metadata?.child_session_id
  if (typeof candidate === 'string' && candidate.length > 0) {
    return candidate
  }
  return null
}

// loadedSkillsFor extracts the `load_skills` argument from the
// delegation event metadata. The backend currently does NOT
// populate this field on the SwarmEvent stream (only the engine
// reads it from the delegate tool arguments to inject the
// matching skill prompts into the child manifest) — see
// internal/plugin/events/events.go DelegationEventData and
// projectDelegationEvent in internal/api/server.go. Surfacing the
// loaded skills on the delegation card requires wiring that field
// through. Until then this function returns [] and the chip row
// stays absent; once the backend lands the field, the existing
// chip row in this component lights up without further frontend
// changes.
function loadedSkillsFor(event: SwarmEvent): string[] {
  const raw = event.metadata?.load_skills
  if (!Array.isArray(raw)) return []
  return raw.filter((s): s is string => typeof s === 'string' && s.length > 0)
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
        :class="{ clickable: isClickable(event) }"
        :data-testid="`delegation-${event.id}`"
        :role="isClickable(event) ? 'button' : undefined"
        :tabindex="isClickable(event) ? 0 : undefined"
        @click="selectDelegationSession(event)"
        @keydown.enter.prevent="selectDelegationSession(event)"
        @keydown.space.prevent="selectDelegationSession(event)"
      >
        <div class="delegation-header">
          <span class="delegation-agent">{{ event.agent_id }}</span>
          <span class="delegation-time">{{ formatTime(event.timestamp) }}</span>
        </div>
        <p class="delegation-summary">{{ delegationSummary(event) }}</p>
        <div
          v-if="loadedSkillsFor(event).length > 0"
          class="delegation-skills-row"
          data-testid="delegation-skills-row"
        >
          <span class="delegation-skills-label">Skills</span>
          <span
            v-for="skill in loadedSkillsFor(event)"
            :key="skill"
            class="delegation-skill-chip"
            data-testid="delegation-skill-chip"
          >
            {{ skill }}
          </span>
        </div>
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

.delegation-card.clickable {
  cursor: pointer;
  transition: background 0.15s, border-left-color 0.15s;
}

.delegation-card.clickable:hover,
.delegation-card.clickable:focus-visible {
  background: var(--bg-secondary);
  outline: none;
}

.delegation-card.clickable:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
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

.delegation-skills-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.3rem;
  margin-top: 0.35rem;
}

.delegation-skills-label {
  font-size: 0.62rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-muted);
}

.delegation-skill-chip {
  font-size: 0.65rem;
  font-family: var(--font-mono);
  padding: 0.1rem 0.4rem;
  border-radius: 10px;
  background: var(--bg-secondary);
  color: var(--text-secondary);
  border: 1px solid var(--border);
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