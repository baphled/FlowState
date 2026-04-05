package support

import "github.com/cucumber/godog"

// initPlanRejectionLoopSteps registers step definitions for the plan rejection loop scenarios.
//
// Expected:
//   - ctx is a valid godog ScenarioContext for step registration.
//
// Side effects:
//   - Registers step definitions on the provided scenario context.
func initPlanRejectionLoopSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the plan-writer has produced a plan$`, thePlanWriterHasProducedPlan)
	ctx.Step(`^the plan-reviewer returns a REJECT verdict$`, thePlanReviewerReturnsReject)
	ctx.Step(`^the planner re-delegates to the plan-writer$`, thePlannerRedelegatesToPlanWriter)
	ctx.Step(`^the plan-writer produces a new plan$`, thePlanWriterProducesNewPlan)
	ctx.Step(`^the plan-reviewer returns an APPROVE verdict$`, thePlanReviewerReturnsApprove)
	ctx.Step(`^the final plan is saved$`, theFinalPlanIsSaved)
	ctx.Step(`^the plan-reviewer rejects the plan (\d+) consecutive times$`, thePlanReviewerRejectsConsecutiveTimes)
	ctx.Step(`^the delegate tool returns an errMaxRejectionsExhausted error$`, theDelegateToolReturnsMaxRejectionsError)
	ctx.Step(`^the planner escalates to the user with the rejection reason$`, thePlannerEscalatesToUserWithReason)
}

// thePlanWriterHasProducedPlan is a pending step stub for rejection loop scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func thePlanWriterHasProducedPlan() error { return godog.ErrPending }

// thePlanReviewerReturnsReject is a pending step stub for rejection loop scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func thePlanReviewerReturnsReject() error { return godog.ErrPending }

// thePlannerRedelegatesToPlanWriter is a pending step stub for rejection loop scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func thePlannerRedelegatesToPlanWriter() error { return godog.ErrPending }

// thePlanWriterProducesNewPlan is a pending step stub for rejection loop scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func thePlanWriterProducesNewPlan() error { return godog.ErrPending }

// thePlanReviewerReturnsApprove is a pending step stub for rejection loop scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func thePlanReviewerReturnsApprove() error { return godog.ErrPending }

// theFinalPlanIsSaved is a pending step stub for rejection loop scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func theFinalPlanIsSaved() error { return godog.ErrPending }

// thePlanReviewerRejectsConsecutiveTimes is a pending step stub for rejection loop scenarios.
//
// Expected:
//   - n is the number of consecutive rejections to simulate.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func thePlanReviewerRejectsConsecutiveTimes(_ int) error { return godog.ErrPending }

// theDelegateToolReturnsMaxRejectionsError is a pending step stub for rejection loop scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func theDelegateToolReturnsMaxRejectionsError() error { return godog.ErrPending }

// thePlannerEscalatesToUserWithReason is a pending step stub for rejection loop scenarios.
//
// Returns:
//   - godog.ErrPending until Wave 5 implementation.
//
// Side effects:
//   - None.
func thePlannerEscalatesToUserWithReason() error { return godog.ErrPending }

var _ = []interface{}{
	initPlanRejectionLoopSteps,
	thePlanWriterHasProducedPlan,
	thePlanReviewerReturnsReject,
	thePlannerRedelegatesToPlanWriter,
	thePlanWriterProducesNewPlan,
	thePlanReviewerReturnsApprove,
	theFinalPlanIsSaved,
	thePlanReviewerRejectsConsecutiveTimes,
	theDelegateToolReturnsMaxRejectionsError,
	thePlannerEscalatesToUserWithReason,
}
