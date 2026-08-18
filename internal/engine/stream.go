// Package engine — stream entry point and emitters.
//
// This file holds the public Stream entry point, its heartbeat, usage and
// context-usage emitters, knowledge-extraction dispatch and the provider
// stream adapter (including tool-result delivery with WriteToolError
// routing for error chunks).
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine/lifecycle"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
)

// SetOnStreamCancel sets the callback invoked when the stream context is
// cancelled. The callback receives the sessionID so downstream wiring
// (e.g. background task cancellation) can clean up per-session resources.
//
// Expected:
//   - fn may be nil (disables the callback).
//
// Side effects:
//   - Replaces any previously set onStreamCancel callback under the engine
//     write lock.
//
// Returns: result of SetOnStreamCancel.
func (e *Engine) SetOnStreamCancel(fn func(sessionID string)) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.onStreamCancel = fn
	e.mu.Unlock()
}

// SeedHistory pre-populates the context store with historical messages for
// sessionID so that the engine retains conversation context after a restart.
//
// Expected:
//   - sessionID is non-empty and identifies a session with prior history.
//   - messages are the historical turns in chronological order, excluding the
//     current user message (the caller is responsible for the exclusion).
//
// Side effects:
//   - Appends messages to e.store on the first call per sessionID.
//   - Subsequent calls for the same sessionID are no-ops (idempotent).
//
// Returns: result of SeedHistory.
func (e *Engine) SeedHistory(sessionID string, messages []provider.Message) {
	if e.store == nil || sessionID == "" || len(messages) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, already := e.seededSessions[sessionID]; already {
		return
	}
	for _, msg := range messages {
		e.store.Append(msg)
	}
	e.seededSessions[sessionID] = struct{}{}
}

