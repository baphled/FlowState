import { test, expect, Page } from "@playwright/test";

// Chat Attachments Backend (May 2026) — PR1 round-trip.
//
// Asserts the user-facing affordance closes UI Parity B3:
//   1. Pick / drag a file → staged in composer (preview chip appears).
//   2. Send with text → POST /api/v1/sessions/{id}/attachments fires
//      first, the returned ids thread onto the subsequent POST /messages
//      body as `attachmentIds`.
//   3. Failure path: 4xx from the attachments endpoint → error toast +
//      staged file stays in place (NOT silently dropped).
//
// Pre-fix (before commits d10212e4..4ff46126) this whole flow was a
// `console.debug` no-op — the staged file was lost on send and the
// text message went without it. Plan §6 task-05 + task-03.

interface MessagesPostBody {
  content?: string;
  attachmentIds?: string[];
}

interface MockState {
  /** Bodies seen by POST /messages — last entry is most recent. */
  messagesPosts: MessagesPostBody[];
  /** Bodies seen by POST /attachments — length is the multipart request count. */
  attachmentsPosts: number;
  /** Force the next POST /attachments call to fail with this status. */
  failNextUpload: { status: number; body: string } | null;
  /**
   * PR4 task-15 — `providerForSession` lets the attachments stub
   * simulate a non-Anthropic session: any PDF upload returns 415
   * with the documented error-envelope body. Image uploads are
   * always 200 regardless of provider. Empty / unset → Anthropic.
   */
  providerForSession?: "anthropic" | "ollama";
  /**
   * PR4 task-15 — deterministic id sequencing so the assertion on
   * the messages-POST body's `attachmentIds` array can match
   * exact ids across both image+PDF uploads.
   */
  nextAttachmentIds?: string[];
}

async function setupMocks(page: Page, state: MockState): Promise<void> {
  await page.addInitScript(() => {
    // SSE stub — chat surface boots EventSource on session load. We don't
    // drive chunks in this spec; the backend round trip is the focus.
    const w = window as unknown as { [k: string]: unknown };
    class FakeEventSource {
      url: string;
      readyState = 1;
      constructor(url: string) {
        this.url = url;
      }
      addEventListener(): void {}
      removeEventListener(): void {}
      close(): void {
        this.readyState = 2;
      }
    }
    w.EventSource = FakeEventSource;
  });

  await page.route("**/api/agents", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([
        {
          id: "agent-1",
          name: "Test Agent",
          description: "attachment fixture",
          model: "claude-sonnet-4-6",
        },
      ]),
    });
  });

  await page.route("**/api/v1/models", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ providers: [] }),
    });
  });

  await page.route("**/api/v1/sessions", async (route) => {
    if (route.request().method() === "POST") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ id: "session-att", agentId: "agent-1" }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([
        {
          id: "session-att",
          agentId: "agent-1",
          currentAgentId: "agent-1",
          title: "Attachment fixture session",
          createdAt: "2026-05-12T00:00:00Z",
          updatedAt: "2026-05-12T00:00:00Z",
          messageCount: 0,
        },
      ]),
    });
  });

  await page.route("**/api/v1/sessions/*/attachments", async (route) => {
    if (route.request().method() !== "POST") {
      await route.fulfill({ status: 405, body: "method not allowed" });
      return;
    }
    state.attachmentsPosts += 1;
    if (state.failNextUpload) {
      const failure = state.failNextUpload;
      state.failNextUpload = null;
      await route.fulfill({
        status: failure.status,
        contentType: "text/plain",
        body: failure.body,
      });
      return;
    }

    // PR4 task-15 — non-Anthropic PDF rejection. Inspect the
    // multipart body for the application/pdf token; when present
    // AND the mock session is bound to a non-Anthropic provider,
    // emit the documented structured-JSON error envelope at 415.
    // Image uploads (and PDFs on Anthropic sessions) fall through
    // to the 200 path below.
    if (state.providerForSession === "ollama") {
      const rawBody = route.request().postData() ?? "";
      if (rawBody.includes("application/pdf")) {
        await route.fulfill({
          status: 415,
          contentType: "application/json",
          body: JSON.stringify({
            error: "provider_does_not_support_pdf",
            message:
              "PDF attachments require an Anthropic model; switch model or remove the PDF",
          }),
        });
        return;
      }
    }

    // PR4 task-16 — deterministic ids per file. nextAttachmentIds
    // shifts off one id per part the upload body carries; falls
    // back to the PR1-era single-id default when unspecified.
    const rawBody = route.request().postData() ?? "";
    const partCount = (
      rawBody.match(/Content-Disposition: form-data; name="files"/g) ?? []
    ).length;
    let ids: string[];
    if (state.nextAttachmentIds && state.nextAttachmentIds.length > 0) {
      ids = state.nextAttachmentIds.splice(0, partCount > 0 ? partCount : 1);
    } else {
      ids = ["att-server-1"];
    }
    const attachments = ids.map((id, idx) => {
      // Crude kind/mediaType inference from the multipart body so
      // the mock's response shape is realistic for mixed uploads.
      const mediaType =
        rawBody.includes("application/pdf") && idx === ids.length - 1
          ? "application/pdf"
          : "image/png";
      const kind = mediaType === "application/pdf" ? "document" : "image";
      return {
        id,
        kind,
        mediaType,
        sizeBytes: 7,
        originalFilename: id + (kind === "document" ? ".pdf" : ".png"),
      };
    });
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ attachments }),
    });
  });

  await page.route("**/api/v1/sessions/*/messages", async (route) => {
    if (route.request().method() !== "POST") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          id: "session-att",
          agentId: "agent-1",
          messages: [],
          messageCount: 0,
        }),
      });
      return;
    }
    const body = (route.request().postDataJSON() ?? {}) as MessagesPostBody;
    state.messagesPosts.push(body);
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        id: "session-att",
        agentId: "agent-1",
        messages: [
          {
            id: "srv-u1",
            role: "user",
            content: body.content ?? "",
            timestamp: "2026-05-12T00:00:01Z",
          },
        ],
        messageCount: 1,
        createdAt: "2026-05-12T00:00:00Z",
        updatedAt: "2026-05-12T00:00:02Z",
      }),
    });
  });
}

