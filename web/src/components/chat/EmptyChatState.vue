<script setup lang="ts">
import { computed } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import Icon from '@/components/common/Icon.vue'

defineOptions({ name: 'EmptyChatState' })

/**
 * UI Parity I10 (May 2026) — empty-state component.
 *
 * Replaces the bare "Start a conversation with the selected agent."
 * line at `ChatView.vue:507-509`. Surfaces three affordances so a
 * fresh session is immediately actionable:
 *
 *   1. Agent card — name + model id + provider, so the user can
 *      confirm at a glance which agent will receive their first
 *      prompt. Falls back gracefully when any of the three values
 *      hasn't loaded yet.
 *
 *   2. Example-prompt chips — four short prompts spanning common
 *      power-user shapes (one explore, one plan, one implement, one
 *      diagnose). Clicking a chip pre-fills `chatStore.composerText`
 *      which MessageInput watches and applies to the textarea.
 *
 *   3. `/help` button — same pre-fill mechanism, writes `/help` so
 *      the existing slash-command picker surfaces the full list.
 *
 * Out of scope (per the cleanup brief):
 *   - i18n of the example prompts (English-only is fine for now;
 *     surface a flag when an i18n layer lands).
 *   - Per-agent example prompts (one shared list is enough until we
 *     have evidence that per-agent tailoring would help).
 */

const chatStore = useChatStore()

const currentAgent = computed(() =>
  chatStore.availableAgentDetails.find((a) => a.id === chatStore.agentId),
)

const agentName = computed(() => currentAgent.value?.name || chatStore.agentId || 'Agent')

/**
 * Prefer the live `chatStore.currentModelId` (mirrors the actively
 * streaming model after a failover) over the agent's catalogue
 * `model` field. Falls back to the catalogue if no current model is
 * set (i.e. fresh session before the first message).
 */
const modelLabel = computed(() => {
  const live = chatStore.currentModelId
  if (live) return live
  return currentAgent.value?.model || ''
})

const providerLabel = computed(() => {
  const live = chatStore.currentProviderId
  if (live) return live
  return currentAgent.value?.provider || ''
})

/**
 * Example prompts — kept short so the chips don't overflow the empty
 * state area on narrow viewports. Three commonly-useful entry shapes
 * for FlowState's typical user flow.
 */
const examplePrompts: readonly string[] = [
  'Explore the repository and summarise the architecture',
  'Help me plan a new feature from scratch',
  'Find and fix a bug in this codebase',
  'Explain how a specific function works',
] as const

function applyExample(prompt: string): void {
  chatStore.composerText = prompt
}

function applyHelp(): void {
  chatStore.composerText = '/help'
}
</script>

<template>
  <div class="empty-chat-state" data-testid="chat-empty-state">
    <!--
      Agent card — name + model + provider. The card is dimmed (text-muted
      colour) so it reads as informational chrome rather than active UI.
    -->
    <div class="empty-state-agent-card" data-testid="empty-state-agent-card">
      <span class="empty-state-agent-icon" aria-hidden="true">
        <Icon name="bot" :size="20" />
      </span>
      <div class="empty-state-agent-meta">
        <div class="empty-state-agent-name">{{ agentName }}</div>
        <div v-if="modelLabel || providerLabel" class="empty-state-agent-model-line">
          <template v-if="modelLabel">{{ modelLabel }}</template>
          <template v-if="modelLabel && providerLabel"> · </template>
          <template v-if="providerLabel">{{ providerLabel }}</template>
        </div>
      </div>
    </div>

    <p class="empty-state-prompt">Try one of these to get started:</p>

    <!--
      Example-prompt chips — clicking pre-fills the composer via
      chatStore.composerText. MessageInput's watcher on composerText
      consumes the value, sets the textarea, and clears the store-side
      mirror so subsequent renders do not re-apply the same prompt.
    -->
    <div class="empty-state-chip-row">
      <button
        v-for="(prompt, idx) in examplePrompts"
        :key="prompt"
        type="button"
        class="empty-state-chip"
        :data-testid="`empty-state-example-${idx}`"
        @click="applyExample(prompt)"
      >
        {{ prompt }}
      </button>
    </div>

    <!--
      /help affordance — same pre-fill mechanism. Surfaces the slash
      registry once the user submits (or via the existing slash picker
      keyed by "/"). Rendered as a discrete button (not a chip) so it
      reads as a distinct kind of action.
    -->
    <div class="empty-state-help-row">
      <button
        type="button"
        class="empty-state-help-button"
        data-testid="empty-state-help-button"
        @click="applyHelp"
      >
        Or type
        <code>/help</code>
        for all commands
      </button>
    </div>
  </div>
</template>

<style scoped>
.empty-chat-state {
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 1rem;
  padding: 2rem 1rem;
  color: var(--text-muted);
  text-align: center;
}

.empty-state-agent-card {
  display: inline-flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.75rem 1.25rem;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}

.empty-state-agent-icon {
  color: var(--accent);
  display: inline-flex;
  align-items: center;
}

.empty-state-agent-meta {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 0.1rem;
}

.empty-state-agent-name {
  font-size: 0.95rem;
  font-weight: 600;
  color: var(--text-primary);
}

.empty-state-agent-model-line {
  font-size: 0.75rem;
  color: var(--text-muted);
  letter-spacing: 0.01em;
}

.empty-state-prompt {
  margin: 0;
  font-size: 0.85rem;
}

.empty-state-chip-row {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 0.5rem;
  max-width: 36rem;
}

.empty-state-chip {
  padding: 0.5rem 0.85rem;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: 999px;
  color: var(--text-primary);
  font-size: 0.85rem;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s, transform 0.15s;
}

.empty-state-chip:hover,
.empty-state-chip:focus-visible {
  border-color: var(--accent);
  background: var(--bg-secondary);
  outline: none;
  transform: translateY(-1px);
}

.empty-state-help-row {
  margin-top: 0.5rem;
}

.empty-state-help-button {
  background: transparent;
  border: none;
  color: var(--text-muted);
  font-size: 0.8rem;
  cursor: pointer;
  padding: 0.25rem 0.5rem;
  border-radius: var(--radius);
  transition: color 0.15s;
}

.empty-state-help-button:hover,
.empty-state-help-button:focus-visible {
  color: var(--text-primary);
  outline: none;
}

.empty-state-help-button code {
  background: var(--bg-elevated);
  padding: 0.05rem 0.35rem;
  border-radius: 4px;
  font-family: monospace;
  font-size: 0.8rem;
  color: var(--accent);
}
</style>
