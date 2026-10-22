import { test, expect, Page } from "@playwright/test";

/**
 * Live UI verification for the QuestionPrompt (Question Tool,
 * Aug 2026). Mirrors `permission-mode-chip.spec.ts`'s mocking
 * patterns: full page.route mock set, localStorage session seeding,
 * and a `test.use({ baseURL })` driven by an env override so the spec
 * runs against `npm run dev` (default) or a built preview.
 *
 * Behaviour pinned (user-observable, not internal):
 *   - Sending a message whose turn suspends on the question tool
 *     renders an inline QuestionPrompt beneath the suspended question
 *     tool_call bubble, carrying the agent's question text and its
 *     options as toggle buttons.
 *   - Single-select when allow_multiple is falsy; free-text input
 *     replaces the options block when the request carries no options.
 *   - Submitting POSTs to
 *     `/api/v1/sessions/{id}/question-answer` with
 *     `{"request_id": "...", "answers": ["..."]}` and receives
 *     `{request_id, status: "answered"}`.
 *   - After the answer, the long-poll diff observes the answered
 *     status and the prompt unmounts (the turn continues).
 */

interface RecordedAnswer {
  sessionId: string;
  body: string;
}

const QUESTION_REQUEST_ID = "qreq-e2e-1";

interface TurnStateFixtureOverrides {
  /**
   * The question_requests slice the turn-state mock reports while the
   * turn is suspended. Omit for a no-question control turn.
   */
  questionRequests?: unknown[];
  turnStatus?: "running" | "completed" | "failed";
}

async function bootstrapMocks(
  page: Page,
  recordedAnswers: RecordedAnswer[],
  overrides: TurnStateFixtureOverrides = {},
): Promise<void> {
  const messages = [
    {
      id: "s1-u",
      role: "user",
      content: "hello",
      timestamp: "2026-08-28T00:00:00Z",
    },
    {
      id: "s1-a",
      role: "assistant",
      content: "world",
      timestamp: "2026-08-28T00:00:01Z",
      status: "completed",
    },
  ];
  await page.route("**/api/health", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: '{"status":"ok"}',
    }),
  );
  await page.route("**/api/agents", (route) =>
    route.fulfill({
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
    }),
  );
  await page.route("**/api/v1/models", (route) =>
    route.fulfill({
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
    }),
  );
  await page.route("**/api/v1/sessions", (route) =>
    route.fulfill({
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
          createdAt: "2026-08-28T00:00:00Z",
          updatedAt: "2026-08-28T00:00:01Z",
          messageCount: messages.length,
        },
      ]),
    }),
  );
  // Question-answer POST seam — register BEFORE the generic
  // sessions/* fall-through (longest-match semantics, mirroring the
  // permission-mode route ordering note in permission-mode-chip).
  await page.route(
    "**/api/v1/sessions/*/question-answer",
    async (route, request) => {
      if (request.method() !== "POST") {
        await route.fulfill({ status: 405 });
        return;
      }
      const body = request.postData() ?? "";
      const match = request.url().match(/\/sessions\/([^/]+)\/question-answer/);
      const sessionId = match ? decodeURIComponent(match[1]) : "";
      recordedAnswers.push({ sessionId, body });
      let requestId = "";
      try {
        requestId =
          (JSON.parse(body) as { request_id?: string }).request_id ?? "";
      } catch {
        // assertion below catches malformed bodies
      }
      // Wire contract: {request_id, status: "answered"}.
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          request_id: requestId,
          status: "answered",
        }),
      });
    },
  );
  await page.route("**/api/v1/sessions/*/messages", async (route) => {
    if (route.request().method() === "POST") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        // Phase-2 turn shape — turn_id drives the long-poll loop.
        body: JSON.stringify({
          id: "session-1",
          agentId: "agent-1",
          messages,
          messageCount: messages.length,
          createdAt: "2026-08-28T00:00:00Z",
          updatedAt: new Date().toISOString(),
          turn_id: "turn-1",
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
  // Turn-state long-poll — the suspended turn carries the pending
  // question_requests slice plus the tool_call bubble row the
  // QuestionPrompt anchors beneath. After the answer POST lands,
  // subsequent polls flip to answered + terminal so the diff clears
  // pendingQuestions and the prompt unmounts.
  await page.route("**/api/v1/sessions/*/turns/*", async (route, request) => {
    if (request.method() !== "GET") {
      await route.fulfill({ status: 405 });
      return;
    }
    const answered = recordedAnswers.length > 0;
    const questionRequests = answered
      ? [
          {
            ...(overrides.questionRequests?.[0] as Record<string, unknown>),
            status: "answered",
          },
        ]
      : (overrides.questionRequests ?? []);
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        turn_id: "turn-1",
        session_id: "session-1",
        status: answered ? "completed" : (overrides.turnStatus ?? "running"),
        started_at: "2026-08-28T00:00:02Z",
        completed_at: answered ? "2026-08-28T00:00:10Z" : null,
        model: { provider: "anthropic", id: "claude-sonnet-4-6" },
        error: "",
        messages: answered
          ? [
              ...messages,
              {
                id: "s1-final",
                role: "assistant",
                content: "proceeding with postgres",
                timestamp: "2026-08-28T00:00:09Z",
                status: "completed",
              },
            ]
          : [
              ...messages,
              {
                id: "s1-q",
                role: "tool_call",
                content: "",
                toolName: "question",
                toolInput: JSON.stringify({ question: "Which database?" }),
                timestamp: "2026-08-28T00:00:05Z",
                status: "pending",
              },
            ],
        question_requests: questionRequests,
      }),
    });
  });
  await page.route("**/api/swarm/events", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: "[]",
    }),
  );
  await page.addInitScript(() => {
    window.localStorage.setItem("chat.currentSessionId", "session-1");
  });
}

