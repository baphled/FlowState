// Package engine provides the AI agent execution engine.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/delegation"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

var (
	errDelegationNotAllowed   = errors.New("delegation not allowed for this agent")
	errTaskTypeMustBeString   = errors.New("task_type must be a string")
	errMessageMustBeString    = errors.New("message must be a string")
	errHandoffMustBeObject    = errors.New("handoff must be an object")
	errBackgroundModeDisabled = errors.New("background mode disabled: no background manager configured")
)

// streamOutputKeyType identifies the context key used for streaming output.
type streamOutputKeyType struct{}

var streamOutputKey streamOutputKeyType

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
	engines           map[string]*Engine
	delegation        agent.Delegation
	sourceAgentID     string
	backgroundManager *BackgroundTaskManager
	coordinationStore coordination.Store
}

// delegationTarget carries the resolved agent, engine, and message for delegation.
type delegationTarget struct {
	agentID string
	engine  *Engine
	message string
	handoff *delegation.Handoff
	chainID string
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
func NewDelegateTool(engines map[string]*Engine, delegationConfig agent.Delegation, sourceAgentID string) *DelegateTool {
	return &DelegateTool{
		engines:       engines,
		delegation:    delegationConfig,
		sourceAgentID: sourceAgentID,
	}
}

// NewDelegateToolWithBackground creates a new delegation tool with background task support.
//
// Expected:
//   - engines is a map of agent IDs to their Engine instances.
//   - delegation is the delegation configuration for the current agent.
//   - sourceAgentID identifies the agent that owns this tool.
//   - backgroundManager is the manager for tracking background tasks.
//   - coordinationStore is the shared store for cross-agent coordination.
//
// Returns:
//   - A configured DelegateTool instance with background support.
//
// Side effects:
//   - None.
func NewDelegateToolWithBackground(
	engines map[string]*Engine,
	delegationConfig agent.Delegation,
	sourceAgentID string,
	backgroundManager *BackgroundTaskManager,
	coordinationStore coordination.Store,
) *DelegateTool {
	return &DelegateTool{
		engines:           engines,
		delegation:        delegationConfig,
		sourceAgentID:     sourceAgentID,
		backgroundManager: backgroundManager,
		coordinationStore: coordinationStore,
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
//   - A tool.Schema describing the required task_type and message properties,
//   - plus optional run_in_background and handoff properties.
//
// Side effects:
//   - None.
func (d *DelegateTool) Schema() tool.Schema {
	schema := tool.Schema{
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
			"run_in_background": {
				Type:        "boolean",
				Description: "If true, run the delegation asynchronously and return a task ID",
			},
			"handoff": {
				Type:        "object",
				Description: "Optional handoff metadata including ChainID for coordination",
			},
		},
		Required: []string{"task_type", "message"},
	}
	return schema
}

// Execute runs the delegation tool by routing the task to the appropriate sub-agent.
// When run_in_background is true and a background manager is configured, the task
// is executed asynchronously and returns a task ID immediately.
//
// Expected:
//   - ctx is a valid context for the delegation operation.
//   - input contains "task_type" and "message" string arguments.
//   - Optional "run_in_background" boolean to run asynchronously.
//   - Optional "handoff" object for ChainID and coordination.
//
// Returns:
//   - A tool.Result containing the sub-agent's aggregated response or task ID.
//   - An error if delegation is not allowed, arguments are invalid, or streaming fails.
//
// Side effects:
//   - Streams a request to the target agent's engine.
//   - Emits DelegationInfo stream chunks when an output channel is available in ctx.
func (d *DelegateTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	target, runAsync, err := d.resolveTargetWithOptions(input)
	if err != nil {
		return tool.Result{}, err
	}

	outChan, hasOutput := streamOutputFromContext(ctx)
	chainID := target.chainID
	if chainID == "" {
		chainID = newDelegationChainID()
	}

	baseInfo := provider.DelegationInfo{
		SourceAgent:  d.sourceAgentID,
		TargetAgent:  target.agentID,
		ChainID:      chainID,
		ModelName:    target.engine.LastModel(),
		ProviderName: target.engine.LastProvider(),
		Description:  target.message,
		StartedAt:    ptrTime(time.Now().UTC()),
	}

	if runAsync {
		return d.executeAsync(ctx, target, baseInfo, outChan, hasOutput)
	}

	return d.executeSync(ctx, target, baseInfo, outChan, hasOutput)
}

// executeSync runs delegation synchronously, blocking until complete.
//
// Expected:
//   - target is the resolved delegation target.
//   - baseInfo contains delegation metadata.
//   - outChan and hasOutput for streaming events.
//
// Returns:
//   - A tool.Result with the delegation result.
//   - An error if delegation fails.
//
// Side effects:
//   - Emits delegation events.
func (d *DelegateTool) executeSync(
	ctx context.Context,
	target delegationTarget,
	baseInfo provider.DelegationInfo,
	outChan chan<- provider.StreamChunk,
	hasOutput bool,
) (tool.Result, error) {
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

// executeAsync runs delegation asynchronously, returning immediately with a task ID.
//
// Expected:
//   - target is the resolved delegation target.
//   - baseInfo contains delegation metadata.
//   - outChan and hasOutput for streaming events.
//
// Returns:
//   - A tool.Result containing the task ID.
//   - An error if background mode is disabled or task launch fails.
//
// Side effects:
//   - Spawns a goroutine for the delegation.
//   - Emits delegation events for started status.
func (d *DelegateTool) executeAsync(
	ctx context.Context,
	target delegationTarget,
	baseInfo provider.DelegationInfo,
	outChan chan<- provider.StreamChunk,
	hasOutput bool,
) (tool.Result, error) {
	if d.backgroundManager == nil {
		return tool.Result{}, errBackgroundModeDisabled
	}

	taskID := fmt.Sprintf("task-%s-%d", target.agentID, time.Now().UTC().UnixNano())

	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "started")

	d.backgroundManager.Launch(ctx, taskID, target.agentID, target.message, func(ctx context.Context) (string, error) {
		result, err := d.executeBackgroundTask(ctx, target, baseInfo, outChan, hasOutput)
		if err != nil {
			return "", err
		}
		return result, nil
	})

	return tool.Result{Output: fmt.Sprintf(`{"task_id": %q, "status": "running"}`, taskID)}, nil
}

// executeBackgroundTask performs the actual delegation within a background goroutine.
//
// Expected:
//   - ctx is the task context with cancellation support.
//   - target is the resolved delegation target.
//   - baseInfo contains delegation metadata.
//   - outChan and hasOutput for streaming events.
//
// Returns:
//   - The delegation result string on success.
//   - An error if delegation fails.
//
// Side effects:
//   - Emits delegation events for completed or failed status.
func (d *DelegateTool) executeBackgroundTask(
	ctx context.Context,
	target delegationTarget,
	baseInfo provider.DelegationInfo,
	outChan chan<- provider.StreamChunk,
	hasOutput bool,
) (string, error) {
	chunks, err := target.engine.Stream(ctx, target.agentID, target.message)
	if err != nil {
		completedAt := time.Now().UTC()
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		return "", fmt.Errorf("delegation failed: %w", err)
	}

	result, err := d.collectDelegationResult(chunks)
	if err != nil {
		completedAt := time.Now().UTC()
		baseInfo.ToolCalls = result.toolCalls
		baseInfo.LastTool = result.lastTool
		baseInfo.CompletedAt = &completedAt
		d.emitDelegationEvent(outChan, hasOutput, baseInfo, "failed")
		return "", err
	}

	completedAt := time.Now().UTC()
	baseInfo.ToolCalls = result.toolCalls
	baseInfo.LastTool = result.lastTool
	baseInfo.CompletedAt = &completedAt
	d.emitDelegationEvent(outChan, hasOutput, baseInfo, "completed")

	return result.response, nil
}

// resolveTargetWithOptions validates input and resolves the target with async options.
//
// Expected:
//   - input contains task_type, message, run_in_background, and optional handoff arguments.
//
// Returns:
//   - The resolved target with chain ID.
//   - Whether to run asynchronously.
//   - An error if delegation is disabled, inputs are invalid, or no target exists.
//
// Side effects:
//   - None.
func (d *DelegateTool) resolveTargetWithOptions(input tool.Input) (delegationTarget, bool, error) {
	if !d.delegation.CanDelegate {
		return delegationTarget{}, false, errDelegationNotAllowed
	}

	taskType, ok := input.Arguments["task_type"].(string)
	if !ok {
		return delegationTarget{}, false, errTaskTypeMustBeString
	}

	message, ok := input.Arguments["message"].(string)
	if !ok {
		return delegationTarget{}, false, errMessageMustBeString
	}

	runAsync := false
	if value, ok := input.Arguments["run_in_background"].(bool); ok {
		runAsync = value
	}

	var handoff *delegation.Handoff
	if handoffArg, ok := input.Arguments["handoff"]; ok && handoffArg != nil {
		h, err := d.parseHandoff(handoffArg)
		if err != nil {
			return delegationTarget{}, false, fmt.Errorf("parsing handoff: %w", err)
		}
		handoff = h
	}

	targetAgentID, ok := d.delegation.DelegationTable[taskType]
	if !ok {
		return delegationTarget{}, false, fmt.Errorf("no agent configured for task type: %s", taskType)
	}

	targetEngine, ok := d.engines[targetAgentID]
	if !ok {
		return delegationTarget{}, false, fmt.Errorf("target agent engine not available: %s", targetAgentID)
	}

	var chainID string
	if handoff != nil {
		chainID = handoff.ChainID
	} else {
		chainID = newDelegationChainID()
	}

	return delegationTarget{
		agentID: targetAgentID,
		engine:  targetEngine,
		message: message,
		handoff: handoff,
		chainID: chainID,
	}, runAsync, nil
}

// parseHandoff parses a handoff argument into a delegation.Handoff struct.
//
// Expected:
//   - handoffArg is an interface{} that can be unmarshalled to Handoff.
//
// Returns:
//   - A parsed Handoff on success.
//   - An error if parsing fails.
//
// Side effects:
//   - None.
func (d *DelegateTool) parseHandoff(handoffArg interface{}) (*delegation.Handoff, error) {
	var h delegation.Handoff

	switch v := handoffArg.(type) {
	case map[string]interface{}:
		data, err := json.Marshal(v)
		if err != nil {
			return nil, errHandoffMustBeObject
		}
		if err := json.Unmarshal(data, &h); err != nil {
			return nil, errHandoffMustBeObject
		}
	case string:
		if err := json.Unmarshal([]byte(v), &h); err != nil {
			return nil, errHandoffMustBeObject
		}
	default:
		return nil, errHandoffMustBeObject
	}

	return &h, nil
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

// BackgroundManager returns the background task manager for this delegate tool.
//
// Returns:
//   - The BackgroundTaskManager if configured, or nil.
//
// Side effects:
//   - None.
func (d *DelegateTool) BackgroundManager() *BackgroundTaskManager {
	return d.backgroundManager
}

// CoordinationStore returns the coordination store for this delegate tool.
//
// Returns:
//   - The coordination.Store if configured, or nil.
//
// Side effects:
//   - None.
func (d *DelegateTool) CoordinationStore() coordination.Store {
	return d.coordinationStore
}
