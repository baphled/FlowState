import { test, expect, Page } from '@playwright/test'

// Live verification of the todowrite content-gap bug:
//   "we are seeing a todo list completing, but we don't see any responses
//    between the update."
//
// The agent emits assistant text, fires `todowrite`, emits more text, fires
// `todowrite` again, and finishes with a closing message. Pre-fix the
// in-flight assistant bubble is never sealed when a tool fires, so
// handleContentChunk reverse-finds the pre-tool assistant (still
// status==='running') and APPENDS post-tool content onto it. The merged
// bubble sits at its original array position — BEFORE every tool_result —
// so the inter-tool / post-tool replies appear to vanish from the chat.
//
// We replace the page's EventSource with a controllable fake so the test
// drives the realistic SSE event sequence (content / tool_call / tool_result)
// in the exact order a backend-driven todowrite turn produces. The REST
// endpoints are mocked via page.route() following the same pattern as
// chat-multi-turn-streaming.spec.ts.
//
// Post-fix: every assistant role chunk between tool boundaries renders as a
// distinct visible bubble in chronological order around the todo updates.

interface PostGate {
  release: () => void
  released: Promise<void>
}

function newGate(): PostGate {
  let release: () => void = () => {}
  const released = new Promise<void>((resolve) => {
    release = resolve
  })
  return { release, released }
}

const PRE_TOOL_TEXT = 'Let me plan this out before starting.'
const INTER_TOOL_TEXT = 'Now starting on step one.'
const POST_TOOL_TEXT = 'All done — task complete.'

async function setupMocks(page: Page, gate: PostGate): Promise<void> {
  const messagesBySession: Record<
    string,
    Array<{ id: string; role: string; content: string; timestamp: string; toolName?: string }>
  > = { 'session-1': [] }

  await page.addInitScript(() => {
    const w = window as unknown as { [k: string]: unknown }
    class FakeEventSource {
      listeners: Record<string, Array<(event: MessageEvent) => void>> = {}
      url: string
      readyState = 1
      constructor(url: string) {
        this.url = url
        ;(w.__sseInstances as FakeEventSource[] | undefined)?.push(this) ??
          (w.__sseInstances = [this])
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
    if (route.request().method() === 'POST') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ id: 'session-1', agentId: 'agent-1' }),
      })
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        {
          id: 'session-1',
          agentId: 'agent-1',
          currentAgentId: 'agent-1',
          title: 'Test session',
          createdAt: '2026-05-04T00:00:00Z',
          updatedAt: '2026-05-04T00:00:00Z',
          messageCount: messagesBySession['session-1']?.length ?? 0,
        },
      ]),
    })
  })

  await page.route('**/api/v1/sessions/*/messages', async (route) => {
    const url = route.request().url()
    const sessionId = url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? 'session-1'

    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON() as { content?: string }
      // Hold the POST until the test releases the gate, after delivering
      // the SSE chunks for this turn. Mirrors chat-multi-turn-streaming.
      await gate.released

      // Backend canonical history after this turn: user prompt followed by
      // three assistant chunks reconciled into separate rows (matches what
      // the engine actually persists when the session-accumulator flushes
      // after each tool_call boundary).
      messagesBySession[sessionId] = [
        ...(messagesBySession[sessionId] ?? []),
        {
          id: 'srv-u1',
          role: 'user',
          content: body.content ?? '',
          timestamp: new Date().toISOString(),
        },
        {
          id: 'srv-a1',
          role: 'assistant',
          content: PRE_TOOL_TEXT,
          timestamp: new Date().toISOString(),
        },
        {
          id: 'srv-t1',
          role: 'tool_result',
          content: '[{"id":"1","content":"step one","status":"pending"}]',
          toolName: 'todowrite',
          timestamp: new Date().toISOString(),
        },
        {
          id: 'srv-a2',
          role: 'assistant',
          content: INTER_TOOL_TEXT,
          timestamp: new Date().toISOString(),
        },
        {
          id: 'srv-t2',
          role: 'tool_result',
          content: '[{"id":"1","content":"step one","status":"in_progress"}]',
          toolName: 'todowrite',
          timestamp: new Date().toISOString(),
        },
        {
          id: 'srv-a3',
          role: 'assistant',
          content: POST_TOOL_TEXT,
          timestamp: new Date().toISOString(),
        },
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

  await page.route('**/api/swarm/events', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([]),
    })
  })
}

