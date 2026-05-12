<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { Message } from '@/types'
import { useChatStore } from '@/stores/chatStore'
import MarkdownRenderer from './MarkdownRenderer.vue'
import ThinkingPanel from './ThinkingPanel.vue'
import CopyButton from '@/components/tools/CopyButton.vue'
import ToolErrorCard from '@/components/tools/ToolErrorCard.vue'
import GenericTool from '@/components/tools/GenericTool.vue'
import { getToolComponent } from '@/tools/toolRegistry'
import { buildToolRenderSpec } from '@/views/toolRenderSpec'
import { sanitiseMessageContent } from '@/lib/messageContentBackstop'

defineOptions({ name: 'MessageBubble' })

// UI Parity bug-fix bundle (May 2026). P2-9: precedingUserPrompt is now
// an optional prop, hoisted to the parent's groupedMessages builder so
// each bubble's lookup is O(1). The internal computed remains as a
// fallback for callers that mount MessageBubble directly without
// pre-resolving the predecessor (existing tests, ad-hoc renders).
const props = defineProps<{
  message: Message
  agentName?: string
  precedingUserPrompt?: { id: string; content: string } | null
}>()

const chatStore = useChatStore()
const now = ref(Date.now())
const elapsedTimer = ref<ReturnType<typeof setInterval> | null>(null)

async function loadDelegatedSession(): Promise<void> {
  if (!props.message.targetAgent) return
  // Bug Hunt (May 2026) sibling-confusion fix — pass the message's
  // chainId alongside the targetAgent so the store can disambiguate
  // sibling delegations to the same agent. Pre-fix the resolver was
  // agent-id-only and silently routed clicks on an earlier delegation
  // card to the most-recent sibling for the same agent.
  await chatStore.loadSessionForDelegation({
    chainId: props.message.chainId,
    agentId: props.message.targetAgent,
  })
}

function startTimer(): void {
  if (elapsedTimer.value !== null) return
  elapsedTimer.value = setInterval(() => {
    now.value = Date.now()
  }, 1000)
}

function stopTimer(): void {
  if (elapsedTimer.value !== null) {
    clearInterval(elapsedTimer.value)
    elapsedTimer.value = null
  }
}

onMounted(() => {
  // Only tick for in-flight delegation messages — elapsedLabel is only
  // displayed there. Running an interval for every bubble in a long
  // conversation burns CPU for no visible effect.
  if (props.message.role === 'delegation_started' && props.message.status !== 'completed') {
    startTimer()
  }
})

onBeforeUnmount(() => {
  stopTimer()
})

const elapsedLabel = computed(() => {
  const startedAt = Date.parse(props.message.timestamp)
  if (Number.isNaN(startedAt)) {
    return '0s'
  }
  const seconds = Math.max(0, Math.floor((now.value - startedAt) / 1000))
  if (seconds < 60) {
    return `${seconds}s`
  }
  const minutes = Math.floor(seconds / 60)
  const remaining = seconds % 60
  return `${minutes}m ${remaining}s`
})

const hasProgress = computed(
  () =>
    typeof props.message.toolCalls === 'number' ||
    typeof props.message.lastTool === 'string',
)

const isDelegationStarted = computed(() => props.message.role === 'delegation_started')
const isDelegation = computed(() => props.message.role === 'delegation')
// B2 (May 2026). isThinking gates the new ThinkingPanel render path
// only when there is something to show — either thinkingBlocks with
// at least one non-empty thinking field, or non-empty content. A
// thinking-role message with neither falls through to the wrapper
// gate and is suppressed (matches the May 2026 blank-bubble gate
// from commit 4c0cee54).
const isThinking = computed(() => {
  if (props.message.role !== 'thinking') return false
  const blocks = props.message.thinkingBlocks ?? []
  if (blocks.some((b) => (b.thinking ?? '').trim().length > 0)) return true
  return (props.message.content ?? '').trim().length > 0
})

