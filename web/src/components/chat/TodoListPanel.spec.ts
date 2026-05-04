import { describe, expect, it, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import TodoListPanel from './TodoListPanel.vue'
import { useTodoStore } from '@/stores/todoStore'
import type { Todo } from '@/stores/todoStore'

// Some sibling specs (e.g. ChatView.spec.ts) install partial localStorage
// mocks that lack `.clear()`. Provide a minimal in-memory shim local to
// this suite so the todo store starts each test with a clean slate without
// depending on the order of file execution.
const memoryStorage: Record<string, string> = {}
const storageMock: Storage = {
  get length() {
    return Object.keys(memoryStorage).length
  },
  key: vi.fn((idx: number) => Object.keys(memoryStorage)[idx] ?? null),
  getItem: vi.fn((key: string) => memoryStorage[key] ?? null),
  setItem: vi.fn((key: string, value: string) => {
    memoryStorage[key] = value
  }),
  removeItem: vi.fn((key: string) => {
    delete memoryStorage[key]
  }),
  clear: vi.fn(() => {
    for (const key of Object.keys(memoryStorage)) {
      delete memoryStorage[key]
    }
  }),
}

Object.defineProperty(window, 'localStorage', {
  value: storageMock,
  configurable: true,
  writable: true,
})

// Seed the store directly — todos are agent-emitted, the store has no
// user-add action. Tests stand in for the agent emission pipeline by
// mutating $state through pinia's $patch.
function seedTodos(todos: Todo[]): void {
  useTodoStore().$patch({ todos })
}

function makeTodo(overrides: Partial<Todo> = {}): Todo {
  return {
    id: overrides.id ?? `todo-${Math.random().toString(36).slice(2, 8)}`,
    content: overrides.content ?? 'sample todo',
    status: overrides.status ?? 'pending',
    createdAt: overrides.createdAt ?? '2026-05-04T09:00:00Z',
    completedAt: overrides.completedAt,
  }
}

describe('TodoListPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    storageMock.clear()
  })

  it('renders the live todo list from the shared todoStore', async () => {
    seedTodos([
      makeTodo({ content: 'write side-panel spec' }),
      makeTodo({ content: 'extract TodoListPanel' }),
    ])

    const wrapper = mount(TodoListPanel)
    await flushPromises()

    const items = wrapper.findAll('[data-testid="todo-item"]')
    expect(items).toHaveLength(2)
    expect(wrapper.text()).toContain('write side-panel spec')
    expect(wrapper.text()).toContain('extract TodoListPanel')
  })

  it('reflects subsequent updates to the shared store without remounting', async () => {
    const wrapper = mount(TodoListPanel)
    await flushPromises()

    expect(wrapper.findAll('[data-testid="todo-item"]')).toHaveLength(0)

    useTodoStore().$patch({
      todos: [makeTodo({ content: 'emitted after mount' })],
    })
    await flushPromises()

    const items = wrapper.findAll('[data-testid="todo-item"]')
    expect(items).toHaveLength(1)
    expect(wrapper.text()).toContain('emitted after mount')
  })

  it('exposes a stable testid hook for the side-panel mount point', () => {
    const wrapper = mount(TodoListPanel)
    expect(wrapper.find('[data-testid="todo-list-panel"]').exists()).toBe(true)
  })

  it('does not surface a user-add affordance in the template', () => {
    const wrapper = mount(TodoListPanel)
    // The user is a pure observer — todos are agent-emitted via the
    // todowrite tool. No add input, no add button, no add hook.
    expect(wrapper.find('[data-testid="todo-input"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="todo-add-btn"]').exists()).toBe(false)
    expect(wrapper.find('input[type="text"]').exists()).toBe(false)
  })

  it('does not surface a per-item delete or toggle affordance', async () => {
    seedTodos([makeTodo({ content: 'agent emitted' })])

    const wrapper = mount(TodoListPanel)
    await flushPromises()

    expect(wrapper.find('[data-testid="todo-delete-btn"]').exists()).toBe(false)
    // Status indicator may exist (e.g. a glyph), but no interactive checkbox
    // or click handler that would mutate store state.
    expect(wrapper.find('[data-testid="todo-item"] input[type="checkbox"]').exists()).toBe(false)
  })
})
