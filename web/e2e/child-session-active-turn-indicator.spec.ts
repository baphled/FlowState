import { test, expect, Page } from '@playwright/test'

// Child Session Turn Registry Plumbing (May 2026) PR3 — backend-authoritative
// Live indicator e2e.
//
// PR1 added the turn.Registry primitives; PR2a plumbed the registry through
// the single-target DelegateTool.executeSync path so the engine stamps an
// activeTurnId on the spawned child session; PR2b plumbed the same registry
// through the swarm fan-out path (bootstrapMemberSession + buildMemberRunner)
// so each member of a swarm-target delegation also gets a child Turn stamp.
//
// PR3 — this slice — flips the FE consumer of the Live indicator on three
// session-list surfaces (ChildSessionsPanel, SessionBrowser, SessionSwitcher)
// from FE-side chatStore.streamingFor to backend-authoritative
// SessionSummary.activeTurnId. These two specs assert the end-to-end shape:
// the backend ships activeTurnId on /api/v1/sessions, the FE renders the
// Live indicator on every child row that has a non-empty value.
//
//   - S9.6: single-target delegation — one child appears with a Live
//     indicator within a tight window after delegation lands.
//   - S9.7: swarm fan-out — multiple children appear with Live indicators
//     concurrently. PR2b's S7.4 contract test verified the backend stamp;
//     this spec verifies the FE renders correctly.
//
// Timing rationale: 3-second budget per child Live indicator on S9.6
// matches the PR7-Live `delegation-mid-poll-child-panel-refresh.spec.ts`
// precedent. The swarm fan-out S9.7 uses a slightly looser 5-second budget
// to absorb the natural staggering between three concurrent member spawns
// (each child is a separate session-list reconcile tick). Both budgets are
// tight enough to catch real regressions (the pre-PR3 path would never
// surface the indicator at all because the FE was looking at the wrong
// field) and loose enough to avoid CI flake on cold worker startup.

