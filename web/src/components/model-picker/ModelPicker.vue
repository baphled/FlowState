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
  font-size: 0.8rem;
  color: var(--text-muted);
  cursor: pointer;
  padding: 0.15rem 0.4rem;
  border-radius: 3px;
  transition: color 0.15s, background 0.15s;
  user-select: none;
}
.model-picker:hover {
  color: var(--accent);
  background: var(--accent-bg);
}
</style>
