// Package validation provides plan quality validators for FlowState.
//
// This package exposes three stateless validators:
//   - SchemaValidator: checks YAML frontmatter structure and required fields.
//   - AssertionValidator: verifies acceptance criteria quality.
//   - ReferenceValidator: checks file and type references in the plan.
//
// Validators accept plan text and return a ValidationResult defined in the
// parent plan package. Use ValidatorChain to compose all three validators
// with short-circuit logic and weighted scoring.
package validation
