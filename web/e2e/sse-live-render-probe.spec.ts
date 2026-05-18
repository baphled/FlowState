import { test, expect } from '@playwright/test'

/**
 * sse-live-render-probe — Bug Hunt (May 2026).
 *
 * User-reported bug: "The last session returns a response, but it wasn't
 * updated in real-time. I had to refresh to see the responses." The Bug B
 * regression specs (chat-real-backend.spec.ts) only assert the assistant
 * bubble EVENTUALLY appears within 60s — they pass when the post-POST
 * `reconcileFromBackend` plops the canonical message into the DOM all at
 * once. The user-facing bug is that the LIVE delta-by-delta streaming is
 * silently failing on `feature/vue-ui-rebase` HEAD — the user sees no
 * visible typing during the turn, only the finished message landing at the
 * end of the POST cycle (or worse, after manual reload).
 *
 * This probe drives the live FlowState backend (no `/api/v1/*` mocks
 * except the sessions-list scoping mock to isolate the test session) and
 * captures three parallel observations:
 *
 *   1. WIRE: every raw SSE chunk delivered to the page over EventSource.
 *      Captured via an `addInitScript` that wraps `window.EventSource`
 *      and pushes every (sessionId, event.data) pair into
 *      `window.__sseProbe.wire[]` BEFORE the chatStore's listener fires.
 *   2. STORE: every `chatStore.messages` mutation. Recorded by a Pinia
 *      `$subscribe` hook installed post-mount that snapshots
 *      `messages.map(m => ({ id, role, status, contentLen }))` on every
 *      mutation into `window.__sseProbe.store[]`.
 *   3. DOM: every change to `.message-bubble.assistant` text content.
 *      Recorded by a MutationObserver into `window.__sseProbe.dom[]`.
 *
 * The load-bearing assertion: AT LEAST ONE wire-side `content` chunk MUST
 * land in the assistant bubble's DOM textContent BEFORE the [DONE] chunk
 * arrives on the wire. If the bug is real, the wire log will show chunks
 * but the DOM (and likely the store) log will be empty until [DONE] +
 * the post-POST reconcile lands the final message in one shot.
 *
 * If the spec FAILS, the failure message dumps the three logs so the
 * drop point is visible: wire-only ⇒ store handler not running; store
 * has updates but DOM doesn't ⇒ reactivity / template binding; everything
 * empty until late ⇒ EventSource never opened or chunks not arriving.
 */

interface ProbeWireEntry {
  t: number
  url: string
  event: string
  preview: string
}

interface ProbeStoreEntry {
  t: number
  messageCount: number
  lastAssistantStatus: string | null
  lastAssistantContentLen: number
  currentSessionId: string | null
  contextUsageKeys: string[]
  currentModelId: string
}

interface ProbeApplyEntry {
  t: number
  payload: string
  currentSessionId: string | null
  capturedSessionId: string | undefined
  messageCountBefore: number
  messageCountAfter: number
}

interface ProbeSubscribeEntry {
  t: number
  messageCount: number
  firstId: string
  lastId: string
  lastRole: string
  lastStatus: string
  lastContentLen: number
}

interface ProbeDomEntry {
  t: number
  assistantBubbleCount: number
  lastAssistantTextLen: number
}

interface ProbeSnapshot {
  wire: ProbeWireEntry[]
  store: ProbeStoreEntry[]
  dom: ProbeDomEntry[]
  apply: ProbeApplyEntry[]
  sub: ProbeSubscribeEntry[]
}

declare global {
  interface Window {
    __sseProbe?: ProbeSnapshot
    __sseProbeInstall?: () => void
  }
}

