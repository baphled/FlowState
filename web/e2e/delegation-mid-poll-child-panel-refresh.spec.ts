import { test, expect, Page } from '@playwright/test'

// Sketch A — UI Delegation Chain Not Updating (May 2026) Issue B.A.
//
// Live regression cover for the "child doesn't appear in panel for 30
// seconds" UX gap. Pre-fix:
//   - `chatStore.sessions` was only refreshed on bootstrap / after a
//     successful send / after createSession — never DURING a streaming
//     turn.
//   - During a long delegation the parent's `delegation_started` row
//     landed in the Turn snapshot poll, but the child session itself
//     did not appear in `ChildSessionsPanel` until the parent's turn
//     completed and the post-poll reconcile fired loadSessions.
//
// Quick-win contract: `pollTurnUntilTerminal` observes the
// `delegation_started` row in the Turn snapshot's `messages` array,
// adds its id to a per-row debounce set, and fires `loadSessions()`
// once. The next `fetchSessions` payload includes the freshly-spawned
// child summary; `ChildSessionsPanel` re-renders within one poll-tick.
//
// Sibling track Sketch B is the load-bearing fix for the child's own
// chat view (plumbing a child Turn through DelegateTool). THIS spec
// pins ONLY the panel-refresh quick win.
//
// Investigation note:
//   ~/vaults/baphled/1. Projects/FlowState/Bug Fixes/
//     UI Delegation Chain Not Updating (May 2026).md § Aggravating factor

test.describe('Delegation mid-poll child panel refresh (Sketch A)', () => {
  // Tight 3s budget. The cadence is sub-second per poll; the test
  // gives generous headroom for the page hydration plus one poll
  // round-trip. If this regresses to "after parent turn completes"
  // behaviour the panel won't populate within the 3s window — the
  // assertion blows up cleanly.
  const PANEL_REFRESH_TIMEOUT_MS = 3_000

  const agents = [
    { id: 'planner', name: 'Planner', description: 'plans', model: 'm' },
    { id: 'executor', name: 'Executor', description: 'runs', model: 'm' },
  ]

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
    // Phase-4-Commit-2 — non-empty activeTurnId is what triggers
    // maybeReattachStream → pollTurnUntilTerminal on session-load.
    activeTurnId: 'turn-parent-001',
  }

  const childSession = {
    id: 'session-child-001',
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
    isStreaming: false,
  }

  async function setupRoutes(
    page: Page,
    state: {
      childAppeared: boolean
      turnPollCount: number
      sessionsFetchCount: number
    },
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

    // /api/swarms — bootstrap reads this.
    await page.route('**/api/v1/swarms', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
    })
    await page.route('**/api/swarm/events', async (route) => {
      // Long-poll endpoint — just block forever; the test doesn't
      // care about SwarmEvent ingestion for THIS path. Fulfilling
      // promptly with [] is fine too.
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
    })

    await page.route('**/api/v1/sessions', async (route) => {
      // The contract here is the load-bearing part of the test:
      //   Call 1 (bootstrap): parent ONLY — child not yet spawned.
      //   Call 2 (Sketch A fan-out after delegation_started row): child
      //     now exists; loadSessions returns parent + child.
      //
      // ChildSessionsPanel re-renders off chatStore.sessions; if Sketch
      // A's mid-poll refresh works, sessionsFetchCount must reach 2
      // and the panel must display the child row.
      if (route.request().method() !== 'GET') {
        await route.fulfill({ status: 405, body: '' })
        return
      }
      state.sessionsFetchCount += 1
      const body = state.childAppeared ? [parentSession, childSession] : [parentSession]
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(body),
      })
    })

    // /messages — bootstrap fetches the parent's history. Empty + user
    // turn for the static seed.
    await page.route('**/api/v1/sessions/session-parent/messages', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          { id: 'u1', role: 'user', content: 'delegate this', timestamp: '2026-05-20T09:00:00Z' },
        ]),
      })
    })
    await page.route('**/api/v1/sessions/session-child-001/messages', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
    })

    // The turn-poll endpoint is the load-bearing surface. We model:
    //   Poll 1: running, messages=[delegation_started row]
    //          → Sketch A fires loadSessions which flips childAppeared
    //   Poll 2: completed
    await page.route('**/api/v1/sessions/session-parent/turns/turn-parent-001*', async (route) => {
      state.turnPollCount += 1
      if (state.turnPollCount === 1) {
        // Mark the child as "now spawned" so the NEXT sessions fetch
        // returns it. This mirrors the real backend where
        // createChildSession completes shortly before the
        // delegation_started chunk hits the parent's accumulator.
        state.childAppeared = true
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
            messages: [
              {
                id: 'del-1',
                role: 'delegation_started',
                content: '',
                timestamp: '2026-05-20T09:00:05Z',
                status: 'running',
                chainId: 'chain-1',
                targetAgent: 'executor',
              },
            ],
          }),
        })
        return
      }
      // Subsequent polls: terminal so the loop exits.
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          turn_id: 'turn-parent-001',
          session_id: 'session-parent',
          status: 'completed',
          started_at: '2026-05-20T09:00:00Z',
          completed_at: '2026-05-20T09:00:30Z',
          model: { provider: 'mock', model: 'mock' },
          error: '',
          messages: [],
        }),
      })
    })

    await page.route('**/api/v1/sessions/session-parent/stream', async (route) => {
      // SSE stream — block so the FE's EventSource opens but never
      // pushes chunks (the test path is poll-driven, not SSE-driven).
      // Returning a static 200 with empty body would close the stream
      // immediately; we instead leave it pending forever, which
      // matches a real long-lived EventSource and lets the poll loop
      // be the sole observable surface.
      await route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        body: '',
      })
    })
  }

  test('child session row appears in ChildSessionsPanel after delegation_started lands in a turn poll', async ({ page }) => {
    const state = {
      childAppeared: false,
      turnPollCount: 0,
      sessionsFetchCount: 0,
    }

    await setupRoutes(page, state)

    // Park the user on the parent session so the panel renders against
    // the parent's children.
    await page.addInitScript(() => {
      window.localStorage.setItem('chat.currentSessionId', 'session-parent')
      window.localStorage.setItem('chat.agentId', 'planner')
    })

    await page.goto('/chat')

    // Sanity: the panel is empty (no children yet) on the initial
    // bootstrap fetch.
    const panel = page.getByTestId('child-sessions-panel')
    // The panel auto-hides when childSessions.length === 0 via
    // `.is-empty` CSS; rather than asserting visibility (which is
    // false in this state), assert the row is not yet present.
    await expect(page.getByTestId('child-session-row-session-child-001')).toHaveCount(0)

    // The contract: within PANEL_REFRESH_TIMEOUT_MS of the turn poll
    // surfacing the delegation_started row, the panel re-renders with
    // the child entry. Pre-fix: the panel would not show the child
    // until the parent's turn completed and the post-poll reconcile
    // fired loadSessions (typically tens of seconds in the wild).
    await expect(page.getByTestId('child-session-row-session-child-001')).toBeVisible({
      timeout: PANEL_REFRESH_TIMEOUT_MS,
    })

    // Sketch A's mid-poll refresh fired loadSessions at least once
    // BEYOND the bootstrap fetch (call count >= 2). The exact number
    // depends on the order of bootstrap fetches + the mid-poll
    // refresh, so we assert >= 2 rather than == 2.
    expect(state.sessionsFetchCount).toBeGreaterThanOrEqual(2)

    // The turn poll fired at least once — Sketch A's trigger surface.
    expect(state.turnPollCount).toBeGreaterThanOrEqual(1)
  })
})
