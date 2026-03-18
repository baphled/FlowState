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

type DelegateTool struct {
	engines    map[string]*Engine
	delegation agent.Delegation
}

func NewDelegateTool(engines map[string]*Engine, delegation agent.Delegation) *DelegateTool {
	return &DelegateTool{
		engines:    engines,
		delegation: delegation,
	}
}

func (d *DelegateTool) Name() string {
	return "delegate"
}

func (d *DelegateTool) Description() string {
	return "Delegate a task to another agent based on task type"
}

func (d *DelegateTool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
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

func (d *DelegateTool) Execute(ctx context.Context, input tool.ToolInput) (tool.ToolResult, error) {
	if !d.delegation.CanDelegate {
		return tool.ToolResult{}, errDelegationNotAllowed
	}

	taskType, ok := input.Arguments["task_type"].(string)
	if !ok {
		return tool.ToolResult{}, errTaskTypeMustBeString
	}

	message, ok := input.Arguments["message"].(string)
	if !ok {
		return tool.ToolResult{}, errMessageMustBeString
	}

	targetAgentID, ok := d.delegation.DelegationTable[taskType]
	if !ok {
		return tool.ToolResult{}, fmt.Errorf("no agent configured for task type: %s", taskType)
	}

	targetEngine, ok := d.engines[targetAgentID]
	if !ok {
		return tool.ToolResult{}, fmt.Errorf("target agent engine not available: %s", targetAgentID)
	}

	chunks, err := targetEngine.Stream(ctx, targetAgentID, message)
	if err != nil {
		return tool.ToolResult{}, fmt.Errorf("delegation failed: %w", err)
	}

	var response strings.Builder
	for chunk := range chunks {
		if chunk.Error != nil {
			return tool.ToolResult{}, fmt.Errorf("delegation stream error: %w", chunk.Error)
		}
		response.WriteString(chunk.Content)
	}

	return tool.ToolResult{Output: response.String()}, nil
}

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
