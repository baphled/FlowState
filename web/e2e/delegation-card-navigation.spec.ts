import { test, expect } from "@playwright/test";

// Live regression cover for the bug where clicking a delegation card in the
// chat thread navigated to /agents/:id (AgentInfoView) instead of loading the
// delegated child session in the same chat view. The fault was a
// <router-link to="/agents/:id" @click.prevent="...">: vue-router 4 still
// pushed the route despite the `.prevent` modifier, sending the user to
// AgentInfoView and forcing them to manually navigate back via `Chat`.
//
// The acceptance contract this spec pins:
//   1. Clicking the delegation card MUST keep the URL on `/chat` (no
//      `/agents/:id` navigation).
//   2. The chat view MUST stay mounted (no AgentInfoView render).
//   3. The chat store MUST load the delegated session (here keyed on the
//      target agent id, matching MessageBubble.loadDelegatedSession).
//   4. When several sessions exist for the delegated agent, the click MUST
//      load the child of the active parent — NOT an unrelated older session
//      that happens to share the same agent id. This pins the regression
//      reported as "no longer able to click on the delegating card and view
//      the delegated agents session" — the prior find() returned the
//      oldest-first session for that agent, which on a long-lived backend
//      almost never points at the actual child of the current parent.
test.describe("Delegation card navigation", () => {
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

  // Two sessions: parent (planner) holds the delegation message that points
  // at executor; child (executor) is what should load when the card is
  // clicked. The MessageBubble click resolves session-by-agent-id, so the
  // executor session is the one chatStore.loadSessionByAgentId('executor')
  // will switch to.
  const sessions = [
    {
      id: "session-parent-001",
      agentId: "planner",
      title: "Parent Plan",
      messageCount: 1,
      createdAt: "2026-05-01T09:00:00Z",
      updatedAt: "2026-05-01T09:00:00Z",
    },
    {
      id: "session-child-001",
      agentId: "executor",
      parentId: "session-parent-001",
      title: "Delegated Run",
      messageCount: 2,
      createdAt: "2026-05-01T09:01:00Z",
      updatedAt: "2026-05-01T09:01:00Z",
    },
  ];

  const messagesBySession: Record<string, Array<Record<string, unknown>>> = {
    "session-parent-001": [
      {
        id: "msg-parent-1",
        role: "user",
        content: "Please delegate to executor.",
        timestamp: "2026-05-01T09:00:00Z",
      },
      {
        id: "msg-delegation-1",
        role: "delegation",
        content: "delegated to executor",
        targetAgent: "executor",
        chainId: "chain-1",
        status: "completed",
        timestamp: "2026-05-01T09:00:30Z",
      },
    ],
    "session-child-001": [
      {
        id: "msg-child-1",
        role: "user",
        content: "Run the build please.",
        timestamp: "2026-05-01T09:01:00Z",
      },
      {
        id: "msg-child-2",
        role: "assistant",
        content: "CHILD SESSION ASSISTANT REPLY (executor).",
        timestamp: "2026-05-01T09:01:30Z",
      },
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
        body: JSON.stringify(messagesBySession[sessionId] ?? []),
      });
    });

    await page.route("**/api/swarm/events", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([]),
      });
    });

    // Land on the parent session so the delegation card is in the thread.
    await page.addInitScript(() => {
      localStorage.setItem("chat.currentSessionId", "session-parent-001");
      localStorage.setItem("chat.agentId", "planner");
    });

    await page.goto("/chat");
  });

  test("clicking the delegation card loads the child session in chat without routing to AgentInfoView", async ({
    page,
  }) => {
    // The delegation card must render with the executor name as a button,
    // not a router-link to /agents/executor (which used to land on the
    // AgentInfoView). We assert the affordance type explicitly so the
    // contract regresses loudly if anyone re-introduces a router-link.
    const card = page.getByTestId("delegation-agent-link").first();
    await expect(card).toBeVisible();
    await expect(card).toHaveText("executor");
    expect(await card.evaluate((el) => el.tagName)).toBe("BUTTON");

    await card.click();

    // URL stays on /chat — the bug surfaced as `/agents/executor`.
    await expect(page).toHaveURL(/\/chat$/);

    // AgentInfoView must NOT have been rendered.
    await expect(page.getByTestId("agent-info-view")).toHaveCount(0);

    // The chat thread now reflects the executor child-session history.
    const messageList = page.getByTestId("message-list");
    await expect(messageList).toContainText(
      "CHILD SESSION ASSISTANT REPLY (executor).",
    );
    await expect(messageList).toContainText("Run the build please.");

    // We're now viewing the child session — the SessionSwitcher (and the
    // entire NavBar) is hidden in child sessions, so the message-list
    // content is the navigation-target signal we use here. The chat thread
    // contains the executor's reply, confirming the chatStore actually
    // swapped sessions rather than calling a no-op handler.
    await expect(page.getByTestId("nav-bar")).toHaveCount(0);

    // The agent/model selector bar stays visible on child sessions but the
    // pickers go into read-only display mode — the user can see *which*
    // model + provider the delegated agent used but cannot change them.
    const bar = page.getByTestId("input-selector-bar");
    await expect(bar).toBeVisible();
    await expect(bar.getByTestId("agent-picker")).toHaveClass(/is-readonly/);
    await expect(bar.getByTestId("model-picker")).toHaveClass(/is-readonly/);
  });

  test("clicking the delegation card loads the child of the active parent, not an unrelated older session for the same agent", async ({
    page,
  }) => {
    // The user has been running the system for a while. The session list
    // contains an OLD standalone executor session (created before this
    // parent existed) plus the actual child created by the active parent's
    // delegation. The card click must land on the child, not the
    // unrelated older session that happens to share the agent id.
    //
    // Pre-fix: chatStore.loadSessionByAgentId did `sessions.find(s => agent
    // matches)` against an oldest-first list, so it returned the stale
    // standalone session. The user reported "we are no longer able to
    // click on the delegating card and view the delegated agents session"
    // — the click did fire but landed on the wrong session.
    const multiAgents = [
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
    const multiSessions = [
      // Old standalone executor session — shares the agent id but is NOT
      // the child of the active parent. Sorted oldest-first by the backend.
      {
        id: "session-stale-executor-001",
        agentId: "executor",
        title: "Old Standalone Executor Run",
        messageCount: 2,
        createdAt: "2026-04-15T08:00:00Z",
        updatedAt: "2026-04-15T08:00:00Z",
      },
      {
        id: "session-parent-002",
        agentId: "planner",
        title: "Active Parent Plan",
        messageCount: 1,
        createdAt: "2026-05-01T09:00:00Z",
        updatedAt: "2026-05-01T09:00:00Z",
      },
      {
        id: "session-correct-child-002",
        agentId: "executor",
        parentId: "session-parent-002",
        title: "Active Parent Delegated Run",
        messageCount: 2,
        createdAt: "2026-05-01T09:01:00Z",
        updatedAt: "2026-05-01T09:01:00Z",
      },
    ];

    const multiMessages: Record<string, Array<Record<string, unknown>>> = {
      "session-parent-002": [
        {
          id: "msg-parent-2",
          role: "delegation",
          content: "delegated to executor",
          targetAgent: "executor",
          chainId: "chain-active",
          status: "completed",
          timestamp: "2026-05-01T09:00:30Z",
        },
      ],
      "session-stale-executor-001": [
        {
          id: "msg-stale-1",
          role: "assistant",
          content: "STALE STANDALONE EXECUTOR REPLY (do not load me).",
          timestamp: "2026-04-15T08:00:30Z",
        },
      ],
      "session-correct-child-002": [
        {
          id: "msg-correct-1",
          role: "assistant",
          content: "CORRECT CHILD REPLY (active parent).",
          timestamp: "2026-05-01T09:01:30Z",
        },
      ],
    };

    // Per-test routes override the beforeEach defaults — page.route('**/x',
    // ...) replaces the prior handler for the same pattern (Playwright
    // documents this as last-registered-wins for identical URL patterns).
    await page.route("**/api/agents", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(multiAgents),
      });
    });
    await page.route("**/api/v1/sessions", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(multiSessions),
      });
    });
    await page.route("**/api/v1/sessions/**/messages", async (route) => {
      const sessionId = getSessionId(route.request().url());
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(multiMessages[sessionId] ?? []),
      });
    });

    // Park the user on the active parent so the in-thread delegation card
    // is rendered against this parent's context.
    await page.evaluate(() => {
      localStorage.setItem("chat.currentSessionId", "session-parent-002");
      localStorage.setItem("chat.agentId", "planner");
    });
    await page.goto("/chat");

    const card = page.getByTestId("delegation-agent-link").first();
    await expect(card).toBeVisible();
    await card.click();

    const messageList = page.getByTestId("message-list");
    // The active parent's child is the one that must load — its content
    // must be visible and the stale standalone session's content must NOT.
    await expect(messageList).toContainText(
      "CORRECT CHILD REPLY (active parent).",
    );
    await expect(messageList).not.toContainText(
      "STALE STANDALONE EXECUTOR REPLY",
    );
  });

  test("clicking a chainId-less delegation card does NOT jump to an unrelated standalone session for the same agent", async ({
    page,
  }) => {
    // Reported 3×: selecting an agent within session A navigated the user
    // to a COMPLETELY DIFFERENT session. The delegation card here carries
    // ONLY a targetAgent — no chainId, no childSessionId (the SSE chain_id
    // is optional and legacy persisted messages predate it). The click
    // therefore falls through to the agent-id resolver.
    //
    // Session A (planner) has NO child for executor. The only executor
    // session in the list is an UNRELATED standalone run (session B).
    // Pre-fix the resolver's global Step-2 picked the most-recent overall
    // match (session B) and hard-jumped there. The fix must keep the user
    // in session A — when no child of the active session matches the
    // agent, stay put rather than teleport to an unrelated session.
    const noChainAgents = [
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
    const noChainSessions = [
      // Active session A — planner. NOT the delegation target's agent, and
      // it has no executor child.
      {
        id: "session-A-active",
        agentId: "planner",
        title: "Active Session A",
        messageCount: 1,
        createdAt: "2026-05-01T09:00:00Z",
        updatedAt: "2026-05-01T09:00:00Z",
      },
      // Unrelated standalone executor session B — most-recent overall
      // match but NOT a child of session A.
      {
        id: "session-B-unrelated",
        agentId: "executor",
        title: "Unrelated Standalone Executor",
        messageCount: 1,
        createdAt: "2026-05-01T08:00:00Z",
        updatedAt: "2026-05-01T08:00:00Z",
      },
    ];
    const noChainMessages: Record<
      string,
      Array<Record<string, unknown>>
    > = {
      "session-A-active": [
        {
          id: "msg-A-user",
          role: "user",
          content: "ACTIVE SESSION A USER MESSAGE (stay here).",
          timestamp: "2026-05-01T09:00:00Z",
        },
        {
          // Delegation card with NO chainId — the bug-triggering payload.
          id: "msg-A-delegation",
          role: "delegation",
          content: "delegated to executor",
          targetAgent: "executor",
          status: "completed",
          timestamp: "2026-05-01T09:00:30Z",
        },
      ],
      "session-B-unrelated": [
        {
          id: "msg-B-1",
          role: "assistant",
          content: "UNRELATED SESSION B REPLY (do not jump here).",
          timestamp: "2026-05-01T08:00:30Z",
        },
      ],
    };

    await page.route("**/api/agents", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(noChainAgents),
      });
    });
    await page.route("**/api/v1/sessions", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(noChainSessions),
      });
    });
    await page.route("**/api/v1/sessions/**/messages", async (route) => {
      const sessionId = getSessionId(route.request().url());
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(noChainMessages[sessionId] ?? []),
      });
    });

    await page.evaluate(() => {
      localStorage.setItem("chat.currentSessionId", "session-A-active");
      localStorage.setItem("chat.agentId", "planner");
    });
    await page.goto("/chat");

    const messageList = page.getByTestId("message-list");
    // We start in session A — its content is visible up front.
    await expect(messageList).toContainText(
      "ACTIVE SESSION A USER MESSAGE (stay here).",
    );

    const card = page.getByTestId("delegation-agent-link").first();
    await expect(card).toBeVisible();
    await card.click();

    // The click must NOT have jumped us to the unrelated standalone
    // session B. We stay in A and never see B's content.
    await expect(messageList).toContainText(
      "ACTIVE SESSION A USER MESSAGE (stay here).",
    );
    await expect(messageList).not.toContainText(
      "UNRELATED SESSION B REPLY (do not jump here).",
    );
    // URL stays on /chat — no navigation side-effect either.
    await expect(page).toHaveURL(/\/chat$/);
  });
});
