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

// NewDelegateTool creates a new delegation tool for the given engines and delegation config.
func NewDelegateTool(engines map[string]*Engine, delegation agent.Delegation) *DelegateTool {
	return &DelegateTool{
		engines:    engines,
		delegation: delegation,
	}
}

// Name returns the tool name.
func (d *DelegateTool) Name() string {
	return "delegate"
}

// Description returns a human-readable description of the tool.
func (d *DelegateTool) Description() string {
	return "Delegate a task to another agent based on task type"
}

// Schema returns the JSON schema for the tool input.
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

// Execute runs the delegation tool with the given input.
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
