package lifecycle

import "github.com/baphled/flowstate/internal/tool"

// PreToolExecCtx carries the boundary between Stream and a single tool call.
// A PreToolExec hook can inspect and modify the tool call before execution.
// Setting Skipped=true prevents this tool call from executing; the engine
// records it as skipped and continues the stream.
type PreToolExecCtx struct {
	Tool    tool.Tool
	Args    map[string]any
	Skipped bool
}

// DefaultPreToolExec returns the default PreToolExec lifecycle stage.
//
// The slot phase has no pre-registered hooks and a pass-through handler.
// Hooks activate the slot by being added to the chain.
func DefaultPreToolExec() LifecycleStage[PreToolExecCtx] {
	return LifecycleStage[PreToolExecCtx]{
		Name:    "pre_tool_exec",
		Handler: passThroughHandler[PreToolExecCtx],
	}
}
