package app

import (
	"cmp"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/skill"
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
		h.loaded = ""
		return h.message(event)
	case tool.TypeToolCall, tool.TypeToolResult:
		return h.toolEvent(event)
	case TypeSkillLoaded:
		return h.skillLoaded(event)
	case TypeAssistantDelta:
		var payload historyMessagePayload
		if historyDecode(event.PayloadJSON, &payload) != nil || payload.Delta == nil || len(h.pending) != 0 || !historyAssistantActor(event.Actor) {
			return ErrInvalidHistory
		}
		h.loaded = ""
		h.streaming = true
	case "turn.start", TypeTurnEnd, TypeProviderError, "session.cancelled", "session.paused", "session.resumed", "turn.checkpoint",
		"hooks.loaded", "hook.invoked", "hook.result":
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
	h.loaded = ""
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
	content, err := historyResultText(event.PayloadJSON, h.limits.MaxResultBytes)
	if err != nil {
		return err
	}
	delete(h.pending, id)
	if call.call.Name == tool.SkillLoadName && *payload.OK {
		h.loaded, h.loadedFrame = id, ""                   // the one call a skill.loaded may follow, bound to the result the model received
		_ = json.Unmarshal(payload.Result, &h.loadedFrame) // a result that is not a frame leaves nothing to bind the record to
	}
	return h.add(provider.Message{Role: "tool", ToolCallID: id, Content: content})
}

// historyResultText is the tool message content of a result event: the shared hookDenialText for a
// hook denial, so LoadSessionHistory agrees with the rebuild, else the bounded payload JSON. Both
// are held to maxBytes.
func historyResultText(payloadJSON string, maxBytes int) (string, error) {
	if text, denied := hookDenialText(payloadJSON); denied {
		if len(text) > maxBytes {
			return "", ErrHistoryLimit // as toolResultContent, when even the bounded form does not fit
		}
		return text, nil
	}
	return toolResultContent(json.RawMessage(payloadJSON), maxBytes)
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

// skillLoaded accepts the skill.loaded record of the skill.load call whose successful result it follows: engine actor,
// that call id, exactly the name, lowercase sha256 and file of the frame the model received in that result (so the record
// cannot claim bytes the model never got), a size of at most skill.MaxFileBytes, no unknown field, and the file's path under
// the skill (a Windows device word is a valid name: such a directory loads on Unix); anything else fails closed (ADR-013 D9).
func (h *historyState) skillLoaded(event store.RecordedEvent) error {
	var payload historySkillPayload
	dec := json.NewDecoder(strings.NewReader(event.PayloadJSON)) // strict where historyDecode is lenient: the record has a fixed shape
	dec.DisallowUnknownFields()
	if h.loaded == "" || event.Actor != session.ActorEngine || historyDecode(event.PayloadJSON, &payload) != nil || dec.Decode(&payload) != nil {
		return ErrInvalidHistory
	}
	sum, hexerr := hex.DecodeString(payload.SHA256)
	file := cmp.Or(payload.File, skill.FileName)
	if payload.ToolCallID != h.loaded || hexerr != nil || len(sum) != 32 || hex.EncodeToString(sum) != payload.SHA256 || // the frame's form: lowercase
		!skill.ValidName(payload.Name) || payload.Bytes <= 0 || payload.Bytes > skill.MaxFileBytes || payload.Path != payload.Name+"/"+file ||
		skill.ValidSupportPath(file) != nil || !strings.HasPrefix(h.loadedFrame, tool.SkillFrameHeader(payload.Name, payload.SHA256, payload.File)+"\n") {
		return ErrInvalidHistory
	}
	h.loaded = ""
	return nil
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
	a, errA := canonicalJSON(left)
	b, errB := canonicalJSON(right)
	return errA == nil && errB == nil && a == b
}
