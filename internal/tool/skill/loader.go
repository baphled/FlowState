package skill

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/tool"
)

// Loader loads skills for the skill_load tool.
type Loader interface {
	// LoadAll returns all available skills.
	LoadAll() ([]skill.Skill, error)
}

// Tool implements the skill_load tool for loading skill content at runtime.
type Tool struct {
	loader Loader
}

// New creates a new skill_load tool with the given skill loader.
//
// Expected:
//   - loader is a non-nil Loader implementation.
//
// Returns:
//   - A configured Tool instance.
//
// Side effects:
//   - None.
func New(loader Loader) *Tool {
	return &Tool{loader: loader}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "skill_load".
//
// Side effects:
//   - None.
func (t *Tool) Name() string {
	return "skill_load"
}

// Description returns a human-readable description of the skill_load tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *Tool) Description() string {
	return "Load a skill's full markdown content by name for runtime guidance"
}

// Schema returns the JSON schema for the skill_load tool arguments.
//
// Returns:
//   - A tool.Schema describing the required skill_name property.
//
// Side effects:
//   - None.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"skill_name": {
				Type:        "string",
				Description: "The name of the skill to load (matches skill directory name or YAML name field)",
			},
		},
		Required: []string{"skill_name"},
	}
}

// Execute loads skill content by name and returns it as tool output.
//
// Expected:
//   - input contains a "skill_name" string argument.
//
// Returns:
//   - A tool.Result with the skill markdown content on success.
//   - An error if the argument is missing, skill not found, or loader fails.
//
// Side effects:
//   - Reads skill files from disk via the loader.
func (t *Tool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	skillName, ok := input.Arguments["skill_name"].(string)
	if !ok || skillName == "" {
		return tool.Result{}, errors.New("skill_name argument is required")
	}

	skills, err := t.loader.LoadAll()
	if err != nil {
		return tool.Result{}, fmt.Errorf("loading skills: %w", err)
	}

	for i := range skills {
		if skills[i].Name == skillName {
			return tool.Result{Output: skills[i].Content}, nil
		}
	}

	return tool.Result{}, fmt.Errorf("skill not found: %s", skillName)
}
