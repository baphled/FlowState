import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { useTodoStore } from './todoStore'

// The todo store is a read-only projection of agent-emitted todos. The user
// is purely an observer — there are no user-driven mutators. These tests
// pin that contract: the store exposes state and read-only derived getters,
// and DOES NOT expose addTodo / toggleTodo / deleteTodo / clearCompleted.
//
// Reference: internal/tui/uikit/widgets/todo_widget.go FormatTodoList — the
// TUI renders agent JSON only and never mutates a todo from user input.

describe('todoStore — read-only contract', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('does not expose user-driven mutators', () => {
    const store = useTodoStore()
    // Cast to a record for property probing so the assertions hold even if
    // someone tries to re-add the actions later — the contract is "absent".
    const probe = store as unknown as Record<string, unknown>
    expect(probe.addTodo).toBeUndefined()
    expect(probe.toggleTodo).toBeUndefined()
    expect(probe.deleteTodo).toBeUndefined()
    expect(probe.clearCompleted).toBeUndefined()
  })

  it('exposes the todos slice and pending/completed getters', () => {
    const store = useTodoStore()
    expect(Array.isArray(store.todos)).toBe(true)
    expect(Array.isArray(store.pendingTodos)).toBe(true)
    expect(Array.isArray(store.completedTodos)).toBe(true)
  })
})
