import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { setActivePinia, createPinia } from "pinia";
import { useSwarmStore } from "@/stores/swarmStore";
import { useChatStore } from "@/stores/chatStore";

/**
 * Test helpers — build a controllable ReadableStream-shaped reader so we can
 * drive the connect() loop step-by-step from the test, including the post-abort
 * "late chunk arrives" case that pins the M8 generation-token contract.
 */
type FakeChunk =
  | { value: Uint8Array; done?: false }
  | { value?: undefined; done: true };

const SWARM_STALL_TIMEOUT_MS = 60_000;
const SWARM_RECONNECT_BASE_DELAY_MS = 2_000;
const SWARM_RECONNECT_MAX_DELAY_MS = 30_000;
const SWARM_RECONNECT_MAX_ATTEMPTS = 5;

function controllableReader() {
  const queue: Array<
    { type: "chunk"; value: FakeChunk } | { type: "error"; value: Error }
  > = [];
  const waiters: Array<{
    resolve: (c: FakeChunk) => void;
    reject: (error: Error) => void;
  }> = [];
  let aborted = false;

  function dispatch(): void {
    while (queue.length > 0 && waiters.length > 0) {
      const waiter = waiters.shift()!;
      const queued = queue.shift()!;
      if (queued.type === "error") {
        waiter.reject(queued.value);
      } else {
        waiter.resolve(queued.value);
      }
    }
  }

  return {
    reader: {
      read: vi.fn(() => {
        if (aborted) {
          return Promise.reject(
            Object.assign(new Error("aborted"), { name: "AbortError" }),
          );
        }
        return new Promise<FakeChunk>((resolve, reject) => {
          waiters.push({ resolve, reject });
          dispatch();
        });
      }),
      cancel: vi.fn(() => {
        aborted = true;
        while (waiters.length > 0) {
          const waiter = waiters.shift()!;
          waiter.resolve({ done: true });
        }
        return Promise.resolve();
      }),
      releaseLock: vi.fn(),
    },
    emit(text: string): void {
      const value = new TextEncoder().encode(text);
      queue.push({ type: "chunk", value: { value, done: false } });
      dispatch();
    },
    close(): void {
      queue.push({ type: "chunk", value: { done: true } });
      dispatch();
    },
    fail(error: Error): void {
      queue.push({ type: "error", value: error });
      dispatch();
    },
    abort(): void {
      aborted = true;
      while (waiters.length > 0) {
        const waiter = waiters.shift()!;
        waiter.resolve({ done: true });
      }
    },
  };
}

function makeFetchResponse(
  reader: ReturnType<typeof controllableReader>["reader"],
) {
  return {
    ok: true,
    status: 200,
    body: {
      getReader: () => reader,
    },
  } as unknown as Response;
}

function flushMicrotasks(times = 5): Promise<void> {
  return (async () => {
    for (let i = 0; i < times; i++) {
      await Promise.resolve();
    }
  })();
}

function createStreamingFetchSpy() {
  const controllers: ReturnType<typeof controllableReader>[] = [];
  const fetchSpy = vi.fn((_url: string | URL, init?: RequestInit) => {
    const controller = controllableReader();
    controllers.push(controller);
    init?.signal?.addEventListener("abort", () => controller.abort(), {
      once: true,
    });
    return Promise.resolve(makeFetchResponse(controller.reader));
  });

  return { fetchSpy, controllers };
}

describe("swarmStore.connect — H5 follow-up: session_id threading", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("passes ?session_id=<currentSessionId> on the swarm/events URL when a session is active", async () => {
    const chat = useChatStore();
    chat.currentSessionId = "session-active-1";

    const ctrl = controllableReader();
    const fetchSpy = vi.fn(() =>
      Promise.resolve(makeFetchResponse(ctrl.reader)),
    );
    vi.stubGlobal("fetch", fetchSpy);

    const swarm = useSwarmStore();
    const p = swarm.connect();
    await flushMicrotasks();

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const firstCall = fetchSpy.mock.calls[0] as unknown as [
      string | URL,
      ...unknown[],
    ];
    const calledUrl = String(firstCall[0]);
    expect(calledUrl).toContain("/swarm/events");
    expect(calledUrl).toContain("session_id=session-active-1");

    // Tear down cleanly.
    ctrl.close();
    await swarm.disconnect();
    await p;
  });

  it("fails loudly without firing fetch when no session id is available (routing bug indicator)", async () => {
    const chat = useChatStore();
    chat.currentSessionId = null;

    const fetchSpy = vi.fn(() => {
      throw new Error(
        "fetch must not be called when no session id is available",
      );
    });
    vi.stubGlobal("fetch", fetchSpy);

    const swarm = useSwarmStore();
    await swarm.connect();

    expect(fetchSpy).not.toHaveBeenCalled();
    expect(swarm.error).not.toBeNull();
    expect(swarm.error).toMatch(/session/i);
    expect(swarm.isLive).toBe(false);
  });
});

