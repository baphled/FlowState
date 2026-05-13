/**
 * SSEEvent — discriminated union mirroring the typed events emitted by the
 * Go SSE pipeline (internal/api/server.go writeSSE* functions and
 * sse_consumer.go). The `type` field on every payload is the discriminant
 * so a switch over `event.type` can be exhaustively checked at compile time.
 *
 * Why a union: pre-this-PR `chatStore.applyContentEvent` was an untyped
 * `Record<string, unknown>` switch with a structural-fallback branch that
 * existed for backward compatibility with an older emitter (see Principal
 * F6). The Go side now ALWAYS tags delegation events with `type: 'delegation'`
 * (see writeSSEDelegationInfo in internal/api/server.go which injects the
 * field even when wrapping a provider DelegationInfo), so the structural
 * fallback is dead code. Replacing the dispatch with an exhaustive switch
 * makes the dead-code removal safe — adding a new event type without a
 * handler now fails compile.
 *
 * Source-of-truth list (search for `Type:` in the writeSSE* helpers):
 *   - "tool_call"           writeSSEToolCall          (internal/api/server.go:1326)
 *   - "skill_load"          writeSSESkillLoad         (server.go:1350)
 *   - "tool_result"         writeSSEToolResult        (server.go:1374)
 *   - "harness_retry"       writeSSEHarnessRetry      (server.go:1398)
 *   - "harness_attempt_start" writeSSEAttemptStart    (server.go:1422)
 *   - "harness_complete"    writeSSEHarnessComplete   (server.go:1446)
 *   - "harness_critic_feedback" writeSSECriticFeedback (server.go:1470)
 *   - "delegation"          writeSSEDelegationInfo    (server.go:1524)
 *
 * Plus three structural variants that have no `type` discriminant:
 *   - content chunks (`{ content: string }`)              writeSSEContent
 *   - error events   (`{ error: string }`)                writeSSEError
 *   - the [DONE] sentinel — a literal string, not JSON
 *
 * The `parseSSEPayload` helper below classifies a raw SSE data string into
 * one of these variants. The chat store passes its `applyContentEvent` raw
 * payload through this so all consumers use the typed shape.
 */

export interface SSEContentChunkEvent {
  kind: 'content'
  content: string
}

export interface SSEErrorEvent {
  kind: 'error'
  error: string
}

/**
 * SSECriticalErrorEvent — fatal provider error event.
 *
 * Emitted by the Go SSE pipeline (writeSSEClientError with category
 * "stream_critical") when handleSessionStream's chunk-error gate
 * classifies a provider error as `provider.SeverityCritical` (revoked
 * OAuth, 401, model-not-found, billing/quota lockout). The same shape
 * comes off SSEConsumer.WriteError for the streaming-package call paths.
 *
 * Wire shape: `{"error":"critical stream error","correlation_id":"<id>"}`.
 * The wire shape is identical to a transient `stream_error` event — the
 * `error` text is the only discriminant, chosen by the engine PR for
 * forward compatibility (parsers that ignore the message text still see
 * a recognisable error event and fall through to the generic
 * `SSEErrorEvent` path). The chat store branches on this event into a
 * persistent banner affordance because the session is unrecoverable
 * until the operator intervenes (re-auth, billing, switch provider).
 *
 * `correlationId` is the server-side log-lookup token. It is surfaced to
 * the user via a "Show details" affordance so they can paste it for
 * support; the server logs the raw error under this id.
 */
export interface SSECriticalErrorEvent {
  kind: 'stream_critical'
  error: string
  correlationId: string
}

/**
 * CRITICAL_STREAM_ERROR_MESSAGE is the canonical safe-message text the
 * engine emits for fatal provider errors (see `clientError` in
 * `internal/api/errors.go` `case "stream_critical"`). The parser uses
 * this as the discriminant because the wire shape `{error, correlation_id}`
 * is shared between transient and critical error categories — only the
 * `error` text identifies criticality.
 */
export const CRITICAL_STREAM_ERROR_MESSAGE = 'critical stream error'

/**
 * CONTEXT_WINDOW_EXCEEDED_MESSAGE is the sibling canonical safe-message
 * the engine emits when its proactive context-window overflow gate
 * refuses a request (see `clientError` `case
 * "stream_critical_context_exceeded"` in `internal/api/errors.go`). The
 * wire shape is identical to a generic `stream_critical` event so the
 * chat store routes both through the same persistent banner; the
 * difference is the verbatim user-visible body, which here names the
 * failure mode and hints at recoverable user actions ("trim recent
 * tool results", "fresh session"). The Vue parser recognises this
 * exact text and emits `kind: 'stream_critical'`, letting the existing
 * CriticalErrorBanner render the actionable copy without a new union
 * variant.
 */
export const CONTEXT_WINDOW_EXCEEDED_MESSAGE =
  'context window exceeded — start a fresh session or trim recent tool results before retrying'

export interface SSEDoneEvent {
  kind: 'done'
}

