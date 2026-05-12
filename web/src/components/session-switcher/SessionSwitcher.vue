<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import { showToast } from '@/composables/useToast'
import Icon from '@/components/common/Icon.vue'

defineOptions({ name: 'SessionSwitcher' })

const chatStore = useChatStore()
const isOpen = ref(false)
// QW-11 — Inline-confirm delete UX. Same vocabulary as SessionBrowser: a
// single row id at a time enters confirm state; clicking the trash icon on
// a different row implicitly cancels the previous.
const pendingDeleteId = ref<string | null>(null)

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

// QW-11 — Read from orderedSessions (streaming-first, then updatedAt desc)
// rather than the raw state.sessions array so the switcher matches the
// canonical ordering used everywhere else. Children are still filtered out:
// the switcher surfaces root sessions only; children are reachable via the
// ChildSessionsPanel under the chat thread.
const parentSessions = computed(() =>
  chatStore.orderedSessions.filter((session) => !session.parentId),
)

// UX consolidation (May 2026) — per-session activity surface. The chat
// header only surfaces the *current* session's streaming state; without
// these helpers a parallel session running in the background was invisible
// from the global navigation. Each row in the dropdown reads its own slot
// via chatStore.streamingFor, and the trigger button lights up when ANY
// non-current session is live so the user gets a peripheral cue even
// before opening the dropdown.
function isSessionStreaming(sessionId: string): boolean {
  return chatStore.streamingFor(sessionId).isStreaming
}

const hasBackgroundActivity = computed(() =>
  chatStore.sessions.some(
    (session) => session.id !== chatStore.currentSessionId && isSessionStreaming(session.id),
  ),
)

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

// QW-11 — Delete handlers. All three are .stop on the row's @click so the
// confirm/cancel toggles don't accidentally also pick the session.
function handleDeleteClick(sessionId: string, event?: Event): void {
  event?.stopPropagation()
  pendingDeleteId.value = sessionId
}

function handleCancelDelete(event?: Event): void {
  event?.stopPropagation()
  pendingDeleteId.value = null
}

async function handleConfirmDelete(sessionId: string, event?: Event): Promise<void> {
  event?.stopPropagation()
  try {
    await chatStore.deleteSession(sessionId)
    pendingDeleteId.value = null
  } catch (err) {
    pendingDeleteId.value = null
    const message = err instanceof Error ? err.message : 'Failed to delete session'
    showToast({ message, variant: 'error' })
  }
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
      <span class="session-icon"><Icon name="message" :size="14" /></span>
      <span class="session-name">{{ currentSessionDisplay }}</span>
      <!--
        Background-activity hint — a small dot in a distinct hue from the
        per-row streaming dot (orange, not green) so the user can
        instantly tell "something else is running" without confusing it
        for the active session. Hidden when only the current session is
        live; that case is already conveyed by the chat header's existing
        agent-activity indicator.
      -->
      <span
        v-if="hasBackgroundActivity"
        class="background-activity-dot"
        data-testid="session-switcher-background-activity"
        aria-label="Another session is currently active"
        title="Another session is currently active"
      />
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
        <span class="option-icon"><Icon name="plus" :size="14" /></span>
        <span class="option-name">New Session</span>
      </li>
      <li v-if="hasSessions" class="session-divider">
        Recent Sessions
      </li>
      <li
        v-for="session in parentSessions"
        :key="session.id"
        class="session-option"
        :class="{ active: session.id === chatStore.currentSessionId, 'is-streaming': isSessionStreaming(session.id) }"
        @click="selectSession(session.id)"
        role="option"
        :aria-selected="session.id === chatStore.currentSessionId"
      >
        <div class="option-title-row">
          <span class="option-title">{{ session.title || session.id.slice(0, 8) }}</span>
          <!--
            Per-row pulsing green dot — same vocabulary as
            ChildSessionsPanel.panel-live so users learn one symbol for
            "this session is streaming" across every place a session is
            listed.
          -->
          <span
            v-if="isSessionStreaming(session.id)"
            class="option-streaming-dot"
            :data-testid="`session-switcher-streaming-${session.id}`"
            aria-label="Currently streaming"
          >●</span>
          <!--
            QW-11 — Per-row delete. Hover-revealed trash icon + inline
            confirm strip, same vocabulary as SessionBrowser. The buttons
            are .stop so toggling them does not also pick the session.
          -->
          <div
            v-if="pendingDeleteId === session.id"
            class="option-delete-confirm"
            :data-testid="`session-switcher-delete-confirm-${session.id}`"
            @click.stop
          >
            <button
              type="button"
              class="option-delete-cancel"
              :data-testid="`session-switcher-cancel-delete-${session.id}`"
              @click.stop="handleCancelDelete($event)"
            >
              Cancel
            </button>
            <button
              type="button"
              class="option-delete-confirm-btn"
              :data-testid="`session-switcher-confirm-delete-${session.id}`"
              @click.stop="handleConfirmDelete(session.id, $event)"
            >
              Delete
            </button>
          </div>
          <button
            v-else
            type="button"
            class="option-delete-button"
            :data-testid="`session-switcher-delete-${session.id}`"
            :aria-label="`Delete session ${session.title || session.id.slice(0, 8)}`"
            @click.stop="handleDeleteClick(session.id, $event)"
          >
            <Icon name="trash" :size="14" aria-label="Delete" />
          </button>
        </div>
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

/*
 * UX consolidation (May 2026) — background-activity dot. Orange (rather
 * than the green used for in-row streaming dots) so the user can
 * distinguish "another session is doing something" from "this row is the
 * one that's streaming". Pulses on the same cadence as the in-row dot to
 * read as the same family of affordance.
 */
.background-activity-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--accent-warning, #f59e0b);
  box-shadow: 0 0 6px rgba(245, 158, 11, 0.6);
  animation: session-switcher-pulse 1.5s ease-in-out infinite;
  flex-shrink: 0;
}

