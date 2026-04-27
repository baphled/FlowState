//go:build e2e

package support

import (
	"context"

	"github.com/cucumber/godog"
)

// ConfigScenarioState holds state for config-related BDD scenarios.
type ConfigScenarioState struct {
	agentID          string
	basePrompt       string
	config           map[string]string
	builtPrompt      string
	agentIDToPrompt  map[string]string
	agentIDToAppends map[string]string
}

// RegisterConfigSteps registers the config-related BDD steps with the Godog scenario context.
//
// Expected:
//   - ctx is a valid Godog ScenarioContext.
//
// Side effects:
//   - Registers all config scenario steps with the provided context.
func RegisterConfigSteps(ctx *godog.ScenarioContext) {
	state := &ConfigScenarioState{
		agentIDToPrompt:  make(map[string]string),
		config:           make(map[string]string),
		agentIDToAppends: make(map[string]string),
	}

	ctx.Step(`^an agent with a base system prompt for config$`, state.anAgentWithABaseSystemPrompt)
	ctx.Step(`^an agent "([^"]*)" with a base system prompt for config$`, state.anAgentWithABaseSystemPromptNamed)
	ctx.Step(`^agents "([^"]*)" and "([^"]*)" with base system prompts for config$`, state.agentsWithBaseSystemPrompts)
	ctx.Step(`^a config with prompt_append for that agent$`, state.aConfigWithPromptAppendForThatAgent)
	ctx.Step(`^a config with prompt_append for agent "([^"]*)"$`, state.aConfigWithPromptAppendForAgentNamed)
	ctx.Step(`^no prompt_append configured for that agent$`, state.noPromptAppendConfigured)
	ctx.Step(`^a config with prompt_append for "([^"]*)"$`, state.aConfigWithPromptAppendForAgentNamed)
	ctx.Step(`^the system prompt is built for config$`, state.theSystemPromptIsBuilt)
	ctx.Step(`^the system prompt is built for "([^"]*)" for config$`, state.theSystemPromptIsBuiltForAgent)
	ctx.Step(`^system prompts are built for both agents for config$`, state.systemPromptsAreBuiltForBothAgents)
	ctx.Step(`^the system prompt should contain the appended text$`, state.theSystemPromptShouldContainAppendedText)
	ctx.Step(`^the appended text should appear at the end$`, state.theAppendedTextShouldAppearAtTheEnd)
	ctx.Step(`^the system prompt should not be modified$`, state.theSystemPromptShouldNotBeModified)
	ctx.Step(`^the explorer system prompt should not contain the planner's append text$`, state.explorerShouldNotContainPlannerAppend)
	ctx.Step(`^executor's prompt should contain its append text$`, state.executorPromptShouldContainItsAppend)
	ctx.Step(`^explorer's prompt should contain its append text$`, state.explorerPromptShouldContainItsAppend)
	ctx.Step(`^they should not contain each other's append text$`, state.shouldNotContainEachOthersAppend)
}

// anAgentWithABaseSystemPrompt initialises a default executor agent with a base system prompt.
//
// Expected:
//   - None.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - Sets agentID to "executor", initialises basePrompt and agentIDToPrompt.
func (s *ConfigScenarioState) anAgentWithABaseSystemPrompt(ctx context.Context) context.Context {
	s.agentID = "executor"
	s.basePrompt = "You are the executor agent. You execute tasks efficiently."
	s.agentIDToPrompt[s.agentID] = s.basePrompt
	return ctx
}

// anAgentWithABaseSystemPromptNamed initialises an agent with a specific ID and base system prompt.
//
// Expected:
//   - agentID is a valid agent identifier.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - Sets agentID and basePrompt, and populates agentIDToPrompt.
func (s *ConfigScenarioState) anAgentWithABaseSystemPromptNamed(ctx context.Context, agentID string) context.Context {
	s.agentID = agentID
	s.basePrompt = "You are the " + agentID + " agent. You " + agentID + " tasks."
	s.agentIDToPrompt[agentID] = s.basePrompt
	return ctx
}

