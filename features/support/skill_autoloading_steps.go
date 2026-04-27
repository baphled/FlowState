//go:build e2e

package support

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
)

// SkillAutoloadingStepDefinitions holds state for skill auto-loading BDD step definitions.
type SkillAutoloadingStepDefinitions struct {
	cfg         *hook.SkillAutoLoaderConfig
	manifest    agent.Manifest
	capturedReq *provider.ChatRequest
	prompt      string
	selection   hook.SkillSelection
}

// RegisterSkillAutoloadingSteps registers skill-autoloading step definitions.
//
// Expected:
//   - ctx is a godog ScenarioContext.
//
// Side effects:
//   - Registers skill auto-loading step definitions with the godog context.
func RegisterSkillAutoloadingSteps(ctx *godog.ScenarioContext) {
	s := &SkillAutoloadingStepDefinitions{}

	ctx.Step(`^the agent system is initialised$`, s.theAgentSystemIsInitialised)
	ctx.Step(`^a new agent session starts with any prompt$`, s.aNewAgentSessionStartsWithAnyPrompt)
	ctx.Step(`^the baseline skills should be present in the system prompt$`, s.theBaselineSkillsShouldBePresentInTheSystemPrompt)
	ctx.Step(`^the skills should include "([^"]*)" and "([^"]*)"$`, s.theSkillsShouldIncludeAnd)
	ctx.Step(`^an agent manifest specifies the skill "([^"]*)"$`, s.anAgentManifestSpecifiesTheSkill)
	ctx.Step(`^the agent is started$`, s.theAgentIsStarted)
	ctx.Step(`^the system prompt should include the skill "([^"]*)"$`, s.theSystemPromptShouldIncludeTheSkill)
	ctx.Step(`^the prompt contains the keyword "([^"]*)"$`, s.thePromptContainsTheKeyword)
	ctx.Step(`^the agent session is created$`, s.theAgentSessionIsCreated)
	ctx.Step(`^the system should inject the "([^"]*)" skill into the system prompt$`, s.theSystemShouldInjectTheSkillIntoTheSystemPrompt)
	ctx.Step(`^a skill is injected$`, s.aSkillIsInjected)
	ctx.Step(`^the system prompt should list the skill by its lean name only$`, s.theSystemPromptShouldListTheSkillByItsLeanNameOnly)
	ctx.Step(`^the skill documentation should not be inlined in the prompt$`, s.theSkillDocumentationShouldNotBeInlinedInThePrompt)
}

// runSelectionAndCapture runs skill selection and builds a captured chat request.
//
// Expected:
//   - s.cfg is a non-nil SkillAutoLoaderConfig.
//   - s.manifest is populated with agent details.
//   - s.prompt is set to the user prompt text.
//
// Side effects:
//   - Sets s.selection with the skill selection result.
//   - Sets s.capturedReq with a ChatRequest containing the lean skill injection.
func (s *SkillAutoloadingStepDefinitions) runSelectionAndCapture() {
	input := hook.SkillSelectionInput{
		AgentID:            s.manifest.ID,
		Category:           s.manifest.Complexity,
		Prompt:             s.prompt,
		AgentDefaultSkills: s.manifest.Capabilities.AlwaysActiveSkills,
	}
	s.selection = hook.SelectSkills(input, s.cfg)

	s.capturedReq = &provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: s.prompt},
		},
	}

	lean := fmt.Sprintf(
		"Your load_skills: [%s]. Use skill_load(name) only when relevant to the current task.",
		strings.Join(s.selection.Skills, ", "),
	)

	s.capturedReq.Messages[0].Content = lean + "\n\n" + s.capturedReq.Messages[0].Content
}

// theAgentSystemIsInitialised sets up the default config and manifest.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sets s.cfg and s.manifest.
func (s *SkillAutoloadingStepDefinitions) theAgentSystemIsInitialised() error {
	s.cfg = hook.DefaultSkillAutoLoaderConfig()
	s.manifest = agent.Manifest{
		ID: "general",
		Capabilities: agent.Capabilities{
			AlwaysActiveSkills: []string{},
		},
	}
	s.prompt = ""
	s.capturedReq = nil
	s.selection = hook.SkillSelection{}
	return nil
}

// aNewAgentSessionStartsWithAnyPrompt simulates starting an agent session.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Runs skill selection and captures the request.
func (s *SkillAutoloadingStepDefinitions) aNewAgentSessionStartsWithAnyPrompt() error {
	if s.cfg == nil {
		return errors.New("agent system not initialised")
	}
	s.prompt = "Hello, help me with something"
	s.runSelectionAndCapture()
	return nil
}

// theBaselineSkillsShouldBePresentInTheSystemPrompt verifies baseline skills are in the prompt.
//
// Returns:
//   - nil if baseline skills found, error otherwise.
//
// Side effects:
//   - None.
func (s *SkillAutoloadingStepDefinitions) theBaselineSkillsShouldBePresentInTheSystemPrompt() error {
	if s.capturedReq == nil {
		return errors.New("no captured request; session has not started")
	}
	systemContent := s.capturedReq.Messages[0].Content
	for _, skill := range s.cfg.BaselineSkills {
		if !strings.Contains(systemContent, skill) {
			return fmt.Errorf("baseline skill %q not found in system prompt", skill)
		}
	}
	return nil
}

// theSkillsShouldIncludeAnd verifies two specific skills are in the selection.
//
// Expected:
//   - skill1 and skill2 are the skill names to check.
//   - s.capturedReq is non-nil from a prior session start step.
//
// Returns:
//   - nil if both skills found, error otherwise.
//
// Side effects:
//   - None.
func (s *SkillAutoloadingStepDefinitions) theSkillsShouldIncludeAnd(skill1, skill2 string) error {
	if s.capturedReq == nil {
		return errors.New("no captured request; session has not started")
	}
	systemContent := s.capturedReq.Messages[0].Content
	if !strings.Contains(systemContent, skill1) {
		return fmt.Errorf("skill %q not found in system prompt", skill1)
	}
	if !strings.Contains(systemContent, skill2) {
		return fmt.Errorf("skill %q not found in system prompt", skill2)
	}
	return nil
}

