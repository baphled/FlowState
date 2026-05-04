<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useFuzzyFilter, type FuzzySearchItem } from '@/composables/useFuzzyFilter'

defineOptions({ name: 'FuzzySearchModal' })

const props = withDefaults(
  defineProps<{
    items: FuzzySearchItem[]
    open: boolean
    placeholder?: string
    emptyMessage?: string
    /**
     * Seed text for the search input when the modal opens. Useful for
     * inline triggers (e.g. "/cle" in the chat input) where the user
     * has already started typing the filter before the picker
     * appeared.
     */
    initialQuery?: string
    /**
     * When true (default), the modal grabs keyboard focus on its
     * search input the moment it opens. Inline trigger pickers
     * (slash / mention from the chat composer) set this false so the
     * textarea retains focus and the user can keep typing the filter
     * fragment in place.
     */
    focusOnOpen?: boolean
  }>(),
  {
    placeholder: 'Search...',
    emptyMessage: 'No results',
    initialQuery: '',
    focusOnOpen: true,
  },
)

const emit = defineEmits<{
  select: [item: FuzzySearchItem]
  close: []
}>()

const inputEl = ref<HTMLInputElement | null>(null)
const itemsRef = computed(() => props.items)

const {
  filteredItems,
  highlightedIndex,
  highlightNext,
  highlightPrev,
  setQuery,
} = useFuzzyFilter(itemsRef)

const groupedItems = computed(() => {
  // Walks the filtered list and rolls runs of items that share the
  // same `group` field into a single bucket. The previous version
  // bootstrapped `currentGroup = undefined`, which collided with
  // ungrouped items (whose group is also undefined) and produced
  // `result[-1]` access on the first iteration. Tracking
  // started-yet explicitly keeps both group=string and group=undefined
  // surfaces correct.
  const result: { group?: string; items: FuzzySearchItem[] }[] = []
  let currentGroup: string | undefined
  let started = false

  for (const item of filteredItems.value) {
    if (!started || item.group !== currentGroup) {
      currentGroup = item.group
      started = true
      result.push({ group: currentGroup, items: [item] })
      continue
    }
    result[result.length - 1].items.push(item)
  }

  return result
})

function selectHighlighted(): void {
  const items = filteredItems.value
  if (items.length === 0) return
  const item = items[highlightedIndex.value]
  emit('select', item)
  emit('close')
}

function handleKeydown(event: KeyboardEvent): void {
  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault()
      highlightNext()
      break
    case 'ArrowUp':
      event.preventDefault()
      highlightPrev()
      break
    case 'Enter':
      event.preventDefault()
      selectHighlighted()
      break
    case 'Escape':
      event.preventDefault()
      emit('close')
      break
  }
}

function handleBackdropClick(): void {
  emit('close')
}

function handleItemClick(item: FuzzySearchItem): void {
  emit('select', item)
  emit('close')
}

function handleSearchInput(event: Event): void {
  const target = event.target as HTMLInputElement
  setQuery(target.value)
}

watch(
  () => props.open,
  async (isOpen) => {
    if (isOpen) {
      // Seed the filter with the trigger fragment so inline pickers
      // (slash / mention) reflect what the user has already typed.
      // Falls back to empty string for the standalone toolbar pickers.
      setQuery(props.initialQuery)
      await nextTick()
      const el = inputEl.value
      if (el) {
        el.value = props.initialQuery
        if (props.focusOnOpen) {
          el.focus()
        }
      }
    }
  },
  { immediate: true },
)

// Keep the input element's displayed value in sync with the seeded
// query when initialQuery changes mid-open (e.g. user types another
// character into the textarea before the modal observed the change).
watch(
  () => props.initialQuery,
  (q) => {
    if (!props.open) return
    setQuery(q)
    const el = inputEl.value
    if (el) el.value = q
  },
)

onMounted(() => {
  document.addEventListener('keydown', handleKeydown)
})

onBeforeUnmount(() => {
  document.removeEventListener('keydown', handleKeydown)
})
</script>

<template>
  <div
    v-if="open"
    class="fuzzy-search-backdrop"
    data-testid="fuzzy-search-backdrop"
    @click.self="handleBackdropClick"
  >
    <div class="fuzzy-search-modal" data-testid="fuzzy-search-modal" @click.stop>
      <input
        ref="inputEl"
        type="text"
        class="fuzzy-search-input"
        data-testid="fuzzy-search-input"
        :placeholder="placeholder"
        @input="handleSearchInput"
      />

      <div class="fuzzy-search-list">
        <template v-if="groupedItems.length === 0">
          <div class="fuzzy-search-empty" data-testid="fuzzy-search-empty">
            {{ emptyMessage }}
          </div>
        </template>

        <template v-for="(group, groupIdx) in groupedItems" :key="group.group ?? groupIdx">
          <div
            v-if="group.group"
            class="fuzzy-search-group-header"
            :data-testid="`fuzzy-search-group-${group.group}`"
          >
            {{ group.group }}
          </div>

          <div
            v-for="(item) in group.items"
            :key="item.id"
            class="fuzzy-search-item"
            :class="{ 'fuzzy-search-item--highlighted': filteredItems.indexOf(item) === highlightedIndex }"
            :data-testid="`fuzzy-search-item-${item.id}`"
            @click="handleItemClick(item)"
          >
            <span class="fuzzy-search-item-label">{{ item.label }}</span>
            <span v-if="item.meta" class="fuzzy-search-item-meta">{{ item.meta }}</span>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.fuzzy-search-backdrop {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.6);
  z-index: 200;
}

.fuzzy-search-modal {
  width: 100%;
  max-width: 480px;
  max-height: 400px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.4);
}

.fuzzy-search-input {
  width: 100%;
  padding: 0.75rem 1rem;
  background: var(--bg-secondary);
  border: none;
  border-bottom: 1px solid var(--border);
  color: var(--text-primary);
  font-size: 0.95rem;
  outline: none;
  box-sizing: border-box;
}

.fuzzy-search-input::placeholder {
  color: var(--text-muted);
}

.fuzzy-search-list {
  overflow-y: auto;
  flex: 1;
}

.fuzzy-search-empty {
  padding: 1.5rem 1rem;
  text-align: center;
  color: var(--text-muted);
  font-size: 0.85rem;
}

.fuzzy-search-group-header {
  padding: 0.4rem 1rem 0.2rem;
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-muted);
  font-weight: 600;
}

.fuzzy-search-item {
  padding: 0.5rem 1rem;
  cursor: pointer;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 0.5rem;
  transition: background 0.1s;
}

.fuzzy-search-item:hover {
  background: var(--bg-secondary);
}

.fuzzy-search-item--highlighted {
  background: var(--accent);
  color: var(--bg-primary);
}

.fuzzy-search-item--highlighted:hover {
  background: var(--accent);
}

.fuzzy-search-item-label {
  font-size: 0.9rem;
  font-weight: 500;
}

.fuzzy-search-item-meta {
  font-size: 0.75rem;
  color: var(--text-muted);
}

.fuzzy-search-item--highlighted .fuzzy-search-item-meta {
  color: var(--bg-primary);
  opacity: 0.8;
}
</style>
