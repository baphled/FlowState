// Package bash provides a tool for executing bash commands with timeout.
package bash

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/pathguard"
	"github.com/baphled/flowstate/internal/tool/truncate"
)

const timeout = 30 * time.Second

// Tool executes bash commands with a configurable timeout.
type Tool struct {
	guard *pathguard.Guard
}

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

// NewWithGuard creates a bash tool that denies commands referencing protected paths.
func NewWithGuard(g *pathguard.Guard) *Tool {
	return &Tool{guard: g}
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

	if t.guard != nil {
		if err := t.guard.CheckCommand(command); err != nil {
			return tool.Result{Error: err}, nil
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	capped := capOutput(ctx, trimmed)
	if err != nil {
		return tool.Result{
			Output: capped,
			Error:  fmt.Errorf("command failed: %w", err),
		}, nil
	}

	return tool.Result{
		Output: capped,
	}, nil
}

// capOutput applies the shared truncation envelope using the session ID
// from ctx. Output stays unchanged when under the byte/line budget.
func capOutput(ctx context.Context, output string) string {
	sessionID, _ := ctx.Value(session.IDKey{}).(string)
	r := truncate.Apply(output, truncate.Options{
		SessionID: sessionID,
		ToolName:  "bash",
	})
	return r.Content
}
