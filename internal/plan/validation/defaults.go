package validation

import "github.com/baphled/flowstate/internal/plan/harness"

// DefaultValidators returns HarnessOption values that wire in the standard
// schema, assertion, and reference validators. Callers that construct a
// harness.Harness should spread these options so the harness has validators
// configured:
//
//	harness.NewHarness(root, validation.DefaultValidators()...)
//
// Returns:
//   - A slice of HarnessOption values for the three default validators.
//
// Side effects:
//   - None.
func DefaultValidators() []harness.HarnessOption {
	return []harness.HarnessOption{
		harness.WithSchemaValidator(&SchemaValidator{}),
		harness.WithAssertionValidator(&AssertionValidator{}),
		harness.WithReferenceValidator(&ReferenceValidator{}),
	}
}
