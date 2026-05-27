<script setup lang="ts">
import { computed } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import type { SessionSummary } from '@/types'

defineOptions({ name: 'ChildSessionsPanel' })

// ChildSessionsPanel is the persistent sibling of DelegationStrip.
//
// DelegationStrip renders transient swarm-bus delegation events that vanish
// after a page reload. ChildSessionsPanel reads the persistent session graph
// from chatStore.sessions and surfaces every child of the current session,
// so the user can navigate back into a delegated child even after a refresh.
//
// We intentionally coexist rather than replace DelegationStrip: the strip
// answers "what's happening right now in my swarm?" (live pulses during a
// long tool-loop), and this panel answers "what sessions did I delegate
// from this thread?" (durable state). Keep both mounted; auto-hide each
// when its data source is empty.
const chatStore = useChatStore()

const childSessions = computed<SessionSummary[]>(() => {
  const parentId = chatStore.currentSessionId
  if (!parentId) return []
  return chatStore.sessions
    .filter((s) => s.parentId === parentId)
    .slice()
    .sort((a, b) => a.createdAt.localeCompare(b.createdAt))
})

function formatTime(iso: string): string {
  if (!iso) return ''
  const parsed = new Date(iso)
  if (Number.isNaN(parsed.getTime())) return ''
  return parsed.toLocaleTimeString()
}

function rowTitle(child: SessionSummary): string {
  const trimmed = child.title?.trim() ?? ''
  if (trimmed.length > 0) return trimmed
  // Truncated id fallback when the backend has not yet titled the session.
  return child.id.length > 12 ? `${child.id.slice(0, 12)}…` : child.id
}

// Child Session Turn Registry Plumbing (May 2026) PR3 — backend-authoritative
// Live indicator. Reads `activeTurnId` straight off the SessionSummary the
// backend just shipped (handleListV1Sessions projects it from the Turn
// registry). A non-empty string means a Running Turn exists for this child;
// empty means idle. The FE-side `chatStore.streamingFor(child.id)` slot is
// intentionally NOT consulted here — child sessions are spawned by the
// engine, not by a user POST, so the parent-side optimistic UI gap that
// motivates `streamingFor` does not apply to child rows.
//
// Dual-source boundary: current-session surfaces (ChatView, MessageInput)
// keep reading `streamingFor` for optimistic UI between chat-send resolve
// and long-poll attach; child-session list surfaces (this panel,
// SessionBrowser, SessionSwitcher) read `activeTurnId`. See §R8 in the
// Child Session Turn Registry Plumbing plan for the drift-risk note.
function isStreaming(child: SessionSummary): boolean {
  return !!child.activeTurnId
}

// Sibling-confusion fix (May 2026 bug-hunt round 7) — route the
// click through chatStore.loadSessionForDelegation rather than
// calling loadSessionMessages directly. This is the same seam every
// delegated-session click surface uses (MessageBubble in-thread
// cards, DelegationPanel swarm-bus cards) so chainId routing,
// validated id hints, and the cold-reload backfill all apply
// uniformly. The pre-fix direct path bypassed the resolver — the
// six prior bug-fix commits (4607120b et al.) protected only the
// in-thread surface, so this panel kept re-opening the bug for
// users who navigated via the persistent child list.
//
// The hint chain: chainId (when known on the persisted Session)
// disambiguates same-agent siblings; childSessionId is the local
// id we already trust because it lives in chatStore.sessions; the
// agent-id fallback completes the chain for legacy data.
async function selectChild(child: SessionSummary): Promise<void> {
  await chatStore.loadSessionForDelegation({
    chainId: child.chainId,
    childSessionId: child.id,
    agentId: child.currentAgentId ?? child.agentId,
  })
}
</script>

<template>
  <section
    class="child-sessions-panel"
    :class="{ 'is-empty': childSessions.length === 0 }"
    data-testid="child-sessions-panel"
  >
    <template v-if="childSessions.length > 0">
      <header class="panel-header">
        <span class="panel-title">Delegated sessions</span>
        <span class="panel-counter">{{ childSessions.length }}</span>
      </header>
      <ul class="panel-list">
        <li
          v-for="child in childSessions"
          :key="child.id"
          class="panel-entry"
          :class="{ 'is-streaming': isStreaming(child) }"
          :data-testid="`child-session-row-${child.id}`"
          :title="rowTitle(child)"
          role="button"
          tabindex="0"
          @click="selectChild(child)"
          @keydown.enter.prevent="selectChild(child)"
          @keydown.space.prevent="selectChild(child)"
        >
          <span class="panel-icon" aria-hidden="true">⤷</span>
          <span class="panel-agent">{{ child.agentId }}</span>
          <span class="panel-summary">{{ rowTitle(child) }}</span>
          <span
            v-if="isStreaming(child)"
            class="panel-live"
            aria-label="Currently streaming"
            data-testid="child-session-streaming-dot"
          >●</span>
          <!--
            UX consolidation (May 2026) — redundant text label paired with
            the green border + green dot so the streaming affordance is
            robust to green colour-blindness. The dot alone fails for users
            who cannot perceive the colour shift; the word "Live" is the
            non-colour-dependent fallback.
          -->
          <span
            v-if="isStreaming(child)"
            class="panel-live-label"
            :data-testid="`child-session-streaming-label-${child.id}`"
          >Live</span>
          <span class="panel-time">{{ formatTime(child.createdAt) }}</span>
        </li>
      </ul>
    </template>
  </section>
</template>

<style scoped>
.child-sessions-panel {
  flex-shrink: 0;
  padding: 0.4rem 1rem;
  background: var(--bg-secondary);
  border-top: 1px solid var(--border);
  font-family: var(--font-mono);
}

.child-sessions-panel.is-empty {
  display: none;
}

.panel-header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-muted);
  margin-bottom: 0.3rem;
}

.panel-title {
  flex: 1;
}

.panel-counter {
  background: var(--accent);
  color: #fff;
  font-size: 0.65rem;
  padding: 0.1rem 0.35rem;
  border-radius: 10px;
}

.panel-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  max-height: 110px;
  overflow-y: auto;
}

.panel-entry {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.25rem 0.5rem;
  font-size: 0.75rem;
  color: var(--text-secondary);
  border: 1px solid var(--border);
  border-left: 2px solid var(--accent);
  border-radius: 0 var(--radius) var(--radius) 0;
  background: transparent;
  cursor: pointer;
  transition: background 0.15s;
}

.panel-entry:hover {
  background: var(--bg-elevated);
}

/*
 * UX consolidation (May 2026) — improved keyboard focus styling. The
 * pre-fix outline-offset:-2px clipped against the border, making focus
 * subtle. A positive offset lifts the ring clear of the row, a focus
 * background change gives a non-outline cue, and the increased outline
 * width makes the target obvious at the row's small visual height.
 */
.panel-entry:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
  background: var(--bg-elevated-hover, var(--bg-elevated));
}

.panel-entry.is-streaming {
  border-left-color: var(--accent-success, #4ade80);
}

.panel-icon {
  color: var(--accent);
  font-weight: 700;
}

.panel-agent {
  font-weight: 600;
  color: var(--text-primary);
}

.panel-summary {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.panel-live {
  color: var(--accent-success, #4ade80);
  font-size: 0.6rem;
  animation: child-sessions-pulse 1.5s ease-in-out infinite;
}

.panel-live-label {
  color: var(--accent-success, #4ade80);
  font-size: 0.65rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  flex-shrink: 0;
}

.panel-time {
  font-size: 0.68rem;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

@keyframes child-sessions-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}
</style>
