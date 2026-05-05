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
 *   - `from` / `to` are "<provider>+<model>" strings (e.g.
 *     "anthropic+claude-sonnet-4-6"). The store splits on "+" to
 *     reconstruct the toolbar pair. The format is opaque to the parser.
 *   - `reason` is a stable closed-set token (rate_limited, billing,
 *     quota, overload, auth_failure, model_not_found, unavailable,
 *     timeout, unknown) the store maps to plain English. Keeping the
 *     mapping client-side decouples toast copy from Go release cadence.
 *
 * All three fields default to empty strings if missing — a future
 * emitter that ships only `type` doesn't crash the dispatch, and the
 * store renders generic copy ("Switched to a different model") in
 * that degraded case.
 */
export interface SSEProviderChangedEvent {
  kind: 'provider_changed'
  from: string
  to: string
  reason: string
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
  | SSEDoneEvent
  | SSEToolCallEvent
  | SSESkillLoadEvent
  | SSEToolResultEvent
  | SSEDelegationEvent
  | SSEHarnessRetryEvent
  | SSEHarnessAttemptStartEvent
  | SSEHarnessCompleteEvent
  | SSEHarnessCriticFeedbackEvent
  | SSEThinkingEvent
  | SSEProviderChangedEvent
  | SSEUnknownEvent
  | SSEMalformedEvent

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
      reason: typeof obj['reason'] === 'string' ? (obj['reason'] as string) : '',
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
    return { kind: 'error', error: obj['error'] as string }
  }

  return { kind: 'unknown', raw: payload }
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
