import { test, expect } from '@playwright/test'

// Live regression cover for the bug where clicking a delegation card in the
// chat thread navigated to /agents/:id (AgentInfoView) instead of loading the
// delegated child session in the same chat view. The fault was a
// <router-link to="/agents/:id" @click.prevent="...">: vue-router 4 still
// pushed the route despite the `.prevent` modifier, sending the user to
// AgentInfoView and forcing them to manually navigate back via `Chat`.
//
// The acceptance contract this spec pins:
//   1. Clicking the delegation card MUST keep the URL on `/chat` (no
//      `/agents/:id` navigation).
//   2. The chat view MUST stay mounted (no AgentInfoView render).
//   3. The chat store MUST load the delegated session (here keyed on the
//      target agent id, matching MessageBubble.loadDelegatedSession).
test.describe('Delegation card navigation', () => {
  const agents = [
    { id: 'planner', name: 'Planner', description: 'Plans work', model: 'claude-sonnet-4-6' },
    { id: 'executor', name: 'Executor', description: 'Runs work', model: 'llama3.2' },
  ]

  // Two sessions: parent (planner) holds the delegation message that points
  // at executor; child (executor) is what should load when the card is
  // clicked. The MessageBubble click resolves session-by-agent-id, so the
  // executor session is the one chatStore.loadSessionByAgentId('executor')
  // will switch to.
  const sessions = [
    {
      id: 'session-parent-001',
      agentId: 'planner',
      title: 'Parent Plan',
      messageCount: 1,
      createdAt: '2026-05-01T09:00:00Z',
      updatedAt: '2026-05-01T09:00:00Z',
    },
    {
      id: 'session-child-001',
      agentId: 'executor',
      parentId: 'session-parent-001',
      title: 'Delegated Run',
      messageCount: 2,
      createdAt: '2026-05-01T09:01:00Z',
      updatedAt: '2026-05-01T09:01:00Z',
    },
  ]

  const messagesBySession: Record<string, Array<Record<string, unknown>>> = {
    'session-parent-001': [
      {
        id: 'msg-parent-1',
        role: 'user',
        content: 'Please delegate to executor.',
        timestamp: '2026-05-01T09:00:00Z',
      },
      {
        id: 'msg-delegation-1',
        role: 'delegation',
        content: 'delegated to executor',
        targetAgent: 'executor',
        chainId: 'chain-1',
        status: 'completed',
        timestamp: '2026-05-01T09:00:30Z',
      },
    ],
    'session-child-001': [
      {
        id: 'msg-child-1',
        role: 'user',
        content: 'Run the build please.',
        timestamp: '2026-05-01T09:01:00Z',
      },
      {
        id: 'msg-child-2',
        role: 'assistant',
        content: 'CHILD SESSION ASSISTANT REPLY (executor).',
        timestamp: '2026-05-01T09:01:30Z',
      },
    ],
  }

  const getSessionId = (url: string) =>
    url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? ''

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

    await page.route('**/api/swarm/events', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
    })

    // Land on the parent session so the delegation card is in the thread.
    await page.addInitScript(() => {
      localStorage.setItem('chat.currentSessionId', 'session-parent-001')
      localStorage.setItem('chat.agentId', 'planner')
    })

    await page.goto('/chat')
  })

  test('clicking the delegation card loads the child session in chat without routing to AgentInfoView', async ({ page }) => {
    // The delegation card must render with the executor name as a button,
    // not a router-link to /agents/executor (which used to land on the
    // AgentInfoView). We assert the affordance type explicitly so the
    // contract regresses loudly if anyone re-introduces a router-link.
    const card = page.getByTestId('delegation-agent-link').first()
    await expect(card).toBeVisible()
    await expect(card).toHaveText('executor')
    expect(await card.evaluate((el) => el.tagName)).toBe('BUTTON')

    await card.click()

    // URL stays on /chat — the bug surfaced as `/agents/executor`.
    await expect(page).toHaveURL(/\/chat$/)

    // AgentInfoView must NOT have been rendered.
    await expect(page.getByTestId('agent-info-view')).toHaveCount(0)

    // The chat thread now reflects the executor child-session history.
    const messageList = page.getByTestId('message-list')
    await expect(messageList).toContainText('CHILD SESSION ASSISTANT REPLY (executor).')
    await expect(messageList).toContainText('Run the build please.')

    // We're now viewing the child session — the SessionSwitcher (and the
    // entire NavBar) is hidden in child sessions, so the message-list
    // content is the navigation-target signal we use here. The chat thread
    // contains the executor's reply, confirming the chatStore actually
    // swapped sessions rather than calling a no-op handler.
    await expect(page.getByTestId('nav-bar')).toHaveCount(0)

    // The agent/model selector bar stays visible on child sessions but the
    // pickers go into read-only display mode — the user can see *which*
    // model + provider the delegated agent used but cannot change them.
    const bar = page.getByTestId('input-selector-bar')
    await expect(bar).toBeVisible()
    await expect(bar.getByTestId('agent-picker')).toHaveClass(/is-readonly/)
    await expect(bar.getByTestId('model-picker')).toHaveClass(/is-readonly/)
  })
})
