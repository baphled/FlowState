// Package plan provides plan management, execution, and result tracking.
//
// This package handles the core plan abstraction for FlowState, including:
//   - Creating and managing execution plans
//   - Tracking plan state and progress
//   - Recording plan results and outcomes
//
// A plan represents a structured sequence of steps to accomplish a goal,
// with support for branching, error handling, and result aggregation.
//
// # Harness vs Reviewer Boundary
//
// The harness and reviewer serve distinct, complementary roles in plan validation:
//
// **Harness (SchemaValidator)** answers: "Does the plan have all required fields?"
//
//   - SchemaValidator: Checks field presence, types, and basic structure
//   - AssertionValidator: Validates assertions within the plan
//   - ReferenceValidator: Resolves references to external resources
//   - LLMCritic: Performs quality checks (disabled when reviewer is active)
//
// **Reviewer Agent** answers: "Is the plan GOOD? Is it feasible and well-designed?"
//
//   - Performs independent quality and feasibility review
//   - Evaluates plan soundness, completeness, and risk assessment
//   - Provides expert feedback on plan quality
//
// When a plan-reviewer agent is present in the delegation table, the harness
// LLMCritic is disabled to avoid double-review. The SchemaValidator remains
// active regardless, as structural validation is always necessary.
package plan