// Stream sends a message and returns a channel of streamed response chunks.
//
// Expected:
//   - ctx is a valid context for the streaming operation.
//   - agentID identifies the agent (currently unused, reserved for future routing).
//   - message is the user's input text.
//
// Returns:
//   - A channel of StreamChunk values containing the response.
//   - An error if the initial provider stream fails.
//
// Side effects:
//   - Appends the user message to the context store.
//   - Embeds the user message if an embedding provider is configured.
//   - Spawns a goroutine to process the stream and handle tool calls.
func (e *Engine) Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error) {
	sessionID := sessionIDFromContext(ctx)

	e.mu.Lock()
	e.currentSessionID = sessionID
	if _, exists := e.sessionComplexity[sessionID]; !exists {
		complexity, _ := EstimateComplexity(message)
		e.sessionComplexity[sessionID] = complexity
	}
	e.mu.Unlock()

	// Resolve THIS call's manifest. When the caller supplies an
	// agentID, we look it up in the registry directly so the
	// snapshot we bind into ctx reflects the requested agent
	// even if a concurrent Stream call races to SetManifest a
	// different agent in between. The legacy SetManifest call
	// keeps the engine's "current" manifest tracking the most-
	// recent dispatch — important for single-session sequential
	// flows, the swap-for-next-turn semantic, and downstream
	// readers that don't yet route through ctx (telemetry that
	// uses activeAgentID falls back to e.manifest under lock).
	var streamManifest agent.Manifest
	if agentID != "" && e.agentRegistry != nil {
		if manifest, found := e.agentRegistry.Get(agentID); found {
			e.mu.RLock()
			currentID := e.manifest.ID
			e.mu.RUnlock()
			if manifest.ID != currentID {
				e.SetManifest(*manifest)
			}
			streamManifest = *manifest
		}
	}
	if streamManifest.ID == "" {
		streamManifest = e.Manifest()
	}

	// Bind the snapshot into ctx. Every downstream read inside
	// the stream lifecycle (buildContextWindow →
	// BuildSystemPromptCtx, buildToolSchemasCtx, the tool-loop
	// retry path, telemetry publishers via activeAgentID) routes
	// through the bound value rather than e.manifest, so a
	// concurrent Stream that triggers SetManifest on a different
	// agent cannot overwrite the in-flight manifest mid-call.
	streamCtx := WithBoundManifest(ctx, streamManifest)

	streamCtx = WithBoundProviderModel(streamCtx, e.LastProvider(), e.LastModel())

	messages := e.buildContextWindow(streamCtx, sessionID, message)

	if _, err := e.lifecycle.ContextAssembly.Execute(lifecycle.ContextAssemblyCtx{
		Messages:    messages,
		TokenBudget: e.ModelContextLimit(),
		Manifest:    &streamManifest,
	}); err != nil {
		slog.Warn("context_assembly stage rejected", "error", err)
	}

	// Thread per-turn attachments onto the final user message in the
	// request payload. Plan "Chat Attachments Backend (May 2026)" §6
	// task-04 — attachments arrive via session.AttachmentsFromContext
	// (the manager populates the key just before calling Stream). The
	// engine seam stays pure-Go-data; per-provider translators lift
	// the slice into native content blocks inside their request-builder.
	if atts := session.AttachmentsFromContext(streamCtx); len(atts) > 0 {
		// The buildContextWindow paths above all append the user message
		// at the tail. Stamp the attachments slice onto that final
		// message so the provider sees them on the current turn only.
		if len(messages) > 0 && messages[len(messages)-1].Role == "user" {
			messages[len(messages)-1].Attachments = atts
		}
	}

	userMsg := provider.Message{Role: "user", Content: message}
	if e.store != nil {
		msgID := e.store.AppendReturningID(userMsg)
		e.embedMessage(streamCtx, message, msgID)
	}

	req := provider.ChatRequest{
		Provider: e.lastProviderCtx(streamCtx),
		Model:    e.lastModelCtx(streamCtx),
		Messages: messages,
		Tools:    e.buildToolSchemasCtx(streamCtx),
	}
	e.applyCategoryParams(&req)

	if override := session.ProviderOverrideFromContext(streamCtx); override != "" {
		req.Provider = override
	}
	if override := session.ModelOverrideFromContext(streamCtx); override != "" {
		req.Model = override
	}
	if e.providerRegistry != nil && req.Provider != "" && req.Model != "" && !e.providerServesModel(req.Provider, req.Model) {
		slog.Warn("session override pair not servable; falling back to engine defaults",
			"provider_override", req.Provider,
			"model_override", req.Model,
		)
		req.Provider = e.lastProviderCtx(streamCtx)
		req.Model = e.lastModelCtx(streamCtx)
	}
	// Per-turn forced tool_choice. The synthesis-hang corrective retry
	// (delegation.go post-member gate loop) forces the gated member to
	// emit the required coordination_store write on re-delegation rather
	// than narrate it. Applied per-turn via context so the first attempt
	// stays unconstrained — only the corrective retry sets the override.
	// Empty means "do not set"; the provider mappers then pick their
	// default "auto" behaviour.
	if override := session.ToolChoiceOverrideFromContext(streamCtx); override != "" {
		req.ToolChoice = override
	}

	// Compute the context_usage payload BEFORE streamFromProvider so
	// the gate's refusal path (which builds the synthetic refusal
	// channel inline) and the success path see the same usage event.
	// The chunk is forwarded to outChan as the first artefact, ahead
	// of any provider chunks (and ahead of the gate refusal chunk on
	// overflow), so the chat UI's chip updates even when the gate
	// refuses the request.
	usageChunk, hasUsage := e.buildContextUsageChunk(&req)

	// Compute the provider_quota payload BEFORE streamFromProvider so
	// the chip pivots in lockstep with context_usage. Same gate
	// semantics — suppressed when the tracker has no Snapshot for
	// (req.Provider, req.Model). Plan §"Engine integration" lines
	// 314-316 + cadence parity with context_usage. PR4.
	quotaChunk, hasQuota := e.buildProviderQuotaChunk(streamCtx, &req)

	// Phase 3 — post-turn emitter. Constructed once per Stream so the
	// goroutine inside the loop can emit a fresh context_usage chunk
	// before every terminal Done. Only wired when the engine can
	// compute a meaningful figure for the request — same gate as the
	// pre-send chunk above so degraded environments stay quiet.
	postTurnEmitter := e.makePostTurnUsageEmitter(&req, hasUsage)

	// Post-turn provider_quota emitter mirrors the context_usage
	// closure above. Constructed once per Stream so the
	// streamWithToolLoop terminal-Done path can emit a fresh
	// provider_quota chunk reflecting the cumulative spend the just-
	// completed turn ticked up. Nil when the tracker is not wired —
	// the runtime gate keeps degraded environments quiet. PR4.
	postTurnQuotaEm := e.makePostTurnQuotaEmitter(&req)
	_ = postTurnQuotaEm // used inside the goroutine below; explicit so future maintainers see the binding

	if _, err := e.lifecycle.PreStream.Execute(lifecycle.PreStreamCtx{
		Messages:    req.Messages,
		TokenBudget: e.ModelContextLimit(),
		Manifest:    &streamManifest,
	}); err != nil {
		slog.Warn("pre_stream stage rejected turn", "error", err)
		return nil, err
	}

	providerChunks, err := e.streamFromProvider(streamCtx, &req)
	e.publishProviderRequestEventCtx(streamCtx, sessionID, req)
	if err != nil {
		e.publishProviderErrorEventCtx(streamCtx, sessionID, "stream_init", &req, err)
		return nil, err
	}

	outChan := make(chan provider.StreamChunk, streamBufferSize)

	// Streaming heartbeat: publish streaming.heartbeat onto the bus at
	// e.heartbeatInterval cadence so the chat UI's adaptive watchdog
	// re-arms even when the provider is silent (long-thinking phases,
	// mid-tool-loop quiet periods, synchronous delegate spans). Per the
	// Streaming Liveness ADR. The hbCtx derives from streamCtx so it
	// cancels when the parent does; defer hbCancel in the chunk-pump
	// goroutine ensures we also stop emitting when the stream completes
	// successfully (streamCtx may outlive the turn).
	hbCtx, hbCancel := context.WithCancel(streamCtx)
	if e.heartbeatInterval > 0 && e.bus != nil {
		go e.runStreamingHeartbeat(hbCtx, sessionID, streamManifest.ID)
	}

	go func() {
		defer close(outChan)
		defer hbCancel()
		// Emit context_usage first so the chip pivots before any
		// content/tool/error chunk lands. Forwarded only when the
		// gate has enough information to compute it (token counter
		// wired AND limit > 0); otherwise dropped silently — a
		// missing chip is a better degradation than a malformed one.
		if hasUsage {
			outChan <- usageChunk
		}
		// Inline provider_quota chunk lands second so the chip's
		// dual-display (context-usage + spend) updates in one
		// frame. Cadence parity with context_usage per plan
		// §"Engine integration" lines 314-316. Suppression via
		// tryEmitProviderQuotaInline keeps the SSE wire quiet
		// between identical payloads (the no-spend boot path can
		// emit the same NotConfigured Snapshot turn after turn).
		if hasQuota {
			e.tryEmitProviderQuotaInline(sessionID, quotaChunk, outChan)
		}
		e.streamWithToolLoop(streamCtx, sessionID, messages, providerChunks, outChan, postTurnEmitter)
		// Post-turn provider_quota emit — fires once after the
		// stream completes (terminal Done forwarded inside
		// streamWithToolLoop). Mirrors the context_usage post-
		// turn cadence makePostTurnUsageEmitter installs inside
		// the loop; we emit here because the post-turn quota
		// emitter is a sibling channel (separate from the
		// context_usage closure the loop hosts). The emitter is
		// nil-safe so streams without quota wiring no-op.
		if postTurnQuotaEm != nil {
			postTurnQuotaEm(streamCtx, outChan)
		}
		e.lifecycle.PostProcess.Execute(lifecycle.PostProcessCtx{
			SessionID: sessionID,
			Messages:  messages,
		})
		e.dispatchKnowledgeExtraction(streamCtx, sessionID, messages)
	}()

	return outChan, nil
}

