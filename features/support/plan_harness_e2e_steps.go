//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"

	"github.com/cucumber/godog"
)

// harnessE2EStepDefinitions holds state for harness e2e BDD scenarios.
type harnessE2EStepDefinitions struct {
	retryCount        int
	maxRetries        int
	planContent       string
	validationErrors  []string
	criticFeedback    string
	lastVerdict       string
	harnessComplete   bool
	escalationMessage string
}

// initPlanHarnessE2ESteps registers step definitions for the plan writer harness e2e scenarios.
//
// Expected: ctx is a valid godog ScenarioContext for step registration.
//
// Side effects: Registers step definitions on the provided scenario context.
func initPlanHarnessE2ESteps(ctx *godog.ScenarioContext) {
	h := &harnessE2EStepDefinitions{maxRetries: 3}

	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		h.retryCount = 0
		h.maxRetries = 3
		h.planContent = ""
		h.validationErrors = nil
		h.criticFeedback = ""
		h.lastVerdict = ""
		h.harnessComplete = false
		h.escalationMessage = ""
		return bddCtx, nil
	})

	ctx.Step(`^a planning session is in progress$`, aPlanningSessionIsInProgress)
	ctx.Step(`^the plan-writer produces an invalid plan on the first attempt$`, h.thePlanWriterProducesInvalidPlanFirstAttempt)
	ctx.Step(`^the harness evaluates the plan-writer output$`, h.theHarnessEvaluatesPlanWriterOutput)
	ctx.Step(`^the harness retries with validation feedback$`, h.theHarnessRetriesWithValidationFeedback)
	ctx.Step(`^the plan-writer produces a valid plan on retry$`, h.thePlanWriterProducesValidPlanOnRetry)
	ctx.Step(`^the plan passes harness evaluation$`, h.thePlanPassesHarnessEvaluation)
	ctx.Step(`^the plan-writer produces a plan that passes schema validation$`, h.thePlanWriterProducesPlanPassingSchema)
	ctx.Step(`^the harness critic evaluates the plan$`, h.theHarnessCriticEvaluatesPlan)
	ctx.Step(`^the critic rejects the plan with feedback$`, h.theCriticRejectsPlanWithFeedback)
	ctx.Step(`^the harness retries with critic feedback$`, h.theHarnessRetriesWithCriticFeedback)
	ctx.Step(`^the plan-writer produces an improved plan that the critic approves$`, h.thePlanWriterProducesImprovedPlan)
	ctx.Step(`^the plan-writer repeatedly produces invalid plans$`, h.thePlanWriterRepeatedlyProducesInvalidPlans)
	ctx.Step(`^the harness exhausts all retry attempts$`, h.theHarnessExhaustsRetryAttempts)
	ctx.Step(`^the harness emits a harness_complete event with validation errors$`, h.theHarnessEmitsCompleteWithErrors)
	ctx.Step(`^the planner escalates to the user$`, h.thePlannerEscalatesToUser)
}

// aPlanningSessionIsInProgress sets up the shared state for planning.
//
// Returns: nil on success.
//
// Side effects: Initializes shared state via initPlanningSession.
func aPlanningSessionIsInProgress() error {
	return initPlanningSession()
}

// thePlanWriterProducesInvalidPlanFirstAttempt simulates an invalid plan on first attempt.
//
// Returns: nil always.
//
// Side effects: Sets planContent and validationErrors.
func (h *harnessE2EStepDefinitions) thePlanWriterProducesInvalidPlanFirstAttempt() error {
	h.planContent = "invalid-plan-missing-tasks"
	h.validationErrors = []string{"Plan is missing required tasks section"}
	return nil
}

// theHarnessEvaluatesPlanWriterOutput validates the plan and finds errors.
//
// Returns: nil if validation errors exist, error otherwise.
//
// Side effects: None.
func (h *harnessE2EStepDefinitions) theHarnessEvaluatesPlanWriterOutput() error {
	if len(h.validationErrors) > 0 {
		return nil
	}
	return errors.New("expected validation errors but got none")
}

// theHarnessRetriesWithValidationFeedback simulates retry with validation feedback.
//
// Returns: nil always.
//
// Side effects: Increments retryCount, updates planContent, clears validationErrors.
func (h *harnessE2EStepDefinitions) theHarnessRetriesWithValidationFeedback() error {
	h.retryCount++
	h.planContent = "valid-plan"
	h.validationErrors = nil
	return nil
}

// thePlanWriterProducesValidPlanOnRetry verifies valid plan on retry.
//
// Returns: nil if planContent is "valid-plan", error otherwise.
//
// Side effects: None.
func (h *harnessE2EStepDefinitions) thePlanWriterProducesValidPlanOnRetry() error {
	if h.planContent != "valid-plan" {
		return fmt.Errorf("expected valid plan, got: %s", h.planContent)
	}
	return nil
}

