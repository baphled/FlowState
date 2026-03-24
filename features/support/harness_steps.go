package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/provider"
)

// HarnessStepDefinitions holds state for harness BDD scenarios.
type HarnessStepDefinitions struct {
	harness          *plan.PlanHarness
	evaluationResult *plan.EvaluationResult
	validationResult *plan.ValidationResult
	projectRoot      string
}

type harnessTestStreamer struct {
	responses []string
	callCount int
}

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
func RegisterHarnessSteps(ctx *godog.ScenarioContext) {
	h := &HarnessStepDefinitions{}

	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		cwd, err := os.Getwd()
		if err != nil {
			return bddCtx, err
		}
		h.projectRoot = filepath.Join(cwd, "..", "..")
		h.harness = plan.NewPlanHarness(h.projectRoot)
		h.evaluationResult = nil
		h.validationResult = nil
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

func (h *HarnessStepDefinitions) aPlannerAgentIsConfiguredWithHarnessEnabled() error {
	if h.harness == nil {
		return errors.New("harness not initialised")
	}
	return nil
}

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

func (h *HarnessStepDefinitions) theValidationScoreIsAbove(threshold float64) error {
	if h.evaluationResult == nil {
		return errors.New("no evaluation result")
	}
	if h.evaluationResult.FinalScore <= threshold {
		return fmt.Errorf("expected score above %f, got %f", threshold, h.evaluationResult.FinalScore)
	}
	return nil
}

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

func (h *HarnessStepDefinitions) theHarnessRetriesWithSpecificErrorFeedback() error {
	if h.evaluationResult == nil {
		return errors.New("no evaluation result")
	}
	if h.evaluationResult.AttemptCount < 2 {
		return fmt.Errorf("expected at least 2 attempts, got %d", h.evaluationResult.AttemptCount)
	}
	return nil
}

func (h *HarnessStepDefinitions) theAttemptCountIsGreaterThan(count int) error {
	if h.evaluationResult == nil {
		return errors.New("no evaluation result")
	}
	if h.evaluationResult.AttemptCount <= count {
		return fmt.Errorf("expected attempt count > %d, got %d", count, h.evaluationResult.AttemptCount)
	}
	return nil
}

func (h *HarnessStepDefinitions) aPlannerAgentIsInInterviewPhase() error {
	return h.aPlannerAgentIsConfiguredWithHarnessEnabled()
}

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

func (h *HarnessStepDefinitions) theHarnessDoesNotValidateTheResponse() error {
	if h.evaluationResult == nil {
		return errors.New("no evaluation result")
	}
	if h.evaluationResult.ValidationResult != nil {
		return errors.New("expected no validation (interview phase), but validation was performed")
	}
	return nil
}

func (h *HarnessStepDefinitions) theResponseIsReturnedAsIs() error {
	if h.evaluationResult == nil {
		return errors.New("no evaluation result")
	}
	if h.evaluationResult.PlanText == "" {
		return errors.New("expected non-empty response")
	}
	return nil
}

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

func (h *HarnessStepDefinitions) theHarnessCapsRetriesAt(maxRetries int) error {
	if h.evaluationResult == nil {
		return errors.New("no evaluation result")
	}
	if h.evaluationResult.AttemptCount > maxRetries {
		return fmt.Errorf("expected at most %d attempts, got %d", maxRetries, h.evaluationResult.AttemptCount)
	}
	return nil
}

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

func (h *HarnessStepDefinitions) aPlanDocumentWithoutYAMLFrontmatter() error {
	return nil
}

func (h *HarnessStepDefinitions) theSchemaValidatorProcessesThePlan() error {
	validator := &plan.SchemaValidator{}
	result, _ := validator.Validate("This is just plain text without frontmatter")
	h.validationResult = result
	return nil
}

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

func (h *HarnessStepDefinitions) thePlanScoreIs(expectedScore float64) error {
	if h.validationResult == nil {
		return errors.New("no validation result")
	}
	if h.validationResult.Score != expectedScore {
		return fmt.Errorf("expected score %f, got %f", expectedScore, h.validationResult.Score)
	}
	return nil
}

func loadValidPlanFromProject(projectRoot string) (string, error) {
	planPath := filepath.Join(projectRoot, "internal", "plan", "testdata", "valid_plan.md")
	data, err := os.ReadFile(planPath)
	if err != nil {
		return "", fmt.Errorf("reading valid plan fixture: %w", err)
	}
	return string(data), nil
}
