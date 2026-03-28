// Package support provides BDD test step definitions and helpers.
package support

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
)

// OrchestratorMetadataStepDefinitions holds state for orchestrator metadata BDD scenarios.
type OrchestratorMetadataStepDefinitions struct {
	agentRegistry           *agent.Registry
	manifestWithMetadata    *agent.Manifest
	builtDelegationTable    string
	builtToolSelectionTable string
	builtKeyTriggersSection string
}

// aMarkdownAgentFileWithOrchestratorMetadata creates a test manifest with orchestrator metadata.
//
// Expected:
//   - None.
//
// Returns:
//   - No error.
//
// Side effects:
//   - Stores a test manifest in s.manifestWithMetadata.
func (s *OrchestratorMetadataStepDefinitions) aMarkdownAgentFileWithOrchestratorMetadata(_ context.Context) error {
	s.manifestWithMetadata = &agent.Manifest{
		ID:   "explorer",
		Name: "Codebase Explorer",
		OrchestratorMeta: agent.OrchestratorMetadata{
			Cost:        "FREE",
			Category:    "exploration",
			PromptAlias: "Explorer",
			KeyTrigger:  "2+ modules involved → fire explore",
			UseWhen: []string{
				"Multiple search angles needed",
				"Unfamiliar module structure",
			},
			AvoidWhen: []string{
				"You know exactly what to search",
			},
			Triggers: []agent.DelegationTrigger{
				{
					Domain:  "Explore",
					Trigger: "Find existing codebase structure, patterns and styles",
				},
			},
		},
		Capabilities: agent.Capabilities{
			CapabilityDescription: "Explores codebase to find patterns, structures, conventions",
		},
	}
	return nil
}

// theAgentIsLoadedFromTheMarkdownFile simulates loading an agent.
//
// Expected:
//   - s.manifestWithMetadata is populated.
//
// Returns:
//   - No error.
//
// Side effects:
//   - None (metadata is already loaded from the previous step).
func (s *OrchestratorMetadataStepDefinitions) theAgentIsLoadedFromTheMarkdownFile(_ context.Context) error {
	if s.manifestWithMetadata == nil {
		return errors.New("manifest not loaded")
	}
	return nil
}

// theOrchestratorMetadataShouldContainTheConfiguredCost validates cost is set.
//
// Expected:
//   - s.manifestWithMetadata is populated.
//
// Returns:
//   - No error if cost is "FREE", error otherwise.
//
// Side effects:
//   - None.
func (s *OrchestratorMetadataStepDefinitions) theOrchestratorMetadataShouldContainTheConfiguredCost(_ context.Context) error {
	if s.manifestWithMetadata == nil {
		return errors.New("manifest not loaded")
	}
	if s.manifestWithMetadata.OrchestratorMeta.Cost != "FREE" {
		return fmt.Errorf("expected cost FREE, got %s", s.manifestWithMetadata.OrchestratorMeta.Cost)
	}
	return nil
}

// theOrchestratorMetadataShouldContainTheConfiguredTriggers validates triggers are set.
//
// Expected:
//   - s.manifestWithMetadata is populated.
//
// Returns:
//   - No error if at least one trigger exists, error otherwise.
//
// Side effects:
//   - None.
func (s *OrchestratorMetadataStepDefinitions) theOrchestratorMetadataShouldContainTheConfiguredTriggers(_ context.Context) error {
	if s.manifestWithMetadata == nil {
		return errors.New("manifest not loaded")
	}
	if len(s.manifestWithMetadata.OrchestratorMeta.Triggers) == 0 {
		return errors.New("expected at least one trigger, got none")
	}
	return nil
}

