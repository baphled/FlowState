import { test, expect, Page } from "@playwright/test";

/**
 * Live UI verification for the stream_critical banner affordance.
 *
 * The banner is the chat UI's response to the engine's `stream_critical`
 * SSE event class (revoked OAuth, 401, model-not-found, billing/quota
 * lockout — see Bug Fixes / Critical Stream Error Gating in the vault).
 *
 * These specs ride the production code path end-to-end:
 *   - Real Pinia store, real Vue components, real DOM.
 *   - SSE delivery is via a FakeEventSource swap-in (same pattern used
 *     by the existing cross-session-streaming spec) so the test can
 *     `fire()` arbitrary chunk payloads on the live connection without
 *     standing up a real Go backend.
 *   - The chunk JSON is the literal wire shape emitted by writeSSEErrorMsg
 *     for the "stream_critical" category (see internal/api/errors.go).
 *
 * Behaviour pinned (assertions are user-observable, not internal):
 *   - Banner visible after a critical chunk; hidden before.
 *   - role="alert" for screen-reader announcement.
 *   - Sanitized message rendered.
 *   - "Show details" reveals the correlation id, hidden by default.
 *   - "Dismiss" clears the banner.
 *   - A fresh critical chunk after dismissal re-shows the banner with
 *     the new correlation id.
 *   - A transient stream_error chunk does NOT trigger the banner
 *     (regression-resistance for the criticality gate).
 *   - Visual distinction (red severity palette) — pinned via computed
 *     background-colour to catch a future restyle that drops red.
 */

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

/**
 * Holds the POST /messages response until the test releases it. The
 * production sendMessage flow disconnects the SSE in its `finally`
 * block once the POST resolves, so a test that needs to fire chunks
 * over the live SSE must hold the POST open while doing so. The page
 * can release via the `__releasePostMessages` hook installed by
 * bootstrapMocks; we don't actually use the release in these specs
 * (we just want the SSE to stay open through assertion + dismiss).
 */
async function bootstrapMocks(page: Page): Promise<void> {
  const messages = [
    {
      id: "s1-u",
      role: "user",
      content: "hello",
      timestamp: "2026-05-07T00:00:00Z",
    },
    {
      id: "s1-a",
      role: "assistant",
      content: "world",
      timestamp: "2026-05-07T00:00:01Z",
      status: "completed",
    },
  ];
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: '{"status":"ok"}',
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
      body: '{"providers":[]}',
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
          title: "Test",
          createdAt: "2026-05-07T00:00:00Z",
          updatedAt: "2026-05-07T00:00:01Z",
          messageCount: messages.length,
        },
      ]),
    });
  });
  await page.route("**/api/v1/sessions/*/messages", async (route) => {
    if (route.request().method() === "POST") {
      // Hold the POST open. The production sendMessage finally-block
      // calls sessionStream.disconnect() the moment the POST resolves,
      // which would close the live SSE we want to fire chunks on. By
      // delaying the response until well after the test's assertions
      // finish, the SSE listeners stay attached and our fired chunks
      // reach applyContentEvent.
      await new Promise((resolve) => setTimeout(resolve, 30000));
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          id: "session-1",
          agentId: "agent-1",
          messages,
          messageCount: messages.length,
          createdAt: "2026-05-07T00:00:00Z",
          updatedAt: new Date().toISOString(),
        }),
      });
      return;
    }
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
      body: "[]",
    });
  });
  await page.addInitScript(() => {
    window.localStorage.setItem("chat.currentSessionId", "session-1");
  });
}

async function gotoChatAndOpenStream(page: Page): Promise<SSEChannel> {
  await page.goto("/chat");
  await expect(page.getByTestId("message-input")).toBeVisible();

  await page.getByTestId("message-input").fill("trigger a fatal error");
  await page.getByTestId("send-button").click();

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

  return page.evaluateHandle(() => {
    const w = window as unknown as {
      __sseDriver: { instances: () => SSEChannel[] };
    };
    return w.__sseDriver.instances()[0];
  }) as unknown as Promise<SSEChannel>;
}

async function fireSSE(page: Page, payload: object | string): Promise<void> {
  await page.evaluate((data) => {
    const w = window as unknown as {
      __sseDriver: {
        instances: () => Array<{ fire: (t: string, d: unknown) => void }>;
      };
    };
    const es = w.__sseDriver.instances()[w.__sseDriver.instances().length - 1];
    es.fire("message", data);
  }, payload);
}