// runStreamingHeartbeat ticks at e.heartbeatInterval and publishes a
// streaming.heartbeat event onto the bus for the duration of an active
// turn. Exits when ctx is cancelled (turn ends or streamCtx cancelled).
//
// The Phase discriminant is "thinking" by default — a safe value
// because the adaptive watchdog's "thinking" threshold (120s in the
// Streaming Liveness ADR) is the longest of the four, so a wrong
// label errs on the side of fewer false-positive stalls. Refining
// the phase from engine state (generating / tool_executing / queued)
// is a follow-on; the wire format already carries the field so
// frontend consumers are forward-compatible.
//
// Side effects:
//   - Publishes EventStreamingHeartbeat on e.bus per tick.
//
// Expected: parameters for runStreamingHeartbeat.
// Returns: result of runStreamingHeartbeat.
func (e *Engine) runStreamingHeartbeat(ctx context.Context, sessionID, agentID string) {
	ticker := time.NewTicker(e.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.publishStreamingHeartbeat(sessionID, agentID, "thinking")
		}
	}
}

// publishStreamingHeartbeat fires one streaming.heartbeat event onto
// the engine bus. Nil-safe on e.bus so providers / tests without a
// wired bus don't crash.
//
// Reads the per-session in-flight cumulative output_tokens off
// e.sessionOutputTokens and threads it onto the bus payload as
// TokenCount so the chat UI's streaming chrome (UI Parity PR5, May
// 2026) can render a live counter next to the working-on label and
// compute tokens-per-second from the delta-vs-prev-tick at the 15s
// cadence. Zero for sessions without a recorded UsageDelta — the
// frontend gates the counter render on >0 so the pre-first-tick
// state stays hidden.
//
// Side effects:
//   - Publishes EventStreamingHeartbeat when e.bus is non-nil.
//
// Expected: parameters for publishStreamingHeartbeat.
// Returns: result of publishStreamingHeartbeat.
func (e *Engine) publishStreamingHeartbeat(sessionID, agentID, phase string) {
	if e.bus == nil {
		return
	}
	e.bus.Publish(events.EventStreamingHeartbeat, events.NewStreamingHeartbeatEvent(events.StreamingHeartbeatEventData{
		SessionID:  sessionID,
		AgentID:    agentID,
		Phase:      phase,
		TokenCount: e.sessionOutputTokensSnapshot(sessionID),
	}))
}

// recordSessionOutputTokens stores the latest cumulative output_tokens
// for an in-flight session so the next streaming.heartbeat tick can
// thread it onto the bus payload. The value is monotonic per-turn (a
// later UsageDelta carries the latest cumulative figure, not an
// increment), so the store overwrites on every write. Concurrent
// writes are serialised by sessionOutputTokensMu (sub-microsecond
// critical section on a single map write — never on the chunk-processing
// hot path's request stack).
//
// Called from processStreamChunks each time a chunk's
// provider.UsageDelta carries OutputTokens >0.
//
// Expected:
//   - sessionID is the in-flight session.
//   - tokens is the cumulative output_tokens from the most recent
//     UsageDelta. Zero is tolerated (the chunk did not carry usage data
//     — but processStreamChunks gates on >0 before calling this so the
//     call is a no-op in that case anyway).
//
// Side effects:
//   - Writes to e.sessionOutputTokens under e.sessionOutputTokensMu.
//
// Returns: result of recordSessionOutputTokens.
func (e *Engine) recordSessionOutputTokens(sessionID string, tokens int64) {
	if sessionID == "" {
		return
	}
	e.sessionOutputTokensMu.Lock()
	defer e.sessionOutputTokensMu.Unlock()
	if e.sessionOutputTokens == nil {
		e.sessionOutputTokens = make(map[string]int64)
	}
	e.sessionOutputTokens[sessionID] = tokens
}

