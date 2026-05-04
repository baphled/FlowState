<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import { useSettingsStore } from '@/stores/settingsStore'
import { useSwarmStore } from '@/stores/swarmStore'
import { resolveAgentName, collapseToolPairs, groupContextTools } from '@/views/chatViewHelpers'
import type { GroupedMessageEntry } from '@/views/chatViewHelpers'
import type { Message } from '@/types'
import MessageBubble from '@/components/chat/MessageBubble.vue'
import MessageInput from '@/components/chat/MessageInput.vue'
import TodoListPanel from '@/components/chat/TodoListPanel.vue'
import DelegationStrip from '@/components/chat/DelegationStrip.vue'
import AgentPicker from '@/components/agent-picker/AgentPicker.vue'
import ModelPicker from '@/components/model-picker/ModelPicker.vue'
import ContextToolGroup from '@/components/tools/ContextToolGroup.vue'
import { registerTools } from '@/tools/registerTools'
import { installSessionHierarchyNav } from '@/composables/useSessionHierarchyNav'

defineOptions({ name: 'ChatView' })

const chatStore = useChatStore()
const settingsStore = useSettingsStore()
const swarmStore = useSwarmStore()

const shellRef = ref<HTMLElement | null>(null)
const messagePaneRef = ref<HTMLElement | null>(null)
const isDraggingSidebar = ref(false)
const showSwarmPane = computed(() => settingsStore.swarmPaneVisible)
const currentSessionSummary = computed(() =>
  chatStore.sessions.find((session) => session.id === chatStore.currentSessionId) ?? null,
)
// Child sessions render the toolbar in read-only mode: the agent and model
// pickers display the values that were used by the delegated agent, but
// clicking them does nothing (changing them mid-thread is not a supported
// flow). The toolbar position is unchanged so the layout doesn't shift on
// navigation. NavBar additionally hides itself in child sessions to remove
// the chat/swarm/session-selection chrome.
const isChildSession = computed(() => Boolean(currentSessionSummary.value?.parentId))

const groupedMessages = computed<GroupedMessageEntry[]>(() =>
  groupContextTools(collapseToolPairs(chatStore.messages)),
)
const hasSidebar = computed(() => settingsStore.swarmPaneVisible)
const lastMessage = computed(() => {
  const messages = chatStore.messages
  return messages.length > 0 ? messages[messages.length - 1] : null
})
const userScrolledUp = ref(false)

async function scrollMessagePaneToBottom(): Promise<void> {
  if (userScrolledUp.value) {
    return
  }

  await nextTick()
  const el = messagePaneRef.value
  if (!el) {
    return
  }

  el.scrollTo({
    top: el.scrollHeight,
    behavior: 'smooth',
  })
}

function onMessagePaneScroll(): void {
  const el = messagePaneRef.value
  if (!el) {
    return
  }

  const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 100
  userScrolledUp.value = !atBottom
}

function agentNameFor(message: Message): string | undefined {
  return resolveAgentName(message, chatStore.availableAgentDetails, chatStore.agentId)
}

function clampSidebarWidth(width: number, containerWidth = 0): number {
  const minWidth = 280
  const maxWidth = 520
  const usableMax = containerWidth > 0 ? Math.min(maxWidth, containerWidth - 360) : maxWidth
  return Math.min(Math.max(width, minWidth), Math.max(minWidth, usableMax))
}

function updateSidebarWidthFromPointer(clientX: number): void {
  const rect = shellRef.value?.getBoundingClientRect()
  if (!rect) {
    return
  }

  const containerWidth = rect.width
  const width = rect.right - clientX
  settingsStore.setChatSidebarWidth(clampSidebarWidth(width, containerWidth))
}

function handleResizeMove(event: MouseEvent): void {
  if (!isDraggingSidebar.value) {
    return
  }

  updateSidebarWidthFromPointer(event.clientX)
}

function stopDragging(): void {
  if (!isDraggingSidebar.value) {
    return
  }

  isDraggingSidebar.value = false
  window.removeEventListener('mousemove', handleResizeMove)
  window.removeEventListener('mouseup', stopDragging)
}

function startDraggingSidebar(event: MouseEvent): void {
  event.preventDefault()
  isDraggingSidebar.value = true
  window.addEventListener('mousemove', handleResizeMove)
  window.addEventListener('mouseup', stopDragging)
}

function toggleSwarmPane(): void {
  settingsStore.setSwarmPaneVisible(false)
}

function showSwarmPaneAgain(): void {
  settingsStore.setSwarmPaneVisible(true)
}

watch(
  () => chatStore.messages.length,
  async () => {
    await scrollMessagePaneToBottom()
  },
)

watch(
  () => lastMessage.value?.content?.length,
  async () => {
    await scrollMessagePaneToBottom()
  },
)

watch(
  () => chatStore.currentSessionId,
  async () => {
    await nextTick()
    userScrolledUp.value = false
    await scrollMessagePaneToBottom()
  },
)

let teardownHierarchyNav: (() => void) | null = null

onMounted(async () => {
  registerTools()
  teardownHierarchyNav = installSessionHierarchyNav()
  await chatStore.restoreStateFromBackend()
  await scrollMessagePaneToBottom()
  void swarmStore.connect()
})

onBeforeUnmount(() => {
  stopDragging()
  swarmStore.disconnect()
  if (teardownHierarchyNav) {
    teardownHierarchyNav()
    teardownHierarchyNav = null
  }
})
</script>

