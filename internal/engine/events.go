// Package engine — event publishing.
//
// This file centralises the engine's eventbus publishers: session
// lifecycle, tool before/after/validation-failure, provider request,
// response and error events, plus the small context helpers they share.
package engine

import (
	"context"
	"errors"
	"log/slog"

	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

// publishSessionEvent publishes a session lifecycle event to the engine bus.
//
// Expected:
//   - action is the session lifecycle transition name.
//
// Returns:
//   - None.
//
// Side effects:
//   - Publishes an event on the engine bus when one is configured.
func (e *Engine) publishSessionEvent(sessionID string, action string) {
	var topic string
	switch action {
	case "created":
		topic = events.EventSessionCreated
	case "ended":
		topic = events.EventSessionEnded
	default:
		topic = "session." + action
	}
	e.bus.Publish(topic, events.NewSessionEvent(events.SessionEventData{
		SessionID: sessionID,
		Action:    action,
	}))
}

// publishToolBeforeEvent publishes a tool execution start event to the engine bus.
//
// Expected:
//   - toolName identifies the tool being executed.
//   - args contains the tool arguments.
//   - toolCallID is the upstream provider wire id (P14b audit trail); empty
//     when the call originates from a non-provider source.
//   - internalToolCallID is the FlowState session-scoped canonical id (P14)
//     stable across provider failover. Plans/Tool Execute Bus Bridge — Engine
//     to SSE (May 2026) — both ids propagate through the bus event so the
//     web SSE projector and the TUI subscriber key SwarmEvents on the same
//     identifier the chunk-driven path used today.
//
// Returns:
//   - None.
//
// Side effects:
//   - Publishes a tool execution start event on the engine bus.
func (e *Engine) publishToolBeforeEvent(sessionID string, toolName string, args map[string]interface{}, toolCallID string, internalToolCallID string) {
	e.bus.Publish(events.EventToolExecuteBefore, events.NewToolEvent(events.ToolEventData{
		SessionID:          sessionID,
		ToolName:           toolName,
		Args:               args,
		ToolCallID:         toolCallID,
		InternalToolCallID: internalToolCallID,
	}))
}

// publishToolAfterEvent publishes a tool execution completion event to the engine bus.
//
// Expected:
//   - toolName identifies the tool being executed.
//   - args contains the tool arguments.
//   - result contains the tool output.
//   - execErr contains the execution error, if any.
//   - toolCallID and internalToolCallID carry the same correlation IDs as
//     the matching publishToolBeforeEvent call; both propagate through the
//     terminal events (tool.execute.after, tool.execute.result on success,
//     tool.execute.error on failure).
//
// Returns:
//   - None.
//
// Side effects:
//   - Publishes a tool execution completion event on the engine bus.

// publishToolArgsValidationFailedEvent publishes a tool-args validation
// failure event to the engine bus. Recommendation E from the May 2026
// codebase-explorer investigation of the glm-4.6 `librarian` mis-call —
// dashboards subscribe to count + attribute these failures by
// provider/model/tool/error-class.
//
// Expected:
//   - ctx is the in-flight stream context; activeAgentID(ctx) resolves the
//     bound manifest's AgentID so concurrent streams' events stay correctly
//     attributed.
//   - toolName identifies the tool whose args failed validation.
//   - valErr is the *ValidationError ValidateToolArgs returned; falls back
//     to an empty error class when the err shape is not the typed one
//     (defensive — never produces an event without a class).
//   - toolCallID and internalToolCallID propagate from the matching
//     publishToolBeforeEvent / publishToolAfterEvent calls.
//
// Side effects:
//   - Publishes a tool.args.validation_failed event on the engine bus.
//
// Returns: result of publishToolArgsValidationFailedEvent.
func (e *Engine) publishToolArgsValidationFailedEvent(ctx context.Context, sessionID string, toolName string, valErr error, toolCallID string, internalToolCallID string) {
	if e.bus == nil {
		return
	}
	data := events.ToolArgsValidationFailedEventData{
		SessionID:          sessionID,
		AgentID:            e.activeAgentID(ctx),
		ProviderName:       e.LastProvider(),
		ModelName:          e.LastModel(),
		ToolName:           toolName,
		Error:              valErr,
		ToolCallID:         toolCallID,
		InternalToolCallID: internalToolCallID,
	}
	var vErr *ValidationError
	if errors.As(valErr, &vErr) {
		data.ValidationErrorClass = string(vErr.Class)
		data.UnknownKeys = vErr.UnknownKeys
		data.MissingKeys = vErr.MissingKeys
		data.ExpectedKeys = vErr.ExpectedKeys
	}
	e.bus.Publish(events.EventToolArgsValidationFailed, events.NewToolArgsValidationFailedEvent(data))
}

// publishToolAfterEvent ...
//
// Expected: parameters for publishToolAfterEvent.
//
// Returns: result of publishToolAfterEvent.
//
// Side effects: None.
func (e *Engine) publishToolAfterEvent(sessionID string, toolName string, args map[string]interface{}, result string, execErr error, toolCallID string, internalToolCallID string) {
	e.bus.Publish(events.EventToolExecuteAfter, events.NewToolEvent(events.ToolEventData{
		SessionID:          sessionID,
		ToolName:           toolName,
		Args:               args,
		Result:             result,
		Error:              execErr,
		ToolCallID:         toolCallID,
		InternalToolCallID: internalToolCallID,
	}))
	if execErr == nil {
		e.bus.Publish(events.EventToolExecuteResult, events.NewToolExecuteResultEvent(events.ToolExecuteResultEventData{
			SessionID:          sessionID,
			ToolName:           toolName,
			Args:               args,
			Result:             result,
			ToolCallID:         toolCallID,
			InternalToolCallID: internalToolCallID,
		}))
	} else {
		e.bus.Publish(events.EventToolExecuteError, events.NewToolExecuteErrorEvent(events.ToolExecuteErrorEventData{
			SessionID:          sessionID,
			ToolName:           toolName,
			Args:               args,
			Error:              execErr,
			ToolCallID:         toolCallID,
			InternalToolCallID: internalToolCallID,
		}))
	}
}

// publishProviderErrorEventCtx is the ctx-aware variant of
// publishProviderErrorEvent. Uses the in-flight stream's bound
// manifest (when present) for AgentID stamping so concurrent
// streams' error events stay correctly attributed.
//
// Expected: parameters for publishProviderErrorEventCtx.
// Returns: result of publishProviderErrorEventCtx.
// Side effects: None.
func (e *Engine) publishProviderErrorEventCtx(ctx context.Context, sessionID string, phase string, req *provider.ChatRequest, err error) {
	stats := provider.RequestDebugStats{}
	estimatedTokens := 0
	if req != nil {
		stats = provider.RequestStats(*req)
		if e != nil && e.tokenCounter != nil {
			estimatedTokens = e.estimateRequestTokens(req)
		}
	}

	data := events.ProviderErrorEventData{
		SessionID:            sessionID,
		AgentID:              e.activeAgentID(ctx),
		ProviderName:         e.LastProvider(),
		ModelName:            e.LastModel(),
		Error:                err,
		Phase:                phase,
		Stage:                phase,
		MessageCount:         stats.MessageCount,
		RequestBytes:         stats.RequestBytes,
		EstimatedInputTokens: estimatedTokens,
	}

	var provErr *provider.Error
	if errors.As(err, &provErr) {
		data.ErrorType = string(provErr.ErrorType)
		data.ErrorCode = provErr.ErrorCode
		data.HTTPStatus = provErr.HTTPStatus
		data.IsRetriable = provErr.IsRetriable
	}
	if conc, ok := e.currentProviderConcurrencyStats(data.ProviderName); ok {
		data.InFlight = conc.InFlight
		data.QueueDepth = conc.QueueDepth
		data.MaxConcurrent = conc.MaxConcurrent
	}
	slog.Warn("provider request failed",
		"session_id", data.SessionID,
		"agent_id", data.AgentID,
		"provider", data.ProviderName,
		"model", data.ModelName,
		"phase", data.Phase,
		"stage", data.Stage,
		"message_count", data.MessageCount,
		"request_bytes", data.RequestBytes,
		"estimated_input_tokens", data.EstimatedInputTokens,
		"in_flight", data.InFlight,
		"queue_depth", data.QueueDepth,
		"max_concurrent", data.MaxConcurrent,
		"error", err,
	)
	if e.bus != nil {
		e.bus.Publish(events.EventProviderError, events.NewProviderErrorEvent(data))
	}
}

// currentProviderConcurrencyStats ...
//
// Expected: parameters for currentProviderConcurrencyStats.
//
// Returns: result of currentProviderConcurrencyStats.
//
// Side effects: None.
func (e *Engine) currentProviderConcurrencyStats(providerName string) (provider.ConcurrencyDebugStats, bool) {
	if e == nil || e.providerRegistry == nil || providerName == "" {
		return provider.ConcurrencyDebugStats{}, false
	}
	p, err := e.providerRegistry.Get(providerName)
	if err != nil {
		return provider.ConcurrencyDebugStats{}, false
	}
	return provider.ConcurrencyStats(p)
}

// applyCategoryParams overlays sampling and budget hints from the
// active manifest's CategoryConfig onto the outgoing ChatRequest.
//
// The resolver is consulted only when:
//   - the engine has a CategoryResolver wired (cfg.CategoryResolver), AND
//   - the active manifest carries a non-empty OrchestratorMeta.Category
//
// Both conditions are deliberately permissive. A nil resolver, an empty
// category, or a resolve error all leave req untouched so callers that
// already populated the optional fields keep their values, and callers
// that did not set anything fall through to the provider's per-model
// defaults (e.g. the Anthropic provider's max_tokens=128k for
// claude-opus-4-7*, 4096 for unknown ids).
//
// Expected:
//   - req is a non-nil ChatRequest under construction. The optional
//     sampling fields may be zero or already populated.
//
// Returns:
//   - None.
//
// Side effects:
//   - Mutates *req: fills MaxTokens / Temperature when the active
//     CategoryConfig supplies them and the caller has not already.
func (e *Engine) applyCategoryParams(req *provider.ChatRequest) {
	if req == nil || e.categoryResolver == nil {
		return
	}
	category := e.manifest.OrchestratorMeta.Category
	if category == "" {
		return
	}
	cfg, err := e.categoryResolver.Resolve(category)
	if err != nil {
		return
	}
	if req.MaxTokens == 0 && cfg.MaxTokens > 0 {
		req.MaxTokens = cfg.MaxTokens
	}
	if req.Temperature == nil && cfg.Temperature != 0 {
		t := cfg.Temperature
		req.Temperature = &t
	}
}

// publishProviderRequestEventCtx is the ctx-aware variant of
// publishProviderRequestEvent. The AgentID stamped on the event
// is sourced from the manifest bound to ctx (the in-flight
// stream's manifest snapshot) when present, falling back to a
// locked read of the engine's active manifest otherwise — so
// concurrent streams stamp their own agent on their own events
// instead of racing on the shared engine field.
//
// Expected: parameters for publishProviderRequestEventCtx.
// Returns: result of publishProviderRequestEventCtx.
// Side effects: None.
func (e *Engine) publishProviderRequestEventCtx(ctx context.Context, sessionID string, req provider.ChatRequest) {
	if e.bus == nil {
		return
	}
	e.bus.Publish(events.EventProviderRequest, events.NewProviderRequestEvent(events.ProviderRequestEventData{
		SessionID:    sessionID,
		AgentID:      e.activeAgentID(ctx),
		ProviderName: req.Provider,
		ModelName:    req.Model,
		Request:      req,
	}))
}

// activeAgentID returns the agent ID bound to ctx (when a
// per-stream manifest binding is present) or the engine's active
// manifest ID under the read lock. Centralises the "which agent
// owns this side-effect" choice so concurrent streams cannot
// race on direct e.manifest.ID reads from telemetry paths.
//
// Expected:
//   - ctx is a valid context that may carry a boundManifestKey
//     value.
//
// Returns:
//   - The bound manifest's ID when present, the engine's active
//     manifest ID otherwise.
//
// Side effects:
//   - None.
func (e *Engine) activeAgentID(ctx context.Context) string {
	if bound, ok := manifestFromContext(ctx); ok {
		return bound.ID
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.manifest.ID
}

// publishProviderResponseEventCtx is the ctx-aware variant of
// publishProviderResponseEvent. Uses the in-flight stream's
// bound manifest for AgentID stamping when present.
//
// Expected: parameters for publishProviderResponseEventCtx.
// Returns: result of publishProviderResponseEventCtx.
// Side effects: None.
func (e *Engine) publishProviderResponseEventCtx(ctx context.Context, sessionID string, responseContent string) {
	if e.bus == nil {
		return
	}
	e.bus.Publish(events.EventProviderResponse, events.NewProviderResponseEvent(events.ProviderResponseEventData{
		SessionID:       sessionID,
		AgentID:         e.activeAgentID(ctx),
		ProviderName:    e.LastProvider(),
		ModelName:       e.LastModel(),
		ResponseContent: responseContent,
	}))
}

// sessionIDFromContext extracts the session ID from the context, returning
// an empty string if no session ID is present.
//
// Expected:
//   - ctx is a valid context that may carry a session.IDKey value.
//
// Returns:
//   - The session ID string, or empty if not set.
//
// Side effects:
//   - None.
func sessionIDFromContext(ctx context.Context) string {
	id, ok := ctx.Value(session.IDKey{}).(string)
	if !ok {
		return ""
	}
	return id
}