// sessionOutputTokensSnapshot returns the most-recently-recorded
// cumulative output_tokens for a session. Zero for sessions with no
// recorded UsageDelta. Read-only fast path under
// sessionOutputTokensMu.RLock so the heartbeat ticker's lookup is
// uncontended with concurrent writes from processStreamChunks.
//
// Returns:
//   - The cumulative output_tokens; zero when the session has no
//     recorded UsageDelta.
//
// Side effects:
//   - None.
//
// Expected: parameters for sessionOutputTokensSnapshot.
func (e *Engine) sessionOutputTokensSnapshot(sessionID string) int64 {
	if sessionID == "" {
		return 0
	}
	e.sessionOutputTokensMu.RLock()
	defer e.sessionOutputTokensMu.RUnlock()
	return e.sessionOutputTokens[sessionID]
}

// makePostTurnUsageEmitter returns a postTurnUsageEmitter closure
// captured against the in-flight request, or nil when the engine cannot
// compute a meaningful figure for the request (no counter, no limit).
//
// The closure synthesises a trailing assistant message from the just-
// completed turn's accumulated content / thinking and rebuilds the
// context_usage payload against (req.Messages + assistant turn). The
// chip ticks up to roughly "what the next send would cost" — matching
// the TUI status-bar's per-redraw refresh against LastContextResult.
//
// Expected:
//   - req captures the request the stream is running against. Provider,
//     Model, Messages, Tools and MaxTokens are all read off req.
//   - hasUsage is the pre-send gate's verdict. When false the closure
//     is nil so the post-turn emit is suppressed for the same
//     environments where the pre-send chunk would also be missing.
//
// Returns:
//   - The closure, or nil when hasUsage=false.
//
// Side effects:
//   - None at construction. The closure has no side effects beyond
//     writing one StreamChunk per call to the supplied outChan.
func (e *Engine) makePostTurnUsageEmitter(req *provider.ChatRequest, hasUsage bool) postTurnUsageEmitter {
	if !hasUsage || req == nil {
		return nil
	}
	// Snapshot the immutable request fields so the closure is not
	// aliased to a caller-mutable struct.
	providerID := req.Provider
	modelID := req.Model
	tools := req.Tools
	maxTokens := req.MaxTokens
	baseMessages := make([]provider.Message, len(req.Messages))
	copy(baseMessages, req.Messages)

	return func(outChan chan<- provider.StreamChunk, postTurnContent, postTurnThinking string) {
		// Build a synthetic post-turn message slice. The caller's
		// baseMessages is the pre-send input; appending the assistant
		// turn produces the input the next send would carry, which is
		// the figure the chip should display post-turn.
		msgs := baseMessages
		if postTurnContent != "" || postTurnThinking != "" {
			msgs = append(msgs, provider.Message{
				Role:     "assistant",
				Content:  postTurnContent,
				Thinking: postTurnThinking,
			})
		}
		body, ok := e.buildContextUsagePayload(providerID, modelID, msgs, tools, maxTokens)
		if !ok {
			return
		}
		outChan <- provider.StreamChunk{
			EventType: "context_usage",
			Content:   body,
		}
	}
}

// dispatchKnowledgeExtraction fires the Phase 3 knowledge extractor on
// a background goroutine when one is configured and Layer 3 is enabled.
// The caller's messages slice is copied so the extractor never races
// with subsequent assembly runs on the shared slab. A fresh
// context.Background with a 30-second deadline is used so the stream's
// original ctx — which closes when the channel drains — does not
// cancel the extraction mid-flight.
//
// Expected:
//   - messages is the final message slice the stream ran against. The
//     caller must not mutate it after calling this method; the copy
//     inside protects only the in-flight extraction, not the caller.
//
// Returns:
//   - None. Errors from the extractor are logged at WARN and do not
//     propagate.
//
// Side effects:
//   - Spawns a goroutine when the extractor is wired and enabled.
func (e *Engine) dispatchKnowledgeExtraction(baseCtx context.Context, sessionID string, messages []provider.Message) {
	if !e.compressionConfig.SessionMemory.Enabled {
		return
	}

	extractor := e.resolveKnowledgeExtractor(sessionID)
	if extractor == nil {
		return
	}

	msgsCopy := make([]provider.Message, len(messages))
	copy(msgsCopy, messages)

	e.extractionWG.Add(1)
	go func() {
		defer e.extractionWG.Done()
		runKnowledgeExtraction(baseCtx, extractor, msgsCopy)
	}()
}

// ErrExtractionTimeout is returned by WaitForBackgroundExtractions
// when the wait expired with work still in flight. It is the sentinel
// callers check to distinguish "timed out after actually waiting" from
// every other nil-error outcome (clean finish, or the no-wait skip
// path taken when timeout <= 0). Pre-M7 the method returned a plain
// bool, and `false` conflated "timed out" with "skipped because
// timeout <= 0" — the CLI warn fired on both, producing a spurious
// warning on every run that opted out of waiting.
var ErrExtractionTimeout = errors.New("engine: background extraction wait timed out")

