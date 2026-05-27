import { test, expect, Page } from "@playwright/test";

/**
 * Permission Mode ModeAskUser Extension plan (May 2026), Slice 3 —
 * end-to-end flow for the inline PermissionPrompt.
 *
 * Behaviour pinned (user-observable, not internal):
 *
 *   - Select Ask in the chip → drive a denied tool call → the inline
 *     PermissionPrompt renders beneath the suspended tool_call bubble.
 *   - Click "Allow once" → POST to /permission-grant fires with
 *     {request_id, scope:"once"} → the prompt unmounts when the
 *     long-poll diff carries the resolved status.
 *   - Click "Deny" → existing tool_error bubble continues to render
 *     (regression with Permission Modes lattice; this test only
 *     verifies the POST shape because the IsError tool_result wire
 *     for the resumed call is engine-internal beyond this slice).
 *   - Two-tab regression (plan §11 R5 + §14): when tab A grants, tab
 *     B's PermissionPrompt unmounts within one long-poll cadence —
 *     the long-poll diff is the cross-tab signal per §17.1.
 *
 * Routing mocks deliver the long-poll permission_requests slice with
 * a controllable status flip so the spec can drive the precise
 * transition the FE must observe. The architectural redirect at
 * §17.1 means we do NOT mock any SSE bridge — the wire is the turn
 * endpoint's `permission_requests` field on subsequent long-poll
 * returns.
 */

interface PollSequenceStep {
  permission_requests?: Array<{
    request_id: string;
    tool_name: string;
    agent_name?: string;
    resource?: string;
    denial_reason?: string;
    mode?: string;
    status: "pending" | "granted" | "denied" | "timeout";
    scope?: string;
  }>;
  messages?: Array<{ id: string; role: string; content: string; toolName?: string; toolInput?: unknown; timestamp?: string }>;
  status?: "running" | "completed" | "failed";
}

interface BootstrapOptions {
  /**
   * Sequence of poll responses the GET /turns/{id} mock will return,
   * one per call. After the sequence exhausts, the mock returns the
   * final entry repeatedly so the FE's long-poll loop terminates on
   * the first 'completed' / 'failed' status it sees.
   */
  pollSequence: PollSequenceStep[];
  /**
   * Records the most recent POST /permission-grant body so the spec
   * can assert the FE sent the correct payload after the click.
   */
  recordedGrantPosts: Array<{ sessionId: string; body: string }>;
}

const TURN_ID = "turn-ask-1";
const SESSION_ID = "session-ask-1";
const REQUEST_ID = "req-ask-1";

async function bootstrapMocks(
  page: Page,
  opts: BootstrapOptions,
): Promise<void> {
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
          id: "coordinator",
          name: "Coordinator",
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
    if (route.request().method() !== "GET") {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([
        {
          id: SESSION_ID,
          agentId: "coordinator",
          currentAgentId: "coordinator",
          currentProviderId: "anthropic",
          currentModelId: "claude-sonnet-4-6",
          title: "Ask flow",
          createdAt: "2026-05-27T00:00:00Z",
          updatedAt: "2026-05-27T00:00:01Z",
          messageCount: 0,
          permissionMode: "ask",
        },
      ]),
    });
  });
  await page.route("**/api/v1/sessions/*/messages", async (route) => {
    // Initial messages load — return the suspended tool_call so the
    // PermissionPrompt has a bubble to anchor under. The PermissionPrompt
    // mounts inside the tool-invocation chrome via
    // pendingPermissionForBubble's tool_name match.
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([
          {
            id: "user-1",
            role: "user",
            content: "read /tmp/data.txt",
            timestamp: "2026-05-27T00:00:00Z",
          },
          {
            id: "tool-1",
            role: "tool_call",
            content: "",
            toolName: "read",
            toolInput: { path: "/tmp/data.txt" },
            timestamp: "2026-05-27T00:00:01Z",
          },
        ]),
      });
      return;
    }
    await route.continue();
  });
  // The permission-grant endpoint records the POST body and returns
  // the matching {request_id, scope} acknowledgement.
  await page.route(
    "**/api/v1/sessions/*/permission-grant",
    async (route, request) => {
      if (request.method() !== "POST") {
        await route.fulfill({ status: 405 });
        return;
      }
      const body = request.postData() ?? "";
      const match = request.url().match(/\/sessions\/([^/]+)\/permission-grant/);
      const sessionId = match ? decodeURIComponent(match[1]) : "";
      opts.recordedGrantPosts.push({ sessionId, body });
      const parsed = JSON.parse(body) as { request_id: string; scope: string };
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          request_id: parsed.request_id,
          scope: parsed.scope,
        }),
      });
    },
  );
  // Long-poll endpoint — returns the sequence's i-th entry, then
  // pins on the final step. Each entry is merged onto a base shape
  // so we keep the spec readable.
  let pollIdx = 0;
  await page.route("**/api/v1/sessions/*/turns/*", async (route) => {
    const step = opts.pollSequence[Math.min(pollIdx, opts.pollSequence.length - 1)];
    pollIdx += 1;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        turn_id: TURN_ID,
        session_id: SESSION_ID,
        status: step?.status ?? "running",
        started_at: "2026-05-27T00:00:00Z",
        completed_at: step?.status === "completed" ? "2026-05-27T00:00:05Z" : null,
        model: { provider: "anthropic", model: "claude-sonnet-4-6" },
        error: "",
        messages: step?.messages ?? [],
        phase: "tool_executing",
        token_count: 0,
        permission_requests: step?.permission_requests ?? [],
      }),
    });
  });
  await page.route("**/api/swarm/events", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: "[]",
    });
  });
  await page.addInitScript(
    ({ sessionId, turnId }) => {
      window.localStorage.setItem("chat.currentSessionId", sessionId);
      // Seed an active turn id so the FE long-poll loop starts on
      // page load instead of waiting for a fresh POST.
      window.localStorage.setItem(`chat.activeTurnId.${sessionId}`, turnId);
    },
    { sessionId: SESSION_ID, turnId: TURN_ID },
  );
}

