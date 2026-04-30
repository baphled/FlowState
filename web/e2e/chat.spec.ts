import { test, expect } from '@playwright/test'

test.describe('Chat view', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/models', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(['claude-3-5-sonnet', 'gpt-4o']),
      })
    })

    await page.route('**/api/chat', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ content: 'Hello from the mock assistant!' }),
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
    await expect(page.getByTestId('message-input')).toBeVisible()
    await expect(page.getByTestId('send-button')).toBeVisible()
  })

  test('sends a message and receives a response', async ({ page }) => {
    const input = page.getByTestId('message-input')
    await input.fill('Hello!')
    await page.getByTestId('send-button').click()

    const messages = page.getByTestId('message-list')
    await expect(messages).toContainText('Hello!')
    await expect(messages).toContainText('Hello from the mock assistant!')
  })

  test('shows a loading indicator while waiting', async ({ page }) => {
    await page.route('**/api/chat', async (route) => {
      await new Promise((r) => setTimeout(r, 200))
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ content: 'Slow response' }),
      })
    })

    await page.getByTestId('message-input').fill('Slow?')
    await page.getByTestId('send-button').click()

    await expect(page.getByTestId('loading-indicator')).toBeVisible()
    await expect(page.getByTestId('message-list')).toContainText('Slow response')
  })

  test('model selector is populated', async ({ page }) => {
    const select = page.getByTestId('model-select')
    await expect(select).toBeVisible()
    await expect(select.locator('option')).toHaveCount(2)
  })
})
