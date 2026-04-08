package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/plan/harness"
	"github.com/baphled/flowstate/internal/plan/validation"
	"github.com/baphled/flowstate/internal/provider"
)

// HarnessStepDefinitions holds state for harness BDD scenarios.
type HarnessStepDefinitions struct {
	harness          *harness.Harness
	evaluationResult *plan.EvaluationResult
	validationResult *plan.ValidationResult
	projectRoot      string
	planText         string
}

// harnessTestStreamer provides pre-configured streaming responses for BDD test scenarios.
type harnessTestStreamer struct {
	responses []string
	callCount int
}

// Stream returns a channel of pre-configured response chunks for BDD test scenarios.
//
// Expected:
//   - The streamer has been initialised with at least one response.
//
// Returns:
//   - A channel of StreamChunk values containing the next pre-configured response.
//   - A nil error (this test implementation does not produce errors).
//
// Side effects:
//   - Increments the internal call counter to cycle through responses.
func (s *harnessTestStreamer) Stream(_ context.Context, _, _ string) (<-chan provider.StreamChunk, error) {
	idx := s.callCount
	if idx >= len(s.responses) {
		idx = len(s.responses) - 1
	}
	resp := s.responses[idx]
	s.callCount++
	ch := make(chan provider.StreamChunk, 10)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: resp}
	}()
	return ch, nil
}

// RegisterHarnessSteps registers all harness-related BDD step definitions.
//
// Expected:
//   - ctx is a valid godog ScenarioContext for step registration.
//
// Side effects:
//   - Registers Before hooks and step definitions on the provided scenario context.
func RegisterHarnessSteps(ctx *godog.ScenarioContext) {
	h := &HarnessStepDefinitions{}

	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		cwd, err := os.Getwd()
		if err != nil {
			return bddCtx, err
		}
		h.projectRoot = filepath.Join(cwd, "..", "..")
		h.harness = harness.NewHarness(h.projectRoot, append(validation.DefaultValidators(), harness.WithMaxRetries(3))...)
		h.evaluationResult = nil
		h.validationResult = nil
		h.planText = ""
		return bddCtx, nil
	})

	ctx.Step(`^a planner agent is configured with harness enabled$`, h.aPlannerAgentIsConfiguredWithHarnessEnabled)
	ctx.Step(`^the planner generates a valid plan$`, h.thePlannerGeneratesAValidPlan)
	ctx.Step(`^the harness accepts the plan without retry$`, h.theHarnessAcceptsThePlanWithoutRetry)
	ctx.Step(`^the validation score is above (\d+\.?\d*)$`, h.theValidationScoreIsAbove)
	ctx.Step(`^the planner generates an invalid plan missing frontmatter$`, h.thePlannerGeneratesAnInvalidPlanMissingFrontmatter)
	ctx.Step(`^the harness retries with specific error feedback$`, h.theHarnessRetriesWithSpecificErrorFeedback)
	ctx.Step(`^the attempt count is greater than (\d+)$`, h.theAttemptCountIsGreaterThan)
	ctx.Step(`^a planner agent is in interview phase$`, h.aPlannerAgentIsInInterviewPhase)
	ctx.Step(`^the user sends a planning question$`, h.theUserSendsAPlanningQuestion)
	ctx.Step(`^the harness does not validate the response$`, h.theHarnessDoesNotValidateTheResponse)
	ctx.Step(`^the response is returned as-is$`, h.theResponseIsReturnedAsIs)
	ctx.Step(`^the planner consistently generates invalid plans$`, h.thePlannerConsistentlyGeneratesInvalidPlans)
	ctx.Step(`^the harness caps retries at (\d+)$`, h.theHarnessCapsRetriesAt)
	ctx.Step(`^returns the best-effort plan with warnings$`, h.returnsTheBestEffortPlanWithWarnings)
	ctx.Step(`^a plan document without YAML frontmatter$`, h.aPlanDocumentWithoutYAMLFrontmatter)
	ctx.Step(`^the schema validator processes the plan$`, h.theSchemaValidatorProcessesThePlan)
	ctx.Step(`^the validation fails with missing frontmatter error$`, h.theValidationFailsWithMissingFrontmatterError)
	ctx.Step(`^the plan score is (\d+\.?\d*)$`, h.thePlanScoreIs)
}

