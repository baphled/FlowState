import { defineStore } from 'pinia'

// Todo represents a single agent-emitted todo entry. The shape mirrors the
// payload produced by the `todowrite` tool (see internal/tui/uikit/widgets/
// todo_widget.go FormatTodoList for the TUI counterpart).
export interface Todo {
  id: string
  content: string
  status: 'pending' | 'completed'
  createdAt: string
  completedAt?: string
}

// The todo store is a READ-ONLY projection of agent-emitted todos. The user
// is purely an observer — there are no user-driven mutators (no add, no
// toggle, no delete). This matches the FlowState TUI semantics where the
// agent's `todowrite` tool emits a chat.Message{Role: "todo_update"} and
// the UI renders it without offering edit affordances (intent.go:4366-4367).
//
// Population path: not yet wired in the web frontend. Tests seed state via
// `$patch({ todos })`. The agent-emit ingestion (SSE → store.$patch) is a
// follow-up — see scope cuts in the bug-fix note.
//
// Persistence: localStorage rehydration is retained for now so the panel
// renders the last-known state on reload. Once the SSE ingestion path is
// wired and todos become session-scoped, this should move to a per-session
// key (or be dropped entirely in favour of fetch-on-load).
export const STORAGE_KEY = 'flowstate-todos'

function loadTodos(): Todo[] {
  if (typeof window === 'undefined') return []
  try {
    const stored = window.localStorage?.getItem?.(STORAGE_KEY)
    if (!stored) return []
    const parsed = JSON.parse(stored) as unknown
    return Array.isArray(parsed) ? (parsed as Todo[]) : []
  } catch {
    return []
  }
}

export const useTodoStore = defineStore('todo', {
  state: () => ({
    todos: loadTodos() as Todo[],
  }),

  getters: {
    pendingTodos(): Todo[] {
      return this.todos.filter((t) => t.status === 'pending')
    },
    completedTodos(): Todo[] {
      return this.todos.filter((t) => t.status === 'completed')
    },
  },
})
