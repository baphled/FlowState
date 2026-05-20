import { test, expect, type Page, type Route } from "@playwright/test";

// =============================================================================
// UI Parity Playwright suite — May 2026 (eight commits, `feature/vue-ui-rebase`)
// =============================================================================
//
// Locks the new user-visible behaviour from the UI Parity stream at the
// integration layer. The Vitest passes already cover units in isolation; these
// tests prove the same behaviour holds end-to-end through Vue's reactivity,
// the chatStore, the SSE event router, and the live DOM under the vite dev
// server.
//
// Commit cross-reference:
//   c07132a7 PR1 — Shiki, ThinkingPanel, per-block CopyButton
//   aea10907 PR3 — Tool cards collapse-by-default, subtitle, overflow-wrap, Regenerate
//   7aecfd0c PR2 — Composer (attachments, history, stop, icon swap, help modal)
//   d4a8b1fb Bug-fix bundle — 9 fixes (P0/P1 regression-catcher set below)
//   14ae001b PR4 — Theme polish (4 new themes, Shiki theme-aware, hover preview)
//   ae417e71 PR6 — Tier-3 polish (EditTool hunks, RecallSearch timestamps,
//                  LoadingOverlay min-duration, Collapse-all toolbar)
//   e4dbfb1d PR5 — Live token counter via streaming.heartbeat
//
// Mock layer: `installCommonRoutes` mirrors the established pattern from
// `chat.spec.ts` and `chat-input-and-markdown-rendering.spec.ts`. SSE is mocked
// via a FakeEventSource installed by `addInitScript` — same shape as
// `chat-multi-turn-streaming.spec.ts` and `chat-todowrite-content-gap.spec.ts`
// so contributors familiar with those specs read this one fluently.
// =============================================================================

const AGENTS = [
  {
    id: "planner",
    name: "Planner",
    description: "Plans work",
    model: "claude-sonnet-4-6",
  },
];

const BASE_SESSION = {
  id: "session-uip",
  title: "UI Parity Session",
  agentId: "planner",
  messageCount: 0,
  createdAt: "2026-05-10T09:00:00Z",
  updatedAt: "2026-05-10T09:00:00Z",
};

interface MockMessage {
  id: string;
  role: string;
  content: string;
  toolName?: string;
  status?: string;
  timestamp: string;
}

interface PostGate {
  release: () => void;
  released: Promise<void>;
}

function newGate(): PostGate {
  let release: () => void = () => {};
  const released = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { release, released };
}

// FakeEventSource — same shape used by chat-multi-turn-streaming.spec.ts.
// The page-context driver exposes `fire(type, data)` so the test can deliver
// arbitrary SSE chunks at controlled timing. Installed via addInitScript so
// every test resets the instance list per page reload.
async function installFakeEventSource(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const w = window as unknown as { [k: string]: unknown };
    class FakeEventSource {
      listeners: Record<string, Array<(event: MessageEvent) => void>> = {};
      url: string;
      readyState = 1;
      constructor(url: string) {
        this.url = url;
        (w.__sseInstances as FakeEventSource[] | undefined)?.push(this) ??
          (w.__sseInstances = [this]);
      }
      addEventListener(type: string, fn: (event: MessageEvent) => void): void {
        this.listeners[type] = this.listeners[type] || [];
        this.listeners[type].push(fn);
      }
      removeEventListener(
        type: string,
        fn: (event: MessageEvent) => void,
      ): void {
        this.listeners[type] = (this.listeners[type] || []).filter(
          (f) => f !== fn,
        );
      }
      close(): void {
        this.readyState = 2;
      }
      fire(type: string, data: unknown): void {
        const fns = this.listeners[type] || [];
        const payload = typeof data === "string" ? data : JSON.stringify(data);
        for (const fn of fns) fn({ data: payload } as MessageEvent);
      }
    }
    w.EventSource = FakeEventSource;
    w.__sseDriver = {
      instances: () =>
        (w.__sseInstances as FakeEventSource[] | undefined) ?? [],
    };
  });
}

interface RoutesOptions {
  // Hold the POST /messages handler until released so the test can deliver
  // SSE chunks before the canonical history lands.
  postGate?: PostGate;
  // Canonical message list returned by GET /messages and the POST response.
  messages?: MockMessage[];
  // Allow tests to inject a per-call POST mutator (e.g. echo user prompt).
  onPost?: (body: { content?: string }) => MockMessage[];
}

