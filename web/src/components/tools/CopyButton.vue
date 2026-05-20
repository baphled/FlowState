<script setup lang="ts">
import { computed, onBeforeUnmount } from "vue";
import { useClipboard } from "@/composables/useClipboard";

defineOptions({ name: "CopyButton" });

const props = defineProps<{
  text: string;
}>();

const { copy, copied, cleanup } = useClipboard();

const icon = computed(() => (copied.value ? "✓" : "📋"));

async function handleCopy(): Promise<void> {
  await copy(props.text);
}

onBeforeUnmount(() => {
  cleanup();
});
</script>

<template>
  <button
    class="copy-button"
    data-testid="copy-btn"
    type="button"
    @click="handleCopy"
  >
    <span class="copy-button__icon" aria-hidden="true">{{ icon }}</span>
    <span class="copy-button__label">{{ copied ? "Copied" : "Copy" }}</span>
  </button>
</template>

<style scoped>
.copy-button {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.35rem 0.6rem;
  border: 1px solid var(--border, rgba(148, 163, 184, 0.35));
  border-radius: 0.5rem;
  background: var(--surface, transparent);
  color: var(--text-secondary, inherit);
  font-size: 0.85rem;
  line-height: 1;
  cursor: pointer;
}

.copy-button:hover {
  border-color: var(--accent, rgba(99, 102, 241, 0.5));
}

.copy-button__icon {
  font-size: 0.95rem;
}

.copy-button__label {
  white-space: nowrap;
}
</style>