test.describe('Todowrite content gap — assistant text between/after tool calls must render', () => {
  test('every assistant chunk around todowrite tool calls renders as a distinct visible bubble', async ({
    page,
  }) => {
    const gate = newGate()
    await setupMocks(page, gate)

    await page.goto('/chat')
    await expect(page.getByTestId('message-input')).toBeVisible()

    const input = page.getByTestId('message-input')
    await input.fill('do this big task with todos')
    await page.getByTestId('send-button').click()

    // Wait for the EventSource to be created.
    await page.waitForFunction(() => {
      return (
        (window as unknown as { __sseDriver?: { instances: () => unknown[] } }).__sseDriver?.instances()
          .length === 1
      )
    })

    // Drive the realistic SSE sequence the engine emits when an agent uses
    // todowrite mid-stream:
    //   pre-tool text → tool_call → tool_result → inter-tool text →
    //   tool_call → tool_result → post-tool text
    //
    // Each `content` event SHOULD land on a distinct assistant bubble, with
    // the tool_result rows interleaved in chronological order. Pre-fix the
    // three content events merge into ONE bubble at the array position of
    // the first chunk, so the inter-tool and post-tool text appear to
    // "vanish" from the user-visible thread.
    await page.evaluate(
      ({ pre, inter, post }) => {
        const driver = (
          window as unknown as {
            __sseDriver: { instances: () => Array<{ fire: (t: string, d: unknown) => void }> }
          }
        ).__sseDriver
        const es = driver.instances()[0]
        es.fire('message', JSON.stringify({ content: pre }))
        es.fire(
          'message',
          JSON.stringify({ type: 'tool_call', name: 'todowrite', status: 'running' }),
        )
        es.fire(
          'message',
          JSON.stringify({
            type: 'tool_result',
            content: '[{"id":"1","content":"step one","status":"pending"}]',
          }),
        )
        es.fire('message', JSON.stringify({ content: inter }))
        es.fire(
          'message',
          JSON.stringify({ type: 'tool_call', name: 'todowrite', status: 'running' }),
        )
        es.fire(
          'message',
          JSON.stringify({
            type: 'tool_result',
            content: '[{"id":"1","content":"step one","status":"in_progress"}]',
          }),
        )
        es.fire('message', JSON.stringify({ content: post }))
      },
      { pre: PRE_TOOL_TEXT, inter: INTER_TOOL_TEXT, post: POST_TOOL_TEXT },
    )

    // Mid-stream snapshot — BEFORE [DONE] and BEFORE refetch.
    // Assertion: every assistant content chunk has produced a SEPARATE
    // visible bubble. Pre-fix this is 1 (all three chunks merged); post-fix
    // it is 3.
    await expect(
      page.getByTestId('message-assistant'),
      'one assistant bubble per chunk between/after tool calls',
    ).toHaveCount(3)
    await expect(page.getByTestId('message-assistant').nth(0)).toContainText(PRE_TOOL_TEXT)
    await expect(page.getByTestId('message-assistant').nth(1)).toContainText(INTER_TOOL_TEXT)
    await expect(page.getByTestId('message-assistant').nth(2)).toContainText(POST_TOOL_TEXT)

    // Two todowrite tool_result rows render between/after the assistant
    // bubbles. The chat thread DOM order must interleave them
    // chronologically — pre-fix the assistant bubble was always at index 0
    // with both tool rows trailing it.
    const bubbles = page.locator('[data-testid="message-assistant"], [data-testid="message-tool_result"]')
    await expect(bubbles).toHaveCount(5)
    const order = await bubbles.evaluateAll((els) =>
      els.map((el) => el.getAttribute('data-testid')),
    )
    expect(order).toEqual([
      'message-assistant',
      'message-tool_result',
      'message-assistant',
      'message-tool_result',
      'message-assistant',
    ])

    // Settle the turn so the post-stream reconcile fires and the canonical
    // backend history (which carries the same six rows) replaces the local
    // optimistic state. The DOM count must still match — three assistants,
    // two tool_results — so the post-fix behaviour holds end-to-end.
    gate.release()
    await page.waitForResponse(
      (res) => res.request().method() === 'POST' && /\/messages$/.test(res.url()),
    )
    await page.evaluate(() => {
      const driver = (
        window as unknown as {
          __sseDriver: { instances: () => Array<{ fire: (t: string, d: unknown) => void }> }
        }
      ).__sseDriver
      const es = driver.instances()[0]
      es.fire('message', '[DONE]')
    })

    // After settle: still three distinct assistant bubbles in the same
    // order, and the inter-tool / post-tool text remains visible.
    await expect(page.getByTestId('message-assistant')).toHaveCount(3)
    await expect(page.getByTestId('message-assistant').nth(0)).toContainText(PRE_TOOL_TEXT)
    await expect(page.getByTestId('message-assistant').nth(1)).toContainText(INTER_TOOL_TEXT)
    await expect(page.getByTestId('message-assistant').nth(2)).toContainText(POST_TOOL_TEXT)
  })
})
