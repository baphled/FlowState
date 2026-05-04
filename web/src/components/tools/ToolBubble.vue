<script setup lang="ts">
import { ref, computed } from 'vue'

interface Props {
  toolName: string
  title: string
  subtitle?: string
  status?: 'pending' | 'running' | 'completed' | 'error'
  defaultOpen?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  status: 'completed',
  defaultOpen: false
})

const isOpen = ref(props.defaultOpen)

function toggleOpen() {
  isOpen.value = !isOpen.value
}

const statusIcon = computed(() => {
  switch (props.status) {
    case 'running': return '⟳'
    case 'completed': return '✓'
    case 'error': return '✕'
    default: return ''
  }
})
</script>

<template>
  <div 
    class="tool-bubble" 
    data-testid="tool-bubble"
    data-component="tool"
    :data-tool="toolName"
    :data-status="status"
    :data-open="isOpen"
  >
    <div class="tool-bubble__trigger" @click="toggleOpen">
      <span class="tool-bubble__chevron" aria-hidden="true">▸</span>
      <div class="tool-bubble__header-text">
        <span class="tool-bubble__title">{{ title }}</span>
        <span v-if="subtitle" class="tool-bubble__subtitle">{{ subtitle }}</span>
      </div>
      <span v-if="statusIcon" class="tool-bubble__status-icon" :class="{ 'tool-bubble__status-icon--spinning': status === 'running' }">
        {{ statusIcon }}
      </span>
    </div>
    
    <div class="tool-bubble__body" :style="{ maxHeight: isOpen ? '1000px' : '0', opacity: isOpen ? '1' : '0' }">
      <div class="tool-bubble__content">
        <slot />
      </div>
    </div>
  </div>
</template>

<style scoped>
.tool-bubble {
  --tool-border: var(--border, #3b3b4f);
  border: 1px solid var(--tool-border);
  border-left: 2px solid var(--event-tool-call, #7aa2f7);
  border-radius: var(--radius, 6px);
  overflow: hidden;
  transition: border-color 0.15s ease;
  margin-bottom: 0.5rem;
  background: var(--surface-low, rgba(30, 30, 46, 0.4));
}

.tool-bubble[data-status="error"] {
  border-left-color: var(--error, #f7768e);
}

.tool-bubble[data-status="pending"] .tool-bubble__title,
.tool-bubble[data-status="running"] .tool-bubble__title {
  background: linear-gradient(
    90deg, 
    var(--text-muted, #565f89) 25%, 
    var(--text-secondary, #a9b1d6) 50%, 
    var(--text-muted, #565f89) 75%
  );
  background-size: 200% 100%;
  animation: text-shimmer 1.5s ease-in-out infinite;
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
}

@keyframes text-shimmer {
  0% { background-position: 200% 0; }
  100% { background-position: -200% 0; }
}

.tool-bubble__trigger {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.4rem 0.6rem;
  cursor: pointer;
  user-select: none;
  font-size: 0.85rem;
  color: var(--text-primary, #c0caf5);
}

.tool-bubble__trigger:hover {
  background: var(--surface-hover, rgba(255, 255, 255, 0.05));
}

.tool-bubble__header-text {
  display: flex;
  flex-direction: column;
  flex: 1;
}

.tool-bubble__title {
  font-weight: 500;
}

.tool-bubble__subtitle {
  font-size: 0.75rem;
  color: var(--text-muted, #565f89);
}

.tool-bubble__chevron {
  transition: transform 0.15s ease;
  color: var(--text-muted, #565f89);
  font-size: 0.7rem;
}

.tool-bubble[data-open="true"] .tool-bubble__chevron {
  transform: rotate(90deg);
}

.tool-bubble__status-icon {
  font-size: 0.9rem;
  min-width: 1rem;
  display: flex;
  justify-content: center;
  align-items: center;
}

.tool-bubble__status-icon--spinning {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.tool-bubble__body {
  overflow: hidden;
  transition: max-height 0.2s ease, opacity 0.15s ease;
}

.tool-bubble__content {
  padding: 0.6rem;
  border-top: 1px solid var(--tool-border);
  font-size: 0.85rem;
}
</style>
