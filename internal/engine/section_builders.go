package engine

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/swarm"
)

// buildDelegationSection builds a delegation table from registry agent metadata.
//
// Filters agents to only those with configured triggers and sorts alphabetically.
// Returns a markdown table with Agent, Cost, and When to use columns.
//
// Expected:
//   - agents is a slice of populated agent manifests.
//
// Returns:
//   - A markdown-formatted delegation table section, or empty string if no agents have triggers.
//
// Side effects:
//   - None.
func buildDelegationSection(agents []*agent.Manifest) string {
	var delegationAgents []*agent.Manifest
	for _, a := range agents {
		if len(a.OrchestratorMeta.Triggers) > 0 {
			delegationAgents = append(delegationAgents, a)
		}
	}

	if len(delegationAgents) == 0 {
		return ""
	}

	slices.SortFunc(delegationAgents, func(a, b *agent.Manifest) int {
		return strings.Compare(a.Name, b.Name)
	})

	var sb strings.Builder
	sb.WriteString("## Delegation Table\n\n")
	sb.WriteString("| Agent | Cost | When to use |\n")
	sb.WriteString("|---|---|---|\n")

	for _, a := range delegationAgents {
		sb.WriteString("| ")
		sb.WriteString(a.Name)
		sb.WriteString(" (")
		sb.WriteString(a.ID)
		sb.WriteString(") | ")
		sb.WriteString(a.OrchestratorMeta.Cost)
		sb.WriteString(" | ")
		useWhenText := ""
		if len(a.OrchestratorMeta.UseWhen) > 0 {
			useWhenText = a.OrchestratorMeta.UseWhen[0]
		}
		sb.WriteString(useWhenText)
		sb.WriteString(" |\n")
	}

	return sb.String()
}

// buildToolSelectionSection builds a tool selection table from registry agent metadata.
//
// Sorts agents by cost (FREE, CHEAP, EXPENSIVE) and includes agent name and description.
// Returns a markdown table suitable for orchestrator guidance on agent selection.
//
// Expected:
//   - agents is a slice of populated agent manifests.
//
// Returns:
//   - A markdown-formatted tool selection table section, or empty string if no agents available.
//
// Side effects:
//   - None.
func buildToolSelectionSection(agents []*agent.Manifest) string {
	if len(agents) == 0 {
		return ""
	}

	agentsCopy := make([]*agent.Manifest, len(agents))
	copy(agentsCopy, agents)

	costOrder := map[string]int{
		"FREE":      0,
		"CHEAP":     1,
		"EXPENSIVE": 2,
	}

	slices.SortFunc(agentsCopy, func(a, b *agent.Manifest) int {
		costA := costOrder[a.OrchestratorMeta.Cost]
		costB := costOrder[b.OrchestratorMeta.Cost]
		if costA != costB {
			return costA - costB
		}
		return strings.Compare(a.Name, b.Name)
	})

	var sb strings.Builder
	sb.WriteString("## Tool Selection\n\n")
	sb.WriteString("Choose agents by cost and capability:\n\n")
	sb.WriteString("| Agent | Cost | Description |\n")
	sb.WriteString("|---|---|---|\n")

	for _, a := range agentsCopy {
		sb.WriteString("| ")
		sb.WriteString(a.OrchestratorMeta.PromptAlias)
		sb.WriteString(" | ")
		sb.WriteString(a.OrchestratorMeta.Cost)
		sb.WriteString(" | ")
		sb.WriteString(a.Capabilities.CapabilityDescription)
		sb.WriteString(" |\n")
	}

	return sb.String()
}

