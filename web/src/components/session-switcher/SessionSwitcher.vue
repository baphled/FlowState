<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useChatStore } from '@/stores/chatStore'

defineOptions({ name: 'SessionSwitcher' })

const chatStore = useChatStore()
const isOpen = ref(false)

const currentSession = computed(() =>
  chatStore.sessions.find((session) => session.id === chatStore.currentSessionId)
)

const currentSessionDisplay = computed(() => {
  if (currentSession.value?.title) {
    return currentSession.value.title
  }

  if (chatStore.currentSessionId) {
    return `Session: ${chatStore.currentSessionId.slice(0, 8)}...`
  }
  return 'New Session'
})

const hasSessions = computed(() => chatStore.sessions.length > 0)

async function createNewSession(): Promise<void> {
  await chatStore.newSession()
  chatStore.clearMessages()
  isOpen.value = false
}

function toggleDropdown(): void {
  if (!isOpen.value) {
    void chatStore.loadSessions()
  }
  isOpen.value = !isOpen.value
}

function closeDropdown(): void {
  isOpen.value = false
}

onMounted(() => {
  void chatStore.loadSessions()
})

async function selectSession(sessionId: string): Promise<void> {
  chatStore.currentSessionId = sessionId
  await chatStore.loadSessionMessages(sessionId)
  isOpen.value = false
}
</script>

<template>
  <div class="session-switcher" data-testid="session-switcher">
    <button
      class="session-switcher-trigger"
      @click="toggleDropdown"
      aria-haspopup="listbox"
      :aria-expanded="isOpen"
    >
      <span class="session-icon">💬</span>
      <span class="session-name">{{ currentSessionDisplay }}</span>
      <span class="dropdown-arrow" :class="{ open: isOpen }">▾</span>
    </button>
    <ul
      v-if="isOpen"
      class="session-dropdown"
      role="listbox"
    >
      <li
        class="session-option new-session"
        @click="createNewSession"
        role="option"
      >
        <span class="option-icon">➕</span>
        <span class="option-name">New Session</span>
      </li>
      <li v-if="hasSessions" class="session-divider">
        Recent Sessions
      </li>
      <li
        v-for="session in chatStore.sessions"
        :key="session.id"
        class="session-option"
        :class="{ active: session.id === chatStore.currentSessionId }"
        @click="selectSession(session.id)"
        role="option"
        :aria-selected="session.id === chatStore.currentSessionId"
      >
        <span class="option-title">{{ session.title || session.id.slice(0, 8) }}</span>
        <span class="option-meta">{{ session.messageCount }} messages</span>
      </li>
      <li v-if="chatStore.isLoadingSessions" class="session-loading">
        Loading sessions...
      </li>
    </ul>
    <div v-if="isOpen" class="dropdown-backdrop" @click="closeDropdown" />
  </div>
</template>

<style scoped>
.session-switcher {
  position: relative;
  display: inline-flex;
}

.session-switcher-trigger {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.25rem 0.6rem;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  cursor: pointer;
  font-size: 0.85rem;
  color: var(--text-primary);
  transition: background 0.15s, border-color 0.15s;
}

.session-switcher-trigger:hover {
  border-color: var(--accent);
}

.session-icon {
  font-size: 0.9rem;
}

.session-name {
  font-weight: 500;
}

.dropdown-arrow {
  font-size: 0.7rem;
  color: var(--text-muted);
  transition: transform 0.15s;
}

.dropdown-arrow.open {
  transform: rotate(180deg);
}

.session-dropdown {
  position: absolute;
  top: 100%;
  left: 0;
  margin-top: 0.25rem;
  min-width: 200px;
  max-height: 300px;
  overflow-y: auto;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
  z-index: 100;
  list-style: none;
  padding: 0.25rem 0;
}

.session-option {
  padding: 0.5rem 0.75rem;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  transition: background 0.1s;
}

.session-option:hover {
  background: var(--bg-secondary);
}

.session-option.active {
  background: var(--accent-bg);
  color: var(--accent);
}

.session-option.new-session {
  flex-direction: row;
  align-items: center;
  gap: 0.5rem;
  border-bottom: 1px solid var(--border);
  margin-bottom: 0.25rem;
  padding-bottom: 0.75rem;
}

.option-icon {
  font-size: 0.9rem;
}

.option-name,
.option-title {
  font-weight: 500;
  font-size: 0.9rem;
}

.option-meta,
.option-desc {
  font-size: 0.75rem;
  color: var(--text-muted);
}

.session-divider {
  font-size: 0.7rem;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  padding: 0.4rem 0.75rem 0.25rem;
  border-top: 1px solid var(--border);
  margin-top: 0.25rem;
}

.session-loading {
  font-size: 0.8rem;
  color: var(--text-muted);
  padding: 0.5rem 0.75rem;
  text-align: center;
}

.dropdown-backdrop {
  position: fixed;
  inset: 0;
  z-index: 99;
}
</style>
