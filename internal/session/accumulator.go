package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/baphled/flowstate/internal/provider"
	tooldisplay "github.com/baphled/flowstate/internal/tool/display"
)

// MessageAppender is a narrow write-back interface for appending session messages.
//
// Expected:
//   - sessionID identifies the target session.
//   - msg is the message to append.
//
// Side effects:
//   - Implementations are expected to persist msg to the session identified by sessionID.
type MessageAppender interface {
	// AppendMessage persists msg to the session identified by sessionID.
	AppendMessage(sessionID string, msg Message)
	// UpdateDelegation locates the most recent message in the session matching
	// chainID and applies mutate to it. Implementations must be a no-op when no
	// matching message is found.
	UpdateDelegation(sessionID, chainID string, mutate func(*Message))
}

// TurnMessageRecorder is the accumulator-to-Turn-registry seam from
// Phase 1 of the "Turn-Based Post-Then-Poll Architecture (May 2026)"
// plan. The Dispatcher constructs a recorder closure that wraps
// turn.Registry.Append and threads it through the streamCtx via
// WithTurnRecorder; the accumulator pulls it from ctx and calls
// RecordTurnMessage at every persistence site (assistant, thinking,
// tool_call, tool_result, delegation_started, delegation).
//
// The function-typed interface keeps `internal/session` free of any
// `internal/turn` import — turn imports session (for Message), so a
// reverse import would cycle. Defining the seam here means the
// Dispatcher (which already imports both) owns the wiring; the
// accumulator only knows about the function signature.
//
// Expected:
//   - turnID identifies the live Turn the message belongs to. Empty
//     turnID is a no-op (the accumulator skips the call entirely
//     when ctx carries no turn_id, so this branch is defensive).
//   - msg is the engine-emitted message being persisted (assistant,
//     thinking, tool_call, tool_result, delegation_started,
//     delegation). User-message rows are NEVER passed here — they
//     belong to the POST snapshot, not Turn.MessagesAdded.
//
// Side effects:
//   - Implementations append the message to the Turn registry's
//     per-turn MessagesAdded slice.
type TurnMessageRecorder func(turnID string, msg Message)

// turnRecorderCtxKey is the unexported context-key type for the
// TurnMessageRecorder seam. Mirrors the unexported-zero-sized-type
// convention from internal/turn's turnIDKey.
type turnRecorderCtxKey struct{}

// turnIDCtxKey is the unexported context-key type for the turn id
// the accumulator reads off the streamCtx. Kept symmetric with
// internal/turn.WithTurnID — but defined here too so the accumulator
// can read the value without importing internal/turn (which would
// cycle: turn imports session for Message). The Dispatcher writes
// to BOTH keys (turn.WithTurnID + session.WithAccumulatorTurnID) so
// both consumers see the same id.
type turnIDCtxKey struct{}

// WithTurnRecorder returns ctx carrying the supplied recorder. The
// Dispatcher calls this once at DispatchSessioned entry; the
// accumulator extracts the recorder from the threaded ctx and calls
// it at each persistence site.
//
// Expected:
//   - parent is the streamCtx the Dispatcher is about to hand to the
//     session manager.
//   - rec is the recorder closure; nil-tolerant — TurnRecorderFromContext
//     returns (nil, false) when no recorder was set.
//
// Returns:
//   - A derived ctx carrying the recorder under turnRecorderCtxKey{}.
//
// Side effects:
//   - None.
func WithTurnRecorder(parent context.Context, rec TurnMessageRecorder) context.Context {
	return context.WithValue(parent, turnRecorderCtxKey{}, rec)
}

// TurnRecorderFromContext extracts the recorder threaded via
// WithTurnRecorder. Returns (nil, false) when no recorder is set OR
// when a nil recorder was stored (treated as absent for symmetry
// with the no-value path).
func TurnRecorderFromContext(ctx context.Context) (TurnMessageRecorder, bool) {
	if ctx == nil {
		return nil, false
	}
	v := ctx.Value(turnRecorderCtxKey{})
	if v == nil {
		return nil, false
	}
	rec, _ := v.(TurnMessageRecorder)
	if rec == nil {
		return nil, false
	}
	return rec, true
}

// WithAccumulatorTurnID returns ctx carrying the supplied turn id.
// The Dispatcher writes BOTH this key AND internal/turn's turnIDKey
// so consumers in both packages can read the value without taking
// the cycling import. The accumulator reads this key.
func WithAccumulatorTurnID(parent context.Context, id string) context.Context {
	return context.WithValue(parent, turnIDCtxKey{}, id)
}

// AccumulatorTurnIDFromContext extracts the turn id stored under
// turnIDCtxKey{}. Returns ("", false) when absent or empty.
func AccumulatorTurnIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v := ctx.Value(turnIDCtxKey{})
	if v == nil {
		return "", false
	}
	id, _ := v.(string)
	if id == "" {
		return "", false
	}
	return id, true
}

// turnAwareAppender wraps a MessageAppender so AppendMessage calls
// ALSO fire the TurnMessageRecorder when ctx carries one. Decoupling
// the wrap from the accumulator's inner switch keeps the existing
// applyChunk / applyToolCall / applyToolResult sites untouched — they
// continue to call appender.AppendMessage, the wrap fans out to both
// the session sink AND the turn registry.
//
// The wrap is set up inside AccumulateStream once per turn (cheap
// closure capture of turnID + recorder from ctx) so no per-call
// ctx-read overhead lands on the hot persistence path.
type turnAwareAppender struct {
	inner    MessageAppender
	turnID   string
	recorder TurnMessageRecorder
}

func (t *turnAwareAppender) AppendMessage(sessionID string, msg Message) {
	// Pre-assign id + timestamp so the Turn.MessagesAdded record
	// agrees with the session-stored copy. The inner appender used to
	// silently overwrite both fields with its own values (taking msg
	// by value), so this wrap recorded the pre-assignment empty-id /
	// zero-time msg to the Turn registry. Pre-assigning here AND
	// teaching the inner appender (manager.go::appendSessionMessage)
	// to be id-preserving makes both views agree.
	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	t.inner.AppendMessage(sessionID, msg)
	if t.turnID != "" && t.recorder != nil {
		t.recorder(t.turnID, msg)
	}
}

func (t *turnAwareAppender) UpdateDelegation(sessionID, chainID string, mutate func(*Message)) {
	// Capture the post-mutate snapshot so the Turn registry sees the
	// live state of the delegation row, not the frozen
	// delegation_started copy that landed at AppendMessage time.
	// Wrap the caller's mutate to intercept the mutated *Message after
	// the inner appender has located it and applied the mutation; this
	// avoids guessing the chain-row identity from outside the manager.
	var snapshot Message
	var captured bool
	t.inner.UpdateDelegation(sessionID, chainID, func(m *Message) {
		mutate(m)
		if m != nil {
			snapshot = *m
			captured = true
		}
	})
	// Fan out to the Turn registry recorder. Without this, the
	// long-poll Turn endpoint (sole live channel post-248345d1)
	// freezes on the delegation_started snapshot and the in-thread
	// delegation card never flips to its terminal state. The recorder
	// is id-keyed in Registry.Append, so repeated UpdateDelegation
	// calls for the same Message.ID collapse onto a single
	// MessagesAdded slot.
	if captured && t.turnID != "" && t.recorder != nil && snapshot.ID != "" {
		t.recorder(t.turnID, snapshot)
	}
}

// AppendMessage appends a message to the session identified by sessionID.
//
// Expected:
//   - sessionID identifies an existing session in the Manager.
//   - msg contains the message to append (ID and Timestamp are assigned internally).
//
// Returns:
//   - None.
//
// Side effects:
//   - Acquires the Manager lock and appends msg to the session's Messages slice.
//   - Does nothing when sessionID is not found.
func (m *Manager) AppendMessage(sessionID string, msg Message) {
	m.appendSessionMessage(sessionID, msg)
}

