<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import { useSettingsStore } from '@/stores/settingsStore'
import { useSwarmStore } from '@/stores/swarmStore'
import {
  resolveAgentName,
  collapseToolPairs,
  groupContextTools,
  buildPrecedingUserPromptMap,
} from '@/views/chatViewHelpers'
import type { GroupedMessageEntry } from '@/views/chatViewHelpers'
import type { Message } from '@/types'
import ContextUsageChip from '@/components/chat/ContextUsageChip.vue'
import QuotaChip from '@/components/chat/QuotaChip.vue'
import CriticalErrorBanner from '@/components/chat/CriticalErrorBanner.vue'
import MessageBubble from '@/components/chat/MessageBubble.vue'
import MessageInput from '@/components/chat/MessageInput.vue'
import QueuedPromptStrip from '@/components/chat/QueuedPromptStrip.vue'
import TodoListPanel from '@/components/chat/TodoListPanel.vue'
import ChildSessionsPanel from '@/components/chat/ChildSessionsPanel.vue'
import EmptyChatState from '@/components/chat/EmptyChatState.vue'
import AgentPicker from '@/components/agent-picker/AgentPicker.vue'
import ModelPicker from '@/components/model-picker/ModelPicker.vue'
import ContextToolGroup from '@/components/tools/ContextToolGroup.vue'
import Icon from '@/components/common/Icon.vue'
import KeyboardHelpModal from '@/components/common/KeyboardHelpModal.vue'
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

// QW-11 — Delegated-session read-only banner. A child session is a replay
// of work another agent did on the user's behalf; injecting prompts there
// is not a supported flow, so we hide MessageInput and show a slim banner
// in its place. The banner exposes a backlink to the parent so the user
// can navigate back to a composable thread.
const parentSessionForBanner = computed(() => {
  const parentId = currentSessionSummary.value?.parentId
  if (!parentId) return null
  return chatStore.sessions.find((s) => s.id === parentId) ?? null
})
const parentSessionTitle = computed(() => {
  const parent = parentSessionForBanner.value
  if (!parent) return 'parent session'
  return parent.title || `Session ${parent.id.slice(0, 8)}`
})
async function goToParentSession(): Promise<void> {
  const parentId = currentSessionSummary.value?.parentId
  if (!parentId) return
  chatStore.currentSessionId = parentId
  await chatStore.loadSessionMessages(parentId)
}

const groupedMessages = computed<GroupedMessageEntry[]>(() =>
  groupContextTools(collapseToolPairs(chatStore.messages)),
)

// UI Parity bug-fix bundle (May 2026). P2-9: precompute the per-bubble
// preceding-user-message lookup once per messages-array change instead
// of letting each MessageBubble re-scan chatStore.messages on every
// chunk. The bubble accepts the resolved tuple as a prop; the helper
// walks the list once and returns a Map<assistantId, prompt | null>.
// Pre-fix on a 200-message session streaming at 30 chunks/sec this
// reactive chain did 200 × 200 × 30 = 1.2M iterations/sec; the prop
// hoist drops it to a single walk per reactive update.
const precedingUserPromptMap = computed(() =>
  buildPrecedingUserPromptMap(chatStore.messages),
)

function precedingUserPromptFor(messageId: string): { id: string; content: string } | null {
  // The map's `get` returns `undefined` for messages that aren't in the
  // map (non-assistant messages); coerce to null so the prop semantics
  // are consistent (explicit null → hide affordance).
  const found = precedingUserPromptMap.value.get(messageId)
  return found ?? null
}

