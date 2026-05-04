import { subscribeSessionStream } from '@/api'

/**
 * 60s fail-safe — if no SSE activity arrives during a send, the consumer
 * assumes the stream is dead and notifies the caller via `onTrip` so it can
 * clear isLoading. Without this the submit gate would stay locked forever
 * after a network hiccup, presenting to the user as "the chat is stuck".
 * Reset on every chunk; cancelled when the stream cleanly terminates.
 */
export const SSE_STALL_TIMEOUT_MS = 60_000

/**
 * Callbacks injected by the consumer of useSessionStream. The composable owns
 * EventSource lifecycle and watchdog timing; it is intentionally agnostic of
 * how the consumer routes payloads or recovers from errors.
 */
export interface SessionStreamCallbacks {
  /** Invoked for every SSE message event. The raw event payload is forwarded as-is. */
  onMessage: (payload: string) => void
  /** Invoked when the EventSource fires its `error` listener. */
  onError: () => void
  /**
   * Invoked when the stall watchdog trips (no SSE activity for
   * SSE_STALL_TIMEOUT_MS). The composable arms this watchdog as part of
   * connect; consumers do not need to call armWatchdog separately on connect.
   */
  onStall: () => void
}

/**
 * Public API of the streaming lifecycle. Encapsulates the single active
 * EventSource and the stall watchdog so the chat store no longer has to manage
 * module-scoped singletons.
 *
 * Contract:
 *   - At most one EventSource is open at any time. `connect` tears down the
 *     previous connection before opening a new one.
 *   - At most one watchdog timer is armed at any time. `armWatchdog` clears
 *     any prior timer before scheduling a new one.
 *   - All operations are safe to call when nothing is active (no-op).
 */
export interface SessionStream {
  /**
   * Open a new EventSource for the given session, replacing any prior
   * connection. Wires the consumer's onMessage / onError callbacks to the
   * EventSource's `message` and `error` events.
   */
  connect(sessionId: string, callbacks: SessionStreamCallbacks): void
  /**
   * Close any active EventSource and clear any pending watchdog timer.
   * Safe to call when nothing is active.
   */
  disconnect(): void
  /**
   * Arm (or re-arm) the stall watchdog. Cancels any existing timer first
   * so callers can safely call this on every chunk to indicate liveness.
   * `onTrip` fires exactly once after SSE_STALL_TIMEOUT_MS of inactivity.
   */
  armWatchdog(onTrip: () => void): void
  /**
   * Cancel any pending watchdog timer without firing it. Safe to call when
   * no timer is armed.
   */
  clearWatchdog(): void
  /** True when an EventSource is currently open. */
  isActive(): boolean
}

/**
 * useSessionStream creates a streaming lifecycle owner. Each invocation
 * returns its own closure-scoped EventSource + watchdog handle, so concurrent
 * consumers (or per-test isolation) get independent state.
 *
 * The chat store currently instantiates this once at module load to preserve
 * the pre-extraction "single in-flight stream per page" invariant. Future work
 * (PR 2) may reconcile concurrent connect attempts; this composable's contract
 * is deliberately minimal so that reconciliation logic can layer on top without
 * touching the EventSource teardown plumbing.
 */
export function useSessionStream(): SessionStream {
  // activeEventSource is the single open SSE connection. Only one is valid at
  // a time — opening a second without closing the first causes the broker to
  // register a duplicate subscriber, producing chunk duplication on the next
  // send. connect() always closes a prior connection before opening a new one.
  let activeEventSource: EventSource | null = null

  // stallWatchdog is the pending fail-safe timer. Implementation detail of the
  // streaming lifecycle, never read by the UI; kept as a closure variable so
  // it cannot accidentally become reactive.
  let stallWatchdog: ReturnType<typeof setTimeout> | null = null

  function disconnect(): void {
    if (activeEventSource !== null) {
      activeEventSource.close()
      activeEventSource = null
    }
    clearWatchdog()
  }

  function clearWatchdog(): void {
    if (stallWatchdog !== null) {
      clearTimeout(stallWatchdog)
      stallWatchdog = null
    }
  }

  function armWatchdog(onTrip: () => void): void {
    clearWatchdog()
    stallWatchdog = setTimeout(() => {
      // Mark the timer as fired BEFORE invoking onTrip so the consumer's
      // callback can synchronously re-arm via this composable without a
      // double-clear racing with the just-cleared handle.
      stallWatchdog = null
      onTrip()
    }, SSE_STALL_TIMEOUT_MS)
  }

  function connect(sessionId: string, callbacks: SessionStreamCallbacks): void {
    // Tear down any prior connection. Without this, the broker registers a
    // second subscriber and the next send produces chunk duplication. The
    // prior watchdog (if any) is also cleared — the new connection arms its
    // own fresh timer below.
    disconnect()

    activeEventSource = subscribeSessionStream(sessionId)
    activeEventSource.addEventListener('message', (event) => {
      const payload = (event as MessageEvent).data as string
      callbacks.onMessage(payload)
    })
    activeEventSource.addEventListener('error', () => {
      callbacks.onError()
    })
    // Arm the stall watchdog as part of connect so the consumer cannot forget
    // to. Re-arming on chunk activity is the consumer's responsibility (via
    // armWatchdog) — the composable knows nothing about chunk semantics.
    armWatchdog(callbacks.onStall)
  }

  function isActive(): boolean {
    return activeEventSource !== null
  }

  return {
    connect,
    disconnect,
    armWatchdog,
    clearWatchdog,
    isActive,
  }
}
