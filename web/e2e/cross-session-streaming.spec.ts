import { test, expect, Page } from '@playwright/test'

// PR-2 cross-session streaming e2e — compounding bug C-3.
//
// Symptom: send a message in session A; while the stream is in flight,
// switch to session B from the session switcher. Pre-fix the still-open
// SSE connection delivered chunks to the chatStore which applied them to
// whatever session was currently active — so session A's chunks appeared
// (and corrupted) session B's thread. The user had to refresh both
// sessions to recover.
//
// Post-fix the SSE message listener captures sessionId in its closure and
// short-circuits when currentSessionId no longer matches. Switching during
// a stream cleanly disconnects (loadSessionMessages calls disconnect)
// AND any late events that escape that disconnect are now dropped by
// either the per-session capture guard or the post-disconnect flush guard
// (added in the C-9 fix).

interface SSEChannel {
  fire: (type: string, data: unknown) => void
  url: string
}

async function installFakeSSE(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const w = window as unknown as { [k: string]: unknown }
    class FakeEventSource {
      listeners: Record<string, Array<(event: MessageEvent) => void>> = {}
      url: string
      readyState = 1
      constructor(url: string) {
        this.url = url
        const list = (w.__sseInstances as FakeEventSource[] | undefined) ?? []
        list.push(this)
        w.__sseInstances = list
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
    }
  })
}

test.describe('Cross-session streaming — chunks for session A must not land on session B', () => {
  test('switching sessions mid-stream does not contaminate the new session with the old session\'s chunks', async ({ page }) => {
    await installFakeSSE(page)

    // Two sessions, both with prior canonical history. Session B starts
    // with a clear text fingerprint we can later assert "is unchanged".
    // Session A's history ends with a completed assistant so no reattach
    // fires on page load — the input is enabled and the test can drive a
    // fresh send.
    const messagesBySession: Record<string, Array<{ id: string; role: string; content: string; timestamp: string; status?: string }>> = {
      'session-A': [
        { id: 'a-u0', role: 'user', content: 'A prior prompt', timestamp: '2026-05-04T00:00:00Z' },
        { id: 'a-a0', role: 'assistant', content: 'A prior reply (completed)', timestamp: '2026-05-04T00:00:01Z' },
      ],
      'session-B': [
        { id: 'b-u1', role: 'user', content: 'B prompt', timestamp: '2026-05-04T00:01:00Z' },
        { id: 'b-a1', role: 'assistant', content: 'B canonical reply (must not be corrupted)', timestamp: '2026-05-04T00:01:01Z' },
      ],
    }

    await page.route('**/api/health', async (route) => {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ status: 'ok' }) })
    })
    await page.route('**/api/agents', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          { id: 'agent-1', name: 'Agent One', description: 'x', model: 'claude-sonnet-4-6' },
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
    await page.route('**/api/v1/sessions', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          {
            id: 'session-A',
            agentId: 'agent-1',
            currentAgentId: 'agent-1',
            title: 'Session A',
            createdAt: '2026-05-04T00:00:00Z',
            updatedAt: '2026-05-04T00:00:00Z',
            messageCount: messagesBySession['session-A'].length,
          },
          {
            id: 'session-B',
            agentId: 'agent-1',
            currentAgentId: 'agent-1',
            title: 'Session B',
            createdAt: '2026-05-04T00:01:00Z',
            updatedAt: '2026-05-04T00:01:01Z',
            messageCount: messagesBySession['session-B'].length,
          },
        ]),
      })
    })
    await page.route('**/api/v1/sessions/*/messages', async (route) => {
      const url = route.request().url()
      const sessionId = url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? 'session-A'
      if (route.request().method() === 'POST') {
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
    await page.route('**/api/swarm/events', async (route) => {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) })
    })

    // Start on session A (persisted).
    await page.addInitScript(() => {
      window.localStorage.setItem('chat.currentSessionId', 'session-A')
    })

    await page.goto('/chat')
    await expect(page.getByTestId('message-input')).toBeVisible()

    // Send a prompt on session A — this opens an SSE connection.
    await page.getByTestId('message-input').fill('streaming on A')
    await page.getByTestId('send-button').click()

    await page.waitForFunction(
      () => {
        const w = window as unknown as { __sseDriver?: { instances: () => unknown[] } }
        return (w.__sseDriver?.instances().length ?? 0) >= 1
      },
      undefined,
      { timeout: 5000 },
    )

    // Deliver a few chunks on session A's SSE — these should land on A.
    await page.evaluate(() => {
      const w = window as unknown as { __sseDriver: { instances: () => Array<SSEChannel> } }
      const es = w.__sseDriver.instances()[0]
      es.fire('message', JSON.stringify({ content: 'A first ' }))
      es.fire('message', JSON.stringify({ content: 'chunk' }))
    })

    // Switch to session B via the SessionSwitcher dropdown — drives the
    // same code path the user takes (loadSessionMessages → disconnect SSE
    // → fetch messages). Click the trigger then the matching session option.
    await page.getByTestId('session-switcher').getByRole('button').first().click()
    // The session option text contains the session title we set in the mock
    // ("Session B"). Click it.
    await page.getByText('Session B', { exact: false }).click()

    // We are now on session B. Verify B's canonical bubble is visible.
    const bAssistant = page.getByTestId('message-assistant').first()
    await expect(bAssistant).toContainText('B canonical reply (must not be corrupted)')

    // The session A SSE connection may still be alive in test land — the
    // browser doesn't close it synchronously. Fire one more chunk on the
    // OLD EventSource. Pre-fix this would be applied to whatever session
    // was now active (B), corrupting B's thread. Post-fix the listener
    // checks currentSessionId === capturedSessionId AND the
    // post-disconnect flush guard drops it.
    await page.evaluate(() => {
      const w = window as unknown as { __sseDriver: { instances: () => Array<SSEChannel> } }
      const es = w.__sseDriver.instances()[0]
      es.fire('message', JSON.stringify({ content: 'STRAY A CHUNK' }))
    })

    // Give the page a moment to (potentially) react.
    await page.waitForTimeout(200)

    // Session B's canonical content is unchanged — no STRAY A CHUNK in any
    // bubble on this page.
    const allAssistantText = await page.getByTestId('message-assistant').allTextContents()
    for (const text of allAssistantText) {
      expect(text).not.toContain('STRAY A CHUNK')
    }
    // And B's canonical bubble is still intact.
    await expect(bAssistant).toContainText('B canonical reply (must not be corrupted)')
  })
})
