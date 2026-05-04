import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useTodoStore } from './todoStore'
import type { Message } from '@/types'

// The todoStore is a read-only projection of agent-emitted todos that arrive
// over the SSE pipeline as `tool_result` events for the `todowrite` tool. The
// user is purely an observer — there are no user-driven mutators (no add, no
// toggle, no delete). State is keyed by session id internally so switching
// sessions changes the displayed slice and switching back restores it.
//
// The agent's todowrite tool emits a JSON array of items shaped like
//   { content: string, status: string, priority: string }
// (see internal/tui/uikit/widgets/todo_widget.go:11-16 todoItem). On the
// persisted history side, the backend stores a tool_result message with
// toolName === "todowrite" and Content === <raw JSON>. Both ingestion and
// hydration consume that same JSON shape.

const TODOWRITE_JSON = JSON.stringify([
  { content: 'first todo', status: 'pending', priority: 'high' },
  { content: 'second todo', status: 'completed', priority: 'low' },
])

const TODOWRITE_JSON_OTHER = JSON.stringify([
  { content: 'other-session todo', status: 'in_progress', priority: 'medium' },
])

function makeMessage(overrides: Partial<Message> & { id: string; role: string }): Message {
  return {
    content: '',
    timestamp: '2026-05-04T09:00:00Z',
    ...overrides,
  }
}

