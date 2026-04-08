package learning

import (
	"context"
)

// Record represents a tool call record for learning purposes.
type Record struct {
	AgentID   string
	ToolsUsed []string
	Outcome   string
}

// ToolCallResult represents the result of a tool call.
type ToolCallResult struct {
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
//   - result is accepted for interface compatibility.
//
// Returns:
//   - An error when persisting the learning record fails.
//
// Side effects:
//   - Writes a learning record via the configured MemoryClient.
func (h *Hook) Handle(ctx context.Context, _ *ToolCallResult) error {
	record := &Record{}

	if agentID, ok := ctx.Value(AgentIDKey).(string); ok {
		record.AgentID = agentID
	}

	return h.client.WriteLearningRecord(record)
}