// WaitForBackgroundExtractions blocks until every in-flight L3
// extraction goroutine dispatched by dispatchKnowledgeExtraction has
// returned, or until timeout elapses — whichever comes first. Long-
// running hosts (flowstate serve) need never call this because the
// goroutines eventually complete under their own 30-second timeout and
// the server keeps the process alive. Short-lived CLI hosts (flowstate
// run) must call this before exiting or the extractions are orphaned
// at os.Exit and the session-memory store is never saved.
//
// Expected:
//   - timeout is the maximum wall-clock duration to wait. A non-
//     positive value is treated as "no wait" and the call returns
//     nil immediately (retaining the legacy fire-and-forget contract
//     when the caller does not care).
//
// Returns:
//   - nil when every dispatched goroutine finished within the timeout
//     OR the caller opted out of waiting with timeout <= 0. Both
//     cases share the "no work to warn about" semantics.
//   - ErrExtractionTimeout when the wait expired with work still in
//     flight. In-flight work continues but the caller is free to
//     proceed; the L3 save may be incomplete.
//
// Side effects:
//   - Blocks the caller's goroutine for at most timeout.
func (e *Engine) WaitForBackgroundExtractions(timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}
	done := make(chan struct{})
	go func() {
		e.extractionWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return ErrExtractionTimeout
	}
}

// resolveKnowledgeExtractor returns the extractor to use for the given
// sessionID. When a factory is wired (production path via
// buildCompressionComponents) it is called with the live sessionID so
// SessionMemoryStore.Save writes under the session actually being
// streamed. When only the static extractor is wired (single-session
// tests) it is returned unchanged. Nil signals "feature disabled for
// this engine".
//
// Expected:
//   - sessionID may be empty; the factory receives it verbatim and is
//     expected to tolerate that (it will produce a memory-dir of "").
//
// Returns:
//   - The extractor to drive, or nil when neither factory nor static
//     extractor is wired.
//
// Side effects:
//   - None at this layer. The factory, if supplied, may allocate.
func (e *Engine) resolveKnowledgeExtractor(sessionID string) *recall.KnowledgeExtractor {
	if e.knowledgeExtractorFactory != nil {
		return e.knowledgeExtractorFactory(sessionID)
	}
	return e.knowledgeExtractor
}

// runKnowledgeExtraction is the body of the background goroutine spawned
// by dispatchKnowledgeExtraction. It uses a deliberately fresh
// context.Background so the stream's ctx — which is cancelled when the
// channel closes — cannot cut the extraction short.
//
// Expected:
//   - extractor is a non-nil KnowledgeExtractor.
//   - msgs is a defensive copy of the caller's slice — safe to read
//     from a goroutine without further coordination.
//
// Returns:
//   - None. Errors are logged at WARN and discarded.
//
// Side effects:
//   - One LLM call and at most one store save through the extractor.
func runKnowledgeExtraction(parent context.Context, extractor *recall.KnowledgeExtractor, msgs []provider.Message) {
	extractCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer cancel()
	if err := extractor.Extract(extractCtx, msgs); err != nil {
		slog.Warn("engine knowledge extraction failed",
			"error", err,
		)
	}
}

// streamFromProvider initiates a streaming chat request with the provider, applying any configured hooks.
//
// Expected:
//   - ctx is a valid context for the streaming operation.
//   - req is a pointer to a chat request with messages and tools.
//
// Returns:
//   - A channel of StreamChunk values from the provider.
//   - An error if the stream fails to initialise.
//
// Side effects:
//   - Executes hook chain if configured. Hooks may mutate req.
//   - When the proactive context-window overflow gate fires, returns
//     (synthetic-channel, nil) carrying a single critical-error chunk
//     and DOES NOT call the upstream provider. The synthetic-channel
//     path is what surfaces the saturation as a stream_critical SSE
//     event the Vue chat banner can render.
func (e *Engine) streamFromProvider(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if ctx.Err() != nil {
		return nil, fmt.Errorf("stream from provider: %w", ctx.Err())
	}
	slog.Info("engine stream request", "provider", req.Provider, "model", req.Model, "messages", len(req.Messages))
	if session.SkipContextWindowOverflowCheckFromContext(ctx) {
		slog.Info("engine stream request skipping proactive overflow gate after compaction",
			"provider", req.Provider, "model", req.Model, "messages", len(req.Messages))
		handler := e.baseStreamHandler()
		if e.hookChain != nil {
			handler = e.hookChain.Execute(handler)
		}
		return handler(ctx, req)
	}
	if pErr := e.checkContextWindowOverflow(req); pErr != nil {
		slog.Warn("engine refused over-budget request",
			"provider", req.Provider, "model", req.Model, "estimated_input_tokens", pErr.EstimatedInputTokens, "limit", pErr.ContextLimit)
		return e.overflowRefusalChannel(pErr), nil
	}
	handler := e.baseStreamHandler()
	if e.hookChain != nil {
		handler = e.hookChain.Execute(handler)
	}
	return handler(ctx, req)
}