// Bug Hunt (May 2026) — session-return streaming visibility. The
// activity-indicator + loading-pulse must reflect the CURRENT
// session's streaming state, not the legacy global isStreaming /
// isLoading flags. The globals are only mirrored from
// setSessionStreaming when sessionId === currentSessionId at the
// moment of the call; navigating A → B → A leaves the globals
// holding whatever B last reported, so a session that is still
// streaming after the user returns shows no indicator.
//
// `streamingFor(currentSessionId)` reads the per-session record
// (Slice A — Streaming Coherence May 2026) which IS updated
// independently of the active view. When currentSessionId is null
// the getter falls back to the legacy globals (e.g. for unit-test
// mounts that set chatStore.isStreaming = true directly without
// wiring a session id), preserving backwards compatibility.
//
// Stays on FE-only streamingFor: current-session optimistic UI between
// chat-send resolve and long-poll attach. Child-session list surfaces
// (ChildSessionsPanel, SessionBrowser, SessionSwitcher) use backend-
// authoritative child.activeTurnId per Child Session Turn Registry plan
// (May 2026) §Item 3 + §R8. The dual-source boundary is current-session
// (here) vs list-rendering (those three components); see plan §R8 for
// the drift-risk note and the future-work item to consolidate.
const activeStreamingState = computed(() => chatStore.streamingFor(chatStore.currentSessionId))
// UI Parity PR5 — Live token counter (May 2026).
//
// The engine threads cumulative output_tokens onto every
// streaming.heartbeat tick; chatStore.tokenCountBySession records
// the latest value per session and tokensPerSecondBySession holds the
// computed rate from the delta between consecutive heartbeats. These
// computeds expose the active session's figures to the streaming
// chrome so the counter chip renders "1,247 tokens · 42 t/s" next
// to the working-on label. Zero token counts gate the chip render
// entirely (hide on pre-first-UsageDelta state); zero rates suppress
// the trailing "· N t/s" segment (single-tick state).
const activeLiveTokenCount = computed(() => {
  const sid = chatStore.currentSessionId
  if (!sid) return 0
  return chatStore.tokenCountBySession[sid] ?? 0
})
const activeLiveTokensPerSecond = computed(() => {
  const sid = chatStore.currentSessionId
  if (!sid) return 0
  return chatStore.tokensPerSecondBySession[sid] ?? 0
})
// Pre-formatted thousands-grouped string (en-GB, matches British
// English convention used across the codebase). Computed so Vue's
// reactivity tracks the count not the formatted output.
const activeLiveTokenCountFormatted = computed(() =>
  activeLiveTokenCount.value.toLocaleString('en-GB'),
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

// QW-9 — Scroll-to-bottom affordance. Clicking the floating button clears
// the userScrolledUp gate (re-arming auto-scroll for subsequent streaming
// chunks) and immediately scrolls smoothly to the latest message. The button
// itself is hidden whenever userScrolledUp is false (the user is already at
// or near the bottom), so this handler is only ever invoked from a visible
// button. The flag flip must happen before the scrollMessagePaneToBottom
// call: that helper short-circuits when userScrolledUp is true.
function handleScrollToBottomClick(): void {
  userScrolledUp.value = false
  scrollMessagePaneToBottom('smooth')
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
    // UX consolidation (May 2026) — tightened from 100px to 24px. The
    // pre-consolidation 100px threshold meant the user had to scroll roughly
    // five MessageBubble heights up before userScrolledUp latched and the
    // floating scroll-to-bottom button appeared, leaving the affordance
    // invisible during normal use. 24px is well under one bubble's height
    // so scrolling 1-2 messages reveals the button while staying within
    // smooth-scroll-animation slop tolerance.
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24
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
  () => activeStreamingState.value.isLoading,
  (loading) => {
    if (loading) {
      userScrolledUp.value = false
    }
  },
)

watch(
  () => chatStore.currentSessionId,
  async (newSessionId) => {
    await nextTick()
    userScrolledUp.value = false
    scrollMessagePaneToBottom('smooth')

    // Bug-O (May 2026) — per-view swarm reattach. The original
    // swarmStore.connect() call in onMounted captured the session at
    // call time, and the backend's eventBelongsToSession predicate
    // pins the SSE socket to that id for the lifetime of the loop.
    // When the user navigates into a child session, delegations
    // spawned by THAT child (grand-children) are scoped to a new
    // session id the open socket never heard of, so the panel goes
    // stale. Per-view semantics: the panel shows delegations
    // belonging to the currently viewed session. On session change
    // tear down → clear stale rows → reattach with the new id.
    //
    // disconnect() also clears the stall and reconnect timers — no
    // EventSource leaks across rapid back-and-forth navigation.
    // connect() is awaited last so the abortController setup races
    // strictly behind the prior disconnect.
    if (newSessionId) {
      await swarmStore.disconnect()
      swarmStore.clear()
      void swarmStore.connect(newSessionId)
    } else {
      await swarmStore.disconnect()
      swarmStore.clear()
    }
  },
)

let teardownHierarchyNav: (() => void) | null = null

// UI Parity PR2 I2 (May 2026) — keyboard-help modal open state.
// Triggered by global `?` (when no input is focused) or `Ctrl+/` (works
// everywhere). The handler logic lives below in handleGlobalKeydown
// so it lives next to the Escape handler and shares a single keydown
// listener registration.
const keyboardHelpOpen = ref(false)

function closeKeyboardHelp(): void {
  keyboardHelpOpen.value = false
}

// UI Parity PR6 — Collapse all / Expand all toolbar (I4 extension).
//
// Flip the store-level override; ToolBubble computes its effective open
// state from chatStore.toolCardOpenOverride and bulk-applies the new
// value. 'auto' is the no-op default — buttons only set 'expanded' or
// 'collapsed' so the per-card state remains intact when the user later
// toggles a single card.
function expandAllToolCards(): void {
  chatStore.toolCardOpenOverride = 'expanded'
}

function collapseAllToolCards(): void {
  chatStore.toolCardOpenOverride = 'collapsed'
}

// Discriminator used by the `?` trigger: opening the modal must NOT
// fight the user mid-prompt. Anything inside a textarea / input /
// contenteditable suppresses the trigger.
function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  const tag = target.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA') return true
  if (target.isContentEditable) return true
  const attr = target.getAttribute('contenteditable')
  if (attr !== null && attr !== 'false') return true
  return false
}

