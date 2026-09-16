package validation

import (
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/plan"
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
func (v *SchemaValidator) Validate(planText string) (*plan.ValidationResult, error) {
	return v.ValidateDocument(plan.ParsePlanDocument(planText), strings.TrimSpace(planText) == "")
}

// ValidateDocument checks a pre-parsed plan document for required
// structure and content, reusing the shared frontmatter parse.
//
// Expected:
//   - doc is a PlanDocument produced by ParsePlanDocument.
//   - empty reports whether the source text was blank.
//
// Returns:
//   - A ValidationResult with score and errors.
//   - An error if the plan is invalid.
//
// Side effects:
//   - None.
func (v *SchemaValidator) ValidateDocument(doc plan.PlanDocument, empty bool) (*plan.ValidationResult, error) {
	result := &plan.ValidationResult{Score: 1.0}
	if empty {
		result.Valid = false
		result.Errors = append(result.Errors, "plan is empty")
		result.Score = 0.0
		return result, errors.New("plan is empty")
	}

	if doc.FrontmatterYAML == "" && doc.Body == "" && doc.Err == nil {
		result.Valid = false
		result.Errors = append(result.Errors, "missing YAML frontmatter")
		result.Score = 0.0
		return result, errors.New("missing YAML frontmatter")
	}

	if doc.Err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, "invalid YAML frontmatter")
		result.Score = 0.0
		return result, errors.New("invalid YAML frontmatter")
	}

	if strings.TrimSpace(doc.File.ID) == "" {
		result.Errors = append(result.Errors, "missing id in frontmatter")
		result.Score -= 0.3
	}
	if strings.TrimSpace(doc.File.Title) == "" {
		result.Errors = append(result.Errors, "missing title in frontmatter")
		result.Score -= 0.3
	}

	if !hasTaskHeaders(doc.Body) {
		result.Errors = append(result.Errors, "no tasks found")
		result.Score -= 0.4
	}

	v.appendExpandedWarnings(doc.File, result)

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

// appendExpandedWarnings appends warnings for the optional expanded
// plan sections missing from file.
//
// Expected:
//   - file holds frontmatter fields parsed from the plan.
//   - result is the ValidationResult to append warnings to.
//
// Returns:
//   - (nothing; modifies result in place)
//
// Side effects:
//   - Appends warnings to result.Warnings for missing optional fields.
func (v *SchemaValidator) appendExpandedWarnings(file plan.File, result *plan.ValidationResult) {
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

// hasTaskHeaders reports whether the markdown body contains any level-2 headers
// that indicate task definitions. This is equivalent to checking whether
// parseTasksFromMarkdown would return a non-empty slice.
//
// Expected:
//   - markdown is the body of a plan document (after frontmatter).
//
// Returns:
//   - true if any "## " header line exists, false otherwise.
//
// Side effects:
//   - None.
func hasTaskHeaders(markdown string) bool {
	for _, line := range strings.Split(markdown, "\n") {
		if strings.HasPrefix(line, "## ") {
			return true
		}
	}
	return false
}