// contextWindowOverflowMessage is the canonical user-facing safe message
// the api/errors.go layer emits when the proactive overflow gate refuses
// a request. The wording must satisfy the user-actionable-copy contract
// pinned by engine_test.go: it names the failure mode ("context window")
// and hints at recoverable user actions ("trim recent tool results",
// "fresh session"). The Vue CriticalErrorBanner renders this verbatim
// as the user-visible body of the persistent banner.
const contextWindowOverflowMessage = "context window exceeded — start a fresh session or trim recent tool results before retrying"

// Output-reserve constants. See checkContextWindowOverflow for rationale.
const (
	// defaultOutputReserve is the reserve applied when the caller did
	// not stamp MaxTokens on the request. Mirrors OpenCode's
	// compaction.ts:30-39 default reserve when its model registry has
	// no explicit OutputLimit. The value is conservative — most
	// production turns use a fraction of this — but pre-this-fix the
	// gate left zero room for output, so the model would either
	// truncate immediately or hang in reasoning-only "thought into
	// the void" turns.
	defaultOutputReserve = 4096
	// minOutputReserve floors the reserve so a small caller-supplied
	// MaxTokens cannot sneak a request through by shrinking the
	// reserve below a usable size. The 1024 floor matches the
	// smallest plausible non-empty assistant turn.
	minOutputReserve = 1024
)

// outputReserveFor returns the output reserve to subtract from the raw
// context limit before comparing against the estimated input. The reserve
// is `max(req.MaxTokens, minOutputReserve)` when MaxTokens is non-zero,
// otherwise `max(model.OutputLimit, minOutputReserve)` when the provider
// registry advertises a per-model OutputLimit, otherwise the engine's
// hardcoded `defaultOutputReserve`. Centralised so the gate and the
// context_usage event emitter agree on the same value.
//
// Promoted to a method on *Engine in Slice 1 of the Phase-4 follow-ups
// so the resolver can route through the failover manager. Pre-Slice-1
// the helper was a free function and the reserve was fixed at
// `max(req.MaxTokens or 4096, 1024)` — a one-size-fits-all default. Per-
// model OutputLimit lets an Anthropic 200K-context model and a glm-4.6
// 128K-context model declare their own reasonable output budgets without
// the caller having to stamp MaxTokens.
//
// Expected:
//   - req is non-nil. MaxTokens may be zero.
//
// Returns:
//   - The reserve in tokens. Strictly positive.
//
// Side effects:
//   - None.
func (e *Engine) outputReserveFor(req *provider.ChatRequest) int {
	if req.MaxTokens > 0 {
		if req.MaxTokens < minOutputReserve {
			return minOutputReserve
		}
		return req.MaxTokens
	}
	if e != nil {
		modelLimit := e.ResolveOutputLimit(req.Provider, req.Model)
		if modelLimit > 0 {
			if modelLimit < minOutputReserve {
				return minOutputReserve
			}
			return modelLimit
		}
	}
	return defaultOutputReserve
}

// checkContextWindowOverflow returns a non-nil *provider.Error when the
// engine's estimated input-token count for req exceeds the per-model
// context limit configured for (req.Provider, req.Model), reserving
// space for the eventual response. It mirrors OpenCode's isOverflow
// gate (compaction.ts:30-89): the smallest viable slice of context-
// management is detect-and-refuse before send. Auto-compaction, old-
// tool-output pruning, and per-tool result truncation are subsequent
// slices that build on this seam.
//
// The reserve formula closes the May 2026 saturation bug where the gate
// compared estimated input against the raw context limit, leaving zero
// budget for the response when the input filled 100% of the window. The
// model would either truncate immediately or hang in reasoning-only
// "thought into the void" turns.
//
//	reserve = max(req.MaxTokens or defaultOutputReserve, minOutputReserve)
//	usable  = max(1, limit - reserve)
//	refuse if estimated > usable
//
// The estimate uses the engine's wired tokenCounter when available
// (TiktokenCounter or ApproximateCounter — the latter's character-based
// 1-token-≈-4-chars heuristic is documented at
// internal/context/token_budget.go ApproximateCounter.Count). When no
// counter is wired, the gate is a deliberate no-op so legacy callers
// without a counter continue to flush as before — failure is the model
// "thinking into the void" later, not a spurious refusal here.
//
// Per-model limits flow through the engine's existing ResolveContextLength
// pipeline (ResolveContextLength → failoverManager.ResolveContextLength →
// provider.Models()), so adding a new model with a documented context
// length is a registry concern, not a hardcoded table here. When the
// resolver yields zero (unknown provider/model), the configured
// systemPromptBudget fallback applies — operators with constrained
// hardware override that fallback via cfg.SystemPromptBudget.
//
// Expected:
//   - req is the assembled chat request, post-buildContextWindow.
//
// Returns:
//   - nil when the request fits or the gate cannot evaluate (no counter).
//   - A *provider.Error{ErrorType: ErrorTypeContextWindowExceeded} when
//     the estimate exceeds the usable budget. Severity classification
//     picks this up via severityFromProviderErrorType and routes the
//     chunk through the SeverityCritical path.
//
// Side effects:
//   - None.
func (e *Engine) checkContextWindowOverflow(req *provider.ChatRequest) *provider.Error {
	if e == nil || req == nil || e.tokenCounter == nil {
		return nil
	}
	limit := e.ResolveContextLength(req.Provider, req.Model)
	if limit <= 0 {
		// No limit known and no fallback wired — pass through. The
		// existing in-stream classification still catches a genuine
		// upstream context-length-exceeded response if the provider
		// returns one.
		return nil
	}

	estimated := e.estimateRequestTokens(req)
	reserve := e.outputReserveFor(req)
	usable := limit - reserve
	if usable < 1 {
		usable = 1
	}
	if estimated <= usable {
		return nil
	}

	return &provider.Error{
		Provider:             req.Provider,
		Model:                req.Model,
		ErrorType:            provider.ErrorTypeContextWindowExceeded,
		Message:              contextWindowOverflowMessage,
		IsRetriable:          false,
		EstimatedInputTokens: estimated,
		ContextLimit:         limit,
	}
}

