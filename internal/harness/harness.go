package harness

import (
	"context"

	"github.com/baphled/flowstate/internal/provider"
)

// Streamer generates LLM responses for a given agent and message.
type Streamer interface {
	// Stream returns a channel of response chunks for the given agent and message.
	Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error)
}

// ValidationResult holds the outcome of validating agent output.
type ValidationResult struct {
	Valid    bool
	Errors   []string
	Warnings []string
	Score    float64
}

// Validator checks agent output against configurable rules.
//
// Implementations compose domain-specific checks behind this uniform signature.
// For example, the plan ValidatorChain normalises three heterogeneous validators
// (schema, assertion, reference) into this single interface.
type Validator interface {
	// Validate checks the given text and returns a result describing validity, errors, and score.
	Validate(text string) (*ValidationResult, error)
}

// CriticVerdict represents the outcome of a critic review.
type CriticVerdict string

const (
	// VerdictPass indicates output meets quality criteria.
	VerdictPass CriticVerdict = "PASS"
	// VerdictFail indicates output does not meet quality criteria.
	VerdictFail CriticVerdict = "FAIL"
	// VerdictDisabled indicates critic evaluation was skipped.
	VerdictDisabled CriticVerdict = "DISABLED"
)

// CriticResult holds the outcome of an LLM-based quality review.
type CriticResult struct {
	Verdict     CriticVerdict
	Issues      []string
	Suggestions []string
	Confidence  float64
}

// Critic performs LLM-based quality assessment of agent output.
//
// The provider is passed per-call rather than at construction to allow
// callers to control which LLM evaluates the output.
type Critic interface {
	// Review assesses the quality of text given prior validation results and returns a verdict.
	Review(ctx context.Context, text string, validation *ValidationResult, llm provider.Provider) (*CriticResult, error)
}

// VoteResult holds the outcome of multi-variant selection.
type VoteResult struct {
	WasTriggered      bool
	VariantsGenerated int
	BestOutput        string
	BestScore         float64
}

// Voter generates output variants and selects the best one.
//
// When the initial output scores below a threshold, the voter generates
// alternative variants via the streamer and picks the highest-scoring one.
type Voter interface {
	// Vote generates variants when the initial score is below threshold and returns the best output.
	Vote(
		ctx context.Context,
		streamer Streamer,
		agentID string,
		message string,
		initialOutput string,
		initialScore float64,
	) (*VoteResult, error)
}

// EvaluationResult holds the final outcome of harness evaluation.
type EvaluationResult struct {
	Output           string
	ValidationResult *ValidationResult
	AttemptCount     int
	FinalScore       float64
}

// Evaluator orchestrates the validate-critique-vote loop for agent output.
//
// Evaluate runs synchronously and returns the final result.
// StreamEvaluate runs the same loop but streams chunks as they arrive,
// emitting harness events (attempt_start, retry, critic_feedback, complete).
type Evaluator interface {
	// Evaluate runs the validate-critique-vote loop synchronously and returns the final result.
	Evaluate(ctx context.Context, streamer Streamer, agentID string, message string) (*EvaluationResult, error)
	// StreamEvaluate runs the validate-critique-vote loop and streams output chunks as they arrive.
	StreamEvaluate(ctx context.Context, streamer Streamer, agentID string, message string) (<-chan provider.StreamChunk, error)
}
