<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import { useSettingsStore } from '@/stores/settingsStore'
import { useSwarmStore } from '@/stores/swarmStore'
import { resolveAgentName, collapseToolPairs } from '@/views/chatViewHelpers'
import type { Message } from '@/types'
import MessageBubble from '@/components/chat/MessageBubble.vue'
import MessageInput from '@/components/chat/MessageInput.vue'
import AgentPicker from '@/components/agent-picker/AgentPicker.vue'
import ModelPicker from '@/components/model-picker/ModelPicker.vue'
import ToolCallPanel from '@/components/tool-calls/ToolCallPanel.vue'
import DelegationPanel from '@/components/swarm/DelegationPanel.vue'
import PlanPanel from '@/components/swarm/PlanPanel.vue'

defineOptions({ name: 'ChatView' })

const chatStore = useChatStore()
const settingsStore = useSettingsStore()
const swarmStore = useSwarmStore()

const shellRef = ref<HTMLElement | null>(null)
const isDraggingSidebar = ref(false)
const showToolPanel = ref(true)
const showDelegationPanel = ref(true)
const showPlanPanel = ref(true)
const showSwarmPane = computed(() => settingsStore.swarmPaneVisible)

const messages = computed(() => collapseToolPairs(chatStore.messages))
const hasSidebar = computed(() => settingsStore.swarmPaneVisible)

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

function toggleToolPanel(): void {
  showToolPanel.value = !showToolPanel.value
}

function toggleDelegationPanel(): void {
  showDelegationPanel.value = !showDelegationPanel.value
}

function togglePlanPanel(): void {
  showPlanPanel.value = !showPlanPanel.value
}

function toggleSwarmPane(): void {
  settingsStore.setSwarmPaneVisible(false)
}

function showSwarmPaneAgain(): void {
  settingsStore.setSwarmPaneVisible(true)
}

onMounted(() => {
  void chatStore.restoreStateFromBackend()
  void swarmStore.connect()
})

onBeforeUnmount(() => {
  stopDragging()
  swarmStore.disconnect()
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

      <section class="message-pane" data-testid="chat-message-pane">
        <div v-if="messages.length === 0" class="empty-state" data-testid="chat-empty-state">
          Start a conversation with the selected agent.
        </div>
        <div v-else class="message-list" data-testid="message-list">
          <MessageBubble
            v-for="(message, index) in messages"
            :key="`${message.role}-${index}`"
            :message="message"
            :agent-name="agentNameFor(message)"
          />
        </div>
      </section>

      <div class="input-selector-bar" data-testid="input-selector-bar">
        <AgentPicker />
        <ModelPicker />
      </div>

      <div v-if="chatStore.isLoading" class="loading-pulse" data-testid="loading-pulse" aria-hidden="true" />

      <MessageInput />
    </div>

    <aside v-if="hasSidebar && showSwarmPane" class="chat-sidebar" :style="{ width: `${settingsStore.chatSidebarWidth}px` }" data-testid="swarm-pane">
      <div class="sidebar-toolbar" data-testid="sidebar-toolbar">
        <button class="sidebar-toggle" :class="{ active: showToolPanel }" data-testid="toggle-tool-panel" @click="toggleToolPanel">
          Tools
        </button>
        <button class="sidebar-toggle" :class="{ active: showDelegationPanel }" data-testid="toggle-delegation-panel" @click="toggleDelegationPanel">
          Delegation
        </button>
        <button class="sidebar-toggle" :class="{ active: showPlanPanel }" data-testid="toggle-plan-panel" @click="togglePlanPanel">
          Plan
        </button>
      </div>

      <div class="sidebar-panels">
        <ToolCallPanel v-if="showToolPanel" class="sidebar-panel" />
        <DelegationPanel v-if="showDelegationPanel" class="sidebar-panel" @close="showDelegationPanel = false" />
        <PlanPanel v-if="showPlanPanel" class="sidebar-panel" @close="showPlanPanel = false" />
        <p v-if="!showToolPanel && !showDelegationPanel && !showPlanPanel" class="sidebar-empty" data-testid="sidebar-empty">
          All sidebar panels are hidden.
        </p>
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
  gap: 0.5rem;
  padding: 0.25rem 1rem;
  background: var(--bg-secondary);
  border-top: 1px solid var(--border);
  flex-shrink: 0;
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

.sidebar-toolbar {
  display: flex;
  gap: 0.4rem;
  padding: 0.5rem;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}

.sidebar-toggle {
  padding: 0.25rem 0.55rem;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--bg-elevated);
  color: var(--text-muted);
  font-size: 0.75rem;
  cursor: pointer;
}

.sidebar-toggle.active {
  color: var(--text-primary);
  border-color: var(--accent);
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

.sidebar-empty {
  margin: 0;
  padding: 1rem;
  color: var(--text-muted);
  font-size: 0.85rem;
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
