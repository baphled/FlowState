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
func NewValidatorChain(projectRoot string) *ValidatorChain {
	return &ValidatorChain{
		schemaValidator:    &SchemaValidator{},
		assertionValidator: &AssertionValidator{},
		referenceValidator: &ReferenceValidator{},
		projectRoot:        projectRoot,
	}
}

// Validate runs schema, assertion, and reference validation with short-circuit and weighted scoring.
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

	// Combine results with weighted scoring
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
