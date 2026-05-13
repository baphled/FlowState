package api

import (
	"encoding/json"
	"net/http"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
)

// sseChunk represents a single content chunk in a server-sent event stream.
type sseChunk struct {
	Content string `json:"content"`
}

// sseError represents an error message in a server-sent event stream.
type sseError struct {
	Error string `json:"error"`
}

// sseToolCall represents a tool call event in a server-sent event stream.
type sseToolCall struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Input  string `json:"input,omitempty"`
}

// sseSkillLoad represents a skill load event in a server-sent event stream.
type sseSkillLoad struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// sseToolResult represents a tool result event in a server-sent event stream.
type sseToolResult struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// sseToolError represents a tool execution failure event in a server-sent
// event stream. Distinct from sseToolResult so the frontend's
// handleToolErrorEvent can flip the matching tool message to status='error'
// in-stream — the legacy `tool_result` event hard-sets status='completed',
// which hid live tool failures until the post-stream history reconcile.
//
// Field semantics:
//   - Content carries the error message (typically the executor's err.Error()
//     prefixed with "Error: ", matching the engine's chunk content for
//     IsError chunks). The frontend renders this verbatim in the tool body.
//
// Additive contract: emitting `tool_error` does NOT replace `tool_result` —
// the legacy event keeps firing for IsError=false chunks. The new event is
// only emitted when the engine stamps ToolResult.IsError=true on the chunk.
type sseToolError struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// sseThinking represents a model reasoning ("thinking") event in a server-sent
// event stream. The discriminant value "thinking" is namespaced specifically to
// avoid collision with future provider-related event types planned by Track B
// (e.g. "provider_changed" for failover transitions).
type sseThinking struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// sseProviderChanged represents a failover transition event in a server-sent
// event stream. The chat UI renders this as a toast notification ("Switched
// to glm-4.6 — primary model rate-limited") plus updates the persistent
// model/provider chip in the input toolbar.
//
// Field semantics:
//   - From / To are "<provider>+<model>" joined strings (the legacy
//     shape, e.g. "anthropic+claude-sonnet-4-6"). They remain on the
//     wire for consumers still parsing the joined form.
//   - FromProvider / FromModel / ToProvider / ToModel are the new
//     split shape mirroring sseModelActive's (provider, model) pair.
//     The chip prefers these to skip the "+" parse hop and the
//     off-by-one bugs around model ids that themselves contain "+"
//     (rare; openrouter). Backend ships both shapes simultaneously so
//     consumers can migrate gracefully.
//   - Reason is a stable machine-readable token from a closed set; see
//     classifyFailoverReason in internal/plugin/failover/stream_hook.go for
//     the full vocabulary. The frontend maps it to plain English.
type sseProviderChanged struct {
	Type         string `json:"type"`
	From         string `json:"from"`
	To           string `json:"to"`
	FromProvider string `json:"from_provider"`
	FromModel    string `json:"from_model"`
	ToProvider   string `json:"to_provider"`
	ToModel      string `json:"to_model"`
	Reason       string `json:"reason"`
}

