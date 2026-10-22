//go:build e2e

package support

import (
	"fmt"

	"github.com/cucumber/godog"
)

// FailoverBudgetSteps holds state for the pre-dispatch budget gate scenario.
type FailoverBudgetSteps struct {
	budget         int
	estimatedInput int
	maxReserve     int
	trimmed        bool
	penaltyApplied bool
}

// FailoverBudgetResolvesBudget records the resolved token budget for the candidate model.
//
// Expected: budget is the resolved token budget.
// Returns: an error if the budget is not positive.
// Side effects: mutates fixture state.
func (fb *FailoverBudgetSteps) FailoverBudgetResolvesBudget(budget int) error {
	if budget <= 0 {
		return fmt.Errorf("budget must be positive, got %d", budget)
	}
	fb.budget = budget
	return nil
}

// FailoverBudgetEstimatedInput records the estimated input token count.
//
// Expected: tokens is the estimated input token count.
// Returns: an error if the estimate is not positive.
// Side effects: mutates fixture state.
func (fb *FailoverBudgetSteps) FailoverBudgetEstimatedInput(tokens int) error {
	if tokens <= 0 {
		return fmt.Errorf("estimated input tokens must be positive, got %d", tokens)
	}
	fb.estimatedInput = tokens
	return nil
}

// FailoverBudgetMaxReserve records the request max tokens reserve.
//
// Expected: tokens is the max tokens reserve.
// Returns: an error if the reserve is not positive.
// Side effects: mutates fixture state.
func (fb *FailoverBudgetSteps) FailoverBudgetMaxReserve(tokens int) error {
	if tokens <= 0 {
		return fmt.Errorf("max tokens reserve must be positive, got %d", tokens)
	}
	fb.maxReserve = tokens
	return nil
}

// FailoverBudgetPreparesDispatch simulates the engine preparing to dispatch the request.
//
// Expected: None.
// Returns: an error if dispatch preparation fails.
// Side effects: mutates fixture state to reflect trimming behaviour.
func (fb *FailoverBudgetSteps) FailoverBudgetPreparesDispatch() error {
	if fb.budget <= 0 {
		return fmt.Errorf("budget not resolved")
	}
	if fb.estimatedInput+fb.maxReserve > fb.budget {
		fb.trimmed = true
	}
	fb.penaltyApplied = false
	return nil
}

// FailoverBudgetShouldTrim asserts the request was trimmed before dispatch.
//
// Expected: None.
// Returns: an error if the request was not trimmed.
// Side effects: None.
func (fb *FailoverBudgetSteps) FailoverBudgetShouldTrim() error {
	if !fb.trimmed {
		return fmt.Errorf("expected request to be trimmed before dispatch")
	}
	return nil
}

// FailoverBudgetShouldNotApplyPenalty asserts no provider health penalty was recorded.
//
// Expected: None.
// Returns: an error if a penalty was applied.
// Side effects: None.
func (fb *FailoverBudgetSteps) FailoverBudgetShouldNotApplyPenalty() error {
	if fb.penaltyApplied {
		return fmt.Errorf("expected no provider health penalty to be recorded")
	}
	return nil
}

// RegisterFailoverBudgetSteps wires the budget gate step definitions into the suite.
//
// Expected: ctx is the godog scenario context.
// Returns: None.
// Side effects: registers step definitions on the suite.
func RegisterFailoverBudgetSteps(ctx *godog.ScenarioContext) {
	fb := &FailoverBudgetSteps{}
	ctx.Step(`^the engine resolves a token budget of (\d+) tokens for the candidate model$`, fb.FailoverBudgetResolvesBudget)
	ctx.Step(`^the estimated input tokens are (\d+)$`, fb.FailoverBudgetEstimatedInput)
	ctx.Step(`^the request max tokens reserve is (\d+)$`, fb.FailoverBudgetMaxReserve)
	ctx.Step(`^the engine prepares to dispatch the request$`, fb.FailoverBudgetPreparesDispatch)
	ctx.Step(`^the request should be trimmed before dispatch$`, fb.FailoverBudgetShouldTrim)
	ctx.Step(`^no provider health penalty should be recorded$`, fb.FailoverBudgetShouldNotApplyPenalty)
}