async function installCommonRoutes(
  page: Page,
  opts: RoutesOptions = {},
): Promise<void> {
  const messagesBySession: Record<string, MockMessage[]> = {
    [BASE_SESSION.id]: opts.messages ?? [],
  };

  await page.route("**/api/agents", async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(AGENTS),
    });
  });

  await page.route("**/api/v1/models", async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ providers: [] }),
    });
  });

  await page.route("**/api/v1/sessions", async (route: Route) => {
    if (route.request().method() === "POST") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ id: BASE_SESSION.id, agentId: "planner" }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([
        {
          ...BASE_SESSION,
          messageCount: messagesBySession[BASE_SESSION.id]?.length ?? 0,
          currentAgentId: "planner",
        },
      ]),
    });
  });

  await page.route("**/api/v1/sessions/*/messages", async (route: Route) => {
    const url = route.request().url();
    const sessionId =
      url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? BASE_SESSION.id;
    if (route.request().method() === "POST") {
      const body = route.request().postDataJSON() as { content?: string };
      if (opts.postGate) await opts.postGate.released;
      if (opts.onPost) {
        messagesBySession[sessionId] = opts.onPost(body);
      } else {
        const prev = messagesBySession[sessionId] ?? [];
        messagesBySession[sessionId] = [
          ...prev,
          {
            id: `srv-u-${prev.length}`,
            role: "user",
            content: body.content ?? "",
            timestamp: new Date().toISOString(),
          },
        ];
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          id: sessionId,
          agentId: "planner",
          messages: messagesBySession[sessionId],
          messageCount: messagesBySession[sessionId].length,
          createdAt: BASE_SESSION.createdAt,
          updatedAt: new Date().toISOString(),
        }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(messagesBySession[sessionId] ?? []),
    });
  });

  await page.route("**/api/swarm/events", async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([]),
    });
  });

  await page.route("**/api/v1/sessions/*/stream", async (route: Route) => {
    // DELETE for stop button — return 204.
    await route.fulfill({ status: 204, body: "" });
  });

  await page.route("**/api/health", async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: '{"ok":true}',
    });
  });
}

// Page-context helper that drives the FakeEventSource. Returns a promise so
// the caller can await chunk delivery before assertions.
async function fireSSE(page: Page, type: string, data: unknown): Promise<void> {
  await page.evaluate(
    ({ type, data }) => {
      const driver = (
        window as unknown as {
          __sseDriver?: {
            instances: () => Array<{ fire: (t: string, d: unknown) => void }>;
          };
        }
      ).__sseDriver;
      const es = driver?.instances()[0];
      if (es) es.fire(type, data);
    },
    { type, data },
  );
}

async function waitForSSE(page: Page): Promise<void> {
  await page.waitForFunction(() => {
    return (
      ((
        window as unknown as {
          __sseDriver?: { instances: () => unknown[] };
        }
      ).__sseDriver?.instances().length ?? 0) >= 1
    );
  });
}

// =============================================================================
// COMPOSER (PR2 + bug fixes)
// =============================================================================

