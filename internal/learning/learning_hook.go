package learning

import (
	"context"
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
//   - An error when persisting the learning record fails.
//
// Side effects:
//   - Writes a learning record via the configured MemoryClient.
func (h *Hook) Handle(ctx context.Context, result *ToolCallResult) error {
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
	return h.client.WriteLearningRecord(record)
}
