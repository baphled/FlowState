import { test, expect } from '@playwright/test'

test.describe('Chat layout — swarm pane toggle', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/models', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ models: ['claude-3-5-sonnet'] }),
      })
    })
    await page.route('**/api/swarm/events', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ events: [] }),
      })
    })
    await page.goto('/chat')
  })

  test('swarm pane is visible by default', async ({ page }) => {
    await expect(page.getByTestId('swarm-pane')).toBeVisible()
  })

  test('toggle button hides the swarm pane', async ({ page }) => {
    await page.getByTestId('toggle-swarm-btn').click()
    await expect(page.getByTestId('swarm-pane')).not.toBeVisible()
  })

  test('show button re-displays the swarm pane after hiding', async ({ page }) => {
    await page.getByTestId('toggle-swarm-btn').click()
    await expect(page.getByTestId('swarm-pane')).not.toBeVisible()

    await page.getByTestId('show-swarm-btn').click()
    await expect(page.getByTestId('swarm-pane')).toBeVisible()
  })

  test('nav bar is always visible', async ({ page }) => {
    await expect(page.getByTestId('nav-bar')).toBeVisible()
    await page.getByTestId('toggle-swarm-btn').click()
    await expect(page.getByTestId('nav-bar')).toBeVisible()
  })
})
