import { test, expect, Page } from "@playwright/test";

/**
 * Live UI verification for the context_usage chip affordance.
 *
 * The chip is the chat UI's response to the engine's `context_usage`
 * SSE event class (Phase 2 of the May 2026 context-window saturation
 * fix — see Bug Fixes / glm Context-Window Saturation Detection in
 * the vault).
 *
 * These specs ride the production code path end-to-end:
 *   - Real Pinia store, real Vue components, real DOM.
 *   - SSE delivery is via a FakeEventSource swap-in (same pattern as
 *     critical-error-banner.spec.ts) so the test can `fire()` arbitrary
 *     chunk payloads on the live connection without standing up a real
 *     Go backend.
 *   - The chunk JSON is the literal wire shape emitted by
 *     writeSSEContextUsage (see internal/api/server.go).
 *
 * Behaviour pinned (assertions are user-observable, not internal):
 *   - Chip is permanently visible whenever a model is selected
 *     (Phase 3 — TUI-cadence parity, May 2026 follow-up). Empty
 *     state shows `—/—` placeholder before any context_usage chunk
 *     lands.
 *   - Chip renders the formatted figure after a usage chunk lands.
 *   - Severity classes pivot at the 75% / 90% thresholds.
 *   - A degraded payload (limit=0) falls back to the empty-state
 *     placeholder rather than rendering a misleading `1234/0`.
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

async function bootstrapMocks(page: Page): Promise<void> {
  const messages = [
    {
      id: "s1-u",
      role: "user",
      content: "hello",
      timestamp: "2026-05-08T00:00:00Z",
    },
    {
      id: "s1-a",
      role: "assistant",
      content: "world",
      timestamp: "2026-05-08T00:00:01Z",
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
      body: JSON.stringify({
        providers: [
          {
            id: "anthropic",
            name: "Anthropic",
            models: [{ id: "claude-sonnet-4-6", name: "Claude Sonnet 4.6" }],
          },
        ],
      }),
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
          // Phase 3 — chip visibility predicate requires a selected
          // model. Seed the session with a default pair so the chip
          // mounts with the empty-state placeholder before any
          // context_usage chunk arrives.
          currentProviderId: "anthropic",
          currentModelId: "claude-sonnet-4-6",
          title: "Test",
          createdAt: "2026-05-08T00:00:00Z",
          updatedAt: "2026-05-08T00:00:01Z",
          messageCount: messages.length,
        },
      ]),
    });
  });
  await page.route("**/api/v1/sessions/*/messages", async (route) => {
    if (route.request().method() === "POST") {
      // Hold the POST open so the production sendMessage finally-block
      // does not disconnect the SSE before our chunks land.
      await new Promise((resolve) => setTimeout(resolve, 30000));
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          id: "session-1",
          agentId: "agent-1",
          messages,
          messageCount: messages.length,
          createdAt: "2026-05-08T00:00:00Z",
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

  await page.getByTestId("message-input").fill("drive a usage event");
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

const DEV_BASE_URL =
  process.env["CONTEXT_USAGE_BASE_URL"] ?? "http://localhost:5173";
test.use({ baseURL: DEV_BASE_URL });

test.describe("Context usage chip — live UI", () => {
  test.beforeEach(async ({ page }) => {
    await installFakeSSE(page);
    await bootstrapMocks(page);
  });

  test("chip shows empty-state placeholder before any context_usage chunk and renders the figure once one lands", async ({
    page,
  }) => {
    await gotoChatAndOpenStream(page);

    // Phase 3 — chip is permanently visible whenever a model is
    // selected. Pre-Phase-3 the chip stayed hidden until the first
    // pre-send context_usage event landed; the user reopening a
    // session saw a blank toolbar until they typed.
    const chip = page.locator('[data-testid="context-usage-chip"]');
    await expect(chip).toBeVisible();
    await expect(chip).toHaveAttribute("data-severity", "neutral");
    await expect(
      page.locator('[data-testid="context-usage-counts"]'),
    ).toContainText("—/—");

    await fireSSE(
      page,
      JSON.stringify({
        type: "context_usage",
        input_tokens: 12345,
        output_reserve: 4096,
        limit: 100000,
        percentage: 12,
        provider: "zai",
        model: "glm-4.6",
      }),
    );

    await expect(chip).toHaveAttribute("role", "status");
    await expect(
      page.locator('[data-testid="context-usage-counts"]'),
    ).toContainText("12K/100K");
    await expect(
      page.locator('[data-testid="context-usage-percentage"]'),
    ).toContainText("12%");
  });

  test("chip pivots to warning severity at 75%+ and danger at 90%+", async ({
    page,
  }) => {
    await gotoChatAndOpenStream(page);

    // 80% — warning threshold.
    await fireSSE(
      page,
      JSON.stringify({
        type: "context_usage",
        input_tokens: 80000,
        output_reserve: 4096,
        limit: 100000,
        percentage: 80,
        provider: "zai",
        model: "glm-4.6",
      }),
    );

    const chip = page.locator('[data-testid="context-usage-chip"]');
    await expect(chip).toHaveAttribute("data-severity", "warning");

    // 95% — danger threshold (matches CriticalErrorBanner red palette).
    await fireSSE(
      page,
      JSON.stringify({
        type: "context_usage",
        input_tokens: 95000,
        output_reserve: 4096,
        limit: 100000,
        percentage: 95,
        provider: "zai",
        model: "glm-4.6",
      }),
    );

    await expect(chip).toHaveAttribute("data-severity", "danger");

    // The danger palette renders the same red as CriticalErrorBanner
    // — pin the computed colour so a future restyle is caught.
    const colour = await chip.evaluate(
      (el) => window.getComputedStyle(el).color,
    );
    expect(colour).toContain("220, 38, 38");
  });

  test("a degraded payload (limit=0) falls back to the empty-state placeholder", async ({
    page,
  }) => {
    // Defensive guard pinned by ContextUsageChip.spec.ts. The engine
    // suppresses the chunk when limit<=0 so this should never reach
    // the chip in practice; the chip's own guard catches a future
    // emitter regression. With Phase 3's always-visible behaviour
    // we no longer hide the chip — it falls back to the empty-state
    // placeholder so the affordance remains present without
    // misleading the user with `1234/0`.
    await gotoChatAndOpenStream(page);

    await fireSSE(
      page,
      JSON.stringify({
        type: "context_usage",
        input_tokens: 1234,
        output_reserve: 4096,
        limit: 0,
        percentage: 0,
        provider: "zai",
        model: "glm-4.6",
      }),
    );

    const chip = page.locator('[data-testid="context-usage-chip"]');
    await expect(chip).toBeVisible();
    await expect(
      page.locator('[data-testid="context-usage-counts"]'),
    ).toContainText("—/—");
  });

  test("chip is visible on initial page load before any send (Phase 3 — always-visible)", async ({
    page,
  }) => {
    // The chip must be present the moment the user opens the
    // session, matching the TUI's StatusBar (always visible). This
    // closes the regression where the user could open a session and
    // see a blank toolbar slot until they typed.
    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

    const chip = page.locator('[data-testid="context-usage-chip"]');
    await expect(chip).toBeVisible();
    await expect(chip).toHaveAttribute("data-severity", "neutral");
    await expect(
      page.locator('[data-testid="context-usage-counts"]'),
    ).toContainText("—/—");
  });

  test("chip updates the rendered figure as successive context_usage chunks arrive", async ({
    page,
  }) => {
    await gotoChatAndOpenStream(page);

    // First turn — small.
    await fireSSE(
      page,
      JSON.stringify({
        type: "context_usage",
        input_tokens: 1000,
        output_reserve: 4096,
        limit: 100000,
        percentage: 1,
        provider: "zai",
        model: "glm-4.6",
      }),
    );

    await expect(
      page.locator('[data-testid="context-usage-percentage"]'),
    ).toContainText("1%");

    // Second turn — saturating.
    await fireSSE(
      page,
      JSON.stringify({
        type: "context_usage",
        input_tokens: 92000,
        output_reserve: 4096,
        limit: 100000,
        percentage: 92,
        provider: "zai",
        model: "glm-4.6",
      }),
    );

    await expect(
      page.locator('[data-testid="context-usage-percentage"]'),
    ).toContainText("92%");
    await expect(
      page.locator('[data-testid="context-usage-chip"]'),
    ).toHaveAttribute("data-severity", "danger");
  });
});