// sseModelActive represents the always-on "actual model is now streaming"
// signal emitted at the start of every successful stream. The chat UI uses
// this to pivot the persistent toolbar chip from the user's selection to
// the actual model the moment streaming starts.
//
// Why a separate event from provider_changed: provider_changed only fires
// on failover transitions (when a previous candidate failed). model_active
// fires unconditionally so the chip can correct itself even on the common
// case where the actual matches the selection — and on the divergent case
// where the actual differs without a failover (agent override, manifest
// override), the chip still pivots to the truth.
//
// Field semantics:
//   - Provider is the canonical provider id (e.g. "anthropic", "zai").
//   - Model is the canonical model id (e.g. "claude-sonnet-4-6", "glm-4.6").
//
// The fields are split rather than concatenated (unlike provider_changed's
// "<provider>+<model>" pair) because the chip rendering reads them as
// separate keys against the availableModels list — splitting on "+"
// would re-introduce a parse step and a class of off-by-one bugs around
// model ids that themselves contain "+" (rare; openrouter).
type sseModelActive struct {
	Type     string `json:"type"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// sseContextUsage represents the always-on context-window usage event
// emitted at the start of every Stream when the engine has enough
// information to compute it (token counter wired AND limit > 0). The
// chat UI renders this as a usage chip alongside the model picker.
//
// Field semantics:
//   - InputTokens — engine-side estimate of the prompt cost.
//   - OutputReserve — the reserve subtracted from limit before the
//     overflow gate compares input against usable.
//   - Limit — resolved per-(provider, model) context window in tokens.
//   - Percentage — round(input / limit * 100). Capped at 999 by the
//     engine so the chip's three-digit formatter is safe.
//   - Provider / Model — canonical ids the chip displays.
//
// Mirrors the wire-shape contract of sseModelActive: the engine
// marshals the payload (no type field — that's the SSE writer's
// contract) and writeSSEContextUsage injects the discriminant.
type sseContextUsage struct {
	Type          string `json:"type"`
	InputTokens   int    `json:"input_tokens"`
	OutputReserve int    `json:"output_reserve"`
	Limit         int    `json:"limit"`
	Percentage    int    `json:"percentage"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
}

// sseProviderQuota is the wire shape for the per-provider quota
// event registered in PR1 of the Provider Quota and Spend Visibility
// plan (May 2026). The discriminator pattern mirrors sseContextUsage:
// the engine marshals the variant-specific JSON payload (no type
// field — that's this writer's contract) and writeSSEProviderQuota
// injects "type":"provider_quota" at the wire layer.
//
// PR1 registers the wire type and the server fan-out case; the
// engine does NOT yet emit chunks for it. PR4 lights up the emission
// path. Wired-but-dormant is the load-bearing intent per plan PR1
// row 425 — the chip's Pinia subscription contract is frozen so the
// Vue side can land in PR4a without waiting on a fresh server PR.
//
// Field semantics (mirrors the discriminated-union shape exposed by
// the engine via quota.Snapshot):
//   - Provider / AccountHash / Model — partition keys.
//   - ObservedAt — wall-clock of the snapshot refresh (RFC 3339).
//   - Stale — true past the rate-limit window's reset wall-clock.
//   - StoreBackend — "memory" | "redis" | "postgres" for the chip's
//     single-instance-scope tooltip per plan B3 fold lines 285-293.
//   - Variant — "rate_limit" | "token_spend" | "not_configured" —
//     the TypeScript-side discriminant the Vue chip switches on.
//   - RateLimit / TokenSpend / NotConfigured — variant-specific
//     payloads. Exactly one is non-empty for every event.
type sseProviderQuota struct {
	Type          string                       `json:"type"`
	Provider      string                       `json:"provider"`
	AccountHash   string                       `json:"account_hash"`
	Model         string                       `json:"model,omitempty"`
	ObservedAt    string                       `json:"observed_at"`
	Stale         bool                         `json:"stale,omitempty"`
	StoreBackend  string                       `json:"store_backend,omitempty"`
	PricingSource string                       `json:"pricing_source,omitempty"`
	Variant       string                       `json:"variant"`
	RateLimit     *sseProviderQuotaRateLimit   `json:"rate_limit,omitempty"`
	TokenSpend    *sseProviderQuotaTokenSpend  `json:"token_spend,omitempty"`
	NotConfigured *sseProviderQuotaNotConfig   `json:"not_configured,omitempty"`
}

// sseProviderQuotaRateLimit is the wire shape for the RateLimit
// variant. Four windows + a tightest-window summary.
type sseProviderQuotaRateLimit struct {
	Requests                 sseQuotaWindow `json:"requests"`
	Tokens                   sseQuotaWindow `json:"tokens"`
	Input                    sseQuotaWindow `json:"input"`
	Output                   sseQuotaWindow `json:"output"`
	TightestPercentRemaining int            `json:"tightest_percent_remaining"`
	TightestResetAt          string         `json:"tightest_reset_at,omitempty"`
}

