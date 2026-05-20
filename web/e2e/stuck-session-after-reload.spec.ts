import { test, expect, Page } from "@playwright/test";

// Live verification of the "stuck session after reload" bug.
//
// Reload during a streaming response → frontend never reattaches the SSE
// consumer → backend chunks vanish → user thinks the chat is frozen and
// types "continue" → the submit gate silently early-returns because
// store.isLoading was left true (or the next prompt is sent but no chunks
// reach the UI because no SSE subscription exists). Either way the user
// sees nothing.
//
// This spec drives the full user-visible flow against a deterministic
// fake SSE backend so the bug shows up exactly as the researcher's
// session capture documented it.
//
// Pre-fix on a1df544: after reload the prior assistant bubble shows the
// partial "first " text and never updates with chunks emitted after the
// reload. Typing "continue" results in no new assistant bubble and no
// surfacing — silent failure.
//
// Post-fix: the SSE consumer reattaches on restoreStateFromBackend, the
// chunks emitted post-reload land on the in-flight assistant, and any
// subsequent retype while the flow remains in flight is surfaced via a
// toast rather than dropped silently.

interface SSEChannel {
  fire: (type: string, data: unknown) => void;
  url: string;
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

async function setupBackendMocks(page: Page, gate: PostGate): Promise<void> {
  // The most-recent assistant is "running" with the partial chunk that
  // arrived BEFORE the reload. After reload, the SSE consumer must
  // reattach so the post-reload chunks land on that message.
  // After the SSE [DONE] fires, the post-PR-2 reconcile reads canonical
  // history again — by then the backend has persisted the full streamed
  // content. Tests can call __markCompleted() to flip the GET response
  // to the completed canonical text.
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
    "session-1": [
      {
        id: "srv-u1",
        role: "user",
        content: "tell me a story",
        timestamp: "2026-05-04T00:00:00Z",
      },
      {
        id: "srv-a1",
        role: "assistant",
        content: "first ",
        timestamp: "2026-05-04T00:00:01Z",
        status: "running",
      },
    ],
  };
  let useCompletedHistory = false;
  let completedHistoryFor: string = "";
  await page.exposeFunction(
    "__markCompleted",
    (sessionId: string, finalContent: string) => {
      useCompletedHistory = true;
      completedHistoryFor = sessionId;
      messagesBySession[sessionId] = [
        {
          id: "srv-u1",
          role: "user",
          content: "tell me a story",
          timestamp: "2026-05-04T00:00:00Z",
        },
        {
          id: "srv-a1",
          role: "assistant",
          content: finalContent,
          timestamp: "2026-05-04T00:00:02Z",
        },
      ];
    },
  );
  void useCompletedHistory;
  void completedHistoryFor;

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
          title: "Stuck session",
          createdAt: "2026-05-04T00:00:00Z",
          updatedAt: "2026-05-04T00:00:01Z",
          messageCount: messagesBySession["session-1"].length,
        },
      ]),
    });
  });

  await page.route("**/api/v1/sessions/*/messages", async (route) => {
    const url = route.request().url();
    const sessionId =
      url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? "session-1";
    if (route.request().method() === "POST") {
      const body = route.request().postDataJSON() as { content?: string };
      // Hold open until the test releases the gate so chunks can arrive
      // first, mirroring the real backend timing.
      await gate.released;
      messagesBySession[sessionId] = [
        ...messagesBySession[sessionId],
        {
          id: `srv-u${Date.now()}`,
          role: "user",
          content: body.content ?? "",
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

test.describe("Stuck session after reload — SSE reconnect + visible submit gate", () => {
  test("post-reload SSE chunks reach the in-flight assistant message", async ({
    page,
  }) => {
    await installFakeSSE(page);
    const gate = newGate();
    await setupBackendMocks(page, gate);

    // Seed the persisted session id so restoreStateFromBackend reattaches
    // to session-1 the moment ChatView mounts.
    await page.addInitScript(() => {
      window.localStorage.setItem("chat.currentSessionId", "session-1");
    });

    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

    // The restored history shows the partial response carried by the
    // backend. Pre-fix this is all the user ever sees because no SSE
    // consumer is attached.
    const firstAssistant = page.getByTestId("message-assistant").first();
    await expect(firstAssistant).toContainText("first");

    // Wait for the SSE reconnect — pre-fix this never happens, post-fix
    // restoreStateFromBackend must subscribe because srv-a1 is "running".
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

    // Backend resumes streaming: the chunks the user missed during reload.
    // Mark the canonical history as completed BEFORE [DONE] fires so the
    // post-PR-2 reconcile (triggered by [DONE]) reads the full text rather
    // than the original mid-stream snapshot — real backend timing: the
    // persistence layer is settled before the broker emits [DONE].
    await page.evaluate(() => {
      (
        window as unknown as {
          __markCompleted: (id: string, c: string) => void;
        }
      ).__markCompleted("session-1", "first response continued");
    });
    await page.evaluate(() => {
      const w = window as unknown as {
        __sseDriver: { instances: () => Array<SSEChannel> };
      };
      const es = w.__sseDriver.instances()[0];
      es.fire("message", JSON.stringify({ content: "response " }));
      es.fire("message", JSON.stringify({ content: "continued" }));
      es.fire("message", "[DONE]");
    });

    // Post-fix: the in-flight bubble updates — first via SSE chunks, then
    // confirmed by the post-[DONE] reconcile against canonical history.
    await expect(firstAssistant).toContainText("first response continued");

    // Tidy: release the unused gate to let the test exit.
    gate.release();
  });

  test("typing while a send is still pending surfaces a toast — no silent drop", async ({
    page,
  }) => {
    await installFakeSSE(page);
    const gate = newGate();
    await setupBackendMocks(page, gate);

    // Start with no persisted in-flight session — fresh chat.
    await page.addInitScript(() => {
      window.localStorage.removeItem("chat.currentSessionId");
    });
    // Override so this test starts with a clean slate (no running assistant).
    await page.route("**/api/v1/sessions/*/messages", async (route) => {
      const url = route.request().url();
      const sessionId =
        url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? "session-1";
      if (route.request().method() === "POST") {
        await gate.released;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            id: sessionId,
            agentId: "agent-1",
            messages: [],
            messageCount: 0,
            createdAt: "2026-05-04T00:00:00Z",
            updatedAt: new Date().toISOString(),
          }),
        });
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([]),
      });
    });

    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

    const input = page.getByTestId("message-input");
    await input.fill("first prompt");
    await page.getByTestId("send-button").click();

    // Wait for the SSE for the first send to be created — confirms
    // isLoading is true and the gate is closed for any second submission.
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

    // While the first send is still pending (gate not released, no [DONE]),
    // the user types another prompt and presses Enter. Pre-fix: silent drop.
    // Post-fix: a toast surfaces the rejection.
    await input.fill("continue");
    await input.press("Enter");

    // The toast container is the canonical surfacing seam; scope to it so
    // we don't pick up any other transient indicators.
    const toast = page
      .getByTestId("toast-container")
      .getByTestId("toast-item")
      .first();
    await expect(toast).toBeVisible({ timeout: 3000 });
    await expect(toast).toContainText(/(in.flight|already|wait|reload)/i);

    // Tidy.
    gate.release();
  });

  // PR-2 headline test. Designed to fail RED on PR-1 tip (4859133) — captured
  // RED output: bubble shows "partial fragment (a few words from SSE)" and
  // never updates with the canonical reply. Post-PR-2 the close-handler
  // unconditionally reconciles with the backend, so the bubble updates as
  // soon as [DONE] arrives without any user action.
  test("post-reload stream completes and reconciles to canonical without a second refresh", async ({
    page,
  }) => {
    await installFakeSSE(page);
    const gate = newGate();

    // Backend canonical state AFTER the stream completes — this is what the
    // post-fix reconcile pulls in to replace the frozen partial. Pre-fix
    // the user never sees this until they hit refresh.
    const canonicalCompletedReply =
      "this is the full canonical reply that the backend persisted after [DONE]";

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
      "session-1": [
        {
          id: "srv-u1",
          role: "user",
          content: "tell me a story",
          timestamp: "2026-05-04T00:00:00Z",
        },
        // Reload-time state: the partial chunk caught by the persistence
        // layer before the user reloaded. Marked 'running' so reattach fires.
        {
          id: "srv-a1",
          role: "assistant",
          content: "partial fragment",
          timestamp: "2026-05-04T00:00:01Z",
          status: "running",
        },
      ],
    };

    let postReloadFetchCount = 0;
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
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([
          {
            id: "session-1",
            agentId: "agent-1",
            currentAgentId: "agent-1",
            title: "Reload-then-complete",
            createdAt: "2026-05-04T00:00:00Z",
            updatedAt: "2026-05-04T00:00:01Z",
            messageCount: messagesBySession["session-1"].length,
          },
        ]),
      });
    });
    await page.route("**/api/v1/sessions/*/messages", async (route) => {
      const url = route.request().url();
      const sessionId =
        url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? "session-1";
      if (route.request().method() === "POST") {
        await gate.released;
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
      // GET. The first call (during restoreStateFromBackend after reload)
      // serves the partial. Every subsequent call (the post-[DONE]
      // reconcile) serves the canonical completed history.
      postReloadFetchCount += 1;
      const messages =
        postReloadFetchCount === 1
          ? messagesBySession[sessionId]
          : [
              {
                id: "srv-u1",
                role: "user",
                content: "tell me a story",
                timestamp: "2026-05-04T00:00:00Z",
              },
              {
                id: "srv-a1",
                role: "assistant",
                content: canonicalCompletedReply,
                timestamp: "2026-05-04T00:00:02Z",
              },
            ];
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(messages),
      });
    });
    await page.route("**/api/swarm/events", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([]),
      });
    });

    await page.addInitScript(() => {
      window.localStorage.setItem("chat.currentSessionId", "session-1");
    });

    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

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

    const firstAssistant = page.getByTestId("message-assistant").first();
    await expect(firstAssistant).toContainText("partial fragment");

    // Backend completes the response, then [DONE]. Post-fix the close
    // handler reconciles with the backend, replacing the partial with the
    // canonical completed text.
    await page.evaluate(() => {
      const w = window as unknown as {
        __sseDriver: { instances: () => Array<SSEChannel> };
      };
      const es = w.__sseDriver.instances()[0];
      es.fire(
        "message",
        JSON.stringify({ content: " (a few words from SSE)" }),
      );
      es.fire("message", "[DONE]");
    });

    // RED on PR-1 tip: bubble shows "partial fragment (a few words from SSE)"
    // and stays that way. Post-fix the reconcile after [DONE] pulls the
    // backend canonical reply and the bubble updates without manual refresh.
    await expect(firstAssistant).toContainText(canonicalCompletedReply, {
      timeout: 3000,
    });

    gate.release();
  });
});