@keyframes session-switcher-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
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

.option-title-row {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}

/*
 * Per-row green streaming dot — matches ChildSessionsPanel's pattern so
 * the "this session is live" affordance reads the same in every list of
 * sessions.
 */
.option-streaming-dot {
  color: var(--accent-success, #4ade80);
  font-size: 0.6rem;
  animation: session-switcher-pulse 1.5s ease-in-out infinite;
  flex-shrink: 0;
}

/*
 * QW-11 — Per-row delete UI. Mirrors SessionBrowser's vocabulary so users
 * learn one affordance for the whole "session list" family. Trash icon is
 * hover-revealed; confirm strip swaps in destructive red for the Delete
 * action.
 */
.option-delete-button {
  margin-left: auto;
  flex-shrink: 0;
  background: transparent;
  border: 1px solid transparent;
  border-radius: var(--radius);
  padding: 0.05rem 0.35rem;
  font-size: 0.75rem;
  cursor: pointer;
  color: var(--text-muted);
  opacity: 0;
  transition: opacity 0.15s, background 0.15s, border-color 0.15s;
}

.session-option:hover .option-delete-button,
.option-delete-button:focus-visible {
  opacity: 1;
}

.option-delete-button:hover {
  background: rgba(248, 113, 113, 0.15);
  border-color: rgba(248, 113, 113, 0.4);
  color: var(--accent-danger, #f87171);
}

.option-delete-confirm {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  margin-left: auto;
  flex-shrink: 0;
}

.option-delete-cancel,
.option-delete-confirm-btn {
  font-size: 0.7rem;
  font-weight: 600;
  border-radius: var(--radius);
  border: 1px solid var(--border);
  padding: 0.05rem 0.4rem;
  cursor: pointer;
}

.option-delete-cancel {
  background: transparent;
  color: var(--text-muted);
}

.option-delete-cancel:hover {
  background: var(--bg-elevated);
  color: var(--text-primary);
}

.option-delete-confirm-btn {
  background: var(--accent-danger, #f87171);
  border-color: var(--accent-danger, #f87171);
  color: white;
}

.option-delete-confirm-btn:hover {
  filter: brightness(1.1);
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
