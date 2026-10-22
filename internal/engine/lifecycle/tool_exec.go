package lifecycle

import (
	"context"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// ToolExecPhase is the sub-lifecycle phase identifier within the ToolExec stage.
type ToolExecPhase string

const (
	// ToolExecBegin marks the start of the tool execution sub-lifecycle.
	ToolExecBegin ToolExecPhase = "begin"
	// ToolExecDispatch marks the dispatch phase where the tool call is routed.
	ToolExecDispatch ToolExecPhase = "dispatch"
	// ToolExecRun marks the core execution phase where the tool runs.
	ToolExecRun ToolExecPhase = "run"
	// ToolExecEnd marks the completion of the tool execution sub-lifecycle.
	ToolExecEnd ToolExecPhase = "end"
)

// ToolExecCtx carries the data for a single tool call within the ToolExec
// sub-lifecycle. Hooks in this stage run before, during, and after the core
// tool execution handler.
//
// Required fields the bridge must set before Execute:
//   - Context, SessionID, ToolCallID — caller identity for events and stores
//   - Tool, Args — the resolved tool and its arguments
//
// Optional fields the bridge may set:
//   - InternalToolCallID — pre-resolved correlation id for observability events
//
// Fields set by the stage handler:
//   - Result — the tool execution result (non-nil after successful execution)
//   - Error — any error from tool execution
type ToolExecCtx struct {
	Context    context.Context
	SessionID  string
	ToolCallID string
	Tool       tool.Tool
	Args       map[string]any
	Result     *tool.Result
	Error      error

	// InternalToolCallID is a pre-resolved FlowState-internal correlation id.
	// When set, hooks and the handler should use it for observability events;
	// when empty, hooks should resolve it from the correlator.
	InternalToolCallID string

	// Sub-lifecycle tracking
	Phase ToolExecPhase
}

// ToolExecResult carries the outcome of executing one tool call.
type ToolExecResult struct {
	ToolCall   provider.ToolCall
	ToolResult tool.Result
	Err        error
}

// DefaultToolExec returns the default ToolExec lifecycle stage.
//
// The ToolExec stage pre-registers hooks that implement permission checks,
// plugin dispatch, quota tracking, knowledge extraction, and event publishing.
// Custom pipelines can reorder or replace these.
//
// Returns: result of DefaultToolExec.
// Side effects: None.
func DefaultToolExec() LifecycleStage[ToolExecCtx] {
	return LifecycleStage[ToolExecCtx]{
		Name:    "tool_exec",
		Handler: passThroughHandler[ToolExecCtx],
	}
}
