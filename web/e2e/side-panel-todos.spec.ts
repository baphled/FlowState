import { test, expect } from '@playwright/test'

const agents = [
  { id: 'planner', name: 'Planner', description: 'Plans work', model: 'claude-sonnet-4-6' },
  { id: 'researcher', name: 'Researcher', description: 'Reads the docs', model: 'llama3.2' },
]

const sessions = [
  {
    id: 'session-parent-001',
    agentId: 'planner',
    title: 'Parent planning session',
    updatedAt: '2026-05-04T09:00:00Z',
    createdAt: '2026-05-04T08:00:00Z',
    messageCount: 1,
  },
  {
    id: 'session-child-77',
    agentId: 'researcher',
    parentId: 'session-parent-001',
    title: 'Delegated research',
    updatedAt: '2026-05-04T09:30:00Z',
    createdAt: '2026-05-04T09:15:00Z',
    messageCount: 2,
  },
]

const messagesBySession: Record<string, Array<{ role: string; content: string }>> = {
  'session-parent-001': [
    { role: 'user', content: 'Kick off the planning' },
    { role: 'assistant', content: 'Planning session intro from the API.' },
  ],
  'session-child-77': [
    { role: 'user', content: 'Research the topic' },
    { role: 'assistant', content: 'Delegated research summary from the API.' },
  ],
}

const swarmEvents = [
  {
    id: 'evt-delegation-77',
    type: 'delegation',
    status: 'started',
    timestamp: '2026-05-04T09:15:00Z',
    agent_id: 'researcher',
    metadata: {
      source_agent: 'planner',
      target_agent: 'researcher',
      child_session_id: 'session-child-77',
      status: 'started',
    },
  },
]

const getSessionId = (url: string) => url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? ''

test.describe('Side panel reserved for todos', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/agents', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(agents),
      })
    })

    await page.route('**/api/v1/sessions', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(sessions),
      })
    })

    await page.route('**/api/v1/sessions/**/messages', async (route) => {
      const sessionId = getSessionId(route.request().url())
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(messagesBySession[sessionId] ?? []),
      })
    })

    // The swarm SSE endpoint streams JSON lines; the route mock here returns
    // a one-shot delegation event so the chat thread can surface it as an
    // inline system entry. The DelegationStrip subscribes to swarmStore and
    // renders this in the chat-main region, NOT the sidebar.
    await page.route('**/api/swarm/events', async (route) => {
      const lines = swarmEvents.map((e) => `data: ${JSON.stringify(e)}`)
      lines.push('data: [DONE]')
      await route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        body: lines.join('\n') + '\n',
      })
    })

    await page.addInitScript(() => {
      window.localStorage.clear()
    })

    await page.goto('/chat')
  })

  test('side panel renders the todo list and not delegation/swarm cards', async ({ page }) => {
    const sidebar = page.getByTestId('swarm-pane')
    await expect(sidebar).toBeVisible()

    // The todo panel is the only content reserved for the side panel.
    await expect(sidebar.getByTestId('todo-list-panel')).toBeVisible()

    // Delegation/tool/plan panels must not appear in the side panel.
    await expect(sidebar.getByTestId('delegation-panel')).toHaveCount(0)
    await expect(sidebar.getByTestId('tool-call-panel')).toHaveCount(0)
    await expect(sidebar.getByTestId('plan-panel')).toHaveCount(0)
  })

  test('side panel exposes no user-add affordance and is unreachable by keyboard', async ({ page }) => {
    const sidebar = page.getByTestId('swarm-pane')
    const panel = sidebar.getByTestId('todo-list-panel')
    await expect(panel).toBeVisible()

    // Todos are agent-emitted (todowrite tool); the user is purely an
    // observer. No input, no add button, and Tab navigation must not land
    // on any focusable control inside the panel that mutates state.
    await expect(panel.getByTestId('todo-input')).toHaveCount(0)
    await expect(panel.getByTestId('todo-add-btn')).toHaveCount(0)
    await expect(panel.getByTestId('todo-delete-btn')).toHaveCount(0)
    await expect(panel.locator('input[type="text"]')).toHaveCount(0)
    await expect(panel.locator('button')).toHaveCount(0)
  })

  test('shows the session-aware empty-state copy when the current session has no todowrite results', async ({ page }) => {
    const sidebar = page.getByTestId('swarm-pane')
    const panel = sidebar.getByTestId('todo-list-panel')
    await expect(panel).toBeVisible()

    // The mocked sessions above carry no todowrite tool_result messages, so
    // the side panel should explicitly say "No todos in this session yet"
    // rather than the previous user-CRUD-era "No tasks yet".
    const empty = panel.getByTestId('todo-empty')
    await expect(empty).toBeVisible()
    await expect(empty).toContainText('No todos in this session yet')
  })

  test('hydrates the side-panel todos from the loaded session\'s history', async ({ page }) => {
    // Override the messages route so the parent session carries a historical
    // todowrite tool_result whose content is the raw JSON the agent emitted.
    await page.route('**/api/v1/sessions/**/messages', async (route) => {
      const sessionId = getSessionId(route.request().url())
      if (sessionId === 'session-parent-001') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify([
            { id: 'm1', role: 'user', content: 'plan something', timestamp: '' },
            {
              id: 'm2',
              role: 'tool_call',
              toolName: 'todowrite',
              content: 'todowrite',
              timestamp: '',
            },
            {
              id: 'm3',
              role: 'tool_result',
              toolName: 'todowrite',
              content: JSON.stringify([
                { content: 'historical from parent', status: 'pending', priority: 'high' },
                { content: 'second one', status: 'completed', priority: 'low' },
              ]),
              timestamp: '',
            },
          ]),
        })
        return
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(messagesBySession[sessionId] ?? []),
      })
    })

    await page.reload()

    const sidebar = page.getByTestId('swarm-pane')
    const panel = sidebar.getByTestId('todo-list-panel')
    await expect(panel).toBeVisible()
    await expect(panel.getByText('historical from parent')).toBeVisible()
    await expect(panel.getByText('second one')).toBeVisible()
  })

  test('delegation events are reachable from the chat thread and switch session on click', async ({ page }) => {
    // The delegation event mocked above must surface inside the chat-main
    // region (DelegationStrip), NOT in the sidebar. Clicking it switches the
    // active session via chatStore.loadSessionMessages(child_session_id).
    const main = page.locator('.chat-main')
    const strip = main.getByTestId('delegation-strip')
    await expect(strip).toBeVisible()

    const entry = strip.getByTestId('delegation-entry-evt-delegation-77')
    await expect(entry).toBeVisible()
    await expect(entry).toHaveAttribute('role', 'button')

    await entry.click()

    const messageList = page.getByTestId('message-list')
    await expect(messageList).toContainText('Delegated research summary from the API.')
  })
})
