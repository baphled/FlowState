// Package bash provides a tool for executing bash commands with timeout.
package bash

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/tool"
)

const timeout = 30 * time.Second

// Tool executes bash commands with a configurable timeout.
type Tool struct{}

// New creates a new bash execution tool.
//
// Returns:
//   - A configured bash Tool instance.
//
// Side effects:
//   - None.
func New() *Tool {
	return &Tool{}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "bash".
//
// Side effects:
//   - None.
func (t *Tool) Name() string {
	return "bash"
}

// Description returns a human-readable description of the bash tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *Tool) Description() string {
	return "Execute bash commands with a 30-second timeout"
}

// Schema returns the JSON schema for the bash tool arguments.
//
// Returns:
//   - A tool.Schema describing the required command property.
//
// Side effects:
//   - None.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"command": {
				Type:        "string",
				Description: "The bash command to execute",
			},
		},
		Required: []string{"command"},
	}
}

// Execute runs the specified bash command and returns its output.
//
// Expected:
//   - ctx is a valid context for the command execution.
//   - input contains a "command" string argument.
//
// Returns:
//   - A tool.Result containing the combined stdout and stderr output.
//   - An error if the command argument is missing.
//
// Side effects:
//   - Executes a bash subprocess with a 30-second timeout.
func (t *Tool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	command, ok := input.Arguments["command"].(string)
	if !ok || command == "" {
		return tool.Result{}, errors.New("command argument is required")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return tool.Result{
			Output: strings.TrimSpace(string(out)),
			Error:  fmt.Errorf("command failed: %w", err),
		}, nil
	}

	return tool.Result{
		Output: strings.TrimSpace(string(out)),
	}, nil
}