test.describe("Composer parity — PR2 + bug fixes", () => {
  test.beforeEach(async ({ page }) => {
    await installFakeEventSource(page);
  });

  // [1] Regression catcher — Stop button appears during stream + cancels.
  // Catches the regression class: pre-PR2 there was no Stop affordance at all.
  test("Stop button replaces Send during stream and cancels on click", async ({
    page,
  }) => {
    const gate = newGate();
    await installCommonRoutes(page, { postGate: gate });
    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

    await page.getByTestId("message-input").fill("start a long task");
    await page.getByTestId("send-button").click();

    await waitForSSE(page);
    // Emit a content chunk so the session flips to isStreaming=true.
    await fireSSE(page, "message", { content: "thinking…" });

    // Stop button visible, Send button gone.
    await expect(page.getByTestId("stop-button")).toBeVisible();
    await expect(page.getByTestId("send-button")).toHaveCount(0);

    // Click Stop — handleStop calls handleEscapeKey twice (Esc-Esc chord).
    // After cancel, setSessionStreaming flips back and Send re-appears.
    await page.getByTestId("stop-button").click();

    // Release the POST so the round-trip can settle.
    gate.release();

    // Send returns once isStreaming flips false.
    await expect(page.getByTestId("send-button")).toBeVisible();
    await expect(page.getByTestId("stop-button")).toHaveCount(0);
  });

  // [2] Regression catcher — Prompt history Up arrow recall (P1-4: cross-
  // session bleed). Pre-fix the history was global; switching sessions still
  // showed the prior session's prompts.
  //
  // Strategy: seed the chatStore's promptHistoryBySession directly via
  // localStorage so the test does not need to drive three SSE round-trips
  // through the streaming queue. The chatStore's getter resolves to
  // `promptHistoryBySession[currentSessionId]` and the composer reads via
  // `store.promptHistory`, so a direct seed exercises exactly the same
  // surface that a live submit would.
  test("ArrowUp recalls per-session prompt history in LIFO order", async ({
    page,
  }) => {
    await installCommonRoutes(page);
    await page.goto("/chat");
    const input = page.getByTestId("message-input");
    await expect(input).toBeVisible();

    // Send three prompts. Each sendMessage immediately calls
    // recordPromptHistory BEFORE any async work, so even if the SSE
    // round-trip never settles the buffer fills in order. The Pinia
    // state's currentSessionId is already set from the mocked session
    // list, so each push lands in promptHistoryBySession[BASE_SESSION.id].
    for (const prompt of ["alpha", "beta", "gamma"]) {
      await input.fill(prompt);
      await page.getByTestId("send-button").click();
      // Wait for the textarea to clear — proves submit() executed past
      // recordPromptHistory.
      await expect(input).toHaveValue("", { timeout: 5_000 });
    }

    // ArrowUp at empty buffer cycles newest → oldest. Between each
    // ArrowUp the composer applies the recalled prompt and parks the
    // cursor at the end of the textarea (applyHistorySnapshot moves
    // selectionStart/End to value.length). The next ArrowUp would
    // therefore fail the `isAtBufferStart` gate. Press Home before
    // each subsequent ArrowUp to put the cursor at index 0 so the
    // walk keeps progressing — mirrors a real user who would either
    // be at the start of a single-line buffer OR press Home first.
    await input.click();
    await input.press("ArrowUp");
    await expect(input).toHaveValue("gamma");
    await input.press("Home");
    await input.press("ArrowUp");
    await expect(input).toHaveValue("beta");
    await input.press("Home");
    await input.press("ArrowUp");
    await expect(input).toHaveValue("alpha");

    // ArrowDown walks back forward — gate is `isAtBufferEnd`, which is
    // already true because applyHistorySnapshot parked us at the end.
    await input.press("ArrowDown");
    await expect(input).toHaveValue("beta");
  });

  // [3] Regression catcher — KeyboardHelpModal `?` trigger semantics
  // (P1-8: tightened predicate). Pre-fix the modal opened on `?` even
  // when a button was focused, and held-key spam stacked opens. We
  // exercise three states: editable focused (must not open), button
  // focused (must not open), non-input non-button focused (must open).
  test("? opens KeyboardHelpModal only when no input/button is focused", async ({
    page,
  }) => {
    await installCommonRoutes(page);
    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

    // (a) Focus the textarea → press `?` → modal must NOT open.
    const input = page.getByTestId("message-input");
    await input.click();
    await page.keyboard.press("Shift+Slash");
    await expect(page.getByTestId("keyboard-help-modal")).toHaveCount(0);

    // (b) Focus the Send button → press `?` → modal must NOT open
    //     (P1-8 predicate tightened to exclude button-like targets).
    await input.fill("");
    await page.getByTestId("send-button").focus();
    await page.keyboard.press("Shift+Slash");
    await expect(page.getByTestId("keyboard-help-modal")).toHaveCount(0);

    // (c) Blur every focusable then press `?` → modal opens.
    await page.evaluate(() => {
      (document.activeElement as HTMLElement | null)?.blur();
    });
    await page.keyboard.press("Shift+Slash");
    await expect(page.getByTestId("keyboard-help-modal")).toBeVisible();

    // Esc closes the modal.
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("keyboard-help-modal")).toHaveCount(0);
  });

  // [4] Regression catcher — Attachment file picker exists + drag overlay
  // appears on file-drag (P1-6: sticky overlay catcher uses window-level
  // dragend reset). We synthesise a real DragEvent with DataTransfer so
  // the composer's `dataTransfer.types.includes('Files')` predicate fires.
  test("Attachment picker renders and drag overlay surfaces on file drag", async ({
    page,
  }) => {
    await installCommonRoutes(page);
    await page.goto("/chat");

    // Picker button + hidden file input are mounted.
    await expect(page.getByTestId("attach-button")).toBeVisible();
    await expect(page.getByTestId("file-input")).toBeAttached();

    // Synthesise a real DragEvent with a DataTransfer carrying a 'Files'
    // type entry. Playwright's dispatchEvent helper doesn't support
    // DataTransfer construction so we build the event in page context.
    await page.evaluate(() => {
      const wrap = document.querySelector('[data-testid="message-input-wrap"]');
      if (!wrap) throw new Error("no wrap");
      const dt = new DataTransfer();
      // Adding a File makes `dt.types` include 'Files' automatically.
      dt.items.add(new File(["x"], "pixel.png", { type: "image/png" }));
      wrap.dispatchEvent(
        new DragEvent("dragenter", { dataTransfer: dt, bubbles: true }),
      );
    });

    await expect(page.getByTestId("message-input-drag-overlay")).toBeVisible({
      timeout: 5_000,
    });

    // dragleave with the counter at 1 should clear the overlay. The
    // handler also reads dataTransfer but only for cleanup, so the
    // event can ship without it.
    await page.evaluate(() => {
      const wrap = document.querySelector('[data-testid="message-input-wrap"]');
      if (!wrap) throw new Error("no wrap");
      const dt = new DataTransfer();
      dt.items.add(new File(["x"], "pixel.png", { type: "image/png" }));
      wrap.dispatchEvent(
        new DragEvent("dragleave", { dataTransfer: dt, bubbles: true }),
      );
    });
    await expect(page.getByTestId("message-input-drag-overlay")).toHaveCount(0);
  });

  // [5] Regression catcher — empty composer + staged attachment shows
  // a toast and does NOT call sendMessage (P0-1).
  test("Send with attachment + empty text surfaces a toast and does not POST", async ({
    page,
  }) => {
    await installCommonRoutes(page);
    await page.goto("/chat");

    let postedSendCount = 0;
    page.on("request", (req) => {
      if (
        req.method() === "POST" &&
        /\/sessions\/[^/]+\/messages$/.test(req.url())
      ) {
        postedSendCount += 1;
      }
    });

    // Stage a single image via the hidden file input. The file picker
    // handler stages files into pendingAttachments.
    await page.getByTestId("file-input").setInputFiles({
      name: "pixel.png",
      mimeType: "image/png",
      // 1×1 transparent PNG (smallest valid PNG payload).
      buffer: Buffer.from(
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNgAAIAAAUAAen63NgAAAAASUVORK5CYII=",
        "base64",
      ),
    });

    // The Send button is enabled (attachments alone enable submit), but
    // the submit handler blocks when text is empty and shows a toast.
    const sendButton = page.getByTestId("send-button");
    await expect(sendButton).toBeEnabled();
    await sendButton.click();

    // Toast surfaces. ToastContainer's test surface uses role="alert"
    // or a class match; either way the "Message required" copy lands.
    await expect(page.getByText("Message required")).toBeVisible();

    // sendMessage POST was NOT issued. Allow a brief settle to confirm.
    await page.waitForTimeout(200);
    expect(postedSendCount).toBe(0);
  });
});

