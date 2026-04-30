<script setup lang="ts">
import type { Message } from '@/types'

defineOptions({ name: 'MessageBubble' })

const props = defineProps<{ message: Message }>()
</script>

<template>
  <div
    class="message-bubble"
    :class="props.message.role"
    :data-testid="`message-${props.message.role}`"
    :data-role="props.message.role"
  >
    <span class="message-role">{{ props.message.role }}</span>
    <p class="message-content">{{ props.message.content }}</p>
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
  font-family: var(--font-mono);
}

.message-bubble.user .message-role { color: var(--accent); }
.message-bubble.assistant .message-role { color: var(--text-secondary); }

.message-content {
  color: var(--text-primary);
  line-height: 1.6;
  white-space: pre-wrap;
}
</style>
