package swarm

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/tool"
)

// SwarmValidateTool validates a single swarm manifest by id.
type SwarmValidateTool struct {
	registry SwarmReader
}

// NewSwarmValidateTool creates a SwarmValidateTool backed by the given registry.
func NewSwarmValidateTool(registry SwarmReader) *SwarmValidateTool {
	return &SwarmValidateTool{registry: registry}
}

func (t *SwarmValidateTool) Name() string        { return "swarm_validate" }
func (t *SwarmValidateTool) Description() string { return "Validate a swarm manifest by id" }

func (t *SwarmValidateTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"id": {Type: "string", Description: "Swarm ID to validate"},
		},
		Required: []string{"id"},
	}
}

// Execute validates the named swarm and returns PASS/FAIL with any validation error.
func (t *SwarmValidateTool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	id, ok := input.Arguments["id"].(string)
	if !ok || id == "" {
		return tool.Result{}, errors.New("id argument is required")
	}

	m, found := t.registry.Get(id)
	if !found {
		return tool.Result{}, fmt.Errorf("swarm %q not found", id)
	}

	if err := m.Validate(nil); err != nil {
		return tool.Result{Output: fmt.Sprintf("FAIL\t%s", id)},
			fmt.Errorf("swarm %q validation failed: %w", id, err)
	}

	return tool.Result{Output: fmt.Sprintf("PASS\t%s", id)}, nil
}
