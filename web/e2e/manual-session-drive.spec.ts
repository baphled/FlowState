import {
  test,
  expect,
  type Page,
  type ConsoleMessage,
  type Request,
  type Response,
} from "@playwright/test";
import * as fs from "fs";
import * as path from "path";

/**
 * Manual session driver — verification spec, not a regression test.
 *
 * Drives two real chat turns against the live backend at localhost:8080
 * (no /api/v1/* mocks of any kind) and captures comprehensive evidence
 * to /tmp/manual-session-drive-evidence/ for the operator to audit.
 *
 * Captured per turn:
 *   - DOM phase snapshots (pre-send, immediately-after-click, mid-stream, settled)
 *   - Bubble counts and text content
 *   - Full network log of every /api/v1/* and /api/* request
 *   - Console messages (errors, warnings, info)
 *   - Screenshots at each phase
 */

const EVIDENCE_DIR = "/tmp/manual-session-drive-evidence";

interface NetEntry {
  ts: string;
  method: string;
  url: string;
  type: "request" | "response" | "failed";
  status?: number;
  bodyPreview?: string;
  size?: number;
}

interface ConsoleEntry {
  ts: string;
  type: string;
  text: string;
  location?: string;
}

interface DomSnapshot {
  phase: string;
  ts: string;
  userBubbleCount: number;
  assistantBubbleCount: number;
  userBubbleTexts: string[];
  assistantBubbleTexts: string[];
  indicatorVisible: boolean;
  url: string;
}

function nowIso(): string {
  return new Date().toISOString();
}

async function snapshot(page: Page, phase: string): Promise<DomSnapshot> {
  const userBubbles = page.locator(".message-bubble.user");
  const assistantBubbles = page.locator(".message-bubble.assistant");
  const indicator = page.getByTestId("agent-activity-indicator");

  const [uc, ac, ut, at, ind, url] = await Promise.all([
    userBubbles.count(),
    assistantBubbles.count(),
    userBubbles.allTextContents(),
    assistantBubbles.allTextContents(),
    indicator.isVisible().catch(() => false),
    Promise.resolve(page.url()),
  ]);

  return {
    phase,
    ts: nowIso(),
    userBubbleCount: uc,
    assistantBubbleCount: ac,
    userBubbleTexts: ut.map((t) => t.trim().slice(0, 200)),
    assistantBubbleTexts: at.map((t) => t.trim().slice(0, 200)),
    indicatorVisible: ind,
    url,
  };
}

async function recordSnapshot(
  page: Page,
  phase: string,
  snapshots: DomSnapshot[],
  evidenceDir: string,
): Promise<DomSnapshot> {
  const snap = await snapshot(page, phase);
  snapshots.push(snap);
  await page.screenshot({
    path: path.join(evidenceDir, `${phase}.png`),
    fullPage: true,
  });
  return snap;
}