// UpdateDelegation locates the most recent message in the named session whose
// ChainID matches chainID and applies mutate to it under the Manager lock,
// re-persisting the session afterwards.
//
// Expected:
//   - sessionID identifies an existing session.
//   - chainID matches the ChainID of an existing in-flight delegation message.
//   - mutate is non-nil and applies the desired changes to the matched message.
//
// Side effects:
//   - Acquires the Manager lock, mutates the message in place, and re-persists
//     the session. No-op when the session or matching message is absent.
func (m *Manager) UpdateDelegation(sessionID, chainID string, mutate func(*Message)) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return
	}
	var snapshot *Session
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].ChainID == chainID {
			mutate(&sess.Messages[i])
			sessionsDir := m.sessionsDir
			persistFn := m.persistFn
			if sessionsDir != "" {
				snap := *sess
				msgs := make([]Message, len(sess.Messages))
				copy(msgs, sess.Messages)
				snap.Messages = msgs
				snapshot = &snap
			}
			m.mu.Unlock()
			if snapshot != nil {
				fn := persistFn
				if fn == nil {
					fn = PersistSession
				}
				_ = fn(sessionsDir, snapshot)
			}
			return
		}
	}
	m.mu.Unlock()
}

// streamAccumState holds mutable accumulation state within AccumulateStream.
//
// The lastModelID / lastProviderID fields track the most recent
// (model, provider) pair stamped by the engine on the StreamChunk
// stream (see engine.go where chunk.ModelID = e.LastModel() and
// chunk.ProviderID = e.LastProvider()).
// The flushContent / flushThinking helpers copy these onto the
// appended assistant / thinking messages so each persisted turn
// carries its provenance — the chip and any future per-bubble badge
// can attribute the turn to the model that actually produced it,
// even after a session reload or a failover replay that switched
// providers mid-turn.
type streamAccumState struct {
	sessionID         string
	agentID           string
	contentBuf        strings.Builder
	thinkingBuf       strings.Builder
	lastToolName      string
	lastToolInput     string
	lastModelID       string
	lastProviderID    string
	seenStartedChains map[string]struct{}
	// pendingThinkingSignature holds the signature received with the
	// most recent thinking chunk. The Anthropic streaming layer emits
	// the thinking content and its associated signature as one chunk
	// (StreamChunk{Thinking, Signature}); the accumulator pairs them
	// into a ThinkingBlock entry below. This is reset after the
	// thinking message flushes so the next thinking block on the
	// same turn captures its own signature.
	pendingThinkingSignature string
	// thinkingBlocks accumulates the structured per-block thinking
	// records (signed and redacted) produced during the current turn.
	// Persisted on the assistant message at flushContent so a session
	// reload can replay them verbatim on the next turn — without
	// them Anthropic silently disables extended thinking continuity.
	thinkingBlocks []provider.ThinkingBlock
	// turnStopReason is the upstream stop reason captured from the
	// `message_delta` chunk, persisted on the assistant message at
	// flushContent.
	turnStopReason string
	// turnHadToolCall records whether a tool_call chunk was observed in
	// the current turn. The synthesizePlaceholderAssistant path at
	// end-of-turn consults this flag together with
	// providerProducesUnifiedAssistant(s.lastProviderID): on providers
	// that natively pack assistant content + tool_use into one wire
	// message (Anthropic), a tool_call already carries the turn's
	// assistant artefact and a synthesised placeholder would
	// double-stamp the persisted history. On providers whose wire
	// format separates `reasoning_content` from `content` and emits
	// tool_calls without re-stamping the assistant body
	// (OpenAI-compat: zai, openai, copilot, ollama), the placeholder
	// is the only thing that carries the turn's accumulated
	// ThinkingBlocks — without it the full reasoning chain across a
	// multi-round tool loop is dropped from persisted history and the
	// chat UI renders thinking → gap → tool widget with no closing
	// assistant turn. See bug-fix note "Tool-bearing turn coverage
	// (delivered May 2026)" for the full forensic trace.
	turnHadToolCall bool
	// turnPlaceholderEmitted (Streaming Coherence Slice C, May 2026) —
	// prevents synthesizePlaceholderAssistant from double-firing within
	// the same turn. Both the chunk.Done path and the channel-close
	// path call the synthesizer; without this gate a true-empty Done
	// followed by channel close would persist two empty-turn placeholders.
	turnPlaceholderEmitted bool
	// turnHadDelegation (Streaming Coherence Slice C, May 2026) —
	// records whether a delegation chunk was observed this turn. The
	// empty-turn placeholder must not fire when the turn produced a
	// delegation_started / delegation message; that artefact is the
	// turn's deliverable.
	turnHadDelegation bool
}

// providerProducesUnifiedAssistant reports whether the named provider's
// wire format packs assistant content (and reasoning, when applicable)
// together with tool_use into a single assistant message per round.
//
// Anthropic returns true: a single `messages.create` response carries
// `content_block`s for text, thinking, and tool_use as siblings under one
// assistant message. The session accumulator's `applyToolCall` already
// emits the tool_call as the turn's visible artefact; synthesising an
// extra empty-content assistant placeholder would change the persisted
// history shape for every reasoning-capable Anthropic turn that uses
// tools.
//
// Every other registered provider returns false. The OpenAI-compat layer
// (zai, openai, copilot, ollama, ollamacloud, openzen) routes
// `reasoning_content` to the Thinking channel separately from Content
// and tool_calls — so a thinking-only turn ending in a tool_call is
// genuinely missing its assistant placeholder unless the accumulator
// synthesises one. Without synthesis, multi-round tool loops accumulate
// ThinkingBlocks across every round and then drop them at end-of-turn,
// producing the user-visible "agent returns empty response after tool
// use" symptom captured in session 089c7cd5-37d8-4a59-868d-366d2dca0cfb.
//
// The function is keyed on the engine-stamped chunk.ProviderID (the
// stable string ID returned by Provider.Name()), not the Provider
// interface, so the accumulator stays consumer-agnostic and does not
// import any provider package. New providers default to false (opt-in
// to the unified-assistant suppression) — safer than the inverse, since
// the synthesis only fires when the turn produced reasoning but no
// content, and at worst attaches an extra placeholder that the UI is
// already designed to render.
//
// This predicate gates ONLY the synthesizePlaceholderAssistant
// double-stamp suppression below — a wire-shape (reasoning-channel)
// concern. The tool_use_no_calls stamp guard uses a SEPARATE predicate,
// providerToolUseFinishIsTrustworthy, because that is a TRUST concern
// (does the provider's tool-use finish always carry a real call) that
// resolves to a different provider set. Keeping them apart prevents the
// trust allowlist from silently changing placeholder-synthesis behaviour.
func providerProducesUnifiedAssistant(providerID string) bool {
	return providerID == "anthropic"
}

// providerToolUseFinishIsTrustworthy reports whether the named provider's
// finish_reason="tool_use" (mapped to stop_reason="tool_use") is reliable:
// when this provider reports a tool-use finish, a real tool_call block
// always accompanies the turn. For such a provider a flushed content row
// carrying stop_reason="tool_use" with zero inline tool_call data is a
// healthy unified-assistant wrap-up (the tool_use landed as a separate
// Role:"tool_call" row, or an earlier round's delegation/tool call was the
// real artefact), NOT a wire-contract violation — so the
// tool_use_no_calls detector at flushContent MUST NOT fire on it.
//
// Wire mechanics, identical across every trusted member. On a HEALTHY
// content+tool turn the provider emits the tool_call chunk(s) BEFORE the
// terminal stop_reason chunk:
//   - Anthropic: `content_block_stop` emits the tool_call
//     (anthropic/streaming.go:345) and the terminal `message_delta` carries
//     stop_reason (anthropic/streaming.go:176) — message_delta is last.
//   - OpenAI-compat: RunStream emits tool_call chunks as they finish AND
//     flushes accumulated tool_calls before mirroring finish_reason into
//     the stop_reason chunk (openaicompat.go:548-588);
//     finish_reason="tool_calls" maps to stop_reason="tool_use"
//     (openaicompat.go:842-843).
// Because the tool_call chunk precedes the stop_reason chunk, the
// accumulator's `applyToolCall` flushes the in-progress content row while
// turnStopReason is still "" — so a healthy turn's content row carries
// StopReason="" by construction and the detector never sees it. The row
// that DOES carry stop_reason="tool_use" is a swarm-lead wrap-up emitted
// AFTER the real tool/delegation work — on a trustworthy provider that
// finish reflects real earlier work, so it must NOT be stamped. Live
// evidence: anthropic session 271080ed and the gpt-4o failover that
// regressed the same way.
//
// anthropic + openai are trusted; every other provider returns false. This
// is NOT a wire-packing distinction (the openai-family wire is structurally
// identical) but a TRUST distinction: glm-5/glm-4.6 (served via zai, and
// reachable via openzen/ollamacloud) genuinely VIOLATE the contract — the
// model announces a tool, the provider reports finish_reason="tool_calls",
// and glm emits ZERO tool_call blocks anywhere in the turn (live dogfood
// sessions 8169ca2d, 32ab2e60). That produces the identical persisted shape
// (content row, stop_reason="tool_use", no tool_call) as the healthy
// wrap-up, so the provider ID is the only signal that separates a
// trustworthy finish (anthropic, openai) from a lying model (glm via zai).
// zai therefore stays OUT so the genuine Bug-G violation is still caught.
// The remaining openaicompat providers (openzen, ollamacloud, ollama,
// github-copilot) stay OUT conservatively — they can serve the same
// unreliable glm-class models, and "default to false" keeps catching real
// violations until a provider is proven trustworthy. Extending the set is
// safe and additive once a false-positive on that provider is observed.
func providerToolUseFinishIsTrustworthy(providerID string) bool {
	switch providerID {
	case "anthropic", "openai":
		return true
	default:
		return false
	}
}

