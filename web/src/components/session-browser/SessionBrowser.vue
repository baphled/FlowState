<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import type { Agent } from '@/types'

defineOptions({ name: 'SessionBrowser' })

const emit = defineEmits<{
  'select-session': [sessionId: string]
  'create-session': []
  'close': []
}>()

const chatStore = useChatStore()

const searchQuery = ref('')
const selectedAgentFilter = ref<string>('all')
const isOpen = ref(false)

function formatRelativeTime(dateString: string): string {
  const date = new Date(dateString)
  const now = new Date()
  const seconds = Math.floor((now.getTime() - date.getTime()) / 1000)

  if (seconds < 60) return 'just now'
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`
  if (seconds < 604800) return `${Math.floor(seconds / 86400)}d ago`
  
  return date.toLocaleDateString()
}

const filteredSessions = computed(() => {
  let sessions = chatStore.sessions

  if (selectedAgentFilter.value !== 'all') {
    sessions = sessions.filter(s => s.agentId === selectedAgentFilter.value)
  }

  if (searchQuery.value.trim()) {
    const query = searchQuery.value.toLowerCase()
    sessions = sessions.filter(s => 
      s.title.toLowerCase().includes(query) ||
      s.id.toLowerCase().includes(query)
    )
  }

  return sessions.sort((a, b) => 
    new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime()
  )
})

const availableAgents = computed(() => {
  const agents = new Set(chatStore.sessions.map(s => s.agentId))
  return Array.from(agents).map(agentId => 
    chatStore.availableAgentDetails.find(a => a.id === agentId)
  ).filter(Boolean) as Agent[]
})

function getAgentName(agentId: string): string {
  const agent = chatStore.availableAgentDetails.find(a => a.id === agentId)
  return agent?.name || agentId
}

function getAgentModel(agentId: string): string {
  const agent = chatStore.availableAgentDetails.find(a => a.id === agentId)
  return agent?.model || ''
}

function getAgentProvider(agentId: string): string {
  const agent = chatStore.availableAgentDetails.find(a => a.id === agentId)
  return agent?.provider || ''
}

// UX consolidation (May 2026) — every list of sessions shows live state
// per-row. SessionBrowser is the modal-style picker; we use the same
// vocabulary as ChildSessionsPanel and SessionSwitcher (green pulsing dot)
// so the affordance is consistent across every session-list surface.
function isSessionStreaming(sessionId: string): boolean {
  return chatStore.streamingFor(sessionId).isStreaming
}

function handleSelectSession(sessionId: string): void {
  emit('select-session', sessionId)
  isOpen.value = false
}

function handleCreateSession(): void {
  emit('create-session')
  isOpen.value = false
}

function handleClose(): void {
  emit('close')
}

function open(): void {
  isOpen.value = true
  void chatStore.loadSessions()
}

defineExpose({ open })

onMounted(() => {
  void chatStore.loadSessions()
})
</script>

<template>
  <div v-if="isOpen" class="session-browser" data-testid="session-browser">
    <div class="session-browser-overlay" @click="handleClose" />
    <div class="session-browser-content" role="dialog" aria-modal="true" aria-labelledby="session-browser-title">
      <div class="session-browser-header">
        <h2 id="session-browser-title">Session Browser</h2>
        <button 
          class="close-button" 
          @click="handleClose"
          aria-label="Close session browser"
        >
          ✕
        </button>
      </div>

      <div class="session-browser-controls">
        <div class="search-container">
          <input
            v-model="searchQuery"
            type="text"
            placeholder="Search sessions..."
            class="search-input"
            aria-label="Search sessions"
          />
          <span class="search-icon">🔍</span>
        </div>

        <div class="filter-container">
          <select
            v-model="selectedAgentFilter"
            class="agent-filter"
            aria-label="Filter by agent"
          >
            <option value="all">All Agents</option>
            <option v-for="agent in availableAgents" :key="agent.id" :value="agent.id">
              {{ agent.name }} ({{ agent.model }})
            </option>
          </select>
        </div>

        <button 
          class="new-session-button"
          @click="handleCreateSession"
          aria-label="Create new session"
        >
          <span class="button-icon">➕</span>
          New Session
        </button>
      </div>

      <div class="session-browser-body">
        <div v-if="chatStore.isLoadingSessions" class="loading-state">
          Loading sessions...
        </div>

        <div v-else-if="filteredSessions.length === 0" class="empty-state">
          <p v-if="searchQuery || selectedAgentFilter !== 'all'">
            No sessions match your search or filter.
          </p>
          <p v-else>
            No sessions yet. Create your first session to get started!
          </p>
        </div>

        <div v-else class="session-list">
          <div
            v-for="session in filteredSessions"
            :key="session.id"
            class="session-card"
            :class="{ active: session.id === chatStore.currentSessionId }"
            @click="handleSelectSession(session.id)"
            role="button"
            tabindex="0"
            :aria-selected="session.id === chatStore.currentSessionId"
            @keydown.enter="handleSelectSession(session.id)"
          >
            <div class="session-card-header">
              <h3 class="session-title">
                {{ session.title || `Session ${session.id.slice(0, 8)}...` }}
              </h3>
              <!--
                Pulsing green dot + "Live" text — matches ChildSessionsPanel
                and SessionSwitcher per-row indicators. The redundant text
                label is the colour-blind-safe fallback.
              -->
              <span
                v-if="isSessionStreaming(session.id)"
                class="streaming-badge"
                :data-testid="`session-browser-streaming-${session.id}`"
                aria-label="Currently streaming"
              >
                <span class="streaming-dot" aria-hidden="true">●</span>
                <span class="streaming-label">Live</span>
              </span>
              <span v-if="session.id === chatStore.currentSessionId" class="current-badge">
                Current
              </span>
            </div>

            <div class="session-card-meta">
              <div class="meta-item">
                <span class="meta-icon">🤖</span>
                <span class="meta-text">{{ getAgentName(session.agentId) }}</span>
              </div>
              <div class="meta-item">
                <span class="meta-icon">📝</span>
                <span class="meta-text">{{ session.messageCount }} messages</span>
              </div>
              <div class="meta-item">
                <span class="meta-icon">🕐</span>
                <span class="meta-text">{{ formatRelativeTime(session.updatedAt) }}</span>
              </div>
            </div>

            <div v-if="getAgentModel(session.agentId)" class="session-card-footer">
              <span class="model-info">
                {{ getAgentModel(session.agentId) }}
                <span v-if="getAgentProvider(session.agentId)" class="provider-info">
                  via {{ getAgentProvider(session.agentId) }}
                </span>
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.session-browser {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
}

.session-browser-overlay {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  backdrop-filter: blur(2px);
}

.session-browser-content {
  position: relative;
  width: 90%;
  max-width: 700px;
  max-height: 80vh;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.4);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.session-browser-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1rem 1.5rem;
  border-bottom: 1px solid var(--border);
  background: var(--bg-elevated);
}

.session-browser-header h2 {
  margin: 0;
  font-size: 1.25rem;
  font-weight: 600;
  color: var(--text-primary);
}

.close-button {
  background: transparent;
  border: none;
  color: var(--text-muted);
  font-size: 1.25rem;
  cursor: pointer;
  padding: 0.25rem 0.5rem;
  border-radius: var(--radius);
  transition: all 0.15s;
  display: flex;
  align-items: center;
  justify-content: center;
}

.close-button:hover {
  background: var(--bg-secondary);
  color: var(--text-primary);
}

.session-browser-controls {
  display: flex;
  gap: 0.75rem;
  padding: 1rem 1.5rem;
  border-bottom: 1px solid var(--border);
  background: var(--bg-secondary);
  flex-wrap: wrap;
}

.search-container {
  position: relative;
  flex: 1;
  min-width: 200px;
}

.search-input {
  width: 100%;
  padding: 0.5rem 2.5rem 0.5rem 0.75rem;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  color: var(--text-primary);
  font-size: 0.9rem;
  transition: border-color 0.15s;
}

.search-input:focus {
  outline: none;
  border-color: var(--accent);
}

.search-icon {
  position: absolute;
  right: 0.75rem;
  top: 50%;
  transform: translateY(-50%);
  color: var(--text-muted);
  font-size: 0.9rem;
  pointer-events: none;
}

.filter-container {
  min-width: 150px;
}

.agent-filter {
  width: 100%;
  padding: 0.5rem 0.75rem;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  color: var(--text-primary);
  font-size: 0.9rem;
  cursor: pointer;
  transition: border-color 0.15s;
}

.agent-filter:focus {
  outline: none;
  border-color: var(--accent);
}

.new-session-button {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 1rem;
  background: var(--accent);
  color: white;
  border: none;
  border-radius: var(--radius);
  font-size: 0.9rem;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.15s;
  white-space: nowrap;
}

.new-session-button:hover {
  filter: brightness(1.1);
}

.button-icon {
  font-size: 1rem;
}

.session-browser-body {
  flex: 1;
  overflow-y: auto;
  padding: 1rem 1.5rem;
}

.loading-state,
.empty-state {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 3rem 1rem;
  color: var(--text-muted);
  text-align: center;
}

.session-list {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.session-card {
  padding: 1rem;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  cursor: pointer;
  transition: all 0.15s;
}

.session-card:hover {
  border-color: var(--accent);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.15);
  transform: translateY(-1px);
}

.session-card.active {
  border-color: var(--accent);
  background: var(--accent-bg);
}

.session-card:focus {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

.session-card-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 0.5rem;
  margin-bottom: 0.75rem;
}

.session-title {
  margin: 0;
  font-size: 1rem;
  font-weight: 500;
  color: var(--text-primary);
  line-height: 1.4;
}

.current-badge {
  flex-shrink: 0;
  padding: 0.125rem 0.5rem;
  background: var(--accent);
  color: white;
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  border-radius: 999px;
}

/*
 * UX consolidation (May 2026) — streaming indicator. Pulsing green dot +
 * "Live" text label so the affordance is robust to green
 * colour-blindness. Vocabulary matches ChildSessionsPanel.panel-live and
 * SessionSwitcher.option-streaming-dot.
 */
.streaming-badge {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  flex-shrink: 0;
  padding: 0.125rem 0.5rem;
  background: rgba(74, 222, 128, 0.12);
  color: var(--accent-success, #4ade80);
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  border-radius: 999px;
}

.streaming-dot {
  font-size: 0.55rem;
  animation: session-browser-pulse 1.5s ease-in-out infinite;
}

@keyframes session-browser-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}

.session-card-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem 1rem;
  margin-bottom: 0.5rem;
}

.meta-item {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  font-size: 0.8rem;
  color: var(--text-secondary);
}

.meta-icon {
  font-size: 0.85rem;
}

.session-card-footer {
  margin-top: 0.5rem;
  padding-top: 0.5rem;
  border-top: 1px solid var(--border);
}

.model-info {
  font-size: 0.75rem;
  color: var(--text-muted);
}

.provider-info {
  color: var(--text-muted);
  opacity: 0.75;
}
</style>