export interface SSEToolCallEvent {
  kind: 'tool_call'
  name: string
  status: string
  input?: string
}

export interface SSESkillLoadEvent {
  kind: 'skill_load'
  name: string
}

export interface SSEToolResultEvent {
  kind: 'tool_result'
  content: string
}

/**
 * SSEToolErrorEvent — tool execution failure event. Emitted by the Go SSE
 * pipeline (writeSSEToolError at internal/api/sse_writers.go) when the
 * engine stamps ToolResult.IsError=true on a tool_result chunk. Distinct
 * event type so the chat store's handleToolErrorEvent can flip the matching
 * running tool_result message to status='error' in-stream — the legacy
 * `tool_result` event hard-sets status='completed', which previously hid
 * live tool failures until the post-stream history reconcile.
 *
 * Additive contract: `tool_error` does NOT replace `tool_result` — the
 * legacy event keeps firing for IsError=false chunks. Existing surfaces
 * that only listen for `tool_result` continue to work; new surfaces opt
 * into the error path by handling this discriminant.
 *
 * Field semantics:
 *   - `content` is the error message verbatim from the wire (typically
 *     prefixed with "Error: " by the engine). Empty string is tolerated
 *     so the parser never throws on a malformed wire.
 */
export interface SSEToolErrorEvent {
  kind: 'tool_error'
  content: string
}

export interface SSEDelegationEvent {
  kind: 'delegation'
  /** Raw JSON-encoded payload — the chat store unpacks specific fields. */
  raw: string
  targetAgent?: string
  chainId?: string
  toolCalls?: number
  lastTool?: string
  status?: string
}

export interface SSEHarnessRetryEvent {
  kind: 'harness_retry'
  content: string
}

export interface SSEHarnessAttemptStartEvent {
  kind: 'harness_attempt_start'
  content: string
}

export interface SSEHarnessCompleteEvent {
  kind: 'harness_complete'
  content: string
}

export interface SSEHarnessCriticFeedbackEvent {
  kind: 'harness_critic_feedback'
  content: string
}

/**
 * SSEThinkingEvent — model-reasoning event. Emitted by the Go SSE pipeline
 * when a provider streams reasoning tokens (Anthropic thinking_delta blocks,
 * openaicompat reasoning_content deltas — see Drop #1 / Drop #2 in the
 * Streaming Signal-Drop fix). The content carries the model's private
 * step-by-step reasoning. The chat store accumulates this onto the in-flight
 * assistant message's `thinkingContent` field but MUST NOT render it as the
 * visible reply — that's reserved for the eventual UI affordance Track B will
 * add.
 *
 * Discriminant chosen as "thinking" specifically to leave room for Track B's
 * planned "provider_changed" event (failover transitions).
 */
export interface SSEThinkingEvent {
  kind: 'thinking'
  content: string
}

/**
 * SSEProviderChangedEvent — failover transition event. Emitted by the Go
 * SSE pipeline (writeSSEProviderChanged at internal/api/server.go) when
 * the failover hook switches providers mid-request because the previous
 * candidate failed (rate-limit, auth, model-not-found, etc.). The chat
 * UI's chatStore dispatches this into:
 *   - A transient toast notification ("Switched to glm-4.6 — primary
 *     model is rate-limited") so the user knows the answer they're now
 *     streaming is from a different model.
 *   - A persistent update to currentProviderId / currentModelId so the
 *     toolbar chip reflects the new active model going forward.
 *
 * Field semantics:
 *   - `from` / `to` are "<provider>+<model>" joined strings (the legacy
 *     shape, e.g. "anthropic+claude-sonnet-4-6"). The store splits on
 *     "+" to reconstruct the pair when the split fields are absent.
 *   - `fromProvider` / `fromModel` / `toProvider` / `toModel` are the
 *     new split shape mirroring SSEModelActiveEvent's (provider, model)
 *     pair. The store prefers these to skip the "+" parse hop and the
 *     off-by-one bugs around model ids that themselves contain "+"
 *     (rare; openrouter). All four default to empty strings when the
 *     wire omits them (older Go emitter still on joined-only shape) so
 *     the store falls back to splitting `to` on "+".
 *   - `reason` is a stable closed-set token (rate_limited, billing,
 *     quota, overload, auth_failure, model_not_found, unavailable,
 *     timeout, unknown) the store maps to plain English. Keeping the
 *     mapping client-side decouples toast copy from Go release cadence.
 *
 * All fields default to empty strings if missing — a future
 * emitter that ships only `type` doesn't crash the dispatch, and the
 * store renders generic copy ("Switched to a different model") in
 * that degraded case.
 */
export interface SSEProviderChangedEvent {
  kind: 'provider_changed'
  from: string
  to: string
  fromProvider: string
  fromModel: string
  toProvider: string
  toModel: string
  reason: string
}

