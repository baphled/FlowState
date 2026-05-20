import { test, expect, Page } from "@playwright/test";

// Live verification of the multi-turn streaming bug:
// "the session keeps upserting the last agent response, instead of updating
// with the new response to the last user prompt."
//
// We replace the page's EventSource with a controllable fake so the test can
// drive realistic chunk timing for two consecutive turns. The REST endpoints
// (sessions, messages POST/GET, agents) are mocked via page.route().
//
// Crucially, the POST /messages route is gated: each call holds open until
// the test releases it. This lets us deliver the SSE chunks for that turn
// BEFORE the POST returns, matching the real backend's timing where chunks
// stream during the same handler that returns the canonical session.
// (See internal/api/server.go handleSessionMessage — the POST blocks on
// SendMessage which iterates the chunk channel; the broker publishes each
// chunk to the SSE stream as it goes.)
//
// Pre-fix on 8e06007 the second turn's chunks land on the previous turn's
// assistant message because handleContentChunk treats any assistant with
// status !== 'completed' as the streaming target — and backend-loaded rows
// have status === undefined.

const TURN_1_RESPONSE = "first response";
const TURN_2_RESPONSE = "second response";

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

async function setupMocks(
  page: Page,
  gates: { turn1: PostGate; turn2: PostGate },
): Promise<void> {
  const messagesBySession: Record<
    string,
    Array<{ id: string; role: string; content: string; timestamp: string }>
  > = {
    "session-1": [],
  };

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
          title: "Test session",
          createdAt: "2026-05-04T00:00:00Z",
          updatedAt: "2026-05-04T00:00:00Z",
          messageCount: messagesBySession["session-1"]?.length ?? 0,
        },
      ]),
    });
  });

  let postCallCount = 0;
  await page.route("**/api/v1/sessions/*/messages", async (route) => {
    const url = route.request().url();
    const sessionId =
      url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? "session-1";

    if (route.request().method() === "POST") {
      postCallCount += 1;
      const turn = postCallCount;
      const body = route.request().postDataJSON() as { content?: string };
      const userId = `srv-u${turn}`;
      const assistantId = `srv-a${turn}`;
      const responseText = turn === 1 ? TURN_1_RESPONSE : TURN_2_RESPONSE;
      const gate = turn === 1 ? gates.turn1 : gates.turn2;

      // Hold the POST until the test releases the gate, after delivering
      // the SSE chunks for this turn. Real backend timing: chunks stream
      // during the same handler that returns the canonical session.
      await gate.released;

      messagesBySession[sessionId] = [
        ...(messagesBySession[sessionId] ?? []),
        {
          id: userId,
          role: "user",
          content: body.content ?? "",
          timestamp: new Date().toISOString(),
        },
        {
          id: assistantId,
          role: "assistant",
          content: responseText,
          timestamp: new Date().toISOString(),
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
}

test.describe("Multi-turn streaming — second turn must not overwrite first", () => {
  test("two consecutive turns render two distinct assistant bubbles, even after refetch", async ({
    page,
  }) => {
    const gates = { turn1: newGate(), turn2: newGate() };
    await setupMocks(page, gates);

    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

    const input = page.getByTestId("message-input");
    await input.fill("first prompt");
    await page.getByTestId("send-button").click();

    // Wait for turn 1's EventSource to be created.
    await page.waitForFunction(() => {
      return (
        (
          window as unknown as { __sseDriver?: { instances: () => unknown[] } }
        ).__sseDriver?.instances().length === 1
      );
    });

    // Deliver turn 1 chunks. Release the POST gate BEFORE [DONE] so that
    // when [DONE] triggers the post-stream reconcile (PR-2 behaviour), the
    // backend has persisted the canonical history. Real backend timing:
    // the POST handler publishes the chunk channel to the broker then
    // returns, while chunks continue flowing over SSE — the chunk-channel
    // close (which produces [DONE]) and the POST return are asynchronous
    // events, but the persistence layer is settled by the time both occur.
    await page.evaluate(() => {
      const driver = (
        window as unknown as {
          __sseDriver: {
            instances: () => Array<{ fire: (t: string, d: unknown) => void }>;
          };
        }
      ).__sseDriver;
      const es = driver.instances()[0];
      es.fire("message", JSON.stringify({ content: "first " }));
      es.fire("message", JSON.stringify({ content: "response" }));
    });
    gates.turn1.release();
    // Wait for the POST to land so messagesBySession reflects turn-1's
    // persisted state before we fire [DONE] and trigger the reconcile.
    await page.waitForResponse(
      (res) =>
        res.request().method() === "POST" && /\/messages$/.test(res.url()),
    );
    await page.evaluate(() => {
      const driver = (
        window as unknown as {
          __sseDriver: {
            instances: () => Array<{ fire: (t: string, d: unknown) => void }>;
          };
        }
      ).__sseDriver;
      const es = driver.instances()[0];
      es.fire("message", "[DONE]");
    });

    // Turn 1 settled: 1 assistant bubble with the first response.
    await expect(page.getByTestId("message-assistant")).toHaveCount(1);
    await expect(page.getByTestId("message-assistant").nth(0)).toContainText(
      TURN_1_RESPONSE,
    );

    // Turn 2: send second prompt; the bug-prone path is the very first
    // chunk arriving while the previous turn's assistant message is still
    // in the messages array (it now has status === undefined from refetch).
    await input.fill("second prompt");
    await page.getByTestId("send-button").click();

    await page.waitForFunction(() => {
      return (
        (
          window as unknown as { __sseDriver?: { instances: () => unknown[] } }
        ).__sseDriver?.instances().length === 2
      );
    });

    // Deliver turn 2 chunks while the POST is pending (this is the
    // mid-stream window the user sees).
    await page.evaluate(() => {
      const driver = (
        window as unknown as {
          __sseDriver: {
            instances: () => Array<{ fire: (t: string, d: unknown) => void }>;
          };
        }
      ).__sseDriver;
      const es = driver.instances()[1];
      es.fire("message", JSON.stringify({ content: "second " }));
      es.fire("message", JSON.stringify({ content: "response" }));
    });

    // Mid-stream snapshot — BEFORE [DONE] and BEFORE refetch settles.
    // Pre-fix: turn 1's bubble has been corrupted into 'first responsesecond response'.
    // Post-fix: turn 1's bubble is still TURN_1_RESPONSE alone.
    const firstAssistant = page.getByTestId("message-assistant").nth(0);
    await expect(firstAssistant).toContainText(TURN_1_RESPONSE);
    await expect(firstAssistant).not.toContainText(TURN_2_RESPONSE);

    // A second assistant bubble exists for turn 2.
    await expect(page.getByTestId("message-assistant")).toHaveCount(2);
    await expect(page.getByTestId("message-assistant").nth(1)).toContainText(
      TURN_2_RESPONSE,
    );

    // Now finish turn 2. Same ordering as turn 1: release the POST first
    // so the canonical history is settled before [DONE] triggers the
    // post-stream reconcile.
    gates.turn2.release();
    await page.waitForResponse(
      (res) =>
        res.request().method() === "POST" && /\/messages$/.test(res.url()),
    );
    await page.evaluate(() => {
      const driver = (
        window as unknown as {
          __sseDriver: {
            instances: () => Array<{ fire: (t: string, d: unknown) => void }>;
          };
        }
      ).__sseDriver;
      const es = driver.instances()[1];
      es.fire("message", "[DONE]");
    });

    // After settle: still two distinct bubbles.
    await expect(page.getByTestId("message-assistant")).toHaveCount(2);
    await expect(page.getByTestId("message-assistant").nth(0)).toContainText(
      TURN_1_RESPONSE,
    );
    await expect(
      page.getByTestId("message-assistant").nth(0),
    ).not.toContainText(TURN_2_RESPONSE);
    await expect(page.getByTestId("message-assistant").nth(1)).toContainText(
      TURN_2_RESPONSE,
    );
  });
});