// B2 (Vue UI Parity vs OpenCode, May 2026). Thinking content sections.
//
// Per the brief, `thinkingBlocks[]` is the better data source —
// the engine persists per-block thinking with optional signatures
// (Anthropic extended-thinking signed blocks, etc). One ThinkingPanel
// per block lets the user disclose them independently.
//
// Legacy thinking-role messages on disk carry `content` populated
// but no `thinkingBlocks` array. The fallback path uses the joined
// content string so older sessions keep rendering.
//
// Each section's text is filtered to non-empty — a thinking block
// with both `thinking` and (e.g.) `redacted` fields is rare on the
// wire but the filter keeps the panel array stable.
const thinkingSections = computed<string[]>(() => {
  if (!isThinking.value) return []
  const blocks = props.message.thinkingBlocks ?? []
  if (blocks.length > 0) {
    return blocks
      .map((b) => (b.thinking ?? '').trim())
      .filter((t) => t.length > 0)
  }
  const content = (props.message.content ?? '').trim()
  return content.length > 0 ? [content] : []
})

// Thinking-only degraded turn — closes the UI follow-up flagged by
// `Empty-Content Thinking-Only Assistant Turn (May 2026)` in the
// FlowState vault. The session accumulator synthesises a placeholder
// assistant Message (commit fbecedfe and siblings) when an OpenAI-
// compat reasoning provider produces reasoning tokens but never emits
// visible content. The placeholder shape on the wire is:
//
//   { role: "assistant", content: "", thinkingBlocks: [...], stopReason: "..." }
//
// Without a UI render branch the user sees a blank bubble — visually
// indistinguishable from a stalled stream. The soft-error affordance
// surfaces "the agent thought but produced no response", a third UX
// class alongside (a) a stalled stream (no bubble at all) and (b) a
// critical stream error (CriticalErrorBanner, role="alert"). This
// affordance uses role="status" (informational) — the turn already
// finished, the user is being told *after the fact* that it produced
// no visible reply.
//
// The predicate is intentionally narrow: ALL three signals must be
// present. That matches the synthesis predicate on the Go side
// (`contentBuf.Len() == 0 && len(thinkingBlocks) > 0` plus a non-empty
// stop_reason from the upstream) and means a non-placeholder empty
// assistant — e.g. a true stall, or a placeholder synthesised by some
// future error path that doesn't carry thinking blocks — does not
// collide with this rendering.
const isThinkingOnlyDegraded = computed(() => {
  if (props.message.role !== 'assistant') return false
  if ((props.message.content ?? '') !== '') return false
  const thinkingBlocks = props.message.thinkingBlocks ?? []
  if (thinkingBlocks.length === 0) return false
  if (!props.message.stopReason) return false
  return true
})

// Empty-turn placeholder render branch — bug fix #27 (May 11 2026).
//
// Slice C (commit a3486538) added `handleStreamDone` logic that pushes an
// empty_turn placeholder into chatStore.messages when [DONE] arrives with
// no running assistant — the engine's synthesizePlaceholderAssistant did
// not emit one and the user prompt would otherwise sit there with no
// follow-up artefact at all. The store-side push shipped but no
// MessageBubble v-else-if consumed the new shape, so true empty turns
// (no content, no thinking, no tool_calls — Anthropic / OpenAI return
// this occasionally) were silently swallowed. The agent-block-render-gate
// follow-up (commit 4c0cee54) added hasRenderableContent which then
// suppressed the wrapper entirely for this shape — making the silent
// swallow load-bearing.
//
// The fix: a dedicated v-else-if branch that surfaces the SAME soft-error
// affordance copy the thinking-only-degraded branch uses ("Reply didn't
// come through" — commit 87c114c8 wording). The user-facing UX is the
// same: the model stopped before replying, try again. The two cases
// stay logically distinct (thinking-only carries reasoning tokens, empty
// turn carries nothing) but the user doesn't need to disambiguate.
//
// The predicate is narrow: assistant role + empty content + empty
// thinkingBlocks + stopReason === 'empty_turn'. The empty-thinkingBlocks
// guard prevents collision with isThinkingOnlyDegraded — if both
// branches matched, the v-else-if order would silently decide which
// fires, masking future state-machine bugs.
const isEmptyTurn = computed(() => {
  if (props.message.role !== 'assistant') return false
  if ((props.message.content ?? '') !== '') return false
  const thinkingBlocks = props.message.thinkingBlocks ?? []
  if (thinkingBlocks.length > 0) return false
  return props.message.stopReason === 'empty_turn'
})

// Both tool_result and an unmatched tool_call (one without a paired
// tool_result — collapseToolPairs leaves it intact) render through the
// same per-tool component. The collapsable card chrome already signals
// "this is a tool invocation", so a separate "TOOL_CALL" role label
// would be redundant.
const isToolInvocation = computed(
  () => props.message.role === 'tool_result' || props.message.role === 'tool_call',
)
const isToolError = computed(() => props.message.role === 'tool_error')