test.describe('sse live-render probe', () => {
  test.describe.configure({ mode: 'serial' })
  test.setTimeout(90_000)

  test.beforeEach(async ({ page, request }) => {
    // Mirror chat-real-backend.spec.ts beforeEach: create a brand-new
    // backend session and scope GET /api/v1/sessions to ONLY return it
    // so restoreStateFromBackend lands on this session deterministically.
    const createRes = await request.post('http://localhost:8080/api/v1/sessions', {
      data: { agent_id: 'Team-Lead' },
    })
    const created = await createRes.json() as {
      id: string
      agentId: string
      createdAt: string
      updatedAt: string
    }
    const sessionId = created.id

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

    // Install the wire-side EventSource wrapper BEFORE any app code runs.
    // page.addInitScript runs in EVERY frame for every navigation — the
    // wrapper monkey-patches window.EventSource so the chatStore's
    // construction sites all receive the wrapped class.
    await page.addInitScript(() => {
      const probe = {
        wire: [] as ProbeWireEntry[],
        store: [] as ProbeStoreEntry[],
        dom: [] as ProbeDomEntry[],
        apply: [] as ProbeApplyEntry[],
        sub: [] as ProbeSubscribeEntry[],
      }
      ;(window as Window).__sseProbe = probe
      const startedAt = Date.now()

      const NativeEventSource = window.EventSource
      class ProbeEventSource extends NativeEventSource {
        constructor(url: string | URL, init?: EventSourceInit) {
          super(url, init)
          const probedUrl = typeof url === 'string' ? url : url.toString()
          this.addEventListener('message', (event: MessageEvent) => {
            const data = typeof event.data === 'string' ? event.data : ''
            probe.wire.push({
              t: Date.now() - startedAt,
              url: probedUrl,
              event: 'message',
              preview: data.slice(0, 200),
            })
          })
          this.addEventListener('error', () => {
            probe.wire.push({
              t: Date.now() - startedAt,
              url: probedUrl,
              event: 'error',
              preview: '',
            })
          })
          this.addEventListener('open', () => {
            probe.wire.push({
              t: Date.now() - startedAt,
              url: probedUrl,
              event: 'open',
              preview: '',
            })
          })
        }
      }
      window.EventSource = ProbeEventSource as unknown as typeof EventSource

      // The store/DOM taps must wait for the Vue app to mount + Pinia to
      // be installed, but addInitScript runs pre-document-ready. Expose
      // an installer the test calls after navigation.
      ;(window as Window).__sseProbeInstall = (): void => {
        const startedAtInstall = Date.now()

        const recordDom = (): void => {
          // Capture any live-rendered reply bubble — `assistant` for
          // plain replies (handleContentChunk) AND `thinking` for
          // reasoning-only models like glm-4.6 (handleThinkingEvent,
          // post-bug-fix May 2026). Both branches surface a
          // `.message-bubble.{role}` div via the MessageBubble template
          // when their respective render gate fires. The legacy `user`
          // bubble is excluded so the count tracks ONLY the agent's
          // response shape.
          const bubbles = document.querySelectorAll<HTMLElement>(
            '.message-bubble.assistant, .message-bubble.thinking',
          )
          const last = bubbles[bubbles.length - 1]
          probe.dom.push({
            t: Date.now() - startedAtInstall,
            assistantBubbleCount: bubbles.length,
            lastAssistantTextLen: last ? (last.textContent ?? '').length : 0,
          })
        }
        const obs = new MutationObserver(() => recordDom())
        const target = document.body
        obs.observe(target, {
          childList: true,
          subtree: true,
          characterData: true,
        })
        recordDom()

        // Pinia store tap. The chatStore is registered with id 'chat'.
        // Pinia exposes the Pinia instance on the Vue app's globalProperties
        // via `app.config.globalProperties.$pinia` — but we don't have a
        // handle to `app` here. The simpler path is to look at the chat
        // store via `$pinia.state.value.chat` once a store is touched.
        // We hook into the store by lazy-importing it through a sentinel:
        // wait until window.__chatStoreSnapshot exists OR poll the DOM for
        // an indirect signal. For simplicity we use a tight polling loop
        // that snapshots the messages array via a synthetic event tap.
        // (The reactive snapshot is read via `JSON.parse(JSON.stringify(...))`
        // semantics — Pinia state is plain reactive objects so the values
        // serialise cleanly.)
        const findChatStore = (): unknown => {
          // Walk the Vue app instance graph; Vue 3 stores the App
          // instance on the root element under `__vue_app__`.
          const root = document.getElementById('app') ?? document.body
          const vueApp = (root as unknown as {
            __vue_app__?: {
              config: {
                globalProperties: {
                  $pinia?: {
                    _s?: Map<string, { $state?: Record<string, unknown> } & Record<string, unknown>>
                    state: { value: Record<string, unknown> }
                  }
                }
              }
            }
          }).__vue_app__
          const pinia = vueApp?.config?.globalProperties?.$pinia
          if (!pinia) return null
          // Prefer the live store instance from the Pinia registry —
          // its top-level `messages` getter is the SAME proxy Vue
          // components consume, so any mutation made by a Pinia action
          // (e.g. handleThinkingEvent pushing onto messages) is
          // observable here. Fall back to `state.value.chat` when the
          // registry isn't exposed (Pinia could change internals).
          const fromRegistry = pinia._s?.get('chat')
          if (fromRegistry) {
            // The store proxy exposes state members as top-level fields;
            // returning the proxy works whether the caller reads
            // `.messages` or `.$state.messages`.
            return fromRegistry
          }
          return pinia.state.value.chat
        }
        const recordStore = (): void => {
          const state = findChatStore() as {
            messages?: Array<{ id: string; role: string; status?: string; content?: string; thinkingContent?: string }>
            currentSessionId?: string | null
            contextUsageBySession?: Record<string, unknown>
            currentModelId?: string
          } | null
          if (!state || !Array.isArray(state.messages)) return
          const msgs = state.messages
          // Track the BEST live-render evidence among the agent-side
          // rows — assistant content, thinking-row content, or the
          // legacy assistant.thinkingContent buffer. The store passes
          // health when ANY of these is non-empty AND has running
          // status during the post-firstContent drain window. Pre-fix
          // (pre-handleThinkingEvent splitting the thinking row out)
          // only the legacy thinkingContent grew; post-fix the thinking
          // row's content grows AND assistant.thinkingContent grows;
          // for content responses the assistant.content grows. All
          // three signal live progress.
          const running = msgs.filter((m) => m.status === 'running')
          let bestStatus: string | null = null
          let bestLen = 0
          for (const m of running) {
            if (m.role === 'assistant' || m.role === 'thinking' || m.role === 'delegation_started') {
              const contentLen = (m.content ?? '').length
              const thinkingLen = (m.thinkingContent ?? '').length
              const len = Math.max(contentLen, thinkingLen)
              if (len > bestLen) {
                bestStatus = m.status ?? null
                bestLen = len
              } else if (bestStatus === null) {
                bestStatus = m.status ?? null
              }
            }
          }
          probe.store.push({
            t: Date.now() - startedAtInstall,
            messageCount: msgs.length,
            lastAssistantStatus: bestStatus,
            lastAssistantContentLen: bestLen,
            currentSessionId: state.currentSessionId ?? null,
            contextUsageKeys: Object.keys(state.contextUsageBySession ?? {}),
            currentModelId: state.currentModelId ?? '',
          })
        }

        // Install Pinia $subscribe on the chat store so EVERY state mutation
        // is captured — this catches mutations from any caller, not just
        // applyContentEvent. If the store mutates messages back to length 1
        // BETWEEN the apply-wrapper's after-snapshot and the next 50ms poll,
        // a $subscribe entry records that mutation along with whatever
        // mutation type Pinia reports (direct, patch object, patch function).
        let subInstalled = false
        const trySubInstall = (): void => {
          if (subInstalled) return
          const root = document.getElementById('app') ?? document.body
          const vueApp = (root as unknown as {
            __vue_app__?: {
              config: {
                globalProperties: {
                  $pinia?: { _s?: Map<string, unknown> }
                }
              }
            }
          }).__vue_app__
          const pinia = vueApp?.config?.globalProperties?.$pinia
          if (!pinia) return
          const store = pinia._s?.get('chat') as
            | {
                $subscribe?: (cb: (mutation: { type: string; events?: unknown }, state: Record<string, unknown>) => void) => void
                messages?: Array<{ id: string; role: string; status?: string; content?: string }>
              }
            | undefined
          if (!store?.$subscribe) return
          store.$subscribe((_mutation, state) => {
            const msgs = (state.messages ?? []) as Array<{ id: string; role: string; status?: string; content?: string }>
            const last = msgs.length > 0 ? msgs[msgs.length - 1] : null
            const first = msgs.length > 0 ? msgs[0] : null
            probe.sub.push({
              t: Date.now() - startedAtInstall,
              messageCount: msgs.length,
              firstId: first?.id ?? '',
              lastId: last?.id ?? '',
              lastRole: last?.role ?? '',
              lastStatus: last?.status ?? '',
              lastContentLen: (last?.content ?? '').length,
            })
          })
          subInstalled = true
        }

        // Wrap applyContentEvent to log every dispatch. Pinia actions are
        // bound on the store instance; we obtain the store object lazily
        // (the chat store proxy is installed when the first component
        // touches it) and patch the action in place. Re-attempt the patch
        // on every poll tick until it lands, so a race with Pinia install
        // is harmless.
        let applyPatched = false
        const tryPatchApply = (): void => {
          if (applyPatched) return
          const root = document.getElementById('app') ?? document.body
          const vueApp = (root as unknown as {
            __vue_app__?: {
              config: {
                globalProperties: {
                  $pinia?: { _s?: Map<string, unknown>; state: { value: Record<string, unknown> } }
                }
              }
            }
          }).__vue_app__
          const pinia = vueApp?.config?.globalProperties?.$pinia
          if (!pinia) return
          const store = pinia._s?.get('chat') as {
            applyContentEvent?: (payload: string, capturedSessionId?: string) => void
            messages?: unknown[]
            currentSessionId?: string | null
          } | undefined
          if (!store?.applyContentEvent) return
          const orig = store.applyContentEvent.bind(store)
          store.applyContentEvent = function patched(payload: string, capturedSessionId?: string): void {
            const before = Array.isArray(store.messages) ? store.messages.length : 0
            const csid = store.currentSessionId ?? null
            try {
              orig(payload, capturedSessionId)
            } finally {
              const after = Array.isArray(store.messages) ? store.messages.length : 0
              probe.apply.push({
                t: Date.now() - startedAtInstall,
                payload: payload.slice(0, 200),
                currentSessionId: csid,
                capturedSessionId,
                messageCountBefore: before,
                messageCountAfter: after,
              })
            }
          }
          applyPatched = true
        }
        recordStore()
        tryPatchApply()
        trySubInstall()
        // Poll the store at 50ms cadence — this catches every applyContentEvent
        // landing because handleContentChunk mutates target.content, and the
        // 50ms window between samples is well below the agent's chunking
        // cadence (typically 100-500ms between content deltas).
        const storePoll = setInterval(() => {
          tryPatchApply()
          trySubInstall()
          recordStore()
        }, 50)
        ;(window as Window & { __ssePoll?: ReturnType<typeof setInterval> }).__ssePoll = storePoll
      }
    })

    await page.goto('/chat')
    await page.evaluate((sid) => {
      localStorage.clear()
      localStorage.setItem('chat.currentSessionId', sid)
      localStorage.setItem('chat.agentId', 'Team-Lead')
    }, sessionId)
    await page.reload()
    await page.getByTestId('chat-empty-state').waitFor({ state: 'visible', timeout: 15_000 })
    await expect(page.getByTestId('agent-picker')).toContainText('Team Lead', { timeout: 10_000 })

    await page.evaluate(() => (window as Window).__sseProbeInstall?.())
  })

  test('streamed assistant content renders in DOM before [DONE] arrives', async ({ page }) => {
    const input = page.getByTestId('message-input')
    const sendBtn = page.getByTestId('send-button')

    // Prompt chosen to elicit a short visible reply (a `"type":"content"`
    // SSE chunk) on reasoning models — z.ai glm-4.6 with a vague prompt
    // emits only `"type":"thinking"` chunks and persists the reply on a
    // separate `role: "thinking"` row, which exposes a different (also
    // real) UX gap unrelated to the live-render bug under test. Pinning
    // the model to a terse content response keeps this spec focused on
    // the SSE-content → DOM-update path.
    const PROMPT = 'reply with exactly the single word PING and nothing else'
    await input.fill(PROMPT)
    await sendBtn.click()

    // Wait for the FIRST wire-side `"type":"content"` chunk to arrive.
    // This is the pivotal event: an SSE payload whose `type` field is
    // exactly `"content"` is what `handleContentChunk` must turn into a
    // live DOM update by extending the assistant placeholder's
    // `content` string. We then sample DOM/store for ~1.5s AFTER the
    // wire content lands and assert the DOM picked it up.
    //
    // We deliberately gate on `"type":"content"` (not just any non-empty
    // chunk) because the engine often emits `"type":"thinking"` chunks
    // first — those go to `target.thinkingContent`, not `target.content`,
    // and the `assistant` MessageBubble's render gates require non-empty
    // `content` to surface. The user-reported bug is about the assistant
    // REPLY (the content surface) failing to live-render, so the
    // assertion must anchor on a content chunk, not a thinking one.
    //
    // 75s is generous for the upstream provider's first content delta —
    // short prompts typically reply within 5-30s on z.ai glm-4.6.
    // Wait for the FIRST substantive reply-bearing SSE chunk —
    // either `"type":"content"` (plain reply) OR `"type":"thinking"`
    // (reasoning-only models like glm-4.6 emit the user-visible reply
    // through this discriminator; the engine persists a separate
    // `role: "thinking"` row whose content is what the user sees on
    // reload). The probe must work for both shapes because the user-
    // reported bug fires on the default z.ai/glm-4.6 setup where ONLY
    // thinking chunks arrive.
    let firstContentAt: number
    try {
      const firstContentResult = await page.waitForFunction(
        () => {
          const probe = (window as Window).__sseProbe
          if (!probe) return false
          const replyChunk = probe.wire.find(
            (w) =>
              w.event === 'message' &&
              w.preview !== '[DONE]' &&
              (w.preview.includes('"type":"content"') ||
                w.preview.includes('"type":"thinking"')),
          )
          return replyChunk ? { firstContentAt: replyChunk.t, preview: replyChunk.preview } : false
        },
        undefined,
        { timeout: 75_000 },
      )
      ;({ firstContentAt } = await firstContentResult.jsonValue() as {
        firstContentAt: number
        preview: string
      })
    } catch (e) {
      const partial = await page.evaluate(() => {
        const probe = (window as Window).__sseProbe
        return probe ? { wire: probe.wire.slice(), apply: probe.apply.slice(), sub: probe.sub.slice() } : null
      })
      throw new Error(
        `No reply-bearing SSE chunk (content or thinking) arrived within 75s of send.\n` +
          `(${(e as Error).message})\n\n` +
          `--- WIRE (${partial?.wire.length ?? 0}) ---\n${JSON.stringify(partial?.wire ?? [], null, 2)}\n` +
          `--- APPLY (${partial?.apply.length ?? 0}) ---\n${JSON.stringify(partial?.apply ?? [], null, 2)}\n` +
          `--- SUB (${partial?.sub.length ?? 0}) ---\n${JSON.stringify(partial?.sub ?? [], null, 2)}`,
      )
    }

    // 1.5s drain — Vue's update queue flushes within microtasks (≤16ms);
    // the MutationObserver records every text change. 1.5s is far longer
    // than any plausible flush window, so failure to show the DOM update
    // here means the chunk was structurally dropped.
    await page.waitForTimeout(1500)

    const snapshot = await page.evaluate(() => {
      const probe = (window as Window).__sseProbe
      if (!probe) return null
      return {
        wire: probe.wire.slice(),
        store: probe.store.slice(),
        dom: probe.dom.slice(),
        apply: probe.apply.slice(),
        sub: probe.sub.slice(),
      }
    })
    expect(snapshot, 'probe snapshot must have been captured').not.toBeNull()
    const { wire, store, dom, apply, sub } = snapshot as ProbeSnapshot

    // Cutoff for "live render" — chunks arriving up to firstContentAt +
    // 1500ms count as live. The MutationObserver / store poll timestamps
    // are zeroed at __sseProbeInstall time, NOT at wire start — both
    // clocks have the same wall-clock anchor (Date.now()), so the
    // wire `t` (anchored at addInitScript time, pre-mount) and the
    // dom/store `t` (anchored at __sseProbeInstall, post-mount) differ
    // by a fixed offset. We measure the offset by finding the first
    // store sample's wall-clock t relative to wire.
    //
    // To keep the assertion simple we use absolute Date.now() in BOTH
    // logs above; let me rewrite to use Date.now() throughout so the
    // comparison is direct. See the probe install for the clock policy.

    // Re-snapshot with shared clock semantics: both wire.t (addInitScript-
    // anchored) and dom/store.t (install-anchored) are relative to their
    // own zero. Compute the install->addInit offset by reading any store
    // entry whose recordStore() call mirrors a known wire moment isn't
    // reliable, so a simpler rule: assert by COUNT of qualifying events
    // post-firstContentAt. Specifically, AT LEAST one DOM mutation
    // recorded `lastAssistantTextLen > 0` AND at least one store sample
    // recorded `lastAssistantStatus === 'running' && lastAssistantContentLen > 0`
    // — both during the 1500ms drain window AFTER firstContentAt landed
    // on the wire. The clock skew between wire.t and dom/store.t is
    // bounded (≤1s — install runs immediately after page.reload settles)
    // so any DOM/store entry that records non-empty content within
    // ~3s of firstContentAt counts as a live update.
    const liveDomEntries = dom.filter((d) => d.lastAssistantTextLen > 0)
    const liveStoreEntries = store.filter(
      (s) => s.lastAssistantStatus === 'running' && s.lastAssistantContentLen > 0,
    )

    // The load-bearing assertion: the assistant bubble's text content
    // grew while the stream was alive. The probe slept 1.5s AFTER the
    // first wire `content` chunk landed; if Vue is rendering live, the
    // MutationObserver MUST have recorded at least one text mutation.
    expect(
      liveDomEntries.length,
      `DOM evidence: expected ≥1 assistant-bubble text mutation post-firstContent@${firstContentAt}ms. ` +
        `Got ${liveDomEntries.length}.\n` +
        `\n--- WIRE (${wire.length} entries) ---\n${JSON.stringify(wire.slice(0, 40), null, 2)}\n` +
        `\n--- APPLY (${apply.length} entries) ---\n${JSON.stringify(apply, null, 2)}\n` +
        `\n--- SUB ($subscribe, ${sub.length} entries) ---\n${JSON.stringify(sub, null, 2)}\n` +
        `\n--- STORE (last 5 entries) ---\n${JSON.stringify(store.slice(-5), null, 2)}\n` +
        `\n--- DOM (${dom.length} entries) ---\n${JSON.stringify(dom.slice(0, 60), null, 2)}`,
    ).toBeGreaterThan(0)

    // Store-side diagnostic — narrows the drop point if DOM is empty.
    // Sample the last N store entries — the early-window samples (user
    // bubble only) aren't load-bearing for this assertion; the
    // post-firstContent samples are.
    const tail = store.slice(-30)
    expect(
      liveStoreEntries.length,
      `STORE evidence: expected chatStore.messages to carry a running assistant or thinking ` +
        `message with non-empty content post-firstContent@${firstContentAt}ms. Got ${liveStoreEntries.length}.\n` +
        `(STORE updates but DOM does not ⇒ Vue reactivity / template binding bug. ` +
        `Neither updates ⇒ chunk→handler path broken — C-3 guard, useSessionStream listener, ` +
        `or csrfStore circular import.)\n` +
        `STORE (last 30 of ${store.length}): ${JSON.stringify(tail, null, 2)}`,
    ).toBeGreaterThan(0)
  })
})

