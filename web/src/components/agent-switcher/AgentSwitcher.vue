<script setup lang="ts">
import type { Agent } from "@/types";
import { ref, onMounted, computed } from "vue";
import { useChatStore } from "@/stores/chatStore";
import Icon from "@/components/common/Icon.vue";

defineOptions({ name: "AgentSwitcher" });

const chatStore = useChatStore();
const isOpen = ref(false);

const currentAgentName = computed(() => {
  const agent = chatStore.availableAgentDetails.find(
    (a: Agent) => a.id === chatStore.agentId,
  );
  return agent?.name ?? chatStore.agentId;
});

const currentAgentSummary = computed(() => {
  const agent = chatStore.availableAgentDetails.find(
    (a: Agent) => a.id === chatStore.agentId,
  );
  const details = [agent?.provider, agent?.model].filter(Boolean);
  return details.length > 0
    ? details.join(" · ")
    : "No model metadata available";
});

const agentOptions = computed(() => {
  return chatStore.availableAgentDetails.map((agent: Agent) => ({
    id: agent.id,
    name: agent.name,
    description: agent.description,
    model: [agent.provider, agent.model].filter(Boolean).join(" · "),
  }));
});

function selectAgent(id: string): void {
  chatStore.setAgent(id);
  isOpen.value = false;
}

function toggleDropdown(): void {
  isOpen.value = !isOpen.value;
}

function closeDropdown(): void {
  isOpen.value = false;
}

onMounted(() => {
  void chatStore.loadAgents();
});
</script>

<template>
  <div class="agent-switcher" data-testid="agent-switcher">
    <button
      class="agent-switcher-trigger"
      @click="toggleDropdown"
      aria-haspopup="listbox"
      :aria-expanded="isOpen"
    >
      <span class="agent-icon"><Icon name="bot" :size="14" /></span>
      <span class="agent-labels">
        <span class="agent-name">{{ currentAgentName }}</span>
        <span class="agent-summary" data-testid="current-agent-summary">{{
          currentAgentSummary
        }}</span>
      </span>
      <span class="dropdown-arrow" :class="{ open: isOpen }">▾</span>
    </button>
    <ul v-if="isOpen" class="agent-dropdown" role="listbox">
      <li
        v-for="agent in agentOptions"
        :key="agent.id"
        class="agent-option"
        :class="{ active: agent.id === chatStore.agentId }"
        @click="selectAgent(agent.id)"
        role="option"
        :aria-selected="agent.id === chatStore.agentId"
      >
        <span class="option-name">{{ agent.name }}</span>
        <span v-if="agent.description" class="option-desc">{{
          agent.description
        }}</span>
        <span v-if="agent.model" class="option-meta">{{ agent.model }}</span>
      </li>
    </ul>
    <div v-if="isOpen" class="dropdown-backdrop" @click="closeDropdown" />
  </div>
</template>

<style scoped>
.agent-switcher {
  position: relative;
  display: inline-flex;
}

.agent-switcher-trigger {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.25rem 0.6rem;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  cursor: pointer;
  font-size: 0.85rem;
  color: var(--text-primary);
  transition:
    background 0.15s,
    border-color 0.15s;
}

.agent-labels {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 0.1rem;
}

.agent-switcher-trigger:hover {
  border-color: var(--accent);
}

.agent-icon {
  font-size: 0.9rem;
}

.agent-name {
  font-weight: 500;
}

.agent-summary {
  font-size: 0.7rem;
  color: var(--text-muted);
  line-height: 1.1;
}

.dropdown-arrow {
  font-size: 0.7rem;
  color: var(--text-muted);
  transition: transform 0.15s;
}

.dropdown-arrow.open {
  transform: rotate(180deg);
}

.agent-dropdown {
  position: absolute;
  top: 100%;
  left: 0;
  margin-top: 0.25rem;
  min-width: 180px;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
  z-index: 100;
  list-style: none;
  padding: 0.25rem 0;
}

.agent-option {
  padding: 0.5rem 0.75rem;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  transition: background 0.1s;
}

.agent-option:hover {
  background: var(--bg-secondary);
}

.agent-option.active {
  background: var(--accent-bg);
  color: var(--accent);
}

.option-name {
  font-weight: 500;
  font-size: 0.9rem;
}

.option-desc {
  font-size: 0.75rem;
  color: var(--text-muted);
}

.option-meta {
  font-size: 0.72rem;
  color: var(--accent);
}

.dropdown-backdrop {
  position: fixed;
  inset: 0;
  z-index: 99;
}
</style>