/**
 * SSEModelActiveEvent — always-on actual-model affordance.
 *
 * Emitted by the Go SSE pipeline (writeSSEModelActive at
 * internal/api/server.go) at the start of EVERY successful stream — not
 * just on failover transitions, where SSEProviderChangedEvent fires. The
 * chat UI's chatStore handles this event by updating
 * currentProviderId / currentModelId so the toolbar chip pivots from
 * the user's selection to the actual model the moment streaming starts.
 *
 * Why a separate event from provider_changed: provider_changed only fires
 * when a fallback candidate succeeded after a previous candidate failed.
 * model_active fires unconditionally, so the chip can correct itself even
 * on the common case where the actual matches the selection — and on the
 * divergent case where the actual differs without a failover (agent
 * override, manifest override), the chip still pivots to the truth.
 *
 * Field semantics:
 *   - `provider` is the canonical provider id (e.g. "anthropic", "zai").
 *   - `model` is the canonical model id (e.g. "claude-sonnet-4-6", "glm-4.6").
 *
 * The fields are split rather than concatenated (unlike provider_changed's
 * "<provider>+<model>" pair) because the chip rendering reads them as
 * separate keys against the availableModels list — splitting on "+"
 * would re-introduce a parse step and a class of off-by-one bugs around
 * model ids that themselves contain "+" (rare; openrouter).
 *
 * Both fields default to empty strings when missing — a degraded payload
 * (defensive: a malformed wire format from a future emitter) leaves the
 * chip on its prior value rather than blanking it out mid-conversation.
 */
export interface SSEModelActiveEvent {
  kind: 'model_active'
  provider: string
  model: string
}

/**
 * SSEContextUsageEvent — always-on context-window usage affordance.
 *
 * Phase 2 of the May 2026 context-window saturation fix (companion to
 * the proactive overflow gate that closes the glm-4.6 "thought into
 * the void" failure mode). The engine emits this chunk as the first
 * artefact of every Stream that has enough information to compute it
 * (token counter wired AND resolved limit > 0). The Go SSE pipeline
 * routes it through writeSSEContextUsage which injects the canonical
 * `"type":"context_usage"` discriminant.
 *
 * The chat UI renders this as a usage chip alongside the model picker.
 * Threshold colours match the CriticalErrorBanner palette:
 *   - <75%   → neutral
 *   - >=75%  → warning (rgb(217, 119, 6) — amber severity)
 *   - >=90%  → danger  (rgb(220, 38, 38) — red severity, same as
 *              the critical-error banner)
 *
 * The chip arrives BEFORE the gate refusal error chunk on the overflow
 * path (the engine pushes it onto outChan as the first goroutine step,
 * ahead of the synthetic refusal channel feed) so the user always sees
 * "how close did this get?" even when the gate denies the request.
 *
 * Field semantics:
 *   - `inputTokens` is the engine-side estimate of the prompt cost.
 *     Conservative (tiktoken / character-based heuristic).
 *   - `outputReserve` is the reserve subtracted from limit before the
 *     overflow gate compares input against usable. Defaults to 4096
 *     when MaxTokens is unstamped; clamped to a 1024 floor when the
 *     caller supplies a smaller value.
 *   - `limit` is the resolved per-(provider, model) context window.
 *   - `percentage` is round(input / limit * 100), capped at 999 on the
 *     engine side so the chip's three-digit formatter is safe.
 *   - `provider` / `model` are the canonical ids the chip pairs with
 *     the figure.
 *
 * All numeric fields default to 0 and string fields to '' when missing
 * — a degraded payload leaves the chip on its prior value rather than
 * blanking out mid-conversation.
 */
export interface SSEContextUsageEvent {
  kind: 'context_usage'
  inputTokens: number
  outputReserve: number
  limit: number
  percentage: number
  provider: string
  model: string
}

/**
 * SSEContextCompactedEvent — auto-compaction telemetry affordance.
 *
 * Slice 6b of the May 2026 context-management Phase-4 follow-ups
 * (companion to Slice 6a's gate-proximity force-fire). The engine's
 * L2 auto-compactor publishes `EventContextCompacted` on the bus
 * when a cold-prefix summary lands; the api-side bridge in
 * `internal/api/event_bridge.go` routes it onto the SSE wire via
 * `writeSSEContextCompacted` (see internal/api/server.go) which
 * injects the canonical `"type":"context_compacted"` discriminant.
 *
 * The chat store routes this into a session-scoped `lastCompaction`
 * slice + `compactionEventCount` counter; the ContextUsageChip flashes
 * for ~2 seconds and exposes a hover tooltip with the saved-tokens
 * delta (`originalTokens - summaryTokens`). The flash is purely
 * a transient acknowledgement — the chip's underlying severity figure
 * keeps tracking the next `context_usage` event in parallel so the
 * compaction is visible without blanking the live usage figure.
 *
 * Field semantics:
 *   - `sessionId` — the session the compaction fired for. Lets the
 *     store ignore events for inactive sessions (a parent watching a
 *     child's transcript via the SSE bridge would otherwise see the
 *     wrong session's compaction).
 *   - `agentId` — the manifest id that owned the compaction. Carried
 *     for completeness; the chip's tooltip does not currently surface
 *     it because the chip is per-conversation and the active agent is
 *     visible elsewhere in the toolbar.
 *   - `originalTokens` — pre-compaction token count of the cold prefix.
 *   - `summaryTokens` — post-compaction token count of the summary.
 *   - `latencyMs` — wall-clock latency of the summariser call. Carried
 *     for completeness; the tooltip does not surface it today.
 *
 * All numeric fields default to 0 and string fields to '' when missing
 * — a degraded wire payload (defensive: a future emitter that ships
 * only the type) leaves the chip's existing state untouched rather
 * than firing a spurious flash.
 */