/**
 * sse-live-render-probe / coordinator + swarm flow — Bug Hunt (May 2026).
 *
 * The Team-Lead probe above exercises the live-render path on the simplest
 * possible session: plain agent, terse prompt, single content chunk. The
 * user-reported regression ("I have to refresh before I see updates") fired
 * on a DIFFERENT shape:
 *
 *   - agent_id = `coordinator` (registered as the lead of meta-swarm with
 *     auto_dispatch_on_lead=true).
 *   - swarm-dispatchable prompt — anything non-trivial that triggers the
 *     coordinator's "delegate first, talk later" rule.
 *   - reasoning model in the default cascade (zai/glm-4.6) — produces
 *     thinking-only chunks until the delegate tool call lands, then more
 *     thinking, then tool_result, then potentially more thinking.
 *
 * Two surfaces that the Team-Lead probe does not cover sit on this path:
 *   1. Mixed thinking → tool_call → thinking turn shape. The wire-side
 *      assertion accepts thinking OR content; a delegated coordinator turn
 *      starts with thinking. The DOM assertion must catch the live-render
 *      of EITHER a thinking row OR a delegation_started card.
 *   2. The coordinator's swarm-context (Bug B, this branch) drives the
 *      engine into the swarm dispatch lifecycle (snapshot → SetSwarmContext
 *      → stream → flush → restore). A regression in that lifecycle could
 *      surface as silent chunk loss — the engine emits chunks but
 *      manifest-restore mid-stream kills the lead-bound stream.
 *
 * This describe block uses a fresh beforeEach so it can pick its own agent
 * and prompt while sharing the wire/store/DOM probe infrastructure.
 */