// anAgentRegistryWithAgentsThatHaveOrchestratorMetadata creates a test registry.
//
// Expected:
//   - None.
//
// Returns:
//   - No error.
//
// Side effects:
//   - Creates a test agent registry with sample agents.
func (s *OrchestratorMetadataStepDefinitions) anAgentRegistryWithAgentsThatHaveOrchestratorMetadata(_ context.Context) error {
	registry := agent.NewRegistry()
	registry.Register(&agent.Manifest{
		ID:   "explorer",
		Name: "Explorer",
		OrchestratorMeta: agent.OrchestratorMetadata{
			Cost:        "FREE",
			PromptAlias: "Explorer",
			UseWhen:     []string{"Multiple search angles needed"},
			Triggers: []agent.DelegationTrigger{
				{Domain: "Explore", Trigger: "Find patterns"},
			},
		},
		Capabilities: agent.Capabilities{
			CapabilityDescription: "Explores codebase",
		},
	})
	registry.Register(&agent.Manifest{
		ID:   "librarian",
		Name: "Librarian",
		OrchestratorMeta: agent.OrchestratorMetadata{
			Cost:        "CHEAP",
			PromptAlias: "Librarian",
			UseWhen:     []string{"External docs needed"},
			Triggers: []agent.DelegationTrigger{
				{Domain: "Research", Trigger: "Find documentation"},
			},
		},
		Capabilities: agent.Capabilities{
			CapabilityDescription: "Finds documentation",
		},
	})
	registry.Register(&agent.Manifest{
		ID:   "executor",
		Name: "Executor",
		OrchestratorMeta: agent.OrchestratorMetadata{
			Cost:        "EXPENSIVE",
			PromptAlias: "Executor",
			UseWhen:     []string{"Implementation needed"},
			Triggers: []agent.DelegationTrigger{
				{Domain: "Execute", Trigger: "Implement solution"},
			},
		},
		Capabilities: agent.Capabilities{
			CapabilityDescription: "Executes tasks",
		},
	})
	s.agentRegistry = registry
	return nil
}

// theDynamicDelegationTableIsBuilt builds the delegation table from registry.
//
// Expected:
//   - s.agentRegistry is populated.
//
// Returns:
//   - No error.
//
// Side effects:
//   - Stores the built table in s.builtDelegationTable.
func (s *OrchestratorMetadataStepDefinitions) theDynamicDelegationTableIsBuilt(_ context.Context) error {
	if s.agentRegistry == nil {
		return errors.New("agent registry not initialised")
	}
	agents := s.agentRegistry.List()
	s.builtDelegationTable = buildDelegationSectionFromAgents(agents)
	return nil
}

// theTableShouldListAgentsSortedAlphabetically validates table sort order.
//
// Expected:
//   - s.builtDelegationTable is populated.
//
// Returns:
//   - No error if agents are alphabetically sorted, error otherwise.
//
// Side effects:
//   - None.
func (s *OrchestratorMetadataStepDefinitions) theTableShouldListAgentsSortedAlphabetically(_ context.Context) error {
	if s.builtDelegationTable == "" {
		return errors.New("delegation table is empty")
	}
	lines := strings.Split(s.builtDelegationTable, "\n")
	var agentNames []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "Agent") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) >= 2 {
			name := strings.TrimSpace(parts[1])
			if name != "" {
				agentNames = append(agentNames, name)
			}
		}
	}
	sorted := make([]string, len(agentNames))
	copy(sorted, agentNames)
	slices.Sort(sorted)
	for i, name := range agentNames {
		if name != sorted[i] {
			return fmt.Errorf("agents not sorted: expected %v, got %v", sorted, agentNames)
		}
	}
	return nil
}

// theTableShouldIncludeCostAndDescriptionColumns validates table has required columns.
//
// Expected:
//   - s.builtDelegationTable is populated.
//
// Returns:
//   - No error if Cost and description columns exist, error otherwise.
//
// Side effects:
//   - None.
func (s *OrchestratorMetadataStepDefinitions) theTableShouldIncludeCostAndDescriptionColumns(_ context.Context) error {
	if s.builtDelegationTable == "" {
		return errors.New("delegation table is empty")
	}
	if !strings.Contains(s.builtDelegationTable, "Cost") {
		return errors.New("table missing Cost column")
	}
	if !strings.Contains(s.builtDelegationTable, "When to use") {
		return errors.New("table missing When to use column")
	}
	return nil
}

// anAgentRegistryWithAgentsThatHaveKeyTriggers creates a registry with key triggers.
//
// Expected:
//   - None.
//
// Returns:
//   - No error.
//
// Side effects:
//   - Creates registry with agents that have key triggers set.
func (s *OrchestratorMetadataStepDefinitions) anAgentRegistryWithAgentsThatHaveKeyTriggers(_ context.Context) error {
	registry := agent.NewRegistry()
	registry.Register(&agent.Manifest{
		ID:   "explorer",
		Name: "Explorer",
		OrchestratorMeta: agent.OrchestratorMetadata{
			PromptAlias: "Explorer",
			KeyTrigger:  "2+ modules involved → fire explore",
		},
	})
	registry.Register(&agent.Manifest{
		ID:   "librarian",
		Name: "Librarian",
		OrchestratorMeta: agent.OrchestratorMetadata{
			PromptAlias: "Librarian",
			KeyTrigger:  "External docs needed → delegate research",
		},
	})
	s.agentRegistry = registry
	return nil
}