// AccumulateStream wraps rawCh with a goroutine that records assistant and tool
// messages into the appender while forwarding every chunk to the returned channel.
//
// Expected:
//   - ctx bounds the lifetime of the accumulator goroutine so a cancelled
//     stream (e.g. the user pressing Esc twice mid-response) stops
//     persisting and closes the forwarded channel promptly rather than
//     waiting for rawCh to drain. Callers with no cancellation need should
//     pass context.Background().
//   - appender is a valid MessageAppender used to persist accumulated messages.
//   - sessionID and agentID are valid identifiers for the active session.
//   - rawCh is the stream channel containing provider chunks.
//
// Returns:
//   - A new channel that receives the same chunks as rawCh.
//
// Side effects:
//   - Spawns a goroutine that appends messages via appender.AppendMessage.
//   - The returned channel is closed once rawCh is fully consumed OR ctx
//     is cancelled, whichever happens first. On ctx cancellation the
//     flushThinking / flushContent finalisers still run so any
//     already-accumulated partial content is not lost.
func AccumulateStream(
	ctx context.Context,
	appender MessageAppender,
	sessionID, agentID string,
	rawCh <-chan provider.StreamChunk,
) <-chan provider.StreamChunk {
	// Phase 1 — Turn-Based Post-Then-Poll Architecture (May 2026).
	// If the streamCtx carries a turn_id + a TurnMessageRecorder (the
	// Dispatcher injects both at DispatchSessioned entry), wrap the
	// appender so every AppendMessage call ALSO records into the Turn
	// registry's MessagesAdded slice for this turn. Absent either
	// value (CLI/orchestrator callers that don't drive sessioned
	// dispatch, accumulator unit tests), the wrap is skipped entirely
	// and the appender is used verbatim — zero behavioural change
	// for non-sessioned callers.
	if turnID, ok := AccumulatorTurnIDFromContext(ctx); ok {
		if rec, recOK := TurnRecorderFromContext(ctx); recOK {
			appender = &turnAwareAppender{
				inner:    appender,
				turnID:   turnID,
				recorder: rec,
			}
		}
	}
	accumCh := make(chan provider.StreamChunk, 64)
	go func() {
		defer close(accumCh)
		s := &streamAccumState{sessionID: sessionID, agentID: agentID}
		// P1/D2: ctx-aware select so a cancelled streaming turn does not
		// park the accumulator goroutine forever on a rawCh whose
		// producer has already abandoned the stream. Mirrors the D1 fix
		// in readNextChunk at the chat-intent layer.
		for {
			select {
			case chunk, ok := <-rawCh:
				if !ok {
					flushThinking(appender, s)
					flushContent(appender, s)
					// Symmetric with the chunk.Done and ctx.Done() paths
					// below: when a turn produced reasoning only and the
					// upstream channel closes without ever emitting a
					// terminal Done chunk, still synthesise the
					// placeholder assistant Message so the persisted
					// history holds the thinking blocks under an
					// assistant turn (and not as a stranded Role:
					// "thinking" with no enclosing assistant). The
					// OpenAI-compat layer only emits Done when
					// FinishReason != "" (see
					// internal/provider/openaicompat/openaicompat.go:229-232),
					// so a thinking-only turn from a reasoning provider
					// (zai/glm-4.6, DeepSeek-R1) hits this branch in
					// production whenever the engine's own Done
					// injection is bypassed (direct accumulator callers,
					// future regressions). The synthesis is gated on
					// the same predicates as the Done path —
					// content-only and thinking-then-tool-call turns are
					// untouched.
					synthesizePlaceholderAssistant(appender, s)
					return
				}
				applyChunk(appender, s, chunk)
				// Forward the chunk, but do not park on the accumCh send
				// past ctx.Done() — otherwise a slow consumer plus a
				// cancelled ctx would still deadlock.
				select {
				case accumCh <- chunk:
				case <-ctx.Done():
					flushThinking(appender, s)
					flushContent(appender, s)
					return
				}
			case <-ctx.Done():
				flushThinking(appender, s)
				flushContent(appender, s)
				synthesizePlaceholderAssistant(appender, s)
				return
			}
		}
	}()
	return accumCh
}

// applyChunk processes one stream chunk, updating accumulation state and persisting messages.
//
// Expected:
//   - appender is the message sink.
//   - s holds the current accumulation state.
//   - chunk is the next chunk from the raw stream.
//
// Returns:
//   - None.
//
// Side effects:
//   - May call appender.AppendMessage to persist accumulated content.
//   - Mutates s.contentBuf, s.thinkingBuf, s.lastToolName, and s.lastToolInput.
func applyChunk(appender MessageAppender, s *streamAccumState, chunk provider.StreamChunk) {
	// Track the latest engine-stamped (model, provider) so flushContent /
	// flushThinking can persist the pair on the assistant message without
	// having to plumb the engine through the accumulator. We update on EVERY
	// chunk that carries a non-empty value so a mid-stream failover (the
	// engine restamps subsequent chunks with the new provider/model after the
	// failover hook has switched candidates) is reflected in the message
	// stamped at the end of the turn.
	if chunk.ModelID != "" {
		s.lastModelID = chunk.ModelID
	}
	if chunk.ProviderID != "" {
		s.lastProviderID = chunk.ProviderID
	}
	switch {
	case chunk.ToolCall != nil:
		applyToolCall(appender, s, chunk.ToolCall)
	case chunk.ToolResult != nil:
		applyToolResult(appender, s, chunk.ToolResult)
	case chunk.DelegationInfo != nil:
		applyDelegation(appender, s, chunk.DelegationInfo)
	case chunk.RedactedThinking != "":
		// Redacted thinking arrives as a single chunk (no deltas).
		// Capture as an opaque thinking block so the next turn replays
		// the encrypted payload verbatim. Anthropic requires this to
		// preserve thinking-continuity guarantees.
		s.thinkingBlocks = append(s.thinkingBlocks, provider.ThinkingBlock{
			Redacted: true,
			Data:     chunk.RedactedThinking,
		})
	case chunk.EventType == "stop_reason":
		// Captured from `message_delta`. Stamp the turn-level stop
		// reason so the subsequent flushContent persists it on the
		// assistant message.
		if chunk.StopReason != "" {
			s.turnStopReason = chunk.StopReason
		}
	case chunk.EventType == "usage":
		// Usage chunks carry no message content — token-accounting
		// snapshots are intentionally not persisted on the message
		// here. Downstream consumers (telemetry, the future
		// per-bubble usage badge) read them from the live stream.
		return
	case chunk.Done:
		flushThinking(appender, s)
		flushContent(appender, s)
		synthesizePlaceholderAssistant(appender, s)
	default:
		if chunk.EventType != "" {
			return
		}
		applyThinkingAndContent(appender, s, chunk)
	}
}

