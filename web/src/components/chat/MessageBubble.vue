<script setup lang="ts">
import { computed } from 'vue'
import type { Message } from '@/types'

defineOptions({ name: 'MessageBubble' })

const props = defineProps<{ message: Message }>()

const isToolRole = computed(() =>
  ['tool_call', 'tool_result', 'tool_error'].includes(props.message.role),
)
const isDelegationStarted = computed(() => props.message.role === 'delegation_started')
const isDelegation = computed(() => props.message.role === 'delegation')
const isThinking = computed(() => props.message.role === 'thinking')
const isPlain = computed(
  () =>
    !isToolRole.value &&
    !isDelegationStarted.value &&
    !isDelegation.value &&
    !isThinking.value,
)

const toolSummary = computed(() => {
  const name = props.message.toolName || props.message.role
  const input = props.message.toolInput
  return input ? `${name} ${input}` : name
})
</script>

<template>
  <div
    class="message-bubble"
    :class="props.message.role"
    :data-testid="`message-${props.message.role}`"
    :data-role="props.message.role"
  >
    <details v-if="isToolRole" class="tool-block">
      <summary class="tool-summary">
        <span class="tool-glyph" aria-hidden="true">▸</span>
        <span class="tool-name">{{ toolSummary }}</span>
      </summary>
      <pre class="tool-content">{{ props.message.content }}</pre>
    </details>

    <div v-else-if="isDelegationStarted" class="delegation-card delegation-card--inflight">
      <span data-testid="delegation-spinner" class="delegation-spinner" aria-hidden="true">⋯</span>
      <pre class="delegation-content">{{ props.message.content }}</pre>
    </div>

    <div v-else-if="isDelegation" class="delegation-card delegation-card--done">
      <pre class="delegation-content">{{ props.message.content }}</pre>
    </div>

    <p v-else-if="isThinking" class="thinking">{{ props.message.content }}</p>

    <template v-else-if="isPlain">
      <span class="message-role">{{ props.message.role }}</span>
      <p class="message-content">{{ props.message.content }}</p>
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
  word-break: break-word;
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

/* Tool blocks: collapsed by default, expand on click. opencode TUI vibe. */
.message-bubble.tool_call,
.message-bubble.tool_result,
.message-bubble.tool_error {
  align-self: stretch;
  max-width: 100%;
  padding: 0.25rem 0.5rem;
  background: transparent;
  border: 1px solid var(--border);
  border-left: 2px solid var(--event-tool-call, var(--text-muted));
}

.message-bubble.tool_error {
  border-left-color: var(--error, #f7768e);
}

.tool-block {
  width: 100%;
}

.tool-summary {
  cursor: pointer;
  list-style: none;
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.8rem;
  color: var(--text-secondary);
  padding: 0.15rem 0;
  user-select: none;
}

.tool-summary::-webkit-details-marker {
  display: none;
}

.tool-glyph {
  display: inline-block;
  transition: transform 0.15s ease;
  color: var(--text-muted);
}

.tool-block[open] .tool-glyph {
  transform: rotate(90deg);
}

.tool-name {
  color: var(--event-tool-call, var(--text-primary));
  font-weight: 600;
}

.message-bubble.tool_error .tool-name {
  color: var(--error, #f7768e);
}

.tool-content {
  margin: 0.4rem 0 0.2rem 1rem;
  padding: 0.5rem 0.75rem;
  background: var(--bg-elevated, rgba(0, 0, 0, 0.2));
  border-radius: 4px;
  font-size: 0.78rem;
  color: var(--text-primary);
  white-space: pre-wrap;
  overflow-x: auto;
  line-height: 1.4;
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
</style>
