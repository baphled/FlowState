package support

import "github.com/cucumber/godog"

// initPlanHarnessE2ESteps registers step definitions for the plan writer harness e2e scenarios.
//
// Expected:
//   - ctx is a valid godog ScenarioContext for step registration.
//
// Side effects:
//   - Registers step definitions on the provided scenario context.
func initPlanHarnessE2ESteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a planning session is in progress$`, aPlanningSessionIsInProgress)
	ctx.Step(`^the plan-writer produces an invalid plan on the first attempt$`, thePlanWriterProducesInvalidPlanFirstAttempt)
	ctx.Step(`^the harness evaluates the plan-writer output$`, theHarnessEvaluatesPlanWriterOutput)
	ctx.Step(`^the harness retries with validation feedback$`, theHarnessRetriesWithValidationFeedback)
	ctx.Step(`^the plan-writer produces a valid plan on retry$`, thePlanWriterProducesValidPlanOnRetry)
	ctx.Step(`^the plan passes harness evaluation$`, thePlanPassesHarnessEvaluation)
	ctx.Step(`^the plan-writer produces a plan that passes schema validation$`, thePlanWriterProducesPlanPassingSchema)
	ctx.Step(`^the harness critic evaluates the plan$`, theHarnessCriticEvaluatesPlan)
	ctx.Step(`^the critic rejects the plan with feedback$`, theCriticRejectsPlanWithFeedback)
	ctx.Step(`^the harness retries with critic feedback$`, theHarnessRetriesWithCriticFeedback)
	ctx.Step(`^the plan-writer produces an improved plan that the critic approves$`, thePlanWriterProducesImprovedPlan)
	ctx.Step(`^the plan-writer repeatedly produces invalid plans$`, thePlanWriterRepeatedlyProducesInvalidPlans)
	ctx.Step(`^the harness exhausts all retry attempts$`, theHarnessExhaustsRetryAttempts)
	ctx.Step(`^the harness emits a harness_complete event with validation errors$`, theHarnessEmitsCompleteWithErrors)
	ctx.Step(`^the planner escalates to the user$`, thePlannerEscalatesToUser)
}

// aPlanningSessionIsInProgress is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func aPlanningSessionIsInProgress() error { return godog.ErrPending }

// thePlanWriterProducesInvalidPlanFirstAttempt is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func thePlanWriterProducesInvalidPlanFirstAttempt() error { return godog.ErrPending }

// theHarnessEvaluatesPlanWriterOutput is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func theHarnessEvaluatesPlanWriterOutput() error { return godog.ErrPending }

// theHarnessRetriesWithValidationFeedback is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func theHarnessRetriesWithValidationFeedback() error { return godog.ErrPending }

// thePlanWriterProducesValidPlanOnRetry is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func thePlanWriterProducesValidPlanOnRetry() error { return godog.ErrPending }

// thePlanPassesHarnessEvaluation is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func thePlanPassesHarnessEvaluation() error { return godog.ErrPending }

// thePlanWriterProducesPlanPassingSchema is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func thePlanWriterProducesPlanPassingSchema() error { return godog.ErrPending }

// theHarnessCriticEvaluatesPlan is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func theHarnessCriticEvaluatesPlan() error { return godog.ErrPending }

// theCriticRejectsPlanWithFeedback is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func theCriticRejectsPlanWithFeedback() error { return godog.ErrPending }

// theHarnessRetriesWithCriticFeedback is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func theHarnessRetriesWithCriticFeedback() error { return godog.ErrPending }

// thePlanWriterProducesImprovedPlan is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func thePlanWriterProducesImprovedPlan() error { return godog.ErrPending }

// thePlanWriterRepeatedlyProducesInvalidPlans is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func thePlanWriterRepeatedlyProducesInvalidPlans() error { return godog.ErrPending }

// theHarnessExhaustsRetryAttempts is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func theHarnessExhaustsRetryAttempts() error { return godog.ErrPending }

// theHarnessEmitsCompleteWithErrors is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func theHarnessEmitsCompleteWithErrors() error { return godog.ErrPending }

// thePlannerEscalatesToUser is a pending step stub for harness e2e scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func thePlannerEscalatesToUser() error { return godog.ErrPending }

var _ = []interface{}{
	initPlanHarnessE2ESteps,
	aPlanningSessionIsInProgress,
	thePlanWriterProducesInvalidPlanFirstAttempt,
	theHarnessEvaluatesPlanWriterOutput,
	theHarnessRetriesWithValidationFeedback,
	thePlanWriterProducesValidPlanOnRetry,
	thePlanPassesHarnessEvaluation,
	thePlanWriterProducesPlanPassingSchema,
	theHarnessCriticEvaluatesPlan,
	theCriticRejectsPlanWithFeedback,
	theHarnessRetriesWithCriticFeedback,
	thePlanWriterProducesImprovedPlan,
	thePlanWriterRepeatedlyProducesInvalidPlans,
	theHarnessExhaustsRetryAttempts,
	theHarnessEmitsCompleteWithErrors,
	thePlannerEscalatesToUser,
}
