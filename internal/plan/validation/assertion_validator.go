package validation

import (
	"fmt"

	"github.com/baphled/flowstate/internal/plan"
)

// AssertionValidator performs semantic validation on a plan File.
//
// It checks for duplicate task titles, circular dependencies, invalid dependency references,
// and missing estimated effort fields. Structural validation is handled by SchemaValidator.
type AssertionValidator struct{}

// Validate performs semantic checks on the given plan File.
//
// It checks for duplicate task titles, circular dependencies, invalid dependency references,
// and missing estimated effort fields. The Valid field is true if no errors are found.
// Score is 1.0 if all checks pass, and is reduced for each violation.
//
// Expected:
//   - planFile is a valid File with tasks.
//
// Returns:
//   - A ValidationResult with errors and score.
//   - An error if any validation check fails.
//
// Side effects:
//   - None.
func (v *AssertionValidator) Validate(planFile *plan.File) (*plan.ValidationResult, error) {
	result := &plan.ValidationResult{Score: 1.0}
	titleSet := make(map[string]struct{})
	titleToIdx := make(map[string]int)

	v.checkDuplicateTitles(planFile, result, titleSet, titleToIdx)
	v.checkInvalidDependencies(planFile, result, titleSet)
	v.checkCircularDependencies(planFile, result, titleToIdx)
	v.checkMissingEffort(planFile, result)

	v.normalizeScore(result)
	result.Valid = len(result.Errors) == 0
	if !result.Valid {
		return result, fmt.Errorf("%s", result.Errors[0])
	}
	return result, nil
}

// checkDuplicateTitles checks for duplicate task titles and updates the result accordingly.
//
// Expected:
//   - planFile contains tasks with titles.
//   - titleSet and titleToIdx are empty maps.
//
// Side effects:
//   - Modifies result by appending errors and reducing score.
//   - Populates titleSet and titleToIdx maps.
func (v *AssertionValidator) checkDuplicateTitles(
	planFile *plan.File,
	result *plan.ValidationResult,
	titleSet map[string]struct{},
	titleToIdx map[string]int,
) {
	for i := range planFile.Tasks {
		task := &planFile.Tasks[i]
		title := task.Title
		if _, exists := titleSet[title]; exists {
			result.Errors = append(result.Errors, fmt.Sprintf("duplicate task title: %q", title))
			result.Score -= 0.2
		} else {
			titleSet[title] = struct{}{}
			titleToIdx[title] = i
		}
	}
}

// checkInvalidDependencies checks for references to non-existent task titles.
//
// Expected:
//   - planFile contains tasks with dependencies.
//   - titleSet contains all valid task titles.
//
// Side effects:
//   - Modifies result by appending errors and reducing score.
func (v *AssertionValidator) checkInvalidDependencies(planFile *plan.File, result *plan.ValidationResult, titleSet map[string]struct{}) {
	for i := range planFile.Tasks {
		task := &planFile.Tasks[i]
		for _, dep := range task.Dependencies {
			if _, ok := titleSet[dep]; !ok {
				result.Errors = append(result.Errors, fmt.Sprintf("unknown dependency: %q in task %q", dep, task.Title))
				result.Score -= 0.2
			}
		}
	}
}

// checkCircularDependencies detects cycles in the task dependency graph using depth-first search.
//
// Expected:
//   - planFile contains tasks with dependencies.
//   - titleToIdx maps all task titles to their indices.
//
// Side effects:
//   - Modifies result by appending errors and reducing score if a cycle is found.
func (v *AssertionValidator) checkCircularDependencies(planFile *plan.File, result *plan.ValidationResult, titleToIdx map[string]int) {
	visited := make(map[string]bool)
	stack := make(map[string]bool)

	var visit func(string) bool
	visit = func(title string) bool {
		if stack[title] {
			return true
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
		task := &planFile.Tasks[taskIdx]
		for _, dep := range task.Dependencies {
			if visit(dep) {
				return true
			}
		}
		stack[title] = false
		return false
	}

	for i := range planFile.Tasks {
		task := &planFile.Tasks[i]
		if visit(task.Title) {
			result.Errors = append(result.Errors, "circular dependency detected")
			result.Score -= 0.3
			break
		}
	}
}

// checkMissingEffort checks that all tasks have an estimated effort value.
//
// Expected:
//   - planFile contains tasks.
//
// Side effects:
//   - Modifies result by appending errors and reducing score for tasks without effort.
func (v *AssertionValidator) checkMissingEffort(planFile *plan.File, result *plan.ValidationResult) {
	for i := range planFile.Tasks {
		task := &planFile.Tasks[i]
		if task.EstimatedEffort == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("missing estimated effort for task %q", task.Title))
			result.Score -= 0.1
		}
	}
}

// normalizeScore clamps the validation score to the range [0.0, 1.0].
//
// Expected:
//   - result is a valid ValidationResult.
//
// Side effects:
//   - Modifies result by clamping the Score field.
func (v *AssertionValidator) normalizeScore(result *plan.ValidationResult) {
	if result.Score < 0.0 {
		result.Score = 0.0
	}
	if result.Score > 1.0 {
		result.Score = 1.0
	}
}
