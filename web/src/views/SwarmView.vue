<template>
  <div class="swarm-view" data-testid="swarm-view">
    <header class="swarm-header">
      <h1>Swarm Activity</h1>
      <div class="swarm-controls">
        <span class="event-count" data-testid="event-count"
          >{{ swarmStore.eventCount }} events</span
        >
        <button
          class="btn-primary"
          data-testid="live-toggle-btn"
          :disabled="swarmStore.isLive"
          @click="swarmStore.connect()"
        >
          {{ swarmStore.isLive ? "Connected..." : "Go Live" }}
        </button>
      </div>
    </header>

    <div v-if="swarmStore.error" class="swarm-error" data-testid="swarm-error">
      {{ swarmStore.error }}
    </div>

    <div
      v-if="events.length === 0 && !swarmStore.isLive"
      class="swarm-empty"
      data-testid="swarm-empty"
    >
      No swarm events yet. Click "Go Live" to see real-time activity.
    </div>

    <ul v-else class="event-list" data-testid="swarm-event-list">
      <li v-for="event in events" :key="event.id">
        <EventCard :event="event" />
      </li>
    </ul>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";
import { useSwarmStore } from "@/stores/swarmStore";
import EventCard from "@/components/swarm/EventCard.vue";

const swarmStore = useSwarmStore();
const events = computed(() => swarmStore.events);

onMounted(() => {
  // Auto-connect on mount
  swarmStore.connect();
});

onUnmounted(() => {
  // Clean up on unmount
  swarmStore.disconnect();
});
</script>

<style scoped>
.swarm-view {
  display: flex;
  flex-direction: column;
  height: 100%;
  padding: 1rem;
  gap: 1rem;
}

.swarm-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.swarm-header h1 {
  font-size: 1.25rem;
  font-weight: 600;
  color: var(--text-primary);
}

.swarm-controls {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.event-count {
  font-size: 0.85rem;
  color: var(--text-secondary);
}

.btn-primary,
.btn-secondary {
  padding: 0.4rem 0.9rem;
  border-radius: var(--radius);
  border: 1px solid var(--border);
  cursor: pointer;
  font-size: 0.85rem;
}

.btn-primary {
  background: var(--accent);
  color: #fff;
  border-color: var(--accent);
}

.btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-secondary {
  background: var(--bg-secondary);
  color: var(--text-primary);
}

.swarm-error {
  padding: 0.75rem 1rem;
  background: #fee;
  color: #c00;
  border-radius: var(--radius);
  font-size: 0.9rem;
}

.swarm-empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-secondary);
  font-size: 0.95rem;
}

.event-list {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  overflow-y: auto;
  flex: 1;
}
</style>