// sseQuotaWindow is one rate-limit window triple. -1 sentinel for
// "not provided" mirrors the Go side.
type sseQuotaWindow struct {
	Limit     int    `json:"limit"`
	Remaining int    `json:"remaining"`
	Reset     string `json:"reset,omitempty"`
}

// sseProviderQuotaTokenSpend is the wire shape for the TokenSpend
// variant. PR1 ships the type; PR4 lights up the emission path.
type sseProviderQuotaTokenSpend struct {
	SpentMinor     int64  `json:"spent_minor"`
	SpentCurrency  string `json:"spent_currency"`
	SpentUSDMinor  int64  `json:"spent_usd_minor"`
	CapMinor       int64  `json:"cap_minor,omitempty"`
	CapCurrency    string `json:"cap_currency,omitempty"`
	Period         string `json:"period"`
	PeriodStart    string `json:"period_start"`
	PeriodEnd      string `json:"period_end"`
	ThresholdAmber int    `json:"threshold_amber"`
	ThresholdRed   int    `json:"threshold_red"`
}

// sseProviderQuotaNotConfig is the wire shape for the NotConfigured
// variant. Reason is operator-visible verbatim via the chip tooltip.
type sseProviderQuotaNotConfig struct {
	Reason string `json:"reason"`
}

// sseContextCompacted is the wire shape for the SSE event Slice 6a
// emits when the engine's L2 auto-compactor publishes
// EventContextCompacted on the bus. Mirrors the wire-shape contract
// of sseContextUsage: untyped JSON arriving from the bridge handler is
// re-marshalled here with the canonical `"type":"context_compacted"`
// discriminant injected so the frontend's discriminated union routes
// correctly.
//
// Field semantics:
//   - SessionID — the session the compaction fired for; lets the
//     frontend ignore events that don't match the active session.
//   - AgentID — the manifest id that owned the compaction; the chip
//     uses this for the "compacted by <agent>" attribution.
//   - OriginalTokens — pre-compaction token count of the cold prefix.
//   - SummaryTokens — post-compaction token count of the summary.
//   - LatencyMS — wall-clock latency of the summariser call.
type sseContextCompacted struct {
	Type           string `json:"type"`
	SessionID      string `json:"session_id"`
	AgentID        string `json:"agent_id"`
	OriginalTokens int    `json:"original_tokens"`
	SummaryTokens  int    `json:"summary_tokens"`
	LatencyMS      int64  `json:"latency_ms"`
	// Trigger identifies the path that fired compaction. Closed
	// vocabulary: ratio | gate_proximity | model_switch |
	// tool_result_wave. Empty is tolerated so historical events that
	// pre-date the field remain decodable; the chip tooltip falls back
	// to the generic copy.
	Trigger string `json:"trigger,omitempty"`
}

// sseGateFailed is the wire shape for the SSE event Plans/Gate Bus
// Bridge — Engine to SSE and TUI (May 2026) emits when the engine's
// runSwarmGates / dispatchMemberGates halts and publishes
// EventGateFailed on the bus. Mirrors the wire-shape contract of
// sseContextCompacted: untyped JSON arriving from the bridge handler
// is re-marshalled here with the canonical `"type":"gate_failed"`
// discriminant injected so the frontend's discriminated union routes
// correctly.
//
// Field semantics (mirrors events.GateEventData with snake_case keys
// the Vue parser expects):
//   - SwarmID — the swarm that halted; the banner subtitle attributes
//     the failure to a swarm name.
//   - Lifecycle — "pre" | "post" | "pre-member" | "post-member"; the
//     banner subtitle distinguishes a swarm-boundary halt from a
//     per-member halt.
//   - MemberID — the failing member when Lifecycle is member-scoped;
//     empty for swarm-level halts.
//   - GateName — the manifest-supplied gate name; the banner title
//     uses this verbatim ("Swarm gate halted: <gate_name>").
//   - GateKind — "ext:<name>" or "builtin:<name>" so the banner can
//     surface the gate family on a power-user toggle.
//   - Reason — the typed *swarm.GateError.Reason; the banner body.
//   - Cause — the wrapped runner error's message, or empty when the
//     halt is clean (a gate that returned without an upstream error).
//   - CoordStoreKeys — the keys the gate inspected, when the gate
//     declares Inputs per Multi-Key Gate Inputs (May 2026); the
//     banner exposes this on a "what was checked?" expander.
type sseGateFailed struct {
	Type           string   `json:"type"`
	SwarmID        string   `json:"swarm_id"`
	Lifecycle      string   `json:"lifecycle"`
	MemberID       string   `json:"member_id"`
	GateName       string   `json:"gate_name"`
	GateKind       string   `json:"gate_kind"`
	Reason         string   `json:"reason"`
	Cause          string   `json:"cause"`
	CoordStoreKeys []string `json:"coord_store_keys,omitempty"`
}

