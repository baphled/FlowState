/**
 * Defensive backstop for chat-bubble content rendering.
 *
 * The primary fix for the May 2026 chat-UI leaks (session
 * 2d8dc0ac-8ad6-4271-a479-76c5093e1dfd) lives on the backend:
 *   - internal/streaming.IsControlEvent gates harness EventType chunks
 *     out of the response/tee paths (engine.go, delegation.go).
 *   - internal/engine.UnwrapTaskResult strips the `<task_result>` wrapper
 *     from the persisted/SSE-emitted tool_result chunk.
 *   - internal/engine.sanitiseTaskError replaces raw provider errors in
 *     background_output payloads with a canonical message + correlation_id.
 *
 * This frontend backstop catches anything that slips through the backend
 * gate — for example, on reload of a session that was persisted before
 * the fix shipped, or against an older backend version. It is NOT the
 * primary fix; the brief explicitly cautioned against frontend
 * over-fixing. Its job is exclusively to prevent visible regressions of
 * the same leak class while the backend is the canonical guard.
 *
 * Three patterns are detected and replaced with a friendly fallback:
 *   1. Leading harness JSON: `{"attempt":N,"maxRetries":M}...`
 *   2. Wrapping `<task_result>...</task_result>` block.
 *   3. Background-output failure JSON: shape with `"error"` + `"status":"failed"`
 *      + `"task_id"` (with or without correlation_id).
 *
 * The function never throws; on any unexpected input it returns the
 * original string unchanged.
 */

const HARNESS_JSON_PREFIX =
  /^\s*\{\s*"attempt"\s*:\s*\d+\s*,\s*"maxRetries"\s*:\s*\d+\s*\}/;

const TASK_RESULT_OPEN = "<task_result>\n";
const TASK_RESULT_CLOSE = "\n</task_result>";

export interface BackstopResult {
  /** The rendered text (cleaned or original). */
  content: string;
  /**
   * When the input matched a known leak pattern, a stable id describing
   * the cleanup that was applied. UI can surface this for diagnostics
   * (e.g. an aria-label or a small inline marker). Empty string when no
   * cleanup was performed.
   */
  appliedFilter:
    | ""
    | "harness-json-prefix"
    | "task-result-wrapper"
    | "delegation-failure-json";
  /**
   * For delegation-failure-json matches, the correlation id parsed out
   * of the payload (if present) so a support lookup can be offered.
   * Empty string in all other cases.
   */
  correlationId: string;
}

/**
 * sanitiseMessageContent runs the input through the leak detectors and
 * returns the cleaned content along with metadata about which filter
 * fired (if any). Designed for use in a Vue computed; pure function.
 */
export function sanitiseMessageContent(raw: string): BackstopResult {
  if (typeof raw !== "string" || raw.length === 0) {
    return { content: raw, appliedFilter: "", correlationId: "" };
  }

  // Leak C: background-output failure JSON.
  // Detect the shape eagerly so the case where the harness JSON prefix
  // sits inside a tool_result wrapper is handled consistently with how
  // the backend serialises errors today.
  const failureMatch = matchDelegationFailure(raw);
  if (failureMatch) {
    return failureMatch;
  }

  // Leak B: <task_result> wrapper. Strip ONLY the canonical exact-match
  // wrapper (open + close) — never partial-strip, never strip an inline
  // mention. Recurse once into the unwrapped content so a wrapper holding
  // a harness JSON prefix is also caught.
  if (raw.startsWith(TASK_RESULT_OPEN) && raw.endsWith(TASK_RESULT_CLOSE)) {
    const inner = raw.slice(
      TASK_RESULT_OPEN.length,
      raw.length - TASK_RESULT_CLOSE.length,
    );
    const innerResult = sanitiseMessageContent(inner);
    if (innerResult.appliedFilter === "harness-json-prefix") {
      // Combined leak — return the harness-stripped inner with the
      // task-result-wrapper filter id as the outer cause.
      return {
        content: innerResult.content,
        appliedFilter: "task-result-wrapper",
        correlationId: "",
      };
    }
    return {
      content: innerResult.content,
      appliedFilter: "task-result-wrapper",
      correlationId: "",
    };
  }

  // Leak A: harness JSON prefix.
  if (HARNESS_JSON_PREFIX.test(raw)) {
    const cleaned = raw.replace(HARNESS_JSON_PREFIX, "").replace(/^\s+/, "");
    return {
      content: cleaned,
      appliedFilter: "harness-json-prefix",
      correlationId: "",
    };
  }

  return { content: raw, appliedFilter: "", correlationId: "" };
}

/**
 * matchDelegationFailure tries to parse raw as JSON with the shape:
 *   {"error": string, "status": "failed", "task_id": string,
 *    "correlation_id"?: string}
 *
 * Returns a BackstopResult with a friendly message when matched, or null
 * otherwise. The exact-match key set guards against false positives —
 * arbitrary tool results that happen to include "error" and "status"
 * keys are not stripped.
 */
function matchDelegationFailure(raw: string): BackstopResult | null {
  const trimmed = raw.trim();
  if (!trimmed.startsWith("{") || !trimmed.endsWith("}")) {
    return null;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return null;
  }
  if (!parsed || typeof parsed !== "object") {
    return null;
  }
  const obj = parsed as Record<string, unknown>;
  if (typeof obj.error !== "string") {
    return null;
  }
  if (obj.status !== "failed") {
    return null;
  }
  if (typeof obj.task_id !== "string") {
    return null;
  }
  const correlationId =
    typeof obj.correlation_id === "string" && obj.correlation_id.length > 0
      ? obj.correlation_id
      : "";
  // Use the sanitised error string from the backend (post-fix) when
  // available; pre-fix payloads have raw provider text in `error` so
  // collapse those to a generic safe message.
  const errLower = obj.error.toLowerCase();
  let friendly: string;
  if (errLower.includes("rate") || errLower.includes("429")) {
    friendly = "Sub-task was rate-limited — please try again in a moment.";
  } else {
    friendly = "Sub-task failed.";
  }
  if (correlationId !== "") {
    friendly += ` (id: ${correlationId})`;
  }
  return {
    content: friendly,
    appliedFilter: "delegation-failure-json",
    correlationId,
  };
}