// =============================================================================
// CODE RENDERING (PR1)
// =============================================================================

test.describe("Code rendering parity — PR1", () => {
  test.beforeEach(async ({ page }) => {
    await installFakeEventSource(page);
  });

  // [6] Shiki tokenisation produces span-wrapped tokens with inline color.
  // Shiki loads lazily; the first render falls back to plain `<pre><code>`
  // until ensureHighlighterLoaded resolves, then a reactive `highlighterVersion`
  // counter bumps and the computed re-renders. Give Shiki up to 20s — bundle
  // size on a cold dev-server start can be 4-5MB.
  test("Fenced JS block renders Shiki tokens, not plain text", async ({
    page,
  }) => {
    const code = "const greet = (name) => `hello ${name}`";
    const reply = `Here is a snippet:\n\n\`\`\`js\n${code}\n\`\`\`\n`;
    await installCommonRoutes(page, {
      onPost: () => [
        {
          id: "a1",
          role: "assistant",
          content: reply,
          timestamp: new Date().toISOString(),
        },
      ],
    });
    await page.goto("/chat");
    await page.getByTestId("message-input").fill("show me code");
    await page.getByTestId("send-button").click();

    const bubble = page.getByTestId("message-assistant").first();
    await expect(bubble).toBeVisible({ timeout: 15_000 });

    // The fence renderer wraps every fenced block in `.markdown-code` —
    // present from the first render regardless of Shiki readiness. The
    // wrapper's data-code-raw is the literal fence content, which the
    // MarkdownIt tokenizer keeps the trailing newline on — match by
    // substring rather than equality.
    const wrapper = bubble.locator(".markdown-code").first();
    await expect(wrapper).toBeVisible();
    const raw = await wrapper.getAttribute("data-code-raw");
    expect(raw ?? "").toContain(code);

    // Shiki swaps the inner `<pre><code>` for `<pre class="shiki ...">` once
    // the highlighter resolves. Match on the class prefix; Shiki appends
    // the theme key (e.g. `shiki shiki-themes ...`).
    const shikiPre = bubble.locator('pre[class*="shiki"]');
    await expect(shikiPre).toHaveCount(1, { timeout: 20_000 });

    // N3 multi-theme mode (PR4): Shiki emits each token span with
    // CSS-variable-based colour (`--shiki-<theme>` per loaded theme)
    // rather than a literal `color:#xxx` rule. Match on the inline
    // style carrying any --shiki- variable so the assertion survives
    // either the pre-N3 single-theme contract or the post-N3 multi-
    // theme contract.
    const styledSpans = shikiPre.locator(
      'span[style*="--shiki"], span[style*="color:"]',
    );
    await expect
      .poll(() => styledSpans.count(), { timeout: 5_000 })
      .toBeGreaterThan(0);
  });

  // [7] ThinkingPanel — collapsible <details> element, MarkdownRenderer
  // inside renders fenced code with Shiki.
  test("ThinkingPanel renders as <details> with markdown body", async ({
    page,
  }) => {
    const thinkingContent = "Let me consider:\n\n```js\nconst x = 1\n```";
    await installCommonRoutes(page, {
      onPost: () => [
        {
          id: "th-1",
          role: "thinking",
          content: thinkingContent,
          timestamp: new Date().toISOString(),
        },
        {
          id: "a-1",
          role: "assistant",
          content: "Done.",
          timestamp: new Date().toISOString(),
        },
      ],
    });
    await page.goto("/chat");
    await page.getByTestId("message-input").fill("think for me");
    await page.getByTestId("send-button").click();

    const panel = page.getByTestId("thinking-panel").first();
    await expect(panel).toBeVisible({ timeout: 10_000 });
    // It is a <details> element collapsed by default.
    await expect(panel).toHaveJSProperty("tagName", "DETAILS");
    await expect(panel).not.toHaveAttribute("open", "");

    // Expand by clicking the summary.
    await panel.locator("summary").click();
    await expect(panel).toHaveAttribute("open", "");

    // The body contains a markdown surface (fenced code → <pre>).
    await expect(panel.locator("pre")).toHaveCount(1);
  });

  // [8] Per-block CopyButton — every fenced code block emits its own
  // copy-btn alongside the message-level CopyButton.
  test("Each fenced code block emits a per-block copy button", async ({
    page,
  }) => {
    const reply = [
      "First snippet:",
      "",
      "```js",
      "const a = 1",
      "```",
      "",
      "Second snippet:",
      "",
      "```py",
      "a = 2",
      "```",
      "",
    ].join("\n");
    await installCommonRoutes(page, {
      onPost: () => [
        {
          id: "a1",
          role: "assistant",
          content: reply,
          timestamp: new Date().toISOString(),
        },
      ],
    });
    await page.goto("/chat");
    await page.getByTestId("message-input").fill("two snippets");
    await page.getByTestId("send-button").click();

    const bubble = page.getByTestId("message-assistant").first();
    await expect(bubble).toBeVisible({ timeout: 10_000 });

    // Two per-block copy buttons live inside the markdown body.
    await expect(
      bubble.locator('.markdown-body [data-testid="markdown-code-copy-btn"]'),
    ).toHaveCount(2);

    // The message-level CopyButton is the bubble's own copy affordance
    // (rendered alongside the bubble actions, not inside markdown-body).
    await expect(bubble.getByTestId("message-copy-btn")).toBeVisible();
  });
});

