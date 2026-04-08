package learning

import (
	"context"
)

// LearningRecord represents a record of a tool call for learning purposes.
type LearningRecord struct {
	AgentID   string
	ToolsUsed []string
	Outcome   string
}

// ToolCallResult represents the result of a tool call (stub for now).
type ToolCallResult struct {
}

// contextKeyType is a type for context keys to avoid collisions with built-in types.
type contextKeyType string

// AgentIDKey is the context key for storing the agent ID.
const AgentIDKey contextKeyType = "AgentID"

// learningHook implements the hook logic.
type learningHook struct {
	client MemoryClient
}

// NewLearningHook creates a new learning hook with the given memory client.
func NewLearningHook(client MemoryClient) *learningHook {
	return &learningHook{client: client}
}

// Handle processes a tool call result and persists a learning record.
func (h *learningHook) Handle(ctx context.Context, result *ToolCallResult) error {
	record := &LearningRecord{}

	// Extract AgentID from context
	if agentID, ok := ctx.Value(AgentIDKey).(string); ok {
		record.AgentID = agentID
	}

	// Write via MemoryClient
	return h.client.WriteLearningRecord(record)
}
