<script setup lang="ts">
import { computed } from "vue";

defineOptions({ name: "SystemMessage" });

const props = withDefaults(
  defineProps<{
    status: "waiting" | "processing" | "completed" | "blocked";
    text?: string;
  }>(),
  {
    text: "",
  },
);

const icon = computed(() => {
  switch (props.status) {
    case "waiting":
      return "🕒";
    case "processing":
      return "⏳";
    case "completed":
      return "✅";
    case "blocked":
      return "⏸️";
  }
});

const displayText = computed(() => {
  if (props.text) return props.text;
  switch (props.status) {
    case "waiting":
      return "Waiting for response...";
    case "processing":
      return "Processing...";
    case "completed":
      return "Completed";
    case "blocked":
      return "Blocked";
  }
});
</script>

<template>
  <div
    class="system-message"
    :data-status="status"
    data-testid="system-message"
  >
    <span class="system-message-icon">{{ icon }}</span>
    <span class="system-message-text">{{ displayText }}</span>
  </div>
</template>

<style scoped>
.system-message {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  padding: 0.5rem 1rem;
  margin: 0.5rem 0;
  background: var(--bg-secondary, #f3f4f6);
  border-radius: 0.25rem;
  font-size: 0.875rem;
  color: var(--text-secondary, #6b7280);
}

.system-message-icon {
  font-size: 1rem;
}

.system-message-text {
  font-weight: 500;
}
</style>