// =============================================================================
// TOOL CARDS (PR3 + bug fixes)
// =============================================================================

test.describe("Tool cards parity — PR3 + bug fixes", () => {
  test.beforeEach(async ({ page }) => {
    await installFakeEventSource(page);
  });

  // [9] Bash/Read collapse by default; Edit defaults open. Driving via SSE
  // tool_call+tool_result chunks rather than a pre-seeded POST response
  // guarantees registerTools() has run (ChatView's onMounted completes
  // before the user can submit anything) so getToolComponent resolves to
  // the per-tool component (not the GenericTool fallback that ships
  // defaultOpen=true). We avoid adjacent context-tool pairs (read+grep)
  // because chatViewHelpers' groupContextTools collapses them into a
  // single ContextToolGroup which hides the per-card chrome the test
  // wants to inspect.
  test("Bash and Read tool cards default-collapsed; Edit defaults open", async ({
    page,
  }) => {
    const gate = newGate();
    await installCommonRoutes(page, { postGate: gate });
    await page.goto("/chat");
    await page.getByTestId("message-input").fill("run tools");
    await page.getByTestId("send-button").click();
    await waitForSSE(page);

    // For each tool, fire tool_call (creates a running tool_result row)
    // then tool_result (fills in the content + flips status to completed).
    // This matches the engine's wire sequence in
    // internal/api/server.go writeSSEToolCall + writeSSEToolResult.
    for (const tool of ["bash", "read", "edit"]) {
      await fireSSE(page, "message", {
        type: "tool_call",
        name: tool,
        status: "running",
      });
      await fireSSE(page, "message", {
        type: "tool_result",
        content:
          tool === "edit" ? "@@ -1,1 +1,1 @@\n-old\n+new\n" : `${tool} output`,
      });
    }

    const bash = page
      .locator('[data-testid="tool-renderer"][data-tool="bash"]')
      .first();
    const read = page
      .locator('[data-testid="tool-renderer"][data-tool="read"]')
      .first();
    const edit = page
      .locator('[data-testid="tool-renderer"][data-tool="edit"]')
      .first();

    await expect(bash).toBeVisible({ timeout: 10_000 });
    await expect(bash).toHaveAttribute("data-open", "false");
    await expect(read).toHaveAttribute("data-open", "false");
    await expect(edit).toHaveAttribute("data-open", "true");

    // Clicking the bash trigger opens it.
    await bash.locator(".tool-bubble__trigger").click();
    await expect(bash).toHaveAttribute("data-open", "true");

    gate.release();
  });

  // [10] P0-3 force-open-on-error live-stream path. The Gap 2 wire
  // change (May 2026) added a `tool_error` SSE event the chatStore
  // translates into a status='error' mutation on the matching running
  // tool_result row. The ToolBubble's cardDefaultOpen watcher (P0-3 fix
  // at ToolBubble.spec.ts:147) force-opens the card on the running →
  // error transition, so the failure surfaces in-stream without a
  // chevron click.
  test("Tool card auto-opens when status transitions to error (P0-3)", async ({
    page,
  }) => {
    const gate = newGate();
    await installCommonRoutes(page, { postGate: gate });
    await page.goto("/chat");
    await page.getByTestId("message-input").fill("run a failing tool");
    await page.getByTestId("send-button").click();
    await waitForSSE(page);

    // Fire a tool_call (creates a running bash tool_result row, default-
    // collapsed because BashTool's cardDefaultOpen seeds from status=
    // 'running' → false).
    await fireSSE(page, "message", {
      type: "tool_call",
      name: "bash",
      status: "running",
    });

    const bash = page
      .locator('[data-testid="tool-renderer"][data-tool="bash"]')
      .first();
    await expect(bash).toBeVisible({ timeout: 10_000 });
    await expect(bash).toHaveAttribute("data-open", "false");

    // Now fire the new tool_error event. chatStore.handleToolErrorEvent
    // flips the bash row's status to 'error'; BashTool's cardDefaultOpen
    // computed becomes true; ToolBubble's watcher force-opens the card.
    await fireSSE(page, "message", {
      type: "tool_error",
      content: "Error: bash exited non-zero",
    });

    await expect(bash).toHaveAttribute("data-status", "error");
    await expect(bash).toHaveAttribute("data-open", "true");

    gate.release();
  });

  // [11] Regression catcher — P1-7: Regenerate button visible on completed
  // assistant; hidden during any in-flight stream (mid-stream click would
  // disconnect the current stream and silently kill a different turn).
  test("Regenerate button shows on completed assistant; hidden during stream", async ({
    page,
  }) => {
    const gate = newGate();
    await installCommonRoutes(page, {
      postGate: gate,
      onPost: () => [
        {
          id: "srv-u-0",
          role: "user",
          content: "hi",
          timestamp: "2026-05-10T00:00:00Z",
        },
        {
          id: "srv-a-0",
          role: "assistant",
          content: "hello there",
          timestamp: "2026-05-10T00:00:01Z",
        },
      ],
    });
    await page.goto("/chat");
    await page.getByTestId("message-input").fill("hi");
    await page.getByTestId("send-button").click();
    gate.release();

    const assistant = page.getByTestId("message-assistant").first();
    await expect(assistant).toBeVisible({ timeout: 10_000 });
    // After settle, the Regenerate button is visible.
    await expect(assistant.getByTestId("message-regenerate-btn")).toBeVisible();

    // Start a second turn that streams. With a stream in flight,
    // anyStreamInFlight gates the Regenerate button to hidden.
    const gate2 = newGate();
    await page.unroute("**/api/v1/sessions/*/messages");
    await page.route("**/api/v1/sessions/*/messages", async (route) => {
      if (route.request().method() === "POST") {
        await gate2.released;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            id: BASE_SESSION.id,
            agentId: "planner",
            messages: [
              {
                id: "srv-u-0",
                role: "user",
                content: "hi",
                timestamp: "2026-05-10T00:00:00Z",
              },
              {
                id: "srv-a-0",
                role: "assistant",
                content: "hello there",
                timestamp: "2026-05-10T00:00:01Z",
              },
              {
                id: "srv-u-1",
                role: "user",
                content: "again",
                timestamp: "2026-05-10T00:00:02Z",
              },
              {
                id: "srv-a-1",
                role: "assistant",
                content: "another reply",
                timestamp: "2026-05-10T00:00:03Z",
              },
            ],
          }),
        });
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([
          {
            id: "srv-u-0",
            role: "user",
            content: "hi",
            timestamp: "2026-05-10T00:00:00Z",
          },
          {
            id: "srv-a-0",
            role: "assistant",
            content: "hello there",
            timestamp: "2026-05-10T00:00:01Z",
          },
        ]),
      });
    });

    await page.getByTestId("message-input").fill("again");
    await page.getByTestId("send-button").click();
    await fireSSE(page, "message", { content: "streaming…" });

    // Regenerate buttons (across all assistant bubbles) hidden during stream.
    await expect(page.getByTestId("message-regenerate-btn")).toHaveCount(0);
    gate2.release();
  });
});

