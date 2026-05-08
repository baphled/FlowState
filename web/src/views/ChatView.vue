<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import { useSettingsStore } from '@/stores/settingsStore'
import { useSwarmStore } from '@/stores/swarmStore'
import { resolveAgentName, collapseToolPairs, groupContextTools } from '@/views/chatViewHelpers'
import type { GroupedMessageEntry } from '@/views/chatViewHelpers'
import type { Message } from '@/types'
import ContextUsageChip from '@/components/chat/ContextUsageChip.vue'
import CriticalErrorBanner from '@/components/chat/CriticalErrorBanner.vue'
import MessageBubble from '@/components/chat/MessageBubble.vue'
import MessageInput from '@/components/chat/MessageInput.vue'
import QueuedPromptStrip from '@/components/chat/QueuedPromptStrip.vue'
import TodoListPanel from '@/components/chat/TodoListPanel.vue'
import DelegationStrip from '@/components/chat/DelegationStrip.vue'
import AgentPicker from '@/components/agent-picker/AgentPicker.vue'
import ModelPicker from '@/components/model-picker/ModelPicker.vue'
import ContextToolGroup from '@/components/tools/ContextToolGroup.vue'
import { registerTools } from '@/tools/registerTools'
import { installSessionHierarchyNav } from '@/composables/useSessionHierarchyNav'
import { showToast } from '@/composables/useToast'

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
const lastMessage = computed(() => {
  const messages = chatStore.messages
  return messages.length > 0 ? messages[messages.length - 1] : null
})
const userScrolledUp = ref(false)
// lastScrollHeight tracks the message pane's scrollHeight at the time of
// the last observed scroll so onMessagePaneScroll can distinguish a
// content-reflow scroll (height grew while we were already at the bottom)
// from a deliberate user scroll. Without this distinction streaming
// content sticky-set userScrolledUp=true on the very first chunk because
// scrollTop / scrollHeight diverged briefly while the new content
// rendered. See Principal F10.
let lastScrollHeight = 0
let lastScrollTop = 0

function scrollMessagePaneToBottom(behavior: ScrollBehavior = 'smooth'): void {
  if (userScrolledUp.value) {
    return
  }
  const el = messagePaneRef.value
  if (!el) {
    return
  }
  el.scrollTo({ top: el.scrollHeight, behavior })
  // Sync the watermark so the next `scroll` event from the synthetic
  // scrollTo doesn't get mis-classified as a user scroll.
  lastScrollHeight = el.scrollHeight
  lastScrollTop = el.scrollTop
}

let scrollRaf: number | null = null
function scheduleInstantScroll(): void {
  if (scrollRaf !== null) {
    cancelAnimationFrame(scrollRaf)
  }
  scrollRaf = requestAnimationFrame(() => {
    scrollMessagePaneToBottom('instant')
    scrollRaf = null
  })
}

function onMessagePaneScroll(): void {
  const el = messagePaneRef.value
  if (!el) {
    return
  }

  const heightDelta = el.scrollHeight - lastScrollHeight
  const topDelta = el.scrollTop - lastScrollTop

  // Reflow detection: when the content grows by N pixels and scrollTop
  // moves by ≤N pixels in the SAME direction, the browser is just
  // re-anchoring the viewport — no user input. The 4px tolerance covers
  // sub-pixel rounding from layout and the smooth-scroll animation
  // adjusting position by a small amount during a height change.
  const isContentReflow = heightDelta > 0 && Math.abs(topDelta) <= heightDelta + 4

  if (!isContentReflow) {
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 100
    userScrolledUp.value = !atBottom
  }

  lastScrollHeight = el.scrollHeight
  lastScrollTop = el.scrollTop
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
  settingsStore.toggleSwarmPane()
}

function showSwarmPaneAgain(): void {
  settingsStore.setSwarmPaneVisible(true)
}

watch(
  () => chatStore.messages.length,
  async () => {
    await nextTick()
    scrollMessagePaneToBottom('smooth')
  },
)

// Auto-scroll watcher: track every shape of the last message that can change
// without the message-list length changing. Pre-fix only `content.length`
// was tracked — delegation/tool in-place mutations (toolCalls increments,
// lastTool replacements, targetAgent assignments) updated the bubble in
// place and the progress card scrolled out of view as new chunks arrived
// (compounding bug C-8 from the PR-2 plan). Cheap derived-shape watcher
// over a small object — Vue diffs by value-equality so unrelated stores
// don't fire it.
watch(
  () => {
    const m = lastMessage.value
    if (!m) return null
    return {
      contentLength: m.content?.length ?? 0,
      // toolCalls is a count (number), not an array — track its value.
      toolCalls: m.toolCalls ?? 0,
      lastTool: m.lastTool ?? '',
      targetAgent: m.targetAgent ?? '',
      status: m.status ?? '',
    }
  },
  scheduleInstantScroll,
  { deep: true },
)

watch(
  () => chatStore.isLoading,
  (loading) => {
    if (loading) {
      userScrolledUp.value = false
    }
  },
)

watch(
  () => chatStore.currentSessionId,
  async () => {
    await nextTick()
    userScrolledUp.value = false
    scrollMessagePaneToBottom('smooth')
  },
)

let teardownHierarchyNav: (() => void) | null = null