export interface SSEContextCompactedEvent {
  kind: 'context_compacted'
  sessionId: string
  agentId: string
  originalTokens: number
  summaryTokens: number
  latencyMs: number
  /**
   * Trigger discriminant — Phase-5 Slice δ.
   *
   * Closed vocabulary on the wire:
   *   - "ratio"             → soft heuristic (`recent / budget > threshold`)
   *   - "gate_proximity"    → next request would refuse on the proactive
   *                           overflow gate (within 5% safety margin)
   *   - "model_switch"      → orchestrator.SwitchModel detected the
   *                           persisted history would saturate the new
   *                           (smaller) model's window
   *   - "tool_result_wave"  → mid-tool-loop hook fired between batches
   *                           after the persisted store crossed the
   *                           gate-proximity boundary
   *
   * Empty / unknown values are tolerated so historical events remain
   * decodable; the chip tooltip falls back to the generic
   * "saved Ns tokens" copy when the discriminant is unrecognised.
   */
  trigger: string
}

/**
 * SSEGateFailedEvent — halt-class swarm-gate failure affordance.
 *
 * Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026). The
 * engine publishes `gate.failed` on the bus when runSwarmGates /
 * dispatchMemberGates halts on a *swarm.GateError; the api-side
 * bridge in `internal/api/event_bridge.go` routes it onto the SSE
 * wire via `writeSSEGateFailed` (see internal/api/server.go) which
 * injects the canonical `"type":"gate_failed"` discriminant.
 *
 * The chat store routes this into a session-scoped `lastGateFailure`
 * slice the GateFailureBanner reads. The banner persists until either
 * Dismiss click or session change — gate failures halt the dispatch,
 * so the operator must acknowledge the affordance rather than have
 * it auto-clear.
 *
 * Field semantics:
 *   - `swarmId` — the swarm that halted (e.g. "a-team", "board-room");
 *     the banner subtitle attributes the failure.
 *   - `lifecycle` — one of "pre" | "post" | "pre-member" | "post-member".
 *     Distinguishes a swarm-boundary halt from a per-member halt.
 *   - `memberId` — the failing member when lifecycle is member-scoped;
 *     empty for swarm-level halts.
 *   - `gateName` — the manifest-supplied gate name; the banner title
 *     uses this verbatim ("Swarm gate halted: <gateName>").
 *   - `gateKind` — "ext:<name>" or "builtin:<name>"; surfaced on a
 *     power-user toggle.
 *   - `reason` — the typed *swarm.GateError.Reason; the banner body.
 *   - `cause` — the wrapped runner error's message, or empty when
 *     the halt is clean (a gate that returned without an upstream error).
 *   - `coordStoreKeys` — the keys the gate inspected, when the gate
 *     declares Inputs per Multi-Key Gate Inputs (May 2026); the banner
 *     exposes this on a "what was checked?" expander.
 *
 * All string fields default to '' when missing on the wire; coord_store_keys
 * defaults to []. A degraded payload (no fields beyond `type`) leaves the
 * banner with empty copy rather than crashing the discriminated-union
 * dispatch.
 */
export interface SSEGateFailedEvent {
  kind: 'gate_failed'
  swarmId: string
  lifecycle: string
  memberId: string
  gateName: string
  gateKind: string
  reason: string
  cause: string
  coordStoreKeys: string[]
}

/**
 * SSEStreamingHeartbeatEvent — engine-emitted liveness tick.
 *
 * Streaming Coherence Slice F (May 2026). The engine publishes
 * `streaming.heartbeat` on the bus at most every ~15s during a turn;
 * the SSE bridge projects it onto the wire under the `streaming.heartbeat`
 * type discriminant. The chat store's adaptive watchdog re-arms on
 * every heartbeat so a long-thinking turn does not trip the 60s
 * stall timeout.
 *
 * Payload's Phase discriminant lets the frontend pick a per-phase
 * threshold for the next watchdog window:
 *   - "generating" — 45s (the standard short window).
 *   - "thinking" — 120s (reasoning providers can pause for minutes).
 *   - "tool_executing" — 180s (long shell scripts, sandboxed builds).
 *   - "queued" — 300s (rate-limit backoff, sandbox queue).
 *
 * Empty / unrecognised phase falls back to the legacy 60s flat threshold.
 *
 * UI Parity PR5 — Live token counter (May 2026). The engine threads the
 * in-flight turn's cumulative output_tokens (Anthropic message_delta,
 * openaicompat trailing-chunk usage) onto every heartbeat tick under
 * the wire key `token_count`. The chat store records the value per
 * session AND computes tokens-per-second from the delta-vs-prev-tick
 * so the streaming chrome renders "1,247 tokens · 42 t/s" next to
 * the working-on label. Zero is the legitimate pre-first-UsageDelta
 * value the chat UI uses to gate the counter render (zero = "no
 * information yet, hide chip"). Forward compatibility: a heartbeat
 * from a pre-PR5 server omits the field; the parser defaults to 0.
 */
