<script setup lang="ts">
import { useToast } from '@/composables/useToast'
import type { Toast } from '@/composables/useToast'

defineOptions({ name: 'ToastContainer' })

const { toasts, removeToast } = useToast()

function handleClose(toast: Toast): void {
  removeToast(toast.id)
}

function handleAction(toast: Toast): void {
  toast.action?.onClick()
}
</script>

<template>
  <Teleport to="body">
    <div class="toast-container" data-testid="toast-container">
      <TransitionGroup name="toast" tag="div" class="toast-list">
        <div
          v-for="toast in toasts"
          :key="toast.id"
          class="toast-item"
          :class="`toast-item--${toast.variant}`"
          data-testid="toast-item"
        >
          <div class="toast-content">
            <span v-if="toast.title" class="toast-title" data-testid="toast-title">
              {{ toast.title }}
            </span>
            <span class="toast-message">{{ toast.message }}</span>
          </div>
          <div class="toast-actions">
            <button
              v-if="toast.action"
              class="toast-action-btn"
              data-testid="toast-action"
              @click="handleAction(toast)"
            >
              {{ toast.action.label }}
            </button>
            <button
              class="toast-close-btn"
              data-testid="toast-close"
              @click="handleClose(toast)"
            >
              &times;
            </button>
          </div>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-container {
  position: fixed;
  bottom: 1.5rem;
  right: 1.5rem;
  z-index: 1000;
  max-width: 400px;
  display: flex;
  flex-direction: column-reverse;
  gap: 0.5rem;
  pointer-events: none;
}

.toast-list {
  display: flex;
  flex-direction: column-reverse;
  gap: 0.5rem;
}

.toast-item {
  background: var(--bg-elevated, #2a2a2a);
  border: 1px solid var(--border, #3a3a3a);
  border-radius: 8px;
  padding: 0.75rem 1rem;
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 0.75rem;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
  pointer-events: auto;
}

.toast-item--default {
  background: var(--bg-elevated, #2a2a2a);
}

.toast-item--success {
  background: rgba(34, 139, 34, 0.15);
  border-color: rgba(34, 139, 34, 0.3);
}

.toast-item--error {
  background: rgba(220, 38, 38, 0.15);
  border-color: rgba(220, 38, 38, 0.3);
}

.toast-item--loading {
  background: var(--bg-elevated, #2a2a2a);
  border-color: var(--accent, #6366f1);
}

.toast-content {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  flex: 1;
  min-width: 0;
}

.toast-title {
  font-weight: 600;
  font-size: 0.9rem;
  color: var(--text-primary, #e5e5e5);
}

.toast-message {
  font-size: 0.85rem;
  color: var(--text-primary, #e5e5e5);
  word-wrap: break-word;
}

.toast-actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-shrink: 0;
}

.toast-action-btn {
  background: transparent;
  border: 1px solid var(--accent, #6366f1);
  color: var(--accent, #6366f1);
  padding: 0.25rem 0.5rem;
  border-radius: 4px;
  font-size: 0.8rem;
  cursor: pointer;
  white-space: nowrap;
}

.toast-action-btn:hover {
  background: var(--accent, #6366f1);
  color: var(--bg-primary, #1a1a1a);
}

.toast-close-btn {
  background: transparent;
  border: none;
  color: var(--text-muted, #888);
  font-size: 1.1rem;
  cursor: pointer;
  padding: 0;
  line-height: 1;
  display: flex;
  align-items: center;
}

.toast-close-btn:hover {
  color: var(--text-primary, #e5e5e5);
}

.toast-enter-active {
  transition: all 150ms ease-out;
}

.toast-leave-active {
  transition: all 100ms ease-in;
}

.toast-enter-from {
  opacity: 0;
  transform: translateY(20px);
}

.toast-leave-to {
  opacity: 0;
  transform: translateY(20px);
}
</style>
