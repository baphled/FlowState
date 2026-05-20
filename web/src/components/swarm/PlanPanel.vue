<script setup lang="ts">
import { computed } from "vue";
import { useSwarmStore } from "@/stores/swarmStore";
import EventCard from "@/components/swarm/EventCard.vue";
import TodoList from "@/components/swarm/TodoList.vue";

defineOptions({ name: "PlanPanel" });

const emit = defineEmits<{
  close: [];
}>();

const swarmStore = useSwarmStore();

const planEvents = computed(() => swarmStore.planEvents);
const statusEvents = computed(() => swarmStore.statusEvents);
const reviewEvents = computed(() => swarmStore.reviewEvents);
</script>

<template>
  <aside class="plan-panel" data-testid="plan-panel">
    <div class="panel-header">
      <span>Plan / Status</span>
      <button
        class="close-btn"
        data-testid="close-plan-btn"
        @click="emit('close')"
      >
        ✕
      </button>
    </div>

    <div class="panel-content">
      <TodoList />
      <hr class="divider" />

      <section v-if="planEvents.length > 0" class="event-section">
        <h3>Plan Artifacts</h3>
        <EventCard v-for="event in planEvents" :key="event.id" :event="event" />
      </section>

      <section v-if="statusEvents.length > 0" class="event-section">
        <h3>Status Transitions</h3>
        <EventCard
          v-for="event in statusEvents"
          :key="event.id"
          :event="event"
        />
      </section>

      <section v-if="reviewEvents.length > 0" class="event-section">
        <h3>Reviews</h3>
        <EventCard
          v-for="event in reviewEvents"
          :key="event.id"
          :event="event"
        />
      </section>

      <p
        v-if="
          planEvents.length === 0 &&
          statusEvents.length === 0 &&
          reviewEvents.length === 0
        "
        class="empty-message"
      >
        No plan or status events yet
      </p>
    </div>
  </aside>
</template>

<style scoped>
.plan-panel {
  width: 30%;
  min-width: 220px;
  max-width: 360px;
  border-left: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  background: var(--bg-secondary);
  flex-shrink: 0;
}

.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--border);
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.08em;
}

.close-btn {
  background: none;
  border: 1px solid var(--border);
  color: var(--text-muted);
  cursor: pointer;
  border-radius: var(--radius);
  padding: 0.15rem 0.5rem;
  font-size: 0.75rem;
  transition:
    color 0.15s,
    border-color 0.15s;
}

.close-btn:hover {
  color: var(--text-primary);
  border-color: var(--accent);
}

.panel-content {
  flex: 1;
  overflow-y: auto;
  padding: 0.5rem;
}

.divider {
  border: none;
  border-top: 1px solid var(--border-color, #e5e7eb);
  margin: 1rem 0;
}

.event-section {
  margin-bottom: 1rem;
}

.event-section h3 {
  font-size: 0.7rem;
  font-weight: 600;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.08em;
  margin-bottom: 0.5rem;
  padding-bottom: 0.25rem;
  border-bottom: 1px solid var(--border);
}

.empty-message {
  color: var(--text-muted);
  font-size: 0.85rem;
  text-align: center;
  padding: 1rem 0;
  font-style: italic;
}
</style>
