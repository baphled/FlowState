import { test, expect, Page } from "@playwright/test";

/**
 * Live UI verification for the PermissionModeChip (Permission Modes
 * (May 2026), Slice 2 + Slice 3).
 *
 * Behaviour pinned (user-observable, not internal):
 *   - Slice 2: The chip is visible in the composer toolbar on initial
 *     page load. Clicking opens the popover with the four canonical
 *     modes plus the loud-disclosure paragraph for Default mode
 *     (plan §5).
 *   - Slice 2: Selecting YOLO updates the chip label and the
 *     data-mode attribute reactively, and writes localStorage.
 *   - Slice 3: Selecting a mode also POSTs to the backend at
 *     `/api/v1/sessions/{id}/permission-mode` with `{"mode": "<value>"}`.
 *   - Slice 3: After a page reload, the chip restores from the backend
 *     payload (the session list's `permissionMode` field), winning
 *     over any stale localStorage value. This is the canonical
 *     precedence (backend > localStorage > default).
 *   - The screenshot snapshot of the chip with the popover open is
 *     written to /tmp/slice2-chip.png for the slice's evidence.
 *
 * Mocking patterns mirror `context-usage-chip.spec.ts`. Slice 3
 * adds a backend mock for the POST endpoint and threads the
 * `permissionMode` field through the session-list mock so the
 * reload-rehydration test exercises the backend-precedence path.
 */

interface BootstrapOptions {
  /**
   * Permission Modes (May 2026) Slice 3 — the value the session-list
   * mock should report for `permissionMode`. Drives the chip's
   * cold-load hydration via the backend-precedence path. Omit to
   * test the legacy / pre-Slice-1 absence shape.
   */
  sessionPermissionMode?: string;
  /**
   * Captures the latest POST body sent to the per-session
   * permission-mode endpoint. Tests assert against this to prove the
   * chip's selection round-tripped to the backend rather than only
   * writing to localStorage.
   */
  recordedPermissionModePosts?: { sessionId: string; body: string }[];
}