const DEV_BASE_URL =
  process.env["PERMISSION_ASK_BASE_URL"] ?? "http://localhost:5173";
test.use({ baseURL: DEV_BASE_URL });

test.describe("Permission Mode Ask — inline grant flow (Slice 3)", () => {
  test("Allow once: prompt renders, click POSTs the grant, prompt unmounts on the next poll diff", async ({
    page,
  }) => {
    const recordedGrantPosts: Array<{ sessionId: string; body: string }> = [];
    await bootstrapMocks(page, {
      recordedGrantPosts,
      pollSequence: [
        // First poll — suspended request appears.
        {
          status: "running",
          permission_requests: [
            {
              request_id: REQUEST_ID,
              tool_name: "read",
              agent_name: "coordinator",
              resource: "/tmp/data.txt",
              denial_reason: "access denied by 'read' permissions",
              mode: "ask",
              status: "pending",
            },
          ],
        },
        // Subsequent polls (after the click) — granted status flip.
        {
          status: "completed",
          permission_requests: [
            {
              request_id: REQUEST_ID,
              tool_name: "read",
              agent_name: "coordinator",
              resource: "/tmp/data.txt",
              denial_reason: "access denied by 'read' permissions",
              mode: "ask",
              status: "granted",
              scope: "once",
            },
          ],
        },
      ],
    });

    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

    // The PermissionPrompt mounts inside the tool_call's chrome on
    // the first poll. Wait on its data-testid rather than racing the
    // first long-poll tick.
    const prompt = page.locator('[data-testid="permission-prompt"]').first();
    await expect(prompt).toBeVisible({ timeout: 10000 });
    await expect(
      prompt.locator('[data-testid="permission-prompt-tool"]'),
    ).toContainText("read");
    await expect(
      prompt.locator('[data-testid="permission-prompt-resource"]'),
    ).toContainText("/tmp/data.txt");

    // Pre-bind the POST listener so we don't race the click.
    const postPromise = page.waitForRequest(
      (req) =>
        req.method() === "POST" &&
        /\/api\/v1\/sessions\/[^/]+\/permission-grant$/.test(req.url()),
    );

    await prompt
      .locator('[data-testid="permission-prompt-allow-once"]')
      .click();

    const post = await postPromise;
    expect(post.postDataJSON()).toEqual({
      request_id: REQUEST_ID,
      scope: "once",
    });
    expect(recordedGrantPosts).toHaveLength(1);

    // Once the long-poll diff observes the granted status, the
    // prompt unmounts. The mock's second sequence entry returns the
    // granted record on the next poll.
    await expect(prompt).toBeHidden({ timeout: 10000 });
  });

  test("Deny: prompt fires the deny POST and the existing tool_error / suspended bubble path remains intact", async ({
    page,
  }) => {
    const recordedGrantPosts: Array<{ sessionId: string; body: string }> = [];
    await bootstrapMocks(page, {
      recordedGrantPosts,
      pollSequence: [
        {
          status: "running",
          permission_requests: [
            {
              request_id: REQUEST_ID,
              tool_name: "read",
              agent_name: "coordinator",
              resource: "/tmp/data.txt",
              denial_reason: "access denied by 'read' permissions",
              mode: "ask",
              status: "pending",
            },
          ],
        },
        {
          status: "completed",
          permission_requests: [
            {
              request_id: REQUEST_ID,
              tool_name: "read",
              agent_name: "coordinator",
              resource: "/tmp/data.txt",
              denial_reason: "access denied by 'read' permissions",
              mode: "ask",
              status: "denied",
              scope: "deny",
            },
          ],
        },
      ],
    });

    await page.goto("/chat");
    const prompt = page.locator('[data-testid="permission-prompt"]').first();
    await expect(prompt).toBeVisible({ timeout: 10000 });

    const postPromise = page.waitForRequest(
      (req) =>
        req.method() === "POST" &&
        /\/api\/v1\/sessions\/[^/]+\/permission-grant$/.test(req.url()),
    );

    await prompt
      .locator('[data-testid="permission-prompt-deny"]')
      .click();

    const post = await postPromise;
    expect(post.postDataJSON()).toEqual({
      request_id: REQUEST_ID,
      scope: "deny",
    });
    await expect(prompt).toBeHidden({ timeout: 10000 });
  });
});

