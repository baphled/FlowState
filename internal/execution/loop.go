package execution

import (
	"context"
	"log/slog"
	"strings"

	"github.com/baphled/flowstate/internal/harness"
	"github.com/baphled/flowstate/internal/provider"
)

// criticProvider is the subset of provider.Provider required by harness.Critic.Review.
type criticProvider interface {
	provider.Provider
}

const (
	defaultMaxRetries = 1
	streamChannelBuf  = 16
)

// loopResult holds the outcome of a single runLoop call.
type loopResult struct {
	result     *harness.EvaluationResult
	stopReason StopReason
	attempts   int
}

// Loop implements [harness.Evaluator] for general-purpose (non-planning) agent evaluation.
//
// It runs a validate-critique cycle, retrying according to the configured [RetryStrategy]
// until the output passes validation or the maximum retry count is exhausted.
type Loop struct {
	validator      harness.Validator
	critic         harness.Critic
	criticProvider criticProvider
	maxRetries     int
	retryStrategy  RetryStrategy
	observer       OutcomeObserver
}

// compile-time assertion: Loop must satisfy harness.Evaluator.
var _ harness.Evaluator = (*Loop)(nil)

// NewLoop constructs a Loop with optional configuration.
//
// Expected:
//   - opts are zero or more Option functions that configure the loop.
//
// Returns:
//   - A configured *Loop ready for use.
//
// Side effects:
//   - None.
func NewLoop(opts ...Option) *Loop {
	l := &Loop{
		maxRetries: defaultMaxRetries,
	}
	for _, o := range opts {
		o(l)
	}
	if l.retryStrategy == nil {
		l.retryStrategy = DefaultRetryStrategy{MaxRetries: l.maxRetries}
	}
	return l
}

// Evaluate runs the validate-critique loop synchronously and returns the final result.
//
// Expected:
//   - ctx is a valid context; cancellation stops the loop immediately.
//   - streamer generates LLM output for the given agentID and message.
//   - agentID identifies the target agent.
//   - message is the user prompt.
//
// Returns:
//   - *harness.EvaluationResult with the best output and final score.
//   - An error if streaming fails on the first attempt.
//
// Side effects:
//   - Calls the optional OutcomeObserver once before returning.
func (l *Loop) Evaluate(ctx context.Context, streamer harness.Streamer, agentID string, message string) (*harness.EvaluationResult, error) {
	r, err := l.runLoop(ctx, streamer, agentID, message)
	if err != nil {
		return nil, err
	}
	l.notifyObserver(r.result, r.stopReason, r.attempts)
	return r.result, nil
}

// StreamEvaluate runs the validate-critique loop and streams output chunks as they arrive.
//
// Expected:
//   - ctx is a valid context; cancellation closes the returned channel.
//   - streamer generates LLM output for the given agentID and message.
//   - agentID identifies the target agent.
//   - message is the user prompt.
//
// Returns:
//   - A receive-only channel that emits StreamChunk values, ending with a Done chunk.
//   - An error if the loop cannot be started.
//
// Side effects:
//   - Spawns a goroutine that drives the evaluation loop and sends chunks.
//   - Calls the optional OutcomeObserver once the loop terminates.
func (l *Loop) StreamEvaluate(
	ctx context.Context,
	streamer harness.Streamer,
	agentID string,
	message string,
) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, streamChannelBuf)
	go func() {
		defer close(ch)
		r, err := l.runLoop(ctx, streamer, agentID, message)
		if err != nil {
			slog.Warn("execution loop stream error", "agent", agentID, "error", err)
			ch <- provider.StreamChunk{Done: true}
			return
		}
		ch <- provider.StreamChunk{Content: r.result.Output}
		ch <- provider.StreamChunk{Done: true}
		l.notifyObserver(r.result, r.stopReason, r.attempts)
	}()
	return ch, nil
}

func (l *Loop) runLoop(ctx context.Context, streamer harness.Streamer, agentID, message string) (loopResult, error) {
	var (
		result   *harness.EvaluationResult
		attempts int
	)
	for {
		select {
		case <-ctx.Done():
			if result == nil {
				result = &harness.EvaluationResult{}
			}
			return loopResult{result: result, stopReason: StopReasonCancelled, attempts: attempts}, nil
		default:
		}

		attempts++
		output, err := l.collectOutput(ctx, streamer, agentID, message)
		if err != nil {
			if attempts == 1 {
				return loopResult{}, err
			}
			return loopResult{result: result, stopReason: StopReasonMaxRetries, attempts: attempts}, nil
		}

		result = &harness.EvaluationResult{
			Output:       output,
			AttemptCount: attempts,
		}
		result = l.validate(result)
		result = l.critique(ctx, result)

		if result.ValidationResult != nil && result.ValidationResult.Valid {
			result.FinalScore = 1.0
			return loopResult{result: result, stopReason: StopReasonPassed, attempts: attempts}, nil
		}
		if !l.retryStrategy.ShouldRetry(attempts, result) {
			return loopResult{result: result, stopReason: StopReasonMaxRetries, attempts: attempts}, nil
		}
	}
}

func (l *Loop) collectOutput(ctx context.Context, streamer harness.Streamer, agentID, message string) (string, error) {
	ch, err := streamer.Stream(ctx, agentID, message)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for chunk := range ch {
		if chunk.Done {
			break
		}
		sb.WriteString(chunk.Content)
	}
	return sb.String(), nil
}

func (l *Loop) validate(result *harness.EvaluationResult) *harness.EvaluationResult {
	if l.validator == nil {
		result.ValidationResult = &harness.ValidationResult{Valid: true, Score: 1.0}
		result.FinalScore = 1.0
		return result
	}
	vr, err := l.validator.Validate(result.Output)
	if err != nil {
		slog.Warn("execution loop validator error", "error", err)
		result.ValidationResult = &harness.ValidationResult{Valid: false}
		return result
	}
	result.ValidationResult = vr
	result.FinalScore = vr.Score
	return result
}

func (l *Loop) critique(ctx context.Context, result *harness.EvaluationResult) *harness.EvaluationResult {
	if l.critic == nil || l.criticProvider == nil {
		return result
	}
	cr, err := l.critic.Review(ctx, result.Output, result.ValidationResult, l.criticProvider)
	if err != nil {
		slog.Warn("execution loop critic error", "error", err)
		return result
	}
	if cr.Verdict == harness.VerdictFail && result.FinalScore > 0 {
		result.FinalScore *= 0.5
	}
	return result
}

func (l *Loop) notifyObserver(result *harness.EvaluationResult, reason StopReason, attempts int) {
	if l.observer == nil {
		return
	}
	l.observer.OnOutcome(Outcome{
		Result:     result,
		StopReason: reason,
		Attempts:   attempts,
	})
}
