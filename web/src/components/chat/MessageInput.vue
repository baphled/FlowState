<script setup lang="ts">
import { ref } from 'vue'
import { useChatStore } from '@/stores/chatStore'

defineOptions({ name: 'MessageInput' })

const store = useChatStore()
const inputText = ref('')

function handleKeydown(event: KeyboardEvent): void {
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault()
    submit()
  }
}

async function submit(): Promise<void> {
  const text = inputText.value.trim()
  if (!text || store.isLoading) return
  inputText.value = ''
  await store.sendMessage(text)
}
</script>

<template>
  <div class="message-input-wrap" data-testid="message-input-wrap">
    <div class="input-row">
      <select
        v-model="store.model"
        class="model-select"
        data-testid="model-select"
        :disabled="store.isLoading"
      >
        <option
          v-for="m in store.availableModels"
          :key="m"
          :value="m"
        >
          {{ m }}
        </option>
      </select>

      <textarea
        v-model="inputText"
        class="message-input"
        data-testid="message-input"
        placeholder="Type a message… (Enter to send, Shift+Enter for newline)"
        rows="3"
        :disabled="store.isLoading"
        @keydown="handleKeydown"
      />

      <button
        class="send-button"
        data-testid="send-button"
        :disabled="store.isLoading || !inputText.trim()"
        @click="submit"
      >
        {{ store.isLoading ? '…' : 'Send' }}
      </button>
    </div>

    <p v-if="store.error" class="input-error" data-testid="chat-error">
      {{ store.error }}
    </p>

    <p class="input-hint">Enter to send · Shift+Enter for newline</p>
  </div>
</template>

<style scoped>
.message-input-wrap {
  padding: 0.75rem 1rem;
  border-top: 1px solid var(--border);
  background: var(--bg-secondary);
  flex-shrink: 0;
}

.input-row {
  display: flex;
  gap: 0.5rem;
  align-items: flex-end;
}

.model-select {
  background: var(--bg-elevated);
  color: var(--text-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 0.3rem 0.5rem;
  font-size: 0.8rem;
  font-family: var(--font-mono);
  cursor: pointer;
  flex-shrink: 0;
}

.message-input {
  flex: 1;
  background: var(--bg-elevated);
  color: var(--text-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 0.5rem 0.75rem;
  font-family: var(--font-sans);
  font-size: 0.95rem;
  resize: none;
  line-height: 1.5;
  transition: border-color 0.15s;
}

.message-input:focus {
  outline: none;
  border-color: var(--accent);
}

.send-button {
  background: var(--accent);
  color: var(--bg-primary);
  border: none;
  border-radius: var(--radius);
  padding: 0.5rem 1.25rem;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.15s, opacity 0.15s;
  flex-shrink: 0;
  align-self: flex-end;
  height: 38px;
}

.send-button:hover:not(:disabled) { background: var(--accent-hover); }
.send-button:disabled { opacity: 0.4; cursor: not-allowed; }

.input-error {
  color: var(--error);
  font-size: 0.8rem;
  margin-top: 0.25rem;
}

.input-hint {
  font-size: 0.72rem;
  color: var(--text-muted);
  margin-top: 0.25rem;
}
</style>