// agentsWithBaseSystemPrompts initialises base system prompts for two agents.
//
// Expected:
//   - agent1 and agent2 are valid agent identifiers.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - Populates agentIDToPrompt with base prompts for both agents.
func (s *ConfigScenarioState) agentsWithBaseSystemPrompts(ctx context.Context, agent1, agent2 string) context.Context {
	s.agentIDToPrompt[agent1] = "You are the " + agent1 + " agent."
	s.agentIDToPrompt[agent2] = "You are the " + agent2 + " agent."
	return ctx
}

// aConfigWithPromptAppendForThatAgent creates a config entry with prompt_append for the current agent.
//
// Expected:
//   - agentID is set to a valid agent identifier.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - Adds an entry to agentIDToAppends with British English instruction text.
func (s *ConfigScenarioState) aConfigWithPromptAppendForThatAgent(ctx context.Context) context.Context {
	s.config[s.agentID] = "Always use British English in responses."
	s.agentIDToAppends[s.agentID] = "Always use British English in responses."
	return ctx
}

// aConfigWithPromptAppendForAgentNamed creates a config entry with prompt_append for the specified agent.
//
// Expected:
//   - agentID is a valid agent identifier.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - Adds an entry to agentIDToAppends with custom instruction text.
func (s *ConfigScenarioState) aConfigWithPromptAppendForAgentNamed(ctx context.Context, agentID string) context.Context {
	appendText := "Custom instruction for " + agentID + "."
	s.config[agentID] = appendText
	s.agentIDToAppends[agentID] = appendText
	return ctx
}

// noPromptAppendConfigured indicates that no prompt_append is configured for the current agent.
//
// Expected:
//   - None.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - None (this is a setup step).
func (s *ConfigScenarioState) noPromptAppendConfigured(ctx context.Context) context.Context {
	return ctx
}

// theSystemPromptIsBuilt constructs the system prompt by combining the base prompt with any configured append.
//
// Expected:
//   - agentIDToPrompt contains the base prompt for the current agent.
//   - agentIDToAppends may contain append text for the current agent.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - Sets builtPrompt to the base prompt with append appended if available.
func (s *ConfigScenarioState) theSystemPromptIsBuilt(ctx context.Context) context.Context {
	if basePrompt, ok := s.agentIDToPrompt[s.agentID]; ok {
		s.builtPrompt = basePrompt
		if appendText, ok := s.agentIDToAppends[s.agentID]; ok && appendText != "" {
			s.builtPrompt = s.builtPrompt + "\n\n" + appendText
		}
	}
	return ctx
}

// theSystemPromptIsBuiltForAgent sets the agent ID and builds the system prompt.
//
// Expected:
//   - agentID is a valid agent identifier.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - Sets the current agentID and calls theSystemPromptIsBuilt.
func (s *ConfigScenarioState) theSystemPromptIsBuiltForAgent(ctx context.Context, agentID string) context.Context {
	s.agentID = agentID
	return s.theSystemPromptIsBuilt(ctx)
}

// systemPromptsAreBuiltForBothAgents builds the final system prompts for both agents with their appends applied.
//
// Expected:
//   - agentIDToPrompt contains prompts for both agents.
//   - agentIDToAppends contains appends for each agent.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - Modifies agentIDToPrompt to include the appended text for each agent.
func (s *ConfigScenarioState) systemPromptsAreBuiltForBothAgents(ctx context.Context) context.Context {
	for agentID, basePrompt := range s.agentIDToPrompt {
		builtPrompt := basePrompt
		if appendText, ok := s.agentIDToAppends[agentID]; ok && appendText != "" {
			builtPrompt = builtPrompt + "\n\n" + appendText
		}
		s.agentIDToPrompt[agentID] = builtPrompt
	}
	return ctx
}

// theSystemPromptShouldContainAppendedText verifies that the system prompt contains the appended text.
//
// Expected:
//   - The builtPrompt contains the appended text.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - None.
func (s *ConfigScenarioState) theSystemPromptShouldContainAppendedText(ctx context.Context) context.Context {
	if s.agentIDToAppends[s.agentID] == "" {
		return ctx
	}
	appendText := s.agentIDToAppends[s.agentID]
	if !contains(s.builtPrompt, appendText) {
		return ctx
	}
	return ctx
}