export interface SSEStreamingHeartbeatEvent {
  kind: 'streaming_heartbeat'
  phase: string
  tokenCount: number
}

/** Catch-all for unrecognised events — preserves forward compatibility. */
export interface SSEUnknownEvent {
  kind: 'unknown'
  raw: string
}

/** Returned when the payload is not parseable as JSON or a known sentinel. */
export interface SSEMalformedEvent {
  kind: 'malformed'
  raw: string
}

export type SSEEvent =
  | SSEContentChunkEvent
  | SSEErrorEvent
  | SSECriticalErrorEvent
  | SSEDoneEvent
  | SSEToolCallEvent
  | SSESkillLoadEvent
  | SSEToolResultEvent
  | SSEToolErrorEvent
  | SSEDelegationEvent
  | SSEHarnessRetryEvent
  | SSEHarnessAttemptStartEvent
  | SSEHarnessCompleteEvent
  | SSEHarnessCriticFeedbackEvent
  | SSEThinkingEvent
  | SSEProviderChangedEvent
  | SSEModelActiveEvent
  | SSEContextUsageEvent
  | SSEContextCompactedEvent
  | SSEProviderQuotaEvent
  | SSEGateFailedEvent
  | SSEStreamingHeartbeatEvent
  | SSEUnknownEvent
  | SSEMalformedEvent

/**
 * Provider Quota wire shape — Pinia store + chip subscribe via
 * applyContentEvent → handleProviderQuotaEvent.
 *
 * PR1 (commit ef40f9b0) froze the wire shape via api.sseProviderQuota
 * at internal/api/sse_writers.go:176-229 (sseProviderQuota); PR4 lit
 * up the engine emission. The discriminator-union `variant` field
 * picks which of {rateLimit, tokenSpend, notConfigured} is non-null.
 *
 * Mirrors the contract spec at web/src/types/contract.spec.ts (PR1
 * deliverable) — any change here MUST update the contract spec AND
 * the Go-side sseProviderQuota struct in lockstep.
 */
export interface SSEProviderQuotaEvent {
  kind: 'provider_quota'
  provider: string
  accountHash: string
  model: string
  observedAt: string
  stale: boolean
  storeBackend: string
  pricingSource: string
  variant: 'rate_limit' | 'token_spend' | 'not_configured'
  rateLimit: SSEProviderQuotaRateLimit | null
  tokenSpend: SSEProviderQuotaTokenSpend | null
  notConfigured: SSEProviderQuotaNotConfig | null
}

export interface SSEProviderQuotaRateLimit {
  requests: SSEProviderQuotaWindow
  tokens: SSEProviderQuotaWindow
  input: SSEProviderQuotaWindow
  output: SSEProviderQuotaWindow
  tightestPercentRemaining: number
  tightestResetAt: string
}

export interface SSEProviderQuotaWindow {
  limit: number
  remaining: number
  reset: string
}

export interface SSEProviderQuotaTokenSpend {
  spentMinor: number
  spentCurrency: string
  spentUsdMinor: number
  capMinor: number
  capCurrency: string
  period: string
  periodStart: string
  periodEnd: string
  thresholdAmber: number
  thresholdRed: number
}

export interface SSEProviderQuotaNotConfig {
  reason: string
}

/**
 * parseSSEPayload classifies a raw SSE data line into a typed SSEEvent.
 *
 * Rules:
 *   - `[DONE]` literal (the writeSSEDone sentinel) → `SSEDoneEvent`.
 *   - JSON with a recognised `type` discriminant → that branch's variant.
 *   - JSON with `content: string` → `SSEContentChunkEvent`.
 *   - JSON with `error: string` → `SSEErrorEvent`.
 *   - JSON without a recognised shape → `SSEUnknownEvent` (preserves the
 *     raw payload so a caller can log it).
 *   - non-JSON / parse error → `SSEMalformedEvent`.
 *
 * The function never throws — the caller's exhaustive switch is responsible
 * for handling each variant, including malformed.
 */