// aPlannerAgentIsConfiguredWithHarnessEnabled verifies the harness has been initialised.
//
// Returns:
//   - An error if the harness is nil.
//
// Side effects:
//   - None.
func (h *HarnessStepDefinitions) aPlannerAgentIsConfiguredWithHarnessEnabled() error {
	if h.harness == nil {
		return errors.New("harness not initialised")
	}
	return nil
}

// thePlannerGeneratesAValidPlan loads a valid plan fixture and evaluates it through the harness.
//
// Returns:
//   - An error if the fixture cannot be loaded or the harness evaluation fails.
//
// Side effects:
//   - Sets h.evaluationResult with the harness output.
func (h *HarnessStepDefinitions) thePlannerGeneratesAValidPlan() error {
	validPlan, err := loadValidPlanFromProject(h.projectRoot)
	if err != nil {
		return err
	}
	streamer := &harnessTestStreamer{responses: []string{validPlan}}
	result, err := h.harness.Evaluate(context.Background(), streamer, "planner", "Generate a plan")
	if err != nil {
		return fmt.Errorf("harness evaluation failed: %w", err)
	}
	h.evaluationResult = result
	return nil
}

// theHarnessAcceptsThePlanWithoutRetry asserts the plan was accepted on the first attempt.
//
// Returns:
//   - An error if the attempt count is not 1 or the plan is not valid.
//
// Side effects:
//   - None.
func (h *HarnessStepDefinitions) theHarnessAcceptsThePlanWithoutRetry() error {
	if h.evaluationResult == nil {
		return errors.New("no evaluation result")
	}
	if h.evaluationResult.AttemptCount != 1 {
		return fmt.Errorf("expected 1 attempt, got %d", h.evaluationResult.AttemptCount)
	}
	if h.evaluationResult.ValidationResult == nil {
		return errors.New("expected validation result, got nil")
	}
	if !h.evaluationResult.ValidationResult.Valid {
		return fmt.Errorf("expected valid plan, got errors: %v", h.evaluationResult.ValidationResult.Errors)
	}
	return nil
}

// theValidationScoreIsAbove asserts the final score exceeds the given threshold.
//
// Expected:
//   - threshold is the minimum acceptable score.
//
// Returns:
//   - An error if the final score is at or below the threshold.
//
// Side effects:
//   - None.
func (h *HarnessStepDefinitions) theValidationScoreIsAbove(threshold float64) error {
	if h.evaluationResult == nil {
		return errors.New("no evaluation result")
	}
	if h.evaluationResult.FinalScore <= threshold {
		return fmt.Errorf("expected score above %f, got %f", threshold, h.evaluationResult.FinalScore)
	}
	return nil
}

// thePlannerGeneratesAnInvalidPlanMissingFrontmatter evaluates an invalid plan followed by a valid retry.
//
// Returns:
//   - An error if the fixture cannot be loaded or the harness evaluation fails.
//
// Side effects:
//   - Sets h.evaluationResult with the harness output from the retry cycle.
func (h *HarnessStepDefinitions) thePlannerGeneratesAnInvalidPlanMissingFrontmatter() error {
	invalidPlan := "---\nid: invalid-plan\ntitle: Invalid Plan\n---\n"
	validPlan, err := loadValidPlanFromProject(h.projectRoot)
	if err != nil {
		return err
	}
	streamer := &harnessTestStreamer{responses: []string{invalidPlan, validPlan}}
	result, err := h.harness.Evaluate(context.Background(), streamer, "planner", "Generate a plan")
	if err != nil {
		return fmt.Errorf("harness evaluation failed: %w", err)
	}
	h.evaluationResult = result
	return nil
}

// theHarnessRetriesWithSpecificErrorFeedback asserts the harness retried with error feedback.
//
// Returns:
//   - An error if fewer than 2 attempts were made or no validation result exists.
//
// Side effects:
//   - None.
func (h *HarnessStepDefinitions) theHarnessRetriesWithSpecificErrorFeedback() error {
	if h.evaluationResult == nil {
		return errors.New("no evaluation result")
	}
	if h.evaluationResult.AttemptCount < 2 {
		return fmt.Errorf("expected at least 2 attempts (indicating retry occurred), got %d", h.evaluationResult.AttemptCount)
	}
	if h.evaluationResult.ValidationResult == nil {
		return errors.New("expected validation result with error details from retry feedback")
	}
	return nil
}