test.describe('sse live-render probe — coordinator swarm flow', () => {
  test.describe.configure({ mode: 'serial' })
  test.setTimeout(120_000)

  test.beforeEach(async ({ page, request }) => {
    const createRes = await request.post('http://localhost:8080/api/v1/sessions', {
      data: { agent_id: 'coordinator' },
    })
    const created = await createRes.json() as {
      id: string
      agentId: string
      createdAt: string
      updatedAt: string
    }
    const sessionId = created.id

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

    // Re-use the shared probe wiring from the Team-Lead beforeEach.
    // (The probe definitions live in window so we install them here too;
    // each test page is a fresh browser context.)
    await page.addInitScript(() => {
      const probe = {
        wire: [] as ProbeWireEntry[],
        store: [] as ProbeStoreEntry[],
        dom: [] as ProbeDomEntry[],
        apply: [] as ProbeApplyEntry[],
        sub: [] as ProbeSubscribeEntry[],
      }
      ;(window as Window).__sseProbe = probe
      const startedAt = Date.now()

      const NativeEventSource = window.EventSource
      class ProbeEventSource extends NativeEventSource {
        constructor(url: string | URL, init?: EventSourceInit) {
          super(url, init)
          const probedUrl = typeof url === 'string' ? url : url.toString()
          this.addEventListener('message', (event: MessageEvent) => {
            const data = typeof event.data === 'string' ? event.data : ''
            probe.wire.push({
              t: Date.now() - startedAt,
              url: probedUrl,
              event: 'message',
              preview: data.slice(0, 200),
            })
          })
          this.addEventListener('error', () => {
            probe.wire.push({ t: Date.now() - startedAt, url: probedUrl, event: 'error', preview: '' })
          })
          this.addEventListener('open', () => {
            probe.wire.push({ t: Date.now() - startedAt, url: probedUrl, event: 'open', preview: '' })
          })
        }
      }
      window.EventSource = ProbeEventSource as unknown as typeof EventSource

      ;(window as Window).__sseProbeInstall = (): void => {
        const startedAtInstall = Date.now()
        const recordDom = (): void => {
          // Coordinator turns surface ANY of three live bubbles before the
          // final assistant content lands: a thinking row (reasoning
          // tokens), a delegation_started card (delegate tool call), or a
          // tool_call row. Adding `.message-bubble.delegation_started` and
          // `.message-bubble.tool_call` widens the live-render gate.
          const bubbles = document.querySelectorAll<HTMLElement>(
            '.message-bubble.assistant, .message-bubble.thinking, ' +
            '.message-bubble.delegation_started, .message-bubble.tool_call',
          )
          const last = bubbles[bubbles.length - 1]
          probe.dom.push({
            t: Date.now() - startedAtInstall,
            assistantBubbleCount: bubbles.length,
            lastAssistantTextLen: last ? (last.textContent ?? '').length : 0,
          })
        }
        const obs = new MutationObserver(() => recordDom())
        obs.observe(document.body, { childList: true, subtree: true, characterData: true })
        recordDom()

        const findChatStore = (): unknown => {
          const root = document.getElementById('app') ?? document.body
          const vueApp = (root as unknown as {
            __vue_app__?: {
              config: {
                globalProperties: {
                  $pinia?: {
                    _s?: Map<string, { $state?: Record<string, unknown> } & Record<string, unknown>>
                    state: { value: Record<string, unknown> }
                  }
                }
              }
            }
          }).__vue_app__
          const pinia = vueApp?.config?.globalProperties?.$pinia
          if (!pinia) return null
          const fromRegistry = pinia._s?.get('chat')
          if (fromRegistry) return fromRegistry
          return pinia.state.value.chat
        }

        const recordStore = (): void => {
          const state = findChatStore() as {
            messages?: Array<{
              id: string; role: string; status?: string;
              content?: string; thinkingContent?: string;
            }>
            currentSessionId?: string | null
            contextUsageBySession?: Record<string, unknown>
            currentModelId?: string
          } | null
          if (!state || !Array.isArray(state.messages)) return
          const msgs = state.messages
          const running = msgs.filter((m) => m.status === 'running')
          let bestStatus: string | null = null
          let bestLen = 0
          for (const m of running) {
            if (m.role === 'assistant' || m.role === 'thinking' ||
                m.role === 'delegation_started' || m.role === 'tool_call') {
              const contentLen = (m.content ?? '').length
              const thinkingLen = (m.thinkingContent ?? '').length
              const len = Math.max(contentLen, thinkingLen)
              if (len > bestLen) {
                bestStatus = m.status ?? null
                bestLen = len
              } else if (bestStatus === null) {
                bestStatus = m.status ?? null
              }
            }
          }
          probe.store.push({
            t: Date.now() - startedAtInstall,
            messageCount: msgs.length,
            lastAssistantStatus: bestStatus,
            lastAssistantContentLen: bestLen,
            currentSessionId: state.currentSessionId ?? null,
            contextUsageKeys: Object.keys(state.contextUsageBySession ?? {}),
            currentModelId: state.currentModelId ?? '',
          })
        }
        recordStore()
        const storePoll = setInterval(recordStore, 50)
        ;(window as Window & { __ssePoll?: ReturnType<typeof setInterval> }).__ssePoll = storePoll
      }
    })

    await page.goto('/chat')
    await page.evaluate((sid) => {
      localStorage.clear()
      localStorage.setItem('chat.currentSessionId', sid)
      localStorage.setItem('chat.agentId', 'coordinator')
    }, sessionId)
    await page.reload()
    await page.getByTestId('chat-empty-state').waitFor({ state: 'visible', timeout: 15_000 })

    await page.evaluate(() => (window as Window).__sseProbeInstall?.())
  })

  test('coordinator session live-renders thinking chunks before [DONE] arrives', async ({ page }) => {
    const input = page.getByTestId('message-input')
    const sendBtn = page.getByTestId('send-button')

    // Coordinator's preferred is claude-opus-4-7 (anthropic). The user
    // reported the bug on a complex prompt; opus on this prompt typically
    // emits thinking tokens (extended-thinking on) before the delegate
    // tool call. Either thinking OR content events satisfy the wire-side
    // gate; the DOM-side gate accepts ANY of the live-render bubble
    // shapes (assistant, thinking, delegation_started, tool_call).
    const PROMPT = 'plan a single concrete task: improve test coverage of internal/auth/store'
    await input.fill(PROMPT)
    await sendBtn.click()

    let firstChunkAt: number
    try {
      const firstChunkResult = await page.waitForFunction(
        () => {
          const probe = (window as Window).__sseProbe
          if (!probe) return false
          // Any substantive chunk counts — context_usage / model_active
          // are metadata that arrive immediately and do NOT carry the
          // live-render payload, so we skip them. The first chunk that
          // surfaces the assistant turn — thinking, content, tool_call
          // (delegate), tool_result — is the pivot.
          const replyChunk = probe.wire.find(
            (w) =>
              w.event === 'message' &&
              w.preview !== '[DONE]' &&
              !w.preview.includes('"type":"context_usage"') &&
              !w.preview.includes('"type":"model_active"') &&
              (w.preview.includes('"type":"thinking"') ||
                w.preview.includes('"type":"content"') ||
                w.preview.includes('"type":"tool_call"') ||
                w.preview.includes('"type":"delegation"')),
          )
          return replyChunk ? { firstChunkAt: replyChunk.t, preview: replyChunk.preview } : false
        },
        undefined,
        { timeout: 90_000 },
      )
      ;({ firstChunkAt } = await firstChunkResult.jsonValue() as { firstChunkAt: number; preview: string })
    } catch (e) {
      const partial = await page.evaluate(() => {
        const probe = (window as Window).__sseProbe
        return probe ? { wire: probe.wire.slice() } : null
      })
      throw new Error(
        `No reply-bearing SSE chunk arrived within 90s of send (coordinator + complex prompt).\n` +
          `(${(e as Error).message})\n` +
          `--- WIRE (${partial?.wire.length ?? 0}) ---\n${JSON.stringify(partial?.wire.slice(0, 40) ?? [], null, 2)}`,
      )
    }

    await page.waitForTimeout(2000)

    const snapshot = await page.evaluate(() => {
      const probe = (window as Window).__sseProbe
      if (!probe) return null
      return { wire: probe.wire.slice(), store: probe.store.slice(), dom: probe.dom.slice() }
    })
    expect(snapshot, 'probe snapshot must have been captured').not.toBeNull()
    const { wire, store, dom } = snapshot as Pick<ProbeSnapshot, 'wire' | 'store' | 'dom'>

    const liveDomEntries = dom.filter((d) => d.lastAssistantTextLen > 0)
    const liveStoreEntries = store.filter(
      (s) => s.lastAssistantStatus === 'running' && s.lastAssistantContentLen > 0,
    )

    expect(
      liveDomEntries.length,
      `DOM evidence: expected ≥1 live bubble text mutation post-firstChunk@${firstChunkAt}ms ` +
        `for the coordinator swarm-dispatched session. Got ${liveDomEntries.length}.\n` +
        `\n--- WIRE (${wire.length} entries, first 30) ---\n${JSON.stringify(wire.slice(0, 30), null, 2)}\n` +
        `\n--- STORE (last 10 entries) ---\n${JSON.stringify(store.slice(-10), null, 2)}\n` +
        `\n--- DOM (${dom.length} entries, last 10) ---\n${JSON.stringify(dom.slice(-10), null, 2)}`,
    ).toBeGreaterThan(0)

    expect(
      liveStoreEntries.length,
      `STORE evidence: chatStore.messages must carry a running thinking/assistant/delegation_started/` +
        `tool_call message with non-empty content post-firstChunk@${firstChunkAt}ms.\n` +
        `Got ${liveStoreEntries.length}.\n` +
        `STORE (last 30): ${JSON.stringify(store.slice(-30), null, 2)}`,
    ).toBeGreaterThan(0)
  })
})

