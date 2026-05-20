import { test, expect } from "@playwright/test";

// Phase 3 of Turn-Based Post-Then-Poll Architecture (May 2026).
// Live-verify probe — drives the chat UI against the real backend and
// asserts:
//   1. POST /api/v1/sessions/{id}/messages fires.
//   2. The chat UI fires GET /turns/{turn_id} polls AT LEAST twice.
//   3. An assistant or thinking bubble renders content WITHOUT a
//      manual page refresh — the "refresh required" symptom that
//      motivated Phase 3 is GONE.

test.describe("Phase 3 — turn-poll live render", () => {
  test.describe.configure({ mode: "serial" });
  test.setTimeout(120_000);

  test("polls fetchTurn and renders assistant content without refresh", async ({
    page,
  }) => {
    const networkEvents: Array<{ t: number; kind: string; detail: string }> =
      [];
    const startedAt = Date.now();

    page.on("request", (req) => {
      const url = req.url();
      const method = req.method();
      if (
        url.match(/\/api\/v1\/sessions\/[^/]+\/messages$/) &&
        method === "POST"
      ) {
        networkEvents.push({
          t: Date.now() - startedAt,
          kind: "POST messages",
          detail: url,
        });
      } else if (url.match(/\/api\/v1\/sessions\/[^/]+\/turns\/[^/?]+/)) {
        networkEvents.push({
          t: Date.now() - startedAt,
          kind: "GET turn",
          detail: url,
        });
      } else if (url.match(/\/api\/v1\/sessions\/[^/]+\/stream/)) {
        networkEvents.push({
          t: Date.now() - startedAt,
          kind: "SSE stream",
          detail: url,
        });
      }
    });

    page.on("pageerror", (err) => {
      // eslint-disable-next-line no-console
      console.log("[pageerror]", String(err));
    });

    // Clear localStorage so the SPA starts fresh (no stale
    // `chat.currentSessionId` pointing at a session the server has
    // forgotten about across restarts — memory-only sessions evaporate
    // when the server cycles).
    await page.addInitScript(() => {
      try {
        window.localStorage.clear();
      } catch {
        // ignore
      }
    });

    await page.goto("http://localhost:5173/chat");

    // Wait for the composer.
    const composer = page.locator("textarea").first();
    await composer.waitFor({ state: "visible", timeout: 30_000 });

    const prompt = "Reply with exactly the word DONE and nothing else.";
    await composer.click();
    await composer.fill(prompt);
    await page.keyboard.press("Enter");

    // Wait for an assistant/thinking bubble.
    const bubble = page
      .locator(".message-bubble.assistant, .message-bubble.thinking")
      .first();

    let bubbleVisible = false;
    try {
      await bubble.waitFor({ state: "visible", timeout: 60_000 });
      bubbleVisible = true;
    } catch {
      bubbleVisible = false;
    }

    // Wait long enough for the POST to settle AND polls to fire. The
    // backend POST observed up to ~8s for the first turn against the
    // engine; we want at least 2 polls (1s + 1s) after that, so wait
    // 15s total.
    await page.waitForTimeout(15_000);

    const bubbleCount = await page
      .locator(".message-bubble.assistant, .message-bubble.thinking")
      .count();
    let lastBubbleText: string | null = null;
    if (bubbleCount > 0) {
      lastBubbleText = await page
        .locator(".message-bubble.assistant, .message-bubble.thinking")
        .last()
        .textContent();
    }

    const postCount = networkEvents.filter(
      (e) => e.kind === "POST messages",
    ).length;
    const turnPollCount = networkEvents.filter(
      (e) => e.kind === "GET turn",
    ).length;
    const sseCount = networkEvents.filter(
      (e) => e.kind === "SSE stream",
    ).length;

    // eslint-disable-next-line no-console
    console.log("NETWORK SUMMARY:", { postCount, turnPollCount, sseCount });
    // eslint-disable-next-line no-console
    console.log("DOM SUMMARY:", {
      bubbleVisible,
      bubbleCount,
      lastBubbleText: lastBubbleText?.slice(0, 200),
    });
    // eslint-disable-next-line no-console
    console.log(
      "TIMELINE:",
      JSON.stringify(
        networkEvents.slice(0, 30).map((e) => ({ t: e.t, kind: e.kind })),
      ),
    );

    // Phase 3 assertions.
    expect(postCount, "POST /messages must fire").toBeGreaterThanOrEqual(1);
    expect(
      turnPollCount,
      "GET /turns/{turn_id} must fire at least twice",
    ).toBeGreaterThanOrEqual(2);
    expect(
      bubbleVisible,
      "assistant/thinking bubble rendered live (no refresh)",
    ).toBe(true);
    expect(
      lastBubbleText?.length ?? 0,
      "bubble content rendered without refresh",
    ).toBeGreaterThan(0);
  });
});