// sseStreamingHeartbeat is the wire shape for the SSE event emitted
// when the engine publishes EventStreamingHeartbeat on the bus
// (Streaming Coherence Slice F follow-up, Bug Fix #62, May 2026).
// Mirrors the wire-shape contract of sseContextCompacted: untyped JSON
// arriving from the bridge handler is re-marshalled here with the
// canonical `"type":"streaming.heartbeat"` discriminant injected so
// the frontend's discriminated union (web/src/lib/sseEvent.ts) routes
// correctly.
//
// Field semantics:
//   - SessionID — the session the heartbeat is bound to; the frontend
//     ignores events that don't match the active session.
//   - AgentID — the manifest id that owned the active turn; included
//     for symmetry with the bus payload and future per-agent telemetry.
//   - Phase — one of "generating" | "thinking" | "tool_executing" |
//     "queued" (closed vocabulary per StreamingHeartbeatEventData);
//     the adaptive stall watchdog uses this to pick the next timeout
//     window. Empty is tolerated so historical wire payloads remain
//     decodable; the frontend falls back to the legacy 60s threshold.
//   - TokenCount — the in-flight turn's cumulative output_tokens per
//     the most recent provider UsageDelta (Anthropic message_delta,
//     openaicompat trailing-chunk usage). UI Parity PR5 (May 2026):
//     the Vue chat UI renders "1,247 tokens · 42 t/s" next to the
//     working-on label and computes tokens-per-second from the
//     delta-vs-prev-tick at the documented 15s heartbeat cadence.
//     NOT omitempty — explicit zero is the legitimate
//     pre-first-UsageDelta value the frontend uses to gate the
//     counter render (zero = "no information yet, hide chip").
type sseStreamingHeartbeat struct {
	Type       string `json:"type"`
	SessionID  string `json:"session_id"`
	AgentID    string `json:"agent_id"`
	Phase      string `json:"phase"`
	TokenCount int64  `json:"token_count"`
}

