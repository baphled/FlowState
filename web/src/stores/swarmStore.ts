import { ref, computed } from "vue";
import { defineStore } from "pinia";
import type { SwarmEvent } from "@/types";
import { joinBaseURL } from "@/api";
import { useChatStore } from "@/stores/chatStore";

const MAX_EVENTS = 500;
const SWARM_STALL_TIMEOUT_MS = 60_000;
const SWARM_RECONNECT_BASE_DELAY_MS = 2_000;
const SWARM_RECONNECT_MAX_DELAY_MS = 30_000;
const SWARM_RECONNECT_MAX_ATTEMPTS = 5;

export const useSwarmStore = defineStore("swarm", () => {
  const events = ref<SwarmEvent[]>([]);
  const isLive = ref(false);
  const error = ref<string | null>(null);
  const abortController = ref<AbortController | null>(null);
  const reconnectAttempt = ref(0);
  let stallTimerId: ReturnType<typeof setTimeout> | null = null;
  let reconnectTimerId: ReturnType<typeof setTimeout> | null = null;
  let shouldResetReconnectAttempt = true;

  // Generation token. Every connect() increments this. The active read loop
  // captures the value at start and only mutates store state (events, isLive,
  // error) when its captured generation still matches the current. This pins
  // the M8 contract: a late `read()` resolution from a previous generation —
  // including the generation's `finally` block — must not touch the store.
  const generation = ref(0);

  function clearStallTimer(): void {
    if (stallTimerId !== null) {
      clearTimeout(stallTimerId);
      stallTimerId = null;
    }
  }

  function armStallTimer(myGeneration: number): void {
    clearStallTimer();
    stallTimerId = setTimeout(() => {
      if (myGeneration === generation.value) {
        attemptReconnect(myGeneration);
      }
    }, SWARM_STALL_TIMEOUT_MS);
  }

  function clearReconnectTimer(): void {
    if (reconnectTimerId !== null) {
      clearTimeout(reconnectTimerId);
      reconnectTimerId = null;
    }
  }

  function attemptReconnect(myGeneration: number): void {
    if (myGeneration !== generation.value) return;
    clearReconnectTimer();
    if (reconnectAttempt.value >= SWARM_RECONNECT_MAX_ATTEMPTS) {
      error.value = `Swarm stream stalled after ${SWARM_RECONNECT_MAX_ATTEMPTS} reconnect attempts`;
      isLive.value = false;
      return;
    }
    reconnectAttempt.value += 1;
    const delay = Math.min(
      SWARM_RECONNECT_BASE_DELAY_MS * Math.pow(2, reconnectAttempt.value - 1),
      SWARM_RECONNECT_MAX_DELAY_MS,
    );
    reconnectTimerId = setTimeout(() => {
      if (myGeneration === generation.value) {
        shouldResetReconnectAttempt = false;
        void connect();
      }
    }, delay);
  }

  function ingestEventLine(line: string): void {
    if (!line.startsWith("data: ")) {
      return;
    }

    const data = line.slice(6);
    if (data === "[DONE]") {
      return;
    }

    try {
      const event = JSON.parse(data) as SwarmEvent;
      if (typeof event.id !== "string") return;
      const idx = events.value.findIndex((e) => e.id === event.id);
      if (idx >= 0) {
        events.value[idx] = event;
      } else {
        const next = [...events.value, event];
        // Evict oldest entries to keep memory bounded.
        events.value =
          next.length > MAX_EVENTS
            ? next.slice(next.length - MAX_EVENTS)
            : next;
      }
      // Bug Hunt (May 2026) sibling-confusion fix — every `delegation`
      // SwarmEvent carries the chain id (event.id) plus the child
      // session id in metadata.child_session_id. Record the pair into
      // chatStore so the in-thread delegation-card click can resolve to
      // the correct sibling when a parent has delegated to the same
      // agent more than once. Idempotent — the engine emits multiple
      // status updates per chain (started, completed, …) and they
      // share the same pair.
      if (event.type === "delegation" && event.metadata) {
        const childSessionId = event.metadata["child_session_id"];
        if (typeof childSessionId === "string" && childSessionId !== "") {
          useChatStore().recordChainSession(event.id, childSessionId);
        }
      }
    } catch {
      return;
    }
  }

  // Bug-O (May 2026) per-view swarm reattach — connect() now accepts an
  // explicit sessionId so the ChatView session-change watcher can reattach
  // to the freshly-navigated-to session WITHOUT racing the chatStore
  // mutation. Falling back to useChatStore().currentSessionId preserves the
  // original onMounted call site (no-arg form). Explicit > magic capture.
  async function connect(sessionIdArg?: string): Promise<void> {
    await disconnect();
    error.value = null;
    if (shouldResetReconnectAttempt) {
      reconnectAttempt.value = 0;
    }
    shouldResetReconnectAttempt = true;

    const sessionId =
      sessionIdArg !== undefined ? sessionIdArg : useChatStore().currentSessionId;
    if (!sessionId) {
      error.value = "cannot connect to swarm events: no active session id";
      isLive.value = false;
      return;
    }

    generation.value += 1;
    const myGeneration = generation.value;

    isLive.value = true;
    abortController.value = new AbortController();

    const url = joinBaseURL(
      `/swarm/events?session_id=${encodeURIComponent(sessionId)}`,
    );

    try {
      const response = await fetch(url, {
        signal: abortController.value.signal,
        // PR3/C8 — send the flowstate_session cookie on the SSE-via-
        // fetch handshake. Without it the protected /api/swarm/events
        // endpoint returns 401 once features.auth_v1 flips on (PR5).
        credentials: "include",
      });

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`);
      }

      const reader = response.body?.getReader();
      if (!reader) {
        throw new Error("Response body is not readable");
      }

      const decoder = new TextDecoder();
      let buffer = "";
      armStallTimer(myGeneration);

      while (true) {
        const { done, value } = await reader.read();
        if (myGeneration !== generation.value) {
          return;
        }
        if (done) {
          clearStallTimer();
          buffer += decoder.decode();
          if (buffer) {
            ingestEventLine(buffer);
          }
          break;
        }
        armStallTimer(myGeneration);
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop() || "";
        for (const line of lines) {
          ingestEventLine(line);
        }
      }
    } catch (e) {
      if (myGeneration !== generation.value) {
        return;
      }
      if (e instanceof Error && e.name !== "AbortError") {
        attemptReconnect(myGeneration);
      }
    } finally {
      if (myGeneration === generation.value) {
        isLive.value = false;
      }
    }
  }

  async function disconnect(): Promise<void> {
    clearStallTimer();
    clearReconnectTimer();
    if (abortController.value) {
      abortController.value.abort();
      abortController.value = null;
    }
    isLive.value = false;
  }

  // Bug-O (May 2026) per-view swarm reattach — clear() resets store state
  // between sessions. ChatView's session-change watcher calls this between
  // disconnect() and connect(newSessionId) so the panel doesn't carry the
  // previous session's delegations / harness rows / errors into the new
  // view. Reconnect counter resets too — a clean session-change should
  // not drag the previous view's stall-ladder progress with it.
  function clear(): void {
    events.value = [];
    reconnectAttempt.value = 0;
    error.value = null;
  }

  // Expose computed for template
  const eventCount = computed(() => events.value.length);

  // Filter events by type
  const delegationEvents = computed(() =>
    events.value.filter((e) => e.type === "delegation"),
  );

  const harnessEvents = computed(() =>
    events.value.filter(
      (e) =>
        e.type === "harness_retry" ||
        e.type === "harness_attempt_start" ||
        e.type === "harness_complete" ||
        e.type === "harness_critic_feedback",
    ),
  );

  const toolEvents = computed(() =>
    events.value.filter(
      (e) => e.type === "tool_call" || e.type === "tool_result",
    ),
  );

  // Filter events by plan artifacts
  const planEvents = computed(() =>
    events.value.filter((e) => e.type === "plan"),
  );

  // Filter events by status transitions (any event with status field indicating state change)
  const statusEvents = computed(() =>
    events.value.filter(
      (e) =>
        e.status &&
        (e.status === "start" ||
          e.status === "progress" ||
          e.status === "complete" ||
          e.status === "error"),
    ),
  );

  // Filter events by review artifacts
  const reviewEvents = computed(() =>
    events.value.filter((e) => e.type === "review"),
  );

  return {
    events,
    isLive,
    error,
    connect,
    disconnect,
    clear,
    eventCount,
    delegationEvents,
    harnessEvents,
    toolEvents,
    planEvents,
    statusEvents,
    reviewEvents,
    reconnectAttempt,
  };
});
