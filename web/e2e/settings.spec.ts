import { test, expect } from "@playwright/test";

test.describe("Settings view", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/swarm/events", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([]),
      });
    });
    await page.goto("/settings");
  });

  test("renders the settings view", async ({ page }) => {
    await expect(page.getByTestId("settings-view")).toBeVisible();
    await expect(page.getByTestId("theme-section")).toBeVisible();
    await expect(page.getByTestId("api-section")).toBeVisible();
    await expect(page.getByTestId("layout-section")).toBeVisible();
  });

  test("selects dark theme and updates data-theme on html", async ({
    page,
  }) => {
    await page.getByTestId("theme-option-dark").click();
    const htmlTheme = await page.evaluate(() =>
      document.documentElement.getAttribute("data-theme"),
    );
    expect(htmlTheme).toBe("dark");
  });

  test("selects light theme and updates data-theme on html", async ({
    page,
  }) => {
    await page.getByTestId("theme-option-light").click();
    const htmlTheme = await page.evaluate(() =>
      document.documentElement.getAttribute("data-theme"),
    );
    expect(htmlTheme).toBe("light");
  });

  test("selects terminal theme and updates data-theme on html", async ({
    page,
  }) => {
    await page.getByTestId("theme-option-terminal").click();
    const htmlTheme = await page.evaluate(() =>
      document.documentElement.getAttribute("data-theme"),
    );
    expect(htmlTheme).toBe("terminal");
  });

  test("updates api host input", async ({ page }) => {
    const input = page.getByTestId("api-host-input");
    await input.fill("http://localhost:9090");
    await expect(input).toHaveValue("http://localhost:9090");
  });

  test("toggles swarm pane setting", async ({ page }) => {
    const toggle = page.getByTestId("swarm-pane-toggle");
    const initialState = await toggle.isChecked();
    await toggle.click();
    expect(await toggle.isChecked()).toBe(!initialState);
  });
});
