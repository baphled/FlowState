<script setup lang="ts">
import { onMounted, nextTick, watch, ref } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import { useSettingsStore } from '@/stores/settingsStore'
import MessageBubble from '@/components/chat/MessageBubble.vue'
import MessageInput from '@/components/chat/MessageInput.vue'
import EventCard from '@/components/swarm/EventCard.vue'
import { useSwarmStore } from '@/stores/swarmStore'

defineOptions({ name: 'ChatView' })

const chatStore = useChatStore()
const swarmStore = useSwarmStore()
const settingsStore = useSettingsStore()
const messageList = ref<HTMLElement | null>(null)

onMounted(async () => {
  await chatStore.loadModels()
  swarmStore.startPolling()
})

async function scrollToBottom(): Promise<void> {
  await nextTick()
  if (messageList.value) {
    messageList.value.scrollTop = messageList.value.scrollHeight
  }
}

watch(() => chatStore.messages.length, scrollToBottom)
</script>

<template>
  <div class="chat-view">
    <div class="primary-pane">
      <div
        ref="messageList"
        class="message-list"
        data-testid="message-list"
      >
        <div v-if="chatStore.messages.length === 0" class="empty-state">
          <p>Start a conversation with FlowState</p>
        </div>

        <MessageBubble
          v-for="(msg, idx) in chatStore.messages"
          :key="idx"
          :message="msg"
        />

        <div v-if="chatStore.isLoading" class="loading-indicator" data-testid="loading-indicator">
          <span class="dot" />
          <span class="dot" />
          <span class="dot" />
        </div>
      </div>

      <MessageInput />
    </div>

    <transition name="pane-slide">
      <aside
        v-show="settingsStore.swarmPaneVisible"
        class="swarm-pane"
        data-testid="swarm-pane"
      >
        <div class="swarm-header">
          <span>Swarm Activity</span>
          <button
            class="toggle-btn"
            data-testid="toggle-swarm-btn"
            @click="settingsStore.toggleSwarmPane()"
          >
            ✕
          </button>
        </div>
        <div class="swarm-events" data-testid="swarm-events">
          <EventCard
            v-for="event in swarmStore.events"
            :key="event.id"
            :event="event"
          />
          <p v-if="swarmStore.events.length === 0" class="swarm-empty">
            No events yet
          </p>
        </div>
      </aside>
    </transition>

    <button
      v-if="!settingsStore.swarmPaneVisible"
      class="show-swarm-btn"
      data-testid="show-swarm-btn"
      @click="settingsStore.toggleSwarmPane()"
    >
      Swarm
    </button>
  </div>
</template>

<style scoped>
.chat-view {
  display: flex;
  height: 100%;
  overflow: hidden;
}

.primary-pane {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-width: 0;
}

.message-list {
  flex: 1;
  overflow-y: auto;
  padding: 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.empty-state {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-muted);
  font-style: italic;
}

.loading-indicator {
  display: flex;
  gap: 0.3rem;
  padding: 0.5rem;
  align-self: flex-start;
}

.dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--text-muted);
  animation: bounce 1s infinite;
}

.dot:nth-child(2) { animation-delay: 0.15s; }
.dot:nth-child(3) { animation-delay: 0.3s; }

@keyframes bounce {
  0%, 80%, 100% { transform: scale(1); opacity: 0.4; }
  40% { transform: scale(1.2); opacity: 1; }
}

.swarm-pane {
  width: 30%;
  min-width: 220px;
  max-width: 360px;
  border-left: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  background: var(--bg-secondary);
  flex-shrink: 0;
}

.swarm-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--border);
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.08em;
}

.toggle-btn, .show-swarm-btn {
  background: none;
  border: 1px solid var(--border);
  color: var(--text-muted);
  cursor: pointer;
  border-radius: var(--radius);
  padding: 0.15rem 0.5rem;
  font-size: 0.75rem;
  transition: color 0.15s, border-color 0.15s;
}

.toggle-btn:hover, .show-swarm-btn:hover {
  color: var(--text-primary);
  border-color: var(--accent);
}

.show-swarm-btn {
  position: absolute;
  right: 0.75rem;
  bottom: 5.5rem;
  writing-mode: vertical-rl;
  text-orientation: mixed;
}

.swarm-events {
  flex: 1;
  overflow-y: auto;
  padding: 0.5rem;
}

.swarm-empty {
  color: var(--text-muted);
  font-size: 0.8rem;
  text-align: center;
  padding: 1rem 0;
}

.pane-slide-enter-active,
.pane-slide-leave-active {
  transition: width 0.25s ease, opacity 0.2s ease;
}

.pane-slide-enter-from,
.pane-slide-leave-to {
  width: 0;
  opacity: 0;
}

@media (max-width: 768px) {
  .swarm-pane { display: none; }
}
</style>
