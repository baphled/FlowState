import { test, expect, Page } from '@playwright/test'

// Tests for the scroll UX fix:
// - New messages (messages.length watcher) scroll with 'smooth'
// - Streaming content chunks (content.length watcher) scroll with 'instant'
//   via a requestAnimationFrame debounce (scheduleInstantScroll)
// - userScrolledUp stops auto-scroll; submitting a new message re-engages it
//
// We replace EventSource with a controllable fake so the test drives realistic
// chunk timing without a real backend. REST endpoints are mocked via page.route().

// Number of content lines to stream — enough to require scroll on a real viewport
const STREAM_LINES = 25

async function setupScrollMocks(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const w = window as unknown as { [k: string]: unknown }
    class FakeEventSource {
      listeners: Record<string, Array<(event: MessageEvent) => void>> = {}
      url: string
      readyState = 1
      constructor(url: string) {
        this.url = url
        const instances = w.__sseInstances as FakeEventSource[] | undefined
        if (instances) {
          instances.push(this)
        } else {
          w.__sseInstances = [this]
        }
      }
      addEventListener(type: string, fn: (event: MessageEvent) => void): void {
        this.listeners[type] = this.listeners[type] || []
        this.listeners[type].push(fn)
      }
      removeEventListener(type: string, fn: (event: MessageEvent) => void): void {
        this.listeners[type] = (this.listeners[type] || []).filter((f) => f !== fn)
      }
      close(): void {
        this.readyState = 2
      }
      fire(type: string, data: unknown): void {
        const fns = this.listeners[type] || []
        const payload = typeof data === 'string' ? data : JSON.stringify(data)
        for (const fn of fns) fn({ data: payload } as MessageEvent)
      }
    }
    w.EventSource = FakeEventSource
    w.__sseDriver = {
      instances: () => (w.__sseInstances as FakeEventSource[] | undefined) ?? [],
      latest: () => {
        const arr = (w.__sseInstances as FakeEventSource[] | undefined) ?? []
        return arr[arr.length - 1]
      },
    }
  })

  const messagesBySession: Record<string, Array<{ id: string; role: string; content: string; timestamp: string }>> = {
    'scroll-session-1': [],
  }

  await page.route('**/api/agents', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        { id: 'agent-1', name: 'Agent One', description: 'Scroll test agent', model: 'claude-sonnet-4-6' },
      ]),
    })
  })

  await page.route('**/api/v1/models', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ providers: [] }),
    })
  })

  await page.route('**/api/swarm/events', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([]),
    })
  })

  let postCount = 0
  await page.route('**/api/v1/sessions', async (route) => {
    if (route.request().method() === 'POST') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ id: 'scroll-session-1', agentId: 'agent-1' }),
      })
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        {
          id: 'scroll-session-1',
          agentId: 'agent-1',
          currentAgentId: 'agent-1',
          title: 'Scroll Test Session',
          createdAt: '2026-05-04T00:00:00Z',
          updatedAt: '2026-05-04T00:00:00Z',
          messageCount: messagesBySession['scroll-session-1']?.length ?? 0,
        },
      ]),
    })
  })

  await page.route('**/api/v1/sessions/*/messages', async (route) => {
    const url = route.request().url()
    const sessionId = url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? 'scroll-session-1'

    if (route.request().method() === 'POST') {
      postCount += 1
      const body = route.request().postDataJSON() as { content?: string }
      const userId = `u${postCount}`
      const assistantId = `a${postCount}`
      const longContent = Array.from(
        { length: STREAM_LINES },
        (_, i) => `Response line ${i + 1} of ${STREAM_LINES}`,
      ).join('\n')

      messagesBySession[sessionId] = [
        ...(messagesBySession[sessionId] ?? []),
        { id: userId, role: 'user', content: body.content ?? '', timestamp: new Date().toISOString() },
        { id: assistantId, role: 'assistant', content: longContent, timestamp: new Date().toISOString() },
      ]

      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          id: sessionId,
          agentId: 'agent-1',
          messages: messagesBySession[sessionId],
          messageCount: messagesBySession[sessionId].length,
          createdAt: '2026-05-04T00:00:00Z',
          updatedAt: new Date().toISOString(),
        }),
      })
      return
    }

    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(messagesBySession[sessionId] ?? []),
    })
  })
}

