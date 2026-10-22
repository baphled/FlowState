package engine

import (
	"slices"
	"strconv"
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
		sb.WriteString(strconv.Itoa(len(m.Members)))
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

// fileToolSet is the set of capability tools that trigger the
// tool-discipline section. Agents without any of these tools (e.g.
// pure-orchestration agents) never see file-tool guidance.
var fileToolSet = map[string]bool{
	"bash":  true,
	"read":  true,
	"write": true,
	"edit":  true,
}

// buildToolDisciplineSection renders the Tool Discipline section that
// steers agents towards purpose-built file tools and economical tool
// use. Agents repeatedly fell back to bash for file CRUD (cat >, tee,
// heredocs, sed -i) and issued one-tool-call-per-message, burning
// tokens and inviting shell-quoting bugs; this section makes the
// expected behaviour explicit at prompt level.
//
// The section is only rendered when the manifest actually has at
// least one file tool — an agent without read/write/edit/bash would
// otherwise receive guidance about tools it cannot call.
//
// Expected:
//   - manifest is the manifest the prompt is being built for; its
//     Capabilities.Tools list is inspected.
//
// Returns:
//   - A markdown Tool Discipline section, or an empty string when the
//     manifest has none of the file tools.
//
// Side effects:
//   - None.
func buildToolDisciplineSection(manifest agent.Manifest) string {
	has := false
	for _, t := range manifest.Capabilities.Tools {
		if fileToolSet[t] {
			has = true
			break
		}
	}
	if !has {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Tool Discipline\n\n")
	b.WriteString("Use purpose-built file tools, not bash:\n")
	b.WriteString("- Creating a new file → use the `write` tool\n")
	b.WriteString("- Modifying an existing file → use the `edit` tool\n")
	b.WriteString("- Reading files → use the `read` tool (with offset/limit for large files)\n")
	b.WriteString("- Never create or modify files via bash (`cat >`, `tee`, heredocs, `sed -i`, `awk -i`) when `write`/`edit` are available; never read via `cat`/`head`/`tail` when `read` is available\n")
	b.WriteString("- Bash is for builds, tests, linting, git, and process/system inspection only\n\n")
	b.WriteString("Tool-call economy — use the least number of tool calls possible:\n")
	b.WriteString("- Batch independent tool calls in a single message\n")
	b.WriteString("- Read only the region of a file you need (offset/limit)\n")
	b.WriteString("- Prefer one precise edit over multiple rewrites")
	return b.String()
}
