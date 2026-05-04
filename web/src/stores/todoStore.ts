import { defineStore } from 'pinia'

export interface Todo {
  id: string
  content: string
  status: 'pending' | 'completed'
  createdAt: string
  completedAt?: string
}

const STORAGE_KEY = 'flowstate-todos'

function loadTodos(): Todo[] {
  if (typeof window === 'undefined') return []
  const stored = localStorage.getItem(STORAGE_KEY)
  if (!stored) return []
  try {
    return JSON.parse(stored)
  } catch {
    return []
  }
}

function persistTodos(todos: Todo[]): void {
  if (typeof window === 'undefined') return
  localStorage.setItem(STORAGE_KEY, JSON.stringify(todos))
}

export const useTodoStore = defineStore('todo', {
  state: () => ({
    todos: loadTodos() as Todo[],
  }),
  
  getters: {
    pendingTodos(): Todo[] {
      return this.todos.filter(t => t.status === 'pending')
    },
    completedTodos(): Todo[] {
      return this.todos.filter(t => t.status === 'completed')
    },
  },
  
  actions: {
    addTodo(content: string): void {
      const todo: Todo = {
        id: `todo-${Date.now()}`,
        content,
        status: 'pending',
        createdAt: new Date().toISOString(),
      }
      this.todos.push(todo)
      persistTodos(this.todos)
    },
    
    toggleTodo(id: string): void {
      const todo = this.todos.find(t => t.id === id)
      if (todo) {
        todo.status = todo.status === 'pending' ? 'completed' : 'pending'
        if (todo.status === 'completed') {
          todo.completedAt = new Date().toISOString()
        } else {
          todo.completedAt = undefined
        }
        persistTodos(this.todos)
      }
    },
    
    deleteTodo(id: string): void {
      const index = this.todos.findIndex(t => t.id === id)
      if (index !== -1) {
        this.todos.splice(index, 1)
        persistTodos(this.todos)
      }
    },
    
    clearCompleted(): void {
      this.todos = this.todos.filter(t => t.status === 'pending')
      persistTodos(this.todos)
    },
  },
})