describe("swarmStore.connect — M8: generation-token isolation across reconnect", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("does not append events read from a stale generation after reconnect", async () => {
    const chat = useChatStore();
    chat.currentSessionId = "session-gen-1";

    const ctrlA = controllableReader();
    const ctrlB = controllableReader();
    let call = 0;
    const fetchSpy = vi.fn(() => {
      call += 1;
      return Promise.resolve(
        makeFetchResponse(call === 1 ? ctrlA.reader : ctrlB.reader),
      );
    });
    vi.stubGlobal("fetch", fetchSpy);

    const swarm = useSwarmStore();

    // First connect — generation 1.
    const pA = swarm.connect();
    await flushMicrotasks();

    // First-generation publishes one valid event.
    ctrlA.emit(
      'data: {"id":"evt-gen1-A","type":"tool_call","timestamp":"2026-05-10T00:00:00Z","agent_id":"a"}\n',
    );
    await flushMicrotasks();
    expect(swarm.events.map((e) => e.id)).toEqual(["evt-gen1-A"]);

    // Now reconnect — generation 2. connect() awaits disconnect() which aborts gen 1,
    // but the prior read loop's pending Promise may still settle late and try to mutate.
    const pB = swarm.connect();
    await flushMicrotasks();

    // Second-generation publishes its own event.
    ctrlB.emit(
      'data: {"id":"evt-gen2-B","type":"tool_call","timestamp":"2026-05-10T00:00:01Z","agent_id":"b"}\n',
    );
    await flushMicrotasks();

    // Now simulate the gen-1 reader's late chunk landing AFTER reconnect — this
    // is the exact race M8 pins. The store must NOT append it: gen-1 is stale.
    ctrlA.emit(
      'data: {"id":"evt-gen1-LATE","type":"tool_call","timestamp":"2026-05-10T00:00:02Z","agent_id":"a"}\n',
    );
    await flushMicrotasks();

    const ids = swarm.events.map((e) => e.id);
    expect(ids).toContain("evt-gen2-B");
    expect(ids).not.toContain("evt-gen1-LATE");

    // Tear down both controllers.
    ctrlA.close();
    ctrlB.close();
    await swarm.disconnect();
    await Promise.all([pA, pB]);
  });

  it("does not flicker isLive=false from a stale generation after reconnect", async () => {
    const chat = useChatStore();
    chat.currentSessionId = "session-gen-2";

    const ctrlA = controllableReader();
    const ctrlB = controllableReader();
    let call = 0;
    const fetchSpy = vi.fn(() => {
      call += 1;
      return Promise.resolve(
        makeFetchResponse(call === 1 ? ctrlA.reader : ctrlB.reader),
      );
    });
    vi.stubGlobal("fetch", fetchSpy);

    const swarm = useSwarmStore();

    const pA = swarm.connect();
    await flushMicrotasks();
    expect(swarm.isLive).toBe(true);

    // Reconnect mid-stream.
    const pB = swarm.connect();
    await flushMicrotasks();

    // Gen 2 is now active. Force gen 1's loop to finish (close its stream) —
    // its `finally { isLive.value = false }` MUST NOT touch the live flag,
    // because gen 2 owns it.
    ctrlA.close();
    await flushMicrotasks();

    // Gen 2 still streaming, isLive must still be true.
    expect(swarm.isLive).toBe(true);

    // Clean up gen 2.
    ctrlB.close();
    await flushMicrotasks();
    expect(swarm.isLive).toBe(false);

    await Promise.all([pA, pB]);
  });
});

