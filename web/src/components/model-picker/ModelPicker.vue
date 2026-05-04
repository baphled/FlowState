<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import { type FuzzySearchItem } from '@/composables/useFuzzyFilter'
import FuzzySearchModal from '@/components/common/FuzzySearchModal.vue'

defineOptions({ name: 'ModelPicker' })

const props = withDefaults(
  defineProps<{
    readonly?: boolean
  }>(),
  {
    readonly: false,
  },
)

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
  if (props.readonly) {
    return
  }

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
    :class="{ 'is-readonly': props.readonly }"
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
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--text-secondary, var(--text-primary));
  cursor: pointer;
  padding: 0.25rem 0.5rem;
  border-radius: 4px;
  background: transparent;
  transition: background 0.15s;
  user-select: none;
  white-space: nowrap;
}

.model-picker:hover {
  background: var(--surface-hover, rgba(255, 255, 255, 0.06));
  color: var(--accent);
}

.model-picker.is-readonly {
  color: var(--text-muted);
  cursor: default;
  opacity: 0.65;
}

.model-picker.is-readonly:hover {
  background: transparent;
  color: var(--text-muted);
}
</style>
