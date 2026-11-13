package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// Typed errors for the per-call correlated EventSink adapter. The
// adapter fails closed: an invalid or mismatched call identity is
// rejected before any event reaches persistence, and a persistence
// failure makes the adapter sticky so no unacknowledged outcome can
// later be exposed as acknowledged.
var (
	// ErrInvalidCallEventCall rejects a provider ToolCall that fails
	// provider validation before any event is appended.
	ErrInvalidCallEventCall = errors.New("app: invalid tool call for event correlation")

	// ErrCallEventMismatch rejects an event that does not match the
	// adapter's bound call identity (tool name or actor).
	ErrCallEventMismatch = errors.New("app: event does not match correlated call")

	// ErrCallEventOrder rejects a result event before its call start,
	// or a repeated start for the same call.
	ErrCallEventOrder = errors.New("app: correlated call event order violation")

	// ErrCallEventSticky reports that a previous append already failed;
	// every later Append fails so no unacknowledged outcome is exposed.
	ErrCallEventSticky = errors.New("app: event sink sticky after append failure")
)

// NewCallEventSink builds the per-call correlated EventSink adapter for
// one immutable provider ToolCall. The call must satisfy provider
// validation; otherwise ErrInvalidCallEventCall is returned and no
// event is ever appended. The caller keeps ownership of
// call.Arguments: the adapter normalises a copy and never mutates the
// caller's payload.
func NewCallEventSink(writer tool.EventSink, call provider.ToolCall) (tool.EventSink, error) {
	if err := call.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCallEventCall, err)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, call.Arguments); err != nil {
		return nil, fmt.Errorf("%w: arguments: %v", ErrInvalidCallEventCall, err)
	}
	return &callEventSink{writer: writer, call: call, args: buf.String()}, nil
}

// Append validates and annotates one tool.call or tool.result event for
// the bound call before it reaches the underlying writer. Any identity
// or order violation is rejected before persistence. Once an append
// fails, the adapter is sticky: every later Append returns
// ErrCallEventSticky joined with the original failure, so an
// unacknowledged outcome can never be exposed as acknowledged.
func (s *callEventSink) Append(ctx context.Context, actor, typ string, payload any) (int64, error) {
	if s.sticky != nil {
		return -1, fmt.Errorf("%w: %w", ErrCallEventSticky, s.sticky)
	}
	seq, err := s.appendValidated(ctx, actor, typ, payload)
	if err != nil {
		s.sticky = err
		return -1, err
	}
	return seq, nil
}

func (s *callEventSink) appendValidated(ctx context.Context, actor, typ string, payload any) (int64, error) {
	toolName, fields, ok := callEventPayload(payload)
	if !ok {
		return -1, fmt.Errorf("%w: unexpected payload for %s", ErrCallEventMismatch, typ)
	}
	if toolName != s.call.Name || actor != tool.ActorPrefix+s.call.Name {
		return -1, fmt.Errorf("%w: actor %q tool %q", ErrCallEventMismatch, actor, toolName)
	}
	fields["tool_call_id"] = s.call.ID
	switch typ {
	case tool.TypeToolCall:
		if s.started || s.done {
			return -1, fmt.Errorf("%w: repeated start for %q", ErrCallEventOrder, s.call.ID)
		}
		args, err := normaliseCallArgs(fields["args"])
		if err != nil || args != s.args {
			return -1, fmt.Errorf("%w: arguments for %q", ErrCallEventMismatch, s.call.ID)
		}
		s.started = true
	case tool.TypeToolResult:
		if s.done || isDeniedCallResult(fields) == s.started {
			return -1, fmt.Errorf("%w: result for %q", ErrCallEventOrder, s.call.ID)
		}
		s.done = true
	default:
		return -1, fmt.Errorf("%w: event type %s", ErrCallEventMismatch, typ)
	}
	return s.writer.Append(ctx, actor, typ, fields)
}

// callEventPayload extracts the loop's tool name and a mutable payload copy;
// ok is false when payload is not the loop's map[string]any shape.
func callEventPayload(payload any) (string, map[string]any, bool) {
	fields, ok := payload.(map[string]any)
	if !ok {
		return "", nil, false
	}
	name, ok := fields["tool"].(string)
	if !ok {
		return "", nil, false
	}
	copied := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		copied[key] = value
	}
	return name, copied, true
}

// normaliseCallArgs compacts the annotated args value back to
// canonical JSON so it can be compared byte-for-byte with the bound
// call's arguments.
func normaliseCallArgs(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func isDeniedCallResult(fields map[string]any) bool {
	if ok, _ := fields["ok"].(bool); ok {
		return false
	}
	failure, _ := fields["error"].(map[string]any)
	code, _ := failure["code"].(string)
	return code == "tool_denied"
}
