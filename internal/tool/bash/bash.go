package bash

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/tool"
)

const timeout = 30 * time.Second

type Tool struct{}

func New() *Tool {
	return &Tool{}
}

func (t *Tool) Name() string {
	return "bash"
}

func (t *Tool) Description() string {
	return "Execute bash commands with a 30-second timeout"
}

func (t *Tool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
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

func (t *Tool) Execute(ctx context.Context, input tool.ToolInput) (tool.ToolResult, error) {
	command, ok := input.Arguments["command"].(string)
	if !ok || command == "" {
		return tool.ToolResult{}, fmt.Errorf("command argument is required")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return tool.ToolResult{
			Output: strings.TrimSpace(string(out)),
			Error:  fmt.Errorf("command failed: %w", err),
		}, nil
	}

	return tool.ToolResult{
		Output: strings.TrimSpace(string(out)),
	}, nil
}
