<script setup lang="ts">
import { useTodoStore } from "@/stores/todoStore";

defineOptions({ name: "TodoList" });

// Todos are agent-emitted (see the `todowrite` tool); this view is a
// read-only projection. No user-driven add, toggle, or delete. The
// canonical mount point is TodoListPanel in the chat side-panel; this
// component only survives because PlanPanel still imports it.
const todoStore = useTodoStore();
</script>

<template>
  <div class="todo-list" data-testid="todo-list">
    <ul class="todo-items">
      <li
        v-for="todo in todoStore.todos"
        :key="todo.id"
        class="todo-item"
        :class="{ completed: todo.status === 'completed' }"
        data-testid="todo-item"
      >
        <span
          class="status-icon"
          :class="`status-${todo.status}`"
          aria-hidden="true"
          >{{ todo.status === "completed" ? "✓" : "○" }}</span
        >
        <span class="todo-text">{{ todo.content }}</span>
      </li>
    </ul>

    <div
      v-if="todoStore.todos.length === 0"
      class="empty"
      data-testid="todo-empty"
    >
      No tasks yet
    </div>
  </div>
</template>

<style scoped>
.todo-list {
  padding: 0.5rem;
}

.todo-items {
  list-style: none;
  padding: 0;
  margin: 0;
}

.todo-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.375rem 0;
  border-bottom: 1px solid var(--border-color, #f3f4f6);
}

.todo-item.completed .todo-text {
  text-decoration: line-through;
  opacity: 0.6;
}

.status-icon {
  font-size: 0.875rem;
  width: 1rem;
  text-align: center;
  color: var(--text-secondary, #9ca3af);
}

.status-icon.status-completed {
  color: var(--accent-color, #3b82f6);
}

.todo-text {
  flex: 1;
  font-size: 0.875rem;
}

.empty {
  text-align: center;
  color: var(--text-secondary, #9ca3af);
  font-size: 0.875rem;
  padding: 1rem;
}
</style>