export function parseSSEPayload(payload: string): SSEEvent {
  if (payload === '[DONE]') {
    return { kind: 'done' }
  }

  let data: unknown
  try {
    data = JSON.parse(payload)
  } catch {
    return { kind: 'malformed', raw: payload }
  }

  if (!data || typeof data !== 'object') {
    return { kind: 'unknown', raw: payload }
  }

  const obj = data as Record<string, unknown>
  const type = typeof obj['type'] === 'string' ? (obj['type'] as string) : undefined

  if (type === 'tool_call') {
    return {
      kind: 'tool_call',
      name: typeof obj['name'] === 'string' ? (obj['name'] as string) : 'unknown',
      status: typeof obj['status'] === 'string' ? (obj['status'] as string) : 'running',
      input: typeof obj['input'] === 'string' ? (obj['input'] as string) : undefined,
    }
  }

  if (type === 'skill_load') {
    return {
      kind: 'skill_load',
      name: typeof obj['name'] === 'string' ? (obj['name'] as string) : 'unknown',
    }
  }

  if (type === 'tool_result') {
    return {
      kind: 'tool_result',
      content: typeof obj['content'] === 'string' ? (obj['content'] as string) : '',
    }
  }

  if (type === 'tool_error') {
    return {
      kind: 'tool_error',
      content: typeof obj['content'] === 'string' ? (obj['content'] as string) : '',
    }
  }

  if (type === 'delegation') {
    return {
      kind: 'delegation',
      raw: payload,
      targetAgent: typeof obj['target_agent'] === 'string' ? (obj['target_agent'] as string) : undefined,
      chainId: typeof obj['chain_id'] === 'string' ? (obj['chain_id'] as string) : undefined,
      toolCalls: typeof obj['tool_calls'] === 'number' ? (obj['tool_calls'] as number) : undefined,
      lastTool: typeof obj['last_tool'] === 'string' ? (obj['last_tool'] as string) : undefined,
      status: typeof obj['status'] === 'string' ? (obj['status'] as string) : undefined,
    }
  }

  if (type === 'thinking') {
    return { kind: 'thinking', content: typeof obj['content'] === 'string' ? (obj['content'] as string) : '' }
  }

  if (type === 'provider_changed') {
    return {
      kind: 'provider_changed',
      from: typeof obj['from'] === 'string' ? (obj['from'] as string) : '',
      to: typeof obj['to'] === 'string' ? (obj['to'] as string) : '',
      fromProvider:
        typeof obj['from_provider'] === 'string' ? (obj['from_provider'] as string) : '',
      fromModel: typeof obj['from_model'] === 'string' ? (obj['from_model'] as string) : '',
      toProvider: typeof obj['to_provider'] === 'string' ? (obj['to_provider'] as string) : '',
      toModel: typeof obj['to_model'] === 'string' ? (obj['to_model'] as string) : '',
      reason: typeof obj['reason'] === 'string' ? (obj['reason'] as string) : '',
    }
  }

  if (type === 'model_active') {
    return {
      kind: 'model_active',
      provider: typeof obj['provider'] === 'string' ? (obj['provider'] as string) : '',
      model: typeof obj['model'] === 'string' ? (obj['model'] as string) : '',
    }
  }

  if (type === 'context_usage') {
    return {
      kind: 'context_usage',
      inputTokens: typeof obj['input_tokens'] === 'number' ? (obj['input_tokens'] as number) : 0,
      outputReserve:
        typeof obj['output_reserve'] === 'number' ? (obj['output_reserve'] as number) : 0,
      limit: typeof obj['limit'] === 'number' ? (obj['limit'] as number) : 0,
      percentage: typeof obj['percentage'] === 'number' ? (obj['percentage'] as number) : 0,
      provider: typeof obj['provider'] === 'string' ? (obj['provider'] as string) : '',
      model: typeof obj['model'] === 'string' ? (obj['model'] as string) : '',
    }
  }

  if (type === 'context_compacted') {
    return {
      kind: 'context_compacted',
      sessionId: typeof obj['session_id'] === 'string' ? (obj['session_id'] as string) : '',
      agentId: typeof obj['agent_id'] === 'string' ? (obj['agent_id'] as string) : '',
      originalTokens:
        typeof obj['original_tokens'] === 'number' ? (obj['original_tokens'] as number) : 0,
      summaryTokens:
        typeof obj['summary_tokens'] === 'number' ? (obj['summary_tokens'] as number) : 0,
      latencyMs: typeof obj['latency_ms'] === 'number' ? (obj['latency_ms'] as number) : 0,
      // Phase-5 Slice δ — Trigger discriminant. Empty default ('')
      // tolerates historical wire payloads that pre-date the field;
      // the chip tooltip falls back to the generic "saved Ns tokens"
      // copy when the discriminant is empty / unrecognised.
      trigger: typeof obj['trigger'] === 'string' ? (obj['trigger'] as string) : '',
    }
  }

  if (type === 'provider_quota') {
    return parseProviderQuotaEvent(obj)
  }

  if (type === 'streaming.heartbeat' || type === 'streaming_heartbeat') {
    // Streaming Coherence Slice F (May 2026) — engine liveness tick.
    // Tolerate both wire formats: the canonical dotted variant per the
    // Engine Bus Event Taxonomy ADR ("streaming.heartbeat"), and the
    // underscore-only variant some SSE bridges normalise to. Phase is
    // optional; an empty value tells the frontend's adaptive watchdog
    // to fall back to the legacy 60s flat threshold.
    //
    // UI Parity PR5 (May 2026) — token_count carries the in-flight
    // turn's cumulative output_tokens. Default to 0 when the field is
    // absent (pre-PR5 server compat) or non-numeric (degraded
    // emitter). The chat UI gates the counter render on >0 so a
    // missing / zero value renders nothing rather than a misleading
    // "0 tokens".
    return {
      kind: 'streaming_heartbeat',
      phase: typeof obj['phase'] === 'string' ? (obj['phase'] as string) : '',
      tokenCount: typeof obj['token_count'] === 'number' ? (obj['token_count'] as number) : 0,
    }
  }

  if (type === 'gate_failed') {
    // Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026):
    // halt-class swarm gate failure. coord_store_keys is optional —
    // populated only when the gate declares Inputs (Multi-Key Gate
    // Inputs plan). String fields default to ''; the keys array
    // defaults to []. A degraded wire payload (only `type`) leaves
    // the banner with empty copy rather than crashing the dispatch.
    let coordStoreKeys: string[] = []
    if (Array.isArray(obj['coord_store_keys'])) {
      coordStoreKeys = (obj['coord_store_keys'] as unknown[]).filter(
        (k): k is string => typeof k === 'string',
      )
    }
    return {
      kind: 'gate_failed',
      swarmId: typeof obj['swarm_id'] === 'string' ? (obj['swarm_id'] as string) : '',
      lifecycle: typeof obj['lifecycle'] === 'string' ? (obj['lifecycle'] as string) : '',
      memberId: typeof obj['member_id'] === 'string' ? (obj['member_id'] as string) : '',
      gateName: typeof obj['gate_name'] === 'string' ? (obj['gate_name'] as string) : '',
      gateKind: typeof obj['gate_kind'] === 'string' ? (obj['gate_kind'] as string) : '',
      reason: typeof obj['reason'] === 'string' ? (obj['reason'] as string) : '',
      cause: typeof obj['cause'] === 'string' ? (obj['cause'] as string) : '',
      coordStoreKeys,
    }
  }

  if (type === 'harness_retry') {
    return { kind: 'harness_retry', content: typeof obj['content'] === 'string' ? (obj['content'] as string) : '' }
  }
  if (type === 'harness_attempt_start') {
    return { kind: 'harness_attempt_start', content: typeof obj['content'] === 'string' ? (obj['content'] as string) : '' }
  }
  if (type === 'harness_complete') {
    return { kind: 'harness_complete', content: typeof obj['content'] === 'string' ? (obj['content'] as string) : '' }
  }
  if (type === 'harness_critic_feedback') {
    return { kind: 'harness_critic_feedback', content: typeof obj['content'] === 'string' ? (obj['content'] as string) : '' }
  }

  // Untyped variants fall through to structural detection.
  if (typeof obj['content'] === 'string') {
    return { kind: 'content', content: obj['content'] as string }
  }
  if (typeof obj['error'] === 'string') {
    const errorText = obj['error'] as string
    // Critical-class fan-out gate: the engine's "stream_critical" category
    // produces the canonical safeMsg `CRITICAL_STREAM_ERROR_MESSAGE`. The
    // sibling category "stream_critical_context_exceeded" produces
    // `CONTEXT_WINDOW_EXCEEDED_MESSAGE` for the proactive context-window
    // overflow gate. Both wire shapes are identical to a transient
    // "stream_error" event, so we discriminate on the safeMsg text. The
    // correlation id is mandatory on the wire (writeSSEErrorMsg always
    // emits it) but we tolerate its absence with an empty string so a
    // degraded payload still surfaces the banner.
    if (
      errorText === CRITICAL_STREAM_ERROR_MESSAGE ||
      errorText === CONTEXT_WINDOW_EXCEEDED_MESSAGE
    ) {
      return {
        kind: 'stream_critical',
        error: errorText,
        correlationId:
          typeof obj['correlation_id'] === 'string'
            ? (obj['correlation_id'] as string)
            : '',
      }
    }
    return { kind: 'error', error: errorText }
  }

  return { kind: 'unknown', raw: payload }
}

