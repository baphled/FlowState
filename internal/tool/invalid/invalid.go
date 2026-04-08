package invalid

import (
	"context"
	"errors"

	"github.com/baphled/flowstate/internal/tool"
)

// Tool represents an invalid tool call.
type Tool struct{}

// New creates a new invalid tool instance.
//
// Returns:
//   - A Tool that always reports an invalid invocation.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func New() *Tool { return &Tool{} }

// Name returns the tool identifier.
//
// Returns:
//   - The string "invalid".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Name() string { return "invalid" }

// Description returns a human-readable description of the invalid tool.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Description() string { return "Represent an invalid tool invocation" }

// Schema returns the input schema for the invalid tool.
//
// Returns:
//   - An empty object schema.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{Type: "object", Properties: map[string]tool.Property{}, Required: []string{}}
}

// Execute always returns an invalid tool call error result.
//
// Expected:
//   - None.
//
// Returns:
//   - A tool.Result containing an invalid invocation error.
//
// Side effects:
//   - None.
func (t *Tool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	return tool.Result{Error: errors.New("invalid tool invocation")}, nil
}