const toolSpec = computed(() => buildToolRenderSpec(props.message))

const toolStatus = computed<'pending' | 'running' | 'completed' | 'error'>(() => {
  if (props.message.status === 'error') return 'error'
  if (props.message.status === 'running') return 'running'
  if (props.message.status === 'pending') return 'pending'
  return 'completed'
})

const toolComponent = computed(() => {
  return getToolComponent(toolSpec.value.toolName) ?? GenericTool
})

// Empty-content assistant suppression — May 10 2026 follow-up to user
// feedback: "Are we outputting an agent response, along with a tool call?
// If so, this seems broken. We should just return the tool calls. Agent
// blocks are for when an agent *actually* has a response."
//
// Two store paths leave a sealed assistant message with empty content:
//
//   1. handleToolCallEvent (chatStore.ts:2509-2511) seals any in-flight
//      assistant placeholder when a tool_call SSE event arrives. If the
//      turn went straight to tool use without first emitting any content
//      chunks, the sealed placeholder carries content === '' and tool_call /
//      tool_result rows in the message list ARE the response.
//   2. The Streaming Coherence Slice C empty_turn placeholder pushed by
//      handleStreamDone carries content === '' + stopReason === 'empty_turn'
//      and no thinkingBlocks. This shape is now consumed by the
//      isEmptyTurn render branch above (bug fix #27, May 11 2026) — the
//      v-else-if ordering routes it there before this suppression matters.
//
// Without this gate `isPlain` rendered the assistant chrome (role label,
// empty MarkdownRenderer, copy-button-with-empty-text) for case 1,
// producing a phantom agent block alongside the tool cards. The gate is
// narrow: assistant role + content (after trim) is empty. The
// thinking-only-degraded and empty-turn branches match their own
// predicates first (`v-else-if` runs in order) so their affordances
// surface correctly.
const hasVisibleAssistantContent = computed(
  () =>
    props.message.role !== 'assistant' ||
    (props.message.content ?? '').trim().length > 0,
)

const isPlain = computed(
  () =>
    !isToolInvocation.value &&
    !isToolError.value &&
    !isDelegationStarted.value &&
    !isDelegation.value &&
    !isThinking.value &&
    !isThinkingOnlyDegraded.value &&
    !isEmptyTurn.value &&
    hasVisibleAssistantContent.value,
)

// Outer wrapper gate — May 11 2026 follow-up. User: "We should not see
// `<div class="message-bubble assistant" ...><!--v-if--></div>` if there
// is no data." Pre-fix the `<div class="message-bubble">` rendered
// unconditionally; when every inner `v-if`/`v-else-if` branch was false
// the result was an empty styled box (padding + border + border-radius)
// visible as a blank card. The wrapper itself must now gate on at least
// one render branch matching.
const hasRenderableContent = computed(
  () =>
    isToolInvocation.value ||
    isToolError.value ||
    isDelegationStarted.value ||
    isDelegation.value ||
    isThinking.value ||
    isThinkingOnlyDegraded.value ||
    isEmptyTurn.value ||
    isPlain.value,
)

// Defensive backstop for the May 2026 chat-UI leak class (session
// 2d8dc0ac). The backend is the primary fix surface — see
// internal/streaming.IsControlEvent, internal/engine.UnwrapTaskResult,
// internal/engine.sanitiseTaskError. This computed catches anything
// that slips through (e.g. session loaded from disk persisted before
// the fix shipped) so non-technical users never see raw harness JSON,
// `<task_result>` markers, or provider stack traces in the chat bubble.
// The friendly fallback string is rendered verbatim — no markdown
// processing — to avoid re-introducing exotic content via the same
// surface the backstop is protecting.
const sanitisedAssistantContent = computed(() =>
  sanitiseMessageContent(props.message.content ?? ''),
)
const sanitisedPlainContent = computed(() =>
  sanitiseMessageContent(props.message.content ?? ''),
)

const displayRole = computed(() =>
  props.message.role === 'assistant' && props.agentName
    ? props.agentName
    : props.message.role,
)

