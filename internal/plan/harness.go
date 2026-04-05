package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
)

// Streamer is the interface for streaming AI responses.
type Streamer interface {
	// Stream returns streaming response chunks for the agent request.
	Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error)
}

// HarnessOption configures optional behaviour on a Harness.
//
// Expected:
//   - Each option mutates the harness during construction only.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type HarnessOption func(*Harness)

// WithCritic attaches an LLM critic and its provider to the harness.
//
// Expected:
//   - critic is a configured LLMCritic (enabled or disabled).
//   - p is a valid provider for critic chat completions.
//
// Returns:
//   - A HarnessOption that sets the critic and provider fields.
//
// Side effects:
//   - None.
func WithCritic(critic *LLMCritic, p provider.Provider) HarnessOption {
	return func(h *Harness) {
		h.critic = critic
		h.criticProvider = p
	}
}

// WithVoter attaches a ConsistencyVoter to the harness.
//
// Expected:
//   - voter is a configured ConsistencyVoter (may be nil for no voting).
//
// Returns:
//   - A HarnessOption that sets the voter field.
//
// Side effects:
//   - None.
func WithVoter(voter *ConsistencyVoter) HarnessOption {
	return func(h *Harness) {
		h.voter = voter
	}
}

// WithMaxRetries sets the maximum number of evaluation attempts on the harness.
//
// Expected:
//   - n is a positive integer. Values less than 1 are ignored.
//
// Returns:
//   - A HarnessOption that sets the maxRetries field.
//
// Side effects:
//   - None.
func WithMaxRetries(n int) HarnessOption {
	return func(h *Harness) {
		if n >= 1 {
			h.maxRetries = n
		}
	}
}

// schemaValidation validates plan documents for required structure and content.
type schemaValidation interface {
	Validate(planText string) (*ValidationResult, error)
}

// assertionValidation performs semantic validation on a plan File.
type assertionValidation interface {
	Validate(planFile *File) (*ValidationResult, error)
}

// referenceValidation checks that file references in plan text exist under the project root.
type referenceValidation interface {
	Validate(planText string, projectRoot string) (*ValidationResult, error)
}

// Harness wraps a Streamer with validate-retry logic and optional LLM critic.
type Harness struct {
	maxRetries         int
	projectRoot        string
	schemaValidator    schemaValidation
	assertionValidator assertionValidation
	referenceValidator referenceValidation
	critic             *LLMCritic
	criticProvider     provider.Provider
	voter              *ConsistencyVoter
}

