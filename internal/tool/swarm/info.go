package swarm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

// SwarmInfoTool returns full details of a single swarm manifest.
type SwarmInfoTool struct {
	registry SwarmReader
}

// NewSwarmInfoTool creates a SwarmInfoTool backed by the given registry.
func NewSwarmInfoTool(registry SwarmReader) *SwarmInfoTool {
	return &SwarmInfoTool{registry: registry}
}

func (t *SwarmInfoTool) Name() string        { return "swarm_info" }
func (t *SwarmInfoTool) Description() string { return "Get full details of a registered swarm" }

func (t *SwarmInfoTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"id": {Type: "string", Description: "Swarm ID to inspect"},
		},
		Required: []string{"id"},
	}
}

// Execute returns a formatted description of the named swarm's lead, members, and gates.
func (t *SwarmInfoTool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	id, ok := input.Arguments["id"].(string)
	if !ok || id == "" {
		return tool.Result{}, errors.New("id argument is required")
	}

	m, found := t.registry.Get(id)
	if !found {
		return tool.Result{}, fmt.Errorf("swarm %q not found", id)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Swarm: %s\n", m.ID)
	if m.Description != "" {
		fmt.Fprintf(&sb, "Description: %s\n", m.Description)
	}
	fmt.Fprintf(&sb, "Lead: %s\n", m.Lead)

	if len(m.Members) > 0 {
		fmt.Fprintf(&sb, "Members (%d): %s\n", len(m.Members), strings.Join(m.Members, ", "))
	} else {
		fmt.Fprintf(&sb, "Members: none\n")
	}

	if len(m.Harness.Gates) > 0 {
		fmt.Fprintf(&sb, "Gates (%d):\n", len(m.Harness.Gates))
		for _, g := range m.Harness.Gates {
			fmt.Fprintf(&sb, "  - %s (%s, %s", g.Name, g.Kind, g.When)
			if g.Target != "" {
				fmt.Fprintf(&sb, ", target=%s", g.Target)
			}
			if g.OutputKey != "" {
				fmt.Fprintf(&sb, ", key=%s", g.OutputKey)
			}
			fmt.Fprintf(&sb, ")\n")
		}
	} else {
		fmt.Fprintf(&sb, "Gates: none\n")
	}

	return tool.Result{Output: sb.String()}, nil
}