// Copy affordance: surface a clipboard button on the user's own messages
// and on assistant replies, mirroring the convention already used inside
// tool-call cards. Tool/delegation/thinking branches each have their own
// chrome (or are non-content), so they intentionally opt out.
const showCopyButton = computed(
  () =>
    isPlain.value &&
    (props.message.role === 'assistant' || props.message.role === 'user'),
)

// Revert affordance: only user messages can be reverted. Clicking revert
// truncates the session at this message and pre-fills the composer so the
// user can edit and re-send without re-typing.
const showRevertButton = computed(
  () => isPlain.value && props.message.role === 'user',
)

// UI Parity I7 (May 2026) — Regenerate affordance on assistant messages.
// Mirrors OpenCode's "regenerate this reply" gesture. Resolves the
// preceding user message in chatStore.messages (the prompt that produced
// this reply), then calls revertToMessage(userId) to truncate +
// sendMessage(prompt) to re-send. Keeps current agent/model untouched —
// the chatStore action targets the active session+agent.
//
// Defensive: if no preceding user message can be found (orphan reply,
// truncated history, malformed thread), the button is hidden rather
// than surfacing a no-op click. Less surprising for the user.
//
// P2-9 (May 2026 bug-fix bundle): prefer the `precedingUserPrompt`
// prop when supplied — the parent's groupedMessages builder resolves
// the predecessor once, so the bubble doesn't re-scan chatStore.messages
// on every chunk (O(N²) cost on long sessions during streaming). When
// the prop is undefined we fall back to the legacy local computed so
// existing callers (and unit tests that don't pass the prop) keep
// working. An EXPLICIT null prop is respected: the parent has told us
// there is no preceding prompt, hide the affordance.
const precedingUserPrompt = computed<{ id: string; content: string } | null>(() => {
  // Explicit prop wins (including explicit null).
  if (props.precedingUserPrompt !== undefined) {
    return props.precedingUserPrompt
  }
  if (props.message.role !== 'assistant') return null
  // Defensive: chatStore.messages may be undefined in test mocks that
  // don't seed the array. Treat that as "no preceding prompt" so the
  // Regenerate affordance hides cleanly.
  const messages = chatStore.messages
  if (!Array.isArray(messages)) return null
  const idx = messages.findIndex((m) => m.id === props.message.id)
  if (idx <= 0) return null
  for (let i = idx - 1; i >= 0; i -= 1) {
    const m = messages[i]
    if (m.role === 'user') {
      return { id: m.id, content: m.content }
    }
  }
  return null
})

// UI Parity bug-fix bundle (May 2026). P1-7: Regenerate clicked
// mid-stream calls revertToMessage which disconnects whatever session
// is currently streaming, silently killing a different in-flight turn.
// Gate the button on "no stream in flight anywhere" so the user
// physically cannot trigger the cascade. We check the per-session map
// AND the legacy flat flags (some test mounts set the flats directly
// without seeding the map).
const anyStreamInFlight = computed<boolean>(() => {
  // streamingFor is the canonical per-session getter; consult it via
  // the active session and any other slot. The flat fields back up the
  // null-session fast path.
  const sessionMap = chatStore.sessionStreaming ?? {}
  for (const slot of Object.values(sessionMap)) {
    if (slot.isStreaming || slot.isLoading) return true
  }
  return Boolean(chatStore.isStreaming) || Boolean(chatStore.isLoading)
})

const showRegenerateButton = computed(
  () =>
    isPlain.value &&
    props.message.role === 'assistant' &&
    precedingUserPrompt.value !== null &&
    !anyStreamInFlight.value,
)

// Failure marker: when a user-message send rejects (network error, backend
// rejection), chatStore marks the optimistic bubble status='failed'. We
// surface that with a small visible affordance so the user can see at a
// glance which message didn't go through. Toast is the loud surfacing;
// this is the persistent indicator on the bubble itself.
const isFailedSend = computed(
  () => props.message.role === 'user' && props.message.status === 'failed',
)

async function handleRevert(): Promise<void> {
  await chatStore.revertToMessage(props.message.id)
}

// I7 — Regenerate: truncate back to the preceding user prompt then
// re-send it as a new turn. The revertToMessage call kills any in-flight
// stream + truncates the session; sendMessage then re-issues the prompt
// against the current agent/model. We capture the prompt content BEFORE
// the revert because revertToMessage may invalidate `precedingUserPrompt`
// once messages get sliced.
async function handleRegenerate(): Promise<void> {
  const target = precedingUserPrompt.value
  if (!target) return
  await chatStore.revertToMessage(target.id)
  await chatStore.sendMessage(target.content)
}
</script>

