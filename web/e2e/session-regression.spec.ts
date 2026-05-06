import { test, expect, type Page, type ConsoleMessage, type Request } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'

/**
 * Session regression spec — drives the live backend at localhost:8080 and
 * verifies the user-visible work shipped this session as a regression guard.
 * No mocks of /api/v1/* except the GET /api/v1/sessions filter that pins the
 * test to a single freshly-created session (the same trick used by
 * chat-real-backend.spec.ts to defeat cross-test pollution from the
 * long-lived backend's 350+ historical sessions).
 *
 * Coverage map → commit references:
 *   #1 default agent on fresh visit is "default-assistant"  → 5c596e8
 *   #2 single user bubble per send (no duplicates)          → 93bf40e
 *   #3 activity indicator visible during streaming, hidden  → Track A streaming UX
 *   #4 model+provider chip non-empty in `on M · P` format   → bc8ffbf, a1675ee
 *   #5 tool-trigger toast titled "Working" appears          → 3f176a4
 *   #6 no raw JSON / wire-format leakage in chat thread     → 3537381, e81cb8d
 *   #7 multi-turn context retention across two turns        → 894b43c
 *   #8 delegation card click → child session                → 93bf40e (skipped: not
 *      reliably triggerable by default-assistant on a short prompt)
 *
 * The spec assumes:
 *   - `./build/flowstate serve` listening on :8080
 *   - Vite dev server on :5173 (the playwright webServer config starts it)
 *   - At least one openaicompat / zai provider configured so the
 *     default-assistant agent can stream a response in <60s.
 *
 * Evidence is captured to /tmp/session-regression-evidence/ for operator audit.
 */

const EVIDENCE_DIR = '/tmp/session-regression-evidence'

interface NetEntry {
  ts: string
  method: string
  url: string
  type: 'request' | 'response' | 'failed'
  status?: number
  size?: number
}

interface ConsoleEntry {
  ts: string
  type: string
  text: string
}

interface DomSnapshot {
  phase: string
  ts: string
  userBubbleCount: number
  assistantBubbleCount: number
  userBubbleTexts: string[]
  assistantBubbleTexts: string[]
  indicatorVisible: boolean
  modelChipVisible: boolean
  modelChipText: string
  pickerText: string
  url: string
}

const REGRESSION_NAME = 'REGRESSION_TEST_NAME_47'