async function bootstrapMocks(
  page: Page,
  opts: BootstrapOptions = {},
): Promise<void> {
  const messages = [
    {
      id: "s1-u",
      role: "user",
      content: "hello",
      timestamp: "2026-05-26T00:00:00Z",
    },
    {
      id: "s1-a",
      role: "assistant",
      content: "world",
      timestamp: "2026-05-26T00:00:01Z",
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
    const summary: Record<string, unknown> = {
      id: "session-1",
      agentId: "agent-1",
      currentAgentId: "agent-1",
      currentProviderId: "anthropic",
      currentModelId: "claude-sonnet-4-6",
      title: "Test",
      createdAt: "2026-05-26T00:00:00Z",
      updatedAt: "2026-05-26T00:00:01Z",
      messageCount: messages.length,
    };
    if (opts.sessionPermissionMode !== undefined) {
      summary["permissionMode"] = opts.sessionPermissionMode;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([summary]),
    });
  });
  // Permission Modes Slice 3 — backend POST seam. The route MUST be
  // registered before `/api/v1/sessions/*` (the bare-session fall-through
  // could swallow a more specific path under Playwright's longest-match
  // semantics). Record the body so the spec can assert the chip's
  // selection round-tripped.
  await page.route(
    "**/api/v1/sessions/*/permission-mode",
    async (route, request) => {
      if (request.method() !== "POST") {
        await route.fulfill({ status: 405 });
        return;
      }
      const body = request.postData() ?? "";
      const match = request.url().match(/\/sessions\/([^/]+)\/permission-mode/);
      const sessionId = match ? decodeURIComponent(match[1]) : "";
      if (opts.recordedPermissionModePosts) {
        opts.recordedPermissionModePosts.push({ sessionId, body });
      }
      let mode = "";
      try {
        mode = (JSON.parse(body) as { mode?: string }).mode ?? "";
      } catch {
        // ignore — assertion below will catch a malformed body
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ id: sessionId, permission_mode: mode }),
      });
    },
  );
  await page.route("**/api/v1/sessions/*/messages", async (route) => {
    if (route.request().method() === "POST") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          id: "session-1",
          agentId: "agent-1",
          messages,
          messageCount: messages.length,
          createdAt: "2026-05-26T00:00:00Z",
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

const DEV_BASE_URL =
  process.env["PERMISSION_MODE_BASE_URL"] ?? "http://localhost:5173";
test.use({ baseURL: DEV_BASE_URL });

test.describe("Permission mode chip — live UI", () => {
  test("chip renders, opens the popover, selects YOLO, POSTs to the backend (Slice 3), and writes localStorage", async ({
    page,
  }) => {
    const recordedPosts: { sessionId: string; body: string }[] = [];
    await bootstrapMocks(page, { recordedPermissionModePosts: recordedPosts });

    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

    const chip = page.locator('[data-testid="permission-mode-chip"]');
    await expect(chip).toBeVisible();
    // First-launch default per plan §6 — `default` mode is the safe
    // baseline. Validating both the label and the data-mode attribute
    // guards against a future emoji-only redesign quietly weakening
    // the assertion surface.
    await expect(
      page.locator('[data-testid="permission-mode-chip-label"]'),
    ).toContainText("Default");
    await expect(chip).toHaveAttribute("data-mode", "default");

    // Click → popover opens with the five canonical modes. The fifth
    // ("Ask") row is the ModeAskUser Extension (May 2026) §3 surface
    // — interactive permission grants per call. The plan §2 tooltip
    // body is rendered directly in the option description so the
    // operator reads the semantics at the moment of choice.
    await chip.click();
    const popover = page.locator(
      '[data-testid="permission-mode-chip-popover"]',
    );
    await expect(popover).toBeVisible();
    await expect(
      page.locator('[data-testid="permission-mode-option-plan"]'),
    ).toBeVisible();
    await expect(
      page.locator('[data-testid="permission-mode-option-default"]'),
    ).toBeVisible();
    await expect(
      page.locator('[data-testid="permission-mode-option-accept_edits"]'),
    ).toBeVisible();
    await expect(
      page.locator('[data-testid="permission-mode-option-ask"]'),
    ).toBeVisible();
    await expect(
      page.locator('[data-testid="permission-mode-option-yolo"]'),
    ).toBeVisible();

    // ModeAskUser Extension (May 2026) §2 — pin the plan tooltip copy
    // on the Ask row so a future restyle can't quietly weaken the
    // operator-facing semantics. The full sentence is rendered in
    // the option-description span, not a hover-only `title`.
    await expect(
      page.locator('[data-testid="permission-mode-option-ask"]'),
    ).toContainText(
      "Pathguard prompts on denial. Operator grants per call. Per-resource grants persist to permissions.yaml.",
    );

    // Loud disclosure (plan §5) — exact copy pinned. The element is
    // present in the DOM (not buried behind a hover-only `title`)
    // exactly so screen readers + visual scanning both pick it up.
    const disclosure = page.locator(
      '[data-testid="permission-mode-default-disclosure"]',
    );
    await expect(disclosure).toBeVisible();
    await expect(disclosure).toHaveText(
      "Default mode does not prompt per tool call. Review the session timeline for what ran.",
    );

    // Capture the popover-open screenshot as Slice 2/3's evidence
    // before clicking through and dismissing the popover.
    await page.screenshot({ path: "/tmp/slice3-chip.png", fullPage: false });

    // Pre-bind a waitForRequest BEFORE the click so the POST never
    // races us — Playwright's auto-waiting on .click() resolves
    // before the in-flight fetch lands.
    const postPromise = page.waitForRequest(
      (req) =>
        req.method() === "POST" &&
        /\/api\/v1\/sessions\/[^/]+\/permission-mode$/.test(req.url()),
    );

    // Select YOLO — chip pivots to the danger palette.
    await page.locator('[data-testid="permission-mode-option-yolo"]').click();
    await expect(chip).toHaveAttribute("data-mode", "yolo");
    await expect(chip).toHaveAttribute("data-severity", "danger");
    await expect(
      page.locator('[data-testid="permission-mode-chip-label"]'),
    ).toContainText("YOLO");
    // Popover closed on selection.
    await expect(popover).not.toBeVisible();

    // Slice 3 contract — the chip POSTs to the backend with the
    // selected mode. The waitForRequest above asserts the request
    // fired; the mock recorder asserts the body shape so a future
    // refactor that drops the `mode` key gets caught.
    const post = await postPromise;
    expect(post.postDataJSON()).toEqual({ mode: "yolo" });
    expect(recordedPosts).toHaveLength(1);
    expect(recordedPosts[0]?.sessionId).toBe("session-1");
    expect(JSON.parse(recordedPosts[0]?.body ?? "{}")).toEqual({
      mode: "yolo",
    });

    // localStorage carries the per-session key — Slice 2 contract
    // preserved as the offline-boot fall-back.
    const stored = await page.evaluate(() =>
      window.localStorage.getItem("flowstate.permissionMode.session-1"),
    );
    expect(stored).toBe("yolo");
  });

  test("reload rehydrates the chip from the backend payload, overriding a stale localStorage value (Slice 3 backend-precedence)", async ({
    page,
  }) => {
    // The canonical precedence is backend > localStorage > default.
    // Seed localStorage with a STALE value before navigation so the
    // chip would render "plan" if localStorage won; then have the
    // session-list mock report "yolo" so the assertion proves the
    // backend payload took precedence.
    await bootstrapMocks(page, { sessionPermissionMode: "yolo" });
    await page.addInitScript(() => {
      // Prime stale localStorage BEFORE the page loads so the race
      // is real — at first paint both sources are populated and the
      // hydration helper must explicitly prefer the backend.
      window.localStorage.setItem("flowstate.permissionMode.session-1", "plan");
    });

    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

    const chip = page.locator('[data-testid="permission-mode-chip"]');
    await expect(chip).toBeVisible();
    // Backend wins — the chip MUST render "yolo" even though
    // localStorage said "plan". A regression that flipped the
    // precedence (localStorage > backend) would render "Plan" here.
    await expect(chip).toHaveAttribute("data-mode", "yolo");
    await expect(
      page.locator('[data-testid="permission-mode-chip-label"]'),
    ).toContainText("YOLO");

    // localStorage is reconciled to the backend's view so a
    // subsequent offline boot reflects the canonical value rather
    // than the stale prior selection.
    const stored = await page.evaluate(() =>
      window.localStorage.getItem("flowstate.permissionMode.session-1"),
    );
    expect(stored).toBe("yolo");
  });

  test("selecting Ask persists across reload (ModeAskUser Extension Slice 1)", async ({
    page,
  }) => {
    // ModeAskUser Extension (May 2026) §4 Slice 1 acceptance:
    // "Selecting Ask in the chip persists across reload" — proves the
    // Permission Modes Slice 3 backend persistence path is reused
    // unchanged for the fifth mode. The first navigation seeds
    // `permissionMode` via the chip click; the second navigation
    // (page.reload) sees the same session-list mock return "ask" as
    // the persisted value, and the chip must restore to Ask.
    const recordedPosts: { sessionId: string; body: string }[] = [];
    let sessionMode: string | undefined;
    await bootstrapMocks(page, { recordedPermissionModePosts: recordedPosts });

    // After the first POST, the session-list mock starts reporting
    // "ask" so the reload hydrates from backend (canonical
    // precedence: backend > localStorage > default).
    await page.route("**/api/v1/sessions", async (route) => {
      const summary: Record<string, unknown> = {
        id: "session-1",
        agentId: "agent-1",
        currentAgentId: "agent-1",
        currentProviderId: "anthropic",
        currentModelId: "claude-sonnet-4-6",
        title: "Test",
        createdAt: "2026-05-26T00:00:00Z",
        updatedAt: "2026-05-26T00:00:01Z",
        messageCount: 2,
      };
      if (sessionMode !== undefined) {
        summary["permissionMode"] = sessionMode;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([summary]),
      });
    });
    await page.route(
      "**/api/v1/sessions/*/permission-mode",
      async (route, request) => {
        if (request.method() !== "POST") {
          await route.fulfill({ status: 405 });
          return;
        }
        const body = request.postData() ?? "";
        const match = request.url().match(/\/sessions\/([^/]+)\/permission-mode/);
        const sessionId = match ? decodeURIComponent(match[1]) : "";
        recordedPosts.push({ sessionId, body });
        try {
          sessionMode = (JSON.parse(body) as { mode?: string }).mode;
        } catch {
          // ignore — the assertion below will catch malformed bodies
        }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ id: sessionId, permission_mode: sessionMode }),
        });
      },
    );

    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

    const chip = page.locator('[data-testid="permission-mode-chip"]');
    await expect(chip).toBeVisible();
    await chip.click();

    const postPromise = page.waitForRequest(
      (req) =>
        req.method() === "POST" &&
        /\/api\/v1\/sessions\/[^/]+\/permission-mode$/.test(req.url()),
    );
    await page.locator('[data-testid="permission-mode-option-ask"]').click();
    const post = await postPromise;
    expect(post.postDataJSON()).toEqual({ mode: "ask" });

    // Chip pivots to the Ask palette immediately.
    await expect(chip).toHaveAttribute("data-mode", "ask");
    await expect(chip).toHaveAttribute("data-severity", "ask");
    await expect(
      page.locator('[data-testid="permission-mode-chip-label"]'),
    ).toContainText("Ask");

    // Reload — backend now reports `permissionMode: "ask"`. Chip
    // restores to Ask without any further operator action,
    // exercising the predecessor Slice 3 hydration path unchanged.
    await page.reload();
    await expect(page.getByTestId("message-input")).toBeVisible();
    await expect(chip).toHaveAttribute("data-mode", "ask");
    await expect(
      page.locator('[data-testid="permission-mode-chip-label"]'),
    ).toContainText("Ask");
  });
});
