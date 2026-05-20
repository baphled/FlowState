import { test, expect, type Page, type Route } from "@playwright/test";

/**
 * Live UI verification for the thinking-only degraded-turn affordance.
 *
 * The Go session accumulator synthesises a placeholder assistant Message
 * when an OpenAI-compatible reasoning provider (zai/glm-4.6, DeepSeek-R1)
 * finishes a turn with reasoning tokens but no visible content. The
 * placeholder shape on the wire is:
 *
 *   {
 *     "role": "assistant",
 *     "content": "",
 *     "thinkingBlocks": [ { "thinking": "...", "signature": "..." } ],
 *     "stopReason": "end_turn",
 *     ...
 *   }
 *
 * The chat UI must render that combination as a soft-error affordance
 * ("the agent thought but produced no response") rather than a blank
 * bubble — a blank bubble is indistinguishable from a stalled stream.
 *
 * The bug-fix that landed the synthesis on the engine side is documented
 * at `Empty-Content Thinking-Only Assistant Turn (May 2026)` in the
 * FlowState vault. This spec pins the user-visible UI follow-up so a
 * future render-path regression can't silently re-stall the chat for
 * reasoning-only turns.
 *
 * Behaviour pinned (assertions are user-observable, not internal):
 *   - The bubble is visible after loading a session whose history
 *     carries the synthesised placeholder.
 *   - The bubble carries `data-testid="thinking-only-affordance"` so
 *     downstream specs / accessibility tooling can locate it.
 *   - It uses `role="status"` (informational) — distinct from the
 *     critical-error banner's `role="alert"` (assertive).
 *   - Visible text communicates BOTH (a) what happened (the model
 *     stopped without replying) and (b) what to do next (try again),
 *     so the user can recover without thinking the chat is broken.
 *     Reword landed May 7 2026 after user feedback that the prior
 *     "No response produced / agent thought through this turn but
 *     produced no response" copy read as a system bug report instead
 *     of a recovery hint.
 *   - A normal content-bearing assistant message in the same session
 *     does NOT trigger the affordance (regression cover).
 *   - The critical-error banner does NOT appear — soft-error and
 *     critical-error surfaces are distinct.
 */

const agents = [
  {
    id: "planner",
    name: "Planner",
    description: "Plans work",
    model: "claude-sonnet-4-6",
  },
];

const SESSION_ID = "session-thinking-only";

const baseSession = {
  id: SESSION_ID,
  agentId: "planner",
  currentAgentId: "planner",
  title: "Thinking-only repro",
  messageCount: 2,
  createdAt: "2026-05-07T09:00:00Z",
  updatedAt: "2026-05-07T09:00:01Z",
};

const placeholderMessages = [
  {
    id: "m-user-1",
    role: "user",
    content: "read this 700KB file and summarise",
    timestamp: "2026-05-07T09:00:00Z",
  },
  {
    // The synthesised placeholder — empty content, structured
    // thinkingBlocks, and a non-empty stopReason. Mirrors the wire
    // shape of session.Message after `synthesizePlaceholderAssistant`
    // ran on a thinking-only turn.
    id: "m-asst-placeholder",
    role: "assistant",
    content: "",
    stopReason: "end_turn",
    thinkingBlocks: [
      {
        thinking: "I should think about this carefully ...",
        signature: "sig-placeholder",
      },
    ],
    timestamp: "2026-05-07T09:00:01Z",
    status: "completed",
  },
];

const normalReplyMessages = [
  {
    id: "m-user-2",
    role: "user",
    content: "hi",
    timestamp: "2026-05-07T09:00:00Z",
  },
  {
    id: "m-asst-normal",
    role: "assistant",
    content: "hello, here is a real reply",
    timestamp: "2026-05-07T09:00:01Z",
    status: "completed",
  },
];

async function installRoutes(
  page: Page,
  messages: typeof placeholderMessages,
): Promise<void> {
  await page.route("**/api/health", async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: '{"status":"ok"}',
    });
  });

  await page.route("**/api/agents", async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(agents),
    });
  });

  await page.route("**/api/v1/models", async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: '{"providers":[]}',
    });
  });

  await page.route("**/api/v1/sessions", async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([baseSession]),
    });
  });

  await page.route("**/api/v1/sessions/**/messages", async (route: Route) => {
    if (route.request().method() === "POST") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          ...baseSession,
          messages,
          messageCount: messages.length,
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

  await page.route("**/api/swarm/events", async (route: Route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: "[]",
    });
  });

  await page.addInitScript(
    ({ id }) => {
      window.localStorage.setItem("chat.currentSessionId", id);
    },
    { id: SESSION_ID },
  );
}

const DEV_BASE_URL =
  process.env["THINKING_ONLY_BASE_URL"] ?? "http://localhost:5173";
test.use({ baseURL: DEV_BASE_URL });

test.describe("Thinking-only degraded turn — live UI", () => {
  test("the synthesised placeholder renders as a soft-error affordance, not a blank bubble", async ({
    page,
  }) => {
    await installRoutes(page, placeholderMessages);
    await page.goto("/chat");

    const affordance = page.locator('[data-testid="thinking-only-affordance"]');
    await expect(affordance).toBeVisible();
    await expect(affordance).toHaveAttribute("role", "status");
    // Pin user outcome: affordance conveys (a) the model stopped before
    // replying, and (b) what the user can do (try again). Don't pin the
    // exact phrasing — leaves room for copy refinement without
    // round-tripping the spec.
    await expect(affordance).toContainText(
      /stopped before replying|didn't (come through|reply)|no reply/i,
    );
    await expect(affordance).toContainText(
      /try (sending|asking|again)|send.*again|ask again/i,
    );

    // Soft-error surface is distinct from the critical-error banner.
    await expect(
      page.locator('[data-testid="critical-error-banner"]'),
    ).toHaveCount(0);
  });

  test("a normal content-bearing assistant message does NOT trigger the affordance", async ({
    page,
  }) => {
    await installRoutes(page, normalReplyMessages);
    await page.goto("/chat");

    // Wait for the assistant content to land, then confirm absence.
    await expect(page.getByText("hello, here is a real reply")).toBeVisible();
    await expect(
      page.locator('[data-testid="thinking-only-affordance"]'),
    ).toHaveCount(0);
  });
});
