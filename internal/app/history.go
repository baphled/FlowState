package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// ErrInvalidHistory rejects malformed, ambiguous or incomplete conversation rows.
var ErrInvalidHistory = errors.New("app: invalid or incomplete history")

// ErrHistoryLimit reports a nonpositive limit or a hard projection cap overflow.
var ErrHistoryLimit = errors.New("app: history limit exceeded")

// LoadSessionHistory verifies the existing store journal with Replay, then builds
// independently owned provider messages under positive hard limits. Any error
// returns no partial history and preserves Replay error identity. This read-only
// loader grants no session admission, retry or permission to execute tools.
// Unknown event types fail closed; callers must handle new journal vocabularies.
func LoadSessionHistory(ctx context.Context, st *Store, sessionID string, limits HistoryLimits) ([]provider.Message, error) {
	events, err := st.Replay(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}
	return replaySessionHistory(events, limits)
}

func replaySessionHistory(events []store.RecordedEvent, limits HistoryLimits) ([]provider.Message, error) {
	if limits.MaxEvents <= 0 || limits.MaxMessages <= 0 || limits.MaxBytes < 2 || limits.MaxResultBytes <= 0 || len(events) > limits.MaxEvents {
		return nil, ErrHistoryLimit
	}
	h := historyState{limits: limits, messages: make([]provider.Message, 0), pending: make(map[string]historyCall), seen: make(map[string]bool), bytes: 2}
	for _, event := range events {
		if err := h.accept(event); err != nil {
			return nil, fmt.Errorf("history seq %d: %w", event.Seq, err)
		}
	}
	if len(h.pending) != 0 || h.streaming {
		return nil, ErrInvalidHistory
	}
	if err := (provider.ChatRequest{Messages: h.messages}).Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidHistory, err)
	}
	return h.messages, nil
}

func (h *historyState) accept(event store.RecordedEvent) error {
	switch event.Type {
	case TypeUserMessage, TypeAssistantMessage:
		return h.message(event)
	case tool.TypeToolCall, tool.TypeToolResult:
		return h.toolEvent(event)
	case TypeAssistantDelta:
		var payload historyMessagePayload
		if historyDecode(event.PayloadJSON, &payload) != nil || payload.Delta == nil || len(h.pending) != 0 || !historyAssistantActor(event.Actor) {
			return ErrInvalidHistory
		}
		h.streaming = true
	case "turn.start", TypeTurnEnd, TypeProviderError, "session.cancelled", "session.paused", "session.resumed", "turn.checkpoint":
		// Metadata never clears pending calls or an unfinished assistant stream.
	default:
		return ErrInvalidHistory
	}
	return nil
}

func (h *historyState) message(event store.RecordedEvent) error {
	var payload historyMessagePayload
	if len(h.pending) != 0 || historyDecode(event.PayloadJSON, &payload) != nil || payload.Content == nil {
		return ErrInvalidHistory
	}
	role := "assistant"
	if event.Type == TypeUserMessage {
		role = "user"
		if h.streaming || event.Actor != "user" || len(payload.ToolCalls) != 0 {
			return ErrInvalidHistory
		}
	} else if !historyAssistantActor(event.Actor) {
		return ErrInvalidHistory
	}
	for _, call := range payload.ToolCalls {
		if call.Validate() != nil || h.seen[call.ID] {
			return ErrInvalidHistory
		}
		h.seen[call.ID] = true
		h.pending[call.ID] = historyCall{call: call}
	}
	h.streaming = false
	return h.add(provider.Message{Role: role, Content: *payload.Content, ToolCalls: payload.ToolCalls})
}

func historyAssistantActor(actor string) bool {
	return actor == "engine" || (strings.HasPrefix(actor, "provider:") && len(actor) > len("provider:"))
}

func (h *historyState) toolEvent(event store.RecordedEvent) error {
	var payload historyToolPayload
	if historyDecode(event.PayloadJSON, &payload) != nil {
		return ErrInvalidHistory
	}
	if payload.ID == nil {
		if len(h.pending) != 0 || h.streaming {
			return ErrInvalidHistory
		}
		return nil // Manual CLI entries cannot synthesize provider calls/results.
	}
	var id string
	if json.Unmarshal(payload.ID, &id) != nil || id == "" {
		return ErrInvalidHistory
	}
	call, exists := h.pending[id]
	if !exists || payload.Tool != call.call.Name || event.Actor != tool.ActorPrefix+call.call.Name {
		return fmt.Errorf("%w: call %q", ErrInvalidHistory, id)
	}
	if event.Type == tool.TypeToolCall {
		return h.startTool(id, payload, call)
	}
	if !validHistoryResult(payload, call.started) {
		return ErrInvalidHistory
	}
	content, err := toolResultContent(json.RawMessage(event.PayloadJSON), h.limits.MaxResultBytes)
	if err != nil {
		return err
	}
	delete(h.pending, id)
	return h.add(provider.Message{Role: "tool", ToolCallID: id, Content: content})
}

func (h *historyState) startTool(id string, payload historyToolPayload, call historyCall) error {
	if call.started || !sameHistoryArguments(payload.Args, call.call.Arguments) || payload.OK != nil || payload.Result != nil || payload.Error != nil {
		return ErrInvalidHistory
	}
	call.started = true
	h.pending[id] = call
	return nil
}

func validHistoryResult(payload historyToolPayload, started bool) bool {
	if payload.OK == nil || payload.Args != nil {
		return false
	}
	if *payload.OK {
		return started && payload.Result != nil && payload.Error == nil
	}
	var failure struct {
		Code    string  `json:"code"`
		Message *string `json:"message"`
	}
	return payload.Result == nil && historyDecode(string(payload.Error), &failure) == nil && failure.Code != "" && failure.Message != nil && (started || failure.Code == "tool_denied")
}

func (h *historyState) add(message provider.Message) error {
	encoded, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("%w: message encoding", ErrInvalidHistory)
	}
	nextBytes := h.bytes + len(encoded)
	if len(h.messages) > 0 {
		nextBytes++
	}
	if len(h.messages) >= h.limits.MaxMessages || nextBytes > h.limits.MaxBytes {
		return ErrHistoryLimit
	}
	h.bytes = nextBytes
	h.messages = append(h.messages, message)
	return nil
}

func historyDecode(raw string, target any) error {
	if !utf8.ValidString(raw) || !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return ErrInvalidHistory
	}
	return json.Unmarshal([]byte(raw), target)
}

func sameHistoryArguments(left, right json.RawMessage) bool {
	var a, b bytes.Buffer
	return json.Compact(&a, left) == nil && json.Compact(&b, right) == nil && bytes.Equal(a.Bytes(), b.Bytes())
}