<template>
  <div
    v-if="hasRenderableContent"
    class="message-bubble"
    :class="[props.message.role, { 'message-bubble--failed': isFailedSend }]"
    :data-testid="`message-${props.message.role}`"
    :data-role="props.message.role"
    :data-status="props.message.status ?? ''"
  >
    <component
      v-if="isToolInvocation"
      :is="toolComponent"
      :tool-name="toolSpec.toolName"
      :heading="toolSpec.heading"
      :body="toolSpec.body"
      :status="toolStatus"
      :tool-input="props.message.toolInput"
      data-testid="tool-renderer"
    />

    <ToolErrorCard
      v-else-if="isToolError"
      :tool-name="props.message.toolName || 'error'"
      :heading="props.message.toolName || 'Error'"
      :body="props.message.content"
      data-testid="tool-error-renderer"
    />

    <div v-else-if="isDelegationStarted" class="delegation-card delegation-card--inflight">
      <span data-testid="delegation-spinner" class="delegation-spinner" aria-hidden="true">⋯</span>
      <div class="delegation-body">
        <div v-if="props.message.targetAgent" class="delegation-header">
          <button
            type="button"
            data-testid="delegation-agent-link"
            class="delegation-agent-link"
            @click="loadDelegatedSession"
          >
            {{ props.message.targetAgent }}
          </button>
          <span data-testid="delegation-elapsed" class="delegation-elapsed">{{ elapsedLabel }}</span>
        </div>
        <pre class="delegation-content">{{ props.message.content }}</pre>
        <div
          v-if="hasProgress"
          data-testid="delegation-progress"
          class="delegation-progress"
        >
          <span class="delegation-progress-count">{{ props.message.toolCalls ?? 0 }} tool calls</span>
          <span v-if="props.message.lastTool" class="delegation-progress-tool">· {{ props.message.lastTool }}</span>
        </div>
      </div>
    </div>

    <div v-else-if="isDelegation" class="delegation-card delegation-card--done">
      <div class="delegation-body">
        <button
          v-if="props.message.targetAgent"
          type="button"
          data-testid="delegation-agent-link"
          class="delegation-agent-link"
          @click="loadDelegatedSession"
        >
          {{ props.message.targetAgent }}
        </button>
        <pre class="delegation-content">{{ props.message.content }}</pre>
      </div>
    </div>

    <!--
      B2 (Vue UI Parity vs OpenCode, May 2026). Replace the flat
      `<p class="thinking">` with one ThinkingPanel per reasoning
      block. Collapsible by default, content routed through
      MarkdownRenderer so embedded code highlights via Shiki (B1).
    -->
    <template v-else-if="isThinking">
      <ThinkingPanel
        v-for="(section, idx) in thinkingSections"
        :key="idx"
        :content="section"
      />
    </template>

    <!--
      Soft-error affordance copy reword (May 7 2026, follow-up to commit
      0f27ac98). User feedback: "Why do we have a message about the agent
      not having a response. That seems pretty weird." The previous
      "No response produced / agent thought through this turn but
      produced no response" wording read as a system bug report and gave
      the user nothing actionable. The reword aims for the same register
      as the amber `role="status"` palette: less alarming than an error,
      conversational, and bearing a clear next step (re-prompt). The
      trigger predicate, palette, and role are unchanged — copy only.
    -->
    <div
      v-else-if="isThinkingOnlyDegraded"
      class="thinking-only-affordance"
      role="status"
      data-testid="thinking-only-affordance"
    >
      <span class="thinking-only-icon" aria-hidden="true">!</span>
      <div class="thinking-only-content">
        <span class="thinking-only-title">Reply didn't come through</span>
        <span class="thinking-only-message">
          The model worked through this turn but stopped before replying. Try sending the prompt again.
        </span>
      </div>
    </div>

    <!--
      Empty-turn placeholder affordance — bug fix #27 (May 11 2026).
      Pre-fix the empty_turn placeholder pushed by handleStreamDone
      (chatStore.ts) reached this template with no matching v-else-if
      and was silently swallowed by the hasRenderableContent gate (commit
      4c0cee54). Reuses the same UX vocabulary as the thinking-only
      branch above ("Reply didn't come through" — commit 87c114c8) since
      from the user's perspective both states are "the model didn't
      reply, try again". The two paths stay separate at the predicate
      level so future divergence (e.g. distinct retry affordance) is a
      narrow edit, not an unwind.
    -->
    <div
      v-else-if="isEmptyTurn"
      class="thinking-only-affordance"
      role="status"
      data-testid="empty-turn-affordance"
    >
      <span class="thinking-only-icon" aria-hidden="true">!</span>
      <div class="thinking-only-content">
        <span class="thinking-only-title">Reply didn't come through</span>
        <span class="thinking-only-message">
          The model finished without producing a reply. Try sending the prompt again.
        </span>
      </div>
    </div>

    <template v-else-if="isPlain">
      <span class="message-role">{{ displayRole }}</span>
      <MarkdownRenderer
        v-if="props.message.role === 'assistant'"
        :content="sanitisedAssistantContent.content"
        :data-leak-backstop="sanitisedAssistantContent.appliedFilter || undefined"
      />
      <p
        v-else
        class="message-content"
        :data-leak-backstop="sanitisedPlainContent.appliedFilter || undefined"
      >{{ sanitisedPlainContent.content }}</p>
      <div v-if="showCopyButton" class="message-actions">
        <span
          v-if="isFailedSend"
          class="failed-marker"
          data-testid="message-failed-marker"
          role="status"
          title="Message failed to send"
        >&#x26A0; Failed to send</span>
        <button
          v-if="showRevertButton"
          type="button"
          class="revert-button"
          data-testid="message-revert-btn"
          title="Revert to this message"
          @click="handleRevert"
        >&#x21A9; Revert</button>
        <button
          v-if="showRegenerateButton"
          type="button"
          class="revert-button"
          data-testid="message-regenerate-btn"
          title="Regenerate this reply"
          @click="handleRegenerate"
        >&#x21BB; Regenerate</button>
        <CopyButton data-testid="message-copy-btn" :text="props.message.content" />
      </div>
    </template>
  </div>