// applyThinkingAndContent handles the default chunk shape (text content
// and/or thinking content). Thinking blocks are persisted at block
// boundaries so multiple thinking blocks in one turn are recorded
// independently rather than concatenated:
//
//   - When a chunk carries Signature, it is the terminal chunk of a
//     thinking block (the Anthropic streaming layer emits exactly one
//     such chunk per content_block_stop). The buffer is flushed to a
//     visible thinking Message and a structured ThinkingBlock is
//     appended for round-trip.
//   - When a chunk carries thinking text but no signature, accumulate
//     into the buffer — this is a legacy/test-only shape; the buffer is
//     drained by flushThinking at Done time.
func applyThinkingAndContent(
	appender MessageAppender, s *streamAccumState, chunk provider.StreamChunk,
) {
	if chunk.Thinking != "" || chunk.Signature != "" || chunk.Content != "" {
		// Streaming Coherence Slice C — fresh turn signal. Any non-Done
		// chunk that carries content / thinking opens a new turn so the
		// per-turn placeholder gate must be reset.
		s.turnPlaceholderEmitted = false
	}
	if chunk.Thinking != "" || chunk.Signature != "" {
		s.thinkingBuf.WriteString(chunk.Thinking)
		if chunk.Signature != "" {
			// Block boundary: emit the visible thinking Message and
			// stage the structured block for the next-turn replay.
			s.pendingThinkingSignature = chunk.Signature
			flushThinking(appender, s)
		}
	}
	if chunk.Content != "" {
		s.contentBuf.WriteString(chunk.Content)
	}
}

// applyToolCall flushes pending content, then stores a tool_call message.
//
// Expected:
//   - appender is the message sink.
//   - s holds the current accumulation state.
//   - tc is the non-nil tool call chunk to record.
//
// Side effects:
//   - Calls flushThinking and flushContent before appending the tool_call message.
//   - Updates s.lastToolName and s.lastToolInput for later use by applyToolResult.
func applyToolCall(appender MessageAppender, s *streamAccumState, tc *provider.ToolCall) {
	// Fabricated-completion guard ordering: the per-turn flag MUST be
	// set BEFORE flushContent so that a content-then-tool turn (the model
	// announced what it was about to do, then issued a real tool_call) is
	// recognised as legitimate by the guard at flushContent. Without this
	// reorder, an announcement carrying completion-language (e.g. "✅
	// about to save to /tmp/x") would be false-flagged because
	// turnHadToolCall is still false at the point flushContent runs.
	s.turnHadToolCall = true
	flushThinking(appender, s)
	flushContent(appender, s)
	input := toolArgValue(tc.Name, tc.Arguments)
	appender.AppendMessage(s.sessionID, Message{
		Role:      "tool_call",
		Content:   tc.Name,
		ToolName:  tc.Name,
		ToolInput: input,
		AgentID:   s.agentID,
	})
	s.lastToolName = tc.Name
	s.lastToolInput = input
	// Streaming Coherence Slice C — a tool round is a fresh turn signal;
	// reset the per-turn placeholder gate.
	s.turnPlaceholderEmitted = false
}

// applyToolResult stores a tool_result or tool_error message.
//
// Expected:
//   - appender is the message sink.
//   - s holds the current accumulation state, including the preceding tool name and input.
//   - tr is the non-nil tool result chunk to record.
//
// Side effects:
//   - Appends a tool_result message (or tool_error on tr.IsError) via appender.AppendMessage.
func applyToolResult(appender MessageAppender, s *streamAccumState, tr *provider.ToolResultInfo) {
	role := "tool_result"
	if tr.IsError {
		role = "tool_error"
	}
	appender.AppendMessage(s.sessionID, Message{
		Role:      role,
		Content:   tr.Content,
		ToolName:  s.lastToolName,
		ToolInput: s.lastToolInput,
		AgentID:   s.agentID,
	})
}

// applyDelegation stores or updates a delegation message reflecting the lifecycle.
//
// Expected:
//   - appender is the message sink.
//   - s holds the current accumulation state.
//   - info is the non-nil delegation info chunk to evaluate.
//
// Side effects:
//   - On the first in-flight ("started", "running", "in_progress") chunk for a
//     given ChainID, appends a "delegation_started" message carrying the
//     structured target/model/tool fields.
//   - On subsequent in-flight chunks for the same ChainID, mutates the existing
//     message in place to refresh ToolCalls, LastTool, Status, ModelName, and
//     the rendered Content summary.
//   - On terminal status ("completed" or "failed") for an in-flight ChainID,
//     mutates the existing message in place flipping Role to "delegation".
//   - On terminal status with no prior in-flight message, appends a fresh
//     "delegation" message.
//   - Does nothing for any other status value.
func applyDelegation(appender MessageAppender, s *streamAccumState, info *provider.DelegationInfo) {
	// Streaming Coherence Slice C — record any delegation activity so
	// the empty-turn synthesiser does not fire on a delegation-bearing
	// turn (the delegation card is the deliverable).
	s.turnHadDelegation = true
	s.turnPlaceholderEmitted = false
	switch info.Status {
	case "started", "running", "in_progress":
		if s.seenStartedChains == nil {
			s.seenStartedChains = make(map[string]struct{})
		}
		key := info.ChainID
		if key == "" {
			key = info.TargetAgent
		}
		if _, seen := s.seenStartedChains[key]; seen {
			appender.UpdateDelegation(s.sessionID, key, func(m *Message) {
				applyDelegationFields(m, info)
			})
			return
		}
		s.seenStartedChains[key] = struct{}{}
		msg := Message{
			Role:    "delegation_started",
			Content: formatDelegationSummary(info),
			AgentID: s.agentID,
			ChainID: key,
		}
		applyDelegationFields(&msg, info)
		appender.AppendMessage(s.sessionID, msg)
	case "completed", "failed":
		key := info.ChainID
		if key == "" {
			key = info.TargetAgent
		}
		if s.seenStartedChains != nil {
			if _, seen := s.seenStartedChains[key]; seen {
				appender.UpdateDelegation(s.sessionID, key, func(m *Message) {
					m.Role = "delegation"
					applyDelegationFields(m, info)
				})
				delete(s.seenStartedChains, key)
				return
			}
		}
		msg := Message{
			Role:    "delegation",
			Content: formatDelegationSummary(info),
			AgentID: s.agentID,
			ChainID: key,
		}
		applyDelegationFields(&msg, info)
		appender.AppendMessage(s.sessionID, msg)
	}
}

// applyDelegationFields copies structured progress fields from info onto m and
// refreshes the human-readable Content summary.
func applyDelegationFields(m *Message, info *provider.DelegationInfo) {
	m.TargetAgent = info.TargetAgent
	m.Status = info.Status
	m.ModelName = info.ModelName
	m.ToolCalls = info.ToolCalls
	m.LastTool = info.LastTool
	m.Content = formatDelegationSummary(info)
}

// flushThinking writes accumulated thinking content as a thinking message and resets the buffer.
//
// Expected:
//   - appender is the message sink.
//   - s holds the current accumulation state with a possibly non-empty thinkingBuf.
//
// Side effects:
//   - Appends a thinking message via appender.AppendMessage and resets s.thinkingBuf.
//   - Does nothing when s.thinkingBuf is empty or whitespace-only.
//
// Whitespace gate (production bug 2026-05-11, req_011Cavnk52Fbsfes8zWumAcm):
// Anthropic rejects HTTP 400 invalid_request_error "each thinking block
// must contain non-whitespace thinking" for any thinking block whose
// text is empty or whitespace-only on a replay turn. A whitespace-only
// thinking buffer carries no information for the UI either — emitting
// a blank thinking bubble is noise. The buffer is reset and the pending
// signature is dropped so the (now-orphan) signature does not survive
// onto a later block; the matching serialisation-layer gate in
// anthropic.buildThinkingBlocks is the belt-and-braces backstop.
func flushThinking(appender MessageAppender, s *streamAccumState) {
	if s.thinkingBuf.Len() == 0 {
		return
	}
	thinking := s.thinkingBuf.String()
	if strings.TrimSpace(thinking) == "" {
		// Reset state so the orphan signature does not bleed into a
		// subsequent block. The block is discarded entirely — neither
		// the visible "thinking" message nor the structured
		// ThinkingBlock entry is written.
		s.thinkingBuf.Reset()
		s.pendingThinkingSignature = ""
		return
	}
	signature := s.pendingThinkingSignature
	appender.AppendMessage(s.sessionID, Message{
		Role:    "thinking",
		Content: thinking,
		AgentID: s.agentID,
	})
	// Capture the signed thinking block for replay on the next turn.
	// Without round-tripping the signature, Anthropic disables thinking
	// continuity silently — see provider.Message.ThinkingBlocks.
	s.thinkingBlocks = append(s.thinkingBlocks, provider.ThinkingBlock{
		Thinking:  thinking,
		Signature: signature,
	})
	s.thinkingBuf.Reset()
	s.pendingThinkingSignature = ""
}

