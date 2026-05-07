import { describe, it, expect } from 'vitest'
import { parseSSEPayload } from './sseEvent'

/**
 * parseSSEPayload classifies a raw SSE data line into the discriminated
 * SSEEvent union. These specs pin the classification rules, especially:
 *   - `[DONE]` is a non-JSON sentinel and must be detected before parse.
 *   - typed events (tool_call, tool_result, delegation, …) prefer the
 *     `type` discriminant over any structural shape.
 *   - untyped content/error chunks fall through to structural detection.
 *   - garbage payloads return `malformed` rather than throwing — every
 *     consumer of the union is responsible for handling that case.
 */
describe('parseSSEPayload', () => {
  it('classifies the [DONE] sentinel before attempting JSON parse', () => {
    expect(parseSSEPayload('[DONE]')).toEqual({ kind: 'done' })
  })

  it('classifies a content chunk by its content field', () => {
    expect(parseSSEPayload('{"content":"hello"}')).toEqual({ kind: 'content', content: 'hello' })
  })

  it('classifies an untyped error chunk by its error field', () => {
    expect(parseSSEPayload('{"error":"something broke"}')).toEqual({ kind: 'error', error: 'something broke' })
  })

  it('classifies an error chunk carrying the canonical "critical stream error" safeMsg as stream_critical and extracts correlation_id', () => {
    // The Go SSE pipeline emits {"error":"critical stream error","correlation_id":"<id>"}
    // when handleSessionStream's chunk-error gate hits a fatal provider
    // error (revoked OAuth, 401, model-not-found, billing/quota lockout).
    // The wire shape is shared with the transient stream_error category,
    // so the parser discriminates on the safeMsg text.
    const ev = parseSSEPayload('{"error":"critical stream error","correlation_id":"abc123"}')
    expect(ev.kind).toBe('stream_critical')
    if (ev.kind === 'stream_critical') {
      expect(ev.error).toBe('critical stream error')
      expect(ev.correlationId).toBe('abc123')
    }
  })

  it('keeps a transient error chunk on the existing kind: "error" path (regression-resistance for stream_critical gate)', () => {
    // Without this guard a future maintainer could broaden the
    // criticality discriminator to match any error text and silently
    // escalate every transient blip into a persistent banner. This spec
    // pins that "stream error" — the canonical transient safeMsg — must
    // continue to land on the existing SSEErrorEvent path.
    const ev = parseSSEPayload('{"error":"stream error","correlation_id":"xyz"}')
    expect(ev.kind).toBe('error')
    if (ev.kind === 'error') {
      expect(ev.error).toBe('stream error')
    }
  })

  it('still emits stream_critical when correlation_id is absent (defensive default to empty string)', () => {
    // The wire format always carries correlation_id but we tolerate its
    // absence so a degraded emitter still surfaces the banner. Empty
    // string lets the UI render the banner without the "Show details"
    // affordance instead of crashing the dispatch.
    const ev = parseSSEPayload('{"error":"critical stream error"}')
    expect(ev.kind).toBe('stream_critical')
    if (ev.kind === 'stream_critical') {
      expect(ev.correlationId).toBe('')
    }
  })

  it('classifies the context-window-exceeded canonical safeMsg as stream_critical and forwards the actionable copy verbatim', () => {
    // The engine emits a structured `*provider.Error` with
    // `ErrorTypeContextWindowExceeded` when the proactive overflow gate
    // refuses a request that would exceed the configured per-model
    // context limit. The api/errors.go layer maps this to a distinct
    // canonical safeMsg that names the failure mode and recommends a
    // recoverable action (trim recent tool results / start a fresh
    // session). The wire shape is the same `{error, correlation_id}`
    // envelope as the existing `stream_critical` event so the chat
    // store still routes it to the persistent CriticalErrorBanner —
    // the only difference is the verbatim message body the user sees.
    const wireMsg =
      'context window exceeded — start a fresh session or trim recent tool results before retrying'
    const payload = JSON.stringify({ error: wireMsg, correlation_id: 'ctxabc' })
    const ev = parseSSEPayload(payload)
    expect(ev.kind).toBe('stream_critical')
    if (ev.kind === 'stream_critical') {
      expect(ev.error).toMatch(/context.*(window|limit)/i)
      expect(ev.error).toMatch(/(trim|fresh|start a new|recent tool)/i)
      expect(ev.correlationId).toBe('ctxabc')
    }
  })

  it('classifies a tool_call by the type discriminant', () => {
    const ev = parseSSEPayload('{"type":"tool_call","name":"bash","status":"running","input":"ls"}')
    expect(ev.kind).toBe('tool_call')
    if (ev.kind === 'tool_call') {
      expect(ev.name).toBe('bash')
      expect(ev.status).toBe('running')
      expect(ev.input).toBe('ls')
    }
  })

  it('classifies a skill_load by the type discriminant', () => {
    const ev = parseSSEPayload('{"type":"skill_load","name":"pre-action"}')
    expect(ev).toEqual({ kind: 'skill_load', name: 'pre-action' })
  })

  it('classifies a tool_result by the type discriminant', () => {
    const ev = parseSSEPayload('{"type":"tool_result","content":"output"}')
    expect(ev).toEqual({ kind: 'tool_result', content: 'output' })
  })

  it('classifies a delegation event by the type discriminant and unpacks fields', () => {
    const payload = JSON.stringify({
      type: 'delegation',
      target_agent: 'executor',
      chain_id: 'chain-1',
      tool_calls: 3,
      last_tool: 'bash',
      status: 'running',
    })
    const ev = parseSSEPayload(payload)
    expect(ev.kind).toBe('delegation')
    if (ev.kind === 'delegation') {
      expect(ev.targetAgent).toBe('executor')
      expect(ev.chainId).toBe('chain-1')
      expect(ev.toolCalls).toBe(3)
      expect(ev.lastTool).toBe('bash')
      expect(ev.status).toBe('running')
      expect(ev.raw).toBe(payload)
    }
  })

  it('classifies harness_retry, harness_attempt_start, harness_complete, harness_critic_feedback by type', () => {
    expect(parseSSEPayload('{"type":"harness_retry","content":"r"}').kind).toBe('harness_retry')
    expect(parseSSEPayload('{"type":"harness_attempt_start","content":"a"}').kind).toBe('harness_attempt_start')
    expect(parseSSEPayload('{"type":"harness_complete","content":"c"}').kind).toBe('harness_complete')
    expect(parseSSEPayload('{"type":"harness_critic_feedback","content":"f"}').kind).toBe('harness_critic_feedback')
  })

  it('classifies a provider_changed event by the type discriminant and unpacks from/to/reason', () => {
    // Track B — failover transition affordance. The Go SSE pipeline
    // emits {"type":"provider_changed","from":"<provider+model>","to":"<provider+model>","reason":"<token>"}
    // when failover.StreamHook switches providers mid-request (anthropic
    // 429 → zai/glm-4.6 takes over). The chat UI dispatches this into a
    // toast notification AND updates the persistent model/provider chip
    // in the input toolbar so the user knows the answer they're now
    // streaming was produced by a different model. Reason is a stable
    // machine-readable token (rate_limited, auth_failure, ...) the
    // store maps to plain English; keeping the mapping client-side
    // decouples copy-changes from Go releases.
    const payload = JSON.stringify({
      type: 'provider_changed',
      from: 'anthropic+claude-sonnet-4-6',
      to: 'zai+glm-4.6',
      reason: 'rate_limited',
    })
    const ev = parseSSEPayload(payload)
    expect(ev.kind).toBe('provider_changed')
    if (ev.kind === 'provider_changed') {
      expect(ev.from).toBe('anthropic+claude-sonnet-4-6')
      expect(ev.to).toBe('zai+glm-4.6')
      expect(ev.reason).toBe('rate_limited')
    }
  })

  it('treats a provider_changed event with missing fields as malformed (defaults to empty strings)', () => {
    // Defensive: a future emitter that ships only `type` without the
    // metadata must NOT crash the union — the parser fills the missing
    // fields with empty strings so the consuming switch sees a
    // well-formed variant. The store's render logic treats empty
    // from/to as "unknown model" copy ("Switched to a different model");
    // this is checked in the store spec.
    const ev = parseSSEPayload('{"type":"provider_changed"}')
    expect(ev.kind).toBe('provider_changed')
    if (ev.kind === 'provider_changed') {
      expect(ev.from).toBe('')
      expect(ev.to).toBe('')
      expect(ev.reason).toBe('')
    }
  })

  it('classifies a model_active event by the type discriminant and unpacks provider/model', () => {
    // May 2026 chip-shows-selection-not-actual fix. The Go SSE pipeline
    // prepends {"type":"model_active","provider":"<id>","model":"<id>"} at
    // the start of EVERY successful stream so the chat UI's toolbar chip
    // can pivot from the user's selection to the actual model the moment
    // streaming starts. The fields are split rather than concatenated
    // (unlike provider_changed's "<provider>+<model>") because the chip
    // reads provider and model as separate keys against availableModels.
    const payload = JSON.stringify({
      type: 'model_active',
      provider: 'zai',
      model: 'glm-4.6',
    })
    const ev = parseSSEPayload(payload)
    expect(ev.kind).toBe('model_active')
    if (ev.kind === 'model_active') {
      expect(ev.provider).toBe('zai')
      expect(ev.model).toBe('glm-4.6')
    }
  })

  it('treats a model_active event with missing fields as well-formed (defaults to empty strings)', () => {
    // Defensive: a malformed wire payload must not crash the union. The
    // store's handler treats empty fields as "no information" and leaves
    // the prior chip values untouched (better than blanking it out
    // mid-conversation when the failover hook ships only the type).
    const ev = parseSSEPayload('{"type":"model_active"}')
    expect(ev.kind).toBe('model_active')
    if (ev.kind === 'model_active') {
      expect(ev.provider).toBe('')
      expect(ev.model).toBe('')
    }
  })

  it('classifies a thinking event by the type discriminant and unpacks content', () => {
    // Drop #2 — thinking SSE event. The Go side emits
    // {"type":"thinking","content":"<reasoning text>"} when the provider
    // streams reasoning tokens (Anthropic thinking_delta, glm-4.6 reasoning_content).
    // Pre-this-PR these chunks were dropped at the openaicompat adapter and
    // never serialised onto the wire, leaving the chat UI silent for tens of
    // seconds while the model reasoned. The discriminant value is "thinking"
    // (not "reasoning", not "thought") to avoid collision with future event
    // types planned by Track B such as "provider_changed".
    const ev = parseSSEPayload('{"type":"thinking","content":"let me reason step by step..."}')
    expect(ev.kind).toBe('thinking')
    if (ev.kind === 'thinking') {
      expect(ev.content).toBe('let me reason step by step...')
    }
  })

  it('returns malformed for non-JSON payloads', () => {
    expect(parseSSEPayload('not json {')).toEqual({ kind: 'malformed', raw: 'not json {' })
  })

  it('returns unknown for JSON without a recognised type or structural shape', () => {
    expect(parseSSEPayload('{"foo":"bar"}')).toEqual({ kind: 'unknown', raw: '{"foo":"bar"}' })
  })

  it('returns unknown for a JSON array (no object discriminant)', () => {
    // Top-level arrays are technically valid JSON but have no `type` field
    // and no structural shape we recognise — must classify as unknown rather
    // than crashing on the property access.
    expect(parseSSEPayload('[1,2,3]')).toEqual({ kind: 'unknown', raw: '[1,2,3]' })
  })
})