</template>

<style scoped>
.message-bubble {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  padding: 0.75rem 1rem;
  border-radius: var(--radius);
  max-width: 85%;
  /*
   * UI Parity I6 (May 2026): swap word-break: break-word →
   * overflow-wrap: anywhere. break-word splits on character boundaries
   * mid-word ONLY when the existing soft-break opportunities don't fit,
   * which makes URLs and IDs disappear off the right edge in some
   * browsers before the engine attempts a hard split. `anywhere` is
   * the more aggressive sibling: it considers EVERY position a valid
   * break opportunity, so long unbreakable runs always wrap inside the
   * card. Pair with min-width: 0 on the flex parent so the bubble
   * shrinks to its container rather than forcing a horizontal
   * scrollbar on the chat pane. Matches OpenCode's overflow handling.
   */
  overflow-wrap: anywhere;
  min-width: 0;
  font-family: var(--font-mono);
}

.message-bubble.user {
  align-self: flex-end;
  background: var(--user-bubble);
  border: 1px solid var(--border);
}

.message-bubble.assistant {
  align-self: flex-start;
  background: var(--assistant-bubble);
  border: 1px solid var(--border);
}

.message-bubble.system {
  align-self: center;
  background: transparent;
  border: 1px dashed var(--border);
  opacity: 0.7;
  font-style: italic;
}

.message-role {
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-muted);
}

.message-bubble.user .message-role {
  color: var(--accent);
}
.message-bubble.assistant .message-role {
  color: var(--text-secondary);
}

.message-content {
  color: var(--text-primary);
  line-height: 1.6;
  white-space: pre-wrap;
  font-family: inherit;
}

/* Copy affordance row, anchored under the message body. Right-aligned to
 * keep the bubble's reading column clean. Matches the small-toolbar vibe
 * of the per-tool-card layout. */
.message-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.4rem;
  margin-top: 0.35rem;
}

.revert-button {
  background: none;
  border: none;
  cursor: pointer;
  font-size: 0.72rem;
  color: var(--text-muted);
  padding: 0.15rem 0.3rem;
  border-radius: var(--radius);
  font-family: inherit;
  transition: color 0.15s, background 0.15s;
}

.revert-button:hover {
  color: var(--accent);
  background: var(--bg-elevated);
}