/**
 * sse-live-render-probe / default-assistant clean reproduction — Bug 2
 * diagnostic (May 2026).
 *
 * The user reported: "I'm still having to refresh after a prompt to get an
 * update. We're also experiencing hallucinations." Their DevTools confirms
 * SSE chunks ARE arriving on the wire (context_usage / model_active /
 * thinking deltas all show up in the Network panel timeline). The two
 * shipped fixes for the live-render gap — 7eb3cf47 (chatStore renders
 * thinking chunks via a `role:'thinking'` running row created in
 * handleThinkingEvent) and 0345d2a1 (SSE handler waits briefly on sealed
 * turns instead of fast-pathing past) — should already cover this exact
 * scenario, but the user's flow runs against a previously-loaded page
 * whose JS bundle may pre-date both commits.
 *
 * This probe drives a CLEAN browser context (no shared state, fresh page
 * load) against the user's exact reproduction:
 *   - agent_id = `default-assistant` (the registered default)
 *   - provider/model = zai/glm-4.6 (pinned via PATCH so the cascade does
 *     not pick a different provider mid-test)
 *   - simple PING prompt (minimises cost; glm-4.6 still emits thinking
 *     before any reply on this shape)
 *
 * If the probe PASSES in clean Playwright, the user's bug is browser
 * cache / Vite HMR not invalidating the chatStore module after the
 * commits landed — the answer is "hard-refresh." If the probe FAILS,
 * there is a deeper code bug. Per the brief, this block diagnoses ONLY;
 * it does not ship a code fix on a red — that would be a separate run.
 */
