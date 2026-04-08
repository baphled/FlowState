package plan

import (
	"context"
	"errors"

	"github.com/baphled/flowstate/internal/tool"
)

const (
	enterName = "plan_enter"
	exitName  = "plan_exit"
)

// EnterTool signals that planning has started.
type EnterTool struct{}

// ExitTool signals that planning has finished.
type ExitTool struct{}

// NewEnter creates a new plan_enter tool instance.
//
// Returns:
//   - An EnterTool that marks plan mode entry.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func NewEnter() *EnterTool { return &EnterTool{} }

// NewExit creates a new plan_exit tool instance.
//
// Returns:
//   - An ExitTool that marks plan mode exit.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func NewExit() *ExitTool { return &ExitTool{} }

// Name returns the tool identifier.
//
// Returns:
//   - The string "plan_enter".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *EnterTool) Name() string { return enterName }

// Description returns a human-readable description of the tool.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *EnterTool) Description() string { return "Enter plan mode" }

// Schema returns the input schema for the tool.
//
// Returns:
//   - An empty object schema.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *EnterTool) Schema() tool.Schema {
	return tool.Schema{Type: "object", Properties: map[string]tool.Property{}, Required: []string{}}
}

// Execute marks the start of plan mode.
//
// Expected:
//   - None.
//
// Returns:
//   - A tool.Result indicating plan mode entry.
//
// Side effects:
//   - None.
func (t *EnterTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	return tool.Result{Title: "Plan", Output: "entered plan mode", Metadata: map[string]interface{}{"mode": "plan", "action": "enter"}}, nil
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "plan_exit".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *ExitTool) Name() string { return exitName }

// Description returns a human-readable description of the tool.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *ExitTool) Description() string { return "Exit plan mode" }

// Schema returns the input schema for the tool.
//
// Returns:
//   - An empty object schema.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *ExitTool) Schema() tool.Schema {
	return tool.Schema{Type: "object", Properties: map[string]tool.Property{}, Required: []string{}}
}

// Execute marks the end of plan mode.
//
// Expected:
//   - None.
//
// Returns:
//   - A tool.Result indicating plan mode exit.
//
// Side effects:
//   - None.
func (t *ExitTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	return tool.Result{Title: "Plan", Output: "exited plan mode", Metadata: map[string]interface{}{"mode": "plan", "action": "exit"}}, nil
}

var _ = errors.New
