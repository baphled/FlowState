<script setup lang="ts">
import { useToolStore } from "@/stores/toolStore";
import ToolCallItem from "./ToolCallItem.vue";

defineOptions({ name: "ToolCallPanel" });

const toolStore = useToolStore();

const emit = defineEmits<{
  close: [];
}>();
</script>

<template>
  <aside class="tool-panel" data-testid="tool-panel">
    <header class="panel-header">
      <span class="panel-title">Tool Activity</span>
      <span v-if="toolStore.toolCount > 0" class="tool-badge">{{
        toolStore.toolCount
      }}</span>
      <button
        class="close-btn"
        data-testid="close-tool-panel"
        @click="emit('close')"
      >
        ✕
      </button>
    </header>
    <div class="tool-list">
      <ToolCallItem
        v-for="event in toolStore.toolEvents"
        :key="event.id"
        :event="event"
      />
      <p v-if="toolStore.toolEvents.length === 0" class="tool-empty">
        No tool calls yet
      </p>
    </div>
  </aside>
</template>

<style scoped>
.tool-panel {
  display: flex;
  flex-direction: column;
  background: var(--bg-secondary);
  border-left: 1px solid var(--border);
}

.panel-header {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.5rem 0.6rem;
  border-bottom: 1px solid var(--border);
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.08em;
}

.panel-title {
  flex: 1;
}

.tool-badge {
  background: var(--accent);
  color: #fff;
  font-size: 0.65rem;
  padding: 0.1rem 0.35rem;
  border-radius: 10px;
}

.close-btn {
  background: none;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  font-size: 0.7rem;
  padding: 0.1rem 0.25rem;
}

.close-btn:hover {
  color: var(--text-primary);
}

.tool-list {
  flex: 1;
  overflow-y: auto;
  padding: 0.4rem;
}

.tool-empty {
  color: var(--text-muted);
  font-size: 0.8rem;
  text-align: center;
  padding: 1rem 0;
}
</style>
