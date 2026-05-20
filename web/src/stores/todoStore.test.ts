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

  // PR7 W4 — history hydration regression pin. A session whose latest todo
  // state was set by `todo_update` (rather than `todowrite`) reloads to an
  // empty panel pre-PR7 because the hydration filter accepts only the
  // literal `"todowrite"`. The agent contract is: one `todowrite` (initial
  // list) + N × `todo_update` (per-status-flip), so the most recent
  // tool_result message on a healthy session is almost always a
  // `todo_update`. The widened filter must accept both names AND respect
  // the "latest wins" ordering — a later `todo_update` overrides an earlier
  // `todowrite`. The body shape is identical (full post-patch list).
  it('derives the current todo list from the latest todo_update tool_result in history', () => {
    const store = useTodoStore()
    const messages: Message[] = [
      makeMessage({ id: 'm1', role: 'user', content: 'kick off' }),
      makeMessage({
        id: 'm2',
        role: 'tool_result',
        toolName: 'todowrite',
        content: JSON.stringify([
          { content: 'initial-1', status: 'pending', priority: 'low' },
          { content: 'initial-2', status: 'pending', priority: 'low' },
        ]),
      }),
      // The agent later flips initial-1 to completed via todo_update;
      // todo_update returns the full updated list. Post-PR7 the hydration
      // path must accept this as the canonical state. The 'completed' flip
      // is load-bearing — todoStore's binary status model collapses
      // 'in_progress' onto 'pending', so an in_progress flip would not
      // distinguish m2 from m3 in the assertion. Using 'completed' makes
      // the regression visible: pre-PR7 the hydration filter rejects m3,
      // so initial-1 stays at the m2 'pending' snapshot.
      makeMessage({
        id: 'm3',
        role: 'tool_result',
        toolName: 'todo_update',
        content: JSON.stringify([
          { content: 'initial-1', status: 'completed', priority: 'low' },
          { content: 'initial-2', status: 'pending', priority: 'low' },
        ]),
      }),
    ]

    store.setCurrentSession('session-update-hydrate')
    store.hydrateFromMessages('session-update-hydrate', messages)

    expect(store.todos).toHaveLength(2)
    // Latest todo_update wins — initial-1 is now completed. Pre-PR7 the
    // todo_update message is invisible to the loop and initial-1 reverts
    // to the m2 'pending' snapshot.
    expect(store.todos[0].content).toBe('initial-1')
    expect(store.todos[0].status).toBe('completed')
    expect(store.todos[1].content).toBe('initial-2')
    expect(store.todos[1].status).toBe('pending')
  })

  // Walks the full agent flow: todowrite → todo_update → todo_update. The
  // "latest wins" loop must observe ordering AND accept either tool name
  // as a valid canonical-state source.
  it('respects latest-wins ordering when todowrite and todo_update interleave', () => {
    const store = useTodoStore()
    const messages: Message[] = [
      makeMessage({
        id: 'm1',
        role: 'tool_result',
        toolName: 'todowrite',
        content: JSON.stringify([
          { content: 'step-a', status: 'pending', priority: 'high' },
        ]),
      }),
      makeMessage({
        id: 'm2',
        role: 'tool_result',
        toolName: 'todo_update',
        content: JSON.stringify([
          { content: 'step-a', status: 'in_progress', priority: 'high' },
        ]),
      }),
      makeMessage({
        id: 'm3',
        role: 'tool_result',
        toolName: 'todo_update',
        content: JSON.stringify([
          { content: 'step-a', status: 'completed', priority: 'high' },
        ]),
      }),
    ]

    store.setCurrentSession('session-interleave')
    store.hydrateFromMessages('session-interleave', messages)

    expect(store.todos).toHaveLength(1)
    expect(store.todos[0].content).toBe('step-a')
    // The last update flipped to completed — that's the canonical state.
    expect(store.todos[0].status).toBe('completed')
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
