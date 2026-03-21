package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/tool"
	toolskill "github.com/baphled/flowstate/internal/tool/skill"
)

// SkillStepDefinitions holds state for skill BDD step definitions.
type SkillStepDefinitions struct {
	stepDefs   *StepDefinitions
	skillTool  *toolskill.Tool
	lastResult tool.Result
	lastErr    error
	tempDir    string
}

// RegisterSkillSteps registers skill-specific step definitions.
//
// Expected:
//   - ctx is a godog ScenarioContext.
//   - stepDefs is a pointer to StepDefinitions.
//
// Side effects:
//   - Registers skill step definitions with the godog context.
//   - Cleans up temporary directories after each scenario.
func RegisterSkillSteps(ctx *godog.ScenarioContext, stepDefs *StepDefinitions) {
	s := &SkillStepDefinitions{stepDefs: stepDefs}
	ctx.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if s.tempDir != "" {
			os.RemoveAll(s.tempDir)
		}
		return ctx, nil
	})
	ctx.Step(`^the skill_load tool is available$`, s.theSkillLoadToolIsAvailable)
	ctx.Step(`^I call skill_load with skill name "([^"]*)"$`, s.iCallSkillLoadWithSkillName)
	ctx.Step(`^the tool should return the skill content$`, s.theToolShouldReturnTheSkillContent)
	ctx.Step(`^the content should contain skill documentation$`, s.theContentShouldContainSkillDocumentation)
	ctx.Step(`^the tool should return an error$`, s.theToolShouldReturnAnError)
	ctx.Step(`^the error message should indicate skill not found$`, s.theErrorMessageShouldIndicateSkillNotFound)
}

// theSkillLoadToolIsAvailable implements a BDD step definition.
//
// Returns:
//   - nil on success, error otherwise.
//
// Side effects:
//   - Creates temporary skill directory at s.tempDir.
//   - Sets s.skillTool.
func (s *SkillStepDefinitions) theSkillLoadToolIsAvailable() error {
	dir, err := os.MkdirTemp("", "skill-bdd-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	s.tempDir = dir

	golangDir := filepath.Join(dir, "golang")
	if err := os.MkdirAll(golangDir, 0o755); err != nil {
		return fmt.Errorf("creating skill dir: %w", err)
	}

	content := "---\nname: golang\ndescription: Go language skill\n---\n# Golang Skill\n\nThis skill provides Go expertise."
	if err := os.WriteFile(filepath.Join(golangDir, "SKILL.md"), []byte(content), 0o600); err != nil {
		return fmt.Errorf("writing skill file: %w", err)
	}

	loader := skill.NewFileSkillLoader(dir)
	s.skillTool = toolskill.New(loader)
	return nil
}

// iCallSkillLoadWithSkillName implements a BDD step definition.
//
// Expected:
//   - skillName is the name of the skill to load.
//
// Returns:
//   - nil (always succeeds; errors stored in s.lastErr).
//
// Side effects:
//   - Executes the skill_load tool with the given skill name.
//   - Sets s.lastResult and s.lastErr.
func (s *SkillStepDefinitions) iCallSkillLoadWithSkillName(skillName string) error {
	input := tool.Input{
		Name:      "skill_load",
		Arguments: map[string]interface{}{"skill_name": skillName},
	}
	s.lastResult, s.lastErr = s.skillTool.Execute(context.Background(), input)
	return nil
}

// theToolShouldReturnTheSkillContent implements a BDD step definition.
//
// Returns:
//   - nil if the tool returned content, error otherwise.
//
// Side effects:
//   - None.
func (s *SkillStepDefinitions) theToolShouldReturnTheSkillContent() error {
	if s.lastErr != nil {
		return fmt.Errorf("expected no error, got: %w", s.lastErr)
	}
	if s.lastResult.Output == "" {
		return errors.New("expected non-empty skill content")
	}
	return nil
}

// theContentShouldContainSkillDocumentation implements a BDD step definition.
//
// Returns:
//   - nil if content length > 10, error otherwise.
//
// Side effects:
//   - None.
func (s *SkillStepDefinitions) theContentShouldContainSkillDocumentation() error {
	if len(s.lastResult.Output) < 10 {
		return fmt.Errorf("skill content too short: %q", s.lastResult.Output)
	}
	return nil
}

// theToolShouldReturnAnError implements a BDD step definition.
//
// Returns:
//   - nil if s.lastErr is non-nil, error otherwise.
//
// Side effects:
//   - None.
func (s *SkillStepDefinitions) theToolShouldReturnAnError() error {
	if s.lastErr == nil {
		return errors.New("expected an error, got nil")
	}
	return nil
}

// theErrorMessageShouldIndicateSkillNotFound implements a BDD step definition.
//
// Returns:
//   - nil if error message contains "skill not found", error otherwise.
//
// Side effects:
//   - None.
func (s *SkillStepDefinitions) theErrorMessageShouldIndicateSkillNotFound() error {
	if s.lastErr == nil {
		return errors.New("expected error, got nil")
	}
	if !strings.Contains(s.lastErr.Error(), "skill not found") {
		return fmt.Errorf("expected 'skill not found' in error, got: %q", s.lastErr.Error())
	}
	return nil
}
