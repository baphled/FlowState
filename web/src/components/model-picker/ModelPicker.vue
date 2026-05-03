<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import { type FuzzySearchItem } from '@/composables/useFuzzyFilter'
import FuzzySearchModal from '@/components/common/FuzzySearchModal.vue'

defineOptions({ name: 'ModelPicker' })

const chatStore = useChatStore()
const isOpen = ref(false)

const currentModelLabel = computed(() => {
  if (!chatStore.currentModelId) {
    return 'Select model'
  }
  return chatStore.currentModelId
})

const fuzzyItems = computed<FuzzySearchItem[]>(() =>
  chatStore.availableModels.map((model) => ({
    id: `${model.providerId}:${model.id}`,
    label: model.name || model.id,
    group: model.providerId,
  })),
)

function openModal(): void {
  isOpen.value = true
}

function closeModal(): void {
  isOpen.value = false
}

async function handleSelect(item: FuzzySearchItem): Promise<void> {
  const separatorIndex = item.id.indexOf(':')
  const providerId = item.id.slice(0, separatorIndex)
  const modelId = item.id.slice(separatorIndex + 1)
  await chatStore.setModel(modelId, providerId)
  closeModal()
}

onMounted(() => {
  if (chatStore.availableModels.length === 0) {
    chatStore.loadModels()
  }
})
</script>

<template>
  <span
    class="model-picker"
    data-testid="model-picker"
    @click="openModal"
  >
    {{ currentModelLabel }}
  </span>

  <FuzzySearchModal
    :items="fuzzyItems"
    :open="isOpen"
    placeholder="Search models..."
    empty-message="No models found"
    @select="handleSelect"
    @close="closeModal"
  />
</template>

<style scoped>
.model-picker {
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
  max-width: 240px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  transition: color 0.15s, border-color 0.15s, background 0.15s;
  user-select: none;
}

.model-picker::after {
  content: '\25BE';
  font-size: 0.65rem;
  flex-shrink: 0;
  opacity: 0.6;
}

.model-picker:hover {
  color: var(--accent);
  border-color: var(--accent);
  background: var(--accent-bg);
}
</style>
