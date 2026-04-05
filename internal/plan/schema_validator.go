package plan

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaValidator validates plan documents for required structure and content.
type SchemaValidator struct{}

// Validate checks the structure and content of a plan markdown document.
//
// Expected:
//   - planText is a markdown document with YAML frontmatter.
//
// Returns:
//   - A ValidationResult with score and errors.
//   - An error if the plan is invalid.
//
// Side effects:
//   - None.
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

	v.validateExpandedFields(parts[1], result)

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

// validateExpandedFields checks the expanded plan sections added in T1.
// These fields are optional for backward compatibility but generate warnings
// when producing plans for harness_enabled agents.
//
// Expected:
//   - yamlContent is the YAML frontmatter content.
//   - result is the ValidationResult to append warnings to.
//
// Returns:
//   - (nothing; modifies result in place)
//
// Side effects:
//   - Appends warnings to result.Warnings for missing optional fields.
func (v *SchemaValidator) validateExpandedFields(yamlContent string, result *ValidationResult) {
	var file File
	if err := yaml.Unmarshal([]byte(yamlContent), &file); err != nil {
		return
	}

	if strings.TrimSpace(file.TLDR) == "" {
		result.Warnings = append(result.Warnings, "plan missing TL;DR section")
	}

	if strings.TrimSpace(file.Context.OriginalRequest) == "" {
		result.Warnings = append(result.Warnings, "plan missing Context.OriginalRequest")
	}

	if strings.TrimSpace(file.WorkObjectives.CoreObjective) == "" {
		result.Warnings = append(result.Warnings, "plan missing WorkObjectives.CoreObjective")
	}

	if len(file.WorkObjectives.Deliverables) == 0 {
		result.Warnings = append(result.Warnings, "plan missing WorkObjectives.Deliverables")
	}

	if strings.TrimSpace(file.VerificationStrategy) == "" {
		result.Warnings = append(result.Warnings, "plan missing VerificationStrategy")
	}
}
