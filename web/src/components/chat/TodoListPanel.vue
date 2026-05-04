<script setup lang="ts">
import { useTodoStore } from '@/stores/todoStore'

defineOptions({ name: 'TodoListPanel' })

// TodoListPanel is the side-panel host for the todoStore. Todos are
// agent-emitted (see the `todowrite` tool and internal/tui/uikit/widgets/
// todo_widget.go FormatTodoList for the TUI counterpart) and the user is
// purely an observer — no add input, no toggle, no delete. The store is
// the single source of truth and is read-only from the UI.
const todoStore = useTodoStore()
</script>

<template>
  <section class="todo-list-panel" data-testid="todo-list-panel">
    <header class="panel-header">
      <span class="panel-title">Todos</span>
      <span v-if="todoStore.pendingTodos.length > 0" class="panel-counter">
        {{ todoStore.pendingTodos.length }}
      </span>
    </header>

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
        >{{ todo.status === 'completed' ? '✓' : '○' }}</span>
        <span class="todo-text">{{ todo.content }}</span>
      </li>
    </ul>

    <div v-if="todoStore.todos.length === 0" class="empty" data-testid="todo-empty">
      No todos in this session yet
    </div>
  </section>
</template>

<style scoped>
.todo-list-panel {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  padding: 0.5rem;
  overflow: hidden;
}

.panel-header {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.25rem 0.25rem 0.5rem;
  border-bottom: 1px solid var(--border);
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.08em;
  margin-bottom: 0.5rem;
}

.panel-title {
  flex: 1;
}

.panel-counter {
  background: var(--accent);
  color: #fff;
  font-size: 0.65rem;
  padding: 0.1rem 0.35rem;
  border-radius: 10px;
}

.todo-items {
  list-style: none;
  padding: 0;
  margin: 0;
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}

.todo-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.375rem 0;
  border-bottom: 1px solid var(--border, #f3f4f6);
}

.todo-item.completed .todo-text {
  text-decoration: line-through;
  opacity: 0.6;
}

.status-icon {
  font-size: 0.875rem;
  width: 1rem;
  text-align: center;
  color: var(--text-muted);
}

.status-icon.status-completed {
  color: var(--accent, #3b82f6);
}

.todo-text {
  flex: 1;
  font-size: 0.875rem;
  color: var(--text-primary);
}

.empty {
  text-align: center;
  color: var(--text-muted, #9ca3af);
  font-size: 0.875rem;
  padding: 1rem;
}
</style>