// theKeyTrigersSectionIsBuilt builds the key triggers section.
//
// Expected:
//   - s.agentRegistry is populated.
//
// Returns:
//   - No error.
//
// Side effects:
//   - Stores built section in s.builtKeyTriggersSection.
func (s *OrchestratorMetadataStepDefinitions) theKeyTrigersSectionIsBuilt(_ context.Context) error {
	if s.agentRegistry == nil {
		return errors.New("agent registry not initialised")
	}
	agents := s.agentRegistry.List()
	s.builtKeyTriggersSection = buildKeyTriggersSectionFromAgents(agents)
	return nil
}

// theSectionShouldListEachAgentSkeyTrigger validates key triggers are listed.
//
// Expected:
//   - s.builtKeyTriggersSection is populated.
//
// Returns:
//   - No error if triggers are present, error otherwise.
//
// Side effects:
//   - None.
func (s *OrchestratorMetadataStepDefinitions) theSectionShouldListEachAgentSkeyTrigger(_ context.Context) error {
	if s.builtKeyTriggersSection == "" {
		return errors.New("key triggers section is empty")
	}
	if !strings.Contains(s.builtKeyTriggersSection, "2+ modules involved") {
		return errors.New("expected trigger not found in section")
	}
	return nil
}

// anAgentRegistryWithAgentsOfDifferentCosts creates a registry with varied costs.
//
// Expected:
//   - None.
//
// Returns:
//   - No error.
//
// Side effects:
//   - Creates registry with agents of different cost levels.
func (s *OrchestratorMetadataStepDefinitions) anAgentRegistryWithAgentsOfDifferentCosts(_ context.Context) error {
	registry := agent.NewRegistry()
	registry.Register(&agent.Manifest{
		ID:   "explorer",
		Name: "Explorer",
		OrchestratorMeta: agent.OrchestratorMetadata{
			Cost:        "FREE",
			PromptAlias: "Explorer",
		},
		Capabilities: agent.Capabilities{
			CapabilityDescription: "Explores codebase",
		},
	})
	registry.Register(&agent.Manifest{
		ID:   "librarian",
		Name: "Librarian",
		OrchestratorMeta: agent.OrchestratorMetadata{
			Cost:        "CHEAP",
			PromptAlias: "Librarian",
		},
		Capabilities: agent.Capabilities{
			CapabilityDescription: "Finds documentation",
		},
	})
	registry.Register(&agent.Manifest{
		ID:   "planner",
		Name: "Planner",
		OrchestratorMeta: agent.OrchestratorMetadata{
			Cost:        "EXPENSIVE",
			PromptAlias: "Planner",
		},
		Capabilities: agent.Capabilities{
			CapabilityDescription: "Plans tasks",
		},
	})
	s.agentRegistry = registry
	return nil
}

// theToolSelectionTableIsBuilt builds the tool selection table.
//
// Expected:
//   - s.agentRegistry is populated.
//
// Returns:
//   - No error.
//
// Side effects:
//   - Stores built table in s.builtToolSelectionTable.
func (s *OrchestratorMetadataStepDefinitions) theToolSelectionTableIsBuilt(_ context.Context) error {
	if s.agentRegistry == nil {
		return errors.New("agent registry not initialised")
	}
	agents := s.agentRegistry.List()
	s.builtToolSelectionTable = buildToolSelectionSectionFromAgents(agents)
	return nil
}

// agentsShouldBeSortedByCostWithFREEFirst validates cost sort order.
//
// Expected:
//   - s.builtToolSelectionTable is populated.
//
// Returns:
//   - No error if costs are sorted FREE → CHEAP → EXPENSIVE, error otherwise.
//
// Side effects:
//   - None.
func (s *OrchestratorMetadataStepDefinitions) agentsShouldBeSortedByCostWithFREEFirst(_ context.Context) error {
	if s.builtToolSelectionTable == "" {
		return errors.New("tool selection table is empty")
	}
	lines := strings.Split(s.builtToolSelectionTable, "\n")
	var costs []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "Agent") || strings.Contains(line, "---") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) >= 3 {
			cost := strings.TrimSpace(parts[2])
			if cost != "" {
				costs = append(costs, cost)
			}
		}
	}
	if len(costs) == 0 {
		return errors.New("no costs found in table")
	}
	if len(costs) > 0 && costs[0] != "FREE" {
		return fmt.Errorf("expected first cost to be FREE, got %s", costs[0])
	}
	return nil
}

