import { describe, it, expect } from "vitest";
import { parseSSEPayload } from "./sseEvent";

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
describe("parseSSEPayload", () => {
  it("classifies the [DONE] sentinel before attempting JSON parse", () => {
    expect(parseSSEPayload("[DONE]")).toEqual({ kind: "done" });
  });

  it("classifies a content chunk by its content field", () => {
    expect(parseSSEPayload('{"content":"hello"}')).toEqual({
      kind: "content",
      content: "hello",
    });
  });

  it("classifies an untyped error chunk by its error field", () => {
    expect(parseSSEPayload('{"error":"something broke"}')).toEqual({
      kind: "error",
      error: "something broke",
    });
  });

  it('classifies an error chunk carrying the canonical "critical stream error" safeMsg as stream_critical and extracts correlation_id', () => {
    // The Go SSE pipeline emits {"error":"critical stream error","correlation_id":"<id>"}
    // when handleSessionStream's chunk-error gate hits a fatal provider
    // error (revoked OAuth, 401, model-not-found, billing/quota lockout).
    // The wire shape is shared with the transient stream_error category,
    // so the parser discriminates on the safeMsg text.
    const ev = parseSSEPayload(
      '{"error":"critical stream error","correlation_id":"abc123"}',
    );
    expect(ev.kind).toBe("stream_critical");
    if (ev.kind === "stream_critical") {
      expect(ev.error).toBe("critical stream error");
      expect(ev.correlationId).toBe("abc123");
    }
  });

  it('keeps a transient error chunk on the existing kind: "error" path (regression-resistance for stream_critical gate)', () => {
    // Without this guard a future maintainer could broaden the
    // criticality discriminator to match any error text and silently
    // escalate every transient blip into a persistent banner. This spec
    // pins that "stream error" — the canonical transient safeMsg — must
    // continue to land on the existing SSEErrorEvent path.
    const ev = parseSSEPayload(
      '{"error":"stream error","correlation_id":"xyz"}',
    );
    expect(ev.kind).toBe("error");
    if (ev.kind === "error") {
      expect(ev.error).toBe("stream error");
    }
  });

  it("still emits stream_critical when correlation_id is absent (defensive default to empty string)", () => {
    // The wire format always carries correlation_id but we tolerate its
    // absence so a degraded emitter still surfaces the banner. Empty
    // string lets the UI render the banner without the "Show details"
    // affordance instead of crashing the dispatch.
    const ev = parseSSEPayload('{"error":"critical stream error"}');
    expect(ev.kind).toBe("stream_critical");
    if (ev.kind === "stream_critical") {
      expect(ev.correlationId).toBe("");
    }
  });

  it("classifies the context-window-exceeded canonical safeMsg as stream_critical and forwards the actionable copy verbatim", () => {
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
      "context window exceeded — start a fresh session or trim recent tool results before retrying";
    const payload = JSON.stringify({
      error: wireMsg,
      correlation_id: "ctxabc",
    });
    const ev = parseSSEPayload(payload);
    expect(ev.kind).toBe("stream_critical");
    if (ev.kind === "stream_critical") {
      expect(ev.error).toMatch(/context.*(window|limit)/i);
      expect(ev.error).toMatch(/(trim|fresh|start a new|recent tool)/i);
      expect(ev.correlationId).toBe("ctxabc");
    }
  });

  it("classifies a tool_call by the type discriminant", () => {
    const ev = parseSSEPayload(
      '{"type":"tool_call","name":"bash","status":"running","input":"ls"}',
    );
    expect(ev.kind).toBe("tool_call");
    if (ev.kind === "tool_call") {
      expect(ev.name).toBe("bash");
      expect(ev.status).toBe("running");
      expect(ev.input).toBe("ls");
    }
  });

  it("classifies a skill_load by the type discriminant", () => {
    const ev = parseSSEPayload('{"type":"skill_load","name":"pre-action"}');
    expect(ev).toEqual({ kind: "skill_load", name: "pre-action" });
  });

  it("classifies a tool_result by the type discriminant", () => {
    const ev = parseSSEPayload('{"type":"tool_result","content":"output"}');
    expect(ev).toEqual({ kind: "tool_result", content: "output" });
  });

  // Gap 2 (tool_error SSE wire, May 2026). Distinct from tool_result so
  // the chat store can flip the matching tool message to status='error'
  // in-stream rather than waiting for the post-stream history reconcile.
  // The wire shape mirrors tool_result (content only) — the type
  // discriminant is the load-bearing field.
  it("classifies a tool_error by the type discriminant", () => {
    const ev = parseSSEPayload(
      '{"type":"tool_error","content":"Error: bash exited non-zero"}',
    );
    expect(ev).toEqual({
      kind: "tool_error",
      content: "Error: bash exited non-zero",
    });
  });

  it("defaults tool_error content to empty string when the wire omits it (defensive — never throws)", () => {
    const ev = parseSSEPayload('{"type":"tool_error"}');
    expect(ev).toEqual({ kind: "tool_error", content: "" });
  });

  it("classifies a delegation event by the type discriminant and unpacks fields", () => {
    const payload = JSON.stringify({
      type: "delegation",
      target_agent: "executor",
      chain_id: "chain-1",
      tool_calls: 3,
      last_tool: "bash",
      status: "running",
    });
    const ev = parseSSEPayload(payload);
    expect(ev.kind).toBe("delegation");
    if (ev.kind === "delegation") {
      expect(ev.targetAgent).toBe("executor");
      expect(ev.chainId).toBe("chain-1");
      expect(ev.toolCalls).toBe(3);
      expect(ev.lastTool).toBe("bash");
      expect(ev.status).toBe("running");
      expect(ev.raw).toBe(payload);
    }
  });

  it("classifies harness_retry, harness_attempt_start, harness_complete, harness_critic_feedback by type", () => {
    expect(parseSSEPayload('{"type":"harness_retry","content":"r"}').kind).toBe(
      "harness_retry",
    );
    expect(
      parseSSEPayload('{"type":"harness_attempt_start","content":"a"}').kind,
    ).toBe("harness_attempt_start");
    expect(
      parseSSEPayload('{"type":"harness_complete","content":"c"}').kind,
    ).toBe("harness_complete");
    expect(
      parseSSEPayload('{"type":"harness_critic_feedback","content":"f"}').kind,
    ).toBe("harness_critic_feedback");
  });

  it("classifies a provider_changed event by the type discriminant and unpacks from/to/reason", () => {
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
      type: "provider_changed",
      from: "anthropic+claude-sonnet-4-6",
      to: "zai+glm-4.6",
      reason: "rate_limited",
    });
    const ev = parseSSEPayload(payload);
    expect(ev.kind).toBe("provider_changed");
    if (ev.kind === "provider_changed") {
      expect(ev.from).toBe("anthropic+claude-sonnet-4-6");
      expect(ev.to).toBe("zai+glm-4.6");
      expect(ev.reason).toBe("rate_limited");
    }
  });

  it("unpacks split fromProvider/fromModel/toProvider/toModel when the Go wire ships them", () => {
    // M3-adjacent — mirror sseModelActive's split shape on
    // sseProviderChanged. The Go SSE writer ships BOTH the legacy joined
    // fields (from / to == "<provider>+<model>") AND the new split
    // fields (from_provider / from_model / to_provider / to_model) on
    // every emit. The parser MUST surface the split fields so the
    // chatStore can skip the "+" parse hop and the off-by-one bugs
    // around model ids that themselves contain "+" (rare; openrouter).
    const payload = JSON.stringify({
      type: "provider_changed",
      from: "anthropic+claude-sonnet-4-6",
      to: "zai+glm-4.6",
      from_provider: "anthropic",
      from_model: "claude-sonnet-4-6",
      to_provider: "zai",
      to_model: "glm-4.6",
      reason: "rate_limited",
    });
    const ev = parseSSEPayload(payload);
    expect(ev.kind).toBe("provider_changed");
    if (ev.kind === "provider_changed") {
      expect(ev.from).toBe("anthropic+claude-sonnet-4-6");
      expect(ev.to).toBe("zai+glm-4.6");
      expect(ev.fromProvider).toBe("anthropic");
      expect(ev.fromModel).toBe("claude-sonnet-4-6");
      expect(ev.toProvider).toBe("zai");
      expect(ev.toModel).toBe("glm-4.6");
      expect(ev.reason).toBe("rate_limited");
    }
  });

  it("defaults split fields to empty strings when the wire omits them (legacy emitter still works)", () => {
    // Forward-compat: an older Go emitter (or a future scenario where
    // the joined fields drop and only the split fields ship, or the
    // reverse) MUST keep the union shape intact. Empty defaults let
    // the chatStore fall back to splitting "+" on the joined fields
    // when the split fields are absent.
    const payload = JSON.stringify({
      type: "provider_changed",
      from: "anthropic+claude-sonnet-4-6",
      to: "zai+glm-4.6",
      reason: "rate_limited",
    });
    const ev = parseSSEPayload(payload);
    expect(ev.kind).toBe("provider_changed");
    if (ev.kind === "provider_changed") {
      expect(ev.from).toBe("anthropic+claude-sonnet-4-6");
      expect(ev.to).toBe("zai+glm-4.6");
      expect(ev.fromProvider).toBe("");
      expect(ev.fromModel).toBe("");
      expect(ev.toProvider).toBe("");
      expect(ev.toModel).toBe("");
    }
  });

  it("treats a provider_changed event with missing fields as malformed (defaults to empty strings)", () => {
    // Defensive: a future emitter that ships only `type` without the
    // metadata must NOT crash the union — the parser fills the missing
    // fields with empty strings so the consuming switch sees a
    // well-formed variant. The store's render logic treats empty
    // from/to as "unknown model" copy ("Switched to a different model");
    // this is checked in the store spec.
    const ev = parseSSEPayload('{"type":"provider_changed"}');
    expect(ev.kind).toBe("provider_changed");
    if (ev.kind === "provider_changed") {
      expect(ev.from).toBe("");
      expect(ev.to).toBe("");
      expect(ev.reason).toBe("");
    }
  });

  it("classifies a model_active event by the type discriminant and unpacks provider/model", () => {
    // May 2026 chip-shows-selection-not-actual fix. The Go SSE pipeline
    // prepends {"type":"model_active","provider":"<id>","model":"<id>"} at
    // the start of EVERY successful stream so the chat UI's toolbar chip
    // can pivot from the user's selection to the actual model the moment
    // streaming starts. The fields are split rather than concatenated
    // (unlike provider_changed's "<provider>+<model>") because the chip
    // reads provider and model as separate keys against availableModels.
    const payload = JSON.stringify({
      type: "model_active",
      provider: "zai",
      model: "glm-4.6",
    });
    const ev = parseSSEPayload(payload);
    expect(ev.kind).toBe("model_active");
    if (ev.kind === "model_active") {
      expect(ev.provider).toBe("zai");
      expect(ev.model).toBe("glm-4.6");
    }
  });

  it("treats a model_active event with missing fields as well-formed (defaults to empty strings)", () => {
    // Defensive: a malformed wire payload must not crash the union. The
    // store's handler treats empty fields as "no information" and leaves
    // the prior chip values untouched (better than blanking it out
    // mid-conversation when the failover hook ships only the type).
    const ev = parseSSEPayload('{"type":"model_active"}');
    expect(ev.kind).toBe("model_active");
    if (ev.kind === "model_active") {
      expect(ev.provider).toBe("");
      expect(ev.model).toBe("");
    }
  });

  it("classifies a context_usage event by the type discriminant and unpacks the figures", () => {
    // Phase 2 of the May 2026 context-window saturation fix. The engine
    // emits {"type":"context_usage", input_tokens, output_reserve, limit,
    // percentage, provider, model} as the first artefact of every Stream.
    // The chat UI's chip renders the input + percentage figure alongside
    // the model picker so the user sees how close the request is to
    // saturating the model's window — including on the gate-refused
    // overflow path where the chunk arrives before the refusal error.
    const payload = JSON.stringify({
      type: "context_usage",
      input_tokens: 12345,
      output_reserve: 4096,
      limit: 100000,
      percentage: 12,
      provider: "zai",
      model: "glm-4.6",
    });
    const ev = parseSSEPayload(payload);
    expect(ev.kind).toBe("context_usage");
    if (ev.kind === "context_usage") {
      expect(ev.inputTokens).toBe(12345);
      expect(ev.outputReserve).toBe(4096);
      expect(ev.limit).toBe(100000);
      expect(ev.percentage).toBe(12);
      expect(ev.provider).toBe("zai");
      expect(ev.model).toBe("glm-4.6");
    }
  });

  it("treats a context_usage event with missing numeric fields as well-formed (defaults to zero)", () => {
    // Defensive: a degraded wire payload (a future emitter that ships
    // only the type) must not crash the union. The store's handler
    // treats zero-figures as "no information" and leaves the chip on
    // its prior value rather than blanking it mid-conversation.
    const ev = parseSSEPayload('{"type":"context_usage"}');
    expect(ev.kind).toBe("context_usage");
    if (ev.kind === "context_usage") {
      expect(ev.inputTokens).toBe(0);
      expect(ev.outputReserve).toBe(0);
      expect(ev.limit).toBe(0);
      expect(ev.percentage).toBe(0);
      expect(ev.provider).toBe("");
      expect(ev.model).toBe("");
    }
  });

  it("preserves the percentage figure verbatim (no clamp / no rounding on the parser side)", () => {
    // The engine caps percentage at 999 before sending — the parser
    // forwards the figure as-is so the store/component can decide how
    // to render it. This pin guards against a future parser-side clamp
    // that would mask an engine-side bug producing out-of-range figures.
    const payload = JSON.stringify({
      type: "context_usage",
      input_tokens: 50,
      output_reserve: 4096,
      limit: 100,
      percentage: 50,
      provider: "zai",
      model: "glm-4.6",
    });
    const ev = parseSSEPayload(payload);
    expect(ev.kind).toBe("context_usage");
    if (ev.kind === "context_usage") {
      expect(ev.percentage).toBe(50);
    }
  });

  it("classifies a thinking event by the type discriminant and unpacks content", () => {
    // Drop #2 — thinking SSE event. The Go side emits
    // {"type":"thinking","content":"<reasoning text>"} when the provider
    // streams reasoning tokens (Anthropic thinking_delta, glm-4.6 reasoning_content).
    // Pre-this-PR these chunks were dropped at the openaicompat adapter and
    // never serialised onto the wire, leaving the chat UI silent for tens of
    // seconds while the model reasoned. The discriminant value is "thinking"
    // (not "reasoning", not "thought") to avoid collision with future event
    // types planned by Track B such as "provider_changed".
    const ev = parseSSEPayload(
      '{"type":"thinking","content":"let me reason step by step..."}',
    );
    expect(ev.kind).toBe("thinking");
    if (ev.kind === "thinking") {
      expect(ev.content).toBe("let me reason step by step...");
    }
  });

  it("classifies a context_compacted SSE event into a typed payload (Slice 6b)", () => {
    // Slice 6b — surface auto-compaction events on the chip. The Go SSE
    // pipeline's writeSSEContextCompacted emits
    // {"type":"context_compacted", session_id, agent_id, original_tokens,
    //  summary_tokens, latency_ms, trigger} when the engine's L2
    // auto-compactor publishes EventContextCompacted. The frontend
    // parser routes this into a typed variant the chat store and chip
    // can consume.
    //
    // Phase-5 Slice δ added the trigger discriminant so the chip
    // tooltip can attribute the cause (ratio | gate_proximity |
    // model_switch | tool_result_wave).
    const payload = JSON.stringify({
      type: "context_compacted",
      session_id: "s-active",
      agent_id: "tech-lead",
      original_tokens: 50000,
      summary_tokens: 5000,
      latency_ms: 1234,
      trigger: "model_switch",
    });
    const ev = parseSSEPayload(payload);
    expect(ev.kind).toBe("context_compacted");
    if (ev.kind === "context_compacted") {
      expect(ev.sessionId).toBe("s-active");
      expect(ev.agentId).toBe("tech-lead");
      expect(ev.originalTokens).toBe(50000);
      expect(ev.summaryTokens).toBe(5000);
      expect(ev.latencyMs).toBe(1234);
      expect(ev.trigger).toBe("model_switch");
    }
  });

  it("classifies a gate_failed SSE event into a typed payload (Gate Bus Bridge)", () => {
    // Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026):
    // surface halt-class swarm-gate failures on the Vue chat surface.
    // The Go SSE pipeline's writeSSEGateFailed emits
    // {"type":"gate_failed", swarm_id, lifecycle, member_id, gate_name,
    //  gate_kind, reason, cause, coord_store_keys} when the engine's
    // runSwarmGates / dispatchMemberGates halts. The frontend parser
    // routes this into a typed variant the chat store and banner can
    // consume.
    const payload = JSON.stringify({
      type: "gate_failed",
      swarm_id: "a-team",
      lifecycle: "post-member",
      member_id: "researcher",
      gate_name: "post-member-researcher-relevance-gate",
      gate_kind: "ext:relevance-gate",
      reason: "off-topic",
      cause: "score 0.31 < threshold 0.5",
      coord_store_keys: ["chain/researcher/output", "chain/topic/spec"],
    });
    const ev = parseSSEPayload(payload);
    expect(ev.kind).toBe("gate_failed");
    if (ev.kind === "gate_failed") {
      expect(ev.swarmId).toBe("a-team");
      expect(ev.lifecycle).toBe("post-member");
      expect(ev.memberId).toBe("researcher");
      expect(ev.gateName).toBe("post-member-researcher-relevance-gate");
      expect(ev.gateKind).toBe("ext:relevance-gate");
      expect(ev.reason).toBe("off-topic");
      expect(ev.cause).toBe("score 0.31 < threshold 0.5");
      expect(ev.coordStoreKeys).toEqual([
        "chain/researcher/output",
        "chain/topic/spec",
      ]);
    }
  });

  it("treats a gate_failed event with missing fields as well-formed (defaults to empty strings / empty array)", () => {
    // Defensive: a degraded wire payload (missing optional fields)
    // must not crash the discriminated-union dispatch. String fields
    // default to ''; coord_store_keys defaults to [].
    const ev = parseSSEPayload('{"type":"gate_failed"}');
    expect(ev.kind).toBe("gate_failed");
    if (ev.kind === "gate_failed") {
      expect(ev.swarmId).toBe("");
      expect(ev.lifecycle).toBe("");
      expect(ev.memberId).toBe("");
      expect(ev.gateName).toBe("");
      expect(ev.gateKind).toBe("");
      expect(ev.reason).toBe("");
      expect(ev.cause).toBe("");
      expect(ev.coordStoreKeys).toEqual([]);
    }
  });

  it("treats a context_compacted event with missing fields as well-formed (defaults to zero / empty string)", () => {
    // Defensive: a degraded wire payload (a future emitter that ships
    // only the type) must not crash the discriminated-union dispatch.
    // Numeric fields default to 0; string fields default to ''. The
    // chat store's handler treats zero / empty as "no information" and
    // either ignores the event or skips the flash — see chatStore spec.
    //
    // Phase-5 Slice δ: an empty trigger is tolerated so historical
    // events that pre-date the field remain decodable; the chip tooltip
    // falls back to the generic copy when trigger is empty.
    const ev = parseSSEPayload('{"type":"context_compacted"}');
    expect(ev.kind).toBe("context_compacted");
    if (ev.kind === "context_compacted") {
      expect(ev.sessionId).toBe("");
      expect(ev.agentId).toBe("");
      expect(ev.originalTokens).toBe(0);
      expect(ev.summaryTokens).toBe(0);
      expect(ev.latencyMs).toBe(0);
      expect(ev.trigger).toBe("");
    }
  });

  it("returns malformed for non-JSON payloads", () => {
    expect(parseSSEPayload("not json {")).toEqual({
      kind: "malformed",
      raw: "not json {",
    });
  });

  it("returns unknown for JSON without a recognised type or structural shape", () => {
    expect(parseSSEPayload('{"foo":"bar"}')).toEqual({
      kind: "unknown",
      raw: '{"foo":"bar"}',
    });
  });

  it("returns unknown for a JSON array (no object discriminant)", () => {
    // Top-level arrays are technically valid JSON but have no `type` field
    // and no structural shape we recognise — must classify as unknown rather
    // than crashing on the property access.
    expect(parseSSEPayload("[1,2,3]")).toEqual({
      kind: "unknown",
      raw: "[1,2,3]",
    });
  });

  // UI Parity PR5 — Live token counter (May 2026).
  //
  // The engine projects the in-flight turn's cumulative output_tokens
  // onto every streaming.heartbeat tick under the wire key
  // `token_count` (int64). The parser MUST surface it on the
  // discriminated SSEStreamingHeartbeatEvent so the chat store can
  // compute tokens-per-second from the delta-vs-prev-tick at the
  // documented 15s cadence and the streaming chrome can render
  // "1,247 tokens · 42 t/s" next to the working-on label.
  describe("streaming_heartbeat — UI Parity PR5 token counter", () => {
    it("extracts token_count onto the parsed event", () => {
      const ev = parseSSEPayload(
        JSON.stringify({
          type: "streaming.heartbeat",
          phase: "generating",
          token_count: 1247,
        }),
      );
      expect(ev.kind).toBe("streaming_heartbeat");
      if (ev.kind === "streaming_heartbeat") {
        expect(ev.phase).toBe("generating");
        expect(ev.tokenCount).toBe(1247);
      }
    });

    it("defaults tokenCount to 0 when the wire payload omits the field (pre-PR5 server compat)", () => {
      // Forward compatibility: a heartbeat from a pre-PR5 server has
      // {type, session_id, agent_id, phase} but no token_count. The
      // parser must default to 0 so the chat store's counter renderer
      // suppresses the chip (zero = "no information yet") rather than
      // showing NaN or crashing.
      const ev = parseSSEPayload(
        JSON.stringify({
          type: "streaming.heartbeat",
          phase: "thinking",
        }),
      );
      expect(ev.kind).toBe("streaming_heartbeat");
      if (ev.kind === "streaming_heartbeat") {
        expect(ev.tokenCount).toBe(0);
      }
    });

    it("extracts token_count from the underscore-only wire variant too", () => {
      // The dotted `streaming.heartbeat` is canonical; the underscore
      // `streaming_heartbeat` is a tolerated alternative some SSE
      // bridges normalise to (per the existing parser branch).
      // tokenCount extraction must work on both variants.
      const ev = parseSSEPayload(
        JSON.stringify({
          type: "streaming_heartbeat",
          phase: "tool_executing",
          token_count: 3500,
        }),
      );
      expect(ev.kind).toBe("streaming_heartbeat");
      if (ev.kind === "streaming_heartbeat") {
        expect(ev.tokenCount).toBe(3500);
      }
    });
  });
});
