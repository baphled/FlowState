import { test, expect } from '@playwright/test'

const mockEvents = [
  {
    id: 'evt-1',
    type: 'delegation',
    timestamp: new Date().toISOString(),
    summary: 'Delegated to hephaestus',
    metadata: {},
  },
  {
    id: 'evt-2',
    type: 'tool_call',
    timestamp: new Date().toISOString(),
    summary: 'Called bash tool',
    metadata: {},
  },
]

test.describe('Swarm view', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/swarm/events', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ events: mockEvents }),
      })
    })

    await page.goto('/swarm')
  })

  test('renders the swarm view', async ({ page }) => {
    await expect(page.getByTestId('swarm-view')).toBeVisible()
  })

  test('loads and displays swarm events', async ({ page }) => {
    const list = page.getByTestId('swarm-event-list')
    await expect(list).toBeVisible()
    await expect(page.getByTestId('event-card-evt-1')).toBeVisible()
    await expect(page.getByTestId('event-card-evt-2')).toBeVisible()
  })

  test('shows correct event types', async ({ page }) => {
    const card1 = page.getByTestId('event-card-evt-1')
    await expect(card1).toHaveAttribute('data-event-type', 'delegation')

    const card2 = page.getByTestId('event-card-evt-2')
    await expect(card2).toHaveAttribute('data-event-type', 'tool_call')
  })

  test('shows event count', async ({ page }) => {
    await expect(page.getByTestId('event-count')).toContainText('2 events')
  })

  test('shows empty state when no events', async ({ page }) => {
    await page.route('**/api/swarm/events', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ events: [] }),
      })
    })
    await page.reload()
    await expect(page.getByTestId('swarm-empty')).toBeVisible()
  })
})