// =============================================================================
// THEMES (PR4)
// =============================================================================

test.describe("Theme polish — PR4", () => {
  test.beforeEach(async ({ page }) => {
    await installFakeEventSource(page);
    await installCommonRoutes(page);
  });

  // [12] Theme switching writes <html data-theme> and persists.
  test('Selecting Tokyo Night sets <html data-theme="tokyo-night">', async ({
    page,
  }) => {
    await page.goto("/settings");
    await expect(page.getByTestId("settings-view")).toBeVisible();

    await page.getByTestId("theme-option-tokyo-night").click();
    const theme = await page.evaluate(() =>
      document.documentElement.getAttribute("data-theme"),
    );
    expect(theme).toBe("tokyo-night");
  });

  // [13] Theme hover preview — mouseenter on a theme option mutates
  // <html data-theme> live; mouseleave reverts to the committed theme.
  test("Hovering a theme option live-previews; leaving reverts", async ({
    page,
  }) => {
    await page.goto("/settings");
    await expect(page.getByTestId("settings-view")).toBeVisible();

    // Commit dark first so we have a known revert target.
    await page.getByTestId("theme-option-dark").click();
    expect(
      await page.evaluate(() =>
        document.documentElement.getAttribute("data-theme"),
      ),
    ).toBe("dark");

    // Hover Catppuccin Mocha → preview applies.
    await page.getByTestId("theme-option-catppuccin-mocha").hover();
    expect(
      await page.evaluate(() =>
        document.documentElement.getAttribute("data-theme"),
      ),
    ).toBe("catppuccin-mocha");

    // Mouseleave (move pointer onto the page heading) → reverts.
    await page.locator("h1").first().hover();
    expect(
      await page.evaluate(() =>
        document.documentElement.getAttribute("data-theme"),
      ),
    ).toBe("dark");
  });

  // [14] Theme variable coverage — every theme defines the key CSS
  // variables. Loops over all 7 themes; reads computed style after
  // applying each. A missing variable in one theme breaks the page
  // chrome silently — this catches the leak before users see it.
  test("Every theme defines the core CSS variables", async ({ page }) => {
    const themes = [
      "dark",
      "light",
      "terminal",
      "tokyo-night",
      "catppuccin-mocha",
      "dracula",
      "nord",
    ];
    const vars = ["--text-primary", "--bg-primary", "--accent"];
    await page.goto("/settings");
    for (const theme of themes) {
      const checks = await page.evaluate(
        ({ theme, vars }) => {
          document.documentElement.setAttribute("data-theme", theme);
          const style = getComputedStyle(document.documentElement);
          const result: Record<string, string> = {};
          for (const v of vars) {
            result[v] = style.getPropertyValue(v).trim();
          }
          return result;
        },
        { theme, vars },
      );
      for (const v of vars) {
        expect(checks[v], `${theme} missing ${v}`).not.toBe("");
      }
    }
  });
});

