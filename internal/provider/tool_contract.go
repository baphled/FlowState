package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

func validIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-", c) {
			return false
		}
	}
	return true
}

func validToolName(name string) bool {
	return validIdentifier(strings.ReplaceAll(name, ".", "_"))
}

func validObject(value json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

// Validate rejects malformed identifiers or Arguments that are not JSON objects.
// It does not validate arguments against an advertised tool's schema.
func (call ToolCall) Validate() error {
	if !validIdentifier(call.ID) || !validToolName(call.Name) {
		return fmt.Errorf("provider: invalid tool call ID or name")
	}
	if !validObject(call.Arguments) {
		return fmt.Errorf("provider: tool arguments must be a JSON object")
	}
	return nil
}

// Validate checks tool definitions and ordered history before adapter translation.
// Call IDs must be globally unique within this request. Each tool result must
// follow and consume one assistant call; unresolved calls are permitted. Legacy
// role/content-only messages remain valid. No tools are executed or schemas applied.
func (req ChatRequest) Validate() error {
	names := make(map[string]bool)
	for _, tool := range req.Tools {
		if !validToolName(tool.Name) || names[tool.Name] || !validObject(tool.Parameters) {
			return fmt.Errorf("provider: invalid or duplicate tool definition")
		}
		names[tool.Name] = true
	}
	seen, pending := make(map[string]bool), make(map[string]bool)
	for _, message := range req.Messages {
		if err := validateMessage(message, seen, pending); err != nil {
			return err
		}
	}
	return nil
}

func validateMessage(message Message, seen, pending map[string]bool) error {
	if len(message.ToolCalls) > 0 && message.Role != "assistant" {
		return fmt.Errorf("provider: calls require assistant role")
	}
	if message.ToolCallID != "" && message.Role != "tool" {
		return fmt.Errorf("provider: result ID requires tool role")
	}
	for _, call := range message.ToolCalls {
		if err := call.Validate(); err != nil {
			return err
		}
		if seen[call.ID] {
			return fmt.Errorf("provider: duplicate tool call ID %q", call.ID)
		}
		seen[call.ID], pending[call.ID] = true, true
	}
	if message.Role == "tool" {
		if !validIdentifier(message.ToolCallID) || !pending[message.ToolCallID] {
			return fmt.Errorf("provider: unmatched tool result ID %q", message.ToolCallID)
		}
		delete(pending, message.ToolCallID)
	}
	return nil
}

// Accept rejects malformed event payloads, duplicate IDs and events after a
// terminal event. Complete calls are recorded only after validation succeeds.
// Legacy zero-kind content/error events are accepted; legacy errors are terminal.
func (validator *StreamValidator) Accept(event StreamEvent) error {
	if validator.terminal {
		return fmt.Errorf("provider: event after terminal event")
	}
	if err := validateEvent(event); err != nil {
		return err
	}
	if event.ToolCall != nil {
		if validator.ids[event.ToolCall.ID] {
			return fmt.Errorf("provider: duplicate tool call ID %q", event.ToolCall.ID)
		}
		if validator.ids == nil {
			validator.ids = make(map[string]bool)
		}
		validator.ids[event.ToolCall.ID] = true
	}
	validator.terminal = event.Err != nil || event.Kind == StreamEventFinish
	return nil
}

func validateEvent(event StreamEvent) error {
	payload := 0
	if event.Content != "" {
		payload |= 1
	}
	if event.Err != nil {
		payload |= 2
	}
	if event.ToolCall != nil {
		payload |= 4
	}
	if event.FinishReason != "" {
		payload |= 8
	}
	switch event.Kind {
	case "":
		if payload < 4 {
			return nil
		}
	case StreamEventText:
		if payload <= 1 {
			return nil
		}
	case StreamEventToolCall:
		if payload == 4 {
			return event.ToolCall.Validate()
		}
	case StreamEventFinish:
		if payload == 8 {
			return nil
		}
	case StreamEventError:
		if payload == 2 {
			return nil
		}
	}
	return fmt.Errorf("provider: invalid stream event kind or payload")
}
