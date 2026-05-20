import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import TodoTool from './TodoTool.vue'

const ToolBubble = {
  props: ['toolName', 'title', 'subtitle', 'status', 'defaultOpen'],
  template: `
    <div data-testid="tool-bubble" data-component="tool" :data-tool="toolName" :data-status="status" :data-default-open="defaultOpen ? 'true' : 'false'">
      <slot />
    </div>
  `,
}

describe('TodoTool', () => {
  // I4: Todos render as a checkbox list — always tabular. The subtitle
  // already shows N active / M total so collapsed is fine and avoids the
  // todo block dominating a busy thread.
  it('starts collapsed by default (always-tabular category)', () => {
    const wrapper = mount(TodoTool, {
      props: { toolName: 'todowrite', heading: 'todowrite', body: '[]', status: 'completed' },
      global: { stubs: { ToolBubble } },
    })
    expect(wrapper.get('[data-testid="tool-bubble"]').attributes('data-default-open')).toBe('false')
  })
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

  // PR7 W2 — parity with `todo_update`. The agent emits ONE `todowrite` for
  // the initial list and N × `todo_update` for each status transition. Both
  // tool names land at this renderer (post-PR7 wiring); the body shape is
  // identical (full JSON array of {content,status,priority}) because
  // `todo_update.Execute` returns the SAME post-patch list as `todowrite`.
  // This spec pins that the checkbox glyph mapping for all four statuses
  // (the TUI reference vocabulary — [ ] pending, [x] completed, [~]
  // in_progress, [-] cancelled) renders correctly when the tool name is
  // `todo_update` rather than `todowrite`. Catches future regressions that
  // re-narrow the renderer to a single name.
  it('renders todo_update JSON as a checkbox list using the same shape as todowrite', () => {
    const body = JSON.stringify([
      { content: 'pending one', status: 'pending', priority: 'high' },
      { content: 'finished one', status: 'completed', priority: 'medium' },
      { content: 'mid-flight one', status: 'in_progress', priority: 'low' },
      { content: 'dropped one', status: 'cancelled', priority: 'low' },
    ])

    const wrapper = mount(TodoTool, {
      props: {
        toolName: 'todo_update',
        heading: 'todo_update',
        body,
        status: 'completed',
      },
      global: { stubs: { ToolBubble } },
    })

    expect(wrapper.find('[data-component="todo-tool"]').exists()).toBe(true)
    const items = wrapper.findAll('[data-testid="todo-item"]')
    expect(items).toHaveLength(4)

    // [ ] pending — unchecked box, no strikethrough.
    expect(items[0].attributes('data-status')).toBe('pending')
    expect(items[0].text()).toContain('[ ]')
    expect(items[0].text()).toContain('pending one')

    // [x] completed — checked box.
    expect(items[1].attributes('data-status')).toBe('completed')
    expect(items[1].text()).toContain('[x]')
    expect(items[1].text()).toContain('finished one')

    // [~] in_progress — distinct from both pending and completed.
    expect(items[2].attributes('data-status')).toBe('in_progress')
    expect(items[2].text()).toContain('[~]')
    expect(items[2].text()).toContain('mid-flight one')

    // [-] cancelled — strikethrough, dimmed.
    expect(items[3].attributes('data-status')).toBe('cancelled')
    expect(items[3].text()).toContain('[-]')
    expect(items[3].text()).toContain('dropped one')
  })
})
