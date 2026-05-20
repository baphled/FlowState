/**
 * streamLog — bounded breadcrumb ring for the streaming subsystem.
 *
 * Why this exists: the original "have to refresh to see updates" bug took a
 * full backend session capture to diagnose because nothing observable crossed
 * the streaming code path. This module records the connect → chunk-batch →
 * watchdog-arm → watchdog-trip → reconcile-call sequence so the next
 * regression can be triaged from the browser DevTools console without
 * re-instrumenting the codebase.
 *
 * Constraints:
 *   - Bounded: at most STREAM_LOG_MAX entries; oldest dropped on overflow.
 *   - Pure structural data only — never persisted chunk text or user content.
 *   - Mirrored at `window.__flowstateStreamLog` so the maintainer can inspect
 *     it live from the browser without rebuilding with debug flags.
 *   - Zero runtime dependencies.
 */

/** Maximum number of entries retained in the ring. */
export const STREAM_LOG_MAX = 50;

export type StreamLogEntry =
  | { kind: "connect"; sessionId: string; at: number }
  | { kind: "disconnect"; sessionId: string; at: number }
  | { kind: "chunk-batch"; sessionId: string; count: number; at: number }
  | { kind: "watchdog-arm"; sessionId: string; at: number }
  | { kind: "watchdog-trip"; sessionId: string; at: number }
  | { kind: "watchdog-clear"; sessionId: string; at: number }
  | { kind: "reconcile-call"; sessionId: string; at: number }
  | {
      kind: "reconcile-result";
      sessionId: string;
      messageCount: number;
      at: number;
    }
  | { kind: "event-dropped"; sessionId: string; reason: string; at: number };

/** Input payload for recordStreamEvent — `at` is filled in by the recorder. */
export type StreamLogInput =
  | { kind: "connect"; sessionId: string }
  | { kind: "disconnect"; sessionId: string }
  | { kind: "chunk-batch"; sessionId: string; count: number }
  | { kind: "watchdog-arm"; sessionId: string }
  | { kind: "watchdog-trip"; sessionId: string }
  | { kind: "watchdog-clear"; sessionId: string }
  | { kind: "reconcile-call"; sessionId: string }
  | { kind: "reconcile-result"; sessionId: string; messageCount: number }
  | { kind: "event-dropped"; sessionId: string; reason: string };

// The live ring. Module-scoped so every consumer in the page sees the same
// breadcrumb list. Tests reset it via clearStreamLog. Do not export — clients
// must go through getStreamLog so the array reference can be swapped if we
// ever need to (no current need; future-proofing).
const ring: StreamLogEntry[] = [];

function mirrorToWindow(): void {
  if (typeof window === "undefined")
    return; // Mirror the live array (same reference) so the maintainer can watch it
    // grow in DevTools without re-fetching. The variable is intentionally
    // namespaced under `__flowstate` to make clear it's a debug surface.
  (
    window as unknown as { __flowstateStreamLog: StreamLogEntry[] }
  ).__flowstateStreamLog = ring;
}

/**
 * Record a single streaming-subsystem event. The `at` timestamp is added by
 * this function (Date.now()) so call sites stay terse. When the ring is at
 * STREAM_LOG_MAX the oldest entry is dropped.
 */
export function recordStreamEvent(input: StreamLogInput): void {
  const entry = { ...input, at: Date.now() } as StreamLogEntry;
  ring.push(entry);
  while (ring.length > STREAM_LOG_MAX) {
    ring.shift();
  }
  mirrorToWindow();
}

/**
 * Returns the live ring. Callers should treat it as read-only — mutations
 * will succeed (it's the same array) but will desync the window mirror's
 * shape from documented intent.
 */
export function getStreamLog(): StreamLogEntry[] {
  return ring;
}

/**
 * Empty the ring. Used by tests; not intended for production code paths.
 */
export function clearStreamLog(): void {
  ring.length = 0;
  mirrorToWindow();
}