test.describe("Chat Attachments Backend (May 2026) — PR1 round-trip", () => {
  test("file picker → staged → send threads attachmentIds onto POST /messages", async ({
    page,
  }) => {
    const state: MockState = {
      messagesPosts: [],
      attachmentsPosts: 0,
      failNextUpload: null,
    };
    await setupMocks(page, state);
    await page.goto("/chat");

    const composer = page.getByTestId("message-input");
    await expect(composer).toBeVisible();

    // Stage one image via the hidden file input. The composer's drag-drop
    // and picker affordances both end up routing to the same staging
    // path; this exercises the picker side.
    const filePicker = page.locator('input[type="file"]').first();
    await filePicker.setInputFiles({
      name: "sample.png",
      mimeType: "image/png",
      buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a]),
    });

    // The staged-attachments row surfaces with a preview chip.
    await expect(page.getByTestId("message-input-attachments")).toBeVisible();

    // Type and send.
    await composer.fill("look at this");
    await composer.press("Enter");

    // Wait for both endpoints to fire.
    await expect.poll(() => state.attachmentsPosts, { timeout: 5000 }).toBe(1);
    await expect
      .poll(() => state.messagesPosts.length, { timeout: 5000 })
      .toBe(1);

    const sent = state.messagesPosts[0];
    expect(sent.content).toBe("look at this");
    expect(sent.attachmentIds).toEqual(["att-server-1"]);

    // Staging row clears on successful send.
    await expect(page.getByTestId("message-input-attachments")).toBeHidden();
  });

  // Chat Attachments Backend PR4 (May 2026) task-16 — PDF e2e.
  // Asserts the user-facing affordance closes:
  //   1. PDF picker → staged with file-icon chip (NOT a thumbnail).
  //   2. Send threads the PDF attachment id onto POST /messages.
  //   3. Non-Anthropic session: PDF upload returns 415, toast
  //      surfaces, no user message persists.
  //   4. Mixed image+PDF: both chips render, both ids land in the
  //      messages-POST body in source order.
  test("PDF round-trip: file-icon chip → Send threads PDF id onto POST /messages (PR4 task-16)", async ({
    page,
  }) => {
    const state: MockState = {
      messagesPosts: [],
      attachmentsPosts: 0,
      failNextUpload: null,
      providerForSession: "anthropic",
      nextAttachmentIds: ["att-pdf-1"],
    };
    await setupMocks(page, state);
    await page.goto("/chat");

    const composer = page.getByTestId("message-input");
    await expect(composer).toBeVisible();

    const filePicker = page.locator('input[type="file"]').first();
    await filePicker.setInputFiles({
      name: "whitepaper.pdf",
      mimeType: "application/pdf",
      buffer: Buffer.from("%PDF-1.4\n%fake-pdf-body\n", "utf-8"),
    });

    // Staged attachments row surfaces with the PDF chip.
    await expect(page.getByTestId("message-input-attachments")).toBeVisible();
    // File-icon badge (not an img thumbnail).
    await expect(
      page.getByTestId("message-input-attachment-doc-icon"),
    ).toBeVisible();
    // No <img> thumbnail rendered for the PDF.
    await expect(
      page.locator("img.message-input-attachment-thumb"),
    ).toHaveCount(0);

    await composer.fill("summarise this paper");
    await composer.press("Enter");

    await expect.poll(() => state.attachmentsPosts, { timeout: 5000 }).toBe(1);
    await expect
      .poll(() => state.messagesPosts.length, { timeout: 5000 })
      .toBe(1);

    const sent = state.messagesPosts[0];
    expect(sent.content).toBe("summarise this paper");
    expect(sent.attachmentIds).toEqual(["att-pdf-1"]);

    // Staging row clears on successful send.
    await expect(page.getByTestId("message-input-attachments")).toBeHidden();
  });

  test("PDF upload on a non-Anthropic session returns 415 with toast; no message sent (PR4 task-16)", async ({
    page,
  }) => {
    const state: MockState = {
      messagesPosts: [],
      attachmentsPosts: 0,
      failNextUpload: null,
      providerForSession: "ollama",
    };
    await setupMocks(page, state);
    await page.goto("/chat");

    const composer = page.getByTestId("message-input");
    await expect(composer).toBeVisible();

    const filePicker = page.locator('input[type="file"]').first();
    await filePicker.setInputFiles({
      name: "whitepaper.pdf",
      mimeType: "application/pdf",
      buffer: Buffer.from("%PDF-1.4\n%fake-pdf-body\n", "utf-8"),
    });

    await expect(page.getByTestId("message-input-attachments")).toBeVisible();

    await composer.fill("summarise this");
    await composer.press("Enter");

    // The upload attempt fires once; the message POST MUST NOT.
    await expect.poll(() => state.attachmentsPosts, { timeout: 5000 }).toBe(1);
    // Give the message POST a chance to (incorrectly) fire — it must not.
    await page.waitForTimeout(250);
    expect(state.messagesPosts).toEqual([]);

    // Error toast visible (the production fetch throws on !res.ok and
    // the composer's catch surfaces a toast — the surface contract
    // mirrors the upload-failure path).
    await expect(page.getByText(/attachment upload failed/i)).toBeVisible();

    // Staged file stays so the user can swap models and retry without
    // re-staging.
    await expect(page.getByTestId("message-input-attachments")).toBeVisible();
  });

  test("mixed image + PDF on Anthropic: both chips render, both ids land in messages-POST body (PR4 task-16)", async ({
    page,
  }) => {
    const state: MockState = {
      messagesPosts: [],
      attachmentsPosts: 0,
      failNextUpload: null,
      providerForSession: "anthropic",
      nextAttachmentIds: ["att-img-1", "att-pdf-1"],
    };
    await setupMocks(page, state);
    await page.goto("/chat");

    const composer = page.getByTestId("message-input");
    await expect(composer).toBeVisible();

    const filePicker = page.locator('input[type="file"]').first();
    await filePicker.setInputFiles([
      {
        name: "cat.png",
        mimeType: "image/png",
        buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a]),
      },
      {
        name: "paper.pdf",
        mimeType: "application/pdf",
        buffer: Buffer.from("%PDF-1.4\n%fake-pdf-body\n", "utf-8"),
      },
    ]);

    await expect(page.getByTestId("message-input-attachments")).toBeVisible();
    // Two chips total.
    await expect(
      page.locator('[data-testid^="message-input-attachment-att-"]'),
    ).toHaveCount(2);
    // Image chip shows a thumbnail.
    await expect(
      page.locator("img.message-input-attachment-thumb"),
    ).toHaveCount(1);
    // PDF chip shows the file-icon badge.
    await expect(
      page.getByTestId("message-input-attachment-doc-icon"),
    ).toBeVisible();

    await composer.fill("compare these");
    await composer.press("Enter");

    await expect.poll(() => state.attachmentsPosts, { timeout: 5000 }).toBe(1);
    await expect
      .poll(() => state.messagesPosts.length, { timeout: 5000 })
      .toBe(1);

    const sent = state.messagesPosts[0];
    expect(sent.content).toBe("compare these");
    expect(sent.attachmentIds).toEqual(["att-img-1", "att-pdf-1"]);
  });

  test("upload failure surfaces a toast and preserves staged attachments", async ({
    page,
  }) => {
    const state: MockState = {
      messagesPosts: [],
      attachmentsPosts: 0,
      failNextUpload: { status: 413, body: "attachment exceeds 5MB cap" },
    };
    await setupMocks(page, state);
    await page.goto("/chat");

    const composer = page.getByTestId("message-input");
    await expect(composer).toBeVisible();

    const filePicker = page.locator('input[type="file"]').first();
    await filePicker.setInputFiles({
      name: "too-big.png",
      mimeType: "image/png",
      buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47]),
    });

    await expect(page.getByTestId("message-input-attachments")).toBeVisible();

    await composer.fill("does this go through?");
    await composer.press("Enter");

    // The upload attempt fires once; the message POST MUST NOT.
    await expect.poll(() => state.attachmentsPosts, { timeout: 5000 }).toBe(1);
    // Give the message POST a chance to (incorrectly) fire — it must not.
    await page.waitForTimeout(250);
    expect(state.messagesPosts).toEqual([]);

    // Error toast visible.
    await expect(page.getByText(/attachment upload failed/i)).toBeVisible();

    // Staged file stays so the user can retry without re-staging — the
    // load-bearing UX promise from plan §6 task-05.
    await expect(page.getByTestId("message-input-attachments")).toBeVisible();
  });
});