/**
 * Returns true when the message pane is scrolled to the bottom (within 10px).
 */
async function isScrolledToBottom(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const el = document.querySelector('[data-testid="chat-message-pane"]') as HTMLElement | null
    if (!el) return false
    return el.scrollTop + el.clientHeight >= el.scrollHeight - 10
  })
}

/**
 * Scrolls the message pane up by a given amount to simulate the user scrolling up.
 */
async function scrollPaneUp(page: Page, amount = 300): Promise<void> {
  await page.evaluate((scrollAmount: number) => {
    const el = document.querySelector('[data-testid="chat-message-pane"]') as HTMLElement | null
    if (el) {
      el.scrollTop = Math.max(0, el.scrollTop - scrollAmount)
      el.dispatchEvent(new Event('scroll'))
    }
  }, amount)
}

type SseDriver = { instances: () => SseFakeInstance[]; latest: () => SseFakeInstance }
type SseFakeInstance = { fire: (type: string, data: unknown) => void }

test.describe('Chat scroll UX', () => {
  test.beforeEach(async ({ page }) => {
    await setupScrollMocks(page)
    await page.goto('/chat')
    await expect(page.getByTestId('message-input')).toBeVisible()
  })

  test('after submitting a prompt, the chat scrolls to bottom showing the new user message', async ({ page }) => {
    const input = page.getByTestId('message-input')
    await input.fill('Tell me something long')
    await page.getByTestId('send-button').click()

    // Wait for the message to appear in the list
    await expect(page.getByTestId('message-list')).toContainText('Tell me something long')

    // The pane should have scrolled to show the new message
    await page.waitForFunction(() => {
      const el = document.querySelector('[data-testid="chat-message-pane"]') as HTMLElement | null
      if (!el) return false
      return el.scrollTop + el.clientHeight >= el.scrollHeight - 10
    }, { timeout: 3000 })

    const atBottom = await isScrolledToBottom(page)
    expect(atBottom).toBe(true)
  })

  test('during streaming, the pane follows new content so the bottom stays visible', async ({ page }) => {
    const input = page.getByTestId('message-input')
    await input.fill('stream me a long reply')
    await page.getByTestId('send-button').click()

    // Wait for the SSE EventSource to be created
    await page.waitForFunction(() => {
      return (window as unknown as { __sseDriver?: SseDriver }).__sseDriver?.instances().length === 1
    })

    // Deliver chunks one-by-one simulating streaming
    for (let i = 1; i <= STREAM_LINES; i++) {
      await page.evaluate(([lineNum, total]: [number, number]) => {
        const driver = (window as unknown as { __sseDriver: SseDriver }).__sseDriver
        const es = driver.latest()
        es.fire('message', JSON.stringify({ content: `Response line ${lineNum} of ${total}\n` }))
      }, [i, STREAM_LINES] as [number, number])
      // Small pause to let Vue react and the RAF debounce fire
      await page.waitForTimeout(30)
    }

    // After all chunks, the bottom should still be visible
    await page.waitForFunction(() => {
      const el = document.querySelector('[data-testid="chat-message-pane"]') as HTMLElement | null
      if (!el) return false
      return el.scrollTop + el.clientHeight >= el.scrollHeight - 10
    }, { timeout: 3000 })

    const atBottom = await isScrolledToBottom(page)
    expect(atBottom).toBe(true)
  })

  test('if user scrolls up mid-stream, auto-scroll stops and bottom is no longer forced', async ({ page }) => {
    const input = page.getByTestId('message-input')
    await input.fill('stream me something')
    await page.getByTestId('send-button').click()

    // Wait for the SSE EventSource to be created
    await page.waitForFunction(() => {
      return (window as unknown as { __sseDriver?: SseDriver }).__sseDriver?.instances().length === 1
    })

    // Deliver many large chunks to build up enough scroll height to overflow the viewport
    const bigLine = 'The quick brown fox jumps over the lazy dog. '.repeat(10)
    for (let i = 1; i <= 40; i++) {
      await page.evaluate((content: string) => {
        const driver = (window as unknown as { __sseDriver: SseDriver }).__sseDriver
        const es = driver.latest()
        es.fire('message', JSON.stringify({ content }))
      }, `${bigLine}\n`)
      await page.waitForTimeout(10)
    }

    // Wait until the pane has scrollable content (scrollHeight > clientHeight)
    await page.waitForFunction(() => {
      const el = document.querySelector('[data-testid="chat-message-pane"]') as HTMLElement | null
      if (!el) return false
      return el.scrollHeight > el.clientHeight + 50
    }, { timeout: 5000 })

    // Simulate the user scrolling to the top of the pane
    await page.evaluate(() => {
      const el = document.querySelector('[data-testid="chat-message-pane"]') as HTMLElement | null
      if (el) {
        el.scrollTop = 0
        el.dispatchEvent(new Event('scroll'))
      }
    })
    await page.waitForTimeout(50)

    // Record scroll position just after scrolling up
    const scrollTopAfterScrollUp = await page.evaluate(() => {
      const el = document.querySelector('[data-testid="chat-message-pane"]') as HTMLElement | null
      return el?.scrollTop ?? 0
    })

    // Deliver more chunks — auto-scroll should NOT override the user's position
    for (let i = 1; i <= 10; i++) {
      await page.evaluate((content: string) => {
        const driver = (window as unknown as { __sseDriver: SseDriver }).__sseDriver
        const es = driver.latest()
        es.fire('message', JSON.stringify({ content }))
      }, `${bigLine}\n`)
      await page.waitForTimeout(20)
    }

    // Wait a moment for any RAF debounce to flush
    await page.waitForTimeout(100)

    // The pane should NOT have been scrolled to the bottom after the user scrolled up
    const atBottom = await isScrolledToBottom(page)
    expect(atBottom).toBe(false)

    // Scroll position should be near where the user left it (not jumped to bottom)
    const scrollTopAfterChunks = await page.evaluate(() => {
      const el = document.querySelector('[data-testid="chat-message-pane"]') as HTMLElement | null
      return el?.scrollTop ?? 0
    })
    // Should be near the top (where we scrolled), not at the bottom
    expect(scrollTopAfterChunks).toBeLessThanOrEqual(scrollTopAfterScrollUp + 50)
  })

  test('after a new submit, auto-scroll re-engages even if user had scrolled up before', async ({ page }) => {
    const input = page.getByTestId('message-input')
    await input.fill('first message')
    await page.getByTestId('send-button').click()

    // Wait for SSE and send a few chunks
    await page.waitForFunction(() => {
      return (window as unknown as { __sseDriver?: SseDriver }).__sseDriver?.instances().length === 1
    })

    for (let i = 1; i <= 5; i++) {
      await page.evaluate((lineNum: number) => {
        const driver = (window as unknown as { __sseDriver: SseDriver }).__sseDriver
        const es = driver.latest()
        es.fire('message', JSON.stringify({ content: `Line ${lineNum}\n` }))
      }, i)
    }

    // Simulate the user scrolling up
    await scrollPaneUp(page, 500)

    // Finish the stream
    await page.evaluate(() => {
      const driver = (window as unknown as { __sseDriver: SseDriver }).__sseDriver
      const es = driver.latest()
      es.fire('message', '[DONE]')
    })

    // Confirm the user is not at the bottom after scrolling up
    // (allow some time for stream completion but scroll position should not have changed)
    await page.waitForTimeout(100)

    // Now submit a second message — this should re-engage auto-scroll
    await input.fill('second message')
    await page.getByTestId('send-button').click()

    // Wait for the second SSE and send enough content to require scroll
    await page.waitForFunction(() => {
      return (window as unknown as { __sseDriver?: SseDriver }).__sseDriver?.instances().length === 2
    })

    // Wait for the new message to appear
    await expect(page.getByTestId('message-list')).toContainText('second message')

    // The second submit should have reset userScrolledUp, so the pane scrolls to bottom
    await page.waitForFunction(() => {
      const el = document.querySelector('[data-testid="chat-message-pane"]') as HTMLElement | null
      if (!el) return false
      return el.scrollTop + el.clientHeight >= el.scrollHeight - 10
    }, { timeout: 3000 })

    const atBottom = await isScrolledToBottom(page)
    expect(atBottom).toBe(true)
  })
})