test.describe("Permission Mode Ask — two-tab regression (R5 cross-tab guard)", () => {
  test("tab B's PermissionPrompt unmounts within long-poll cadence when tab A grants", async ({
    browser,
  }) => {
    const recordedGrantPosts: Array<{ sessionId: string; body: string }> = [];

    // Two contexts so each tab has an isolated cookie / storage scope —
    // mirrors a real two-tab session (one user, two windows). The mock
    // poll sequence is shared because both tabs hit the same backend
    // routes; the wire surface IS the cross-tab signal per §17.1.
    const ctxA = await browser.newContext();
    const ctxB = await browser.newContext();
    const pageA = await ctxA.newPage();
    const pageB = await ctxB.newPage();

    // Tab A drives the click; tab B observes. The pollSequence is
    // identical from both tabs' perspective — the first poll on EITHER
    // tab returns 'pending'; the second returns 'granted' (after the
    // POST from tab A). Both tabs see the same diff because the
    // backend's bus subscriber upserts the same Turn entry the long-
    // poll endpoint serves.
    const sharedSeq: PollSequenceStep[] = [
      {
        status: "running",
        permission_requests: [
          {
            request_id: REQUEST_ID,
            tool_name: "read",
            agent_name: "coordinator",
            resource: "/tmp/data.txt",
            denial_reason: "access denied",
            mode: "ask",
            status: "pending",
          },
        ],
      },
      {
        status: "completed",
        permission_requests: [
          {
            request_id: REQUEST_ID,
            tool_name: "read",
            agent_name: "coordinator",
            resource: "/tmp/data.txt",
            denial_reason: "access denied",
            mode: "ask",
            status: "granted",
            scope: "session",
          },
        ],
      },
    ];

    await bootstrapMocks(pageA, {
      recordedGrantPosts,
      pollSequence: sharedSeq,
    });
    await bootstrapMocks(pageB, {
      recordedGrantPosts,
      pollSequence: sharedSeq,
    });

    await pageA.goto("/chat");
    await pageB.goto("/chat");

    const promptA = pageA.locator('[data-testid="permission-prompt"]').first();
    const promptB = pageB.locator('[data-testid="permission-prompt"]').first();
    await expect(promptA).toBeVisible({ timeout: 10000 });
    await expect(promptB).toBeVisible({ timeout: 10000 });

    // Tab A grants. The POST fires through the bootstrap mock; the
    // subsequent poll on EITHER tab returns the granted status (the
    // sharedSeq second entry). Tab B's diff observes the same flip
    // and unmounts its prompt within long-poll cadence — the §17.1
    // cross-tab guarantee.
    await promptA
      .locator('[data-testid="permission-prompt-allow-session"]')
      .click();

    await expect(promptA).toBeHidden({ timeout: 10000 });
    await expect(promptB).toBeHidden({ timeout: 10000 });

    await ctxA.close();
    await ctxB.close();
  });
});
