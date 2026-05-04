<script setup lang="ts">
import { ref } from 'vue'
import { useTodoStore } from '@/stores/todoStore'

defineOptions({ name: 'TodoList' })

const todoStore = useTodoStore()
const newTodoText = ref('')

function addTodo(): void {
  const text = newTodoText.value.trim()
  if (!text) return
  todoStore.addTodo(text)
  newTodoText.value = ''
}

function toggleTodo(id: string): void {
  todoStore.toggleTodo(id)
}

function deleteTodo(id: string): void {
  todoStore.deleteTodo(id)
}
</script>

<template>
  <div class="todo-list" data-testid="todo-list">
    <div class="todo-input">
      <input
        v-model="newTodoText"
        type="text"
        placeholder="Add a task..."
        data-testid="todo-input"
        @keyup.enter="addTodo"
      />
      <button class="add-btn" data-testid="todo-add-btn" @click="addTodo">
        Add
      </button>
    </div>
    
    <ul class="todo-items">
      <li
        v-for="todo in todoStore.todos"
        :key="todo.id"
        class="todo-item"
        :class="{ completed: todo.status === 'completed' }"
        data-testid="todo-item"
      >
        <input
          type="checkbox"
          :checked="todo.status === 'completed'"
          @change="toggleTodo(todo.id)"
        />
        <span class="todo-text" @click="toggleTodo(todo.id)">{{ todo.content }}</span>
        <button class="delete-btn" data-testid="todo-delete-btn" @click="deleteTodo(todo.id)">
          ✕
        </button>
      </li>
    </ul>
    
    <div v-if="todoStore.todos.length === 0" class="empty" data-testid="todo-empty">
      No tasks yet
    </div>
  </div>
</template>

<style scoped>
.todo-list {
  padding: 0.5rem;
}

.todo-input {
  display: flex;
  gap: 0.5rem;
  margin-bottom: 0.75rem;
}

.todo-input input {
  flex: 1;
  padding: 0.375rem 0.5rem;
  border: 1px solid var(--border-color, #e5e7eb);
  border-radius: 0.25rem;
  font-size: 0.875rem;
}

.add-btn {
  padding: 0.375rem 0.75rem;
  background: var(--accent-color, #3b82f6);
  color: white;
  border: none;
  border-radius: 0.25rem;
  font-size: 0.875rem;
  cursor: pointer;
}

.add-btn:hover {
  opacity: 0.9;
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

.todo-item input[type="checkbox"] {
  cursor: pointer;
}

.todo-text {
  flex: 1;
  cursor: pointer;
  font-size: 0.875rem;
}

.delete-btn {
  padding: 0.125rem 0.375rem;
  background: none;
  border: none;
  color: var(--text-secondary, #9ca3af);
  cursor: pointer;
  font-size: 0.75rem;
}

.delete-btn:hover {
  color: var(--error-color, #ef4444);
}

.empty {
  text-align: center;
  color: var(--text-secondary, #9ca3af);
  font-size: 0.875rem;
  padding: 1rem;
}
</style>