// flushContent writes accumulated assistant content as an assistant message and resets the buffer.
//
// Expected:
//   - appender is the message sink.
//   - s holds the current accumulation state with a possibly non-empty contentBuf.
//
// Side effects:
//   - Appends an assistant message via appender.AppendMessage and resets s.contentBuf.
//   - Does nothing when s.contentBuf is empty.
func flushContent(appender MessageAppender, s *streamAccumState) {
	if s.contentBuf.Len() == 0 {
		return
	}
	// Stamp the (model, provider) pair seen on the most recent engine-tagged
	// chunk so each persisted assistant turn carries its provenance. When the
	// stream produced no model/provider at all (legacy providers, test
	// streams), the fields stay empty — Message.ModelName / ProviderName are
	// `omitempty` so the wire and on-disk JSON remain stable.
	//
	// ThinkingBlocks and StopReason are also stamped here so a session
	// reload reconstructs everything needed to replay extended-thinking
	// continuity on the next turn (Anthropic silently disables thinking
	// without the original signatures and redacted payloads).
	msg := Message{
		Role:         "assistant",
		Content:      s.contentBuf.String(),
		AgentID:      s.agentID,
		ModelName:    s.lastModelID,
		ProviderName: s.lastProviderID,
		StopReason:   s.turnStopReason,
	}
	if len(s.thinkingBlocks) > 0 {
		// Copy the slice so subsequent mutation of s.thinkingBlocks
		// (e.g. a follow-up turn within the same accumulator instance)
		// cannot retro-edit the persisted message.
		blocks := make([]provider.ThinkingBlock, len(s.thinkingBlocks))
		copy(blocks, s.thinkingBlocks)
		msg.ThinkingBlocks = blocks
	}
	// Fabricated-completion guard (May 2026). When a content-bearing
	// turn matches the documented fabrication signature AND the turn
	// produced no tool_call AND no delegation, stamp the synthetic
	// StopReasonFabricatedCompletion so downstream consumers (chat UI,
	// audit, future blocking logic) can flag a self-reported file
	// operation that has no qualifying evidence on the wire.
	//
	// Live reproducer: glm-5.1 coordinator turns emitted
	// "✅ Master bug report written to: ~/vaults/..." twice in a row
	// with ZERO preceding write/bash/delegate tool calls. The guard
	// only sets StopReason — content flows verbatim — so the existing
	// flushContent persistence shape is preserved and the guard is
	// purely additive metadata. The upstream stop_reason is preserved
	// when the content does NOT match the signature.
	if !s.turnHadToolCall && !s.turnHadDelegation && matchesFabricationSignature(msg.Content) {
		msg.StopReason = StopReasonFabricatedCompletion
	}
	// Abandoned-tool guard (Bug E, May 2026). When a content-bearing turn
	// is whitespace-only AND carries reasoning AND produced NO tool_call
	// AND NO delegation, the model committed to a tool call in the
	// reasoning channel but never emitted it. The persisted assistant
	// would otherwise carry a stray newline as content with an empty
	// StopReason — the chat UI sees a "completed" turn with no work,
	// no soft-error affordance, and the user's request is silently
	// dropped. Stamp StopReasonAbandonedTool so the UI / audit can flag
	// the abandoned plan; the thinking content survives on the message
	// so the user can see what the model intended to do.
	//
	// Live reproducer: child session 3fcb56df-8224-485c-9187-2aaad9ed5879
	// (glm-4.5 executor on zai). Sequence was thinking → content="\n"
	// → done; no tool_call ever emitted despite the reasoning channel
	// containing a fully-formed "Let me use the write tool" plan.
	if !s.turnHadToolCall &&
		!s.turnHadDelegation &&
		len(s.thinkingBlocks) > 0 &&
		strings.TrimSpace(msg.Content) == "" {
		msg.StopReason = StopReasonAbandonedTool
	}
	// Tool-use-no-calls detector (Bug G, May 2026; predicate widened
	// May 2026 after the dogfood session 32ab2e60-a69d-4bfc-9f64-1d5ae0734b39
	// repro). When the upstream stop_reason is "tool_use" on a
	// flushContent-emitted assistant message, the provider violated its
	// own wire contract — the "tool_use" finish_reason claims a
	// tool_call block will accompany the message and zero such blocks
	// landed on THIS message. The accumulator stamps
	// StopReasonToolUseNoCalls so the session manager's status-flip
	// path marks the session failed and the chat UI / audit can flag
	// the contract violation rather than rendering a silent "completed"
	// bubble whose content body only announced intent.
	//
	// Predicate scope is message-local, not turn-local. The original
	// gating also required `!s.turnHadToolCall && !s.turnHadDelegation`,
	// which silently excused a contract violation on the FINAL response
	// of any turn that had performed earlier tool-call or delegation
	// work. Dogfood session 32ab2e60 caught the planner-on-glm-5 path
	// where an earlier @plan-writer delegation succeeded, then the
	// wrap-up assistant message emitted stop_reason="tool_use" with
	// zero tool_call blocks — the over-narrow turn-scope check skipped
	// the detector and the user saw status=active on a malformed final
	// response. The wire contract is per-message: the assistant message
	// carrying stop_reason="tool_use" must itself be accompanied by a
	// tool_call block. Earlier tool-call or delegation activity is
	// orthogonal and does not excuse a broken final response.
	//
	// By construction, a flushContent-emitted assistant message never
	// carries inline tool_call data — Message.ToolCalls is a count summary
	// and is always zero when flushContent builds msg above. Tool calls
	// are persisted as separate Role:"tool_call" messages emitted by
	// applyToolCall, which calls flushContent FIRST with a still-empty
	// turnStopReason — so a healthy turn (content → tool_call →
	// stop_reason → Done) flushes the content message with StopReason=""
	// at applyToolCall time and the Done-time flushContent on an empty
	// buffer is a no-op. The "tool_use" stop_reason only lands on a
	// persisted assistant message when it arrived before any subsequent
	// tool_call — which IS the contract-violation shape.
	//
	// Ordering: this guard fires AFTER FabricatedCompletion and
	// AbandonedTool because their signatures are more specific
	// (self-reported file-op phrases or whitespace-only content with
	// thinking) and SHOULD claim ownership on overlap. Both stamp a
	// non-"tool_use" StopReason before this guard, so the literal
	// match below naturally yields ownership to them. The
	// StreamTruncated detector below predicates on `msg.StopReason ==
	// ""` so it is naturally mutually exclusive with this guard.
	// Independence preserved across the four sentinels: each owns a
	// distinct stop_reason key.
	//
	// Live reproducers:
	//   - session 8169ca2d-5536-41af-b947-ba3fd7514416 (original Bug G,
	//     no prior tool work this turn) — plan-writer agent on glm-5/zai
	//     emitted Content="Now I'll generate the full plan..." with
	//     stop_reason="tool_use" and zero tool_calls.
	//   - session 32ab2e60-a69d-4bfc-9f64-1d5ae0734b39 (predicate-widen
	//     repro, turn HAD earlier delegations) — planner agent on glm-5
	//     delegated to plan-reviewer + plan-writer earlier in the turn,
	//     then its wrap-up message emitted stop_reason="tool_use" with
	//     toolCalls=None. The over-narrow turn-scope gate skipped the
	//     detector and the session stayed active.
	//
	// Provider discrimination (May 2026): the detector must NOT fire on a
	// provider whose tool_use finish is TRUSTWORTHY (Anthropic, OpenAI).
	// There a healthy content+tool_use turn legitimately carries
	// stop_reason="tool_use" while the real tool_use landed as a separate
	// Role:"tool_call" row (or an earlier round's delegation/tool call was
	// the real artefact) — so this flushed content row has zero inline
	// tool_call data by construction (see the flushContent note above and
	// the wire-ordering proof in providerToolUseFinishIsTrustworthy) and is
	// NOT a wire-contract violation. Without this guard a fully-successful
	// Anthropic swarm lead (plan published, final message end_turn) was
	// falsely stamped tool_use_no_calls and latched to status=failed (live
	// session 271080ed); the SAME false-positive then regressed when a
	// planning-loop lead failed over to openai/gpt-4o. For providers whose
	// tool_use finish is NOT trustworthy (zai/glm, which announce a tool
	// then emit zero calls — sessions 8169ca2d, 32ab2e60) the per-message
	// wire contract still holds and the genuine Bug-G detection is preserved.
	if msg.StopReason == "tool_use" && !providerToolUseFinishIsTrustworthy(s.lastProviderID) {
		msg.StopReason = StopReasonToolUseNoCalls
	}
	// Stream-truncation detector (Bug F, May 2026). When a content-bearing
	// turn produced meaningful payload AND produced NO tool_call AND NO
	// delegation AND the upstream provider gave NO stop_reason chunk,
	// the openaicompat wire cut mid-output before the model reached its
	// terminal payload. Stamp StopReasonStreamTruncated so the session
	// manager can flip Status active -> failed and the chat UI / audit
	// can surface the failure immediately rather than waiting on the
	// 30-min boot-time orphan reap.
	//
	// Ordering: the AbandonedTool guard above fires FIRST because its
	// signature (whitespace-only content + thinking) is mutually exclusive
	// with this detector's "meaningful content" gate — that pre-existing
	// sentinel keeps its sole-owner status on its specific shape. The
	// FabricatedCompletion guard above also pre-empts when its phrases
	// match, so a self-reported "✅ saved to ..." turn that lacked an
	// upstream stop_reason still surfaces as fabrication (the dominant
	// signal) rather than truncation.
	//
	// Live reproducer: session 5e37d947-c049-4d5c-a209-658d9c6a5186 —
	// plan-writer on glm-5/zai. Final assistant Content was "Let me
	// generate the comprehensive revision:" (154 chars), Thinking=2074,
	// ToolCalls=0, StopReason="". The model announced intent and the
	// stream cut before the planned tool_use payload landed.
	if msg.StopReason == "" &&
		!s.turnHadToolCall &&
		!s.turnHadDelegation &&
		strings.TrimSpace(msg.Content) != "" {
		msg.StopReason = StopReasonStreamTruncated
	}
	appender.AppendMessage(s.sessionID, msg)
	s.contentBuf.Reset()
	s.thinkingBlocks = nil
	s.turnStopReason = ""
	// Streaming Coherence Slice C — content-bearing turn does its own
	// emit; mark the per-turn placeholder slot as filled so the
	// subsequent synthesizer call (chunk.Done after flushContent) is a
	// no-op rather than emitting an empty-turn placeholder beside the
	// just-flushed content.
	s.turnPlaceholderEmitted = true
}

