<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useChatStore } from "@/stores/chatStore";
import FuzzySearchModal from "@/components/common/FuzzySearchModal.vue";
import type { FuzzySearchItem } from "@/composables/useFuzzyFilter";

defineOptions({ name: "AgentPicker" });

const props = withDefaults(
  defineProps<{
    readonly?: boolean;
  }>(),
  {
    readonly: false,
  },
);

const chatStore = useChatStore();

const isOpen = ref(false);

const currentAgent = computed(() =>
  chatStore.availableAgentDetails.find((a) => a.id === chatStore.agentId),
);

const label = computed(() => currentAgent.value?.name ?? "Select agent");

const agentItems = computed<FuzzySearchItem[]>(() =>
  chatStore.availableAgentDetails.map((agent) => ({
    id: agent.id,
    label: agent.name,
    group: "",
    meta: agent.description,
  })),
);

function openPicker(): void {
  if (props.readonly) {
    return;
  }

  isOpen.value = true;
}

function handleSelect(item: FuzzySearchItem): void {
  void chatStore.setAgent(item.id);
  isOpen.value = false;
}

function handleClose(): void {
  isOpen.value = false;
}

onMounted(() => {
  if (chatStore.availableAgentDetails.length === 0) {
    void chatStore.loadAgents();
  }
});
</script>

<template>
  <span
    class="agent-picker"
    :class="{ 'is-readonly': props.readonly }"
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
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--accent);
  cursor: pointer;
  padding: 0.25rem 0.5rem;
  border-radius: 4px;
  background: transparent;
  transition: background 0.15s;
  user-select: none;
  white-space: nowrap;
}

.agent-picker:hover {
  background: var(--surface-hover, rgba(255, 255, 255, 0.06));
}

.agent-picker.is-readonly {
  color: var(--text-muted);
  cursor: default;
  opacity: 0.65;
}

.agent-picker.is-readonly:hover {
  background: transparent;
}
</style>
