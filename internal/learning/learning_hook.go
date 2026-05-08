package learning

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// extractCallStack returns the function names from the current call stack.
// It skips internal frames to surface user-meaningful function names.
//
// Expected: None.
//
// Returns:
//   - A slice of function names from the current call stack.
//
// Side effects:
//   - None.
func extractCallStack() []string {
	pcs := make([]uintptr, 10)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var stack []string
	for {
		frame, more := frames.Next()
		if frame.Function != "" {
			parts := strings.Split(frame.Function, "/")
			name := parts[len(parts)-1]
			stack = append(stack, name)
		}
		if !more {
			break
		}
	}
	return stack
}

// Record represents a tool call record for learning purposes.
type Record struct {
	AgentID   string
	ToolsUsed []string
	Outcome   string
}

// ToolCallResult represents the result of a tool call.
type ToolCallResult struct {
	Outcome string
}

// contextKeyType is a type for context keys to avoid collisions with built-in types.
type contextKeyType string

// AgentIDKey is the context key for storing the agent ID.
const AgentIDKey contextKeyType = "AgentID"

// Hook implements the hook logic.
type Hook struct {
	client MemoryClient
}

// NewLearningHook creates a new learning hook with the given memory client.
//
// Expected:
//   - client implements MemoryClient.
//
// Returns:
//   - A Hook that persists learning records through the supplied client.
//
// Side effects:
//   - None.
func NewLearningHook(client MemoryClient) *Hook {
	return &Hook{client: client}
}

// Handle processes a tool call result and persists a learning record.
//
// Expected:
//   - ctx may contain an AgentID value.
//   - result may contain an Outcome value.
//
// Returns:
//   - An error when persisting the learning record fails. The returned error
//     is always safe to format with fmt and slog: any panic from a
//     misbehaving error implementation (for example, a typed-nil error whose
//     Error() method dereferences its receiver) is recovered and converted
//     into a degraded sentinel, so the subscriber goroutine cannot crash.
//
// Side effects:
//   - Writes a learning record via the configured MemoryClient.
func (h *Hook) Handle(ctx context.Context, result *ToolCallResult) (err error) {
	// Defence in depth: a MemoryClient implementation may return an error
	// whose Error() panics (the April 2026 flowstate.log entries were
	// produced when the MCP client constructed `&json.UnmarshalTypeError{Type: nil}`,
	// which panics on dereference). Recover so the event-bus goroutine keeps
	// running and surface a safe error to the caller.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("learning hook recovered from panic: %v", r)
		}
	}()

	record := &Record{}

	if agentID, ok := ctx.Value(AgentIDKey).(string); ok {
		record.AgentID = agentID
	}

	// Populate ToolsUsed from call stack
	record.ToolsUsed = extractCallStack()

	// Populate Outcome from result
	if result != nil {
		record.Outcome = result.Outcome
	}

	if h.client == nil {
		return nil
	}
	writeErr := h.client.WriteLearningRecord(record)
	return sanitiseError(writeErr)
}

// sanitiseError returns an error whose Error() method is guaranteed not to
// panic. If the supplied error's Error() panics (e.g. a typed-nil pointer
// wrapped in the error interface, or a stdlib *json.UnmarshalTypeError with
// a nil reflect.Type), the panic is recovered and replaced with a degraded
// sentinel that preserves diagnostic intent without crashing downstream
// log handlers.
//
// Expected:
//   - err may be nil or any error value, including malformed implementations.
//
// Returns:
//   - nil when err is nil.
//   - A safe error value otherwise.
//
// Side effects:
//   - None.
func sanitiseError(err error) error {
	if err == nil {
		return nil
	}
	safe := safeErrorString(err)
	return errors.New(safe)
}

// safeErrorString invokes err.Error() with a recover guard.
//
// Expected:
//   - err is non-nil.
//
// Returns:
//   - The string returned by err.Error(), or a degraded message describing
//     the recovered panic.
//
// Side effects:
//   - None.
func safeErrorString(err error) (s string) {
	defer func() {
		if r := recover(); r != nil {
			s = fmt.Sprintf("[learning hook: degraded error from %T (Error method panicked: %v)]", err, r)
		}
	}()
	return err.Error()
}