describe('todoStore — read-only contract', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('does not expose user-driven mutators', () => {
    const store = useTodoStore()
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

describe('todoStore — agent-emit ingestion', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('parses a todowrite tool_result JSON payload into the active session\'s slice', () => {
    const store = useTodoStore()
    store.setCurrentSession('session-A')

    store.ingestToolResult('session-A', TODOWRITE_JSON)

    expect(store.todos).toHaveLength(2)
    expect(store.todos[0].content).toBe('first todo')
    expect(store.todos[0].status).toBe('pending')
    expect(store.todos[1].status).toBe('completed')
  })

  it('keeps todos for inactive sessions out of the displayed slice', () => {
    const store = useTodoStore()
    store.setCurrentSession('session-A')
    store.ingestToolResult('session-A', TODOWRITE_JSON)
    store.ingestToolResult('session-B', TODOWRITE_JSON_OTHER)

    // Active session is A; B's todos must not bleed into store.todos.
    expect(store.todos).toHaveLength(2)
    expect(store.todos.every((t) => t.content !== 'other-session todo')).toBe(true)
  })

  it('replaces the per-session slice on each emission (todowrite is the canonical state)', () => {
    const store = useTodoStore()
    store.setCurrentSession('session-A')
    store.ingestToolResult('session-A', TODOWRITE_JSON)
    expect(store.todos).toHaveLength(2)

    const next = JSON.stringify([
      { content: 'replaced', status: 'pending', priority: 'low' },
    ])
    store.ingestToolResult('session-A', next)

    expect(store.todos).toHaveLength(1)
    expect(store.todos[0].content).toBe('replaced')
  })

  it('treats non-JSON tool_result content as a no-op without throwing', () => {
    const store = useTodoStore()
    store.setCurrentSession('session-A')

    expect(() => store.ingestToolResult('session-A', 'not-json')).not.toThrow()
    expect(store.todos).toHaveLength(0)
  })
})

describe('todoStore — session swap', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('swaps the displayed slice when the active session changes', () => {
    const store = useTodoStore()

    store.setCurrentSession('session-A')
    store.ingestToolResult('session-A', TODOWRITE_JSON)
    store.ingestToolResult('session-B', TODOWRITE_JSON_OTHER)

    expect(store.todos.map((t) => t.content)).toEqual(['first todo', 'second todo'])

    store.setCurrentSession('session-B')
    expect(store.todos.map((t) => t.content)).toEqual(['other-session todo'])

    store.setCurrentSession('session-A')
    expect(store.todos.map((t) => t.content)).toEqual(['first todo', 'second todo'])
  })

  it('returns an empty list for a session that has no recorded todos', () => {
    const store = useTodoStore()
    store.setCurrentSession('session-fresh')
    expect(store.todos).toEqual([])
    expect(store.pendingTodos).toEqual([])
    expect(store.completedTodos).toEqual([])
  })
})

describe('todoStore — hydration from session history', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('derives the current todo list from the latest todowrite tool_result in history', () => {
    const store = useTodoStore()
    const messages: Message[] = [
      makeMessage({ id: 'm1', role: 'user', content: 'kick off' }),
      makeMessage({
        id: 'm2',
        role: 'tool_call',
        toolName: 'todowrite',
        content: 'todowrite',
      }),
      makeMessage({
        id: 'm3',
        role: 'tool_result',
        toolName: 'todowrite',
        content: JSON.stringify([
          { content: 'stale-1', status: 'pending', priority: 'low' },
        ]),
      }),
      makeMessage({
        id: 'm4',
        role: 'tool_result',
        toolName: 'todowrite',
        content: TODOWRITE_JSON,
      }),
      makeMessage({ id: 'm5', role: 'assistant', content: 'done' }),
    ]

    store.setCurrentSession('session-hydrate')
    store.hydrateFromMessages('session-hydrate', messages)

    expect(store.todos).toHaveLength(2)
    expect(store.todos.map((t) => t.content)).toEqual(['first todo', 'second todo'])
  })

  it('ignores tool_result messages for tools other than todowrite', () => {
    const store = useTodoStore()
    const messages: Message[] = [
      makeMessage({
        id: 'm1',
        role: 'tool_result',
        toolName: 'bash',
        content: 'ls -la output',
      }),
      makeMessage({
        id: 'm2',
        role: 'tool_result',
        toolName: 'edit',
        content: 'edit applied',
      }),
    ]

    store.setCurrentSession('session-no-todos')
    store.hydrateFromMessages('session-no-todos', messages)

    expect(store.todos).toEqual([])
  })

  it('clears the slice for a session whose history holds no todowrite results', () => {
    const store = useTodoStore()
    // Pre-seed the session with stale todos (simulating a previous live ingestion).
    store.setCurrentSession('session-X')
    store.ingestToolResult('session-X', TODOWRITE_JSON)
    expect(store.todos).toHaveLength(2)

    // Hydrating from a history that contains no todowrite results must reset
    // the slice — the canonical state is "no todos", not the leftover live one.
    store.hydrateFromMessages('session-X', [
      makeMessage({ id: 'm1', role: 'user', content: 'hi' }),
    ])
    expect(store.todos).toEqual([])
  })
})

describe('todoStore — persistence model', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('does not read or write the legacy global flowstate-todos localStorage key', () => {
    // The parent branch left a global localStorage key from the user-CRUD era.
    // Now that todos are session-scoped and derived from session history, the
    // global key is meaningless and must not be referenced.
    //
    // This spec spies on getItem/setItem and asserts neither was ever called
    // with the legacy key — the strongest possible "the store does not touch
    // it" check across constructor, ingestion, and hydration paths.
    const getSpy = vi.spyOn(Storage.prototype, 'getItem')
    const setSpy = vi.spyOn(Storage.prototype, 'setItem')

    try {
      const store = useTodoStore()
      store.setCurrentSession('session-A')
      store.ingestToolResult(
        'session-A',
        JSON.stringify([{ content: 'live', status: 'pending', priority: 'low' }]),
      )
      store.hydrateFromMessages('session-A', [])

      const touchedLegacyGet = getSpy.mock.calls.some(([key]) => key === 'flowstate-todos')
      const touchedLegacySet = setSpy.mock.calls.some(([key]) => key === 'flowstate-todos')

      expect(touchedLegacyGet).toBe(false)
      expect(touchedLegacySet).toBe(false)
    } finally {
      getSpy.mockRestore()
      setSpy.mockRestore()
    }
  })
})
