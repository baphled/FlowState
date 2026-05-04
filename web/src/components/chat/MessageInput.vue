<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import FuzzySearchModal from '@/components/common/FuzzySearchModal.vue'
import { detectTrigger, insertToken, type TriggerDescriptor } from '@/composables/useInputTriggers'
import { SLASH_COMMANDS } from '@/commands/slashCommands'
import type { FuzzySearchItem } from '@/composables/useFuzzyFilter'
import { showToast } from '@/composables/useToast'

defineOptions({ name: 'MessageInput' })

const store = useChatStore()
const inputText = ref('')
const textareaRef = ref<HTMLTextAreaElement | null>(null)

// activeTrigger drives picker visibility. The caret-tracking handlers
// recompute it on every input/keyup/click, so it always reflects the
// latest textarea state.
const activeTrigger = ref<TriggerDescriptor | null>(null)

// Static slash-command catalogue mirrored from the TUI registry. See
// web/src/commands/slashCommands.ts for the canonical source-of-truth
// notes. Mapped to FuzzySearchItem for the modal.
const commandItems = computed<FuzzySearchItem[]>(() =>
  SLASH_COMMANDS.map((cmd) => ({
    id: cmd.name,
    label: `/${cmd.name}`,
    meta: cmd.description,
  })),
)

// Combined agent + swarm surface for "@" mentions. Swarms have no read
// API yet so the slice is empty until one lands; the picker still works
// for agents alone.
const mentionItems = computed<FuzzySearchItem[]>(() => {
  const agents = store.availableAgentDetails.map<FuzzySearchItem>((agent) => ({
    id: agent.id,
    label: `@${agent.id}`,
    group: 'Agents',
    meta: agent.name,
  }))
  // Swarm slice intentionally empty — see InputTriggerPickers note for
  // backend wiring TODO. Keep the group present so the surface is
  // discoverable when the data lands.
  return agents
})

// Group label varies between the two pickers — slash commands have no
// group so they render as a flat list.
const slashOpen = computed(() => activeTrigger.value?.kind === 'slash')
const mentionOpen = computed(() => activeTrigger.value?.kind === 'mention')

// Initial-fragment seed pushed into the modal's search input so the
// user's typing-as-they-trigger filters the list immediately rather
// than only after they type inside the modal.
const initialQuery = computed(() => activeTrigger.value?.fragment ?? '')

function handleInput(): void {
  autoResize()
  recomputeTrigger()
}

function recomputeTrigger(): void {
  const el = textareaRef.value
  if (!el) {
    activeTrigger.value = null
    return
  }
  // Read straight from the DOM element instead of `inputText.value` —
  // when this fires inside the same input event as v-model's auto-
  // generated listener, the reactive ref may not have been assigned
  // yet. The element's `.value` is always authoritative.
  activeTrigger.value = detectTrigger(el.value, el.selectionStart ?? 0)
}

function handleKeydown(event: KeyboardEvent): void {
  // Esc closes any open picker without losing the buffer.
  if (event.key === 'Escape' && activeTrigger.value !== null) {
    event.preventDefault()
    activeTrigger.value = null
    return
  }

  // Arrow keys / Enter / Esc inside an open picker are owned by the
  // FuzzySearchModal's own document-level handler, so the textarea
  // stays out of the way. Submitting on Enter only fires when no
  // picker is open.
  if (activeTrigger.value !== null) {
    return
  }

  if (event.key === 'Enter' && !event.shiftKey && !event.altKey) {
    event.preventDefault()
    submit()
  }
}

function autoResize(): void {
  const el = textareaRef.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = `${Math.min(el.scrollHeight, 200)}px`
}