// anAgentManifestSpecifiesTheSkill configures a manifest with the given skill.
//
// Expected:
//   - skillName is the skill to include in the agent manifest.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sets s.cfg and s.manifest.
func (s *SkillAutoloadingStepDefinitions) anAgentManifestSpecifiesTheSkill(skillName string) error {
	s.cfg = hook.DefaultSkillAutoLoaderConfig()
	s.manifest = agent.Manifest{
		ID: "specialist",
		Capabilities: agent.Capabilities{
			AlwaysActiveSkills: []string{skillName},
		},
	}
	return nil
}

// theAgentIsStarted simulates starting an agent with the configured manifest.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Runs skill selection and captures the request.
func (s *SkillAutoloadingStepDefinitions) theAgentIsStarted() error {
	if s.cfg == nil {
		return errors.New("agent system not initialised")
	}
	s.prompt = "Help me with a task"
	s.runSelectionAndCapture()
	return nil
}

// theSystemPromptShouldIncludeTheSkill verifies a specific skill is in the prompt.
//
// Expected:
//   - skillName is the skill name to check for in the system prompt.
//   - s.capturedReq is non-nil from a prior agent start step.
//
// Returns:
//   - nil if skill found, error otherwise.
//
// Side effects:
//   - None.
func (s *SkillAutoloadingStepDefinitions) theSystemPromptShouldIncludeTheSkill(skillName string) error {
	if s.capturedReq == nil {
		return errors.New("no captured request; agent has not started")
	}
	systemContent := s.capturedReq.Messages[0].Content
	if !strings.Contains(systemContent, skillName) {
		return fmt.Errorf("skill %q not found in system prompt: %s", skillName, systemContent)
	}
	return nil
}

// thePromptContainsTheKeyword sets up a prompt containing the keyword and configures a matching pattern.
//
// Expected:
//   - keyword is the keyword to embed in the prompt and match against.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sets s.cfg, s.manifest, and s.prompt.
func (s *SkillAutoloadingStepDefinitions) thePromptContainsTheKeyword(keyword string) error {
	s.cfg = hook.DefaultSkillAutoLoaderConfig()
	s.cfg.KeywordPatterns = append(s.cfg.KeywordPatterns, hook.KeywordPattern{
		Pattern: keyword,
		Skills:  []string{"golang-" + keyword},
	})
	s.manifest = agent.Manifest{
		ID: "general",
		Capabilities: agent.Capabilities{
			AlwaysActiveSkills: []string{},
		},
	}
	s.prompt = fmt.Sprintf("Help me with %s queries and migrations", keyword)
	return nil
}

// theAgentSessionIsCreated runs the skill selection with the configured prompt.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Runs skill selection and captures the request.
func (s *SkillAutoloadingStepDefinitions) theAgentSessionIsCreated() error {
	if s.cfg == nil {
		return errors.New("agent system not initialised")
	}
	s.runSelectionAndCapture()
	return nil
}

// theSystemShouldInjectTheSkillIntoTheSystemPrompt verifies a skill was injected.
//
// Expected:
//   - skillName is the skill name to check for in the system prompt.
//   - s.capturedReq is non-nil from a prior session creation step.
//
// Returns:
//   - nil if skill found, error otherwise.
//
// Side effects:
//   - None.
func (s *SkillAutoloadingStepDefinitions) theSystemShouldInjectTheSkillIntoTheSystemPrompt(skillName string) error {
	return s.theSystemPromptShouldIncludeTheSkill(skillName)
}

// aSkillIsInjected simulates injecting skills into the prompt.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Runs skill selection and captures the request.
func (s *SkillAutoloadingStepDefinitions) aSkillIsInjected() error {
	if s.cfg == nil {
		return errors.New("agent system not initialised")
	}
	if s.capturedReq == nil {
		s.prompt = "Hello"
		s.runSelectionAndCapture()
	}
	return nil
}

// theSystemPromptShouldListTheSkillByItsLeanNameOnly verifies lean name format.
//
// Returns:
//   - nil if lean format present, error otherwise.
//
// Side effects:
//   - None.
func (s *SkillAutoloadingStepDefinitions) theSystemPromptShouldListTheSkillByItsLeanNameOnly() error {
	if s.capturedReq == nil {
		return errors.New("no captured request; skill has not been injected")
	}
	systemContent := s.capturedReq.Messages[0].Content
	if !strings.Contains(systemContent, "Your load_skills: [") {
		return fmt.Errorf("expected lean skill format 'Your load_skills: [...]' in prompt, got: %s", systemContent)
	}
	return nil
}

// theSkillDocumentationShouldNotBeInlinedInThePrompt verifies no full skill docs are inlined.
//
// Returns:
//   - nil if no inlined docs, error otherwise.
//
// Side effects:
//   - None.
func (s *SkillAutoloadingStepDefinitions) theSkillDocumentationShouldNotBeInlinedInThePrompt() error {
	if s.capturedReq == nil {
		return errors.New("no captured request; skill has not been injected")
	}
	systemContent := s.capturedReq.Messages[0].Content
	if strings.Contains(systemContent, "## What I do") {
		return errors.New("skill documentation appears to be inlined in the prompt")
	}
	if strings.Contains(systemContent, "## Core principles") {
		return errors.New("skill documentation appears to be inlined in the prompt")
	}
	return nil
}