// theAttemptCountIsGreaterThan asserts the attempt count exceeds the given value.
//
// Expected:
//   - count is the minimum number of attempts expected.
//
// Returns:
//   - An error if the attempt count is not greater than count.
//
// Side effects:
//   - None.
func (h *HarnessStepDefinitions) theAttemptCountIsGreaterThan(count int) error {
	if h.evaluationResult == nil {
		return errors.New("no evaluation result")
	}
	if h.evaluationResult.AttemptCount <= count {
		return fmt.Errorf("expected attempt count > %d, got %d", count, h.evaluationResult.AttemptCount)
	}
	return nil
}

// aPlannerAgentIsInInterviewPhase sets up the harness for an interview-phase scenario.
//
// Returns:
//   - An error if the harness is nil.
//
// Side effects:
//   - None.
func (h *HarnessStepDefinitions) aPlannerAgentIsInInterviewPhase() error {
	return h.aPlannerAgentIsConfiguredWithHarnessEnabled()
}

// theUserSendsAPlanningQuestion simulates sending a planning question during interview phase.
//
// Returns:
//   - An error if the harness evaluation fails.
//
// Side effects:
//   - Sets h.evaluationResult with the harness output.
func (h *HarnessStepDefinitions) theUserSendsAPlanningQuestion() error {
	interviewResponse := "Can you tell me more about your project requirements and goals?"
	streamer := &harnessTestStreamer{responses: []string{interviewResponse}}
	result, err := h.harness.Evaluate(context.Background(), streamer, "planner", "I want to build a CLI tool")
	if err != nil {
		return fmt.Errorf("harness evaluation failed: %w", err)
	}
	h.evaluationResult = result
	return nil
}

// theHarnessDoesNotValidateTheResponse asserts no validation was performed during interview phase.
//
// Returns:
//   - An error if a validation result is present.
//
// Side effects:
//   - None.
func (h *HarnessStepDefinitions) theHarnessDoesNotValidateTheResponse() error {
	if h.evaluationResult == nil {
		return errors.New("no evaluation result")
	}
	if h.evaluationResult.ValidationResult != nil {
		return errors.New("expected no validation (interview phase), but validation was performed")
	}
	return nil
}

// theResponseIsReturnedAsIs asserts the response was returned without modification.
//
// Returns:
//   - An error if the plan text is empty.
//
// Side effects:
//   - None.
func (h *HarnessStepDefinitions) theResponseIsReturnedAsIs() error {
	if h.evaluationResult == nil {
		return errors.New("no evaluation result")
	}
	if h.evaluationResult.PlanText == "" {
		return errors.New("expected non-empty response")
	}
	return nil
}

// thePlannerConsistentlyGeneratesInvalidPlans evaluates a streamer that always returns invalid plans.
//
// Returns:
//   - An error if the harness evaluation fails.
//
// Side effects:
//   - Sets h.evaluationResult with the harness output from repeated invalid plans.
func (h *HarnessStepDefinitions) thePlannerConsistentlyGeneratesInvalidPlans() error {
	invalidPlan := "---\nid: invalid-plan\ntitle: Invalid Plan\n---\n"
	streamer := &harnessTestStreamer{responses: []string{invalidPlan, invalidPlan, invalidPlan}}
	result, err := h.harness.Evaluate(context.Background(), streamer, "planner", "Generate a plan")
	if err != nil {
		return fmt.Errorf("harness evaluation failed: %w", err)
	}
	h.evaluationResult = result
	return nil
}