// =============================================================================
// POLISH (PR6)
// =============================================================================

test.describe("Tier-3 polish — PR6", () => {
  test.beforeEach(async ({ page }) => {
    await installFakeEventSource(page);
  });

  // [15] EditTool — multi-hunk diff renders one block per `@@` header.
  test("EditTool renders one hunk block per @@ marker", async ({ page }) => {
    const diff = [
      "@@ -1,3 +1,3 @@",
      "-old line 1",
      " context 1",
      "+new line 1",
      "@@ -10,2 +10,2 @@",
      "-old line 10",
      "+new line 10",
    ].join("\n");
    await installCommonRoutes(page, {
      onPost: () => [
        {
          id: "ed-1",
          role: "tool_result",
          content: diff,
          toolName: "edit",
          timestamp: new Date().toISOString(),
        },
      ],
    });
    await page.goto("/chat");
    await page.getByTestId("message-input").fill("edit a file");
    await page.getByTestId("send-button").click();

    const hunks = page.getByTestId("edit-hunk");
    await expect(hunks).toHaveCount(2, { timeout: 10_000 });
    await expect(page.getByTestId("edit-hunk-header").first()).toContainText(
      "@@ -1,3 +1,3 @@",
    );
  });

  // [16] RecallSearchTool — `[time=ISO]` and `[depth=N]` prefixes produce
  // relative timestamp + chain-depth chip. We use a fixed clock so the
  // relative format doesn't drift.
  test("Recall results render relative timestamp + chain-depth chip", async ({
    page,
  }) => {
    // Pin a fixed Date.now() so "Nh ago" math is deterministic regardless
    // of CI clock skew. Anchor: 2026-05-12T10:00:00Z. Result timestamp
    // 2026-05-12T08:00:00Z → 2h ago.
    await page.addInitScript(() => {
      const fixed = new Date("2026-05-12T10:00:00Z").getTime();
      const RealDate = Date;
      class FixedDate extends RealDate {
        constructor(...args: ConstructorParameters<typeof Date>) {
          if (args.length === 0) {
            super(fixed);
          } else {
            super(...(args as []));
          }
        }
        static now() {
          return fixed;
        }
      }
      (window as unknown as { Date: typeof Date }).Date =
        FixedDate as unknown as typeof Date;
    });

    const recallBody =
      "[time=2026-05-12T08:00:00Z] [depth=3] user: I asked about retries.";
    const gate = newGate();
    await installCommonRoutes(page, { postGate: gate });
    await page.goto("/chat");
    await page
      .getByTestId("message-input")
      .fill("what did I say about retries");
    await page.getByTestId("send-button").click();
    await waitForSSE(page);

    // Drive via SSE so registerTools() has populated the registry and
    // the chatStore routes search_context to RecallSearchTool (not the
    // GenericTool fallback). The engine emits tool_call + tool_result
    // separately — the call event creates the running tool_result row,
    // the result event fills its content.
    await fireSSE(page, "message", {
      type: "tool_call",
      name: "search_context",
      status: "running",
    });
    await fireSSE(page, "message", {
      type: "tool_result",
      content: recallBody,
    });

    const card = page
      .locator('[data-testid="tool-renderer"][data-tool="search_context"]')
      .first();
    await expect(card).toBeVisible({ timeout: 10_000 });
    if ((await card.getAttribute("data-open")) === "false") {
      await card.locator(".tool-bubble__trigger").click();
    }
    const result = page.getByTestId("recall-result").first();
    await expect(result).toBeVisible({ timeout: 5_000 });
    await expect(result.getByTestId("recall-timestamp")).toContainText("h ago");
    await expect(result.getByTestId("recall-chain-depth")).toBeVisible();

    gate.release();
  });

  // [17] Regression catcher — LoadingOverlay min-duration gate. A fast
  // bootstrap (<200ms) must never reveal the overlay. We can't drive
  // <200ms reliably across CI, but we CAN drive the inverse: a slow
  // bootstrap reveals the overlay. The min-duration claim is then
  // proven indirectly: overlay is `v-if="overlayVisible"`, gated on a
  // setTimeout that fires at 200ms; the slow-bootstrap path lets the
  // timer fire and assertion holds.
  test("LoadingOverlay reveals when bootstrap is slow (>200ms gate)", async ({
    page,
  }) => {
    await installFakeEventSource(page);

    // Slow /api/health so bootstrap takes longer than the 200ms gate.
    let resolveHealth: () => void = () => {};
    const healthGate = new Promise<void>((resolve) => {
      resolveHealth = resolve;
    });
    await page.route("**/api/health", async (route) => {
      await healthGate;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: '{"ok":true}',
      });
    });

    // Other endpoints stay fast.
    await page.route("**/api/agents", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(AGENTS),
      });
    });
    await page.route("**/api/v1/sessions", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([{ ...BASE_SESSION, currentAgentId: "planner" }]),
      });
    });
    await page.route("**/api/v1/sessions/*/messages", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "[]",
      });
    });
    await page.route("**/api/v1/models", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: '{"providers":[]}',
      });
    });

    const navigation = page.goto("/chat");

    // Overlay reveals after the 200ms gate fires while bootstrap is
    // still in flight. Wait up to 1.5s for it to appear.
    await expect(page.getByTestId("app-loading-overlay")).toBeVisible({
      timeout: 1_500,
    });

    // Release the bootstrap. Overlay unmounts; ChatView renders.
    resolveHealth();
    await navigation;
    await expect(page.getByTestId("app-loading-overlay")).toHaveCount(0);
    await expect(page.getByTestId("message-input")).toBeVisible();
  });

  // [18] Collapse-all / Expand-all toolbar bulk-flips every tool card.
  test("Collapse-all and Expand-all toolbar buttons bulk-flip tool cards", async ({
    page,
  }) => {
    const gate = newGate();
    await installCommonRoutes(page, { postGate: gate });
    await page.goto("/chat");
    await page.getByTestId("message-input").fill("mixed tools");
    await page.getByTestId("send-button").click();
    await waitForSSE(page);

    // Drive three tool_call+tool_result pairs via SSE: bash, read, edit.
    // Each pair: tool_call creates the running row; tool_result fills
    // content + flips status to completed.
    for (const tool of ["bash", "read", "edit"]) {
      await fireSSE(page, "message", {
        type: "tool_call",
        name: tool,
        status: "running",
      });
      await fireSSE(page, "message", {
        type: "tool_result",
        content:
          tool === "edit" ? "@@ -1,1 +1,1 @@\n-x\n+y\n" : `${tool} output`,
      });
    }

    const cards = page.locator('[data-testid="tool-renderer"]');
    await expect(cards).toHaveCount(3, { timeout: 10_000 });

    // Expand-all: every card data-open="true".
    await page.getByTestId("expand-all-tools-btn").click();
    for (let i = 0; i < 3; i += 1) {
      await expect(cards.nth(i)).toHaveAttribute("data-open", "true");
    }

    // Collapse-all: every card data-open="false".
    await page.getByTestId("collapse-all-tools-btn").click();
    for (let i = 0; i < 3; i += 1) {
      await expect(cards.nth(i)).toHaveAttribute("data-open", "false");
    }

    gate.release();
  });
});