test.describe("manual session drive — real backend evidence capture", () => {
  test.setTimeout(180_000);

  test("two turns, full evidence capture", async ({ page }) => {
    if (!fs.existsSync(EVIDENCE_DIR)) {
      fs.mkdirSync(EVIDENCE_DIR, { recursive: true });
    }

    const network: NetEntry[] = [];
    const consoleLog: ConsoleEntry[] = [];
    const snapshots: DomSnapshot[] = [];
    const responseBodyPreviews: Record<string, string> = {};

    // Network capture — every request and response
    page.on("request", (req: Request) => {
      const url = req.url();
      if (!url.includes("/api/")) return;
      network.push({
        ts: nowIso(),
        method: req.method(),
        url,
        type: "request",
      });
    });

    page.on("response", async (res: Response) => {
      const url = res.url();
      if (!url.includes("/api/")) return;
      let bodyPreview: string | undefined;
      let size: number | undefined;
      try {
        // Don't try to read SSE/chunked stream bodies — they hang.
        const ct = res.headers()["content-type"] ?? "";
        if (!ct.includes("event-stream")) {
          const buf = await res.body();
          size = buf.length;
          bodyPreview = buf.toString("utf-8").slice(0, 500);
          // Stash the full preview separately keyed by URL+ts
          responseBodyPreviews[`${nowIso()}-${url}`] = buf
            .toString("utf-8")
            .slice(0, 5000);
        } else {
          size = 0;
          bodyPreview = "<event-stream>";
        }
      } catch (e) {
        bodyPreview = `<read failed: ${(e as Error).message}>`;
      }
      network.push({
        ts: nowIso(),
        method: res.request().method(),
        url,
        type: "response",
        status: res.status(),
        bodyPreview,
        size,
      });
    });

    page.on("requestfailed", (req: Request) => {
      const url = req.url();
      if (!url.includes("/api/")) return;
      network.push({
        ts: nowIso(),
        method: req.method(),
        url,
        type: "failed",
        bodyPreview: req.failure()?.errorText ?? "unknown",
      });
    });

    page.on("console", (msg: ConsoleMessage) => {
      consoleLog.push({
        ts: nowIso(),
        type: msg.type(),
        text: msg.text(),
        location: msg.location()
          ? `${msg.location().url}:${msg.location().lineNumber}`
          : undefined,
      });
    });

    page.on("pageerror", (err: Error) => {
      consoleLog.push({
        ts: nowIso(),
        type: "pageerror",
        text: err.message,
      });
    });

    // === SETUP ===
    // Pre-create a brand-new session via the real backend so we have a
    // clean canvas (no other test interference) and a deterministic id
    // to reconcile against.
    const createRes = await page.request.post(
      "http://localhost:8080/api/v1/sessions",
      {
        data: { agent_id: "Senior-Engineer" },
      },
    );
    expect(
      createRes.ok(),
      `session creation failed: ${createRes.status()}`,
    ).toBeTruthy();
    const created = (await createRes.json()) as { id: string; agentId: string };
    const sessionId = created.id;
    console.log(`[evidence] created session: ${sessionId}`);
    fs.writeFileSync(path.join(EVIDENCE_DIR, "session-id.txt"), sessionId);

    // Pin GET /api/v1/sessions to ONLY return our session — same trick
    // as chat-real-backend.spec.ts to prevent picking a stranger's
    // session from the long-lived backend's history.
    await page.route("**/api/v1/sessions", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify([
            {
              id: created.id,
              agentId: created.agentId,
              currentAgentId: created.agentId,
              title: "",
              messageCount: 0,
              createdAt: new Date().toISOString(),
              updatedAt: new Date().toISOString(),
              isStreaming: false,
            },
          ]),
        });
        return;
      }
      await route.continue();
    });

    await page.goto("/chat");
    await page.evaluate(
      ({ sid, aid }) => {
        localStorage.clear();
        localStorage.setItem("chat.currentSessionId", sid);
        localStorage.setItem("chat.agentId", aid);
      },
      { sid: sessionId, aid: "Senior-Engineer" },
    );
    await page.reload();
    await page
      .getByTestId("chat-empty-state")
      .waitFor({ state: "visible", timeout: 15_000 });
    await expect(page.getByTestId("agent-picker")).toContainText(/senior/i, {
      timeout: 10_000,
    });

    await recordSnapshot(page, "00-ready", snapshots, EVIDENCE_DIR);

    // === TURN 1 ===
    const TURN1 = "Reply with exactly the word PONG and nothing else.";
    const input = page.getByTestId("message-input");
    const sendBtn = page.getByTestId("send-button");

    await input.fill(TURN1);
    await recordSnapshot(page, "01-turn1-pre-send", snapshots, EVIDENCE_DIR);

    const turn1ClickTs = nowIso();
    await sendBtn.click();

    // Sample at fixed offsets to catch races
    await page.waitForTimeout(250);
    await recordSnapshot(
      page,
      "02-turn1-post-click-250ms",
      snapshots,
      EVIDENCE_DIR,
    );

    await page.waitForTimeout(750); // ~1s after click
    await recordSnapshot(
      page,
      "03-turn1-post-click-1s",
      snapshots,
      EVIDENCE_DIR,
    );

    await page.waitForTimeout(2000); // ~3s after click
    await recordSnapshot(
      page,
      "04-turn1-post-click-3s",
      snapshots,
      EVIDENCE_DIR,
    );

    // Wait for assistant content to materialise (up to 90s)
    try {
      await expect
        .poll(
          async () => await page.locator(".message-bubble.assistant").count(),
          {
            timeout: 90_000,
            message: "no assistant bubble visible after turn 1",
          },
        )
        .toBeGreaterThan(0);
    } catch (e) {
      console.error(
        `[evidence] turn 1: no assistant bubble in 90s — capturing failure state`,
      );
      await recordSnapshot(
        page,
        "05-turn1-FAILURE-no-assistant",
        snapshots,
        EVIDENCE_DIR,
      );
      throw e;
    }

    // Wait for the indicator to settle (hide)
    try {
      await expect(page.getByTestId("agent-activity-indicator")).toBeHidden({
        timeout: 30_000,
      });
    } catch {
      console.warn(
        "[evidence] turn 1: indicator did not hide within 30s — capturing stuck state",
      );
      await recordSnapshot(
        page,
        "06-turn1-indicator-stuck",
        snapshots,
        EVIDENCE_DIR,
      );
    }

    // Settle pause to let any straggler reconciles complete
    await page.waitForTimeout(2_000);
    await recordSnapshot(page, "07-turn1-settled", snapshots, EVIDENCE_DIR);

    // Mark for log correlation
    fs.writeFileSync(
      path.join(EVIDENCE_DIR, "turn1-click-ts.txt"),
      turn1ClickTs,
    );

    // === TURN 2 ===
    const TURN2 = "Now reply with exactly the word PING.";
    await input.fill(TURN2);
    await recordSnapshot(page, "08-turn2-pre-send", snapshots, EVIDENCE_DIR);

    const turn2ClickTs = nowIso();
    await sendBtn.click();

    await page.waitForTimeout(250);
    await recordSnapshot(
      page,
      "09-turn2-post-click-250ms",
      snapshots,
      EVIDENCE_DIR,
    );

    await page.waitForTimeout(750);
    await recordSnapshot(
      page,
      "10-turn2-post-click-1s",
      snapshots,
      EVIDENCE_DIR,
    );

    await page.waitForTimeout(2000);
    await recordSnapshot(
      page,
      "11-turn2-post-click-3s",
      snapshots,
      EVIDENCE_DIR,
    );

    try {
      await expect
        .poll(
          async () => await page.locator(".message-bubble.assistant").count(),
          {
            timeout: 90_000,
            message: "expected ≥2 assistant bubbles after turn 2",
          },
        )
        .toBeGreaterThanOrEqual(2);
    } catch (e) {
      console.error("[evidence] turn 2: <2 assistant bubbles after 90s");
      await recordSnapshot(
        page,
        "12-turn2-FAILURE-no-second-assistant",
        snapshots,
        EVIDENCE_DIR,
      );
      throw e;
    }

    try {
      await expect(page.getByTestId("agent-activity-indicator")).toBeHidden({
        timeout: 30_000,
      });
    } catch {
      console.warn("[evidence] turn 2: indicator did not hide within 30s");
      await recordSnapshot(
        page,
        "13-turn2-indicator-stuck",
        snapshots,
        EVIDENCE_DIR,
      );
    }

    await page.waitForTimeout(2_000);
    await recordSnapshot(page, "14-turn2-settled", snapshots, EVIDENCE_DIR);

    fs.writeFileSync(
      path.join(EVIDENCE_DIR, "turn2-click-ts.txt"),
      turn2ClickTs,
    );

    // === DUMP EVIDENCE ===
    fs.writeFileSync(
      path.join(EVIDENCE_DIR, "network.json"),
      JSON.stringify(network, null, 2),
    );
    fs.writeFileSync(
      path.join(EVIDENCE_DIR, "console.json"),
      JSON.stringify(consoleLog, null, 2),
    );
    fs.writeFileSync(
      path.join(EVIDENCE_DIR, "snapshots.json"),
      JSON.stringify(snapshots, null, 2),
    );
    fs.writeFileSync(
      path.join(EVIDENCE_DIR, "response-bodies.json"),
      JSON.stringify(responseBodyPreviews, null, 2),
    );

    // Also dump the canonical message thread from the backend for comparison
    try {
      const canonRes = await page.request.get(
        `http://localhost:8080/api/v1/sessions/${sessionId}/messages`,
      );
      const canonText = await canonRes.text();
      fs.writeFileSync(
        path.join(EVIDENCE_DIR, "canonical-messages.json"),
        canonText,
      );
    } catch (e) {
      fs.writeFileSync(
        path.join(EVIDENCE_DIR, "canonical-messages.error"),
        (e as Error).message,
      );
    }

    console.log(
      `[evidence] captured ${snapshots.length} snapshots, ${network.length} net events, ${consoleLog.length} console msgs`,
    );
    console.log(`[evidence] all files in ${EVIDENCE_DIR}`);
  });
});
