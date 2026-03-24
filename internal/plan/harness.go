package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
	"gopkg.in/yaml.v3"
)

// Streamer is the interface for streaming AI responses.
type Streamer interface {
	// Stream returns streaming response chunks for the agent request.
	Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error)
}

// EvaluationResult holds the outcome of a harness evaluation.
type EvaluationResult struct {
	PlanText         string
	ValidationResult *ValidationResult
	AttemptCount     int
	FinalScore       float64
}

// PlanHarness wraps a Streamer with validate-retry logic.
//
//nolint:revive // PlanHarness name is intentional for plan-specific workflows.
type PlanHarness struct {
	maxRetries         int
	projectRoot        string
	schemaValidator    *SchemaValidator
	assertionValidator *AssertionValidator
	referenceValidator *ReferenceValidator
}

// NewPlanHarness creates a PlanHarness with validators and retry settings.
//
// Expected:
//   - projectRoot is the absolute path to the project root directory.
//
// Returns:
//   - A configured PlanHarness with schema, assertion, and reference validators.
//
// Side effects:
//   - None.
func NewPlanHarness(projectRoot string) *PlanHarness {
	return &PlanHarness{
		maxRetries:         3,
		projectRoot:        projectRoot,
		schemaValidator:    &SchemaValidator{},
		assertionValidator: &AssertionValidator{},
		referenceValidator: &ReferenceValidator{},
	}
}

// Evaluate runs the plan harness over a streaming response.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - streamer provides streaming access to the LLM.
//   - agentID identifies the planner agent.
//   - message is the initial planning prompt.
//
// Returns:
//   - An EvaluationResult containing the plan text, validation result, attempt count, and final score.
//   - An error if streaming or context cancellation fails.
//
// Side effects:
//   - Streams responses from the LLM; may retry up to maxRetries times.
func (h *PlanHarness) Evaluate(
	ctx context.Context,
	streamer Streamer,
	agentID string,
	message string,
) (*EvaluationResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	aggregator := &Aggregator{}
	currentMessage := message

	for attempt := 1; attempt <= h.maxRetries; attempt++ {
		planText, err := streamPlan(ctx, streamer, aggregator, agentID, currentMessage)
		if err != nil {
			return nil, err
		}

		phase := hook.DetectPhase(planText)
		if phase != hook.PhaseGeneration {
			return &EvaluationResult{
				PlanText:     planText,
				AttemptCount: attempt,
			}, nil
		}

		validation := h.validatePlan(planText)
		if validation.Valid {
			return &EvaluationResult{
				PlanText:         planText,
				ValidationResult: validation,
				AttemptCount:     attempt,
				FinalScore:       validation.Score,
			}, nil
		}

		if attempt < h.maxRetries {
			feedback := buildFeedback(validation)
			currentMessage = appendFeedback(currentMessage, feedback)
			continue
		}

		validation.Warnings = append(validation.Warnings, fmt.Sprintf("validation failed after %d attempts", attempt))
		return &EvaluationResult{
			PlanText:         planText,
			ValidationResult: validation,
			AttemptCount:     attempt,
			FinalScore:       validation.Score,
		}, nil
	}

	return nil, errors.New("evaluation exhausted retries")
}

// validatePlan runs schema, assertion, and reference validation against the given plan text.
//
// Expected:
//   - planText contains a plan document to validate.
//
// Returns:
//   - A combined ValidationResult from all validators.
//
// Side effects:
//   - None.
func (h *PlanHarness) validatePlan(planText string) *ValidationResult {
	schemaResult, err := h.schemaValidator.Validate(planText)
	if err != nil {
		return schemaResult
	}

	plan := &File{Tasks: tasksFromPlanText(planText)}
	assertionResult, assertionErr := h.assertionValidator.Validate(plan)
	if assertionErr != nil && assertionResult != nil {
		assertionResult.Warnings = append(assertionResult.Warnings, assertionErr.Error())
	}
	referenceResult, referenceErr := h.referenceValidator.Validate(planText, h.projectRoot)
	if referenceErr != nil && referenceResult != nil {
		referenceResult.Warnings = append(referenceResult.Warnings, referenceErr.Error())
	}

	return combineValidationResults(schemaResult, assertionResult, referenceResult)
}

