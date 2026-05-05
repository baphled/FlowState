package session

import (
	"context"
	"fmt"
	"strings"

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
//   - Does nothing when s.thinkingBuf is empty.
func flushThinking(appender MessageAppender, s *streamAccumState) {
	if s.thinkingBuf.Len() == 0 {
		return
	}
	thinking := s.thinkingBuf.String()
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
	appender.AppendMessage(s.sessionID, msg)
	s.contentBuf.Reset()
	s.thinkingBlocks = nil
	s.turnStopReason = ""
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
