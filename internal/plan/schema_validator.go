package plan

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidationResult contains the outcome of schema validation for a plan document.
//
// Valid is true if the plan passes all required checks. Errors lists fatal issues
// that prevent use. Warnings lists non-fatal issues. Score is a float from 0.0
// (completely invalid) to 1.0 (perfect).
type ValidationResult struct {
	Valid    bool     // True if the plan is valid
	Errors   []string // Fatal errors
	Warnings []string // Non-fatal warnings
	Score    float64  // 0.0 (invalid) to 1.0 (perfect)
}

// SchemaValidator validates plan documents for required structure and content.
type SchemaValidator struct{}

// Validate checks the structure and content of a plan markdown document.
//
// Returns a ValidationResult and error if the plan is invalid.
func (v *SchemaValidator) Validate(planText string) (*ValidationResult, error) {
	result := &ValidationResult{Score: 1.0}
	if strings.TrimSpace(planText) == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "plan is empty")
		result.Score = 0.0
		return result, errors.New("plan is empty")
	}

	parts := strings.SplitN(planText, "---", 3)
	if len(parts) < 3 {
		result.Valid = false
		result.Errors = append(result.Errors, "missing YAML frontmatter")
		result.Score = 0.0
		return result, errors.New("missing YAML frontmatter")
	}

	var fm Frontmatter
	if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, "invalid YAML frontmatter")
		result.Score = 0.0
		return result, errors.New("invalid YAML frontmatter")
	}

	if strings.TrimSpace(fm.ID) == "" {
		result.Errors = append(result.Errors, "missing id in frontmatter")
		result.Score -= 0.3
	}
	if strings.TrimSpace(fm.Title) == "" {
		result.Errors = append(result.Errors, "missing title in frontmatter")
		result.Score -= 0.3
	}

	tasks := parseTasksFromMarkdown(parts[2])
	if len(tasks) == 0 {
		result.Errors = append(result.Errors, "no tasks found")
		result.Score -= 0.4
	}

	if result.Score < 0.0 {
		result.Score = 0.0
	}
	if result.Score > 1.0 {
		result.Score = 1.0
	}
	result.Valid = len(result.Errors) == 0
	if !result.Valid {
		return result, fmt.Errorf("%s", strings.Join(result.Errors, "; "))
	}
	return result, nil
}