async function submit(): Promise<void> {
  const text = inputText.value.trim()
  if (!text) return
  // Pre-fix this branch silently early-returned when isLoading was true.
  // The user typed, pressed Enter, and saw nothing — leading them to
  // conclude the chat was stuck. Now we surface the rejection via a
  // toast so the cause is unambiguous. We keep the buffer intact so the
  // user doesn't lose what they typed; closing the picker still happens
  // because the rejection is a UI dead end either way.
  if (store.isLoading) {
    showToast({
      message: 'An earlier message is still in flight. Wait for it to finish or reload the page.',
      title: 'Send blocked',
      variant: 'error',
    })
    return
  }
  inputText.value = ''
  activeTrigger.value = null
  await store.sendMessage(text)
}

async function applySelection(item: FuzzySearchItem): Promise<void> {
  const trigger = activeTrigger.value
  if (!trigger) return
  // Reject cross-picker contamination: a slash-command item must not be
  // applied when the mention trigger is active, and vice versa. The hidden
  // picker (v-show, not v-if) can fire @select with its top item before the
  // visible picker fires, inserting the wrong token at the wrong position.
  if (trigger.kind === 'slash' && !item.label.startsWith('/')) return
  if (trigger.kind === 'mention' && !item.label.startsWith('@')) return
  const result = insertToken(inputText.value, trigger, item.label)
  inputText.value = result.text
  activeTrigger.value = null
  await nextTick()
  const el = textareaRef.value
  if (el) {
    el.focus()
    el.selectionStart = result.caret
    el.selectionEnd = result.caret
  }
}

function closePicker(): void {
  activeTrigger.value = null
}

onMounted(() => {
  // Agents may not be loaded yet when the input mounts inside a fresh
  // chat session — kick it off so the @-picker has something to show.
  if (store.availableAgentDetails.length === 0) {
    void store.loadAgents()
  }
})

// Watch composerText so a revert-to-message action pre-fills the textarea.
// We consume the value immediately and reset it to '' so subsequent renders
// do not re-apply the same text.
watch(
  () => store.composerText,
  (text) => {
    if (text) {
      inputText.value = text
      store.composerText = ''
      void nextTick(() => {
        autoResize()
        textareaRef.value?.focus()
      })
    }
  },
)
</script>

<template>
  <div class="message-input-wrap" data-testid="message-input-wrap">
    <div class="input-row">
      <textarea
        v-model="inputText"
        ref="textareaRef"
        class="message-input"
        data-testid="message-input"
        placeholder="Type a message… (Enter to send, Shift+Enter or Alt+Enter for newline)"
        rows="1"
        @input="handleInput"
        @keyup="recomputeTrigger"
        @click="recomputeTrigger"
        @keydown="handleKeydown"
      />

      <button
        class="send-button"
        data-testid="send-button"
        :disabled="!inputText.trim()"
        @click="submit"
      >
        {{ store.isLoading ? '…' : 'Send' }}
      </button>
    </div>

    <p v-if="store.error" class="input-error" data-testid="chat-error">
      {{ store.error }}
    </p>

    <p class="input-hint">Enter to send · Shift+Enter / Alt+Enter for newline · "/" commands · "@" agents</p>

    <!--
      Slash and mention pickers reuse FuzzySearchModal — the same scaffolding
      that backs the toolbar AgentPicker / ModelPicker — so there is exactly
      one fuzzy-search overlay pattern in the app. Mutually exclusive: only
      one of slashOpen / mentionOpen is true at a time, driven by detectTrigger.
    -->
    <FuzzySearchModal
      :items="commandItems"
      :open="slashOpen"
      :initial-query="initialQuery"
      :focus-on-open="false"
      placeholder="Search commands..."
      empty-message="No matching commands"
      @select="applySelection"
      @close="closePicker"
    />

    <FuzzySearchModal
      :items="mentionItems"
      :open="mentionOpen"
      :initial-query="initialQuery"
      :focus-on-open="false"
      placeholder="Search agents and swarms..."
      empty-message="No matching agents or swarms"
      @select="applySelection"
      @close="closePicker"
    />
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
  max-height: 200px;
  overflow-y: auto;
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
