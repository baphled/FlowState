package validation

import "github.com/baphled/flowstate/internal/plan"

// DefaultValidators returns HarnessOption values that wire in the standard
// schema, assertion, and reference validators. Callers that construct a
// plan.Harness should spread these options so the harness has validators
// configured:
//
//	plan.NewHarness(root, validation.DefaultValidators()...)
//
// Returns:
//   - A slice of HarnessOption values for the three default validators.
//
// Side effects:
//   - None.
func DefaultValidators() []plan.HarnessOption {
	return []plan.HarnessOption{
		plan.WithSchemaValidator(&SchemaValidator{}),
		plan.WithAssertionValidator(&AssertionValidator{}),
		plan.WithReferenceValidator(&ReferenceValidator{}),
	}
}