// thePlanPassesHarnessEvaluation verifies the plan passes evaluation.
//
// Returns: nil if no validation errors, error otherwise.
//
// Side effects: None.
func (h *harnessE2EStepDefinitions) thePlanPassesHarnessEvaluation() error {
	if len(h.validationErrors) > 0 {
		return fmt.Errorf("validation errors still present: %v", h.validationErrors)
	}
	return nil
}

// thePlanWriterProducesPlanPassingSchema simulates a plan that passes schema but fails critic.
//
// Returns: nil always.
//
// Side effects: Sets planContent, clears validationErrors.
func (h *harnessE2EStepDefinitions) thePlanWriterProducesPlanPassingSchema() error {
	h.planContent = "schema-valid-but-poor-quality-plan"
	h.validationErrors = nil
	return nil
}

// theHarnessCriticEvaluatesPlan simulates the critic evaluating the plan.
//
// Returns: nil if planContent is poor quality, error otherwise.
//
// Side effects: Sets criticFeedback for poor quality plans.
func (h *harnessE2EStepDefinitions) theHarnessCriticEvaluatesPlan() error {
	if h.planContent == "schema-valid-but-poor-quality-plan" {
		h.criticFeedback = "Plan lacks sufficient detail for implementation"
		return nil
	}
	return errors.New("expected poor quality plan for critic to reject")
}

// theCriticRejectsPlanWithFeedback records the critic rejection.
//
// Returns: nil if criticFeedback is set, error otherwise.
//
// Side effects: Sets lastVerdict to "REJECT".
func (h *harnessE2EStepDefinitions) theCriticRejectsPlanWithFeedback() error {
	h.lastVerdict = "REJECT"
	if h.criticFeedback == "" {
		return errors.New("no critic feedback present")
	}
	return nil
}

// theHarnessRetriesWithCriticFeedback simulates retry with critic feedback.
//
// Returns: nil always.
//
// Side effects: Increments retryCount, updates planContent, clears criticFeedback.
func (h *harnessE2EStepDefinitions) theHarnessRetriesWithCriticFeedback() error {
	h.retryCount++
	h.planContent = "improved-high-quality-plan"
	h.criticFeedback = ""
	return nil
}

// thePlanWriterProducesImprovedPlan verifies the improved plan.
//
// Returns: nil if planContent is improved, error otherwise.
//
// Side effects: Sets lastVerdict to "APPROVE".
func (h *harnessE2EStepDefinitions) thePlanWriterProducesImprovedPlan() error {
	if h.planContent != "improved-high-quality-plan" {
		return fmt.Errorf("expected improved plan, got: %s", h.planContent)
	}
	h.lastVerdict = "APPROVE"
	return nil
}

// thePlanWriterRepeatedlyProducesInvalidPlans simulates repeated invalid plans.
//
// Returns: nil always.
//
// Side effects: Resets retryCount, sets planContent and validationErrors.
func (h *harnessE2EStepDefinitions) thePlanWriterRepeatedlyProducesInvalidPlans() error {
	h.retryCount = 0
	h.planContent = "invalid-plan"
	h.validationErrors = []string{"Missing tasks", "No timeline", "No resources"}
	return nil
}

// theHarnessExhaustsRetryAttempts simulates exhausting all retries.
//
// Returns: nil always.
//
// Side effects: Sets retryCount to maxRetries, sets harnessComplete to true.
func (h *harnessE2EStepDefinitions) theHarnessExhaustsRetryAttempts() error {
	h.retryCount = h.maxRetries
	h.harnessComplete = true
	return nil
}

// theHarnessEmitsCompleteWithErrors verifies the complete event with errors.
//
// Returns: nil if harness is complete with validation errors, error otherwise.
//
// Side effects: None.
func (h *harnessE2EStepDefinitions) theHarnessEmitsCompleteWithErrors() error {
	if !h.harnessComplete {
		return errors.New("harness not complete")
	}
	if len(h.validationErrors) == 0 {
		return errors.New("expected validation errors")
	}
	return nil
}

// thePlannerEscalatesToUser verifies escalation to user.
//
// Returns: nil if harness exhausted with errors, error otherwise.
//
// Side effects: Sets escalationMessage.
func (h *harnessE2EStepDefinitions) thePlannerEscalatesToUser() error {
	if h.harnessComplete && len(h.validationErrors) > 0 {
		h.escalationMessage = fmt.Sprintf("Harness exhausted after %d retries with errors: %v", h.maxRetries, h.validationErrors)
		return nil
	}
	return errors.New("cannot escalate: harness not exhausted or no errors")
}

var _ = []interface{}{
	initPlanHarnessE2ESteps,
}
