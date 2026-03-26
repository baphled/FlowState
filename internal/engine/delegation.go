// Package engine provides the AI agent execution engine.
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// streamOutputKeyType identifies the context key used for streaming output.
type streamOutputKeyType struct{}

var streamOutputKey streamOutputKeyType

var (
	errDelegationNotAllowed = errors.New("delegation not allowed for this agent")
	errTaskTypeMustBeString = errors.New("task_type must be a string")
	errMessageMustBeString  = errors.New("message must be a string")
)

// WithStreamOutput returns a child context carrying the given output channel
// so that tools (e.g. DelegateTool) can inject chunks into the parent stream.
//
// Expected:
//   - ctx is a valid context to extend.
//   - ch is the stream output channel to attach.
//
// Returns:
//   - A child context containing the output channel.
//
// Side effects:
//   - Stores the output channel in the returned context for later retrieval.
func WithStreamOutput(ctx context.Context, ch chan<- provider.StreamChunk) context.Context {
	return context.WithValue(ctx, streamOutputKey, ch)
}

// streamOutputFromContext extracts the output channel from the context, if present.
//
// Expected:
//   - ctx may carry a stream output channel stored by WithStreamOutput.
//
// Returns:
//   - The output channel and true when present, or a nil channel and false otherwise.
//
// Side effects:
//   - None.
func streamOutputFromContext(ctx context.Context) (chan<- provider.StreamChunk, bool) {
	ch, ok := ctx.Value(streamOutputKey).(chan<- provider.StreamChunk)
	return ch, ok
}

// DelegateTool enables an engine to delegate tasks to other agents.
type DelegateTool struct {
	engines       map[string]*Engine
	delegation    agent.Delegation
	sourceAgentID string
}

// delegationTarget carries the resolved agent, engine, and message for delegation.
type delegationTarget struct {
	agentID string
	engine  *Engine
	message string
}

// delegationResult carries the aggregated response and stream metadata from delegation.
type delegationResult struct {
	response  string
	toolCalls int
	lastTool  string
}

// NewDelegateTool creates a new delegation tool for the given engines, delegation configuration,
// and source agent identifier used for event attribution.
//
// Expected:
//   - engines is a map of agent IDs to their Engine instances.
//   - delegation is the delegation configuration for the current agent.
//   - sourceAgentID identifies the agent that owns this tool.
//
// Returns:
//   - A configured DelegateTool instance.
//
// Side effects:
//   - None.
func NewDelegateTool(engines map[string]*Engine, delegation agent.Delegation, sourceAgentID string) *DelegateTool {
	return &DelegateTool{
		engines:       engines,
		delegation:    delegation,
		sourceAgentID: sourceAgentID,
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
// When the context carries a stream output channel (via WithStreamOutput), Execute
// emits DelegationInfo chunks with status "started", "completed", or "failed".
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
//   - Emits DelegationInfo stream chunks when an output channel is available in ctx.
func (d *DelegateTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	target, err := d.resolveTarget(input)
	if err != nil {
		return tool.Result{}, err
	}

	outChan, hasOutput := streamOutputFromContext(ctx)
	baseInfo := provider.DelegationInfo{
		SourceAgent:  d.sourceAgentID,
		TargetAgent:  target.agentID,
		ChainID:      newDelegationChainID(),
		ModelName:    target.engine.LastModel(),
		ProviderName: target.engine.LastProvider(),
		Description:  target.message,
		StartedAt:    ptrTime(time.Now().UTC()),
	}

	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "started")

	chunks, err := target.engine.Stream(ctx, target.agentID, target.message)
	if err != nil {
		completedAt := time.Now().UTC()
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		return tool.Result{}, fmt.Errorf("delegation failed: %w", err)
	}

	result, err := d.collectDelegationResult(chunks)
	if err != nil {
		completedAt := time.Now().UTC()
		baseInfo.ToolCalls = result.toolCalls
		baseInfo.LastTool = result.lastTool
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		return tool.Result{}, err
	}

	completedAt := time.Now().UTC()
	baseInfo.ToolCalls = result.toolCalls
	baseInfo.LastTool = result.lastTool
	baseInfo.CompletedAt = &completedAt
	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "completed")

	return tool.Result{Output: result.response}, nil
}