// UI Parity bug-fix bundle (May 2026). P1-8: pre-fix the `?` trigger
// fired any time the bare key was observed on a non-editable target,
// which included buttons, links, role="button" elements — tab to a
// button, press ?, and the modal popped open unexpectedly. The
// predicate below excludes those button-ish surfaces so the modal only
// opens when the user clearly meant to ask for help (i.e. focus is on
// neutral chrome).
function isButtonLikeTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  const tag = target.tagName
  if (tag === 'BUTTON' || tag === 'A') return true
  const role = target.getAttribute('role')
  if (role === 'button' || role === 'link' || role === 'menuitem' || role === 'tab') return true
  return false
}

// H9 — Bug Hunt Findings (May 2026). The Slice G escape-twice listener
// was originally registered as an inline anonymous arrow inside
// onMounted with no matching removeEventListener in onBeforeUnmount.
// After N route round-trips (Chat → other → Chat → ...) N copies of
// the handler stayed live, so a single Escape press fanned out into N
// concurrent DELETE /v1/sessions/{id}/stream requests. Lifting the
// handler into a setup-scope const captures a stable identity that
// onBeforeUnmount can pass to removeEventListener for clean teardown.
function handleGlobalKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    void chatStore.handleEscapeKey()
    return
  }
  // UI Parity PR2 I2 — keyboard-help triggers. `?` is unshifted from
  // `/` on most layouts; treat both forms identically. Ctrl+/ works
  // anywhere (mirrors the OpenCode chord), the bare `?` is suppressed
  // inside editables so the user can still type questions into the
  // composer.
  if (event.ctrlKey && event.key === '/') {
    event.preventDefault()
    keyboardHelpOpen.value = true
    return
  }
  // UI Parity bug-fix bundle (May 2026). P1-8: tightened predicate.
  // (1) event.repeat — held-key spam must not spawn a modal-open per
  //     tick; one keystroke = at most one open.
  // (2) shift+slash code probe — `event.key === '?'` is layout-
  //     dependent. Pairing with `event.code === 'Slash' && shiftKey`
  //     keeps the trigger robust on AZERTY/Dvorak where `?` may sit
  //     on a different physical key.
  // (3) button-like targets — pre-fix the trigger fired for ANY
  //     non-editable target; tab to a button and `?` opened the
  //     modal. Exclude button / link / role="button" etc.
  if (event.repeat) return
  const isHelpKey = event.key === '?' || (event.code === 'Slash' && event.shiftKey)
  if (
    isHelpKey &&
    !isEditableTarget(event.target) &&
    !isButtonLikeTarget(event.target)
  ) {
    event.preventDefault()
    keyboardHelpOpen.value = true
  }
}

onMounted(async () => {
  // Tool renderer registration moved to web/src/main.ts module init — see
  // the comment there. Calling it here ran AFTER the first child render of
  // MessageBubble, so the computed `toolComponent` (a non-reactive
  // Map-lookup) latched on GenericTool for every todowrite / todo_update
  // tool_result the session loaded with. Registering before app.mount()
  // guarantees every bubble's first computed sees the populated registry.
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
  // Register global keydown listener for escape-twice keybinding. The
  // handler is a setup-scope const (handleGlobalKeydown) so onBeforeUnmount
  // can pass the same identity to removeEventListener and clean up
  // properly across route round-trips. See H9 in Bug Hunt Findings.
  document.addEventListener('keydown', handleGlobalKeydown)
})