// StopReasonThinkingOnly is the synthetic stop reason stamped on a
// placeholder assistant Message when a turn produced reasoning without
// an upstream `stop_reason` chunk to inherit. It exists because the Vue
// UI affordance for thinking-only degraded turns (commit 0f27ac98) keys
// on `stopReason !== ""` to distinguish a degraded turn from a still-
// streaming bubble; without a non-empty value the existing render
// branch sees nothing to anchor on and the bubble stays blank.
//
// Reasoning providers vary on whether they emit a structured
// `stop_reason` event before Done. On glm-4.5/glm-4.6 via zai, the
// provider can finish a turn after emitting only `reasoning_content`
// tokens — no content, no tool_call, no stop_reason — and the
// accumulator has no upstream value to copy onto the synthesised
// placeholder. Stamping a synthetic "thinking_only" value here keeps
// the UI invariant from 0f27ac98 intact without touching the Vue
// render branch and without inventing a real stop_reason that would
// confuse downstream consumers (telemetry, model-side replay).
//
// See `Bug Fixes/Empty-Content Thinking-Only Assistant Turn (May 2026)`
// in the FlowState vault for the forensic trace; reproducer session
// 718b5d51-f01b-45f0-80bb-31329a9d44e7.
const StopReasonThinkingOnly = "thinking_only"

// StopReasonEmptyTurn is the synthetic stop reason stamped on a placeholder
// assistant Message when a turn produced no content, no thinking, and no
// tool calls — a true empty turn (Streaming Coherence Slice C, May 2026).
// Pre-slice such turns persisted no assistant artefact at all and the UI's
// in-flight assistant bubble was left running with empty content; the user
// observed "no answer, indicator stuck" until the watchdog tripped 60s
// later. The placeholder makes the silence visible: the chat UI renders a
// soft-error affordance (mirroring `thinking_only`) so the user knows the
// turn ended without producing output and can compose a follow-up rather
// than wait for nothing.
//
// Wire-format-stable: the value is read by the Vue `MessageBubble`
// render branch, not by any backend consumer.
const StopReasonEmptyTurn = "empty_turn"

// StopReasonFabricatedCompletion is the synthetic stop reason stamped on
// a content-bearing assistant Message when the turn matches the
// documented fabrication signature (self-reported file operation phrases
// like "✅", "written to", "persisted to", "saved to", "created the file")
// AND the same turn produced NO tool_call AND NO delegation. The model
// has self-reported a completed file operation it never performed;
// stamping the message lets downstream consumers (chat UI, audit log,
// future blocking logic) distinguish a fabricated completion claim from
// a real one.
//
// Live reproducer: glm-5.1 acting as coordinator emitted assistant
// turns containing "✅ Master bug report written to: ~/vaults/..." twice
// in a row with ZERO preceding write/bash/delegate tool calls. Nothing
// was saved. The guard is content-additive only — Message.Content flows
// verbatim and only Message.StopReason is set. The guard fires only at
// the content-bearing flushContent path; the thinking-only and
// empty-turn synthesisers continue to set their own sentinels.
//
// Wire-format-stable: the value is read by the Vue MessageBubble render
// branch and the audit pipeline; no backend consumer keys on the
// specific string.
const StopReasonFabricatedCompletion = "fabricated_completion"

// StopReasonAbandonedTool is the synthetic stop reason stamped on a
// content-bearing assistant Message whose content is whitespace-only AND
// whose thinking is non-empty AND that produced NO tool_call AND NO
// delegation. The model committed to a tool call in the reasoning channel
// but never emitted it; the wire-level finish_reason claims completion
// but the turn produced no actual work. Stamping the message lets
// downstream consumers (chat UI, audit log) distinguish an abandoned-tool
// turn from a legitimately empty answer, and the thinking blocks survive
// on the persisted message so the user can see the abandoned plan and
// re-issue.
//
// Live reproducer: child session 3fcb56df-8224-485c-9187-2aaad9ed5879
// (glm-4.5 executor under coordinator on zai). The model's reasoning
// channel said "Let me use the write tool to create this file" but the
// content channel emitted only "\n" and no structured tool_call followed.
// Pre-guard, the persisted assistant carried Content="\n", StopReason="",
// and the chat UI rendered a silent "completed" bubble — the user's
// "write the report" request was dropped without any signal that the
// model had failed to act.
//
// Wire-format-stable: the value is read by the Vue MessageBubble render
// branch and the audit pipeline; no backend consumer keys on the
// specific string.
const StopReasonAbandonedTool = "abandoned_tool"