// contextUsagePayload is the JSON shape of the context_usage SSE event.
// Pre-marshalled into chunk.Content so the SSE writer in
// internal/api/server.go can re-emit it verbatim with the canonical
// `"type":"context_usage"` discriminant injected by writeSSEContextUsage.
//
// Field semantics:
//   - InputTokens — engine-side estimate of the prompt cost
//     (estimateRequestTokens; conservative tiktoken / character-based).
//   - OutputReserve — the reserve subtracted from limit to compute
//     usable. Matches outputReserveFor for the same request.
//   - Limit — the resolved per-(provider, model) context window in tokens.
//   - Percentage — round(input_tokens / limit * 100). Capped at 999
//     so a degraded estimate cannot break the chip's three-digit
//     formatter.
//   - Provider / Model — canonical ids the chip displays alongside
//     the usage figure.
type contextUsagePayload struct {
	InputTokens   int    `json:"input_tokens"`
	OutputReserve int    `json:"output_reserve"`
	Limit         int    `json:"limit"`
	Percentage    int    `json:"percentage"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
}

// buildContextUsageChunk computes the context_usage payload for req and
// returns it as a StreamChunk{EventType: "context_usage"}. Emitted as
// the first artefact on every Stream that has enough information to
// compute it: token counter wired AND resolved limit > 0. When either
// is missing the chunk is suppressed (returns hasUsage=false) — a
// missing chip is a better degradation than a chip showing zeros.
//
// Expected:
//   - req is the assembled chat request, post-buildContextWindow.
//
// Returns:
//   - chunk with EventType="context_usage" and JSON payload in Content.
//   - hasUsage=false when the engine cannot compute a meaningful figure.
//
// Side effects:
//   - None beyond JSON marshalling.
func (e *Engine) buildContextUsageChunk(req *provider.ChatRequest) (provider.StreamChunk, bool) {
	if e == nil || req == nil {
		return provider.StreamChunk{}, false
	}
	body, ok := e.buildContextUsagePayload(req.Provider, req.Model, req.Messages, req.Tools, req.MaxTokens)
	if !ok {
		return provider.StreamChunk{}, false
	}
	return provider.StreamChunk{
		EventType: "context_usage",
		Content:   body,
	}, true
}

// buildContextUsagePayload is the shared core of every context_usage
// emitter. It returns the JSON-marshalled contextUsagePayload for the
// (provider, model, messages, tools, maxTokens) tuple, or hasUsage=false
// when the engine cannot compute a meaningful figure (no counter or no
// resolvable limit).
//
// Phase 3 extracted this from buildContextUsageChunk so the post-turn
// emission and the api-server's session-load / agent-model-switch hooks
// re-use the exact same shape and reserve formula as the pre-send
// emission. Drift between emission sites was the failure mode this
// extraction prevents — every `context_usage` event the chip dispatches
// must agree on output_reserve / percentage / limit semantics.
//
// Expected:
//   - providerID and modelID identify the (provider, model) pair the
//     usage figure is for.
//   - messages is the conversation slice the input-token estimate
//     should be computed against.
//   - tools is the tool-schema slice the per-tool overhead is summed
//     across (zero when no tools are wired).
//   - maxTokens is the caller-supplied output limit; pass 0 to use
//     defaultOutputReserve.
//
// Returns:
//   - body — JSON encoding of contextUsagePayload, ready to drop into
//     a StreamChunk.Content or write to an SSE response verbatim.
//   - hasUsage=false when no token counter is wired OR the resolved
//     limit is zero.
//
// Side effects:
//   - None beyond JSON marshalling.
func (e *Engine) buildContextUsagePayload(providerID, modelID string, messages []provider.Message, tools []provider.Tool, maxTokens int) (string, bool) {
	if e == nil || e.tokenCounter == nil {
		return "", false
	}
	limit := e.ResolveContextLength(providerID, modelID)
	if limit <= 0 {
		return "", false
	}

	syntheticReq := &provider.ChatRequest{
		Provider:  providerID,
		Model:     modelID,
		Messages:  messages,
		Tools:     tools,
		MaxTokens: maxTokens,
	}
	estimated := e.estimateRequestTokens(syntheticReq)
	reserve := e.outputReserveFor(syntheticReq)
	pct := 0
	if limit > 0 {
		pct = (estimated * 100) / limit
		if pct > 999 {
			pct = 999
		}
	}

	payload := contextUsagePayload{
		InputTokens:   estimated,
		OutputReserve: reserve,
		Limit:         limit,
		Percentage:    pct,
		Provider:      providerID,
		Model:         modelID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		// Marshal cannot fail for a struct of primitives, but if it
		// somehow did we suppress the event rather than emitting a
		// malformed chunk the parser would classify as "unknown".
		return "", false
	}
	return string(body), true
}

// ContextUsageJSONForSession is the public Phase 3 helper the api server
// calls on session-load (SSE-connect) and after agent / model switch
// PATCH so the chip ticks up immediately rather than waiting for the
// next pre-send. Returns the same JSON payload shape the streamed
// `context_usage` chunk carries — the api server writes it directly to
// the SSE response (with the type discriminant injected) on
// /sessions/{id}/stream connect, and embeds it in the JSON body of the
// agent / model PATCH responses.
//
// Mirrors the TUI's StatusBar pattern (internal/tui/intents/chat/intent.go
// syncStatusBar): the chip reflects current state at all times, not
// just after a send.
//
// Expected:
//   - providerID and modelID identify the (provider, model) pair the
//     usage figure is for. Caller is the api server passing the
//     session's CurrentProviderID / CurrentModelID.
//   - messages is the session's current message history projected to
//     provider.Message shape.
//
// Returns:
//   - JSON body of contextUsagePayload (no `type` field — the SSE
//     writer / JSON serialiser at the api boundary injects that),
//     OR an empty string when hasUsage=false.
//   - hasUsage=false when no token counter is wired or the resolved
//     limit is zero. The api server suppresses the event in that case
//     rather than emitting a malformed chunk.
//
// Side effects:
//   - None beyond JSON marshalling.
func (e *Engine) ContextUsageJSONForSession(providerID, modelID string, messages []provider.Message) (string, bool) {
	// Build the tool-schema slice from the current manifest so the
	// per-tool overhead in the estimate matches what a fresh send
	// would carry. The pre-send emission already pays this cost via
	// buildToolSchemasCtx; reuse the same surface for cadence parity.
	tools := e.ToolSchemas()
	return e.buildContextUsagePayload(providerID, modelID, messages, tools, 0)
}

// estimateRequestTokens approximates the prompt-token count for req
// using the engine's configured tokenCounter. The estimate sums every
// message's Content + Thinking and adds a small fixed budget for tool-
// schema overhead per Tool. It is intentionally conservative; the gate
// is allowed to over-fire (refuse a marginally-fitting request) but
// must not under-fire (let an over-budget request through).
//
// Expected:
//   - req is non-nil. The engine's tokenCounter is non-nil (caller
//     guards this).
//
// Returns:
//   - The estimated input-token count.
//
// Side effects:
//   - None.
func (e *Engine) estimateRequestTokens(req *provider.ChatRequest) int {
	const perToolOverhead = 32
	total := 0
	for _, m := range req.Messages {
		if m.Content != "" {
			total += e.tokenCounter.Count(m.Content)
		}
		if m.Thinking != "" {
			total += e.tokenCounter.Count(m.Thinking)
		}
		for _, tc := range m.ToolCalls {
			total += e.tokenCounter.Count(tc.Name)
			for k, v := range tc.Arguments {
				total += e.tokenCounter.Count(k)
				if s, ok := v.(string); ok {
					total += e.tokenCounter.Count(s)
				}
			}
		}
	}
	for _, t := range req.Tools {
		total += e.tokenCounter.Count(t.Name) + e.tokenCounter.Count(t.Description) + perToolOverhead
	}
	return total
}

// overflowRefusalChannel returns a closed-after-first-chunk channel
// carrying a single Done error chunk wrapping pErr. The synthetic-
// channel shape lets the proactive gate surface refusal through the
// same processStreamChunks → outChan → SSE consumer path as any other
// upstream error, so callers see the saturation as a stream_critical
// event without skipping the chunk-level retry / classification logic.
//
// Expected:
//   - pErr is the structured *provider.Error built by
//     checkContextWindowOverflow. Non-nil.
//
// Returns:
//   - A buffered channel containing exactly one chunk: {Error: pErr,
//     Done: true}, then closed.
//
// Side effects:
//   - None beyond channel allocation.
func (e *Engine) overflowRefusalChannel(pErr *provider.Error) <-chan provider.StreamChunk {
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{
		Error: pErr,
		Done:  true,
	}
	close(ch)
	return ch
}

// baseStreamHandler returns the base handler function for streaming chat requests.
//
// Returns:
//   - A hook.HandlerFunc that delegates to the failback chain or direct chat provider.
//
// Side effects:
//   - None.
//
// Expected: parameters for baseStreamHandler.
func (e *Engine) baseStreamHandler() hook.HandlerFunc {
	return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		if req.Provider != "" && e.providerRegistry != nil {
			p, err := e.providerRegistry.Get(req.Provider)
			if err == nil {
				return p.Stream(ctx, *req)
			}
		}
		if e.chatProvider != nil {
			return e.chatProvider.Stream(ctx, *req)
		}
		return nil, errors.New("no provider available: configure either ChatProvider or FailoverManager")
	}
}