describe("swarmStore.connect — stall watchdog and auto-reconnect", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("triggers a reconnect attempt when the stream stalls", async () => {
    const chat = useChatStore();
    chat.currentSessionId = "session-stall-1";

    const { fetchSpy } = createStreamingFetchSpy();
    vi.stubGlobal("fetch", fetchSpy);

    const swarm = useSwarmStore();
    const connectPromise = swarm.connect();
    await flushMicrotasks();

    await vi.advanceTimersByTimeAsync(SWARM_STALL_TIMEOUT_MS);
    await flushMicrotasks();

    expect(swarm.reconnectAttempt).toBe(1);
    expect(fetchSpy).toHaveBeenCalledTimes(1);

    await swarm.disconnect();
    await flushMicrotasks();
    await connectPromise;
  });

  it("surfaces an error after the maximum reconnect attempts are exceeded", async () => {
    const chat = useChatStore();
    chat.currentSessionId = "session-stall-2";

    const { fetchSpy } = createStreamingFetchSpy();
    vi.stubGlobal("fetch", fetchSpy);

    const swarm = useSwarmStore();
    void swarm.connect();
    await flushMicrotasks();

    const reconnectDelays = [
      SWARM_RECONNECT_BASE_DELAY_MS,
      SWARM_RECONNECT_BASE_DELAY_MS * 2,
      SWARM_RECONNECT_BASE_DELAY_MS * 4,
      SWARM_RECONNECT_BASE_DELAY_MS * 8,
      SWARM_RECONNECT_MAX_DELAY_MS,
    ];

    for (const [index, delay] of reconnectDelays.entries()) {
      await vi.advanceTimersByTimeAsync(SWARM_STALL_TIMEOUT_MS);
      await flushMicrotasks();
      expect(swarm.reconnectAttempt).toBe(index + 1);

      await vi.advanceTimersByTimeAsync(delay);
      await flushMicrotasks();
    }

    await vi.advanceTimersByTimeAsync(SWARM_STALL_TIMEOUT_MS);
    await flushMicrotasks();

    expect(swarm.reconnectAttempt).toBe(SWARM_RECONNECT_MAX_ATTEMPTS);
    expect(swarm.error).toBe(
      `Swarm stream stalled after ${SWARM_RECONNECT_MAX_ATTEMPTS} reconnect attempts`,
    );
    expect(swarm.isLive).toBe(false);

    await swarm.disconnect();
    await flushMicrotasks();
  });

  it("cancels stall and reconnect timers on disconnect", async () => {
    const chat = useChatStore();
    chat.currentSessionId = "session-stall-3";

    const { fetchSpy } = createStreamingFetchSpy();
    vi.stubGlobal("fetch", fetchSpy);

    const swarm = useSwarmStore();

    const firstConnect = swarm.connect();
    await flushMicrotasks();

    await swarm.disconnect();
    await flushMicrotasks();
    await vi.advanceTimersByTimeAsync(SWARM_STALL_TIMEOUT_MS);
    await flushMicrotasks();

    expect(swarm.reconnectAttempt).toBe(0);
    expect(fetchSpy).toHaveBeenCalledTimes(1);

    const secondConnect = swarm.connect();
    await flushMicrotasks();

    await vi.advanceTimersByTimeAsync(SWARM_STALL_TIMEOUT_MS);
    await flushMicrotasks();
    expect(swarm.reconnectAttempt).toBe(1);

    await swarm.disconnect();
    await flushMicrotasks();
    await vi.advanceTimersByTimeAsync(SWARM_RECONNECT_BASE_DELAY_MS);
    await flushMicrotasks();

    expect(fetchSpy).toHaveBeenCalledTimes(2);
    expect(swarm.isLive).toBe(false);

    await Promise.all([firstConnect, secondConnect]);
  });

  it("resets the stall timer when data is received successfully", async () => {
    const chat = useChatStore();
    chat.currentSessionId = "session-stall-4";

    const { fetchSpy, controllers } = createStreamingFetchSpy();
    vi.stubGlobal("fetch", fetchSpy);

    const swarm = useSwarmStore();
    const connectPromise = swarm.connect();
    await flushMicrotasks();

    await vi.advanceTimersByTimeAsync(30_000);
    controllers[0]?.emit(
      'data: {"id":"evt-reset-1","type":"tool_call","timestamp":"2026-05-12T00:00:00Z","agent_id":"agent-reset"}\n',
    );
    await flushMicrotasks();

    await vi.advanceTimersByTimeAsync(45_000);
    await flushMicrotasks();

    expect(swarm.reconnectAttempt).toBe(0);
    expect(swarm.events.map((event) => event.id)).toContain("evt-reset-1");

    await vi.advanceTimersByTimeAsync(15_000);
    await flushMicrotasks();

    expect(swarm.reconnectAttempt).toBe(1);

    await swarm.disconnect();
    await flushMicrotasks();
    await connectPromise;
  });

  it("prevents stale generations from scheduling reconnects", async () => {
    const chat = useChatStore();
    chat.currentSessionId = "session-stall-5";

    const ctrlA = controllableReader();
    const ctrlB = controllableReader();
    let call = 0;
    const fetchSpy = vi.fn((_url: string | URL, init?: RequestInit) => {
      call += 1;
      const controller = call === 1 ? ctrlA : ctrlB;
      init?.signal?.addEventListener("abort", () => controller.abort(), {
        once: true,
      });
      return Promise.resolve(makeFetchResponse(controller.reader));
    });
    vi.stubGlobal("fetch", fetchSpy);

    const swarm = useSwarmStore();

    const firstConnect = swarm.connect();
    await flushMicrotasks();

    const secondConnect = swarm.connect();
    await flushMicrotasks();

    ctrlA.fail(new Error("late generation failure"));
    await flushMicrotasks();

    expect(swarm.reconnectAttempt).toBe(0);
    expect(fetchSpy).toHaveBeenCalledTimes(2);

    await swarm.disconnect();
    await flushMicrotasks();
    await Promise.all([firstConnect, secondConnect]);
  });
});