// streamPlan streams a plan response from the LLM and aggregates the chunks into a single string.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - streamer provides streaming access to the LLM.
//
// Returns:
//   - The aggregated plan text string.
//   - An error if streaming or aggregation fails.
//
// Side effects:
//   - Streams data from the LLM via the streamer.
func streamPlan(
	ctx context.Context,
	streamer Streamer,
	aggregator *Aggregator,
	agentID string,
	message string,
) (string, error) {
	chunks, err := streamer.Stream(ctx, agentID, message)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		return "", fmt.Errorf("streaming response: %w", err)
	}

	planText, err := aggregator.Aggregate(ctx, chunks)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		return "", fmt.Errorf("aggregating stream: %w", err)
	}

	return planText, nil
}

// tasksFromPlanText extracts and normalises tasks from a plan's markdown body.
//
// Expected:
//   - planText contains a plan with YAML frontmatter delimiters.
//
// Returns:
//   - A slice of Task values parsed from the markdown body.
//
// Side effects:
//   - None.
func tasksFromPlanText(planText string) []Task {
	parts := strings.SplitN(planText, "---", 3)
	if len(parts) < 3 {
		return []Task{}
	}
	tasks := parseTasksFromMarkdown(parts[2])
	for i := range tasks {
		tasks[i].Dependencies = normalizeDependencies(tasks[i].Dependencies)
	}
	return tasks
}

// normalizeDependencies removes empty and "none" entries from a dependency list.
//
// Expected:
//   - deps is a string slice of dependency identifiers (may contain empty or "none" values).
//
// Returns:
//   - A cleaned slice with empty and "none" entries removed.
//
// Side effects:
//   - None.
func normalizeDependencies(deps []string) []string {
	if len(deps) == 0 {
		return deps
	}

	cleaned := make([]string, 0, len(deps))
	for _, dep := range deps {
		trimmed := strings.TrimSpace(dep)
		if trimmed == "" {
			continue
		}
		if strings.EqualFold(trimmed, "none") {
			continue
		}
		cleaned = append(cleaned, trimmed)
	}
	return cleaned
}

// combineValidationResults merges multiple validation results into a single averaged result.
//
// Expected:
//   - results contains zero or more ValidationResult pointers (nil entries are skipped).
//
// Returns:
//   - A single ValidationResult with averaged score and combined errors and warnings.
//
// Side effects:
//   - None.
func combineValidationResults(results ...*ValidationResult) *ValidationResult {
	combined := &ValidationResult{Valid: true, Score: 1.0}
	count := 0
	scoreSum := 0.0

	for _, result := range results {
		if result == nil {
			continue
		}
		count++
		scoreSum += result.Score
		if !result.Valid {
			combined.Valid = false
		}
		combined.Errors = append(combined.Errors, result.Errors...)
		combined.Warnings = append(combined.Warnings, result.Warnings...)
	}

	if count == 0 {
		combined.Score = 0.0
	} else {
		combined.Score = scoreSum / float64(count)
	}

	if combined.Score < 0.0 {
		combined.Score = 0.0
	}
	if combined.Score > 1.0 {
		combined.Score = 1.0
	}
	if len(combined.Errors) > 0 {
		combined.Valid = false
	}

	return combined
}

// buildFeedback constructs a human-readable feedback string from validation errors and warnings.
//
// Expected:
//   - result is a non-nil ValidationResult.
//
// Returns:
//   - A formatted feedback string listing validation issues.
//
// Side effects:
//   - None.
func buildFeedback(result *ValidationResult) string {
	issues := result.Errors
	if len(issues) == 0 {
		issues = result.Warnings
	}
	if len(issues) == 0 {
		issues = []string{"unknown validation failure"}
	}

	var builder strings.Builder
	builder.WriteString("Your plan failed validation. Issues:\n")
	for _, issue := range issues {
		builder.WriteString("- ")
		builder.WriteString(issue)
		builder.WriteString("\n")
	}
	builder.WriteString("Fix these specific issues and regenerate the complete plan.")
	return builder.String()
}

