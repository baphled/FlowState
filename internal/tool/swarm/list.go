package swarm

import (
	"context"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

// SwarmListTool lists every registered swarm with its id, lead, member count, and gate count.
type SwarmListTool struct {
	registry SwarmReader
}

// NewSwarmListTool creates a SwarmListTool backed by the given registry.
//
// Expected: parameters for NewSwarmListTool.
// Returns: result of NewSwarmListTool.
// Side effects: None.
func NewSwarmListTool(registry SwarmReader) *SwarmListTool {
	return &SwarmListTool{registry: registry}
}

// Name ...
//
// Expected: parameters for Name.
//
// Returns: result of Name.
//
// Side effects: None.
func (t *SwarmListTool) Name() string { return "swarm_list" }

// Description ...
//
// Expected: parameters for Description.
//
// Returns: result of Description.
//
// Side effects: None.
func (t *SwarmListTool) Description() string { return "List all registered swarms" }

// Schema ...
//
// Expected: parameters for Schema.
//
// Returns: result of Schema.
//
// Side effects: None.
func (t *SwarmListTool) Schema() tool.Schema {
	return tool.Schema{Type: "object", Properties: map[string]tool.Property{}}
}

// Execute returns a formatted table of all registered swarms.
//
// Expected: parameters for Execute.
// Returns: result of Execute.
// Side effects: None.
func (t *SwarmListTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	manifests := t.registry.List()
	if len(manifests) == 0 {
		return tool.Result{Output: "no swarms registered"}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%-20s %-20s %7s %5s\n", "ID", "LEAD", "MEMBERS", "GATES")
	fmt.Fprintf(&sb, "%s\n", strings.Repeat("-", 58))
	for _, m := range manifests {
		fmt.Fprintf(&sb, "%-20s %-20s %7d %5d\n",
			m.ID, m.Lead, len(m.Members), len(m.Harness.Gates))
	}
	return tool.Result{Output: sb.String()}, nil
}