/**
 * parseProviderQuotaEvent classifies the raw SSE provider_quota
 * payload into the typed SSEProviderQuotaEvent variant. Defensive
 * defaults throughout — a degraded wire payload returns an event
 * the chip can render as the empty/neutral state rather than
 * throwing or blanking.
 *
 * Plan §"Vue integration (OD-4 resolution)" lines 326-336 (PR4a).
 * Contract spec parity: web/src/types/contract.spec.ts.
 */
function parseProviderQuotaEvent(obj: Record<string, unknown>): SSEProviderQuotaEvent {
  const rawVariant = typeof obj['variant'] === 'string' ? (obj['variant'] as string) : ''
  let variant: SSEProviderQuotaEvent['variant'] = 'not_configured'
  if (rawVariant === 'rate_limit' || rawVariant === 'token_spend' || rawVariant === 'not_configured') {
    variant = rawVariant
  }

  const rateLimitRaw = obj['rate_limit']
  const rateLimit =
    rateLimitRaw && typeof rateLimitRaw === 'object'
      ? parseProviderQuotaRateLimit(rateLimitRaw as Record<string, unknown>)
      : null

  const tokenSpendRaw = obj['token_spend']
  const tokenSpend =
    tokenSpendRaw && typeof tokenSpendRaw === 'object'
      ? parseProviderQuotaTokenSpend(tokenSpendRaw as Record<string, unknown>)
      : null

  const notConfiguredRaw = obj['not_configured']
  const notConfigured =
    notConfiguredRaw && typeof notConfiguredRaw === 'object'
      ? {
          reason:
            typeof (notConfiguredRaw as Record<string, unknown>)['reason'] === 'string'
              ? ((notConfiguredRaw as Record<string, unknown>)['reason'] as string)
              : '',
        }
      : null

  return {
    kind: 'provider_quota',
    provider: typeof obj['provider'] === 'string' ? (obj['provider'] as string) : '',
    accountHash: typeof obj['account_hash'] === 'string' ? (obj['account_hash'] as string) : '',
    model: typeof obj['model'] === 'string' ? (obj['model'] as string) : '',
    observedAt: typeof obj['observed_at'] === 'string' ? (obj['observed_at'] as string) : '',
    stale: obj['stale'] === true,
    storeBackend: typeof obj['store_backend'] === 'string' ? (obj['store_backend'] as string) : '',
    pricingSource: typeof obj['pricing_source'] === 'string' ? (obj['pricing_source'] as string) : '',
    variant,
    rateLimit,
    tokenSpend,
    notConfigured,
  }
}

