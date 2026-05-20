import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  recordStreamEvent,
  getStreamLog,
  clearStreamLog,
  STREAM_LOG_MAX,
} from "./streamLog";

// streamLog is a small bounded ring-buffer of streaming-subsystem breadcrumbs.
// It exists so the next time the chat appears stuck the user can paste
// `window.__flowstateStreamLog` and the maintainer can see the connect →
// chunk-batch → watchdog-arm → watchdog-trip → reconcile-call sequence
// without re-instrumenting the codebase. The original "have to refresh" bug
// took a full session capture to diagnose precisely because nothing observable
// crossed the streaming subsystem.
describe("streamLog", () => {
  beforeEach(() => {
    clearStreamLog();
  });

  afterEach(() => {
    clearStreamLog();
    // Drop the window mirror so cross-test pollution can't carry across files.
    if (typeof window !== "undefined") {
      delete (window as unknown as { __flowstateStreamLog?: unknown })
        .__flowstateStreamLog;
    }
  });

  it("records a structured event entry with kind + timestamp + payload", () => {
    recordStreamEvent({ kind: "connect", sessionId: "session-1" });

    const entries = getStreamLog();
    expect(entries).toHaveLength(1);
    expect(entries[0].kind).toBe("connect");
    expect(entries[0].sessionId).toBe("session-1");
    expect(typeof entries[0].at).toBe("number");
    expect(entries[0].at).toBeGreaterThan(0);
  });

  it("caps stored entries at STREAM_LOG_MAX (oldest entries drop off the front)", () => {
    // Push more than the cap, verify the ring drops the oldest.
    const overflow = 10;
    for (let i = 0; i < STREAM_LOG_MAX + overflow; i++) {
      recordStreamEvent({
        kind: "chunk-batch",
        sessionId: "session-1",
        count: i,
      });
    }

    const entries = getStreamLog();
    expect(entries).toHaveLength(STREAM_LOG_MAX);
    // The oldest `overflow` entries (counts 0..overflow-1) must be gone.
    const firstCount =
      entries[0].kind === "chunk-batch" ? entries[0].count : -1;
    expect(firstCount).toBe(overflow);
    // The most recent entry must be the highest count we pushed.
    const lastCount =
      entries[entries.length - 1].kind === "chunk-batch"
        ? (entries[entries.length - 1] as { count: number }).count
        : -1;
    expect(lastCount).toBe(STREAM_LOG_MAX + overflow - 1);
  });

  it("mirrors the live ring onto window.__flowstateStreamLog so it can be inspected from DevTools", () => {
    recordStreamEvent({ kind: "reconcile-call", sessionId: "session-1" });
    recordStreamEvent({
      kind: "reconcile-result",
      sessionId: "session-1",
      messageCount: 3,
    });

    const w = window as unknown as {
      __flowstateStreamLog?: ReturnType<typeof getStreamLog>;
    };
    expect(w.__flowstateStreamLog).toBeDefined();
    // The mirror is the live array — same reference each call so the user
    // can observe it grow in DevTools without re-fetching.
    expect(w.__flowstateStreamLog).toBe(getStreamLog());
    expect(w.__flowstateStreamLog).toHaveLength(2);
  });

  it("records all the lifecycle kinds we care about (connect/disconnect/watchdog-arm/watchdog-trip/watchdog-clear/reconcile-call/reconcile-result)", () => {
    recordStreamEvent({ kind: "connect", sessionId: "s" });
    recordStreamEvent({ kind: "chunk-batch", sessionId: "s", count: 5 });
    recordStreamEvent({ kind: "watchdog-arm", sessionId: "s" });
    recordStreamEvent({ kind: "watchdog-trip", sessionId: "s" });
    recordStreamEvent({ kind: "watchdog-clear", sessionId: "s" });
    recordStreamEvent({ kind: "reconcile-call", sessionId: "s" });
    recordStreamEvent({
      kind: "reconcile-result",
      sessionId: "s",
      messageCount: 7,
    });
    recordStreamEvent({ kind: "disconnect", sessionId: "s" });

    expect(getStreamLog().map((e) => e.kind)).toEqual([
      "connect",
      "chunk-batch",
      "watchdog-arm",
      "watchdog-trip",
      "watchdog-clear",
      "reconcile-call",
      "reconcile-result",
      "disconnect",
    ]);
  });

  it("clearStreamLog empties the ring (and the window mirror reflects the change)", () => {
    recordStreamEvent({ kind: "connect", sessionId: "s" });
    expect(getStreamLog()).toHaveLength(1);
    clearStreamLog();
    expect(getStreamLog()).toHaveLength(0);
    const w = window as unknown as {
      __flowstateStreamLog?: ReturnType<typeof getStreamLog>;
    };
    expect(w.__flowstateStreamLog).toHaveLength(0);
  });
});
