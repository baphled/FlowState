import { test, expect, Page } from '@playwright/test'

// Chat Attachments Backend (May 2026) — PR1 round-trip.
//
// Asserts the user-facing affordance closes UI Parity B3:
//   1. Pick / drag a file → staged in composer (preview chip appears).
//   2. Send with text → POST /api/v1/sessions/{id}/attachments fires
//      first, the returned ids thread onto the subsequent POST /messages
//      body as `attachmentIds`.
//   3. Failure path: 4xx from the attachments endpoint → error toast +
//      staged file stays in place (NOT silently dropped).
//
// Pre-fix (before commits d10212e4..4ff46126) this whole flow was a
// `console.debug` no-op — the staged file was lost on send and the
// text message went without it. Plan §6 task-05 + task-03.

interface MessagesPostBody {
  content?: string
  attachmentIds?: string[]
}

interface MockState {
  /** Bodies seen by POST /messages — last entry is most recent. */
  messagesPosts: MessagesPostBody[]
  /** Bodies seen by POST /attachments — length is the multipart request count. */
  attachmentsPosts: number
  /** Force the next POST /attachments call to fail with this status. */
  failNextUpload: { status: number; body: string } | null
}

async function setupMocks(page: Page, state: MockState): Promise<void> {
  await page.addInitScript(() => {
    // SSE stub — chat surface boots EventSource on session load. We don't
    // drive chunks in this spec; the backend round trip is the focus.
    const w = window as unknown as { [k: string]: unknown }
    class FakeEventSource {
      url: string
      readyState = 1
      constructor(url: string) {
        this.url = url
      }
      addEventListener(): void {}
      removeEventListener(): void {}
      close(): void {
        this.readyState = 2
      }
    }
    w.EventSource = FakeEventSource
  })

  await page.route('**/api/agents', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        { id: 'agent-1', name: 'Test Agent', description: 'attachment fixture', model: 'claude-sonnet-4-6' },
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
        body: JSON.stringify({ id: 'session-att', agentId: 'agent-1' }),
      })
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        {
          id: 'session-att',
          agentId: 'agent-1',
          currentAgentId: 'agent-1',
          title: 'Attachment fixture session',
          createdAt: '2026-05-12T00:00:00Z',
          updatedAt: '2026-05-12T00:00:00Z',
          messageCount: 0,
        },
      ]),
    })
  })

  await page.route('**/api/v1/sessions/*/attachments', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.fulfill({ status: 405, body: 'method not allowed' })
      return
    }
    state.attachmentsPosts += 1
    if (state.failNextUpload) {
      const failure = state.failNextUpload
      state.failNextUpload = null
      await route.fulfill({ status: failure.status, contentType: 'text/plain', body: failure.body })
      return
    }
    // Return one deterministic attachment per uploaded file. The frontend
    // doesn't read sizeBytes / mediaType in the send-path so a single
    // placeholder shape per file is enough for the round-trip pin.
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        attachments: [
          { id: 'att-server-1', mediaType: 'image/png', sizeBytes: 7, originalFilename: 'sample.png' },
        ],
      }),
    })
  })

  await page.route('**/api/v1/sessions/*/messages', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          id: 'session-att',
          agentId: 'agent-1',
          messages: [],
          messageCount: 0,
        }),
      })
      return
    }
    const body = (route.request().postDataJSON() ?? {}) as MessagesPostBody
    state.messagesPosts.push(body)
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        id: 'session-att',
        agentId: 'agent-1',
        messages: [
          { id: 'srv-u1', role: 'user', content: body.content ?? '', timestamp: '2026-05-12T00:00:01Z' },
        ],
        messageCount: 1,
        createdAt: '2026-05-12T00:00:00Z',
        updatedAt: '2026-05-12T00:00:02Z',
      }),
    })
  })
}

test.describe('Chat Attachments Backend (May 2026) — PR1 round-trip', () => {
  test('file picker → staged → send threads attachmentIds onto POST /messages', async ({ page }) => {
    const state: MockState = { messagesPosts: [], attachmentsPosts: 0, failNextUpload: null }
    await setupMocks(page, state)
    await page.goto('/chat')

    const composer = page.getByTestId('message-input')
    await expect(composer).toBeVisible()

    // Stage one image via the hidden file input. The composer's drag-drop
    // and picker affordances both end up routing to the same staging
    // path; this exercises the picker side.
    const filePicker = page.locator('input[type="file"]').first()
    await filePicker.setInputFiles({
      name: 'sample.png',
      mimeType: 'image/png',
      buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a]),
    })

    // The staged-attachments row surfaces with a preview chip.
    await expect(page.getByTestId('message-input-attachments')).toBeVisible()

    // Type and send.
    await composer.fill('look at this')
    await composer.press('Enter')

    // Wait for both endpoints to fire.
    await expect.poll(() => state.attachmentsPosts, { timeout: 5000 }).toBe(1)
    await expect.poll(() => state.messagesPosts.length, { timeout: 5000 }).toBe(1)

    const sent = state.messagesPosts[0]
    expect(sent.content).toBe('look at this')
    expect(sent.attachmentIds).toEqual(['att-server-1'])

    // Staging row clears on successful send.
    await expect(page.getByTestId('message-input-attachments')).toBeHidden()
  })

  test('upload failure surfaces a toast and preserves staged attachments', async ({ page }) => {
    const state: MockState = {
      messagesPosts: [],
      attachmentsPosts: 0,
      failNextUpload: { status: 413, body: 'attachment exceeds 5MB cap' },
    }
    await setupMocks(page, state)
    await page.goto('/chat')

    const composer = page.getByTestId('message-input')
    await expect(composer).toBeVisible()

    const filePicker = page.locator('input[type="file"]').first()
    await filePicker.setInputFiles({
      name: 'too-big.png',
      mimeType: 'image/png',
      buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47]),
    })

    await expect(page.getByTestId('message-input-attachments')).toBeVisible()

    await composer.fill('does this go through?')
    await composer.press('Enter')

    // The upload attempt fires once; the message POST MUST NOT.
    await expect.poll(() => state.attachmentsPosts, { timeout: 5000 }).toBe(1)
    // Give the message POST a chance to (incorrectly) fire — it must not.
    await page.waitForTimeout(250)
    expect(state.messagesPosts).toEqual([])

    // Error toast visible.
    await expect(page.getByText(/attachment upload failed/i)).toBeVisible()

    // Staged file stays so the user can retry without re-staging — the
    // load-bearing UX promise from plan §6 task-05.
    await expect(page.getByTestId('message-input-attachments')).toBeVisible()
  })
})
