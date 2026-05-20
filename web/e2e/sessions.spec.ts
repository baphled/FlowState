import { test, expect } from "@playwright/test";

test.describe("Session switching", () => {
  const agents = [
    {
      id: "planner",
      name: "Planner",
      description: "Plans work",
      model: "claude-sonnet-4-6",
    },
    {
      id: "executor",
      name: "Executor",
      description: "Runs work",
      model: "llama3.2",
    },
  ];

  const sessions = [
    {
      id: "session-12345678",
      agentId: "planner",
      title: "Planning Sync",
      updatedAt: "2026-05-01T09:00:00Z",
      createdAt: "2026-04-30T09:00:00Z",
      messageCount: 3,
    },
    {
      id: "session-87654321",
      agentId: "executor",
      title: "Sprint Retro",
      updatedAt: "2026-05-01T10:00:00Z",
      createdAt: "2026-05-01T09:30:00Z",
      messageCount: 5,
    },
    {
      id: "session-22223333",
      agentId: "planner",
      title: "Bug Bash Notes",
      updatedAt: "2026-05-01T11:00:00Z",
      createdAt: "2026-05-01T10:30:00Z",
      messageCount: 2,
    },
  ];

  const messagesBySession = {
    "session-12345678": [
      { role: "user", content: "What is left for planning?" },
      { role: "assistant", content: "Planning Sync summary from the API." },
    ],
    "session-87654321": [
      { role: "user", content: "How did the sprint go?" },
      { role: "assistant", content: "Sprint Retro summary from the API." },
    ],
    "session-22223333": [
      { role: "user", content: "Any defects left?" },
      { role: "assistant", content: "Bug Bash Notes summary from the API." },
    ],
  };

  const getSessionId = (url: string) =>
    url.match(/\/sessions\/([^/]+)\/messages/)?.[1] ?? "";

  test.beforeEach(async ({ page }) => {
    await page.route("**/api/agents", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(agents),
      });
    });

    await page.route("**/api/v1/sessions", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(sessions),
      });
    });

    await page.route("**/api/v1/sessions/**/messages", async (route) => {
      const sessionId = getSessionId(route.request().url());
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(
          messagesBySession[sessionId as keyof typeof messagesBySession] ?? [],
        ),
      });
    });

    await page.route("**/api/swarm/events", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([]),
      });
    });

    await page.goto("/chat");
  });

  test("renders the session list from API responses", async ({ page }) => {
    const sessionSwitcher = page.getByTestId("session-switcher");

    await sessionSwitcher.getByRole("button").click();

    await expect(sessionSwitcher.getByRole("listbox")).toBeVisible();
    await expect(
      sessionSwitcher.getByRole("option", { name: /Planning Sync/i }),
    ).toBeVisible();
    await expect(
      sessionSwitcher.getByRole("option", { name: /Sprint Retro/i }),
    ).toBeVisible();
    await expect(
      sessionSwitcher.getByRole("option", { name: /Bug Bash Notes/i }),
    ).toBeVisible();
    await expect(sessionSwitcher).toContainText("3 messages");
    await expect(sessionSwitcher).toContainText("5 messages");
  });

  test("displays session titles in the switcher", async ({ page }) => {
    const sessionSwitcher = page.getByTestId("session-switcher");

    await expect(sessionSwitcher.getByRole("button")).toContainText(
      /Planning Sync/,
    );

    await sessionSwitcher.getByRole("button").click();
    await expect(sessionSwitcher).toContainText("Planning Sync");
    await expect(sessionSwitcher).toContainText("Sprint Retro");
    await expect(sessionSwitcher).toContainText("Bug Bash Notes");
  });

  test("switches to the clicked session", async ({ page }) => {
    const sessionSwitcher = page.getByTestId("session-switcher");
    const messageList = page.getByTestId("message-list");

    await expect(messageList).toContainText(
      "Planning Sync summary from the API.",
    );

    await sessionSwitcher.getByRole("button").click();
    await sessionSwitcher
      .getByRole("option", { name: /Sprint Retro/i })
      .click();

    await expect(sessionSwitcher.getByRole("button")).toContainText(
      /Sprint Retro/,
    );
    await expect(messageList).toContainText(
      "Sprint Retro summary from the API.",
    );
    await expect(messageList).not.toContainText(
      "Planning Sync summary from the API.",
    );
  });

  test("shows recent sessions in the session switcher dropdown", async ({
    page,
  }) => {
    const sessionSwitcher = page.getByTestId("session-switcher");

    await sessionSwitcher.getByRole("button").click();

    await expect(sessionSwitcher).toContainText("Recent Sessions");
    await expect(
      sessionSwitcher.getByRole("option", { name: /New Session/i }),
    ).toBeVisible();
    await expect(sessionSwitcher.getByRole("option")).toHaveCount(4);
  });
});