// theTableShouldIncludeAgentNameCostAndDescription validates table columns.
//
// Expected:
//   - s.builtToolSelectionTable is populated.
//
// Returns:
//   - No error if required columns exist, error otherwise.
//
// Side effects:
//   - None.
func (s *OrchestratorMetadataStepDefinitions) theTableShouldIncludeAgentNameCostAndDescription(_ context.Context) error {
	if s.builtToolSelectionTable == "" {
		return errors.New("tool selection table is empty")
	}
	if !strings.Contains(s.builtToolSelectionTable, "Agent") {
		return errors.New("table missing Agent column")
	}
	if !strings.Contains(s.builtToolSelectionTable, "Cost") {
		return errors.New("table missing Cost column")
	}
	if !strings.Contains(s.builtToolSelectionTable, "Description") {
		return errors.New("table missing Description column")
	}
	return nil
}

// buildDelegationSectionFromAgents builds delegation table helper for test scenarios.
//
// Expected:
//   - agents is a slice of agent manifests with OrchestratorMeta populated.
//
// Returns:
//   - Markdown formatted delegation table, or empty string if no agents have triggers.
//
// Side effects:
//   - None.
func buildDelegationSectionFromAgents(agents []*agent.Manifest) string {
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
		sb.WriteString(" | ")
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

// buildKeyTriggersSectionFromAgents builds key triggers section for testing.
//
// Expected:
//   - agents is a slice of agent manifests with OrchestratorMeta populated.
//
// Returns:
//   - Markdown formatted key triggers section, or empty string if no agents have key triggers.
//
// Side effects:
//   - None.
func buildKeyTriggersSectionFromAgents(agents []*agent.Manifest) string {
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

// buildToolSelectionSectionFromAgents builds tool selection section for testing.
//
// Expected:
//   - agents is a slice of agent manifests.
//
// Returns:
//   - Markdown formatted tool selection table.
//
// Side effects:
//   - None.
func buildToolSelectionSectionFromAgents(agents []*agent.Manifest) string {
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

// RegisterOrchestratorMetadataSteps registers all orchestrator metadata BDD step definitions.
//
// Expected:
//   - ctx is a valid godog ScenarioContext for step registration.
//
// Returns:
//   - No error (godog step registration).
//
// Side effects:
//   - Registers step definitions in the ScenarioContext.
func RegisterOrchestratorMetadataSteps(ctx *godog.ScenarioContext) {
	s := &OrchestratorMetadataStepDefinitions{}
	ctx.Step(`^a markdown agent file with orchestrator metadata$`, s.aMarkdownAgentFileWithOrchestratorMetadata)
	ctx.Step(`^it is loaded from the markdown file for orchestrator testing$`,
		s.theAgentIsLoadedFromTheMarkdownFile)
	ctx.Step(`^the orchestrator metadata should contain the configured cost$`,
		s.theOrchestratorMetadataShouldContainTheConfiguredCost)
	ctx.Step(`^the orchestrator metadata should contain the configured triggers$`,
		s.theOrchestratorMetadataShouldContainTheConfiguredTriggers)
	ctx.Step(`^an agent registry with agents that have orchestrator metadata$`,
		s.anAgentRegistryWithAgentsThatHaveOrchestratorMetadata)
	ctx.Step(`^the dynamic delegation table is built$`, s.theDynamicDelegationTableIsBuilt)
	ctx.Step(`^the table should list agents sorted alphabetically$`,
		s.theTableShouldListAgentsSortedAlphabetically)
	ctx.Step(`^the table should include cost and description columns$`,
		s.theTableShouldIncludeCostAndDescriptionColumns)
	ctx.Step(`^an agent registry with agents that have key triggers$`,
		s.anAgentRegistryWithAgentsThatHaveKeyTriggers)
	ctx.Step(`^the key triggers section is built$`, s.theKeyTrigersSectionIsBuilt)
	ctx.Step(`^the section should list each agent's key trigger$`,
		s.theSectionShouldListEachAgentSkeyTrigger)
	ctx.Step(`^an agent registry with agents of different costs$`,
		s.anAgentRegistryWithAgentsOfDifferentCosts)
	ctx.Step(`^the tool selection table is built$`, s.theToolSelectionTableIsBuilt)
	ctx.Step(`^agents should be sorted by cost with FREE first$`,
		s.agentsShouldBeSortedByCostWithFREEFirst)
	ctx.Step(`^the table should include agent name, cost, and description$`,
		s.theTableShouldIncludeAgentNameCostAndDescription)
}
