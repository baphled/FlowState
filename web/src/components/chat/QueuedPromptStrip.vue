<script setup lang="ts">
/**
 * QueuedPromptStrip (Slice E — Streaming Coherence May 2026).
 *
 * Renders the per-session queue of prompts the user composed while a
 * prior turn was streaming. Each pill is clickable for revert: clicking
 * X pops the prompt out of the queue and pushes its text back onto
 * `composerText` so the user can edit-then-resend (mirrors the
 * `revertToMessage` edit pattern).
 *
 * Layout: rendered between the message thread and the composer
 * (mounted by ChatView). A queue with zero entries renders nothing —
 * the strip is invisible when there is nothing pending.
 */
import { computed } from 'vue'
import { useChatStore } from '@/stores/chatStore'

defineOptions({ name: 'QueuedPromptStrip' })

const store = useChatStore()

const queue = computed<string[]>(() => {
  const id = store.currentSessionId
  if (!id) return []
  return store.queuedPrompts[id] ?? []
})

function revert(index: number): void {
  if (!store.currentSessionId) return
  const removed = store.popQueuedPromptFor(store.currentSessionId, index)
  if (removed !== null) {
    // Push the prompt onto composerText so MessageInput's watcher
    // pre-fills the textarea (matches revertToMessage's contract).
    store.composerText = removed
  }
}
</script>

<template>
  <div
    v-if="queue.length > 0"
    class="queued-prompt-strip"
    data-testid="queued-prompt-strip"
    role="list"
    :aria-label="`${queue.length} queued prompts`"
  >
    <span class="queue-label">Queued:</span>
    <button
      v-for="(text, index) in queue"
      :key="`${index}-${text}`"
      class="queue-pill"
      data-testid="queued-prompt-pill"
      type="button"
      role="listitem"
      :title="`Revert: ${text}`"
      @click="revert(index)"
    >
      <span class="pill-text">{{ text }}</span>
      <span class="pill-x" aria-hidden="true">×</span>
    </button>
  </div>
</template>

<style scoped>
.queued-prompt-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 0.4rem;
  padding: 0.4rem 1rem;
  background: var(--bg-secondary);
  border-top: 1px solid var(--border);
  flex-shrink: 0;
  font-size: 0.78rem;
}

.queue-label {
  color: var(--text-muted);
  letter-spacing: 0.02em;
}

.queue-pill {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  padding: 0.2rem 0.6rem;
  border-radius: 999px;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  color: var(--text-primary);
  cursor: pointer;
  max-width: 320px;
  font: inherit;
}

.queue-pill:hover {
  border-color: var(--accent);
}

.pill-text {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.pill-x {
  color: var(--text-muted);
  font-weight: 700;
}

.queue-pill:hover .pill-x {
  color: var(--accent);
}
</style>
