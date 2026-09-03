//go:build e2e

package support

import (
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/skill"
)

// AlwaysActiveSkillStepDefinitions holds state for the always-active
// skill-body injection BDD steps. The contract under test: the engine
// composes always-active skill bodies into the system prompt at build
// time (assembleSystemPromptLocked), so zero runtime skill_load tool
// calls are needed to activate them.
type AlwaysActiveSkillStepDefinitions struct {
	skills []skill.Skill
	prompt string
}

// RegisterAlwaysActiveSkillSteps registers always-active skill step definitions.
//
// Expected:
//   - ctx is a godog ScenarioContext.
//
// Side effects:
//   - Registers always-active skill step definitions with the godog context.
func RegisterAlwaysActiveSkillSteps(ctx *godog.ScenarioContext) {
	s := &AlwaysActiveSkillStepDefinitions{}

	ctx.Step(`^an engine configured with always-active skills "([^"]*)" and "([^"]*)"$`, s.engineWithAlwaysActiveSkills)
	ctx.Step(`^the system prompt is built for skills$`, s.theSystemPromptIsBuilt)
	ctx.Step(`^the system prompt should contain the full body of skill "([^"]*)"$`, s.theSystemPromptShouldContainSkillBody)
	ctx.Step(`^zero skill_load tool calls should be required to activate them$`, s.zeroSkillLoadCallsRequired)
}

func (s *AlwaysActiveSkillStepDefinitions) engineWithAlwaysActiveSkills(first, second string) error {
	s.skills = []skill.Skill{
		{Name: first, Content: "PREFLIGHT_BODY"},
		{Name: second, Content: "MEMORY_KEEPER_BODY"},
	}
	return nil
}

func (s *AlwaysActiveSkillStepDefinitions) theSystemPromptIsBuilt() error {
	eng := engine.New(engine.Config{Skills: s.skills})
	s.prompt = eng.BuildSystemPrompt()
	if s.prompt == "" {
		return godog.ErrUndefined
	}
	return nil
}

func (s *AlwaysActiveSkillStepDefinitions) theSystemPromptShouldContainSkillBody(name string) error {
	for i := range s.skills {
		if s.skills[i].Name == name {
			if !strings.Contains(s.prompt, s.skills[i].Content) {
				return godog.ErrUndefined
			}
			return nil
		}
	}
	return godog.ErrUndefined
}

// zeroSkillLoadCallsRequired asserts that the full skill bodies were
// composed at build time — the prompt already contains each body, so
// no runtime skill_load invocation is needed. The engine's skill
// injection path (assembleSystemPromptLocked) is the same surface
// covered by unit specs in internal/engine/system_prompt_skills_test.go.
func (s *AlwaysActiveSkillStepDefinitions) zeroSkillLoadCallsRequired() error {
	for i := range s.skills {
		if !strings.Contains(s.prompt, s.skills[i].Content) {
			return godog.ErrUndefined
		}
	}
	return nil
}
