import { test, expect } from '@playwright/test'

// End-to-end coverage for the five session-hierarchy navigation behaviours:
//   1. DelegationStrip click → load child session
//   2. Ctrl+X then Down chord → load most-recent child of current session
//   3. ArrowLeft / ArrowRight on a child → previous / next sibling
//   4. Toolbar (input-selector-bar) hidden when active session has parentId
//   5. ArrowUp on a child → load parent
//
// Run this spec with `playwright test --workers=1` so the keyboard chord
// (Ctrl+X then ArrowDown) isn't interleaved with another worker's keystrokes.

test.describe.configure({ mode: 'serial' })

test.describe('Session hierarchy navigation', () => {
  const agents = [
    { id: 'planner', name: 'Planner', description: 'Plans work', model: 'claude-sonnet-4-6' },
    { id: 'executor', name: 'Executor', description: 'Runs work', model: 'llama3.2' },
  ]

  // Hierarchy:
  //   parent-1 (no parent)
  //   ├── child-a  (createdAt earliest)
  //   ├── child-b
  //   └── child-c  (createdAt latest → "last delegated")
  const sessions = [
    {
      id: 'parent-1',
      agentId: 'planner',
      title: 'Parent Session',
      createdAt: '2026-05-01T09:00:00Z',
      updatedAt: '2026-05-01T12:00:00Z',
      messageCount: 4,
    },
    {
      id: 'child-a',
      agentId: 'executor',
      title: 'Child A',
      parentId: 'parent-1',
      createdAt: '2026-05-01T09:10:00Z',
      updatedAt: '2026-05-01T09:30:00Z',
      messageCount: 2,
    },
    {
      id: 'child-b',
      agentId: 'executor',
      title: 'Child B',
      parentId: 'parent-1',
      createdAt: '2026-05-01T09:20:00Z',
      updatedAt: '2026-05-01T09:40:00Z',
      messageCount: 2,
    },
    {
      id: 'child-c',
      agentId: 'executor',
      title: 'Child C',
      parentId: 'parent-1',
      createdAt: '2026-05-01T09:30:00Z',
      updatedAt: '2026-05-01T09:50:00Z',
      messageCount: 2,
    },
  ]

  const messagesBySession: Record<string, Array<{ role: string; content: string }>> = {
    'parent-1': [{ role: 'user', content: 'Parent thread' }],
    'child-a': [{ role: 'user', content: 'Child A thread' }],
    'child-b': [{ role: 'user', content: 'Child B thread' }],
    'child-c': [{ role: 'user', content: 'Child C thread' }],
  }

  // Surface a delegation event whose child_session_id points at child-b — used
  // by the "click a delegation card" test.
  const swarmEvents = [
    {
      id: 'evt-deleg-1',
      type: 'delegation',
      status: 'started',
      timestamp: '2026-05-01T09:20:00Z',
      agent_id: 'executor',
      metadata: {
        source_agent: 'planner',
        target_agent: 'executor',
        child_session_id: 'child-b',
      },
    },
  ]

  const getSessionId = (url: string): string => url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? ''

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

    // The swarmStore consumes a streaming SSE response from /api/swarm/events.
    // Fulfil with a single `data: {...}\n\n` frame so DelegationStrip can
    // surface the seeded delegation event.
    await page.route('**/api/swarm/events', async (route) => {
      const body = swarmEvents.map((e) => `data: ${JSON.stringify(e)}\n\n`).join('')
      await route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        body,
      })
    })

    // Start on the parent session so the chord/click flow has somewhere to go.
    await page.addInitScript(() => {
      window.localStorage.setItem('chat.currentSessionId', 'parent-1')
    })

    await page.goto('/chat')
  })

  test('hides the input-selector-bar in a child session and shows it in the parent', async ({ page }) => {
    // Parent session: bar visible.
    await expect(page.getByTestId('input-selector-bar')).toBeVisible()

    // Switch to a child via Left/Right (after Ctrl+X Down moves us to a child)
    // — but for this test we use the delegation card to land on a child.
    const strip = page.getByTestId('delegation-strip')
    await strip.locator('[data-testid^="delegation-entry-"]').first().click()

    // Bar should disappear in the child session.
    await expect(page.getByTestId('input-selector-bar')).toHaveCount(0)
  })

  test('clicking a delegation card loads the child session directly', async ({ page }) => {
    const strip = page.getByTestId('delegation-strip')
    await expect(strip).toBeVisible()

    await strip.locator('[data-testid^="delegation-entry-"]').first().click()

    // The session switcher reflects the navigation target.
    await expect(page.getByTestId('session-switcher').getByRole('button')).toContainText(/Child B/)
  })

  test('Ctrl+X then ArrowDown loads the most-recent child (child-c)', async ({ page }) => {
    // Move focus off the composer onto the message pane so the keybinds fire.
    await page.getByTestId('chat-message-pane').click()

    await page.keyboard.down('Control')
    await page.keyboard.press('x')
    await page.keyboard.up('Control')
    await page.keyboard.press('ArrowDown')

    // child-c has the latest createdAt of all parent-1 children.
    await expect(page.getByTestId('session-switcher').getByRole('button')).toContainText(/Child C/)
  })

  test('ArrowLeft and ArrowRight navigate siblings inside a child session', async ({ page }) => {
    // Start by entering child-b via the delegation strip.
    await page.getByTestId('delegation-strip')
      .locator('[data-testid^="delegation-entry-"]')
      .first()
      .click()
    await expect(page.getByTestId('session-switcher').getByRole('button')).toContainText(/Child B/)

    // Move focus off any input.
    await page.getByTestId('chat-message-pane').click()

    await page.keyboard.press('ArrowLeft')
    await expect(page.getByTestId('session-switcher').getByRole('button')).toContainText(/Child A/)

    await page.keyboard.press('ArrowRight')
    await expect(page.getByTestId('session-switcher').getByRole('button')).toContainText(/Child B/)

    await page.keyboard.press('ArrowRight')
    await expect(page.getByTestId('session-switcher').getByRole('button')).toContainText(/Child C/)
  })

  test('ArrowUp on a child session navigates to the parent', async ({ page }) => {
    // Land on child-b first.
    await page.getByTestId('delegation-strip')
      .locator('[data-testid^="delegation-entry-"]')
      .first()
      .click()
    await expect(page.getByTestId('session-switcher').getByRole('button')).toContainText(/Child B/)

    await page.getByTestId('chat-message-pane').click()
    await page.keyboard.press('ArrowUp')

    await expect(page.getByTestId('session-switcher').getByRole('button')).toContainText(/Parent Session/)
    // And the toolbar reappears once we're back on the parent.
    await expect(page.getByTestId('input-selector-bar')).toBeVisible()
  })
})
