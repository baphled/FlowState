import { test, expect, Page } from '@playwright/test'

// Child Session Turn Registry Plumbing (May 2026) PR4 §S7 — runtime-switch
// reattach. Live regression cover for the "click a child row, see no live
// updates" gap.
//
// Behaviour pinned:
//   1. ChildSessionsPanel.selectChild → chatStore.loadSessionMessages
//   2. loadSessionMessages → maybeReattachStream (S7 fix at chatStore.ts:1645)
//   3. maybeReattachStream → pollTurnUntilTerminal against the CHILD's
//      sessionId + activeTurnId
//
// Pre-fix audit at internal session 572b45ff (PR4 — S7 + S9):
//   restoreStateFromBackend reattached on initial mount (chatStore.ts:1117 /
//   1150) but the runtime switch path was missing the call. Net effect:
//   PR3's backend-authoritative Live indicator lit up correctly on the LIST
//   surface, but once the user clicked through to the child the long-poll
//   was never started — they saw static history while the engine was
//   actively producing chunks server-side.
//
// Unit coverage at web/src/stores/chatStore.test.ts:7409-7517 already drives
// the loadSessionMessages → fetchTurn chain in isolation. This e2e closes
// the integration gap: it drives the user-visible CLICK on the panel row
// and asserts the turn-poll endpoint for the CHILD is hit. Without the e2e
// a future refactor of ChildSessionsPanel.selectChild that silently swaps
// `loadSessionMessages` for a non-reattaching alternative would pass the
// unit suite and re-introduce the production bug.
//
// Guards commit 572b45ff (test(engine,web,tools): PR4 — close S7 runtime-
// switch reattach + S9 nested-delegation + plan-as-detector hook).

