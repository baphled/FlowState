<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { listModels } from "@/api";
import { useChatStore } from "@/stores/chatStore";
import type { ModelsResponse, ProviderInfo } from "@/types";

defineOptions({ name: "ModelSwitcher" });

const chatStore = useChatStore();

const isOpen = ref(false);
const isLoading = ref(false);
const errorMessage = ref<string | null>(null);
const providers = ref<ProviderInfo[]>([]);
const hasLoaded = ref(false);
const rootEl = ref<HTMLElement | null>(null);

const triggerLabel = computed(() => {
  if (chatStore.currentProviderId && chatStore.currentModelId) {
    return `${chatStore.currentProviderId}/${chatStore.currentModelId}`;
  }
  return "Select model";
});

async function loadModels(): Promise<void> {
  isLoading.value = true;
  errorMessage.value = null;
  try {
    const response: ModelsResponse = await listModels();
    providers.value = response.providers ?? [];
    hasLoaded.value = true;
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err);
  } finally {
    isLoading.value = false;
  }
}

function toggleDropdown(): void {
  isOpen.value = !isOpen.value;
}

function closeDropdown(): void {
  isOpen.value = false;
}

function selectModel(providerId: string, modelId: string): void {
  void chatStore.setModel(modelId, providerId);
  closeDropdown();
}

function retry(): void {
  void loadModels();
}

function handleDocumentMouseDown(event: MouseEvent): void {
  if (!isOpen.value) {
    return;
  }
  const target = event.target as Node | null;
  if (rootEl.value && target && rootEl.value.contains(target)) {
    return;
  }
  closeDropdown();
}

function handleDocumentKeyDown(event: KeyboardEvent): void {
  if (event.key === "Escape") {
    closeDropdown();
  }
}

onMounted(() => {
  void loadModels();
  document.addEventListener("mousedown", handleDocumentMouseDown);
  document.addEventListener("keydown", handleDocumentKeyDown);
});

onBeforeUnmount(() => {
  document.removeEventListener("mousedown", handleDocumentMouseDown);
  document.removeEventListener("keydown", handleDocumentKeyDown);
});
</script>

<template>
  <div ref="rootEl" class="model-switcher" data-testid="model-switcher">
    <button
      type="button"
      class="model-switcher-trigger"
      data-testid="model-switcher-trigger"
      aria-haspopup="listbox"
      :aria-expanded="isOpen"
      @click="toggleDropdown"
    >
      <span class="model-icon">🧠</span>
      <span class="model-label">{{ triggerLabel }}</span>
      <span class="dropdown-arrow" :class="{ open: isOpen }">▾</span>
    </button>

    <div
      v-if="isLoading"
      class="model-switcher-status model-switcher-status-inline"
      data-testid="model-switcher-loading"
    >
      Loading models…
    </div>

    <div
      v-else-if="errorMessage"
      class="model-switcher-status model-switcher-status-inline model-switcher-status-error"
      data-testid="model-switcher-error"
    >
      <span class="error-text">{{ errorMessage }}</span>
      <button
        type="button"
        class="retry-button"
        data-testid="model-switcher-retry"
        @click="retry"
      >
        Retry
      </button>
    </div>

    <div
      v-if="isOpen"
      class="model-switcher-dropdown"
      data-testid="model-switcher-dropdown"
      role="listbox"
    >
      <div v-if="providers.length === 0" class="model-switcher-status">
        No models available
      </div>

      <ul v-else class="provider-list">
        <li
          v-for="provider in providers"
          :key="provider.id"
          class="provider-group"
        >
          <div class="provider-name">{{ provider.id }}</div>
          <ul class="model-list">
            <li
              v-for="model in provider.models"
              :key="model.id"
              class="model-option"
              :class="{
                active:
                  provider.id === chatStore.currentProviderId &&
                  model.id === chatStore.currentModelId,
              }"
              :data-testid="`model-option-${provider.id}-${model.id}`"
              role="option"
              :aria-selected="
                provider.id === chatStore.currentProviderId &&
                model.id === chatStore.currentModelId
              "
              @click="selectModel(provider.id, model.id)"
            >
              <span class="model-option-name">{{ model.name }}</span>
              <span class="model-option-id">{{ model.id }}</span>
            </li>
          </ul>
        </li>
      </ul>
    </div>
  </div>
</template>

<style scoped>
.model-switcher {
  position: relative;
  display: inline-flex;
}

.model-switcher-trigger {
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

.model-switcher-trigger:hover {
  border-color: var(--accent);
}

.model-icon {
  font-size: 0.9rem;
}

.model-label {
  font-weight: 500;
}

.dropdown-arrow {
  font-size: 0.7rem;
  color: var(--text-muted);
  transition: transform 0.15s;
}

.dropdown-arrow.open {
  transform: rotate(180deg);
}

.model-switcher-dropdown {
  position: absolute;
  top: 100%;
  left: 0;
  margin-top: 0.25rem;
  min-width: 240px;
  max-height: 320px;
  overflow-y: auto;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
  z-index: 100;
  padding: 0.25rem 0;
}

.model-switcher-status {
  padding: 0.5rem 0.75rem;
  font-size: 0.8rem;
  color: var(--text-muted);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}

.model-switcher-status-error {
  color: var(--text-primary);
}

.error-text {
  flex: 1;
}

.retry-button {
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 0.15rem 0.5rem;
  font-size: 0.75rem;
  color: var(--text-primary);
  cursor: pointer;
}

.retry-button:hover {
  border-color: var(--accent);
}

.provider-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.provider-group {
  padding: 0.25rem 0;
  border-bottom: 1px solid var(--border);
}

.provider-group:last-child {
  border-bottom: none;
}

.provider-name {
  padding: 0.25rem 0.75rem;
  font-size: 0.72rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-muted);
}

.model-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.model-option {
  padding: 0.4rem 0.75rem;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
  transition: background 0.1s;
}

.model-option:hover {
  background: var(--bg-secondary);
}

.model-option.active {
  background: var(--accent-bg);
  color: var(--accent);
}

.model-option-name {
  font-weight: 500;
  font-size: 0.85rem;
}

.model-option-id {
  font-size: 0.72rem;
  color: var(--text-muted);
}
</style>