onMounted(async () => {
  registerTools()
  teardownHierarchyNav = installSessionHierarchyNav()
  // Principal F7: a network blip during initial hydration must surface a
  // toast and assign chatStore.error rather than leave the user staring at
  // a blank screen with no signal. The store's restore action does NOT
  // catch its own errors (they bubble for callers to decide UX) — this
  // mount-time call is the only consumer that needs a user-facing
  // recovery affordance.
  //
  // We call bootstrap() (not restoreStateFromBackend directly) so that
  // App.vue's loading-overlay gate and this mount-time hydration share a
  // single in-flight promise — App.vue's earlier call seeded it, this
  // call awaits the same singleton and gets the same resolution / same
  // rejection. Without the singleton, both call sites would each kick
  // off independent fetchAgents/fetchSessions/fetchModels round-trips.
  try {
    await chatStore.bootstrap()
  } catch (err) {
    const message = err instanceof Error ? err.message : 'Failed to load sessions'
    chatStore.error = message
    showToast({
      title: 'Could not load chat history',
      message,
      variant: 'error',
      duration: 6000,
    })
  }
  scrollMessagePaneToBottom('smooth')
  void swarmStore.connect()

  // Slice G — Escape-twice cancel cascade (Streaming Coherence May 2026).
  // Register global keydown listener for escape-twice keybinding.
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      void chatStore.handleEscapeKey()
    }
  })
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

      <!--
        Persistent banner for stream_critical SSE events. Mounted above
        the message-pane so the user sees fatal provider errors
        (revoked OAuth, 401, billing/quota) immediately, distinct from
        the transient-error toast at the viewport's bottom-right. The
        banner is gated entirely on chatStore.criticalError — it is
        invisible when the state is null.
      -->
      <CriticalErrorBanner />

      <section ref="messagePaneRef" class="message-pane" data-testid="chat-message-pane" @scroll="onMessagePaneScroll">
        <div v-if="groupedMessages.length === 0" class="empty-state" data-testid="chat-empty-state">
          Start a conversation with the selected agent.
        </div>
        <div v-else class="message-list" data-testid="message-list">
          <template v-for="(entry, index) in groupedMessages" :key="entry.type === 'message' ? entry.message.id : `context-group-${index}`">
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
        <ContextUsageChip />
        <ModelPicker :readonly="isChildSession" />
      </div>

      <!--
        Activity affordance: pre-fix the loading-pulse and the activity
        indicator were gated on disjoint store flags (loading-pulse on
        isLoading && !isStreaming; indicator on isStreaming alone). When
        the backend emitted no intermediate `content` events on the SSE
        stream — only the [DONE] sentinel — `isStreaming` was never true
        for the entire send, so the user saw only a thin shimmer bar (the
        2px loading-pulse) and reported "no loading dots, no animation".
        The indicator now surfaces while EITHER flag is on, so the user
        gets a continuous "the agent is working…" affordance from the
        click through to the response landing in the thread.
        See bug-fix note "Vue Chat Fresh-Session Duplicate User Bubble +
        Missing Streaming Affordance (May 2026)".
      -->
      <div
        v-if="chatStore.isLoading && !chatStore.isStreaming"
        class="loading-pulse"
        data-testid="loading-pulse"
        aria-hidden="true"
      />

      <!--
        Track B — model+provider visibility during streaming.
        The activity-indicator label now includes the active model and
        provider when both are known, so the user can see at a glance
        WHICH model is producing the answer they're watching arrive.
        After a failover (provider_changed SSE event), the chatStore
        updates currentProviderId/currentModelId so this label
        reflects the new active model immediately — paired with the
        transient toast that announces the switch.
      -->
      <div
        v-if="chatStore.isStreaming || chatStore.isLoading"
        class="agent-activity-indicator"
        data-testid="agent-activity-indicator"
        role="status"
        aria-live="polite"
      >
        <span class="agent-activity-dot" aria-hidden="true" />
        <span class="agent-activity-label">{{ chatStore.agentId }} is working…</span>
        <span
          v-if="chatStore.currentModelId || chatStore.currentProviderId"
          class="agent-activity-model"
          data-testid="agent-activity-model"
        >
          on {{ chatStore.currentModelId || chatStore.currentProviderId }}<template
            v-if="chatStore.currentModelId && chatStore.currentProviderId"
          > · {{ chatStore.currentProviderId }}</template>
        </span>
      </div>

      <!--
        Slice E (May 2026) — queued prompts rendered between the
        thread and the composer. Submit-while-streaming pushes onto
        the queue rather than bouncing the prompt; clicking X reverts
        the prompt into the composer for edit-then-resend.
      -->
      <QueuedPromptStrip />

      <MessageInput />
    </div>

    <aside v-if="showSwarmPane" class="chat-sidebar" :style="{ width: `${settingsStore.chatSidebarWidth}px` }" data-testid="swarm-pane">
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

.agent-activity-indicator {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.3rem 1rem;
  background: var(--bg-secondary);
  border-top: 1px solid var(--border);
  flex-shrink: 0;
  font-size: 0.8rem;
  color: var(--accent);
}

.agent-activity-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--accent);
  animation: pulse-dot 1.2s ease-in-out infinite;
  flex-shrink: 0;
}

.agent-activity-label {
  color: var(--text-muted);
}

.agent-activity-model {
  color: var(--text-muted);
  font-size: 0.75rem;
  letter-spacing: 0.02em;
  opacity: 0.85;
  margin-left: 0.15rem;
}

@keyframes pulse-dot {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.4; transform: scale(0.75); }
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
