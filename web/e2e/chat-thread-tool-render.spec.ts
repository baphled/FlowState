import { test, expect } from '@playwright/test'

// Chat-thread MessageBubble must render TodoTool for both `todowrite` and
// `todo_update` tool_result rows in a session's historical message list —
// NOT GenericTool's raw JSON pre-block.
//
// Production bug:
//
//   registerTools() runs in ChatView.onMounted (web/src/views/ChatView.vue:444).
//   Vue's onMounted fires AFTER the component's initial template render. The
//   message-list children (MessageBubble instances) mount as part of that
//   initial render, so each bubble's
//
//     const toolComponent = computed(() => getToolComponent(...) ?? GenericTool)
//
//   evaluates against a STILL-EMPTY toolComponents Map. Because the Map is a
//   plain non-reactive `new Map()`, when registerTools() later mutates it the
//   computed never re-evaluates. Every todowrite / todo_update tool_result
//   in the static message-list silently renders the GenericTool fallback —
//   surfacing the raw JSON output the user explicitly complained about
//   ("we should never see the raw command").
//
// Test seam:
//
//   `data-component="todo-tool"` is set by TodoTool.vue. Its presence inside
//   `[data-testid="message-list"]` means MessageBubble's component dispatch
//   picked TodoTool from the registry — i.e. registerTools() ran BEFORE
//   the bubble computed its renderer. Conversely, `data-component="generic-
//   tool-output"` (set by GenericTool.vue's <pre><code>) means the bubble
//   fell back to the raw-JSON renderer.
//
// PR7-Web's unit tests passed because the vitest harness calls
// registerTools() synchronously before render. Runtime mount order is
// different — this Playwright spec drives the production path end-to-end.

const agents = [
  {
    id: 'planner',
    name: 'Planner',
    description: 'Plans work',
    model: 'claude-sonnet-4-6',
  },
]

const sessions = [
  {
    id: 'session-todo-render',
    agentId: 'planner',
    title: 'Todo render session',
    updatedAt: '2026-05-04T09:00:00Z',
    createdAt: '2026-05-04T08:00:00Z',
    messageCount: 5,
  },
]

// History layout:
//
//   user "plan something"
//   tool_result todowrite     — initial three-item list, mixed statuses
//   user "make progress"
//   tool_result todo_update   — same items, with status transitions applied
//
// The todo_update mirror covers the same render gap for the per-flip tool
// the agent emits between todowrite snapshots — see registerTools.ts:39-49.
const todowriteBody = JSON.stringify([
  { content: 'todowrite-first', status: 'pending', priority: 'high' },
  { content: 'todowrite-second', status: 'in_progress', priority: 'high' },
  { content: 'todowrite-third', status: 'completed', priority: 'low' },
])

const todoUpdateBody = JSON.stringify([
  { content: 'todoupdate-first', status: 'pending', priority: 'high' },
  { content: 'todoupdate-second', status: 'in_progress', priority: 'high' },
  { content: 'todoupdate-third', status: 'completed', priority: 'low' },
])

const messagesBySession: Record<string, Array<{ id: string; role: string; content: string; toolName?: string; timestamp: string }>> = {
  'session-todo-render': [
    { id: 'm1', role: 'user', content: 'plan something', timestamp: '2026-05-04T08:01:00Z' },
    {
      id: 'm2',
      role: 'tool_result',
      toolName: 'todowrite',
      content: todowriteBody,
      timestamp: '2026-05-04T08:02:00Z',
    },
    { id: 'm3', role: 'user', content: 'make progress', timestamp: '2026-05-04T08:03:00Z' },
    {
      id: 'm4',
      role: 'tool_result',
      toolName: 'todo_update',
      content: todoUpdateBody,
      timestamp: '2026-05-04T08:04:00Z',
    },
  ],
}

const getSessionId = (url: string): string =>
  url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? ''

