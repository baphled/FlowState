// Package engine provides the AI agent execution engine.
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

var (
	errDelegationNotAllowed = errors.New("delegation not allowed for this agent")
	errTaskTypeMustBeString = errors.New("task_type must be a string")
	errMessageMustBeString  = errors.New("message must be a string")
)

// DelegateTool enables an engine to delegate tasks to other agents.
type DelegateTool struct {
	engines    map[string]*Engine
	delegation agent.Delegation
}

// NewDelegateTool creates a new delegation tool for the given engines and delegation configuration.
//
// Expected:
//   - engines is a map of agent IDs to their Engine instances.
//   - delegation is the delegation configuration for the current agent.
//
// Returns:
//   - A configured DelegateTool instance.
//
// Side effects:
//   - None.
func NewDelegateTool(engines map[string]*Engine, delegation agent.Delegation) *DelegateTool {
	return &DelegateTool{
		engines:    engines,
		delegation: delegation,
	}
}

// Name returns the tool name.
//
// Returns:
//   - The string "delegate".
//
// Side effects:
//   - None.
func (d *DelegateTool) Name() string {
	return "delegate"
}

// Description returns a human-readable description of the delegation tool.
//
// Returns:
//   - A string describing what the tool does.
//
// Side effects:
//   - None.
func (d *DelegateTool) Description() string {
	return "Delegate a task to another agent based on task type"
}

// Schema returns the JSON schema for the delegation tool input.
//
// Returns:
//   - A tool.Schema describing the required task_type and message properties.
//
// Side effects:
//   - None.
func (d *DelegateTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"task_type": {
				Type:        "string",
				Description: "The type of task to delegate (e.g., testing, coding)",
			},
			"message": {
				Type:        "string",
				Description: "The message or instruction to send to the target agent",
			},
		},
		Required: []string{"task_type", "message"},
	}
}

// Execute runs the delegation tool by routing the task to the appropriate sub-agent.
//
// Expected:
//   - ctx is a valid context for the delegation operation.
//   - input contains "task_type" and "message" string arguments.
//
// Returns:
//   - A tool.Result containing the sub-agent's aggregated response.
//   - An error if delegation is not allowed, arguments are invalid, or streaming fails.
//
// Side effects:
//   - Streams a request to the target agent's engine.
func (d *DelegateTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	if !d.delegation.CanDelegate {
		return tool.Result{}, errDelegationNotAllowed
	}

	taskType, ok := input.Arguments["task_type"].(string)
	if !ok {
		return tool.Result{}, errTaskTypeMustBeString
	}

	message, ok := input.Arguments["message"].(string)
	if !ok {
		return tool.Result{}, errMessageMustBeString
	}

	targetAgentID, ok := d.delegation.DelegationTable[taskType]
	if !ok {
		return tool.Result{}, fmt.Errorf("no agent configured for task type: %s", taskType)
	}

	targetEngine, ok := d.engines[targetAgentID]
	if !ok {
		return tool.Result{}, fmt.Errorf("target agent engine not available: %s", targetAgentID)
	}

	chunks, err := targetEngine.Stream(ctx, targetAgentID, message)
	if err != nil {
		return tool.Result{}, fmt.Errorf("delegation failed: %w", err)
	}

	var response strings.Builder
	for chunk := range chunks {
		if chunk.Error != nil {
			return tool.Result{}, fmt.Errorf("delegation stream error: %w", chunk.Error)
		}
		response.WriteString(chunk.Content)
	}

	return tool.Result{Output: response.String()}, nil
}

// DelegateToAgent sends a message to a sub-agent and streams the response.
//
// Expected:
//   - ctx is a valid context for the delegation operation.
//   - engines is a map of agent IDs to their Engine instances.
//   - taskType identifies the delegation target via the delegation table.
//   - message is the instruction to send to the target agent.
//
// Returns:
//   - A channel of StreamChunk values from the target agent.
//   - An error if delegation is not allowed or the target agent is unavailable.
//
// Side effects:
//   - Initiates a streaming request on the target agent's engine.
func (e *Engine) DelegateToAgent(
	ctx context.Context,
	engines map[string]*Engine,
	taskType string,
	message string,
) (<-chan provider.StreamChunk, error) {
	if !e.manifest.Delegation.CanDelegate {
		return nil, errDelegationNotAllowed
	}

	targetAgentID, ok := e.manifest.Delegation.DelegationTable[taskType]
	if !ok {
		return nil, fmt.Errorf("no agent configured for task type: %s", taskType)
	}

	targetEngine, ok := engines[targetAgentID]
	if !ok {
		return nil, fmt.Errorf("target agent engine not available: %s", targetAgentID)
	}

	return targetEngine.Stream(ctx, targetAgentID, message)
}
