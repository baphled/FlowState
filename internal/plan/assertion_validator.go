package plan

import (
	"fmt"
)

// AssertionValidator performs semantic validation on a plan File.
//
// It checks for duplicate task titles, circular dependencies, invalid dependency references,
// and missing estimated effort fields. Structural validation is handled by SchemaValidator.
type AssertionValidator struct{}

// Validate performs semantic checks on the given plan File.
//
// It returns a ValidationResult with errors and warnings for:
//   - Duplicate task titles
//   - Circular dependencies
//   - Invalid dependency references
//   - Missing estimated effort fields
//
// The Valid field is true if no errors are found. Score is 1.0 if all checks pass, and is reduced for each violation.
func (v *AssertionValidator) Validate(plan *File) (*ValidationResult, error) {
	titleSet := make(map[string]struct{})
	titleToIdx := make(map[string]int)
	result := &ValidationResult{Score: 1.0}
	for i, task := range plan.Tasks {
		title := task.Title
		if _, exists := titleSet[title]; exists {
			result.Errors = append(result.Errors, fmt.Sprintf("duplicate task title: %q", title))
			result.Score -= 0.2
		} else {
			titleSet[title] = struct{}{}
			titleToIdx[title] = i
		}
	}

	// Check for invalid dependency references
	for _, task := range plan.Tasks {
		for _, dep := range task.Dependencies {
			if _, ok := titleSet[dep]; !ok {
				result.Errors = append(result.Errors, fmt.Sprintf("unknown dependency: %q in task %q", dep, task.Title))
				result.Score -= 0.2
			}
		}
	}

	// Check for circular dependencies using DFS
	visited := make(map[string]bool)
	stack := make(map[string]bool)
	var visit func(string) bool
	visit = func(title string) bool {
		if stack[title] {
			return true // cycle detected
		}
		if visited[title] {
			return false
		}
		visited[title] = true
		stack[title] = true
		taskIdx, ok := titleToIdx[title]
		if !ok {
			stack[title] = false
			return false
		}
		task := plan.Tasks[taskIdx]
		for _, dep := range task.Dependencies {
			if visit(dep) {
				return true
			}
		}
		stack[title] = false
		return false
	}
	for _, task := range plan.Tasks {
		if visit(task.Title) {
			result.Errors = append(result.Errors, "circular dependency detected")
			result.Score -= 0.3
			break
		}
	}

	// Check for missing estimated effort
	for _, task := range plan.Tasks {
		if task.EstimatedEffort == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("missing estimated effort for task %q", task.Title))
			result.Score -= 0.1
		}
	}

	if result.Score < 0.0 {
		result.Score = 0.0
	}
	if result.Score > 1.0 {
		result.Score = 1.0
	}
	result.Valid = len(result.Errors) == 0
	if !result.Valid {
		return result, fmt.Errorf("%s", result.Errors[0])
	}
	return result, nil
}
