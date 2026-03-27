package recall

import (
	"context"
	"fmt"
	"strings"

	chainrecall "github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tool"
)

// ChainGetMessagesTool retrieves messages from a specific agent in the shared chain context.
type ChainGetMessagesTool struct {
	store chainrecall.ChainContextStore
}

// NewChainGetMessagesTool creates a new ChainGetMessagesTool backed by the given chain store.
//
// Expected:
//   - store is a valid, non-nil ChainContextStore.
//
// Returns:
//   - A pointer to an initialised ChainGetMessagesTool.
//
// Side effects:
//   - None.
func NewChainGetMessagesTool(store chainrecall.ChainContextStore) *ChainGetMessagesTool {
	return &ChainGetMessagesTool{store: store}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "chain_get_messages".
//
// Side effects:
//   - None.
func (t *ChainGetMessagesTool) Name() string {
	return "chain_get_messages"
}

// Description returns a human-readable description of the tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *ChainGetMessagesTool) Description() string {
	return "Retrieve the most recent messages from a specific agent in the shared chain context"
}

// Schema returns the JSON schema for the tool inputs.
//
// Returns:
//   - A tool.Schema describing the agent_id and last properties.
//
// Side effects:
//   - None.
func (t *ChainGetMessagesTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"agent_id": {
				Type:        "string",
				Description: "The agent ID to retrieve messages from. Leave empty to retrieve from all agents.",
			},
			"last": {
				Type:        "integer",
				Description: "Number of most recent messages to retrieve (default 10)",
			},
		},
		Required: []string{},
	}
}

// Execute retrieves messages from the chain context store for the specified agent.
//
// Expected:
//   - ctx is a valid context.
//   - input may contain an "agent_id" string and a "last" integer argument.
//
// Returns:
//   - A tool.Result containing formatted messages.
//   - An error if the retrieval fails.
//
// Side effects:
//   - None.
func (t *ChainGetMessagesTool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	agentID, ok := input.Arguments["agent_id"].(string)
	if !ok {
		agentID = ""
	}

	last := 10
	if raw, ok := input.Arguments["last"]; ok {
		if f, ok := raw.(float64); ok && f > 0 {
			last = int(f)
		}
	}

	messages, err := t.store.GetByAgent(agentID, last)
	if err != nil {
		return tool.Result{}, fmt.Errorf("retrieving chain messages: %w", err)
	}

	if len(messages) == 0 {
		return tool.Result{Output: ""}, nil
	}

	var parts []string
	for _, m := range messages {
		parts = append(parts, fmt.Sprintf("agent:%s role:%s content:%s", agentID, m.Role, m.Content))
	}
	return tool.Result{Output: strings.Join(parts, "\n---\n")}, nil
}
