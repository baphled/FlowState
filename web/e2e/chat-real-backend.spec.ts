import { test, expect } from '@playwright/test'

/**
 * Real-backend chat-flow specs.
 *
 * Unlike the rest of the e2e suite, these specs DO NOT install `page.route`
 * mocks for `/api/v1/*`. They drive the live FlowState backend at
 * http://localhost:8080 (proxied via the Vite dev server) so the tests
 * exercise the actual SSE timing and POST sequencing — the very surfaces
 * that the mock-based specs cannot characterise. Two production bugs were
 * shipped past the mock-based suite:
 *
 *   - Bug A: brand-new session, the user prompt rendered as two bubbles
 *     because the SSE `[DONE]` handler called `reconcileFromBackend` before
 *     `await sendSessionMessage` resolved, the merge preserved the local
 *     `temp-*` orphan, and the subsequent optimistic-id swap produced two
 *     messages with the same id.
 *   - Bug B: brand-new session, the user saw no agent-activity indicator
 *     and no streamed assistant content — the assistant response only
 *     appeared after a manual reload because the post-POST code path did
 *     not reconcile the canonical backend state into local messages.
 *
 * These specs assume a live backend that is reachable at /api/v1/* via the
 * dev server and an agent (`team-lead` or any default) capable of producing
 * a short response within ~30s. They are skipped when the backend is
 * unreachable (the suite is gated on `webServer` health, not on individual
 * test reachability — so a missing backend manifests as a hard failure with
 * a clear "POST /messages 502" trace, which is what we want).
 */