const DEV_BASE_URL =
  process.env["QUESTION_PROMPT_BASE_URL"] ?? "http://localhost:5173";
test.use({ baseURL: DEV_BASE_URL });

test.describe("QuestionPrompt — live UI", () => {
  test("renders the pending question with options, submits the selection to question-answer, and unmounts on answered", async ({
    page,
  }) => {
    const recordedAnswers: RecordedAnswer[] = [];
    await bootstrapMocks(page, recordedAnswers, {
      questionRequests: [
        {
          request_id: QUESTION_REQUEST_ID,
          tool_name: "question",
          agent_name: "Agent One",
          question: "Which database should I target?",
          options: ["postgres", "sqlite"],
          allow_multiple: false,
          status: "pending",
        },
      ],
    });

    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

    // Send a message → POST /messages returns turn_id → the long-poll
    // loop fetches the turn whose question_requests slice carries the
    // pending question anchored to the question tool_call bubble.
    await page.getByTestId("message-input").fill("go");
    await page.getByTestId("message-input").press("Enter");

    const prompt = page.locator('[data-testid="question-prompt"]');
    await expect(prompt).toBeVisible({ timeout: 10_000 });
    await expect(
      page.locator('[data-testid="question-prompt-question"]'),
    ).toHaveText("Which database should I target?");

    // Single-select: clicking postgres marks it pressed.
    await page
      .locator('[data-testid="question-prompt-option"][data-option="postgres"]')
      .click();
    await expect(
      page.locator(
        '[data-testid="question-prompt-option"][data-option="postgres"]',
      ),
    ).toHaveAttribute("aria-pressed", "true");

    // Pre-bind the waitForRequest BEFORE the click so the POST never
    // races us (mirrors the permission-mode-chip pattern).
    const postPromise = page.waitForRequest(
      (req) =>
        req.method() === "POST" &&
        /\/api\/v1\/sessions\/[^/]+\/question-answer$/.test(req.url()),
    );

    await page.getByTestId("question-prompt-submit").click();

    const post = await postPromise;
    expect(post.postDataJSON()).toEqual({
      request_id: QUESTION_REQUEST_ID,
      answers: ["postgres"],
    });
    expect(recordedAnswers).toHaveLength(1);
    expect(recordedAnswers[0]?.sessionId).toBe("session-1");

    // The next long-poll observes status:"answered" → the diff removes
    // the entry from pendingQuestions → the prompt unmounts.
    await expect(prompt).not.toBeVisible({ timeout: 10_000 });
  });

  test("free-text fallback: no options renders the input and submits the typed answer", async ({
    page,
  }) => {
    const recordedAnswers: RecordedAnswer[] = [];
    await bootstrapMocks(page, recordedAnswers, {
      questionRequests: [
        {
          request_id: QUESTION_REQUEST_ID,
          tool_name: "question",
          question: "Which region?",
          status: "pending",
        },
      ],
    });

    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

    await page.getByTestId("message-input").fill("deploy");
    await page.getByTestId("message-input").press("Enter");

    await expect(
      page.locator('[data-testid="question-prompt"]'),
    ).toBeVisible({ timeout: 10_000 });
    // No options → free-text input replaces the options block.
    await expect(
      page.locator('[data-testid="question-prompt-options"]'),
    ).toHaveCount(0);
    await expect(
      page.locator('[data-testid="question-prompt-input"]'),
    ).toBeVisible();

    await page
      .getByTestId("question-prompt-input")
      .fill("eu-west-1");
    await page.getByTestId("question-prompt-submit").click();

    await expect
      .poll(() => recordedAnswers.length, { timeout: 10_000 })
      .toBe(1);
    expect(JSON.parse(recordedAnswers[0]?.body ?? "{}")).toEqual({
      request_id: QUESTION_REQUEST_ID,
      answers: ["eu-west-1"],
    });
  });

  test("multi-select: allow_multiple submits every selected option", async ({
    page,
  }) => {
    const recordedAnswers: RecordedAnswer[] = [];
    await bootstrapMocks(page, recordedAnswers, {
      questionRequests: [
        {
          request_id: QUESTION_REQUEST_ID,
          tool_name: "question",
          question: "Which checks?",
          options: ["lint", "test", "build"],
          allow_multiple: true,
          status: "pending",
        },
      ],
    });

    await page.goto("/chat");
    await expect(page.getByTestId("message-input")).toBeVisible();

    await page.getByTestId("message-input").fill("verify");
    await page.getByTestId("message-input").press("Enter");

    await expect(
      page.locator('[data-testid="question-prompt"]'),
    ).toBeVisible({ timeout: 10_000 });

    await page
      .locator('[data-testid="question-prompt-option"][data-option="lint"]')
      .click();
    await page
      .locator('[data-testid="question-prompt-option"][data-option="build"]')
      .click();
    await page.getByTestId("question-prompt-submit").click();

    await expect
      .poll(() => recordedAnswers.length, { timeout: 10_000 })
      .toBe(1);
    expect(JSON.parse(recordedAnswers[0]?.body ?? "{}")).toEqual({
      request_id: QUESTION_REQUEST_ID,
      answers: ["lint", "build"],
    });
  });
});
