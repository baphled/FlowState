import { defineStore } from 'pinia'
import type { Message } from '@/types'

// Todo represents a single agent-emitted todo entry. The display shape mirrors
// the JSON the `todowrite` tool emits — see internal/tui/uikit/widgets/
// todo_widget.go:11-16 (todoItem) for the canonical Go counterpart. The TUI
// receives the raw JSON via the same channel and renders it through
// FormatTodoList; the web frontend parses it directly into Todo[].
export interface Todo {
  id: string
  content: string
  status: 'pending' | 'completed'
  createdAt: string
  completedAt?: string
}

// The agent emits items shaped like { content, status, priority }. We map
// the wider TUI status vocabulary ("in_progress", "cancelled", …) onto the
// binary pending/completed the panel cares about so the side panel stays
// simple. "completed" stays "completed"; everything else is "pending" from
// the panel's point of view (the TUI itself uses richer status icons, but
// the web side panel is intentionally low-fidelity).
interface TodowriteItem {
  content?: unknown
  status?: unknown
  priority?: unknown
}

// The todo store is a READ-ONLY projection of agent-emitted todos that arrive
// over the SSE pipeline as `tool_result` events for the `todowrite` tool. The
// user is purely an observer — there are no user-driven mutators (no add, no
// toggle, no delete). State is keyed by session id internally so switching
// sessions changes the displayed slice and switching back restores it. There
// is no localStorage persistence: the canonical state lives in the backend
// session message stream and is rehydrated via hydrateFromMessages on load.
//
// Population paths (both wired by chatStore):
//   1. Live ingestion — chatStore.applyContentEvent recognises a tool_call
//      with name "todowrite" and forwards the next tool_result content into
//      ingestToolResult, parsing the raw JSON as the canonical state.
//   2. Hydration — chatStore.loadSessionMessages calls hydrateFromMessages
//      with the fetched history; the latest tool_result whose toolName is
//      "todowrite" wins (matching the TUI semantics where the most recent
//      todowrite emission is the current state).
//
// The legacy `flowstate-todos` localStorage key from the user-CRUD era is
// dropped — todos are derived state, not user input.
function isString(value: unknown): value is string {
  return typeof value === 'string'
}

function todoFromItem(item: TodowriteItem, index: number): Todo | null {
  if (!isString(item.content) || item.content.length === 0) {
    return null
  }
  const status = isString(item.status) && item.status === 'completed' ? 'completed' : 'pending'
  return {
    // Items in a todowrite emission are positional — the canonical state is
    // the full array, so a deterministic index-based id is sufficient and
    // keeps Vue's :key stable across re-renders of the same emission.
    id: `todo-${index}`,
    content: item.content,
    status,
    createdAt: '',
    completedAt: undefined,
  }
}

function parseTodowritePayload(content: string): Todo[] | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(content)
  } catch {
    return null
  }
  if (!Array.isArray(parsed)) {
    return null
  }
  const todos: Todo[] = []
  for (let i = 0; i < parsed.length; i += 1) {
    const todo = todoFromItem(parsed[i] as TodowriteItem, i)
    if (todo) {
      todos.push(todo)
    }
  }
  return todos
}

export const useTodoStore = defineStore('todo', {
  state: () => ({
    bySession: {} as Record<string, Todo[]>,
    currentSessionId: null as string | null,
  }),

  getters: {
    todos(): Todo[] {
      if (!this.currentSessionId) return []
      return this.bySession[this.currentSessionId] ?? []
    },
    pendingTodos(): Todo[] {
      return this.todos.filter((t) => t.status === 'pending')
    },
    completedTodos(): Todo[] {
      return this.todos.filter((t) => t.status === 'completed')
    },
  },

  actions: {
    // setCurrentSession swaps the displayed slice. The panel is reactive to
    // the `todos` getter, which derives from currentSessionId — assigning
    // here is enough for the UI to reflect the change.
    setCurrentSession(sessionId: string | null): void {
      this.currentSessionId = sessionId
    },

    // ingestToolResult handles a single live emission of the todowrite tool.
    // The canonical state is the full array the agent emitted, so we replace
    // the per-session slice rather than merge — this mirrors the TUI, which
    // re-renders the full list on every todo_update.
    ingestToolResult(sessionId: string, content: string): void {
      const parsed = parseTodowritePayload(content)
      if (!parsed) return
      this.bySession[sessionId] = parsed
    },

    // hydrateFromMessages derives the current todo list from a session's
    // persisted message history. The latest tool_result whose toolName is
    // "todowrite" is the canonical state. If no such message is present the
    // slice is reset to empty so a previously-active live state cannot
    // bleed across reloads of a session that has cleared its todos.
    hydrateFromMessages(sessionId: string, messages: readonly Message[]): void {
      let latest: Todo[] = []
      let found = false
      for (const message of messages) {
        if (message.role !== 'tool_result' || message.toolName !== 'todowrite') {
          continue
        }
        const parsed = parseTodowritePayload(message.content ?? '')
        if (parsed) {
          latest = parsed
          found = true
        }
      }
      // Always set the slice — including to [] when no todowrite message
      // exists in history. Without this, a session with stale live state
      // would keep displaying it after a reload, which would be a lie.
      this.bySession[sessionId] = found ? latest : []
    },
  },
})