// resolveTarget validates the input and resolves the target engine for delegation.
//
// Expected:
//   - input contains string task_type and message arguments.
//
// Returns:
//   - The resolved target agent ID.
//   - The target engine for the delegated task.
//   - The delegation message.
//   - An error if delegation is disabled, inputs are invalid, or no target exists.
//
// Side effects:
//   - Reads the delegation configuration and agent engine map.
func (d *DelegateTool) resolveTarget(input tool.Input) (delegationTarget, error) {
	if !d.delegation.CanDelegate {
		return delegationTarget{}, errDelegationNotAllowed
	}

	taskType, ok := input.Arguments["task_type"].(string)
	if !ok {
		return delegationTarget{}, errTaskTypeMustBeString
	}

	message, ok := input.Arguments["message"].(string)
	if !ok {
		return delegationTarget{}, errMessageMustBeString
	}

	targetAgentID, ok := d.delegation.DelegationTable[taskType]
	if !ok {
		return delegationTarget{}, fmt.Errorf("no agent configured for task type: %s", taskType)
	}

	targetEngine, ok := d.engines[targetAgentID]
	if !ok {
		return delegationTarget{}, fmt.Errorf("target agent engine not available: %s", targetAgentID)
	}

	return delegationTarget{agentID: targetAgentID, engine: targetEngine, message: message}, nil
}

// collectDelegationResult aggregates streamed chunks from the delegated agent.
//
// Expected:
//   - chunks is the stream returned by the target engine.
//
// Returns:
//   - The concatenated response text.
//   - The number of chunks observed.
//   - The most recent tool name seen in the stream.
//   - An error if the stream yields a chunk error.
//
// Side effects:
//   - Reads from the streamed chunk channel until it closes or returns an error.
func (d *DelegateTool) collectDelegationResult(chunks <-chan provider.StreamChunk) (delegationResult, error) {
	var response strings.Builder
	toolCalls := 0
	lastTool := ""
	for chunk := range chunks {
		toolCalls++
		if chunk.ToolCall != nil && chunk.ToolCall.Name != "" {
			lastTool = chunk.ToolCall.Name
		}
		if chunk.Error != nil {
			return delegationResult{}, fmt.Errorf("delegation stream error: %w", chunk.Error)
		}
		response.WriteString(chunk.Content)
	}

	return delegationResult{response: response.String(), toolCalls: toolCalls, lastTool: lastTool}, nil
}

// newDelegationChainID returns a unique identifier for a delegation chain.
//
// Returns:
//   - A chain identifier string derived from the current UTC time.
//
// Side effects:
//   - Reads the current clock to ensure uniqueness.
func newDelegationChainID() string {
	return fmt.Sprintf("chain-%d", time.Now().UTC().UnixNano())
}

// emitDelegationEvent sends a DelegationInfo chunk to the output channel when available.
//
// Expected:
//   - hasOutput indicates whether delegation events should be published.
//   - base contains the delegation metadata to reuse for the emitted chunk.
//
// Side effects:
//   - Sends a delegation update to the output channel when streaming output is enabled.
func (d *DelegateTool) emitDelegationEvent(
	outChan chan<- provider.StreamChunk, hasOutput bool,
	base provider.DelegationInfo, status string,
) {
	if !hasOutput {
		return
	}
	info := base
	info.Status = status
	outChan <- provider.StreamChunk{DelegationInfo: &info}
}

// ptrTime returns a pointer to the supplied time.
//
// Expected:
//   - t is a valid time value to reference.
//
// Returns:
//   - A pointer to t.
//
// Side effects:
//   - None.
func ptrTime(t time.Time) *time.Time {
	return &t
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
