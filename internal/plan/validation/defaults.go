package validation

import "github.com/baphled/flowstate/internal/plan/harness"

// DefaultValidators returns Option values that wire in the standard
// schema, assertion, and reference validators. Callers that construct a
// harness.Harness should spread these options so the harness has validators
// configured:
//
//	harness.NewHarness(root, validation.DefaultValidators()...)
//
// Returns:
//   - A slice of Option values for the three default validators.
//
// Side effects:
//   - None.
func DefaultValidators() []harness.Option {
	return []harness.Option{
		harness.WithSchemaValidator(&SchemaValidator{}),
		harness.WithAssertionValidator(&AssertionValidator{}),
		harness.WithReferenceValidator(&ReferenceValidator{}),
	}
}