// Bug Hunt (May 2026) sibling-confusion fix — every delegation
// SwarmEvent ingested by swarmStore must record the (chainId,
// childSessionId) pair into chatStore.chainSessions so the in-thread
// delegation-card click can disambiguate siblings.
describe("swarmStore.ingestEventLine — delegation chain → session recording", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("records chainId → childSessionId on a `delegation` event with metadata", async () => {
    // Drive a single delegation event through connect() and assert the
    // chatStore.chainSessions map was populated.
    const chatStore = useChatStore();
    chatStore.currentSessionId = "parent-session";

    const ctrl = controllableReader();
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(makeFetchResponse(ctrl.reader)),
    );

    const swarm = useSwarmStore();
    const p = swarm.connect();
    await flushMicrotasks();

    const event = {
      id: "chain-abc",
      type: "delegation",
      status: "started",
      timestamp: "2026-05-11T09:00:00Z",
      agent_id: "executor",
      metadata: {
        child_session_id: "child-session-abc",
        parent_session_id: "parent-session",
      },
      schema_version: 1,
    };
    ctrl.emit(`data: ${JSON.stringify(event)}\n`);
    await flushMicrotasks();

    expect(chatStore.chainSessions["chain-abc"]).toBe("child-session-abc");

    ctrl.close();
    await flushMicrotasks();
    await p;
  });

  it("ignores non-delegation events and events missing child_session_id", async () => {
    const chatStore = useChatStore();
    chatStore.currentSessionId = "parent-session";

    const ctrl = controllableReader();
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(makeFetchResponse(ctrl.reader)),
    );

    const swarm = useSwarmStore();
    const p = swarm.connect();
    await flushMicrotasks();

    // tool_call event — not a delegation, MUST NOT touch the map.
    ctrl.emit(
      `data: ${JSON.stringify({
        id: "tool-1",
        type: "tool_call",
        timestamp: "2026-05-11T09:00:00Z",
        agent_id: "executor",
        metadata: { child_session_id: "should-not-record" },
        schema_version: 1,
      })}\n`,
    );
    // delegation event without child_session_id — MUST NOT record.
    ctrl.emit(
      `data: ${JSON.stringify({
        id: "chain-no-child",
        type: "delegation",
        status: "started",
        timestamp: "2026-05-11T09:00:00Z",
        agent_id: "executor",
        metadata: { parent_session_id: "parent-session" },
        schema_version: 1,
      })}\n`,
    );
    await flushMicrotasks();

    expect(chatStore.chainSessions["tool-1"]).toBeUndefined();
    expect(chatStore.chainSessions["chain-no-child"]).toBeUndefined();

    ctrl.close();
    await flushMicrotasks();
    await p;
  });
});