// sseHarnessRetry represents a harness retry event in a server-sent event stream.
type sseHarnessRetry struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// sseAttemptStart represents a harness attempt start event in a server-sent event stream.
type sseAttemptStart struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// sseHarnessComplete represents a harness completion event in a server-sent event stream.
type sseHarnessComplete struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// sseCriticFeedback represents a harness critic feedback event in a server-sent event stream.
type sseCriticFeedback struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// writeSSEContent marshals content as a JSON chunk and writes it as a server-sent event.
//
// Expected:
//   - content is the text to send in the SSE chunk.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded chunk to response.
//   - Flushes response buffer.
func writeSSEContent(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseChunk{Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEError marshals an error message as a JSON error and writes it as a server-sent event.
//
// Expected:
//   - errMsg is the error message text to send.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded error to response.
//   - Flushes response buffer.
func writeSSEError(w http.ResponseWriter, flusher http.Flusher, errMsg string) {
	data := sseError{Error: errMsg}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEDone writes the completion marker as a server-sent event.
//
// Expected:
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with "[DONE]" marker to response.
//   - Flushes response buffer.
func writeSSEDone(w http.ResponseWriter, flusher http.Flusher) {
	writeSSE(w, flusher, "[DONE]")
}

// writeSSEToolCall marshals a tool call as a JSON event and writes it as a server-sent event.
//
// Expected:
//   - name is the tool name being invoked.
//   - input is the raw JSON-encoded arguments string (may be empty).
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded tool call to response.
//   - Flushes response buffer.
func writeSSEToolCall(w http.ResponseWriter, flusher http.Flusher, name, input string) {
	data := sseToolCall{Type: "tool_call", Name: name, Status: "running", Input: input}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSESkillLoad marshals a skill load as a JSON event and writes it as a server-sent event.
//
// Expected:
//   - name is the skill name being loaded.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded skill load to response.
//   - Flushes response buffer.
func writeSSESkillLoad(w http.ResponseWriter, flusher http.Flusher, name string) {
	data := sseSkillLoad{Type: "skill_load", Name: name}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEToolError marshals a tool execution failure as a JSON event and
// writes it as a server-sent event. Routed from handleSessionStream when the
// engine stamps ToolResult.IsError=true on a tool_result chunk (Gap 2 — May
// 2026). Distinct event type so the Vue chatStore's handleToolErrorEvent can
// flip the matching tool message's status to 'error' in-stream rather than
// waiting for the post-stream history reconcile.
//
// Expected:
//   - content is the error message (the engine prefixes the executor's error
//     with "Error: " on the chunk).
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded tool error to response.
//   - Flushes response buffer.
func writeSSEToolError(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseToolError{Type: "tool_error", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEToolResult marshals a tool result as a JSON event and writes it as a server-sent event.
//
// Expected:
//   - content is the tool result content to send.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded tool result to response.
//   - Flushes response buffer.
func writeSSEToolResult(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseToolResult{Type: "tool_result", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEThinking marshals a model-reasoning chunk as a JSON event and writes
// it as a server-sent event.
//
// Expected:
//   - content is the thinking text emitted by the provider's reasoning channel
//     (Anthropic thinking_delta blocks, OpenAI-compat reasoning_content deltas).
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded thinking event to response.
//   - Flushes response buffer.
func writeSSEThinking(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseThinking{Type: "thinking", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEProviderChanged emits a typed failover-transition SSE event by
// re-parsing the payload JSON marshalled by the failover hook and re-emitting
// it with the canonical "type":"provider_changed" discriminant injected.
//
// Why re-marshal instead of pass-through: the failover hook marshals
// {from, to, reason} (no type field — that's the SSE writer's contract).
// Injecting the type field here keeps the emitter side unaware of the
// frontend dispatch convention, mirroring how writeSSEDelegationInfo
// injects "type":"delegation" on the wire.
//
// Expected:
//   - payload is the JSON encoded by failover.providerChangedPayload (from/to/reason).
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded provider_changed event.
//   - Flushes response buffer.
//   - On a malformed payload, drops the event silently rather than emitting
//     a malformed SSE event the frontend's parser would classify as
//     "unknown" and discard. This keeps the wire clean for the user-visible
//     toast: a corrupt failover signal is worse than no signal.
func writeSSEProviderChanged(w http.ResponseWriter, flusher http.Flusher, payload string) {
	var parsed struct {
		From         string `json:"from"`
		To           string `json:"to"`
		FromProvider string `json:"from_provider"`
		FromModel    string `json:"from_model"`
		ToProvider   string `json:"to_provider"`
		ToModel      string `json:"to_model"`
		Reason       string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return
	}
	data := sseProviderChanged{
		Type:         "provider_changed",
		From:         parsed.From,
		To:           parsed.To,
		FromProvider: parsed.FromProvider,
		FromModel:    parsed.FromModel,
		ToProvider:   parsed.ToProvider,
		ToModel:      parsed.ToModel,
		Reason:       parsed.Reason,
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEModelActive emits a typed "actual model is now streaming" SSE
// event by re-parsing the payload JSON marshalled by the failover hook
// and re-emitting it with the canonical "type":"model_active" discriminant
// injected.
//
// Same pattern as writeSSEProviderChanged: the failover hook marshals
// {provider, model} (no type field — that's the SSE writer's contract).
// Injecting the type field here keeps the emitter side unaware of the
// frontend dispatch convention.
//
// Expected:
//   - payload is the JSON encoded by failover.modelActivePayload (provider/model).
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded model_active event.
//   - Flushes response buffer.
//   - On a malformed payload, drops the event silently rather than
//     emitting a malformed SSE event the frontend's parser would
//     classify as "unknown" and discard. The chip stays on the
//     optimistic selection rather than blanking out mid-conversation.
func writeSSEModelActive(w http.ResponseWriter, flusher http.Flusher, payload string) {
	var parsed struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return
	}
	data := sseModelActive{
		Type:     "model_active",
		Provider: parsed.Provider,
		Model:    parsed.Model,
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEContextUsage emits a typed context_usage SSE event by
// re-parsing the payload JSON marshalled by the engine and re-emitting
// it with the canonical "type":"context_usage" discriminant injected.
//
// Same pattern as writeSSEModelActive: the engine marshals the figures
// (no type field — that's the SSE writer's contract). Injecting the
// type field here keeps the emitter side unaware of the frontend
// dispatch convention.
//
// Expected:
//   - payload is the JSON encoded by engine.contextUsagePayload.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded context_usage event.
//   - Flushes response buffer.
//   - On a malformed payload, drops the event silently rather than
//     emitting a malformed SSE event the frontend's parser would
//     classify as "unknown" and discard. The chip stays on the
//     prior value rather than blanking out mid-conversation.
func writeSSEContextUsage(w http.ResponseWriter, flusher http.Flusher, payload string) {
	var parsed struct {
		InputTokens   int    `json:"input_tokens"`
		OutputReserve int    `json:"output_reserve"`
		Limit         int    `json:"limit"`
		Percentage    int    `json:"percentage"`
		Provider      string `json:"provider"`
		Model         string `json:"model"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return
	}
	data := sseContextUsage{
		Type:          "context_usage",
		InputTokens:   parsed.InputTokens,
		OutputReserve: parsed.OutputReserve,
		Limit:         parsed.Limit,
		Percentage:    parsed.Percentage,
		Provider:      parsed.Provider,
		Model:         parsed.Model,
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEProviderQuota emits a typed provider_quota SSE event by
// re-parsing the payload JSON marshalled by the engine and
// re-emitting it with the canonical "type":"provider_quota"
// discriminant injected. Mirrors writeSSEContextUsage at line 621.
//
// PR1 of the Provider Quota and Spend Visibility plan registers this
// writer (and the matching server fan-out case at server.go) so the
// wire contract is frozen — the chip's Pinia subscription contract
// (web/src/stores/quotaStore.ts in PR4a) lands against a stable
// server surface. PR4 lights up the actual emission path on the
// engine side.
//
// Expected:
//   - payload is the JSON encoded by the engine's
//     buildProviderQuotaChunk (PR4). The shape mirrors
//     quota.Snapshot's discriminated union.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes one SSE data line carrying the JSON-encoded
//     provider_quota event.
//   - Flushes response buffer.
//   - On a malformed payload, drops the event silently rather than
//     emitting a malformed SSE event the frontend's parser would
//     classify as "unknown" and discard. The chip stays on the prior
//     value rather than blanking out mid-conversation.
//
// PR1 acceptance: this function is callable; the server fan-out has
// the matching case; engine does not yet emit. PR4 wires the
// emitter.
func writeSSEProviderQuota(w http.ResponseWriter, flusher http.Flusher, payload string) {
	var parsed sseProviderQuota
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return
	}
	// Stamp the discriminant — the engine omits it by contract.
	parsed.Type = "provider_quota"
	jsonData, err := json.Marshal(parsed)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEContextCompacted emits a typed context_compacted SSE event by
// marshalling the supplied bus payload with the canonical
// `"type":"context_compacted"` discriminant injected.
//
// Same pattern as writeSSEContextUsage: the bridge handler in
// event_bridge.go produces a sanitised payload from
// pluginevents.ContextCompactedEventData; this writer adds the type
// field. Slice 6a — bridge for the L2 auto-compactor's bus event onto
// the SSE wire so Slice 6b's chip flash can render.
//
// Expected:
//   - data is the sanitised payload from newContextCompactedHandler:
//     `{event_type, session_id, agent_id, original_tokens,
//     summary_tokens, latency_ms}`.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes one SSE data line carrying the JSON-encoded
//     context_compacted event.
//   - Flushes response buffer.
//   - On a payload that fails to marshal (defence in depth — the
//     bridge handler produces a struct of primitives), drops the
//     event silently rather than emitting a malformed event the
//     frontend's parser would classify as "unknown" and discard.
func writeSSEContextCompacted(w http.ResponseWriter, flusher http.Flusher, data map[string]any) {
	sessionID, _ := data["session_id"].(string)
	agentID, _ := data["agent_id"].(string)
	originalTokens, _ := data["original_tokens"].(int)
	summaryTokens, _ := data["summary_tokens"].(int)
	latencyMS, _ := data["latency_ms"].(int64)
	// Phase-5 Slice δ — the Trigger discriminant flows verbatim from
	// the bridge handler. Empty is tolerated so historical events that
	// pre-date the field remain decodable.
	trigger, _ := data["trigger"].(string)

	payload := sseContextCompacted{
		Type:           "context_compacted",
		SessionID:      sessionID,
		AgentID:        agentID,
		OriginalTokens: originalTokens,
		SummaryTokens:  summaryTokens,
		LatencyMS:      latencyMS,
		Trigger:        trigger,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEGateFailed emits a typed gate_failed SSE event by marshalling
// the supplied bus payload with the canonical `"type":"gate_failed"`
// discriminant injected.
//
// Same pattern as writeSSEContextCompacted: the bridge handler in
// event_bridge.go produces a sanitised payload from
// events.GateEventData; this writer adds the type field. Plans/Gate
// Bus Bridge — Engine to SSE and TUI (May 2026).
//
// Expected:
//   - data is the sanitised payload from newGateFailedHandler:
//     `{event_type, swarm_id, lifecycle, member_id, gate_name,
//     gate_kind, reason, cause, coord_store_keys}`.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes one SSE data line carrying the JSON-encoded gate_failed
//     event.
//   - Flushes response buffer.
//   - On a payload that fails to marshal (defence in depth — the
//     bridge handler produces a struct of primitives), drops the
//     event silently rather than emitting a malformed event the
//     frontend's parser would classify as "unknown" and discard.
func writeSSEGateFailed(w http.ResponseWriter, flusher http.Flusher, data map[string]any) {
	swarmID, _ := data["swarm_id"].(string)
	lifecycle, _ := data["lifecycle"].(string)
	memberID, _ := data["member_id"].(string)
	gateName, _ := data["gate_name"].(string)
	gateKind, _ := data["gate_kind"].(string)
	reason, _ := data["reason"].(string)
	cause, _ := data["cause"].(string)

	var coordKeys []string
	if raw, ok := data["coord_store_keys"]; ok {
		switch v := raw.(type) {
		case []string:
			coordKeys = v
		case []any:
			coordKeys = make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					coordKeys = append(coordKeys, s)
				}
			}
		}
	}

	payload := sseGateFailed{
		Type:           "gate_failed",
		SwarmID:        swarmID,
		Lifecycle:      lifecycle,
		MemberID:       memberID,
		GateName:       gateName,
		GateKind:       gateKind,
		Reason:         reason,
		Cause:          cause,
		CoordStoreKeys: coordKeys,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEStreamingHeartbeat emits a typed streaming.heartbeat SSE event
// by marshalling the supplied bus payload with the canonical
// `"type":"streaming.heartbeat"` discriminant injected.
//
// Same pattern as writeSSEContextCompacted: the bridge handler in
// event_bridge.go (newStreamingHeartbeatHandler) produces a sanitised
// payload from events.StreamingHeartbeatEventData; this writer adds the
// type field. Streaming Coherence Slice F follow-up (Bug Fix #62, May
// 2026): closes the gap where the engine published
// EventStreamingHeartbeat on the bus but no subscriber forwarded it
// onto the wire, leaving the frontend's adaptive stall watchdog
// without the live signal it documents.
//
// Expected:
//   - data is the sanitised payload from newStreamingHeartbeatHandler:
//     `{event_type, session_id, agent_id, phase}`.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes one SSE data line carrying the JSON-encoded
//     streaming.heartbeat event.
//   - Flushes response buffer.
//   - On a payload that fails to marshal (defence in depth — the
//     bridge handler produces a struct of primitives), drops the
//     event silently rather than emitting a malformed event the
//     frontend's parser would classify as "unknown" and discard.
func writeSSEStreamingHeartbeat(w http.ResponseWriter, flusher http.Flusher, data map[string]any) {
	sessionID, _ := data["session_id"].(string)
	agentID, _ := data["agent_id"].(string)
	phase, _ := data["phase"].(string)
	// UI Parity PR5 (May 2026) — token_count is int64 on the bus
	// payload (events.StreamingHeartbeatEventData.TokenCount); the
	// bridge handler threads it onto the sanitised map under the
	// snake_case wire key. Defensive type-assert tolerates a missing
	// or wrong-typed field by defaulting to zero — the frontend's
	// counter renderer gates on >0 so a zero from a degraded
	// emitter renders as "no information yet, hide chip".
	tokenCount, _ := data["token_count"].(int64)

	payload := sseStreamingHeartbeat{
		Type:       "streaming.heartbeat",
		SessionID:  sessionID,
		AgentID:    agentID,
		Phase:      phase,
		TokenCount: tokenCount,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEHarnessRetry marshals a harness retry as a JSON event and writes it as a server-sent event.
//
// Expected:
//   - content describes the validation failure and retry reason.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded harness retry event to response.
//   - Flushes response buffer.
func writeSSEHarnessRetry(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseHarnessRetry{Type: "harness_retry", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEAttemptStart marshals a harness attempt start as a JSON event and writes it as a server-sent event.
//
// Expected:
//   - content describes the attempt being started.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded attempt start event to response.
//   - Flushes response buffer.
func writeSSEAttemptStart(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseAttemptStart{Type: "harness_attempt_start", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEHarnessComplete marshals a harness completion as a JSON event and writes it as a server-sent event.
//
// Expected:
//   - content describes the evaluation outcome.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded harness complete event to response.
//   - Flushes response buffer.
func writeSSEHarnessComplete(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseHarnessComplete{Type: "harness_complete", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSECriticFeedback marshals harness critic feedback as a JSON event and writes it as a server-sent event.
//
// Expected:
//   - content describes the critic's feedback on the plan.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded critic feedback event to response.
//   - Flushes response buffer.
func writeSSECriticFeedback(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseCriticFeedback{Type: "harness_critic_feedback", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEDelegation marshals a delegation event as JSON and writes it as a server-sent event.
//
// Expected:
//   - event contains delegation metadata.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes SSE data line with JSON-encoded delegation event to response.
//   - Flushes response buffer.
func writeSSEDelegation(w http.ResponseWriter, flusher http.Flusher, event streaming.DelegationEvent) {
	jsonData, err := json.Marshal(event)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSEDelegationInfo emits a plain SSE data line carrying the JSON-encoded
// provider DelegationInfo payload. A "type":"delegation" field is injected so
// the frontend message listener can route by type rather than by SSE event name.
// Named SSE events (event: delegation) are only delivered to addEventListener
// listeners registered for that specific name — not to the generic "message"
// listener the frontend uses — so we must emit a plain data: line instead.
func writeSSEDelegationInfo(w http.ResponseWriter, flusher http.Flusher, info *provider.DelegationInfo) {
	if info == nil {
		return
	}
	type payload struct {
		Type string `json:"type"`
		*provider.DelegationInfo
	}
	data := payload{Type: "delegation", DelegationInfo: info}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}

// writeSSE writes a server-sent event data line and flushes the response buffer.
//
// Expected:
//   - data is the content to send in the SSE data line.
//   - flusher supports HTTP flushing.
//
// Side effects:
//   - Writes "data: " prefix, data, and two newlines to response.
//   - Flushes response buffer to send data immediately.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, data string) {
	if _, err := w.Write([]byte("data: " + data + "\n\n")); err != nil {
		return
	}
	flusher.Flush()
}