onBeforeUnmount(() => {
  stopDragging()
  swarmStore.disconnect()
  document.removeEventListener('keydown', handleGlobalKeydown)
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

      <div class="message-pane-wrap">
        <section ref="messagePaneRef" class="message-pane" data-testid="chat-message-pane" @scroll="onMessagePaneScroll">
          <!--
            UI Parity I10 (May 2026) — empty-state surfaces an agent
            card + example-prompt chips + /help affordance. The
            `data-testid="chat-empty-state"` pin lives on the
            component root so the 5 e2e specs that wait on its
            visibility continue to work unchanged.
          -->
          <EmptyChatState v-if="groupedMessages.length === 0" />
          <div v-else class="message-list" data-testid="message-list">
            <template v-for="(entry, index) in groupedMessages" :key="entry.type === 'message' ? entry.message.id : `context-group-${index}`">
              <MessageBubble
                v-if="entry.type === 'message'"
                :message="entry.message"
                :agent-name="agentNameFor(entry.message)"
                :preceding-user-prompt="precedingUserPromptFor(entry.message.id)"
              />
              <ContextToolGroup
                v-else-if="entry.type === 'context-group'"
                :messages="entry.messages"
                :tool-counts="entry.toolCounts"
              />
            </template>
          </div>
        </section>
        <!--
          QW-9 — Floating scroll-to-bottom affordance. Visible only when the
          user has scrolled up (userScrolledUp=true). Click clears the gate
          and smooth-scrolls to the latest message, re-arming auto-scroll
          for subsequent streaming chunks. Layered above the message pane
          via absolute positioning inside .message-pane-wrap so it doesn't
          shift the existing flex layout (ChildSessionsPanel /
          input-selector-bar all still flow normally below the wrap).
        -->
        <button
          v-if="userScrolledUp"
          type="button"
          class="scroll-to-bottom-btn"
          data-testid="scroll-to-bottom-btn"
          aria-label="Scroll to latest message"
          title="Scroll to latest message"
          @click="handleScrollToBottomClick"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="18"
            height="18"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <polyline points="6 9 12 15 18 9" />
          </svg>
        </button>
      </div>

      <!--
        ChildSessionsPanel surfaces persistent children of the current
        session derived from chatStore.sessions. Auto-hides when there are
        no children. The legacy DelegationStrip (transient swarm-bus pulse
        view) was removed in the UX consolidation (May 2026): its
        in-thread delegation list duplicated this panel's job, the pulses
        vanished on reload, and DelegationPanel still surfaces raw swarm
        events in the swarm pane for users who want them.
      -->
      <ChildSessionsPanel />

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
        <QuotaChip />
        <ModelPicker :readonly="isChildSession" />
        <!--
          UI Parity PR6 — Collapse all / Expand all (I4 extension). Bulk
          toggle every ToolBubble via the store-level override. The buttons
          live in the toolbar where tool-density is highest; per-card state
          is preserved and resumes when the override returns to 'auto'.
        -->
        <button
          type="button"
          class="tool-toggle-btn"
          data-testid="expand-all-tools-btn"
          title="Expand all tool cards"
          @click="expandAllToolCards"
        >
          Expand all
        </button>
        <button
          type="button"
          class="tool-toggle-btn"
          data-testid="collapse-all-tools-btn"
          title="Collapse all tool cards"
          @click="collapseAllToolCards"
        >
          Collapse all
        </button>
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
        v-if="activeStreamingState.isLoading && !activeStreamingState.isStreaming"
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
        v-if="activeStreamingState.isStreaming || activeStreamingState.isLoading"
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
        <!--
          UI Parity PR5 — Live token counter (May 2026).
          Renders the active session's cumulative output_tokens and
          tokens-per-second from the engine's streaming.heartbeat
          ticks. Hidden entirely until a positive count arrives so a
          fresh turn does not flash "0 tokens" before the provider's
          first message_delta. The trailing rate segment is suppressed
          on the first tick (no predecessor to delta against) so the
          chrome reads "1,247 tokens" rather than the misleading
          "1,247 tokens · 0 t/s".
        -->
        <span
          v-if="activeLiveTokenCount > 0"
          class="agent-activity-tokens"
          data-testid="agent-activity-tokens"
        >
          {{ activeLiveTokenCountFormatted }} tokens<template
            v-if="activeLiveTokensPerSecond > 0"
          > · {{ activeLiveTokensPerSecond }} t/s</template>
        </span>
      </div>

      <!--
        Slice E (May 2026) — queued prompts rendered between the
        thread and the composer. Submit-while-streaming pushes onto
        the queue rather than bouncing the prompt; clicking X reverts
        the prompt into the composer for edit-then-resend.
      -->
      <QueuedPromptStrip />

      <!--
        QW-11 — Delegated child sessions are read-only. The composer is
        hidden so the user cannot inject prompts into a replayed thread;
        in its place a slim banner surfaces "this session was delegated
        from <parent>" with a backlink to the parent so the user can
        return to a composable thread without hunting for it in the
        switcher.
      -->
      <MessageInput v-if="!isChildSession" />
      <div
        v-else
        class="child-session-readonly-banner"
        data-testid="child-session-readonly-banner"
        role="status"
        aria-live="polite"
      >
        <span class="readonly-banner-icon" aria-hidden="true">
          <Icon name="inbox" :size="16" />
        </span>
        <span class="readonly-banner-text">
          This session was delegated from
          <button
            type="button"
            class="readonly-banner-parent-link"
            data-testid="child-session-readonly-parent-link"
            @click="goToParentSession"
          >{{ parentSessionTitle }}</button>
          · read-only.
        </span>
      </div>
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

    <!--
      UI Parity PR2 I2 (May 2026) — discoverable keyboard-shortcut list.
      Triggered by `?` (no-input-focus) or `Ctrl+/` (always). The modal
      is mounted at the view root so it overlays the entire chat shell.
    -->
    <KeyboardHelpModal :open="keyboardHelpOpen" @close="closeKeyboardHelp" />
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

.message-pane-wrap {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
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

/*
 * QW-9 — Floating scroll-to-bottom button. Pinned to the bottom-right of
 * the message-pane wrap so it sits inside the chat thread region, above
 * the messages but below the ChildSessionsPanel / composer (those are
 * siblings of .message-pane-wrap, not children, so they flow normally and
 * the button never overlaps them). The 1.25rem
 * inset clears the typical scrollbar gutter without overlapping a
 * MessageBubble's right edge.
 */
.scroll-to-bottom-btn {
  position: absolute;
  right: 1.25rem;
  bottom: 1rem;
  width: 36px;
  height: 36px;
  padding: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--bg-elevated);
  color: var(--text-primary);
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.18);
  transition: transform 120ms ease, box-shadow 120ms ease, background 120ms ease;
  z-index: 5;
}

.scroll-to-bottom-btn:hover {
  background: var(--bg-hover, var(--bg-elevated));
  transform: translateY(-1px);
  box-shadow: 0 3px 10px rgba(0, 0, 0, 0.24);
}

.scroll-to-bottom-btn:focus-visible {
  outline: 2px solid var(--accent, #4c8bf5);
  outline-offset: 2px;
}

.message-list {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

/*
 * UI Parity I10 (May 2026) — the legacy `.empty-state` rule moved into
 * `EmptyChatState.vue` so the component owns its own typography. The
 * `data-testid="chat-empty-state"` pin moved with it.
 */

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

/*
 * UI Parity PR6 — Collapse all / Expand all toolbar (I4 extension). Small
 * neutral buttons next to the model picker. Hover lifts the border colour to
 * the accent token so the affordance becomes discoverable on touch.
 */
.tool-toggle-btn {
  padding: 0.25rem 0.55rem;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--bg-elevated);
  color: var(--text-muted);
  font-size: 0.75rem;
  cursor: pointer;
  white-space: nowrap;
  transition: border-color 0.15s, color 0.15s;
}

.tool-toggle-btn:hover {
  border-color: var(--accent);
  color: var(--text-primary);
}

.loading-pulse {
  height: 2px;
  flex-shrink: 0;
  background: linear-gradient(90deg, transparent 0%, var(--accent) 50%, transparent 100%);
  background-size: 200% 100%;
  animation: pulse-shimmer 1.5s ease-in-out infinite;
}

/*
 * QW-11 — Delegated-session read-only banner. Takes the place of
 * MessageInput on child sessions so the layout doesn't shift between
 * parent and child navigation. Muted background to distinguish from the
 * primary composer; backlink rendered as a button-styled inline link.
 */
.child-session-readonly-banner {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.65rem 1rem;
  background: var(--bg-secondary);
  border-top: 1px solid var(--border);
  flex-shrink: 0;
  font-size: 0.85rem;
  color: var(--text-secondary);
}

.readonly-banner-icon {
  font-size: 1rem;
}

.readonly-banner-text {
  line-height: 1.4;
}

.readonly-banner-parent-link {
  background: transparent;
  border: none;
  padding: 0;
  font: inherit;
  color: var(--accent);
  text-decoration: underline;
  cursor: pointer;
}

.readonly-banner-parent-link:hover {
  filter: brightness(1.15);
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
