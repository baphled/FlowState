import { describe, expect, it, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import TodoListPanel from './TodoListPanel.vue'
import { useTodoStore } from '@/stores/todoStore'

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

describe('TodoListPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    storageMock.clear()
  })

  it('renders the live todo list from the shared todoStore', async () => {
    const todoStore = useTodoStore()
    todoStore.addTodo('write side-panel spec')
    todoStore.addTodo('extract TodoListPanel')

    const wrapper = mount(TodoListPanel)
    await flushPromises()

    const items = wrapper.findAll('[data-testid="todo-item"]')
    expect(items).toHaveLength(2)
    expect(wrapper.text()).toContain('write side-panel spec')
    expect(wrapper.text()).toContain('extract TodoListPanel')
  })

  it('reflects subsequent additions to the shared store without remounting', async () => {
    const todoStore = useTodoStore()

    const wrapper = mount(TodoListPanel)
    await flushPromises()

    expect(wrapper.findAll('[data-testid="todo-item"]')).toHaveLength(0)

    todoStore.addTodo('added after mount')
    await flushPromises()

    const items = wrapper.findAll('[data-testid="todo-item"]')
    expect(items).toHaveLength(1)
    expect(wrapper.text()).toContain('added after mount')
  })

  it('toggles a todo through the shared store when its checkbox is clicked', async () => {
    const todoStore = useTodoStore()
    todoStore.addTodo('finish the rebase')
    const todoId = todoStore.todos[0].id

    const wrapper = mount(TodoListPanel)
    await flushPromises()

    const checkbox = wrapper.find('[data-testid="todo-item"] input[type="checkbox"]')
    expect((checkbox.element as HTMLInputElement).checked).toBe(false)

    await checkbox.setValue(true)
    await flushPromises()

    expect(todoStore.todos.find((t) => t.id === todoId)?.status).toBe('completed')
  })

  it('exposes a stable testid hook for the side-panel mount point', () => {
    const wrapper = mount(TodoListPanel)
    expect(wrapper.find('[data-testid="todo-list-panel"]').exists()).toBe(true)
  })
})
