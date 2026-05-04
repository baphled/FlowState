import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import TodoTool from './TodoTool.vue'

const ToolBubble = {
  props: ['toolName', 'title', 'subtitle', 'status'],
  template: `
    <div data-testid="tool-bubble" data-component="tool" :data-tool="toolName" :data-status="status">
      <slot />
    </div>
  `,
}

describe('TodoTool', () => {
  it('renders todowrite JSON as a checkbox list', () => {
    const body = JSON.stringify([
      { content: 'write the failing spec', status: 'pending', priority: 'high' },
      { content: 'make it pass', status: 'completed', priority: 'medium' },
      { content: 'add the regression test', status: 'in_progress', priority: 'low' },
    ])

    const wrapper = mount(TodoTool, {
      props: {
        toolName: 'todowrite',
        heading: 'todowrite',
        body,
        status: 'completed',
      },
      global: {
        stubs: { ToolBubble },
      },
    })

    expect(wrapper.find('[data-component="todo-tool"]').exists()).toBe(true)
    const items = wrapper.findAll('[data-testid="todo-item"]')
    expect(items).toHaveLength(3)

    // The pending item is unchecked.
    expect(items[0].attributes('data-status')).toBe('pending')
    expect(items[0].text()).toContain('write the failing spec')

    // The completed item is checked.
    expect(items[1].attributes('data-status')).toBe('completed')
    expect(items[1].text()).toContain('make it pass')

    // in_progress should be visually distinct (non-completed but not pending).
    expect(items[2].attributes('data-status')).toBe('in_progress')
    expect(items[2].text()).toContain('add the regression test')
  })

  it('shows an empty-state when the array is empty', () => {
    const wrapper = mount(TodoTool, {
      props: {
        toolName: 'todowrite',
        heading: 'todowrite',
        body: '[]',
      },
      global: { stubs: { ToolBubble } },
    })

    expect(wrapper.find('[data-component="todo-tool"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-testid="todo-item"]')).toHaveLength(0)
    expect(wrapper.text()).toContain('Todo list cleared')
  })

  it('falls back gracefully when the body is not valid JSON', () => {
    const wrapper = mount(TodoTool, {
      props: {
        toolName: 'todowrite',
        heading: 'todowrite',
        body: 'not json',
      },
      global: { stubs: { ToolBubble } },
    })

    expect(wrapper.find('[data-component="todo-tool"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('todos updated')
  })
})
