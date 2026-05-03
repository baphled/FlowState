<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import FuzzySearchModal from '@/components/common/FuzzySearchModal.vue'
import type { FuzzySearchItem } from '@/composables/useFuzzyFilter'

defineOptions({ name: 'AgentPicker' })

const chatStore = useChatStore()

const isOpen = ref(false)

const currentAgent = computed(() =>
  chatStore.availableAgentDetails.find((a) => a.id === chatStore.agentId),
)

const label = computed(() => currentAgent.value?.name ?? 'Select agent')

const agentItems = computed<FuzzySearchItem[]>(() =>
  chatStore.availableAgentDetails.map((agent) => ({
    id: agent.id,
    label: agent.name,
    group: '',
    meta: agent.description,
  })),
)

function openPicker(): void {
  isOpen.value = true
}

function handleSelect(item: FuzzySearchItem): void {
  void chatStore.setAgent(item.id)
  isOpen.value = false
}

function handleClose(): void {
  isOpen.value = false
}

onMounted(() => {
  if (chatStore.availableAgentDetails.length === 0) {
    void chatStore.loadAgents()
  }
})
</script>

<template>
  <span
    class="agent-picker"
    data-testid="agent-picker"
    @click="openPicker"
  >
    {{ label }}
  </span>

  <FuzzySearchModal
    :items="agentItems"
    :open="isOpen"
    placeholder="Search agents..."
    empty-message="No agents found"
    @select="handleSelect"
    @close="handleClose"
  />
</template>

<style scoped>
.agent-picker {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--text-muted);
  cursor: pointer;
  padding: 0.2rem 0.55rem;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--bg-elevated);
  max-width: 180px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  transition: color 0.15s, border-color 0.15s, background 0.15s;
  user-select: none;
}

.agent-picker::after {
  content: '\25BE';
  font-size: 0.65rem;
  flex-shrink: 0;
  opacity: 0.6;
}

.agent-picker:hover {
  color: var(--accent);
  border-color: var(--accent);
  background: var(--accent-bg);
}
</style>