test.describe('Chat thread renders TodoTool for todowrite/todo_update on first paint', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/agents', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(agents),
      })
    })

    await page.route('**/api/v1/models', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ providers: [] }),
      })
    })

    await page.route('**/api/v1/sessions', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(sessions),
      })
    })

    await page.route('**/api/v1/sessions/*/messages', async (route) => {
      const sessionId = getSessionId(route.request().url())
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(messagesBySession[sessionId] ?? []),
      })
    })

    await page.route('**/api/swarm/events', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        body: 'data: [DONE]\n',
      })
    })

    await page.addInitScript(() => {
      window.localStorage.clear()
      // Pre-seed the active session so restoreStateFromBackend picks the
      // session whose history we mocked above. Without this the bootstrap
      // path may not have a session selected on first paint.
      window.localStorage.setItem(
        'flowstate.activeSessionId',
        'session-todo-render',
      )
      window.localStorage.setItem('flowstate.activeAgentId', 'planner')
    })

    await page.goto('/chat')
  })

  test('todowrite tool_result renders TodoTool, not GenericTool raw JSON', async ({ page }) => {
    const messageList = page.getByTestId('message-list')
    await expect(messageList).toBeVisible()

    // The historical todowrite row must surface a TodoTool render — the
    // `data-component="todo-tool"` seam is set on TodoTool.vue's renderer
    // root. Its presence proves MessageBubble's component-dispatch picked
    // TodoTool from the registry on first paint.
    const todoRenderers = messageList.locator('[data-component="todo-tool"]')
    // Two tool_result rows (todowrite + todo_update) both map to TodoTool.
    await expect(todoRenderers).toHaveCount(2)

    // Conversely, the GenericTool raw-JSON <pre><code> output must NOT
    // appear inside the message-list. Pre-fix the same two rows render
    // GenericTool because the registry was empty when MessageBubble's
    // computed evaluated.
    await expect(
      messageList.locator('[data-component="generic-tool-output"]'),
    ).toHaveCount(0)
  })

  test('todowrite renders the canonical todo items with status attributes', async ({ page }) => {
    const messageList = page.getByTestId('message-list')
    await expect(messageList).toBeVisible()

    // Locate the todowrite tool_result row specifically. MessageBubble
    // wraps each row with data-testid="message-tool_result"; we narrow
    // to the first one (matches m2 above, the todowrite snapshot).
    const todoRenderers = messageList.locator('[data-component="todo-tool"]')
    const todowriteRenderer = todoRenderers.first()

    const items = todowriteRenderer.locator('[data-testid="todo-item"]')
    await expect(items).toHaveCount(3)
    await expect(items.nth(0)).toHaveAttribute('data-status', 'pending')
    await expect(items.nth(0)).toContainText('todowrite-first')
    await expect(items.nth(1)).toHaveAttribute('data-status', 'in_progress')
    await expect(items.nth(1)).toContainText('todowrite-second')
    await expect(items.nth(2)).toHaveAttribute('data-status', 'completed')
    await expect(items.nth(2)).toContainText('todowrite-third')
  })

  test('todo_update renders the canonical todo items with status attributes', async ({ page }) => {
    const messageList = page.getByTestId('message-list')
    await expect(messageList).toBeVisible()

    // The second todo render in message order is the todo_update row (m4).
    // PR7 W1 registered `todo_update` alongside `todowrite` so both names
    // dispatch to TodoTool — this assertion exercises the second entry.
    const todoRenderers = messageList.locator('[data-component="todo-tool"]')
    const todoUpdateRenderer = todoRenderers.nth(1)

    const items = todoUpdateRenderer.locator('[data-testid="todo-item"]')
    await expect(items).toHaveCount(3)
    await expect(items.nth(0)).toHaveAttribute('data-status', 'pending')
    await expect(items.nth(0)).toContainText('todoupdate-first')
    await expect(items.nth(1)).toHaveAttribute('data-status', 'in_progress')
    await expect(items.nth(1)).toContainText('todoupdate-second')
    await expect(items.nth(2)).toHaveAttribute('data-status', 'completed')
    await expect(items.nth(2)).toContainText('todoupdate-third')
  })
})