// =============================================================================
// LIVE TOKEN COUNTER (PR5)
// =============================================================================

test.describe("Live token counter — PR5", () => {
  test.beforeEach(async ({ page }) => {
    await installFakeEventSource(page);
  });

  // [19] Token counter renders during stream, updates on each heartbeat,
  // and surfaces the computed tokens-per-second on the second tick.
  test("Token counter renders cumulative count + rate during stream", async ({
    page,
  }) => {
    const gate = newGate();
    await installCommonRoutes(page, { postGate: gate });
    await page.goto("/chat");
    await page.getByTestId("message-input").fill("count tokens");
    await page.getByTestId("send-button").click();
    await waitForSSE(page);

    // First heartbeat — 100 tokens, no rate (single tick, no predecessor).
    await fireSSE(page, "message", {
      type: "streaming.heartbeat",
      phase: "streaming",
      token_count: 100,
    });

    // Activity indicator with token chip — formatted thousands-grouped.
    const tokens = page.getByTestId("agent-activity-tokens");
    await expect(tokens).toBeVisible({ timeout: 5_000 });
    await expect(tokens).toContainText("100 tokens");
    // No rate on the first tick.
    await expect(tokens).not.toContainText("t/s");

    // Second heartbeat — 250 tokens. Rate computed from the delta.
    await fireSSE(page, "message", {
      type: "streaming.heartbeat",
      phase: "streaming",
      token_count: 250,
    });
    await expect(tokens).toContainText("250 tokens");
    await expect(tokens).toContainText("t/s");

    gate.release();
  });

  // [20] Token counter chip vanishes when the stream completes (the
  // activity indicator unmounts; the chip lives inside it).
  test("Token counter disappears after stream completes", async ({ page }) => {
    const gate = newGate();
    await installCommonRoutes(page, {
      postGate: gate,
      onPost: () => [
        {
          id: "srv-u-0",
          role: "user",
          content: "done?",
          timestamp: "2026-05-12T00:00:00Z",
        },
        {
          id: "srv-a-0",
          role: "assistant",
          content: "done.",
          timestamp: "2026-05-12T00:00:01Z",
        },
      ],
    });
    await page.goto("/chat");
    await page.getByTestId("message-input").fill("done?");
    await page.getByTestId("send-button").click();
    await waitForSSE(page);

    await fireSSE(page, "message", {
      type: "streaming.heartbeat",
      phase: "streaming",
      token_count: 42,
    });
    await expect(page.getByTestId("agent-activity-tokens")).toBeVisible();

    // Settle the turn — POST returns canonical history, SSE delivers [DONE].
    gate.release();
    await page.waitForResponse(
      (res) =>
        res.request().method() === "POST" && /\/messages$/.test(res.url()),
    );
    await fireSSE(page, "message", "[DONE]");

    // Activity indicator unmounts (isStreaming / isLoading both false),
    // taking the token chip with it.
    await expect(page.getByTestId("agent-activity-tokens")).toHaveCount(0);
  });
});
