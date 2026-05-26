import { test, expect, Page } from "@playwright/test";

/**
 * Live UI verification for the PermissionModeChip (Permission Modes
 * (May 2026), Slice 2).
 *
 * Behaviour pinned (user-observable, not internal):
 *   - The chip is visible in the composer toolbar on initial page
 *     load.
 *   - Clicking the chip opens the popover containing the four
 *     canonical modes.
 *   - The loud-disclosure paragraph for Default mode is rendered in
 *     the DOM (not hover-only) when the popover is open. This is
 *     the v1 mitigation for the "Default does not prompt per call"
 *     surface — plan §5 requires the operator see it at the
 *     decision point.
 *   - Selecting YOLO updates the chip label and the data-mode
 *     attribute reactively, with no backend round-trip.
 *   - Refreshing the page persists the selection from localStorage
 *     keyed under `flowstate.permissionMode.<sessionId>`.
 *   - The screenshot snapshot of the chip with the popover open is
 *     written to /tmp/slice2-chip.png for the slice's evidence.
 *
 * Slice 3 will switch the persistence layer to a backend POST. This
 * spec deliberately exercises localStorage-only behaviour because
 * that is the contract Slice 2 ships.
 *
 * Mocking patterns mirror `context-usage-chip.spec.ts`. The chip
 * itself makes no fetch calls in Slice 2, so the route table only
 * needs the bootstrap surface (agents / models / sessions / health).
 */

async function bootstrapMocks(page: Page): Promise<void> {
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
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([
        {
          id: "session-1",
          agentId: "agent-1",
          currentAgentId: "agent-1",
          currentProviderId: "anthropic",
          currentModelId: "claude-sonnet-4-6",
          title: "Test",
          createdAt: "2026-05-26T00:00:00Z",
          updatedAt: "2026-05-26T00:00:01Z",
          messageCount: messages.length,
        },
      ]),
    });
  });
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
  test.beforeEach(async ({ page }) => {
    await bootstrapMocks(page);
  });

  test("chip renders in the composer toolbar, opens the popover with all four modes plus the Default disclosure, selects YOLO, and persists across reload", async ({
    page,
  }) => {
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

    // Click → popover opens with the four canonical modes.
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
      page.locator('[data-testid="permission-mode-option-yolo"]'),
    ).toBeVisible();

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

    // Capture the popover-open screenshot as Slice 2's evidence
    // before clicking through and dismissing the popover.
    await page.screenshot({ path: "/tmp/slice2-chip.png", fullPage: false });

    // Select YOLO — chip pivots to the danger palette.
    await page.locator('[data-testid="permission-mode-option-yolo"]').click();
    await expect(chip).toHaveAttribute("data-mode", "yolo");
    await expect(chip).toHaveAttribute("data-severity", "danger");
    await expect(
      page.locator('[data-testid="permission-mode-chip-label"]'),
    ).toContainText("YOLO");
    // Popover closed on selection.
    await expect(popover).not.toBeVisible();

    // localStorage carries the per-session key — Slice 2 contract.
    const stored = await page.evaluate(() =>
      window.localStorage.getItem("flowstate.permissionMode.session-1"),
    );
    expect(stored).toBe("yolo");

    // Reload — the chip rehydrates from localStorage.
    await page.reload();
    await expect(page.getByTestId("message-input")).toBeVisible();
    const chipAfterReload = page.locator(
      '[data-testid="permission-mode-chip"]',
    );
    await expect(chipAfterReload).toHaveAttribute("data-mode", "yolo");
    await expect(
      page.locator('[data-testid="permission-mode-chip-label"]'),
    ).toContainText("YOLO");
  });
});