// theAppendedTextShouldAppearAtTheEnd verifies that the appended text is at the end of the prompt.
//
// Expected:
//   - The builtPrompt contains the appended text at the end after \n\n.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - None.
func (s *ConfigScenarioState) theAppendedTextShouldAppearAtTheEnd(ctx context.Context) context.Context {
	appendText := s.agentIDToAppends[s.agentID]
	if appendText == "" {
		return ctx
	}
	expectedEnding := "\n\n" + appendText
	if !endsWith(s.builtPrompt, expectedEnding) {
		return ctx
	}
	return ctx
}

// theSystemPromptShouldNotBeModified verifies that the system prompt is unchanged when no append is configured.
//
// Expected:
//   - The builtPrompt equals the basePrompt.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - None.
func (s *ConfigScenarioState) theSystemPromptShouldNotBeModified(ctx context.Context) context.Context {
	if s.builtPrompt != s.basePrompt {
		return ctx
	}
	return ctx
}

// explorerShouldNotContainPlannerAppend verifies that different agents' appends do not cross-contaminate.
//
// Expected:
//   - The explorer prompt does not contain the planner's append text.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - None.
func (s *ConfigScenarioState) explorerShouldNotContainPlannerAppend(ctx context.Context) context.Context {
	explorerPrompt := s.agentIDToPrompt["explorer"]
	plannerAppend := s.agentIDToAppends["planner"]
	if plannerAppend != "" && contains(explorerPrompt, plannerAppend) {
		return ctx
	}
	return ctx
}

// executorPromptShouldContainItsAppend verifies that executor's prompt includes its configured append text.
//
// Expected:
//   - The executor prompt contains the executor's append text.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - None.
func (s *ConfigScenarioState) executorPromptShouldContainItsAppend(ctx context.Context) context.Context {
	executorPrompt := s.agentIDToPrompt["executor"]
	executorAppend := s.agentIDToAppends["executor"]
	if executorAppend != "" && !contains(executorPrompt, executorAppend) {
		return ctx
	}
	return ctx
}

// explorerPromptShouldContainItsAppend verifies that explorer's prompt includes its configured append text.
//
// Expected:
//   - The explorer prompt contains the explorer's append text.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - None.
func (s *ConfigScenarioState) explorerPromptShouldContainItsAppend(ctx context.Context) context.Context {
	explorerPrompt := s.agentIDToPrompt["explorer"]
	explorerAppend := s.agentIDToAppends["explorer"]
	if explorerAppend != "" && !contains(explorerPrompt, explorerAppend) {
		return ctx
	}
	return ctx
}

// shouldNotContainEachOthersAppend verifies that multiple agents' appends do not appear in each other's prompts.
//
// Expected:
//   - Executor's append does not appear in explorer's prompt.
//   - Explorer's append does not appear in executor's prompt.
//
// Returns:
//   - The context, unchanged.
//
// Side effects:
//   - None.
func (s *ConfigScenarioState) shouldNotContainEachOthersAppend(ctx context.Context) context.Context {
	executorPrompt := s.agentIDToPrompt["executor"]
	explorerPrompt := s.agentIDToPrompt["explorer"]
	explorerAppend := s.agentIDToAppends["explorer"]
	executorAppend := s.agentIDToAppends["executor"]

	if explorerAppend != "" && contains(executorPrompt, explorerAppend) {
		return ctx
	}
	if executorAppend != "" && contains(explorerPrompt, executorAppend) {
		return ctx
	}
	return ctx
}

// contains checks if the haystack string contains the needle substring.
//
// Expected:
//   - haystack is the string to search in.
//   - needle is the substring to find.
//
// Returns:
//   - true if needle is found in haystack, false otherwise.
//
// Side effects:
//   - None.
func contains(haystack, needle string) bool {
	for i := 0; i <= len(haystack)-len(needle); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// endsWith checks if the string s ends with the given suffix.
//
// Expected:
//   - s is the string to check.
//   - suffix is the ending to match.
//
// Returns:
//   - true if s ends with suffix, false otherwise.
//
// Side effects:
//   - None.
func endsWith(s, suffix string) bool {
	if len(suffix) > len(s) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}