/* Failed-send marker: shown when chatStore marks the optimistic user
 * message status='failed'. Persistent inline indicator paired with the
 * existing toast — minimum viable surfacing per PR-2 brief. Uses a danger
 * tint so it's visually distinct from the muted action buttons next to it. */
.failed-marker {
  font-size: 0.72rem;
  color: var(--danger, #f87171);
  padding: 0.15rem 0.3rem;
  user-select: none;
  letter-spacing: 0.02em;
}

/* Subtle red border on a failed user bubble so the failure is clear even
 * before the user reads the marker text. Doesn't replace the marker —
 * complements it. */
.message-bubble--failed.message-bubble.user {
  border-color: var(--danger, #f87171);
}

/* Tool blocks: collapsed by default, expand on click. opencode TUI vibe. */
.message-bubble.tool_result,
.message-bubble.tool_error {
  align-self: stretch;
  max-width: 100%;
  padding: 0;
  background: transparent;
  border: none;
}

/* Delegation cards: inline within main chat. */
.message-bubble.delegation,
.message-bubble.delegation_started {
  align-self: stretch;
  max-width: 100%;
  padding: 0;
  background: transparent;
  border: none;
}

.delegation-card {
  display: flex;
  gap: 0.5rem;
  align-items: flex-start;
  padding: 0.5rem 0.75rem;
  border: 1px solid var(--border);
  border-left: 2px solid var(--event-delegation, var(--accent));
  border-radius: var(--radius);
  background: var(--bg-elevated, transparent);
}

.delegation-card--inflight {
  border-left-color: var(--accent, #7aa2f7);
}

.delegation-content {
  margin: 0;
  font-size: 0.8rem;
  color: var(--text-secondary);
  white-space: pre-wrap;
  font-family: inherit;
  flex: 1;
}

.delegation-spinner {
  display: inline-block;
  color: var(--accent, #7aa2f7);
  font-weight: 700;
  animation: pulse 1.2s ease-in-out infinite;
}

.delegation-body {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  flex: 1;
  min-width: 0;
}

.delegation-header {
  display: flex;
  align-items: baseline;
  gap: 0.5rem;
  font-size: 0.75rem;
}

.delegation-agent-link {
  color: var(--accent, #7aa2f7);
  font-weight: 600;
  text-decoration: none;
  border-bottom: 1px dotted var(--accent, #7aa2f7);
}

.delegation-agent-link:hover {
  border-bottom-style: solid;
}

.delegation-elapsed {
  color: var(--text-muted);
  font-size: 0.7rem;
  font-variant-numeric: tabular-nums;
}

.delegation-progress {
  display: flex;
  gap: 0.4rem;
  font-size: 0.7rem;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

.delegation-progress-tool {
  color: var(--event-tool-call, var(--text-secondary));
}

@keyframes pulse {
  0%,
  100% {
    opacity: 0.4;
  }
  50% {
    opacity: 1;
  }
}

.thinking {
  font-style: italic;
  color: var(--text-muted);
  opacity: 0.8;
  font-size: 0.85rem;
  line-height: 1.5;
  margin: 0;
}

/* Thinking-only degraded-turn affordance.
 *
 * Visual language mirrors CriticalErrorBanner's layout (icon + title +
 * message stacked) but uses the warning (--warning) palette instead of
 * the red --error palette so the user can tell the two surfaces apart
 * at a glance. CriticalErrorBanner = fatal stream failure (red,
 * role="alert", anchored at top of chat). This = degraded turn
 * (amber, role="status", inline in the conversation flow). */
.thinking-only-affordance {
  display: flex;
  align-items: flex-start;
  gap: 0.6rem;
  padding: 0.65rem 0.85rem;
  border: 1px solid var(--warning, #e0af68);
  border-left-width: 3px;
  border-radius: var(--radius);
  background: var(--bg-elevated, transparent);
  color: var(--text-primary);
  font-size: 0.85rem;
}

.thinking-only-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.25rem;
  height: 1.25rem;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--warning, #e0af68);
  color: var(--bg-primary, #1a1b26);
  font-weight: 700;
  font-size: 0.85rem;
  line-height: 1;
}

.thinking-only-content {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  flex: 1;
  min-width: 0;
}

.thinking-only-title {
  font-weight: 600;
  color: var(--warning, #e0af68);
  font-size: 0.9rem;
}

.thinking-only-message {
  color: var(--text-secondary, var(--text-primary));
  word-wrap: break-word;
  line-height: 1.4;
}
</style>