// Bug-O — per-view swarm reattach on session-change.
//
// Pre-fix: swarmStore.connect() captured useChatStore().currentSessionId at
// call time. The backend's eventBelongsToSession predicate filters every SSE
// chunk against that captured id for the lifetime of the read loop. When the
// user navigated into a child session, delegations spawned by THAT child
// (grand-children) were scoped to a new session id the open socket had never
// heard of, so the panel went stale.
//
// Fix: parameterise connect(sessionId?) — explicit > magic capture — and add
// clear() so the consumer (ChatView) can reset events between sessions
// without leaking stale rows from the previous view.
describe("swarmStore.connect — Bug-O per-view session reattach", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("threads an explicit sessionId argument onto the URL (preferring the argument over chatStore.currentSessionId)", async () => {
    // Pre-fix shape: connect() ignored arguments and read
    // useChatStore().currentSessionId at call time. The fix makes the
    // argument authoritative so a ChatView watcher can reattach to the
    // freshly-navigated-to session WITHOUT racing the chatStore mutation.
    const chat = useChatStore();
    chat.currentSessionId = "session-stale-in-store";

    const ctrl = controllableReader();
    const fetchSpy = vi.fn(() =>
      Promise.resolve(makeFetchResponse(ctrl.reader)),
    );
    vi.stubGlobal("fetch", fetchSpy);

    const swarm = useSwarmStore();
    const p = swarm.connect("session-explicit-arg");
    await flushMicrotasks();

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const firstCall = fetchSpy.mock.calls[0] as unknown as [
      string | URL,
      ...unknown[],
    ];
    const calledUrl = String(firstCall[0]);
    expect(calledUrl).toContain("session_id=session-explicit-arg");
    expect(calledUrl).not.toContain("session_id=session-stale-in-store");

    ctrl.close();
    await swarm.disconnect();
    await p;
  });

  it("falls back to chatStore.currentSessionId when no argument is supplied (preserves the original mount-time call site)", async () => {
    // Back-compat — onMounted does `swarmStore.connect()` (no arg). The
    // fallback path must still pick up the active session.
    const chat = useChatStore();
    chat.currentSessionId = "session-fallback-1";

    const ctrl = controllableReader();
    const fetchSpy = vi.fn(() =>
      Promise.resolve(makeFetchResponse(ctrl.reader)),
    );
    vi.stubGlobal("fetch", fetchSpy);

    const swarm = useSwarmStore();
    const p = swarm.connect();
    await flushMicrotasks();

    const calledUrl = String(
      (fetchSpy.mock.calls[0] as unknown as [string | URL])[0],
    );
    expect(calledUrl).toContain("session_id=session-fallback-1");

    ctrl.close();
    await swarm.disconnect();
    await p;
  });

  it("clear() empties events and resets reconnect / error state", async () => {
    // The companion to parameterised connect — ChatView needs to wipe
    // stale rows from the previous session before reattaching. The
    // reconnect counter and error state must reset too so a clean
    // session-change isn't dragging the previous view's stall ladder.
    const chat = useChatStore();
    chat.currentSessionId = "session-clear-1";

    const ctrl = controllableReader();
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(makeFetchResponse(ctrl.reader))),
    );

    const swarm = useSwarmStore();
    const p = swarm.connect();
    await flushMicrotasks();

    ctrl.emit(
      'data: {"id":"evt-prev-1","type":"tool_call","timestamp":"2026-05-20T00:00:00Z","agent_id":"a"}\n',
    );
    await flushMicrotasks();
    expect(swarm.events.length).toBe(1);

    // Simulate a reconnect-pending state — the previous session was
    // mid-stall when the user navigated away.
    swarm.reconnectAttempt = 3;
    swarm.error = "stale stall error from previous view";

    swarm.clear();

    expect(swarm.events).toEqual([]);
    expect(swarm.reconnectAttempt).toBe(0);
    expect(swarm.error).toBeNull();

    ctrl.close();
    await swarm.disconnect();
    await p;
  });

  it("rapid back-and-forth connect calls do not leak streams (each new connect aborts the previous fetch's AbortSignal)", async () => {
    // EventSource-leak regression pin. The store uses fetch+AbortController
    // rather than EventSource, but the contract is the same: each connect
    // must abort the previous in-flight fetch so we don't pile up sockets
    // on a chatty user that bounces between sessions.
    const chat = useChatStore();
    chat.currentSessionId = "session-bounce-0";

    const signals: AbortSignal[] = [];
    const fetchSpy = vi.fn((_url: string | URL, init?: RequestInit) => {
      if (init?.signal) signals.push(init.signal);
      const ctrl = controllableReader();
      init?.signal?.addEventListener(
        "abort",
        () => ctrl.abort(),
        { once: true },
      );
      return Promise.resolve(makeFetchResponse(ctrl.reader));
    });
    vi.stubGlobal("fetch", fetchSpy);

    const swarm = useSwarmStore();

    const p1 = swarm.connect("session-bounce-1");
    await flushMicrotasks();
    const p2 = swarm.connect("session-bounce-2");
    await flushMicrotasks();
    const p3 = swarm.connect("session-bounce-3");
    await flushMicrotasks();

    expect(fetchSpy).toHaveBeenCalledTimes(3);

    // Each prior connect must have aborted its AbortSignal — the proof a
    // sibling EventSource-style stream would be torn down rather than
    // leaked. The current (third) signal is still live.
    expect(signals[0]?.aborted).toBe(true);
    expect(signals[1]?.aborted).toBe(true);
    expect(signals[2]?.aborted).toBe(false);

    // The last-called URL pins the active session.
    const lastCallUrl = String(
      (fetchSpy.mock.calls[2] as unknown as [string | URL])[0],
    );
    expect(lastCallUrl).toContain("session_id=session-bounce-3");

    await swarm.disconnect();
    await Promise.all([p1, p2, p3]);
  });
});