// appendFeedback appends validation feedback to the original message for retry prompts.
//
// Expected:
//   - feedback contains the validation feedback to append.
//
// Returns:
//   - The original message with feedback appended, or just the feedback if the message is empty.
//
// Side effects:
//   - None.
func appendFeedback(message string, feedback string) string {
	if strings.TrimSpace(message) == "" {
		return feedback
	}
	return message + "\n\n" + feedback
}

// ValidatorChain composes schema, assertion, and reference validators with short-circuit logic and weighted scoring.
type ValidatorChain struct {
	schemaValidator    *SchemaValidator
	assertionValidator *AssertionValidator
	referenceValidator *ReferenceValidator
	projectRoot        string
}

// NewValidatorChain creates a ValidatorChain with all validators.
//
// Expected:
//   - projectRoot is the absolute path to the project root directory.
//
// Returns:
//   - A configured ValidatorChain with schema, assertion, and reference validators.
//
// Side effects:
//   - None.
func NewValidatorChain(projectRoot string) *ValidatorChain {
	return &ValidatorChain{
		schemaValidator:    &SchemaValidator{},
		assertionValidator: &AssertionValidator{},
		referenceValidator: &ReferenceValidator{},
		projectRoot:        projectRoot,
	}
}

// Validate runs schema, assertion, and reference validation with short-circuit and weighted scoring.
//
// Expected:
//   - planText contains a plan with YAML frontmatter and markdown body.
//
// Returns:
//   - A ValidationResult with aggregated errors, warnings, and a weighted score.
//   - An error if any validator encounters a fatal failure.
//
// Side effects:
//   - None.
func (v *ValidatorChain) Validate(planText string) (*ValidationResult, error) {
	schemaResult, schemaErr := v.schemaValidator.Validate(planText)
	if schemaErr != nil {
		return schemaResult, schemaErr
	}
	if !schemaResult.Valid {
		return schemaResult, nil
	}

	file, err := parseFile(planText)
	if err != nil {
		return &ValidationResult{Valid: false, Errors: []string{fmt.Sprintf("failed to parse plan: %v", err)}}, nil
	}
	assertionResult, assertionErr := v.assertionValidator.Validate(file)
	if assertionErr != nil {
		return assertionResult, assertionErr
	}

	referenceResult, referenceErr := v.referenceValidator.Validate(planText, v.projectRoot)
	if referenceErr != nil {
		return referenceResult, referenceErr
	}

	combined := &ValidationResult{Valid: true, Score: 1.0}
	results := []*ValidationResult{schemaResult, assertionResult, referenceResult}
	count := 0
	scoreSum := 0.0

	for _, result := range results {
		if result == nil {
			continue
		}
		count++
		scoreSum += result.Score
		if !result.Valid {
			combined.Valid = false
		}
		combined.Errors = append(combined.Errors, result.Errors...)
		combined.Warnings = append(combined.Warnings, result.Warnings...)
	}

	if count == 0 {
		combined.Score = 0.0
	} else {
		combined.Score = scoreSum / float64(count)
	}

	if combined.Score < 0.0 {
		combined.Score = 0.0
	}
	if combined.Score > 1.0 {
		combined.Score = 1.0
	}
	if len(combined.Errors) > 0 {
		combined.Valid = false
	}

	return combined, nil
}

// parseFile extracts and unmarshals YAML frontmatter from a plan text into a File struct.
//
// Expected:
//   - planText contains a plan with YAML frontmatter delimited by "---".
//
// Returns:
//   - A File struct populated from the YAML frontmatter.
//   - An error if the frontmatter is missing or cannot be unmarshalled.
//
// Side effects:
//   - None.
func parseFile(planText string) (*File, error) {
	parts := strings.SplitN(planText, "---", 3)
	if len(parts) < 3 {
		return nil, errors.New("missing YAML frontmatter")
	}

	var file File
	if err := yaml.Unmarshal([]byte(parts[1]), &file); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	return &file, nil
}