// Allow the live-verification dev server port to be overridden (the
// shared dev port 5173 may be claimed by another worktree). Default
// matches the playwright.config baseURL.
const DEV_BASE_URL =
  process.env["CRITICAL_BANNER_BASE_URL"] ?? "http://localhost:5173";
test.use({ baseURL: DEV_BASE_URL });

test.describe("Critical error banner — live UI", () => {
  test.beforeEach(async ({ page }) => {
    await installFakeSSE(page);
    await bootstrapMocks(page);
  });

  test("banner is hidden before any event and renders on a stream_critical chunk", async ({
    page,
  }) => {
    await gotoChatAndOpenStream(page);

    // Hidden before any critical event lands on the open SSE.
    await expect(
      page.locator('[data-testid="critical-error-banner"]'),
    ).toHaveCount(0);

    await fireSSE(
      page,
      JSON.stringify({
        error: "critical stream error",
        correlation_id: "live-id-1",
      }),
    );

    const banner = page.locator('[data-testid="critical-error-banner"]');
    await expect(banner).toBeVisible();
    await expect(banner).toHaveAttribute("role", "alert");
    await expect(
      page.locator('[data-testid="critical-error-message"]'),
    ).toContainText("critical stream error");
  });

  test("Show details reveals the correlation id and Dismiss clears the banner", async ({
    page,
  }) => {
    await gotoChatAndOpenStream(page);
    await fireSSE(
      page,
      JSON.stringify({
        error: "critical stream error",
        correlation_id: "live-id-2",
      }),
    );

    await expect(
      page.locator('[data-testid="critical-error-banner"]'),
    ).toBeVisible();
    await expect(
      page.locator('[data-testid="critical-error-correlation-id"]'),
    ).toHaveCount(0);

    await page.locator('[data-testid="critical-error-details-toggle"]').click();
    await expect(
      page.locator('[data-testid="critical-error-correlation-id"]'),
    ).toHaveText("live-id-2");

    await page.locator('[data-testid="critical-error-dismiss"]').click();
    await expect(
      page.locator('[data-testid="critical-error-banner"]'),
    ).toHaveCount(0);
  });

  test("a fresh stream_critical event re-shows the banner after dismissal with the new id", async ({
    page,
  }) => {
    await gotoChatAndOpenStream(page);

    await fireSSE(
      page,
      JSON.stringify({
        error: "critical stream error",
        correlation_id: "first-live-id",
      }),
    );
    await page.locator('[data-testid="critical-error-dismiss"]').click();
    await expect(
      page.locator('[data-testid="critical-error-banner"]'),
    ).toHaveCount(0);

    await fireSSE(
      page,
      JSON.stringify({
        error: "critical stream error",
        correlation_id: "second-live-id",
      }),
    );

    await expect(
      page.locator('[data-testid="critical-error-banner"]'),
    ).toBeVisible();
    await page.locator('[data-testid="critical-error-details-toggle"]').click();
    await expect(
      page.locator('[data-testid="critical-error-correlation-id"]'),
    ).toHaveText("second-live-id");
  });

  test("a transient stream_error event does NOT render the critical banner", async ({
    page,
  }) => {
    await gotoChatAndOpenStream(page);

    await fireSSE(
      page,
      JSON.stringify({ error: "stream error", correlation_id: "transient-id" }),
    );

    await expect(
      page.locator('[data-testid="critical-error-banner"]'),
    ).toHaveCount(0);
  });

  test("banner uses the red severity palette (computed background)", async ({
    page,
  }) => {
    await gotoChatAndOpenStream(page);
    await fireSSE(
      page,
      JSON.stringify({
        error: "critical stream error",
        correlation_id: "visual-id",
      }),
    );

    const banner = page.locator('[data-testid="critical-error-banner"]');
    await expect(banner).toBeVisible();

    // Pin the visual distinction — rgba(220, 38, 38, …) is the red
    // severity palette also used by the toast --error variant. A
    // future restyle that drops the red severity is caught.
    const bg = await banner.evaluate(
      (el) => window.getComputedStyle(el).backgroundColor,
    );
    expect(bg).toContain("220, 38, 38");

    // Capture an evidence screenshot for the report.
    await banner.screenshot({ path: "test-results/critical-error-banner.png" });
  });
});