<template>
  <div class="chat-view" data-testid="chat-view" ref="shellRef">
    <div class="chat-main">
      <div class="swarm-controls">
        <button v-if="showSwarmPane" class="swarm-toggle-btn" data-testid="toggle-swarm-btn" @click="toggleSwarmPane">
          Hide swarm pane
        </button>
        <button v-else class="swarm-toggle-btn" data-testid="show-swarm-btn" @click="showSwarmPaneAgain">
          Show swarm pane
        </button>
      </div>

      <section ref="messagePaneRef" class="message-pane" data-testid="chat-message-pane" @scroll="onMessagePaneScroll">
        <div v-if="groupedMessages.length === 0" class="empty-state" data-testid="chat-empty-state">
          Start a conversation with the selected agent.
        </div>
        <div v-else class="message-list" data-testid="message-list">
          <template v-for="(entry, index) in groupedMessages" :key="entry.type === 'message' ? `${entry.message.role}-${index}` : `context-group-${index}`">
            <MessageBubble
              v-if="entry.type === 'message'"
              :message="entry.message"
              :agent-name="agentNameFor(entry.message)"
            />
            <ContextToolGroup
              v-else-if="entry.type === 'context-group'"
              :messages="entry.messages"
              :tool-counts="entry.toolCounts"
            />
          </template>
        </div>
      </section>

      <DelegationStrip />

      <!--
        The toolbar is rendered in the same DOM position for both parent and
        child sessions so the bar layout doesn't shift on navigation. In a
        child session the agent + model pickers go into a read-only display
        mode (label only, no click-to-open) and a provider label is added so
        the user can see *which* model + provider the delegated agent used.
      -->
      <div class="input-selector-bar" data-testid="input-selector-bar">
        <AgentPicker :readonly="isChildSession" />
        <span
          v-if="chatStore.currentProviderId"
          class="provider-label"
          data-testid="toolbar-provider-label"
        >
          {{ chatStore.currentProviderId }}
        </span>
        <ModelPicker :readonly="isChildSession" />
      </div>

      <div
        v-if="chatStore.isLoading && !chatStore.isStreaming"
        class="loading-pulse"
        data-testid="loading-pulse"
        aria-hidden="true"
      />

      <MessageInput />
    </div>

    <aside v-if="hasSidebar && showSwarmPane" class="chat-sidebar" :style="{ width: `${settingsStore.chatSidebarWidth}px` }" data-testid="swarm-pane">
      <div class="sidebar-panels">
        <TodoListPanel class="sidebar-panel" />
      </div>

      <button
        class="sidebar-resize-handle"
        data-testid="chat-sidebar-resize-handle"
        type="button"
        aria-label="Resize chat sidebar"
        @mousedown="startDraggingSidebar"
      >
        <span class="resize-grip" />
      </button>
    </aside>
  </div>
</template>

<style scoped>
.chat-view {
  display: flex;
  flex: 1;
  min-height: 0;
  overflow: hidden;
  background: var(--bg-primary);
}

.chat-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.swarm-controls {
  display: flex;
  justify-content: flex-end;
  padding: 0.5rem 1rem 0;
  flex-shrink: 0;
}

.swarm-toggle-btn {
  padding: 0.25rem 0.55rem;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--bg-elevated);
  color: var(--text-muted);
  font-size: 0.75rem;
  cursor: pointer;
}

.message-pane {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.message-list {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.empty-state {
  height: 100%;
  display: grid;
  place-items: center;
  color: var(--text-muted);
  font-size: 0.95rem;
}

.input-selector-bar {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.3rem 1rem;
  background: var(--bg-secondary);
  border-top: 1px solid var(--border);
  flex-shrink: 0;
}

.provider-label {
  font-size: 0.75rem;
  color: var(--text-muted);
  letter-spacing: 0.02em;
  user-select: none;
  white-space: nowrap;
}

.loading-pulse {
  height: 2px;
  flex-shrink: 0;
  background: linear-gradient(90deg, transparent 0%, var(--accent) 50%, transparent 100%);
  background-size: 200% 100%;
  animation: pulse-shimmer 1.5s ease-in-out infinite;
}

@keyframes pulse-shimmer {
  0% {
    background-position: 200% 0;
  }
  100% {
    background-position: -200% 0;
  }
}

.chat-sidebar {
  position: relative;
  flex-shrink: 0;
  min-width: 280px;
  max-width: 520px;
  border-left: 1px solid var(--border);
  background: var(--bg-secondary);
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.sidebar-panels {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.sidebar-panel {
  flex: 1 1 0;
  min-height: 0;
}

.sidebar-resize-handle {
  position: absolute;
  top: 0;
  left: -4px;
  width: 8px;
  height: 100%;
  padding: 0;
  border: 0;
  background: transparent;
  cursor: col-resize;
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 2;
}

.sidebar-resize-handle:hover .resize-grip,
.sidebar-resize-handle:active .resize-grip {
  background: var(--accent);
}

.resize-grip {
  width: 2px;
  height: 48px;
  border-radius: 999px;
  background: var(--border);
  box-shadow: -3px 0 0 var(--border), 3px 0 0 var(--border);
}

.chat-sidebar,
.sidebar-panels,
.message-pane {
  min-height: 0;
}
</style>
