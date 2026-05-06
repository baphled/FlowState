import { test, expect, type Page, type Route } from '@playwright/test'

// Behaviour-pinning Playwright suite covering two regressions reported by the
// user against feature/vue-ui-rebase that the prior unit-test pass missed:
//
//  Bug 5 — Shift+Enter must insert "\n" into the chat composer textarea
//          without submitting the message. The existing vitest spec at
//          MessageInput.spec.ts:152-168 only asserted `sendSpy not called`
//          (Pattern 1 from the May 2026 retro: tests pin implementation,
//          not behaviour) so the actual "newline in the textarea" expectation
//          was never enforced.
//
//  Bug 3 — Assistant message bubbles must render markdown as HTML, not
//          display it as raw source. MarkdownRenderer.spec.ts asserts the
//          renderer in isolation but no test drove the live UI end-to-end
//          for headings, lists, code, bold/italic, links, tables, blockquote.
//
// Both scenarios run against the live vite dev server via the standard
// Playwright route mocks established in chat.spec.ts (no real backend).

const agents = [
  { id: 'planner', name: 'Planner', description: 'Plans work', model: 'claude-sonnet-4-6' },
  { id: 'executor', name: 'Executor', description: 'Runs work', model: 'llama3.2' },
]

const baseSession = {
  id: 'session-bug-repro',
  title: 'Bug Repro Session',
  agentId: 'planner',
  messageCount: 0,
  createdAt: '2026-05-06T09:00:00Z',
  updatedAt: '2026-05-06T09:00:00Z',
}

async function installCommonRoutes(page: Page, assistantReply: string): Promise<void> {
  await page.route('**/api/agents', async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(agents),
    })
  })

  await page.route('**/api/v1/sessions', async (route: Route) => {
    if (route.request().method() === 'POST') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ id: baseSession.id }),
      })
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([baseSession]),
    })
  })

  await page.route('**/api/v1/sessions/**/messages', async (route: Route) => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON() as { content?: string }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          id: baseSession.id,
          agentId: 'planner',
          messages: [
            { role: 'user', content: body.content ?? '' },
            { role: 'assistant', content: assistantReply },
          ],
          createdAt: baseSession.createdAt,
          updatedAt: baseSession.updatedAt,
        }),
      })
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        { role: 'assistant', content: assistantReply },
      ]),
    })
  })

  await page.route('**/api/swarm/events', async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([]),
    })
  })
}

test.describe('Bug 5 — Shift+Enter inserts a newline in the composer', () => {
  test.beforeEach(async ({ page }) => {
    await installCommonRoutes(page, 'no-op')
    await page.goto('/chat')
  })

  test('typing "line one", Shift+Enter, "line two" produces "line one\\nline two" in the textarea and does not submit', async ({ page }) => {
    const input = page.getByTestId('message-input')
    await expect(input).toBeVisible()

    let postedSendCount = 0
    page.on('request', (request) => {
      if (request.method() === 'POST' && /\/sessions\/[^/]+\/messages$/.test(request.url())) {
        postedSendCount += 1
      }
    })

    await input.click()
    await input.pressSequentially('line one')
    await input.press('Shift+Enter')
    await input.pressSequentially('line two')

    // Behaviour pin: the textarea must contain a literal newline between
    // the two segments. Pre-fix this assertion fails because a stacked
    // document-level keydown handler in FuzzySearchModal calls
    // event.preventDefault() on every Enter (regardless of modifier or
    // open state), which suppresses the textarea's default \n insertion.
    await expect(input).toHaveValue('line one\nline two')

    // Implementation-pin retained from MessageInput.spec.ts:152-168 —
    // Shift+Enter must NOT trigger a send. We assert it via the network
    // surface rather than a vitest spy because we are driving the live
    // UI.
    expect(postedSendCount).toBe(0)
  })
})

test.describe('Bug 3 — Markdown renders as HTML in assistant bubbles', () => {
  // Single rich-markdown reply that exercises every element MarkdownRenderer
  // is meant to support: headings, lists, code (fenced + inline), emphasis,
  // links, tables, blockquote.
  const RICH_MARKDOWN = [
    '# Heading One',
    '',
    '## Heading Two',
    '',
    'A paragraph with **bold text**, *italic text*, and inline `code`.',
    '',
    'A [link to docs](https://example.com/docs).',
    '',
    '- ul item one',
    '- ul item two',
    '- ul item three',
    '',
    '1. ol item one',
    '2. ol item two',
    '',
    '```js',
    'const greet = (name) => `hello ${name}`',
    '```',
    '',
    '> a blockquote line',
    '',
    '| Col A | Col B |',
    '| ----- | ----- |',
    '| a1    | b1    |',
    '| a2    | b2    |',
    '',
  ].join('\n')

  test.beforeEach(async ({ page }) => {
    await installCommonRoutes(page, RICH_MARKDOWN)
    await page.goto('/chat')
  })

  test('headings, lists, code, bold/italic, link, table, and blockquote all render as HTML elements inside the assistant bubble', async ({ page }) => {
    // Drive an assistant reply by sending any user message — the mocked
    // POST /messages handler returns the rich-markdown reply.
    const input = page.getByTestId('message-input')
    await input.fill('show me markdown')
    await page.getByTestId('send-button').click()

    const assistantBubble = page.getByTestId('message-assistant').first()
    await expect(assistantBubble).toBeVisible({ timeout: 10_000 })

    // Scope all assertions to the bubble so we do not pick up any
    // chrome (NavBar, AgentPicker labels) that happens to share a tag.
    const md = assistantBubble.locator('.markdown-body')
    await expect(md).toBeVisible()

    // Behaviour pins per element. Each await is independent so a partial
    // breakage tells us exactly which element regressed instead of
    // collapsing into a single "bubble has no markdown" failure.
    await expect(md.locator('h1', { hasText: 'Heading One' })).toHaveCount(1)
    await expect(md.locator('h2', { hasText: 'Heading Two' })).toHaveCount(1)

    await expect(md.locator('strong', { hasText: 'bold text' })).toHaveCount(1)
    await expect(md.locator('em', { hasText: 'italic text' })).toHaveCount(1)

    // Inline code: <code> not inside a <pre>.
    const inlineCode = md.locator('code:not(pre code)')
    await expect(inlineCode.filter({ hasText: 'code' })).toHaveCount(1)

    // Link.
    const link = md.locator('a', { hasText: 'link to docs' })
    await expect(link).toHaveCount(1)
    await expect(link).toHaveAttribute('href', 'https://example.com/docs')

    // Lists.
    await expect(md.locator('ul > li')).toHaveCount(3)
    await expect(md.locator('ol > li')).toHaveCount(2)

    // Fenced code block.
    const fenced = md.locator('pre > code')
    await expect(fenced).toHaveCount(1)
    await expect(fenced).toContainText('const greet')

    // Blockquote.
    await expect(md.locator('blockquote', { hasText: 'a blockquote line' })).toHaveCount(1)

    // Table.
    await expect(md.locator('table')).toHaveCount(1)
    await expect(md.locator('table thead th')).toHaveCount(2)
    await expect(md.locator('table tbody tr')).toHaveCount(2)

    // Final guard: the raw markdown source must NOT appear verbatim in
    // the bubble's text content (i.e. the renderer didn't fall back to
    // a `<pre>{{content}}</pre>` style escape).
    const rawText = await assistantBubble.textContent()
    expect(rawText ?? '').not.toContain('# Heading One')
    expect(rawText ?? '').not.toContain('**bold text**')
  })
})