// StopReasonStreamTruncated is the synthetic stop reason stamped on an
// assistant Message whose turn produced ANY model activity (content OR
// thinking) AND produced NO tool_call AND NO delegation AND the upstream
// provider gave NO stop_reason chunk. The signature is the openaicompat
// truncation pattern: the model announced intent (in content or thinking)
// then the wire cut before a terminal finish_reason landed, leaving the
// session's chat history with a persisted assistant turn that carried
// StopReason="" and the session metadata's Status stuck on "active"
// indefinitely — the boot-time orphan reap (30 min grace) was the only
// recovery path.
//
// Distinct from StopReasonAbandonedTool: that sentinel keys on
// whitespace-only CONTENT with thinking (model self-cancelled by emitting
// a stray newline alongside a complete reasoning chain). Truncation is a
// wire-level cut MID-OUTPUT — the model never reached its terminal
// payload, the upstream finish_reason was never emitted, and openaicompat
// at openaicompat.go:540-547 (`if sawFinish`) consequently never emitted
// Done. The accumulator's channel-close branch flushes whatever partial
// content / thinking accumulated and synthesises the placeholder with
// this sentinel so the session manager at manager.go:appendSessionMessage
// can flip Status active -> failed and surface the failure.
//
// Live reproducers (read-only — referenced for forensic context):
//   - Session 8779c2ae-69a4-... — plan-writer on glm-4.6/zai. Final
//     assistant: Content="", Thinking=30382, ToolCalls=0, StopReason="".
//   - Session 5e37d947-c049-4d5c-a209-658d9c6a5186 — plan-writer on
//     glm-5/zai. Final assistant: Content=154 ("Let me generate the
//     comprehensive revision:"), Thinking=2074, ToolCalls=0, StopReason="".
//
// False-positive surface: a synthetic test fixture that sends a raw Done
// chunk WITHOUT a preceding stop_reason chunk also lands here — that
// shape is not producible in production by the openaicompat provider
// (which only emits Done when finish_reason was seen). The detector
// treats those fixtures as truncation by design; see the existing
// "preserves the empty-turn sentinel and routes empty-stop-reason
// thinking-only through the stream-truncation detector" pin in
// accumulator_test.go.
//
// Wire-format-stable: the value is read by the Vue MessageBubble render
// branch (which keys on `stopReason !== ""` to surface a soft-error
// affordance — see web/src/components/chat/MessageBubble.vue:239) and by
// the session manager's status-flip path. The string literal is the
// surface — downstream consumers must match exactly.
//
// Operator-visible behaviour after this lands: when glm truncates a
// plan-writer dispatch, the session's Status flips from "active" to
// "failed" the moment the placeholder lands, the chat UI renders a
// soft-error affordance immediately, and the audit pipeline sees a
// non-empty stopReason instead of a silent stuck-active row.
const StopReasonStreamTruncated = "stream_truncated"

// StopReasonToolUseNoCalls is the synthetic stop reason stamped on a
// content-bearing assistant Message whose upstream stop_reason claims
// `tool_use` (the wire contract: "the response MUST include tool_call
// blocks") AND that produced NO tool_call AND NO delegation. The
// provider lied about the wire contract — it announced tool_use as the
// finish reason and emitted zero tool calls. Stamping the message lets
// downstream consumers (chat UI, audit log, session manager status-flip
// path) distinguish a provider contract violation from a clean turn
// and surface the failure immediately.
//
// Distinct from the three sibling detectors:
//   - StopReasonAbandonedTool: requires WHITESPACE-only content AND
//     non-empty thinking; the model self-cancelled in the reasoning
//     channel. This sentinel fires on NON-EMPTY content (the model
//     emitted prose narrating intent) with ANY thinking state.
//   - StopReasonStreamTruncated: requires EMPTY upstream stop_reason;
//     the wire cut before a terminal finish_reason arrived. This
//     sentinel fires when stop_reason IS present and IS specifically
//     `tool_use` — the wire reached its terminal signal but the signal
//     itself is a contract lie.
//   - StopReasonFabricatedCompletion: requires content matching the
//     self-reported-file-op signature ("✅", "written to", etc). This
//     sentinel keys on the upstream stop_reason value, not on content
//     phrasing — so it catches contract violations whose content does
//     not happen to match the fabrication phrase set.
//
// Live reproducer: session 8169ca2d-5536-41af-b947-ba3fd7514416 —
// plan-writer agent on glm-5/zai. Final assistant message (msg[263])
// carried Content="Now I'll generate the full plan. Let me compose
// the Expanded OMO plan document:", ToolCalls=0, and the upstream
// stop_reason was "tool_use". The plan was never written; the session
// status was "completed" with no signal to the user that the turn
// silently failed the provider contract.
//
// Wire-format-stable: the value is read by the Vue MessageBubble
// render branch (which keys on `stopReason !== ""` to surface a
// soft-error affordance — see web/src/components/chat/MessageBubble.vue:239)
// and by the session manager's status-flip path
// (manager.go:appendSessionMessage). The string literal is the
// surface — downstream consumers must match exactly.
//
// Operator-visible behaviour after this lands: when a glm-family
// provider announces tool_use without emitting tool calls, the
// session's Status flips from "active" to "failed" the moment the
// placeholder lands, the chat UI renders a soft-error affordance
// immediately, and the audit pipeline sees `stop_reason:
// "tool_use_no_calls"` instead of a silent "completed" row.
const StopReasonToolUseNoCalls = "tool_use_no_calls"

// fabricationPhrases is the set of self-reported-completion phrases that
// trigger the StopReasonFabricatedCompletion stamp when present in an
// assistant turn that produced no tool_call and no delegation. The set
// matches what glm-5.1 actually emitted in the live reproducer. Add to
// this list cautiously — the precision/recall trade-off is intentional;
// the guard is meant to flag SELF-REPORTED FILE OPERATIONS, not generic
// affirmations.
var fabricationPhrases = []string{
	"✅",
	"written to",
	"persisted to",
	"saved to",
	"created the file",
}