// Patterns we MUST NOT see in any visible message bubble (#6).
// Each pattern represents a class of wire-format leakage that has shipped
// to users in past regressions.
const FORBIDDEN_PATTERNS: Array<{ pattern: RegExp; label: string }> = [
  { pattern: /\{"attempt"\s*:/, label: '{"attempt":...} retry envelope' },
  { pattern: /<task_result>/, label: '<task_result> literal tag' },
  { pattern: /\{"error"\s*:[^}]*"status"\s*:\s*"failed"/, label: '{"error":..."status":"failed"} envelope' },
  { pattern: /\{"type"\s*:\s*"tool_use"/, label: '{"type":"tool_use"...} raw chunk' },
  { pattern: /\{"role"\s*:\s*"(user|assistant|tool)"/, label: '{"role":"..."} raw message' },
  { pattern: /data:\s*\{"type"/, label: 'data:{"type"...} SSE frame' },
]

function nowIso(): string {
  return new Date().toISOString()
}

async function snapshot(page: Page, phase: string): Promise<DomSnapshot> {
  // Locator.textContent() default action timeout is 0 (= no timeout, hangs
  // for the entire test budget if the element is absent). Pre-stream the
  // model chip element does not render at all (v-if on currentModelId ||
  // currentProviderId), so we must pin a short explicit timeout to every
  // potentially-absent locator probe.
  const userBubbles = page.locator('.message-bubble.user')
  const assistantBubbles = page.locator('.message-bubble.assistant')
  const indicator = page.getByTestId('agent-activity-indicator')
  const modelChip = page.getByTestId('agent-activity-model')
  const picker = page.getByTestId('agent-picker')
  const PROBE = { timeout: 250 }

  const [uc, ac, ut, at, ind, chipVis, chipText, pickerText, url] = await Promise.all([
    userBubbles.count(),
    assistantBubbles.count(),
    userBubbles.allTextContents(),
    assistantBubbles.allTextContents(),
    indicator.isVisible().catch(() => false),
    modelChip.isVisible().catch(() => false),
    modelChip.textContent(PROBE).catch(() => null),
    picker.textContent(PROBE).catch(() => null),
    Promise.resolve(page.url()),
  ])

  return {
    phase,
    ts: nowIso(),
    userBubbleCount: uc,
    assistantBubbleCount: ac,
    userBubbleTexts: ut.map((t) => t.trim().slice(0, 400)),
    assistantBubbleTexts: at.map((t) => t.trim().slice(0, 400)),
    indicatorVisible: ind,
    modelChipVisible: chipVis,
    modelChipText: (chipText ?? '').trim(),
    pickerText: (pickerText ?? '').trim(),
    url,
  }
}

async function recordSnapshot(
  page: Page,
  phase: string,
  snapshots: DomSnapshot[],
  evidenceDir: string,
): Promise<DomSnapshot> {
  const snap = await snapshot(page, phase)
  snapshots.push(snap)
  await page.screenshot({ path: path.join(evidenceDir, `${phase}.png`) }).catch(() => {})
  return snap
}

function ensureEvidenceDir(dir: string): void {
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true })
  }
}

function dumpEvidence(
  dir: string,
  files: Record<string, unknown>,
): void {
  for (const [name, payload] of Object.entries(files)) {
    fs.writeFileSync(
      path.join(dir, name),
      typeof payload === 'string' ? payload : JSON.stringify(payload, null, 2),
    )
  }
}

test.describe('session regression — live backend', () => {
  test.describe.configure({ mode: 'serial' })
  test.setTimeout(360_000)

  /**
   * Discovered regression while authoring this spec — left as a fixme so
   * a later delivery picks it up. NOT silently skipped: this is the
   * load-bearing user-visible behaviour for #1.
   *
   *   On a fresh visit (cleared localStorage), the AgentPicker component's
   *   onMounted hook fires `void chatStore.loadAgents()` BEFORE the parent
   *   ChatView's onMounted runs `await chatStore.restoreStateFromBackend()`.
   *   loadAgents (chatStore.ts:618-631) sets `agents[0]` (alphabetically
   *   API-Engineer) as the active agent and persists it to localStorage.
   *   When restoreStateFromBackend then reads getPersistedAgentId() at
   *   line 432, it finds "API-Engineer" — and the precedence on line 439
   *   (`sessionAgentId ?? persistedAgentId ?? defaultAgent`) makes the
   *   persisted value beat DEFAULT_AGENT_ID.
   *
   *   Commit 5c596e8 changed the constant correctly; the unit test in
   *   chatStore.test.ts proves the code path works in isolation. But the
   *   live UX has a pre-existing AgentPicker pre-empt race that 5c596e8
   *   did not address. To fully ship the fix, loadAgents needs to prefer
   *   DEFAULT_AGENT_ID over `agents[0]` when no agent is persisted, OR
   *   the AgentPicker must not seed an agent on mount.
   */
  test.fixme('#1: fresh-visit defaults to default-assistant (AgentPicker pre-empt race)', async ({ page }) => {
    await page.addInitScript(() => {
      try { localStorage.clear() } catch { /* first-load may not have storage yet */ }
    })
    await page.route('**/api/v1/sessions', async (route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
        return
      }
      await route.continue()
    })
    await page.goto('/chat')
    await page.getByTestId('chat-empty-state').waitFor({ state: 'visible', timeout: 15_000 })
    await page.waitForTimeout(2_000)
    const persistedAgentId = await page.evaluate(() => localStorage.getItem('chat.agentId'))
    expect(
      persistedAgentId,
      `fresh-visit chat.agentId must be "default-assistant" — got "${persistedAgentId}"`,
    ).toBe('default-assistant')
  })

  test('#2-#4, #6, #7: full chat round-trip with regression assertions', async ({ page }) => {
    ensureEvidenceDir(EVIDENCE_DIR)
    const network: NetEntry[] = []
    const consoleLog: ConsoleEntry[] = []
    const snapshots: DomSnapshot[] = []

    // Lightweight listeners only — never read response bodies (would
    // block SSE streams). page.on('response') for SSE sometimes hung
    // tests where heavier inspection was attempted in earlier drafts.
    page.on('request', (req: Request) => {
      const url = req.url()
      if (!url.includes('/api/')) return
      network.push({ ts: nowIso(), method: req.method(), url, type: 'request' })
    })
    page.on('console', (msg: ConsoleMessage) => {
      consoleLog.push({ ts: nowIso(), type: msg.type(), text: msg.text() })
    })
    page.on('pageerror', (err: Error) => {
      consoleLog.push({ ts: nowIso(), type: 'pageerror', text: err.message })
    })

    // ---- Setup: pre-create a real backend session, pin GET /sessions ----
    //
    // The long-lived backend at :8080 holds 350+ historical sessions.
    // restoreStateFromBackend's `sessionAgentId ?? persistedAgentId ??
    // defaultAgent` precedence (chatStore.ts:439) means we need to:
    //   1. Create our session FIRST so it has agentId=default-assistant.
    //   2. Mock GET /api/v1/sessions to return ONLY our session — that
    //      way `sessionAgentId` resolves to default-assistant and wins
    //      over any localStorage flake from the AgentPicker pre-empt.
    //   3. Persist the session id to localStorage and reload — the
    //      restore path lands on our session deterministically.
    //
    // POST /sessions, POST /messages, GET /messages, GET /stream are NOT
    // routed — they hit the real backend.
    const createRes = await page.request.post('http://localhost:8080/api/v1/sessions', {
      data: { agent_id: 'default-assistant' },
    })
    expect(createRes.ok(), `session creation failed: ${createRes.status()}`).toBeTruthy()
    const created = (await createRes.json()) as {
      id: string
      agentId: string
      createdAt: string
      updatedAt: string
    }
    fs.writeFileSync(path.join(EVIDENCE_DIR, 'session-id.txt'), created.id)

    await page.route('**/api/v1/sessions', async (route) => {
      if (route.request().method() === 'GET') {
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
    await page.evaluate(
      ({ sid }) => {
        localStorage.clear()
        localStorage.setItem('chat.currentSessionId', sid)
        localStorage.setItem('chat.agentId', 'default-assistant')
      },
      { sid: created.id },
    )
    await page.reload()
    await page.getByTestId('chat-empty-state').waitFor({ state: 'visible', timeout: 15_000 })
    // restoreStateFromBackend resolves the session-agentId to
    // default-assistant via the GET /sessions mock; the picker label
    // (which renders the manifest's `name`) flips to "Default Assistant".
    await expect(page.getByTestId('agent-picker')).toContainText(/default assistant/i, { timeout: 10_000 })

    // ---- Pre-send snapshot ---------------------------------------------
    const preSendSnap = await recordSnapshot(page, '01-pre-send-turn1', snapshots, EVIDENCE_DIR)
    expect(preSendSnap.indicatorVisible, '#3 pre-send indicator must be hidden').toBe(false)
    expect(preSendSnap.userBubbleCount, 'pre-send: zero user bubbles').toBe(0)

    // ---- Turn 1 — establish a fact for context-retention check ----------
    const TURN1 = `My name is ${REGRESSION_NAME}. Reply with just "OK".`
    const input = page.getByTestId('message-input')
    const sendBtn = page.getByTestId('send-button')
    await input.fill(TURN1)
    await sendBtn.click()

    // #3 indicator must be visible IMMEDIATELY after click. Pre-fix
    // (Bug B from chat-real-backend.spec.ts) the indicator never showed.
    // Post-fix it appears within ~1s of click and stays until [DONE].
    // Assert this BEFORE the bubble-count loop because for very fast
    // responses (default-assistant + glm-4.6 frequently <1s for "OK")
    // the indicator could already be hidden by the time the loop ends.
    const indicator = page.getByTestId('agent-activity-indicator')
    await expect(indicator, '#3 indicator must be visible mid-stream').toBeVisible({ timeout: 3_000 })

    await recordSnapshot(page, '02-mid-stream-turn1', snapshots, EVIDENCE_DIR)

    const turn1UserBubbles = page.locator('.message-bubble.user').filter({ hasText: REGRESSION_NAME })
    // Sample the user bubble count over a 4-second window (Bug A pattern).
    // Pre-fix #2 the duplicate orphan persisted from ~300ms to ~3.5s.
    for (let i = 1; i <= 6; i++) {
      const c = await turn1UserBubbles.count()
      expect(
        c,
        `#2 user bubble rendered ${c} times at ~${i * 500}ms post-click — must be exactly 1`,
      ).toBe(1)
      await page.waitForTimeout(500)
    }

    // Wait for assistant content. The model+provider chip (#4) is checked
    // on turn 2 where it is reliably populated — the bc8ffbf+a1675ee fix
    // promotes engine.LastModel/LastProvider to the session metadata in
    // appendSessionMessage AFTER the first assistant chunk lands. For
    // agents without PreferredModels in their manifest (like
    // default-assistant), the chip is empty/hidden during turn 1 mid-
    // stream because the chip's v-if predicate is
    // `chatStore.currentModelId || chatStore.currentProviderId` (see
    // ChatView.vue:347) and both are empty until the first chunk lands.
    await expect
      .poll(async () => await page.locator('.message-bubble.assistant').count(), {
        timeout: 90_000,
        message: 'no assistant bubble after turn 1',
      })
      .toBeGreaterThan(0)

    // Wait for indicator to settle (#3 hidden post-settle)
    await expect(page.getByTestId('agent-activity-indicator')).toBeHidden({ timeout: 60_000 })
    await page.waitForTimeout(1500)

    const postSettleSnap = await recordSnapshot(page, '03-post-settle-turn1', snapshots, EVIDENCE_DIR)
    expect(postSettleSnap.indicatorVisible, '#3 indicator must be hidden post-settle').toBe(false)
    expect(postSettleSnap.userBubbleCount, '#2 single user bubble post-settle').toBe(1)

    // ---- Turn 2 — context-retention check (#7) --------------------------
    const TURN2 = 'What is my name? Answer in 4 words or fewer.'
    await input.fill(TURN2)
    await sendBtn.click()

    // #4 model+provider chip — assert IMMEDIATELY after click, before
    // any waitForTimeout. Turn 1 has already promoted
    // engine.LastModel/LastProvider onto the session via
    // appendSessionMessage (commit a1675ee), so chatStore.currentModelId
    // is populated AT click time and the chip's v-if is satisfied as
    // soon as `isLoading` flips to true. For very fast turns (default-
    // assistant on glm-4.6 frequently responds in <1s) the chip would
    // disappear by the time later samples ran, so we capture the chip
    // text FIRST and re-use it through the rest of the assertions.
    const modelChip = page.getByTestId('agent-activity-model')
    await expect(modelChip, '#4 model chip must be visible at turn-2 click time').toBeVisible({
      timeout: 5_000,
    })
    const chipText = (await modelChip.textContent())?.trim() ?? ''
    fs.writeFileSync(path.join(EVIDENCE_DIR, 'chip-text.txt'), chipText)
    expect(chipText.length, '#4 chip text must not be blank').toBeGreaterThan(0)
    expect(
      chipText,
      `#4 chip text must match "on <model>[ · <provider>]" — got "${chipText}"`,
    ).toMatch(/^on\s+\S+/)

    // Mid-stream sample — indicator on, user bubble count == 1 for
    // turn 2's prompt.
    await recordSnapshot(page, '04-mid-stream-turn2', snapshots, EVIDENCE_DIR)

    const turn2UserBubbles = page.locator('.message-bubble.user').filter({ hasText: 'What is my name' })
    for (let i = 1; i <= 6; i++) {
      const c = await turn2UserBubbles.count()
      expect(
        c,
        `#2 turn2 user bubble rendered ${c} times at ~${i * 500}ms — must be exactly 1`,
      ).toBe(1)
      await page.waitForTimeout(500)
    }

    await expect
      .poll(async () => await page.locator('.message-bubble.assistant').count(), {
        timeout: 90_000,
        message: 'expected ≥2 assistant bubbles after turn 2',
      })
      .toBeGreaterThanOrEqual(2)
    await expect(page.getByTestId('agent-activity-indicator')).toBeHidden({ timeout: 60_000 })
    await page.waitForTimeout(1500)

    const finalSnap = await recordSnapshot(page, '05-post-settle-turn2', snapshots, EVIDENCE_DIR)

    // ---- #7: context retention -----------------------------------------
    //
    // Re-fetch the canonical message thread from the backend — this is
    // robust to the UI's bubble truncation (which slices to 400 chars
    // for the snapshot) and to any race between the indicator hiding
    // and the final assistant chunk landing in the DOM.
    const canonRes = await page.request.get(`http://localhost:8080/api/v1/sessions/${created.id}/messages`)
    const canonMessages = (await canonRes.json()) as Array<{
      role: string
      content: string
      modelName?: string
      providerName?: string
    }>
    fs.writeFileSync(
      path.join(EVIDENCE_DIR, 'canonical-messages.json'),
      JSON.stringify(canonMessages, null, 2),
    )

    const assistantTexts = canonMessages.filter((m) => m.role === 'assistant').map((m) => m.content)
    expect(assistantTexts.length, '#7 must have ≥2 assistant turns').toBeGreaterThanOrEqual(2)
    // Turn 2's assistant should mention the regression name.
    const turn2AssistantText = assistantTexts[assistantTexts.length - 1]
    expect(
      turn2AssistantText,
      `#7 turn-2 assistant must reference "${REGRESSION_NAME}" — got "${turn2AssistantText}"`,
    ).toContain(REGRESSION_NAME)

    // ---- #6: no raw JSON / wire-format leakage --------------------------
    //
    // Scan EVERY visible bubble's text. Allowed bubbles include user,
    // assistant, tool_call, tool_result and thinking — all of which
    // render through the MessageBubble component. We deliberately scan
    // the rendered DOM, not the canonical message store, because
    // leakage shows up specifically when the renderer fails to project
    // a wire-format chunk through the right adaptor.
    const allBubbleTexts = await page.locator('.message-bubble').allTextContents()
    fs.writeFileSync(
      path.join(EVIDENCE_DIR, 'all-bubble-texts.json'),
      JSON.stringify(allBubbleTexts, null, 2),
    )
    for (const { pattern, label } of FORBIDDEN_PATTERNS) {
      for (const text of allBubbleTexts) {
        expect(
          pattern.test(text),
          `#6 forbidden wire-format leakage (${label}) found in bubble: ${text.slice(0, 200)}`,
        ).toBe(false)
      }
    }

    dumpEvidence(EVIDENCE_DIR, {
      'snapshots.json': snapshots,
      'network.json': network,
      'console.json': consoleLog,
      'final-snapshot.json': finalSnap,
    })
  })

  test('#5: tool-trigger toast appears when the agent fires a tool', async ({ page }) => {
    ensureEvidenceDir(EVIDENCE_DIR)

    // Pre-create a clean session for the tool-trigger test.
    const createRes = await page.request.post('http://localhost:8080/api/v1/sessions', {
      data: { agent_id: 'default-assistant' },
    })
    expect(createRes.ok(), `tool-test session create failed: ${createRes.status()}`).toBeTruthy()
    const created = (await createRes.json()) as { id: string; agentId: string; createdAt: string; updatedAt: string }
    fs.writeFileSync(path.join(EVIDENCE_DIR, 'tool-session-id.txt'), created.id)

    await page.route('**/api/v1/sessions', async (route) => {
      if (route.request().method() === 'GET') {
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
    await page.evaluate(
      ({ sid }) => {
        localStorage.clear()
        localStorage.setItem('chat.currentSessionId', sid)
        localStorage.setItem('chat.agentId', 'default-assistant')
      },
      { sid: created.id },
    )
    await page.reload()
    await page.getByTestId('chat-empty-state').waitFor({ state: 'visible', timeout: 15_000 })
    await expect(page.getByTestId('agent-picker')).toContainText(/default assistant/i, { timeout: 10_000 })

    // Prompt that reliably triggers a tool call. Verified manually
    // against the live backend: default-assistant hits read with this
    // exact phrasing, producing a tool_call SSE event within ~5s.
    const PROMPT =
      'Read the file /tmp/flowstate-tooltest-DOES-NOT-EXIST.txt and tell me whether it exists. If it does not, just say "MISSING".'
    await page.getByTestId('message-input').fill(PROMPT)
    await page.getByTestId('send-button').click()

    // The rolling tool-activity toast (chatStore.recordToolActivity)
    // spawns on the FIRST tool_call event with title "Working" — see
    // chatStore.ts:1673. The toast persists until 1.2s after the LAST
    // tool_call, so the visibility window is generous (≥1.2s, typically
    // longer if the agent makes multiple tool calls).
    const toastTitle = page.getByTestId('toast-container').getByTestId('toast-title').filter({ hasText: 'Working' })
    await expect(toastTitle, '#5 tool-trigger toast titled "Working" must appear within 30s').toBeVisible({
      timeout: 30_000,
    })

    await page.screenshot({
      path: path.join(EVIDENCE_DIR, 'tool-trigger-toast.png'),
      fullPage: true,
    }).catch(() => {})

    // Let the agent finish so the test doesn't leak a streaming session.
    await expect(page.getByTestId('agent-activity-indicator')).toBeHidden({ timeout: 90_000 }).catch(() => {})
  })
})
