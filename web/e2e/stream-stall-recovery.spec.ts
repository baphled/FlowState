import { test, expect, Page } from "@playwright/test";

// PR-2 stream-stall recovery e2e.
//
// Symptom: send a message; chunks arrive; the SSE then stalls (proxy hang
// or network glitch) without ever delivering [DONE]. Pre-fix the 60s
// watchdog cleared the input gate but the bubble stayed frozen on the
// partial chunk — the user had to refresh to see the canonical completed
// reply that the backend had already persisted.
//
// Post-fix the watchdog trip handler also reconciles with the backend, so
// the bubble updates from the partial to the canonical state without any
// user action.
//
// Test mechanics:
//   - Inject a controllable FakeEventSource that lets the test drive chunks
//     and DELIBERATELY never sends [DONE].
//   - Override the GET /messages handler so the post-stall reconcile
//     returns canonical completed history different from the SSE partial.
//   - Use Playwright's page.clock to fast-forward time past the 60s
//     watchdog threshold without burning real seconds. The watchdog is a
//     setTimeout under the hood, which page.clock controls deterministically.

interface SSEChannel {
  fire: (type: string, data: unknown) => void;
  url: string;
}

async function installFakeSSE(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const w = window as unknown as { [k: string]: unknown };
    class FakeEventSource {
      listeners: Record<string, Array<(event: MessageEvent) => void>> = {};
      url: string;
      readyState = 1;
      constructor(url: string) {
        this.url = url;
        const list = (w.__sseInstances as FakeEventSource[] | undefined) ?? [];
        list.push(this);
        w.__sseInstances = list;
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

interface StallGate {
  release: () => void;
  released: Promise<void>;
}

function newGate(): StallGate {
  let release: () => void = () => {};
  const released = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { release, released };
}

async function setupBackendMocks(
  page: Page,
  gate: StallGate,
  canonicalReply: string,
): Promise<void> {
  const messagesBySession: Record<
    string,
    Array<{
      id: string;
      role: string;
      content: string;
      timestamp: string;
      status?: string;
    }>
  > = {
    "session-1": [],
  };

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok" }),
    });
  });
  await page.route("**/api/agents", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([
        {
          id: "agent-1",
          name: "Agent One",
          description: "x",
          model: "claude-sonnet-4-6",
        },
      ]),
    });
  });
  await page.route("**/api/v1/models", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ providers: [] }),
    });
  });
  await page.route("**/api/v1/sessions", async (route) => {
    if (route.request().method() === "POST") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ id: "session-1", agentId: "agent-1" }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([
        {
          id: "session-1",
          agentId: "agent-1",
          currentAgentId: "agent-1",
          title: "Stall recovery",
          createdAt: "2026-05-04T00:00:00Z",
          updatedAt: "2026-05-04T00:00:00Z",
          messageCount: messagesBySession["session-1"].length,
        },
      ]),
    });
  });

  let postFetchCount = 0;
  await page.route("**/api/v1/sessions/*/messages", async (route) => {
    const url = route.request().url();
    const sessionId =
      url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? "session-1";

    if (route.request().method() === "POST") {
      // Hold open until the test releases the gate so the bubble appears
      // mid-stream from SSE only (matches real backend timing).
      await gate.released;
      messagesBySession[sessionId] = [
        {
          id: "srv-u1",
          role: "user",
          content: "long task",
          timestamp: "2026-05-04T00:00:00Z",
        },
        {
          id: "srv-a1",
          role: "assistant",
          content: canonicalReply,
          timestamp: "2026-05-04T00:00:02Z",
        },
      ];
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          id: sessionId,
          agentId: "agent-1",
          messages: messagesBySession[sessionId],
          messageCount: messagesBySession[sessionId].length,
          createdAt: "2026-05-04T00:00:00Z",
          updatedAt: new Date().toISOString(),
        }),
      });
      return;
    }
    // GET. Watchdog-trip reconcile fires this — it must return the
    // canonical completed reply so the bubble can recover from the
    // frozen partial.
    postFetchCount += 1;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(messagesBySession[sessionId] ?? []),
    });
  });
  await page.route("**/api/swarm/events", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([]),
    });
  });
  // Pin the variable so the linter doesn't complain about it being unused —
  // the count is observable via the test asserts on the bubble update.
  void postFetchCount;
}

test.describe("Stream stall recovery — watchdog trip reconciles to canonical", () => {
  test("a stalled SSE recovers to backend canonical state when the watchdog trips", async ({
    page,
  }) => {
    // Install fake clock BEFORE the page loads so the chatStore's
    // setTimeout (used by useSessionStream's armWatchdog) is captured by
    // the fake clock. We can then fast-forward past the 60s threshold
    // without burning real time.
    await page.clock.install({ time: new Date("2026-05-04T00:00:00Z") });
    await installFakeSSE(page);
    const gate = newGate();
    const canonicalReply =
      "recovered: the canonical completed reply backend persisted";
    await setupBackendMocks(page, gate, canonicalReply);

    await page.addInitScript(() => {
      window.localStorage.removeItem("chat.currentSessionId");
    });
    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

    const input = page.getByTestId("message-input");
    await input.fill("long task");
    await page.getByTestId("send-button").click();

    // Wait for the SSE for the send. waitForFunction does not advance the
    // fake clock, but the polling loop does run on real time.
    await page.waitForFunction(
      () => {
        const w = window as unknown as {
          __sseDriver?: { instances: () => unknown[] };
        };
        return (w.__sseDriver?.instances().length ?? 0) >= 1;
      },
      undefined,
      { timeout: 5000 },
    );

    // Deliver a partial chunk, then DELIBERATELY no [DONE] — simulate the
    // proxy hanging mid-stream. Backend has the full canonical answer
    // (programmed into the GET /messages mock) but SSE never delivers it.
    await page.evaluate(() => {
      const w = window as unknown as {
        __sseDriver: { instances: () => Array<SSEChannel> };
      };
      const es = w.__sseDriver.instances()[0];
      es.fire("message", JSON.stringify({ content: "partial frozen " }));
    });

    // Mid-stream snapshot: the bubble shows the partial.
    const lastAssistant = page.getByTestId("message-assistant").last();
    await expect(lastAssistant).toContainText("partial frozen");

    // Release the POST so the reconcile fired by the watchdog can read
    // canonical state. The POST handler is awaiting the gate before
    // returning, so without releasing it the GET that reconcile triggers
    // would race with an in-flight POST and the test could be flaky.
    gate.release();

    // Fast-forward 61s of fake clock — past the 60s SSE_STALL_TIMEOUT_MS.
    // The watchdog setTimeout fires synchronously inside this advance, which
    // calls handleStreamStall → reconcileFromBackend. The reconcile's
    // microtask chain (await fetchSessionMessages) settles via real-time
    // microtask flushing.
    await page.clock.fastForward(61_000);

    // Post-fix the bubble updates to the canonical reply rather than
    // staying stuck on the partial. Pre-fix the watchdog only cleared the
    // gate — the bubble stayed frozen on 'partial frozen'.
    await expect(lastAssistant).toContainText(canonicalReply, {
      timeout: 5000,
    });
  });
});