test.describe('chat real backend', () => {
  // Real-backend tests share backend state (the sessions list, the agent
  // configurations) across workers. Running them in parallel produces
  // cross-test interference: one test's `restoreStateFromBackend` can
  // pick up another test's just-created session and start sending into
  // it. Serial mode guarantees each spec sees a stable backend snapshot.
  test.describe.configure({ mode: 'serial' })
  test.setTimeout(120_000)

  test.beforeEach(async ({ page, request }) => {
    // Real-backend setup: create a brand-new backend session via the
    // API, then INTERCEPT only `GET /api/v1/sessions` to return ONLY
    // our session in the list. This is the ONLY mock — every other
    // /api/v1/* call goes to the real backend untouched, so the bug
    // surface (POST /messages → SSE [DONE] → state reconcile) is
    // exercised against live code.
    //
    // Why mock just GET /sessions: the long-lived backend at
    // http://localhost:8080 keeps every session ever created, including
    // those from other tests and developer runs. `restoreStateFromBackend`
    // falls back to "any session for the default agent" when the
    // persisted-id session isn't found, and on a polluted backend that
    // fallback picks a stranger's session. Limiting GET /sessions'
    // response to OUR session forces the fallback to land on our
    // brand-new empty session every time.
    //
    // The POST /sessions, POST /messages, GET /messages and GET /stream
    // endpoints are NOT routed — they hit the real backend.
    const createRes = await request.post('http://localhost:8080/api/v1/sessions', {
      data: { agent_id: 'Team-Lead' },
    })
    const created = (await createRes.json() as { id: string; agentId: string; createdAt: string; updatedAt: string })
    const sessionId = created.id

    await page.route('**/api/v1/sessions', async (route) => {
      if (route.request().method() === 'GET') {
        // Only return OUR session. The chatStore picks it as the active
        // session for the team-lead agent on restore.
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify([
            {
              id: created.id,
              agentId: created.agentId,
              currentAgentId: created.agentId,
              title: '',
              messageCount: 0,
              createdAt: created.createdAt,
              updatedAt: created.updatedAt,
              isStreaming: false,
            },
          ]),
        })
        return
      }
      await route.continue()
    })

    await page.goto('/chat')
    await page.evaluate((sid) => {
      localStorage.clear()
      localStorage.setItem('chat.currentSessionId', sid)
      localStorage.setItem('chat.agentId', 'Team-Lead')
    }, sessionId)
    await page.reload()
    await page.getByTestId('chat-empty-state').waitFor({ state: 'visible', timeout: 15_000 })
    // Wait for restoreStateFromBackend to settle on the Team-Lead
    // session — until this completes, `loadAgents` will have set the
    // agent to the first available (alphabetical: API-Engineer) and a
    // send dispatched in this window would race against the restore
    // and corrupt the test setup.
    await expect(page.getByTestId('agent-picker')).toContainText('Team Lead', { timeout: 10_000 })
  })

  test('fresh session: user prompt rendered exactly once (Bug A)', async ({ page }) => {
    // Bug A regression: pre-fix the optimistic `temp-*` user bubble and the
    // server-id user bubble both appeared in the thread after a fresh send.
    // The duplicate is observable within ~2s of the click — the SSE [DONE]
    // arrives early and the early reconcile leaks the orphan.
    //
    // We assert that the user bubble matching our prompt is rendered
    // EXACTLY ONCE at every sample over a 4-second window starting
    // ~500ms after the click. The window is deliberately wide enough
    // to cover both the pre-POST race (early SSE [DONE] fires reconcile
    // before optimistic-id swap) and the post-POST race (reconcile
    // collides with the optimistic bubble). The pre-fix duplicate
    // persists from ~300ms to ~3.5s in our reproduction, so a ≥4s
    // window with multiple samples is enough to catch any timing
    // variation.
    //
    // We do NOT wait for the assistant bubble — that would couple this
    // spec to the agent's response latency (which can be many tens of
    // seconds). The post-send reconcile is exercised separately by
    // Bug B part 2.
    const input = page.getByTestId('message-input')
    const sendBtn = page.getByTestId('send-button')

    const PROMPT = 'one two three'
    await input.fill(PROMPT)
    await sendBtn.click()

    const earlyUserBubbles = page.locator(`.message-bubble.user`).filter({ hasText: PROMPT })
    const sampleCount = 8
    const sampleIntervalMs = 500
    for (let i = 1; i <= sampleCount; i++) {
      await page.waitForTimeout(sampleIntervalMs)
      const count = await earlyUserBubbles.count()
      expect(
        count,
        `user prompt rendered ${count} times at sample ${i} (t≈${i * sampleIntervalMs}ms after send) — must always be 1`,
      ).toBe(1)
    }
  })

  test('fresh session: agent activity indicator visible while streaming (Bug B part 1)', async ({ page }) => {
    // Bug B part 1: the user reported no loading-dots / no animation while
    // the agent was working. Pre-fix `isStreaming` was only set true on the
    // first SSE `content` event. With the live backend emitting only
    // `[DONE]` (no intermediate content events) the indicator never showed.
    //
    // Post-fix: the indicator is shown for the entire window between the
    // SSE connect and the [DONE] sentinel — independent of whether content
    // events arrive — so the user has a continuous "the agent is thinking"
    // affordance.
    const input = page.getByTestId('message-input')
    const sendBtn = page.getByTestId('send-button')
    const indicator = page.getByTestId('agent-activity-indicator')

    await input.fill('say hi back')
    await sendBtn.click()

    await expect(indicator, 'agent-activity-indicator must appear within 1s of send')
      .toBeVisible({ timeout: 1_000 })
  })

  test('fresh session: assistant response appears without manual reload (Bug B part 2)', async ({ page }) => {
    // Bug B part 2: pre-fix the assistant content arrived only after
    // F5/reload because the post-POST code path called `loadSessions()`
    // (sessions list only) but never reconciled the canonical message
    // history. A late-completing agent therefore never landed in local
    // state without a full hydration cycle.
    //
    // Post-fix: `sendMessage` always reconciles after `await
    // sendSessionMessage` resolves, so the final assistant content is
    // visible without any user action.
    const input = page.getByTestId('message-input')
    const sendBtn = page.getByTestId('send-button')

    await input.fill('say hi back')
    await sendBtn.click()

    // The assistant content must materialise within 60s (generous to
    // accommodate slow providers; the median is ~5s for a short prompt).
    // We rely on the .message-bubble.assistant selector — if the response
    // fans out into delegations or tool calls the count is allowed to be
    // higher; the assertion is "at least one", not "exactly one".
    await expect
      .poll(
        async () => await page.locator('.message-bubble.assistant').count(),
        { timeout: 60_000, message: 'no assistant bubble visible without reload — Bug B regression' },
      )
      .toBeGreaterThan(0)
  })

  test('fresh session: model+provider chip renders after the first assistant turn (Track B chip-on-fresh-session)', async ({ page }) => {
    // May 2026 regression cover. Track B's initial implementation only
    // populated chatStore.currentModelId / currentProviderId on a
    // `provider_changed` SSE transition, which never fires on the happy
    // path. A user on a fresh session saw nothing for the model — the
    // chip's `v-if` fell through and the activity indicator showed only
    // "<agent> is working…" with no provenance.
    //
    // The fix lives at two layers:
    //   - Backend: handleCreateSession seeds the session's
    //     CurrentProviderID / CurrentModelID from the agent manifest's
    //     first PreferredModels entry (when present).
    //   - Backend: appendSessionMessage promotes the engine-stamped
    //     (model, provider) onto the session whenever an assistant
    //     turn lands. This is the path exercised here — the user's
    //     live agent config typically has no PreferredModels (the
    //     deployed manifests at ~/.config/flowstate/agents/*.md don't
    //     declare them), so the chip becomes populated only after the
    //     first assistant chunk lands and the engine's e.LastModel() /
    //     e.LastProvider() flow into the message.
    //
    // The frontend reads from chatStore.currentModelId /
    // currentProviderId; both are kept in sync with the session
    // metadata via reconcileFromBackend on every poll. The chip's
    // `data-testid="agent-activity-model"` becomes visible once at
    // least one of the fields is non-empty.
    const input = page.getByTestId('message-input')
    const sendBtn = page.getByTestId('send-button')

    await input.fill('say "ok"')
    await sendBtn.click()

    // Wait for an assistant bubble first — that confirms the engine has
    // streamed at least one chunk (so e.LastModel() / e.LastProvider()
    // are populated) and the post-POST reconcile has refreshed the
    // session metadata.
    await expect
      .poll(
        async () => await page.locator('.message-bubble.assistant').count(),
        { timeout: 60_000, message: 'no assistant bubble — cannot assert chip provenance' },
      )
      .toBeGreaterThan(0)

    // The chip is bound to chatStore state and rendered while
    // isStreaming OR isLoading is true. After the assistant bubble
    // arrives we may have settled to neither, so the chip will hide.
    // Read the underlying store state directly — that's the load-bearing
    // assertion for the regression: the *data* is now populated, even if
    // the chip happens not to be on-screen at this exact tick.
    const storeState = await page.evaluate(() => {
      const w = window as unknown as {
        __chatStoreSnapshot?: () => { currentModelId: string; currentProviderId: string }
      }
      // Prefer an injected snapshot helper when present (the dev build
      // exposes one for diagnostics); fall back to scraping window for
      // a Pinia instance.
      if (typeof w.__chatStoreSnapshot === 'function') return w.__chatStoreSnapshot()
      return null
    })

    // Re-trigger streaming so the chip is on-screen for the DOM-evidence
    // assertion. We send a second prompt and assert the chip renders
    // immediately — at this point the store state from the first turn is
    // already promoted, so the chip must show non-empty content.
    await input.fill('again')
    await sendBtn.click()

    const chip = page.getByTestId('agent-activity-model')
    await expect(chip, 'model chip must be visible while the second turn streams').toBeVisible({
      timeout: 10_000,
    })
    const chipText = (await chip.textContent())?.trim() ?? ''
    expect(chipText.length, `chip text must not be blank — got "${chipText}"`).toBeGreaterThan(0)
    expect(chipText, 'chip should start with "on " per the activity-indicator format').toMatch(/^on\s+\S+/)

    // Optional store-state diagnostic. When the diagnostic helper isn't
    // injected (production build, default config), the chip-text
    // assertion above is the load-bearing check.
    if (storeState) {
      expect(
        storeState.currentModelId.length + storeState.currentProviderId.length,
        'at least one of currentModelId / currentProviderId must be set after the first assistant turn',
      ).toBeGreaterThan(0)
    }
  })
})