// buildKeyTriggersSection builds a key triggers list from registry agent metadata.
//
// Filters agents with a configured KeyTrigger and builds a bullet list.
// Returns agent key triggers formatted for quick orchestrator reference.
//
// Expected:
//   - agents is a slice of populated agent manifests.
//
// Returns:
//   - A markdown-formatted key triggers section, or empty string if no agents have triggers.
//
// Side effects:
//   - None.
func buildKeyTriggersSection(agents []*agent.Manifest) string {
	var triggeredAgents []*agent.Manifest
	for _, a := range agents {
		if a.OrchestratorMeta.KeyTrigger != "" {
			triggeredAgents = append(triggeredAgents, a)
		}
	}

	if len(triggeredAgents) == 0 {
		return ""
	}

	slices.SortFunc(triggeredAgents, func(a, b *agent.Manifest) int {
		return strings.Compare(a.Name, b.Name)
	})

	var sb strings.Builder
	sb.WriteString("## Key Triggers\n\n")
	sb.WriteString("Delegate to agents when you identify these patterns:\n\n")

	for _, a := range triggeredAgents {
		sb.WriteString("- **")
		sb.WriteString(a.OrchestratorMeta.PromptAlias)
		sb.WriteString("**: ")
		sb.WriteString(a.OrchestratorMeta.KeyTrigger)
		sb.WriteString("\n")
	}

	return sb.String()
}

// filterByAllowlist returns only agents whose IDs are in the allowlist.
//
// Returns agents in the same order as they appear in the input slice.
//
// Expected:
//   - agents is a slice of populated agent manifests.
//   - allowlist is a slice of agent IDs to filter by.
//
// Returns:
//   - A slice of agents whose IDs are in the allowlist.
//
// Side effects:
//   - None.
func filterByAllowlist(agents []*agent.Manifest, allowlist []string) []*agent.Manifest {
	if len(allowlist) == 0 {
		return agents
	}

	allowlistMap := make(map[string]bool)
	for _, id := range allowlist {
		allowlistMap[id] = true
	}

	var filtered []*agent.Manifest
	for _, a := range agents {
		if allowlistMap[a.ID] {
			filtered = append(filtered, a)
		}
	}

	return filtered
}

// buildSwarmSection builds a table of all registered swarms so delegating
// agents discover swarms automatically without manual manifest updates.
// Each row shows the swarm ID, lead agent, member count, and description.
//
// Expected:
//   - reg is a non-nil swarm registry.
//
// Returns:
//   - A markdown-formatted swarms section, or empty string if no swarms exist.
//
// Side effects:
//   - None.
func buildSwarmSection(reg *swarm.Registry) string {
	manifests := reg.List()
	if len(manifests) == 0 {
		return ""
	}

	slices.SortFunc(manifests, func(a, b *swarm.Manifest) int {
		return strings.Compare(a.ID, b.ID)
	})

	var sb strings.Builder
	sb.WriteString("## Available Swarms\n\n")
	sb.WriteString("Delegate to a swarm using `delegate(subagent_type=\"<swarm-id>\", ...)`. ")
	sb.WriteString("The swarm's lead agent orchestrates the members automatically.\n\n")
	sb.WriteString("| Swarm | Lead | Members | Description |\n")
	sb.WriteString("|---|---|---|---|\n")

	for _, m := range manifests {
		sb.WriteString("| ")
		sb.WriteString(m.ID)
		sb.WriteString(" | ")
		sb.WriteString(m.Lead)
		sb.WriteString(" | ")
		desc := m.Description
		if len(desc) > 80 {
			desc = desc[:77] + "..."
		}
		desc = strings.ReplaceAll(desc, "\n", " ")
		desc = strings.TrimSpace(desc)
		sb.WriteString(fmt.Sprintf("%d", len(m.Members)))
		sb.WriteString(" | ")
		sb.WriteString(desc)
		sb.WriteString(" |\n")
	}

	return sb.String()
}

// buildTemporalSection returns a markdown section containing the current date
// so agents can reason about deadlines, schedules, and relative time. Without
// this block, agents like deadline-scanner cannot compute "within 7 days"
// because they have no reliable source for "today".
//
// Expected:
//   - nowFunc returns the current time. Tests inject a fixed value; production
//     passes time.Now.
//
// Returns:
//   - A markdown-formatted temporal context section.
//
// Side effects:
//   - None.
func buildTemporalSection(nowFunc func() time.Time) string {
	now := nowFunc().UTC()
	return "## Temporal Context\n\nToday is " + now.Format("2006-01-02") + " (" + now.Format("Monday") + ", UTC)"
}
