package validation

import (
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/plan"
	"gopkg.in/yaml.v3"
)

// ValidatorChain composes schema, assertion, and reference validators with short-circuit logic and weighted scoring.
type ValidatorChain struct {
	schemaValidator    *SchemaValidator
	assertionValidator *AssertionValidator
	referenceValidator *ReferenceValidator
	projectRoot        string
}

// NewValidatorChain creates a ValidatorChain with all validators.
//
// Expected:
//   - projectRoot is the absolute path to the project root directory.
//
// Returns:
//   - A configured ValidatorChain with schema, assertion, and reference validators.
//
// Side effects:
//   - None.
func NewValidatorChain(projectRoot string) *ValidatorChain {
	return &ValidatorChain{
		schemaValidator:    &SchemaValidator{},
		assertionValidator: &AssertionValidator{},
		referenceValidator: &ReferenceValidator{},
		projectRoot:        projectRoot,
	}
}

// Validate runs schema, assertion, and reference validation with short-circuit and weighted scoring.
//
// Expected:
//   - planText contains a plan with YAML frontmatter and markdown body.
//
// Returns:
//   - A ValidationResult with aggregated errors, warnings, and a weighted score.
//   - An error if any validator encounters a fatal failure.
//
// Side effects:
//   - None.
func (v *ValidatorChain) Validate(planText string) (*plan.ValidationResult, error) {
	schemaResult, schemaErr := v.schemaValidator.Validate(planText)
	if schemaErr != nil {
		return schemaResult, schemaErr
	}
	if !schemaResult.Valid {
		return schemaResult, nil
	}

	file, err := parsePlanFile(planText)
	if err != nil {
		return &plan.ValidationResult{Valid: false, Errors: []string{fmt.Sprintf("failed to parse plan: %v", err)}}, nil
	}
	assertionResult, assertionErr := v.assertionValidator.Validate(file)
	if assertionErr != nil {
		return assertionResult, assertionErr
	}

	referenceResult, referenceErr := v.referenceValidator.Validate(planText, v.projectRoot)
	if referenceErr != nil {
		return referenceResult, referenceErr
	}

	combined := &plan.ValidationResult{Valid: true, Score: 1.0}
	results := []*plan.ValidationResult{schemaResult, assertionResult, referenceResult}
	count := 0
	scoreSum := 0.0

	for _, result := range results {
		if result == nil {
			continue
		}
		count++
		scoreSum += result.Score
		if !result.Valid {
			combined.Valid = false
		}
		combined.Errors = append(combined.Errors, result.Errors...)
		combined.Warnings = append(combined.Warnings, result.Warnings...)
	}

	if count == 0 {
		combined.Score = 0.0
	} else {
		combined.Score = scoreSum / float64(count)
	}

	if combined.Score < 0.0 {
		combined.Score = 0.0
	}
	if combined.Score > 1.0 {
		combined.Score = 1.0
	}
	if len(combined.Errors) > 0 {
		combined.Valid = false
	}

	return combined, nil
}

// parsePlanFile extracts and unmarshals YAML frontmatter from plan text into a File struct.
// This is a local helper that replicates the plan-level parseFile logic to avoid
// depending on an unexported function across package boundaries.
//
// Expected:
//   - planText contains a plan with YAML frontmatter delimited by "---".
//
// Returns:
//   - A File struct populated from the YAML frontmatter.
//   - An error if the frontmatter is missing or cannot be unmarshalled.
//
// Side effects:
//   - None.
func parsePlanFile(planText string) (*plan.File, error) {
	parts := strings.SplitN(planText, "---", 3)
	if len(parts) < 3 {
		return nil, errors.New("missing YAML frontmatter")
	}

	var file plan.File
	if err := yaml.Unmarshal([]byte(parts[1]), &file); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	return &file, nil
}
