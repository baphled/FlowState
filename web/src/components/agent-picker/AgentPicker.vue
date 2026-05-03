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
  font-size: 0.8rem;
  color: var(--text-muted);
  cursor: pointer;
  padding: 0.15rem 0.4rem;
  border-radius: 3px;
  transition: color 0.15s, background 0.15s;
  user-select: none;
}

.agent-picker:hover {
  color: var(--accent);
  background: var(--accent-bg);
}
</style>