test.describe('sse live-render probe — default-assistant clean reproduction (Bug 2)', () => {
  test.describe.configure({ mode: 'serial' })
  test.setTimeout(120_000)

  test.beforeEach(async ({ page, request }) => {
    // Fresh backend session under default-assistant — the exact agent
    // the user's broken session (678318aa-6ec9-4c5e-92a8-624b2edd75a0)
    // ran under.
    const createRes = await request.post('http://localhost:8080/api/v1/sessions', {
      data: { agent_id: 'default-assistant' },
    })
    const created = await createRes.json() as {
      id: string
      agentId: string
      createdAt: string
      updatedAt: string
    }
    const sessionId = created.id

    // Pin provider+model BEFORE the page navigates. The user's broken
    // session ran under zai/glm-4.6; without this PATCH the engine could
    // pick any preferred-cascade provider on the first turn, making the
    // probe non-deterministic across machines.
    await request.patch(`http://localhost:8080/api/v1/sessions/${sessionId}/model`, {
      data: { providerId: 'zai', modelId: 'glm-4.6' },
    })

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

    // Same probe wiring as the Team-Lead block. Wire-side EventSource
    // wrap + DOM MutationObserver + Pinia $subscribe — captures the
    // full chunk → store → DOM path so a red surface tells us WHICH
    // layer dropped the update.
    await page.addInitScript(() => {
      const probe = {
        wire: [] as ProbeWireEntry[],
        store: [] as ProbeStoreEntry[],
        dom: [] as ProbeDomEntry[],
        apply: [] as ProbeApplyEntry[],
        sub: [] as ProbeSubscribeEntry[],
      }
      ;(window as Window).__sseProbe = probe
      const startedAt = Date.now()

      const NativeEventSource = window.EventSource
      class ProbeEventSource extends NativeEventSource {
        constructor(url: string | URL, init?: EventSourceInit) {
          super(url, init)
          const probedUrl = typeof url === 'string' ? url : url.toString()
          this.addEventListener('message', (event: MessageEvent) => {
            const data = typeof event.data === 'string' ? event.data : ''
            probe.wire.push({
              t: Date.now() - startedAt,
              url: probedUrl,
              event: 'message',
              preview: data.slice(0, 200),
            })
          })
          this.addEventListener('error', () => {
            probe.wire.push({ t: Date.now() - startedAt, url: probedUrl, event: 'error', preview: '' })
          })
          this.addEventListener('open', () => {
            probe.wire.push({ t: Date.now() - startedAt, url: probedUrl, event: 'open', preview: '' })
          })
        }
      }
      window.EventSource = ProbeEventSource as unknown as typeof EventSource

      ;(window as Window).__sseProbeInstall = (): void => {
        const startedAtInstall = Date.now()
        const recordDom = (): void => {
          // Default-assistant on glm-4.6 emits thinking before any
          // content (the model is reasoning-on by default on the zai
          // provider). The thinking row surfaces as
          // `.message-bubble.thinking`; the eventual reply surfaces as
          // `.message-bubble.assistant`. Either bubble growing in the
          // DOM before [DONE] arrives counts as live-render.
          const bubbles = document.querySelectorAll<HTMLElement>(
            '.message-bubble.assistant, .message-bubble.thinking',
          )
          const last = bubbles[bubbles.length - 1]
          probe.dom.push({
            t: Date.now() - startedAtInstall,
            assistantBubbleCount: bubbles.length,
            lastAssistantTextLen: last ? (last.textContent ?? '').length : 0,
          })
        }
        const obs = new MutationObserver(() => recordDom())
        obs.observe(document.body, { childList: true, subtree: true, characterData: true })
        recordDom()

        const findChatStore = (): unknown => {
          const root = document.getElementById('app') ?? document.body
          const vueApp = (root as unknown as {
            __vue_app__?: {
              config: {
                globalProperties: {
                  $pinia?: {
                    _s?: Map<string, { $state?: Record<string, unknown> } & Record<string, unknown>>
                    state: { value: Record<string, unknown> }
                  }
                }
              }
            }
          }).__vue_app__
          const pinia = vueApp?.config?.globalProperties?.$pinia
          if (!pinia) return null
          const fromRegistry = pinia._s?.get('chat')
          if (fromRegistry) return fromRegistry
          return pinia.state.value.chat
        }
        const recordStore = (): void => {
          const state = findChatStore() as {
            messages?: Array<{ id: string; role: string; status?: string; content?: string; thinkingContent?: string }>
            currentSessionId?: string | null
          } | null
          if (!state || !Array.isArray(state.messages)) return
          const msgs = state.messages
          const running = msgs.filter((m) => m.status === 'running')
          let bestStatus: string | null = null
          let bestLen = 0
          for (const m of running) {
            if (m.role === 'assistant' || m.role === 'thinking') {
              const contentLen = (m.content ?? '').length
              const thinkingLen = (m.thinkingContent ?? '').length
              const len = Math.max(contentLen, thinkingLen)
              if (len > bestLen) {
                bestStatus = m.status ?? null
                bestLen = len
              } else if (bestStatus === null) {
                bestStatus = m.status ?? null
              }
            }
          }
          probe.store.push({
            t: Date.now() - startedAtInstall,
            messageCount: msgs.length,
            lastAssistantStatus: bestStatus,
            lastAssistantContentLen: bestLen,
            currentSessionId: state.currentSessionId ?? null,
            contextUsageKeys: [],
            currentModelId: '',
          })
        }
        recordStore()
        const storePoll = setInterval(recordStore, 50)
        ;(window as Window & { __ssePoll?: ReturnType<typeof setInterval> }).__ssePoll = storePoll
      }
    })

    await page.goto('/chat')
    await page.evaluate((sid) => {
      localStorage.clear()
      localStorage.setItem('chat.currentSessionId', sid)
      localStorage.setItem('chat.agentId', 'default-assistant')
    }, sessionId)
    await page.reload()
    await page.getByTestId('chat-empty-state').waitFor({ state: 'visible', timeout: 15_000 })

    await page.evaluate(() => (window as Window).__sseProbeInstall?.())
  })

  test('default-assistant session on zai/glm-4.6 live-renders thinking before [DONE]', async ({ page }) => {
    const input = page.getByTestId('message-input')
    const sendBtn = page.getByTestId('send-button')

    // Terse prompt — keeps the upstream cost low while still triggering
    // glm-4.6's thinking surface (the model emits reasoning tokens before
    // every reply, even single-token ones).
    const PROMPT = 'say the word PING and nothing else'
    await input.fill(PROMPT)
    await sendBtn.click()

    // Wait for the first reply-bearing SSE chunk — either thinking or
    // content. context_usage / model_active land first and are skipped
    // because they don't surface the assistant turn.
    let firstChunkAt: number
    try {
      const firstChunkResult = await page.waitForFunction(
        () => {
          const probe = (window as Window).__sseProbe
          if (!probe) return false
          const replyChunk = probe.wire.find(
            (w) =>
              w.event === 'message' &&
              w.preview !== '[DONE]' &&
              !w.preview.includes('"type":"context_usage"') &&
              !w.preview.includes('"type":"model_active"') &&
              (w.preview.includes('"type":"thinking"') ||
                w.preview.includes('"type":"content"')),
          )
          return replyChunk ? { firstChunkAt: replyChunk.t, preview: replyChunk.preview } : false
        },
        undefined,
        { timeout: 90_000 },
      )
      ;({ firstChunkAt } = await firstChunkResult.jsonValue() as { firstChunkAt: number; preview: string })
    } catch (e) {
      const partial = await page.evaluate(() => {
        const probe = (window as Window).__sseProbe
        return probe ? { wire: probe.wire.slice() } : null
      })
      throw new Error(
        `No reply-bearing SSE chunk arrived within 90s of send (default-assistant + zai/glm-4.6).\n` +
          `(${(e as Error).message})\n` +
          `--- WIRE (${partial?.wire.length ?? 0}) ---\n${JSON.stringify(partial?.wire.slice(0, 40) ?? [], null, 2)}`,
      )
    }

    // 2s drain — gives the store poll (50ms cadence) and the MutationObserver
    // ample time to capture the post-firstChunk live updates. 7eb3cf47's
    // handleThinkingEvent path creates a thinking running row + appends
    // thinkingContent on every delta; the MutationObserver should pick up
    // either the row insertion or its text growth.
    await page.waitForTimeout(2000)

    const snapshot = await page.evaluate(() => {
      const probe = (window as Window).__sseProbe
      if (!probe) return null
      return { wire: probe.wire.slice(), store: probe.store.slice(), dom: probe.dom.slice() }
    })
    expect(snapshot, 'probe snapshot must have been captured').not.toBeNull()
    const { wire, store, dom } = snapshot as Pick<ProbeSnapshot, 'wire' | 'store' | 'dom'>

    const liveDomEntries = dom.filter((d) => d.lastAssistantTextLen > 0)
    const liveStoreEntries = store.filter(
      (s) => s.lastAssistantStatus === 'running' && s.lastAssistantContentLen > 0,
    )

    expect(
      liveDomEntries.length,
      `DOM evidence: expected ≥1 thinking-or-assistant bubble text mutation post-firstChunk@${firstChunkAt}ms ` +
        `for the default-assistant + zai/glm-4.6 session (user's exact reproduction).\n` +
        `Got ${liveDomEntries.length}.\n` +
        `\n--- WIRE (${wire.length} entries, first 30) ---\n${JSON.stringify(wire.slice(0, 30), null, 2)}\n` +
        `\n--- STORE (last 10 entries) ---\n${JSON.stringify(store.slice(-10), null, 2)}\n` +
        `\n--- DOM (${dom.length} entries, last 10) ---\n${JSON.stringify(dom.slice(-10), null, 2)}`,
    ).toBeGreaterThan(0)

    expect(
      liveStoreEntries.length,
      `STORE evidence: chatStore.messages must carry a running thinking/assistant message ` +
        `with non-empty content post-firstChunk@${firstChunkAt}ms.\n` +
        `(WIRE has chunks but STORE empty ⇒ handler path broken; STORE updates but DOM empty ⇒ ` +
        `Vue reactivity / template binding; both empty ⇒ chunk listener never wired.)\n` +
        `STORE (last 30): ${JSON.stringify(store.slice(-30), null, 2)}`,
    ).toBeGreaterThan(0)
  })
})