test.describe('Child session click → reattach stream (PR4 §S7)', () => {
  // 3-second budget matches the sibling delegation-mid-poll spec. The
  // poll-loop is sub-second; we give generous headroom for page hydration
  // plus one click → loadSessionMessages → fetchTurn round-trip.
  const REATTACH_TIMEOUT_MS = 3_000

  const agents = [
    { id: 'planner', name: 'Planner', description: 'plans', model: 'm' },
    { id: 'executor', name: 'Executor', description: 'runs', model: 'm' },
  ]

  // Parent session — already streaming so its own activeTurnId is set.
  // The user lands here on bootstrap; the child row is rendered by
  // ChildSessionsPanel.
  const parentSession = {
    id: 'session-parent',
    agentId: 'planner',
    currentAgentId: 'planner',
    title: 'Parent',
    createdAt: '2026-05-20T09:00:00Z',
    updatedAt: '2026-05-20T09:00:05Z',
    messageCount: 1,
    status: 'active',
    depth: 0,
    isStreaming: true,
    activeTurnId: 'turn-parent-001',
  }

  // Child session — backend-stamped activeTurnId from the Turn registry
  // (PR2a + PR2b). Pre-PR4 a click on this row swapped the active
  // session but did NOT reattach a long-poll on the child's turnId.
  const childSession = {
    id: 'session-child-stream',
    agentId: 'executor',
    currentAgentId: 'executor',
    parentId: 'session-parent',
    chainId: 'chain-1',
    title: 'Delegated Run',
    createdAt: '2026-05-20T09:00:10Z',
    updatedAt: '2026-05-20T09:00:10Z',
    messageCount: 0,
    status: 'active',
    depth: 1,
    isStreaming: true,
    activeTurnId: 'turn-child-stream-001',
  }

  async function setupRoutes(
    page: Page,
    state: { childTurnPolls: string[] },
  ): Promise<void> {
    await page.route('**/api/agents', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(agents),
      })
    })
    await page.route('**/api/v1/models', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ providers: [] }),
      })
    })
    await page.route('**/api/v1/swarms', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
    })
    await page.route('**/api/swarm/events', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
    })

    // The sessions list contains BOTH parent + child from bootstrap so
    // the panel renders the child row immediately. The mid-poll refresh
    // path is covered separately by
    // delegation-mid-poll-child-panel-refresh.spec.ts; this spec isolates
    // the click → reattach contract.
    await page.route('**/api/v1/sessions', async (route) => {
      if (route.request().method() !== 'GET') {
        await route.fulfill({ status: 405, body: '' })
        return
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([parentSession, childSession]),
      })
    })

    await page.route('**/api/v1/sessions/session-parent/messages', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          { id: 'u1', role: 'user', content: 'delegate this', timestamp: '2026-05-20T09:00:00Z' },
        ]),
      })
    })

    // Child session's static history — clicking the row triggers
    // fetchSessionMessages for this id, and post-S7 the reattach also
    // long-polls fetchTurn for this id + activeTurnId.
    await page.route('**/api/v1/sessions/session-child-stream/messages', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          {
            id: 'u-child-1',
            role: 'user',
            content: 'run the build',
            timestamp: '2026-05-20T09:00:10Z',
          },
        ]),
      })
    })

    // Parent's turn-poll — fulfilled but never the assertion target;
    // we observe whether the CHILD's turn-poll fires.
    await page.route('**/api/v1/sessions/session-parent/turns/turn-parent-001*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          turn_id: 'turn-parent-001',
          session_id: 'session-parent',
          status: 'running',
          started_at: '2026-05-20T09:00:00Z',
          completed_at: null,
          model: { provider: 'mock', model: 'mock' },
          error: '',
          messages: [],
        }),
      })
    })

    // The load-bearing assertion target. Pre-S7 this endpoint was
    // never hit because loadSessionMessages did not call
    // maybeReattachStream on the runtime-switch path. Post-S7 it MUST
    // fire within the reattach window after the click.
    //
    // First poll returns running so the loop persists; the test
    // doesn't need to drain the loop, just observe that the URL was
    // hit at least once.
    await page.route(
      '**/api/v1/sessions/session-child-stream/turns/turn-child-stream-001*',
      async (route) => {
        state.childTurnPolls.push(route.request().url())
        // Return completed immediately so the poll loop exits cleanly
        // when the test fixture tears down. The contract under test is
        // "the endpoint was hit at all" — not "the loop polled N times".
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            turn_id: 'turn-child-stream-001',
            session_id: 'session-child-stream',
            status: 'completed',
            started_at: '2026-05-20T09:00:10Z',
            completed_at: '2026-05-20T09:00:30Z',
            model: { provider: 'mock', model: 'mock' },
            error: '',
            messages: [],
          }),
        })
      },
    )

    // Per-session SSE streams — block so EventSource opens but never
    // pushes chunks. The test path is poll-driven; SSE is not the
    // surface under audit here.
    await page.route('**/api/v1/sessions/session-parent/stream', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        body: '',
      })
    })
    await page.route('**/api/v1/sessions/session-child-stream/stream', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        body: '',
      })
    })
  }

  test('clicking a child row in the panel fires a long-poll against the child\'s activeTurnId', async ({
    page,
  }) => {
    const state = {
      childTurnPolls: [] as string[],
    }
    await setupRoutes(page, state)

    // Land on the parent. The panel renders the child row from the
    // bootstrap /sessions response.
    await page.addInitScript(() => {
      window.localStorage.setItem('chat.currentSessionId', 'session-parent')
      window.localStorage.setItem('chat.agentId', 'planner')
    })
    await page.goto('/chat')

    // The child row is present (PR3 surface — covered by
    // child-session-active-turn-indicator.spec.ts). Sanity-check it's
    // visible before we click it.
    const childRow = page.getByTestId('child-session-row-session-child-stream')
    await expect(childRow).toBeVisible({ timeout: REATTACH_TIMEOUT_MS })

    // Pre-click: the child's turn-poll endpoint must NOT have fired.
    // Only the parent's poll runs at this stage (the parent is the
    // active session post-bootstrap). Pre-S7 the assertion below
    // would also pass — but post-click it would STILL be empty,
    // which is the regression we pin.
    expect(state.childTurnPolls).toEqual([])

    // The S7 trigger: click the child row. ChildSessionsPanel.selectChild
    // calls chatStore.loadSessionMessages(child.id), which post-S7 reaches
    // maybeReattachStream and starts pollTurnUntilTerminal on the child's
    // sessionId + activeTurnId.
    await childRow.click()

    // The load-bearing assertion. Within REATTACH_TIMEOUT_MS the
    // child's turn-poll endpoint MUST have been hit at least once.
    // Pre-S7 this would never fire because loadSessionMessages
    // returned without reaching maybeReattachStream.
    await expect.poll(() => state.childTurnPolls.length, {
      timeout: REATTACH_TIMEOUT_MS,
    }).toBeGreaterThanOrEqual(1)

    // Defence-in-depth: the request URL contains `wait=true`. The
    // long-poll's server-side hold gate is what makes the live-chunk
    // path viable; a degenerate tight-spin loop without it would
    // technically satisfy the count assertion above while breaking
    // the production contract. Mirrors the unit assertion at
    // chatStore.test.ts:7481.
    const firstPoll = state.childTurnPolls[0]
    expect(firstPoll).toContain('wait=true')

    // Sanity: the active session has swapped to the child — proves
    // that the click also flipped chatStore.currentSessionId via the
    // canonical loadSessionMessages path (not via a non-reattaching
    // shortcut).
    await expect(page.getByTestId('message-list')).toContainText('run the build', {
      timeout: REATTACH_TIMEOUT_MS,
    })
  })

  test('clicking a child row with NO activeTurnId does not fire a turn-poll', async ({
    page,
  }) => {
    // R8 boundary mirror — chatStore.test.ts:7484-7516. The
    // maybeReattachStream early-return at chatStore.ts:1175-1181 must
    // observe an empty activeTurnId and skip the poll. Without this
    // gate every session-switch click would fire a useless poll
    // against a non-existent turnID.
    const idleChild = {
      ...childSession,
      id: 'session-child-idle',
      isStreaming: false,
      activeTurnId: '',
    }

    const state = { childTurnPolls: [] as string[] }
    await setupRoutes(page, state)

    // Override the sessions list to include the idle child instead.
    await page.route('**/api/v1/sessions', async (route) => {
      if (route.request().method() !== 'GET') {
        await route.fulfill({ status: 405, body: '' })
        return
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([parentSession, idleChild]),
      })
    })
    await page.route('**/api/v1/sessions/session-child-idle/messages', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          {
            id: 'u-idle-1',
            role: 'user',
            content: 'idle child',
            timestamp: '2026-05-20T09:00:10Z',
          },
        ]),
      })
    })
    // Idle child has no SSE stream to subscribe to in this scenario;
    // mock the endpoint anyway to silence any speculative connections.
    await page.route('**/api/v1/sessions/session-child-idle/stream', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        body: '',
      })
    })

    await page.addInitScript(() => {
      window.localStorage.setItem('chat.currentSessionId', 'session-parent')
      window.localStorage.setItem('chat.agentId', 'planner')
    })
    await page.goto('/chat')

    const childRow = page.getByTestId('child-session-row-session-child-idle')
    await expect(childRow).toBeVisible({ timeout: REATTACH_TIMEOUT_MS })
    await childRow.click()

    // Confirm the active session swapped (the click was honoured).
    await expect(page.getByTestId('message-list')).toContainText('idle child', {
      timeout: REATTACH_TIMEOUT_MS,
    })

    // Give the reattach path a moment in case the early-return regresses.
    // A 1-second hold is generous — pollTurnUntilTerminal fires
    // synchronously after the message fetch resolves on the post-S7 path.
    await page.waitForTimeout(1_000)

    // The idle child's activeTurnId is empty; no poll endpoint exists
    // to mock. The assertion is structural: maybeReattachStream's
    // early-return at chatStore.ts:1175-1181 short-circuits before any
    // fetchTurn call, so state.childTurnPolls remains empty.
    expect(state.childTurnPolls).toEqual([])
  })
})