// theHarnessCapsRetriesAt asserts the attempt count does not exceed the given maximum.
//
// Expected:
//   - maxRetries is the maximum number of attempts allowed.
//
// Returns:
//   - An error if the attempt count exceeds maxRetries.
//
// Side effects:
//   - None.
func (h *HarnessStepDefinitions) theHarnessCapsRetriesAt(maxRetries int) error {
	if h.evaluationResult == nil {
		return errors.New("no evaluation result")
	}
	if h.evaluationResult.AttemptCount > maxRetries {
		return fmt.Errorf("expected at most %d attempts, got %d", maxRetries, h.evaluationResult.AttemptCount)
	}
	return nil
}

// returnsTheBestEffortPlanWithWarnings asserts the result contains an invalid plan with warnings.
//
// Returns:
//   - An error if the plan is valid or has no warnings.
//
// Side effects:
//   - None.
func (h *HarnessStepDefinitions) returnsTheBestEffortPlanWithWarnings() error {
	if h.evaluationResult == nil {
		return errors.New("no evaluation result")
	}
	if h.evaluationResult.ValidationResult == nil {
		return errors.New("expected validation result with warnings")
	}
	if h.evaluationResult.ValidationResult.Valid {
		return errors.New("expected invalid plan (best-effort), got valid")
	}
	if len(h.evaluationResult.ValidationResult.Warnings) == 0 {
		return errors.New("expected warnings in best-effort result")
	}
	return nil
}

// aPlanDocumentWithoutYAMLFrontmatter sets up a plan document that lacks YAML frontmatter.
//
// Returns:
//   - Always returns nil.
//
// Side effects:
//   - Sets h.planText to a plain text string without frontmatter.
func (h *HarnessStepDefinitions) aPlanDocumentWithoutYAMLFrontmatter() error {
	h.planText = "This is just plain text without frontmatter"
	return nil
}

// theSchemaValidatorProcessesThePlan runs schema validation on the configured plan text.
//
// Returns:
//   - An error if no plan text has been configured.
//
// Side effects:
//   - Sets h.validationResult with the schema validation output.
func (h *HarnessStepDefinitions) theSchemaValidatorProcessesThePlan() error {
	if h.planText == "" {
		return errors.New("no plan text configured — Given step must set up the plan document first")
	}
	validator := &validation.SchemaValidator{}
	result, _ := validator.Validate(h.planText)
	h.validationResult = result
	return nil
}

// theValidationFailsWithMissingFrontmatterError asserts the validation failed with a frontmatter error.
//
// Returns:
//   - An error if the validation passed or lacks the expected frontmatter error.
//
// Side effects:
//   - None.
func (h *HarnessStepDefinitions) theValidationFailsWithMissingFrontmatterError() error {
	if h.validationResult == nil {
		return errors.New("no validation result")
	}
	if h.validationResult.Valid {
		return errors.New("expected invalid result")
	}
	hasFrontmatterError := false
	for _, e := range h.validationResult.Errors {
		if e == "missing YAML frontmatter" {
			hasFrontmatterError = true
			break
		}
	}
	if !hasFrontmatterError {
		return fmt.Errorf("expected 'missing YAML frontmatter' error, got: %v", h.validationResult.Errors)
	}
	return nil
}

// thePlanScoreIs asserts the validation score matches the expected value.
//
// Expected:
//   - expectedScore is the exact score to match.
//
// Returns:
//   - An error if the score does not match.
//
// Side effects:
//   - None.
func (h *HarnessStepDefinitions) thePlanScoreIs(expectedScore float64) error {
	if h.validationResult == nil {
		return errors.New("no validation result")
	}
	if h.validationResult.Score != expectedScore {
		return fmt.Errorf("expected score %f, got %f", expectedScore, h.validationResult.Score)
	}
	return nil
}

// loadValidPlanFromProject reads the valid plan fixture from the project's testdata directory.
//
// Expected:
//   - projectRoot contains the internal/plan/testdata/valid_plan.md fixture file.
//
// Returns:
//   - The plan fixture content as a string.
//   - An error if the file cannot be read.
//
// Side effects:
//   - Reads a file from disk.
func loadValidPlanFromProject(projectRoot string) (string, error) {
	planPath := filepath.Join(projectRoot, "internal", "plan", "testdata", "valid_plan.md")
	data, err := os.ReadFile(planPath)
	if err != nil {
		return "", fmt.Errorf("reading valid plan fixture: %w", err)
	}
	return string(data), nil
}
