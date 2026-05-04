import { test, expect } from '@playwright/test'

// ...existing tests...

test.describe('Chat view', () => {
  let agents = [
    { id: 'planner', name: 'Planner', description: 'Plans work', model: 'claude-sonnet-4-6' },
    { id: 'executor', name: 'Executor', description: 'Runs work', model: 'llama3.2' },
  ]

  let sessions = [
    {
      id: 'session-12345678',
      title: 'Planning Sync',
      agentId: 'planner',
      messageCount: 3,
      createdAt: '2026-04-30T09:00:00Z',
      updatedAt: '2026-05-01T09:00:00Z',
    },
    {
      id: 'session-87654321',
      title: 'Sprint Retro',
      agentId: 'executor',
      messageCount: 3,
      createdAt: '2026-05-01T09:00:00Z',
      updatedAt: '2026-05-01T10:00:00Z',
    },
  ]

  let messages = [
    { role: 'user', content: 'Hello!' },
    { role: 'assistant', content: 'Hello from the mock assistant!' },
  ]

  const messagesBySession: Record<string, typeof messages> = {
    'session-12345678': messages,
    'session-87654321': messages,
  }

  const getSessionId = (url: string) => url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? ''

  test.beforeEach(async ({ page }) => {
    await page.route('**/api/agents', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(agents),
      })
    })

    await page.route('**/api/v1/sessions', async (route) => {
      if (route.request().method() === 'POST') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ id: 'session-98765432' }),
        })
        return
      }

      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(sessions),
      })
    })

    await page.route('**/api/v1/sessions/**/messages', async (route) => {
      const sessionId = getSessionId(route.request().url())

      if (route.request().method() === 'POST') {
        const body = route.request().postDataJSON() as { content?: string }
        const currentMessages = messagesBySession[sessionId] ?? []

        messagesBySession[sessionId] = [
          ...currentMessages,
          { role: 'user', content: body.content ?? '' },
          { role: 'assistant', content: 'Hello from the mock assistant!' },
        ]

        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            id: sessionId,
            agentId: sessionId === 'session-87654321' ? 'executor' : 'planner',
            messages: messagesBySession[sessionId],
            createdAt: '2026-05-01T00:00:00Z',
            updatedAt: '2026-05-01T00:00:00Z',
          }),
        })
        return
      }

      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(messagesBySession[sessionId] ?? messages),
      })
    })

    await page.route('**/api/swarm/events', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
    })

    await page.goto('/chat')
  })

  test('renders the chat view', async ({ page }) => {
    await expect(page.getByTestId('nav-bar')).toBeVisible()
    await expect(page.getByTestId('agent-switcher')).toBeVisible()
    await expect(page.getByTestId('session-switcher')).toBeVisible()
    await expect(page.getByTestId('current-agent-summary')).toContainText('claude-sonnet-4-6')
    await expect(page.getByTestId('current-agent-summary')).not.toContainText('models')
    await expect(page.getByTestId('message-input')).toBeVisible()
    await expect(page.getByTestId('send-button')).toBeVisible()
    await expect(page.getByTestId('model-select')).toHaveCount(0)
  })

  test('opens and uses the nav bar switchers', async ({ page }) => {
    const agentSwitcher = page.getByTestId('agent-switcher')
    await agentSwitcher.getByRole('button').click()
    await expect(agentSwitcher.getByRole('listbox')).toBeVisible()
    await agentSwitcher.getByRole('option', { name: /Executor/i }).click()
    await expect(agentSwitcher.getByRole('button')).toContainText('Executor')
    await expect(agentSwitcher).toContainText('llama3.2')

    const sessionSwitcher = page.getByTestId('session-switcher')
    await sessionSwitcher.getByRole('button').click()
    await expect(sessionSwitcher.getByRole('listbox')).toBeVisible()
    await sessionSwitcher.getByRole('option', { name: /Sprint Retro/i }).click()
    await expect(sessionSwitcher.getByRole('button')).toContainText(/Sprint Retro/)
  })

  test('sends a message and receives a response', async ({ page }) => {
    const input = page.getByTestId('message-input')
    await input.fill('Hello!')
    await page.getByTestId('send-button').click()

    const messages = page.getByTestId('message-list')
    await expect(messages).toContainText('Hello!')
    await expect(messages).toContainText('Hello from the mock assistant!')
  })

  test('opens the slash command picker when "/" is typed and inserts the chosen command', async ({ page }) => {
    const input = page.getByTestId('message-input')
    await input.click()
    await input.press('/')

    // The picker reuses the FuzzySearchModal scaffolding so its backdrop
    // shares the same testid as the toolbar AgentPicker / ModelPicker
    // — only one is ever open at a time, so this is unambiguous.
    await expect(page.getByTestId('fuzzy-search-backdrop')).toBeVisible()
    await expect(page.getByTestId('fuzzy-search-item-clear')).toBeVisible()
    await expect(page.getByTestId('fuzzy-search-item-help')).toBeVisible()

    await page.getByTestId('fuzzy-search-item-clear').click()
    await expect(page.getByTestId('fuzzy-search-backdrop')).toHaveCount(0)
    await expect(input).toHaveValue('/clear ')
  })

  test('opens the agent mention picker when "@" is typed mid-message', async ({ page }) => {
    const input = page.getByTestId('message-input')
    await input.click()
    await input.fill('hey ')
    await input.press('@')

    await expect(page.getByTestId('fuzzy-search-backdrop')).toBeVisible()
    await expect(page.getByTestId('fuzzy-search-item-planner')).toBeVisible()

    await page.getByTestId('fuzzy-search-item-planner').click()
    await expect(page.getByTestId('fuzzy-search-backdrop')).toHaveCount(0)
    await expect(input).toHaveValue('hey @planner ')
  })

  test('restores last-used session and agent after localStorage compaction', async ({ page }) => {
    const agentSwitcher = page.getByTestId('agent-switcher')
    await agentSwitcher.getByRole('button').click()
    await agentSwitcher.getByRole('option', { name: /Executor/i }).click()
    await expect(agentSwitcher.getByRole('button')).toContainText('Executor')

    const sessionSwitcher = page.getByTestId('session-switcher')
    await sessionSwitcher.getByRole('button').click()
    await sessionSwitcher.getByRole('option', { name: /Sprint Retro/i }).click()
    await expect(sessionSwitcher.getByRole('button')).toContainText(/Sprint Retro/)

    await page.evaluate(() => localStorage.clear())

    agents = [
      { id: 'executor', name: 'Executor', description: 'Runs work', model: 'llama3.2' },
      { id: 'planner', name: 'Planner', description: 'Plans work', model: 'claude-sonnet-4-6' },
    ]

    sessions = [
      {
        id: 'session-87654321',
        title: 'Sprint Retro',
        agentId: 'executor',
        messageCount: 3,
        createdAt: '2026-05-01T09:00:00Z',
        updatedAt: '2026-05-01T10:00:00Z',
      },
      {
        id: 'session-12345678',
        title: 'Planning Sync',
        agentId: 'planner',
        messageCount: 3,
        createdAt: '2026-04-30T09:00:00Z',
        updatedAt: '2026-05-01T09:00:00Z',
      },
    ]

    await page.reload()

    await expect(page.getByTestId('agent-switcher').getByRole('button')).toContainText('Executor')
    await expect(page.getByTestId('session-switcher').getByRole('button')).toContainText(/Sprint Retro/)
  })

})