// matchesFabricationSignature reports whether content matches any phrase
// in fabricationPhrases (case-insensitive for ASCII phrases; the literal
// ✅ glyph is compared byte-for-byte since case-folding does not apply).
func matchesFabricationSignature(content string) bool {
	if content == "" {
		return false
	}
	// Compare the lowercased content against the ASCII phrases. The
	// non-ASCII glyph ✅ is checked against the original content so the
	// case-folding is a no-op for it.
	lower := strings.ToLower(content)
	for _, phrase := range fabricationPhrases {
		if phrase == "✅" {
			if strings.Contains(content, phrase) {
				return true
			}
			continue
		}
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// StopReasonTurnInterrupted is the synthetic stop reason stamped on a
// placeholder assistant Message when a tool-bearing turn ends due to a
// provider stream closure (network drop, timeout, or provider failure).
// The tool calls are persisted as-is, allowing the chat UI to render
// them with an interrupted state indicator and the user to retry or
// branch. Without this synthetic Done, the session has no assistant
// artefact and the chat UI's in-flight bubble hangs until the 60s
// watchdog trips (session 1776011172813458779, May 2026).
//
// Wire-format-stable: the value is read by the Vue `MessageBubble`
// render branch, not by any backend consumer.
const StopReasonTurnInterrupted = "turn_interrupted"

// StopReasonToolLoopExceeded is the synthetic stop reason stamped on the
// terminal Done chunk when the engine's tool loop hits a cap — either the
// repeat-call detector (the same tool name + canonicalised arguments
// recurring across consecutive continuations) or the absolute iteration
// backstop. It bounds the unbounded re-request hang where a provider
// re-emits the same (often tool-not-found) call every continuation,
// spinning the loop forever (15,404 iterations observed before the cap).
//
// Wire-format-stable and non-empty: the value is read by the Vue
// `MessageBubble` soft-error render branch, which keys on
// `stopReason !== ""`. A blank value would collapse back into the
// indistinguishable-from-success path.
const StopReasonToolLoopExceeded = "tool_loop_exceeded"

// synthesizePlaceholderAssistant emits an empty-content assistant message
// carrying the accumulated thinking blocks when a turn produced reasoning
// without an enclosing assistant artefact.
//
// Background: OpenAI-compat reasoning providers (zai/glm-4.6, DeepSeek-R1)
// finish a turn after emitting only `reasoning_content` tokens, with no
// content delta. The same providers, mid-tool-loop, emit
// `thinking + tool_call` rounds back-to-back where ThinkingBlocks
// accumulate across every round but never co-attach to a content-bearing
// assistant message (their wire shape splits reasoning from content).
// Without this synthesis, flushContent's empty-contentBuf early-return
// leaves the persisted session with a free-floating thinking message
// (or, worse, a multi-round chain of thinking+tool_call rows) and no
// enclosing `Role: "assistant"` turn — the chat UI sees nothing to
// render and the user reports "the agent returned an empty response
// after tool use". The placeholder makes the turn renderable: the UI
// gets one assistant message per turn carrying the full reasoning
// chain, matching the harness pattern other tools converge on.
//
// Expected:
//   - appender is the message sink.
//   - s holds the current accumulation state, immediately after
//     flushThinking + flushContent have run.
//
// Side effects:
//   - Appends an assistant Message with empty Content, the accumulated
//     thinking blocks, and the turn's stop_reason / model / provider
//     stamps when:
//     1. contentBuf is empty (flushContent emitted nothing this turn), AND
//     2. thinkingBlocks is non-empty (the model emitted reasoning), AND
//     3. NOT (turnHadToolCall && providerProducesUnifiedAssistant(s.lastProviderID)) —
//     the suppression only applies to providers whose wire format
//     natively packs assistant content + tool_use into one message
//     (Anthropic). On every other provider, a tool-bearing turn that
//     produced reasoning STILL needs the placeholder to carry the
//     accumulated ThinkingBlocks across the persisted history. This
//     reverses the over-aggressive blanket tool-call gate
//     introduced alongside the original synthesis fix
//     (commit f918bb9f) — see bug-fix note for the forensic trace.
//   - The persisted Message ALWAYS carries a non-empty StopReason: the
//     upstream `turnStopReason` when one was captured, otherwise the
//     synthetic StopReasonThinkingOnly fallback so the Vue UI
//     affordance from commit 0f27ac98 (`stopReason !== ""`) can locate
//     the placeholder. A raw-thinking turn from a reasoning provider
//     (glm-4.5 via zai, reproducer session
//     718b5d51-f01b-45f0-80bb-31329a9d44e7 message index 9) finishes
//     without ever emitting a structured `stop_reason` event; without
//     the fallback the placeholder would persist with StopReason=""
//     and the chat UI would render a blank bubble.
//   - Resets s.thinkingBlocks and s.turnStopReason on success.
//   - Does nothing when any of the gates is unmet.
func synthesizePlaceholderAssistant(appender MessageAppender, s *streamAccumState) {
	if s.contentBuf.Len() != 0 {
		return
	}
	// Streaming Coherence Slice C (May 2026) — re-entry guard. Both
	// chunk.Done and channel-close call this synthesizer; emit at most
	// once per turn.
	if s.turnPlaceholderEmitted {
		return
	}
	// Streaming Coherence Slice C (May 2026) — true-empty-turn fall-through.
	//
	// Pre-slice this synthesizer required `len(s.thinkingBlocks) > 0`. A
	// turn that produced nothing — no content, no thinking, no tool call
	// — left the UI's in-flight bubble running until the 60s watchdog
	// tripped. The fall-through below emits a placeholder assistant
	// stamped with `StopReasonEmptyTurn` so the chat UI renders a soft-
	// error affordance immediately on Done.
	//
	// Gating:
	//   - contentBuf empty (existing gate above).
	//   - thinkingBlocks empty (this branch — the prior contract still
	//     handles thinking-only).
	//   - No tool call this turn (`turnHadToolCall == false`). A
	//     tool-bearing turn never warrants an empty-turn placeholder:
	//     the tool-result is the turn's deliverable.
	if len(s.thinkingBlocks) == 0 {
		// Empty-turn placeholder gates: no tools (tool-result is the
		// deliverable), no delegations (delegation card is the
		// deliverable). When neither, the turn is truly empty and
		// warrants an empty_turn placeholder.
		if !s.turnHadToolCall && !s.turnHadDelegation {
			appender.AppendMessage(s.sessionID, Message{
				Role:         "assistant",
				Content:      "",
				AgentID:      s.agentID,
				ModelName:    s.lastModelID,
				ProviderName: s.lastProviderID,
				StopReason:   StopReasonEmptyTurn,
			})
			s.turnStopReason = ""
			s.turnPlaceholderEmitted = true
		}
		return
	}
	// Provider-aware suppression: only providers whose wire format
	// already packs assistant content + tool_use into a single message
	// (Anthropic) get the tool-call gate. Every other provider falls
	// through and synthesises the placeholder so the turn's accumulated
	// ThinkingBlocks are not silently dropped from persisted history.
	if s.turnHadToolCall && providerProducesUnifiedAssistant(s.lastProviderID) {
		return
	}
	// Copy the slice so subsequent mutation of s.thinkingBlocks (a
	// follow-up turn within the same accumulator instance) cannot
	// retro-edit the persisted message.
	blocks := make([]provider.ThinkingBlock, len(s.thinkingBlocks))
	copy(blocks, s.thinkingBlocks)
	stopReason := s.turnStopReason
	if stopReason == "" {
		// Stream-truncation detector (Bug F, May 2026). Pre-detector
		// this branch stamped the synthetic StopReasonThinkingOnly so
		// the Vue UI affordance from 0f27ac98 (which keys on
		// `stopReason !== ""`) could locate the placeholder. Live
		// production data — session 8779c2ae-69a4-... (glm-4.6 plan-
		// writer on zai) had Content="", Thinking=30382, ToolCalls=0,
		// StopReason="" — shows this signature IS the openaicompat
		// truncation pattern: the model emitted reasoning tokens for
		// the full plan, then the upstream wire cut before any
		// terminal finish_reason or tool_use payload landed. The
		// session was left stuck on Status="active" until the 30-min
		// orphan reap.
		//
		// The new sentinel (a) preserves the UI invariant — stop_reason
		// is still non-empty so the soft-error affordance lands — and
		// (b) drives the session manager's flip from active to failed
		// so the failure surfaces immediately. The render branch in
		// the Vue MessageBubble (web/src/components/chat/MessageBubble.vue)
		// already routes non-empty stop_reason values through the
		// soft-error path, so no UI change is required.
		stopReason = StopReasonStreamTruncated
	}
	appender.AppendMessage(s.sessionID, Message{
		Role:           "assistant",
		Content:        "",
		AgentID:        s.agentID,
		ModelName:      s.lastModelID,
		ProviderName:   s.lastProviderID,
		StopReason:     stopReason,
		ThinkingBlocks: blocks,
	})
	s.thinkingBlocks = nil
	s.turnStopReason = ""
	s.turnPlaceholderEmitted = true
}

// formatDelegationSummary builds a human-readable summary of a delegation event.
//
// Expected:
//   - info is the non-nil DelegationInfo to summarise.
//
// Returns:
//   - A newline-joined string containing the target agent, status, optional model name, and tool call count.
//
// Side effects:
//   - None.
func formatDelegationSummary(info *provider.DelegationInfo) string {
	parts := []string{fmt.Sprintf("│ %s [%s]", info.TargetAgent, info.Status)}
	if info.ModelName != "" {
		parts = append(parts, "  Model: "+info.ModelName)
	}
	if info.ToolCalls > 0 {
		toolInfo := fmt.Sprintf("  %d tool calls", info.ToolCalls)
		if info.LastTool != "" {
			toolInfo += fmt.Sprintf(" (last: %s)", info.LastTool)
		}
		parts = append(parts, toolInfo)
	}
	return strings.Join(parts, "\n")
}

// toolArgValue returns the primary display argument value for the given tool call.
//
// Delegates to tooldisplay.PrimaryArgValue, which applies the tiered fallback
// (hand-coded primary key → preferred fallback keys → compact JSON of all
// string args) so unknown tools (delegate, search_nodes, coordination_store,
// MCP tools, etc.) still produce an informative ToolInput rather than an
// empty string. Sensitive args are redacted and long values are truncated.
//
// Expected:
//   - name is a tool identifier.
//   - args contains the tool call argument map.
//
// Returns:
//   - A display string suitable for storage in Message.ToolInput, or an
//     empty string when no informative value can be derived.
//
// Side effects:
//   - None.
func toolArgValue(name string, args map[string]any) string {
	value, _ := tooldisplay.PrimaryArgValue(name, args)
	return value
}