test.describe('Child session Live indicator — backend-authoritative activeTurnId (PR3 S9.6 / S9.7)', () => {
  const SINGLE_TARGET_TIMEOUT_MS = 3_000
  const SWARM_FANOUT_TIMEOUT_MS = 5_000

  const agents = [
    { id: 'planner', name: 'Planner', description: 'plans', model: 'm' },
    { id: 'executor', name: 'Executor', description: 'runs', model: 'm' },
    { id: 'reviewer', name: 'Reviewer', description: 'reviews', model: 'm' },
    { id: 'researcher', name: 'Researcher', description: 'researches', model: 'm' },
  ]

  // Shared parent session — both specs delegate from this id. The parent
  // is itself streaming (non-empty activeTurnId) because the user's POST
  // landed a turn before the delegation fanned out; child rows must light
  // up independently.
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

  async function commonRoutes(page: Page): Promise<void> {
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
    await page.route('**/api/v1/sessions/session-parent/messages', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          { id: 'u1', role: 'user', content: 'delegate this', timestamp: '2026-05-20T09:00:00Z' },
        ]),
      })
    })
    await page.route('**/api/v1/sessions/session-parent/stream', async (route) => {
      // SSE stream — block so the FE's EventSource opens but never pushes
      // chunks. The test path is poll-driven, not SSE-driven.
      await route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        body: '',
      })
    })
  }

  // -------------------------------------------------------------------------
  // S9.6 — single-target delegation surfaces one child with a Live indicator
  // -------------------------------------------------------------------------
  test('single-target delegation: child row surfaces a Live indicator within 3s (S9.6)', async ({
    page,
  }) => {
    const childSession = {
      id: 'session-child-single',
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
      // PR2a — the backend stamps activeTurnId on the spawned child via
      // turn.Registry. PR3 — the FE reads it for the indicator.
      activeTurnId: 'turn-child-single-001',
    }

    let childAppeared = false
    let turnPollCount = 0

    await commonRoutes(page)
    await page.route('**/api/v1/sessions/session-child-single/messages', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
    })
    await page.route('**/api/v1/sessions', async (route) => {
      if (route.request().method() !== 'GET') {
        await route.fulfill({ status: 405, body: '' })
        return
      }
      const body = childAppeared ? [parentSession, childSession] : [parentSession]
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(body),
      })
    })
    await page.route('**/api/v1/sessions/session-parent/turns/turn-parent-001*', async (route) => {
      turnPollCount += 1
      if (turnPollCount === 1) {
        // First poll lands a delegation_started row. The FE's Sketch A
        // mid-poll refresh observes it and fires loadSessions, which now
        // returns the freshly-spawned child with a stamped activeTurnId.
        childAppeared = true
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

    await page.addInitScript(() => {
      window.localStorage.setItem('chat.currentSessionId', 'session-parent')
      window.localStorage.setItem('chat.agentId', 'planner')
    })
    await page.goto('/chat')

    // The child row must appear AND surface a Live indicator (the green
    // dot + "Live" text label) within the tight window. The label test-id
    // is the load-bearing assertion: it's gated on the panel's
    // `isStreaming(child)` predicate, which post-PR3 reads
    // SessionSummary.activeTurnId. Pre-PR3 this would never light up
    // because the FE consulted chatStore.streamingFor (which has no
    // entry for the engine-spawned child).
    await expect(
      page.getByTestId('child-session-row-session-child-single'),
    ).toBeVisible({ timeout: SINGLE_TARGET_TIMEOUT_MS })

    await expect(
      page.getByTestId('child-session-streaming-label-session-child-single'),
    ).toBeVisible({ timeout: SINGLE_TARGET_TIMEOUT_MS })
    await expect(
      page.getByTestId('child-session-streaming-label-session-child-single'),
    ).toHaveText('Live')

    // Pre-PR3 the panel's row carried the is-streaming class only when
    // chatStore.streamingFor reported true. Post-PR3 the same class is
    // driven by activeTurnId — assert the class is present to pin the
    // CSS-side affordance too.
    await expect(
      page.getByTestId('child-session-row-session-child-single'),
    ).toHaveClass(/is-streaming/, { timeout: SINGLE_TARGET_TIMEOUT_MS })
  })

  // -------------------------------------------------------------------------
  // S9.7 — swarm fan-out surfaces multiple children with Live indicators
  // -------------------------------------------------------------------------
  test('swarm fan-out: multiple child rows surface Live indicators concurrently within 5s (S9.7)', async ({
    page,
  }) => {
    // Three swarm members each get their own child session. PR2b stamps
    // an activeTurnId on each via bootstrapMemberSession → buildMemberRunner.
    // The FE list-surface must light up all three independently.
    const children = [
      {
        id: 'session-child-swarm-1',
        agentId: 'executor',
        currentAgentId: 'executor',
        parentId: 'session-parent',
        chainId: 'chain-swarm',
        title: 'Swarm Member 1',
        createdAt: '2026-05-20T09:00:11Z',
        updatedAt: '2026-05-20T09:00:11Z',
        messageCount: 0,
        status: 'active',
        depth: 1,
        isStreaming: true,
        activeTurnId: 'turn-child-swarm-1',
      },
      {
        id: 'session-child-swarm-2',
        agentId: 'reviewer',
        currentAgentId: 'reviewer',
        parentId: 'session-parent',
        chainId: 'chain-swarm',
        title: 'Swarm Member 2',
        createdAt: '2026-05-20T09:00:12Z',
        updatedAt: '2026-05-20T09:00:12Z',
        messageCount: 0,
        status: 'active',
        depth: 1,
        isStreaming: true,
        activeTurnId: 'turn-child-swarm-2',
      },
      {
        id: 'session-child-swarm-3',
        agentId: 'researcher',
        currentAgentId: 'researcher',
        parentId: 'session-parent',
        chainId: 'chain-swarm',
        title: 'Swarm Member 3',
        createdAt: '2026-05-20T09:00:13Z',
        updatedAt: '2026-05-20T09:00:13Z',
        messageCount: 0,
        status: 'active',
        depth: 1,
        isStreaming: true,
        activeTurnId: 'turn-child-swarm-3',
      },
    ]

    let childrenAppeared = false
    let turnPollCount = 0

    await commonRoutes(page)
    for (const child of children) {
      await page.route(`**/api/v1/sessions/${child.id}/messages`, async (route) => {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify([]),
        })
      })
    }
    await page.route('**/api/v1/sessions', async (route) => {
      if (route.request().method() !== 'GET') {
        await route.fulfill({ status: 405, body: '' })
        return
      }
      const body = childrenAppeared ? [parentSession, ...children] : [parentSession]
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(body),
      })
    })
    await page.route('**/api/v1/sessions/session-parent/turns/turn-parent-001*', async (route) => {
      turnPollCount += 1
      if (turnPollCount === 1) {
        // First poll surfaces three delegation_started rows simultaneously
        // — the swarm fan-out shape. Sketch A fires loadSessions once and
        // the next /sessions response carries all three children.
        childrenAppeared = true
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
                chainId: 'chain-swarm',
                targetAgent: 'executor',
              },
              {
                id: 'del-2',
                role: 'delegation_started',
                content: '',
                timestamp: '2026-05-20T09:00:06Z',
                status: 'running',
                chainId: 'chain-swarm',
                targetAgent: 'reviewer',
              },
              {
                id: 'del-3',
                role: 'delegation_started',
                content: '',
                timestamp: '2026-05-20T09:00:07Z',
                status: 'running',
                chainId: 'chain-swarm',
                targetAgent: 'researcher',
              },
            ],
          }),
        })
        return
      }
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

    await page.addInitScript(() => {
      window.localStorage.setItem('chat.currentSessionId', 'session-parent')
      window.localStorage.setItem('chat.agentId', 'planner')
    })
    await page.goto('/chat')

    // All three swarm members must surface AND each must light up its
    // Live indicator within the looser swarm window. The looseness
    // (5s rather than 3s) absorbs the natural staggering between three
    // concurrent reconcile ticks; the multi-child contract still pins
    // the regression because pre-PR3 NONE of them would light up.
    for (const child of children) {
      await expect(page.getByTestId(`child-session-row-${child.id}`)).toBeVisible({
        timeout: SWARM_FANOUT_TIMEOUT_MS,
      })
      await expect(
        page.getByTestId(`child-session-streaming-label-${child.id}`),
      ).toBeVisible({ timeout: SWARM_FANOUT_TIMEOUT_MS })
      await expect(
        page.getByTestId(`child-session-streaming-label-${child.id}`),
      ).toHaveText('Live')
      await expect(page.getByTestId(`child-session-row-${child.id}`)).toHaveClass(
        /is-streaming/,
        { timeout: SWARM_FANOUT_TIMEOUT_MS },
      )
    }
  })
})