// NewHarness creates a Harness with validators, retry settings, and optional configuration.
//
// Expected:
//   - projectRoot is the absolute path to the project root directory.
//   - opts are optional HarnessOption values (e.g. WithCritic).
//
// Returns:
//   - A configured Harness with schema, assertion, and reference validators.
//
// Side effects:
//   - None.
func NewHarness(projectRoot string, opts ...HarnessOption) *Harness {
	h := &Harness{
		maxRetries:  1,
		projectRoot: projectRoot,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// WithSchemaValidator sets the schema validator used by the harness.
//
// Expected:
//   - v implements the schemaValidation interface.
//
// Returns:
//   - A HarnessOption that sets the schema validator.
//
// Side effects:
//   - None.
func WithSchemaValidator(v schemaValidation) HarnessOption {
	return func(h *Harness) { h.schemaValidator = v }
}

// WithAssertionValidator sets the assertion validator used by the harness.
//
// Expected:
//   - v implements the assertionValidation interface.
//
// Returns:
//   - A HarnessOption that sets the assertion validator.
//
// Side effects:
//   - None.
func WithAssertionValidator(v assertionValidation) HarnessOption {
	return func(h *Harness) { h.assertionValidator = v }
}

// WithReferenceValidator sets the reference validator used by the harness.
//
// Expected:
//   - v implements the referenceValidation interface.
//
// Returns:
//   - A HarnessOption that sets the reference validator.
//
// Side effects:
//   - None.
func WithReferenceValidator(v referenceValidation) HarnessOption {
	return func(h *Harness) { h.referenceValidator = v }
}

// StreamEvaluate runs the plan harness over a streaming response, forwarding chunks in real-time.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - streamer provides streaming access to the LLM.
//   - agentID identifies the planner agent.
//   - message is the initial planning prompt.
//
// Returns:
//   - A read-only channel of StreamChunk values forwarded live from the LLM.
//   - An error if the initial context is already cancelled.
//
// Side effects:
//   - Spawns a goroutine that streams responses, validates, and retries up to maxRetries times.
//   - Emits harness_retry event chunks between retry attempts.
//   - The returned channel is closed when streaming and evaluation are complete.
func (h *Harness) StreamEvaluate(
	ctx context.Context,
	streamer Streamer,
	agentID string,
	message string,
) (<-chan provider.StreamChunk, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	outCh := make(chan provider.StreamChunk)
	go h.runStreamEvaluation(ctx, streamer, agentID, message, outCh)
	return outCh, nil
}

// trySend sends a chunk to outCh, aborting if the context is cancelled.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - outCh is the destination channel for the chunk.
//   - chunk is the StreamChunk to deliver.
//
// Returns:
//   - True if the chunk was sent, false if the context was cancelled.
//
// Side effects:
//   - Sends chunk to outCh or blocks until the context is cancelled.
func trySend(ctx context.Context, outCh chan<- provider.StreamChunk, chunk provider.StreamChunk) bool {
	select {
	case outCh <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

// runStreamEvaluation executes the retry loop for StreamEvaluate in a dedicated goroutine.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - streamer provides streaming access to the LLM.
//   - agentID identifies the planner agent.
//   - message is the initial planning prompt.
//   - outCh is the channel to forward chunks to.
//
// Returns:
//   - (nothing; sends results via outCh)
//
// Side effects:
//   - Closes outCh when evaluation completes.
//   - Sends error chunks on stream failures.
//   - Sends harness_retry event chunks between retry attempts.
func (h *Harness) runStreamEvaluation(
	ctx context.Context,
	streamer Streamer,
	agentID string,
	message string,
	outCh chan<- provider.StreamChunk,
) {
	defer close(outCh)
	currentMessage := message

	for attempt := 1; attempt <= h.maxRetries; attempt++ {
		trySend(ctx, outCh, provider.StreamChunk{
			EventType: "harness_attempt_start",
			Content:   fmt.Sprintf(`{"attempt":%d,"maxRetries":%d}`, attempt, h.maxRetries),
		})

		planText, err := h.streamAttempt(ctx, streamer, agentID, currentMessage, outCh)
		if err != nil {
			trySend(ctx, outCh, provider.StreamChunk{Error: err})
			return
		}

		phase := hook.DetectPhase(planText)
		slog.Info("harness phase detected", "phase", phaseString(phase), "agentID", agentID)
		if phase != hook.PhaseGeneration {
			trySend(ctx, outCh, provider.StreamChunk{Done: true})
			return
		}

		result, feedback := h.evaluateStreamAttempt(ctx, planText, attempt, outCh)
		if result != nil {
			result = h.applyVoter(ctx, streamer, agentID, currentMessage, result)
			emitPlanArtifact(ctx, outCh, result)
			emitHarnessComplete(ctx, outCh, result)
			trySend(ctx, outCh, provider.StreamChunk{Done: true})
			return
		}

		if attempt < h.maxRetries {
			slog.Warn("harness retrying", "attempt", attempt, "maxRetries", h.maxRetries, "feedbackLen", len(feedback))
			retryChunk := provider.StreamChunk{
				EventType: "harness_retry",
				Content:   fmt.Sprintf("Plan validation failed (attempt %d/%d). Retrying...", attempt, h.maxRetries),
			}
			if !trySend(ctx, outCh, retryChunk) {
				return
			}
		}
		currentMessage = appendFeedback(currentMessage, feedback)
	}

	slog.Error("harness exhausted retries", "maxRetries", h.maxRetries)
	trySend(ctx, outCh, provider.StreamChunk{Done: true})
}

// streamAttempt streams a single attempt from the LLM, forwarding chunks to outCh while accumulating text.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - streamer provides streaming access to the LLM.
//   - agentID identifies the planner agent.
//   - message is the planning prompt for this attempt.
//   - outCh is the channel to forward live chunks to.
//
// Returns:
//   - The accumulated plan text from all received chunks.
//   - An error if streaming fails, context is cancelled, or the plan exceeds 1MB.
//
// Side effects:
//   - Forwards each content chunk to outCh in real-time.
//   - Suppresses the inner Done chunk (the caller owns final Done signalling).
func (h *Harness) streamAttempt(
	ctx context.Context,
	streamer Streamer,
	agentID string,
	message string,
	outCh chan<- provider.StreamChunk,
) (string, error) {
	chunks, err := streamer.Stream(ctx, agentID, message)
	if err != nil {
		return "", fmt.Errorf("streaming response: %w", err)
	}

	return h.forwardAndAccumulate(ctx, chunks, outCh)
}

// forwardAndAccumulate reads from an inner chunk channel, forwarding to outCh and accumulating text.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - chunks is the inner chunk channel from the streamer.
//   - outCh is the channel to forward live chunks to.
//
// Returns:
//   - The accumulated plan text.
//   - An error if the stream contains an error chunk, exceeds 1MB, or context is cancelled.
//
// Side effects:
//   - Forwards non-Done chunks to outCh.
func (h *Harness) forwardAndAccumulate(
	ctx context.Context,
	chunks <-chan provider.StreamChunk,
	outCh chan<- provider.StreamChunk,
) (string, error) {
	var builder strings.Builder
	received := false

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				return closedChannelResult(builder.String(), received)
			}
			forwarded, err := processStreamChunk(ctx, chunk, &builder, outCh)
			if err != nil {
				return "", err
			}
			if forwarded {
				received = true
			}
		}
	}
}

// closedChannelResult returns the accumulated text or an error if no content was received.
//
// Expected:
//   - text is the accumulated plan content.
//   - received indicates whether any content chunks were processed.
//
// Returns:
//   - The accumulated text if content was received, or an error if the stream was empty.
//
// Side effects:
//   - None.
func closedChannelResult(text string, received bool) (string, error) {
	if !received {
		return "", errors.New("empty stream: no content received")
	}
	return text, nil
}

// processStreamChunk handles a single chunk: checks for errors, skips Done markers,
// accumulates content, and forwards to outCh.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - chunk is a StreamChunk to process.
//   - builder accumulates the plan text content.
//   - outCh is the channel to forward non-Done, non-error chunks to.
//
// Returns:
//   - True if content was forwarded, false otherwise.
//   - An error if the chunk contained an error, the accumulated content exceeds 1MB, or the context is cancelled.
//
// Side effects:
//   - Sends non-Done chunks to outCh.
//   - Appends chunk content to the builder.
func processStreamChunk(
	ctx context.Context,
	chunk provider.StreamChunk,
	builder *strings.Builder,
	outCh chan<- provider.StreamChunk,
) (bool, error) {
	if chunk.Error != nil {
		return false, chunk.Error
	}
	if chunk.Done {
		return false, nil
	}
	if chunk.Content != "" {
		builder.WriteString(chunk.Content)
		if builder.Len() > maxPlanSize {
			return false, errors.New("plan exceeds maximum size of 1MB")
		}
	}
	select {
	case outCh <- chunk:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	return chunk.Content != "", nil
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
func (h *Harness) Evaluate(
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
			return &EvaluationResult{PlanText: planText, AttemptCount: attempt}, nil
		}

		result, feedback := h.evaluateAttempt(ctx, planText, attempt)
		if result != nil {
			result = h.applyVoter(ctx, streamer, agentID, currentMessage, result)
			return result, nil
		}
		currentMessage = appendFeedback(currentMessage, feedback)
	}

	return nil, errors.New("evaluation exhausted retries")
}

// evaluateAttempt validates and critiques a single plan attempt.
//
// Expected:
//   - ctx is a valid context.
//   - planText contains a plan in the generation phase.
//   - attempt is the current 1-based attempt number.
//
// Returns:
//   - A final EvaluationResult if the attempt concludes evaluation, or nil with retry feedback.
//
// Side effects:
//   - May send a chat request to the critic provider.
func (h *Harness) evaluateAttempt(ctx context.Context, planText string, attempt int) (*EvaluationResult, string) {
	validation := h.validatePlan(planText)
	if validation.Valid {
		return h.handleValidPlan(ctx, planText, validation, attempt)
	}

	if attempt < h.maxRetries {
		return nil, buildFeedback(validation)
	}

	validation.Warnings = append(validation.Warnings, "validation failed after "+strconv.Itoa(attempt)+" attempts")
	return &EvaluationResult{
		PlanText: planText, ValidationResult: validation,
		AttemptCount: attempt, FinalScore: validation.Score,
	}, ""
}

// handleValidPlan runs the critic on a structurally valid plan and returns a result or retry feedback.
//
// Expected:
//   - ctx is a valid context.
//   - planText is the structurally valid plan text.
//   - validation is the passing ValidationResult.
//   - attempt is the current 1-based attempt number.
//
// Returns:
//   - A final EvaluationResult if critic passes or final attempt, or nil with critic feedback for retry.
//
// Side effects:
//   - May send a chat request to the critic provider.
func (h *Harness) handleValidPlan(
	ctx context.Context, planText string, validation *ValidationResult, attempt int,
) (*EvaluationResult, string) {
	criticFeedback := h.runCritic(ctx, planText, validation)
	if criticFeedback == "" {
		return &EvaluationResult{
			PlanText: planText, ValidationResult: validation,
			AttemptCount: attempt, FinalScore: validation.Score,
		}, ""
	}
	if attempt < h.maxRetries {
		return nil, criticFeedback
	}
	validation.Warnings = append(validation.Warnings, "critic rejected plan on final attempt")
	return &EvaluationResult{
		PlanText: planText, ValidationResult: validation,
		AttemptCount: attempt, FinalScore: validation.Score,
	}, ""
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
func (h *Harness) validatePlan(planText string) *ValidationResult {
	schemaResult, err := h.schemaValidator.Validate(planText)
	if err != nil {
		return schemaResult
	}

	planFile := &File{Tasks: tasksFromPlanText(planText)}
	assertionResult, assertionErr := h.assertionValidator.Validate(planFile)
	if assertionErr != nil && assertionResult != nil {
		assertionResult.Warnings = append(assertionResult.Warnings, assertionErr.Error())
	}
	referenceResult, referenceErr := h.referenceValidator.Validate(planText, h.projectRoot)
	if referenceErr != nil && referenceResult != nil {
		referenceResult.Warnings = append(referenceResult.Warnings, referenceErr.Error())
	}

	return combineValidationResults(schemaResult, assertionResult, referenceResult)
}

// runCritic invokes the LLM critic if configured and returns feedback for retry, or empty string on pass.
//
// Expected:
//   - ctx is a valid context.
//   - planText is the raw plan text.
//   - validation is the validator result to pass to the critic.
//
// Returns:
//   - A feedback string for retry if the critic rejects the plan, or empty string if critic passes or is unconfigured.
//
// Side effects:
//   - Sends a chat request to the critic provider when critic is configured and enabled.
func (h *Harness) runCritic(ctx context.Context, planText string, validation *ValidationResult) string {
	if h.critic == nil || h.criticProvider == nil {
		return ""
	}

	parsedPlan, parseErr := parseFile(planText)
	if parseErr != nil {
		parsedPlan = nil
	}

	criticResult, err := h.critic.Review(ctx, parsedPlan, planText, validation, h.criticProvider)
	if err != nil {
		validation.Warnings = append(validation.Warnings, "critic error: "+err.Error())
		return ""
	}

	if criticResult.Verdict == VerdictFail {
		return buildCriticFeedback(criticResult)
	}

	return ""
}

// evaluateStreamAttempt validates and critiques a single plan attempt, emitting events to outCh.
//
// Expected:
//   - ctx is a valid context.
//   - planText contains a plan in the generation phase.
//   - attempt is the current 1-based attempt number.
//   - outCh is the channel for emitting observability events.
//
// Returns:
//   - A final EvaluationResult if the attempt concludes evaluation, or nil with retry feedback.
//
// Side effects:
//   - Emits harness_critic_feedback and slog validation events via outCh.
func (h *Harness) evaluateStreamAttempt(
	ctx context.Context, planText string, attempt int, outCh chan<- provider.StreamChunk,
) (*EvaluationResult, string) {
	validation := h.validatePlan(planText)
	slog.Info("harness validation",
		"valid", validation.Valid,
		"score", validation.Score,
		"errors", len(validation.Errors),
		"warnings", len(validation.Warnings),
	)
	if validation.Valid {
		return h.handleValidPlanStream(ctx, planText, validation, attempt, outCh)
	}

	if attempt < h.maxRetries {
		return nil, buildFeedback(validation)
	}

	validation.Warnings = append(validation.Warnings, "validation failed after "+strconv.Itoa(attempt)+" attempts")
	return &EvaluationResult{
		PlanText: planText, ValidationResult: validation,
		AttemptCount: attempt, FinalScore: validation.Score,
	}, ""
}

// handleValidPlanStream runs the critic on a structurally valid plan, emitting feedback events to outCh.
//
// Expected:
//   - ctx is a valid context.
//   - planText is the structurally valid plan text.
//   - validation is the passing ValidationResult.
//   - attempt is the current 1-based attempt number.
//   - outCh is the channel for emitting observability events.
//
// Returns:
//   - A final EvaluationResult if critic passes or final attempt, or nil with critic feedback for retry.
//
// Side effects:
//   - Emits harness_critic_feedback via outCh when the critic runs.
func (h *Harness) handleValidPlanStream(
	ctx context.Context, planText string, validation *ValidationResult, attempt int, outCh chan<- provider.StreamChunk,
) (*EvaluationResult, string) {
	criticFeedback := h.runCriticStream(ctx, planText, validation, outCh)
	if criticFeedback == "" {
		return &EvaluationResult{
			PlanText: planText, ValidationResult: validation,
			AttemptCount: attempt, FinalScore: validation.Score,
		}, ""
	}
	if attempt < h.maxRetries {
		return nil, criticFeedback
	}
	validation.Warnings = append(validation.Warnings, "critic rejected plan on final attempt")
	return &EvaluationResult{
		PlanText: planText, ValidationResult: validation,
		AttemptCount: attempt, FinalScore: validation.Score,
	}, ""
}

// runCriticStream invokes the LLM critic and emits a harness_critic_feedback event to outCh.
//
// Expected:
//   - ctx is a valid context.
//   - planText is the raw plan text.
//   - validation is the validator result to pass to the critic.
//   - outCh is the channel for emitting observability events.
//
// Returns:
//   - A feedback string for retry if the critic rejects the plan, or empty string if critic passes or is unconfigured.
//
// Side effects:
//   - Sends a chat request to the critic provider when configured.
//   - Emits harness_critic_feedback to outCh on successful critic review.
func (h *Harness) runCriticStream(
	ctx context.Context, planText string, validation *ValidationResult, outCh chan<- provider.StreamChunk,
) string {
	if h.critic == nil || h.criticProvider == nil {
		return ""
	}

	parsedPlan, parseErr := parseFile(planText)
	if parseErr != nil {
		parsedPlan = nil
	}

	criticResult, err := h.critic.Review(ctx, parsedPlan, planText, validation, h.criticProvider)
	if err != nil {
		validation.Warnings = append(validation.Warnings, "critic error: "+err.Error())
		return ""
	}

	emitCriticFeedback(ctx, outCh, criticResult)

	if criticResult.Verdict == VerdictFail {
		return buildCriticFeedback(criticResult)
	}

	return ""
}

// applyVoter runs the consistency voter if configured and score is below threshold.
//
// Expected:
//   - ctx is a valid context.
//   - streamer provides streaming access to the LLM.
//   - agentID identifies the planner agent.
//   - message is the original planning prompt.
//   - result is a non-nil EvaluationResult from successful validation.
//
// Returns:
//   - The original result if voter is nil or not triggered, otherwise result with updated plan and score.
//
// Side effects:
//   - Spawns goroutines to generate plan variants when voter triggers.
func (h *Harness) applyVoter(
	ctx context.Context, streamer Streamer, agentID string, message string, result *EvaluationResult,
) *EvaluationResult {
	if h.voter == nil {
		return result
	}
	voteResult, err := h.voter.Vote(ctx, streamer, VoteRequest{
		AgentID:      agentID,
		Message:      message,
		InitialPlan:  result.PlanText,
		InitialScore: result.FinalScore,
	})
	if err != nil || !voteResult.WasTriggered {
		return result
	}
	result.PlanText = voteResult.BestPlan
	result.FinalScore = voteResult.BestScore
	return result
}

// emitPlanArtifact sends a plan_artifact event to outCh with the approved plan content.
//
// Expected:
//   - ctx is a valid context.
//   - outCh is the channel for emitting observability events.
//   - result is a non-nil EvaluationResult with a valid plan.
//
// Side effects:
//   - Sends a plan_artifact StreamChunk to outCh.
func emitPlanArtifact(ctx context.Context, outCh chan<- provider.StreamChunk, result *EvaluationResult) {
	trySend(ctx, outCh, provider.StreamChunk{
		EventType: "plan_artifact",
		Content:   result.PlanText,
	})
}

// emitCriticFeedback sends a harness_critic_feedback event to outCh with the critic verdict.
//
// Expected:
//   - ctx is a valid context.
//   - outCh is the channel for emitting observability events.
//   - result is a non-nil CriticResult.
//
// Side effects:
//   - Sends a harness_critic_feedback StreamChunk to outCh.
func emitCriticFeedback(ctx context.Context, outCh chan<- provider.StreamChunk, result *CriticResult) {
	issuesJSON, err := json.Marshal(result.Issues)
	if err != nil {
		issuesJSON = []byte("[]")
	}
	payload := fmt.Sprintf(
		`{"verdict":%q,"confidence":%g,"issues":%s}`,
		result.Verdict, result.Confidence, issuesJSON,
	)
	trySend(ctx, outCh, provider.StreamChunk{
		EventType: "harness_critic_feedback",
		Content:   payload,
	})
	trySend(ctx, outCh, provider.StreamChunk{
		EventType: "review_verdict",
		Content:   payload,
	})
}

// emitHarnessComplete sends a harness_complete event to outCh with the evaluation result summary.
//
// Expected:
//   - ctx is a valid context.
//   - outCh is the channel for emitting observability events.
//   - result is a non-nil EvaluationResult.
//
// Side effects:
//   - Sends a harness_complete StreamChunk to outCh.
func emitHarnessComplete(ctx context.Context, outCh chan<- provider.StreamChunk, result *EvaluationResult) {
	payload, err := json.Marshal(map[string]any{
		"valid":        result.ValidationResult != nil && result.ValidationResult.Valid,
		"score":        result.FinalScore,
		"attemptCount": result.AttemptCount,
		"errors":       validationErrors(result.ValidationResult),
		"warnings":     validationWarnings(result.ValidationResult),
	})
	if err != nil {
		payload = []byte("{}")
	}
	trySend(ctx, outCh, provider.StreamChunk{
		EventType: "harness_complete",
		Content:   string(payload),
	})
}

// validationErrors returns the error list from a ValidationResult, or nil if the result is nil.
//
// Expected:
//   - v may be nil; returns nil in that case.
//
// Returns:
//   - A slice of error message strings, or nil if v is nil.
//
// Side effects:
//   - None.
func validationErrors(v *ValidationResult) []string {
	if v != nil {
		return v.Errors
	}
	return nil
}

// validationWarnings returns the warning list from a ValidationResult, or nil if the result is nil.
//
// Expected:
//   - v may be nil; returns nil in that case.
//
// Returns:
//   - A slice of warning message strings, or nil if v is nil.
//
// Side effects:
//   - None.
func validationWarnings(v *ValidationResult) []string {
	if v != nil {
		return v.Warnings
	}
	return nil
}

// phaseString returns a human-readable string for a PlanPhase value.
//
// Expected:
//   - phase is a valid hook.PlanPhase constant.
//
// Returns:
//   - A lowercase string representation of the phase, or "unknown" for unrecognised values.
//
// Side effects:
//   - None.
func phaseString(phase hook.PlanPhase) string {
	switch phase {
	case hook.PhaseInterview:
		return "interview"
	case hook.PhaseGeneration:
		return "generation"
	default:
		return "unknown"
	}
}

// buildCriticFeedback constructs a retry feedback string from critic issues.
//
// Expected:
//   - result is a non-nil CriticResult with Verdict == VerdictFail.
//
// Returns:
//   - A formatted feedback string listing critic issues and suggestions.
//
// Side effects:
//   - None.
func buildCriticFeedback(result *CriticResult) string {
	var b strings.Builder
	b.WriteString("The LLM critic rejected your plan. Issues:\n")
	for _, issue := range result.Issues {
		b.WriteString("- ")
		b.WriteString(issue)
		b.WriteString("\n")
	}
	if len(result.Suggestions) > 0 {
		b.WriteString("Suggestions:\n")
		for _, suggestion := range result.Suggestions {
			b.WriteString("- ")
			b.WriteString(suggestion)
			b.WriteString("\n")
		}
	}
	b.WriteString("Fix these specific issues and regenerate the complete plan.")
	return b.String()
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
