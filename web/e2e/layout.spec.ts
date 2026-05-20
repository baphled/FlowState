import { test, expect } from "@playwright/test";

test.describe("Chat layout — swarm pane toggle", () => {
  // Seed a single parent session so the layout tests run against a known
  // chrome state (NavBar shown, toolbar shown). Without this the dev server
  // returns whatever real sessions are persisted on the backend, which can
  // include a child session — and the NavBar is intentionally hidden in
  // child sessions, which would falsely fail "nav bar is always visible".
  const parentSession = {
    id: "layout-parent-1",
    agentId: "planner",
    title: "Layout parent",
    createdAt: "2026-05-01T09:00:00Z",
    updatedAt: "2026-05-01T09:00:00Z",
    messageCount: 0,
  };

  test.beforeEach(async ({ page }) => {
    await page.route("**/api/v1/sessions", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([parentSession]),
      });
    });
    await page.route("**/api/v1/sessions/**/messages", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([]),
      });
    });
    await page.route("**/api/swarm/events", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([]),
      });
    });
    await page.addInitScript((id: string) => {
      window.localStorage.setItem("chat.currentSessionId", id);
    }, parentSession.id);
    await page.goto("/chat");
  });

  test("swarm pane is visible by default", async ({ page }) => {
    await expect(page.getByTestId("swarm-pane")).toBeVisible();
  });

  test("toggle button hides the swarm pane", async ({ page }) => {
    await page.getByTestId("toggle-swarm-btn").click();
    await expect(page.getByTestId("swarm-pane")).not.toBeVisible();
  });

  test("show button re-displays the swarm pane after hiding", async ({
    page,
  }) => {
    await page.getByTestId("toggle-swarm-btn").click();
    await expect(page.getByTestId("swarm-pane")).not.toBeVisible();

    await page.getByTestId("show-swarm-btn").click();
    await expect(page.getByTestId("swarm-pane")).toBeVisible();
  });

  // The NavBar is visible whenever the active session is a parent session
  // (or no session is loaded). It is intentionally hidden in child sessions —
  // see e2e/session-hierarchy-nav.spec.ts for that contract.
  test("nav bar is visible on a parent session", async ({ page }) => {
    await expect(page.getByTestId("nav-bar")).toBeVisible();
    await page.getByTestId("toggle-swarm-btn").click();
    await expect(page.getByTestId("nav-bar")).toBeVisible();
  });
});
