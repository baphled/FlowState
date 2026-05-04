<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useChatStore } from '@/stores/chatStore'
import { type FuzzySearchItem } from '@/composables/useFuzzyFilter'
import FuzzySearchModal from '@/components/common/FuzzySearchModal.vue'
import type { Agent, Model, ModelPreference } from '@/types'

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

// activeAgent looks up the manifest for the currently-selected agent
// from the store. Returned undefined when there is no active agent —
// the resolver below treats that as "no policy", i.e. fully permissive.
const activeAgent = computed<Agent | undefined>(() =>
  chatStore.availableAgentDetails.find((a) => a.id === chatStore.agentId),
)

// isPreferred reports whether the supplied provider/model pair is in
// the agent's PreferredModels list. The test is independent of policy
// — the preferred list is a recommendation regardless of strictness.
function isPreferred(agent: Agent | undefined, providerId: string, modelId: string): boolean {
  if (!agent || !agent.preferred_models) {
    return false
  }
  return agent.preferred_models.some(
    (pref: ModelPreference) => pref.provider === providerId && pref.model === modelId,
  )
}

// applyPolicy restricts and orders the model list according to the
// agent's model_policy.
//
// Semantics (mirrors agent.Manifest.IsModelAllowed on the Go side):
//   - undefined agent OR empty/permissive policy → full list, with
//     preferred entries hoisted to the top so the operator sees the
//     "best for the job" model first. Stable for the rest.
//   - "strict" policy with a non-empty preferred list → only listed
//     pairs survive. If none of the configured providers serve any of
//     the listed models the result is empty (the picker shows the
//     "No models" empty state).
//   - "strict" policy with an empty preferred list → degrades to
//     permissive. A strict-but-empty config is meaningless and must
//     not lock the operator out of every model.
function applyPolicy(agent: Agent | undefined, models: Model[]): Model[] {
  const policy = agent?.model_policy ?? ''
  const prefs = agent?.preferred_models ?? []

  if (policy === 'strict' && prefs.length > 0) {
    // Preserve the agent's preferred order — operators read the
    // first entry as the recommended default.
    return prefs
      .map((pref) =>
        models.find((m) => m.providerId === pref.provider && m.id === pref.model),
      )
      .filter((m): m is Model => m !== undefined)
  }

  // Permissive (or strict-empty): preferred entries first, then the
  // rest in their original order. This mirrors how Charm's TUI lists
  // recently-used items first while leaving the rest navigable.
  if (prefs.length === 0) {
    return models
  }

  const preferredKeys = new Set(prefs.map((p) => `${p.provider}:${p.model}`))
  const preferred = prefs
    .map((pref) =>
      models.find((m) => m.providerId === pref.provider && m.id === pref.model),
    )
    .filter((m): m is Model => m !== undefined)
  const rest = models.filter((m) => !preferredKeys.has(`${m.providerId}:${m.id}`))
  return [...preferred, ...rest]
}

const visibleModels = computed<Model[]>(() =>
  applyPolicy(activeAgent.value, chatStore.availableModels),
)

const fuzzyItems = computed<FuzzySearchItem[]>(() =>
  visibleModels.value.map((model) => {
    const preferred = isPreferred(activeAgent.value, model.providerId, model.id)
    return {
      id: `${model.providerId}:${model.id}`,
      label: model.name || model.id,
      group: model.providerId,
      // Only badge preferred models when the policy is *not* strict —
      // under strict the entire list is preferred, so the badge would
      // be redundant noise. Under permissive it's the cue the brief
      // asks for ("subtle visual cue so the user knows which models
      // are recommended even when the picker is unrestricted").
      meta:
        preferred && activeAgent.value?.model_policy !== 'strict'
          ? 'Preferred'
          : undefined,
    }
  }),
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