function parseProviderQuotaWindow(obj: Record<string, unknown>): SSEProviderQuotaWindow {
  return {
    limit: typeof obj['limit'] === 'number' ? (obj['limit'] as number) : -1,
    remaining: typeof obj['remaining'] === 'number' ? (obj['remaining'] as number) : -1,
    reset: typeof obj['reset'] === 'string' ? (obj['reset'] as string) : '',
  }
}

function parseProviderQuotaRateLimit(obj: Record<string, unknown>): SSEProviderQuotaRateLimit {
  return {
    requests: parseProviderQuotaWindow(
      (obj['requests'] as Record<string, unknown>) ?? {},
    ),
    tokens: parseProviderQuotaWindow(
      (obj['tokens'] as Record<string, unknown>) ?? {},
    ),
    input: parseProviderQuotaWindow(
      (obj['input'] as Record<string, unknown>) ?? {},
    ),
    output: parseProviderQuotaWindow(
      (obj['output'] as Record<string, unknown>) ?? {},
    ),
    tightestPercentRemaining:
      typeof obj['tightest_percent_remaining'] === 'number'
        ? (obj['tightest_percent_remaining'] as number)
        : -1,
    tightestResetAt:
      typeof obj['tightest_reset_at'] === 'string' ? (obj['tightest_reset_at'] as string) : '',
  }
}

function parseProviderQuotaTokenSpend(obj: Record<string, unknown>): SSEProviderQuotaTokenSpend {
  return {
    spentMinor: typeof obj['spent_minor'] === 'number' ? (obj['spent_minor'] as number) : 0,
    spentCurrency: typeof obj['spent_currency'] === 'string' ? (obj['spent_currency'] as string) : '',
    spentUsdMinor: typeof obj['spent_usd_minor'] === 'number' ? (obj['spent_usd_minor'] as number) : 0,
    capMinor: typeof obj['cap_minor'] === 'number' ? (obj['cap_minor'] as number) : 0,
    capCurrency: typeof obj['cap_currency'] === 'string' ? (obj['cap_currency'] as string) : '',
    period: typeof obj['period'] === 'string' ? (obj['period'] as string) : '',
    periodStart: typeof obj['period_start'] === 'string' ? (obj['period_start'] as string) : '',
    periodEnd: typeof obj['period_end'] === 'string' ? (obj['period_end'] as string) : '',
    thresholdAmber: typeof obj['threshold_amber'] === 'number' ? (obj['threshold_amber'] as number) : -1,
    thresholdRed: typeof obj['threshold_red'] === 'number' ? (obj['threshold_red'] as number) : -1,
  }
}

/**
 * exhaustivenessGuard is the standard Never-check pattern. Pass any
 * unhandled discriminant from a switch statement to catch missing cases
 * at compile time:
 *
 *   switch (event.kind) {
 *     case 'done': ...; break
 *     // ... every kind handled
 *     default:
 *       exhaustivenessGuard(event) // fails compile if a kind is missing
 *   }
 */
export function exhaustivenessGuard(_value: never): never {
  // Runtime-side defence — the throw should be unreachable when the type
  // system is satisfied, but adding a real throw means a forgotten case
  // surfaces as an actionable error instead of silent dead-code execution.
  throw new Error('Unhandled SSEEvent kind — switch is not exhaustive')
}
