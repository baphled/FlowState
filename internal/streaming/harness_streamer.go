package streaming

import (
	"context"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/harness"
	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/provider"
)

// PlanEvaluator abstracts the plan harness evaluation for testability.
//
// Evaluate runs validation synchronously and returns the completed result.
// StreamEvaluate forwards chunks in real-time while running the same validation loop.
type PlanEvaluator interface {
	// Evaluate runs validation on the streamed plan output and returns the evaluation result.
	Evaluate(ctx context.Context, streamer Streamer, agentID string, message string) (*plan.EvaluationResult, error)
	// StreamEvaluate runs validation while forwarding response chunks live to the caller.
	StreamEvaluate(ctx context.Context, streamer Streamer, agentID string, message string) (<-chan provider.StreamChunk, error)
}

// ExecutionEvaluator abstracts the execution harness evaluation for testability.
//
// Unlike PlanEvaluator, it returns the shared harness.EvaluationResult rather
// than a plan-specific result, enabling mode-based routing without conflating
// the two result types.
type ExecutionEvaluator interface {
	// Evaluate runs the validate-critique loop synchronously and returns the final result.
	Evaluate(ctx context.Context, streamer Streamer, agentID string, message string) (*harness.EvaluationResult, error)
	// StreamEvaluate runs the validate-critique loop and streams output chunks as they arrive.
	StreamEvaluate(ctx context.Context, streamer Streamer, agentID string, message string) (<-chan provider.StreamChunk, error)
}

// AgentRegistry abstracts agent manifest lookup for testability.
type AgentRegistry interface {
	// Get retrieves an agent manifest by ID, returning false if not found.
	Get(id string) (*agent.Manifest, bool)
}

// HarnessStreamer decorates a Streamer with harness validation for enabled agents.
type HarnessStreamer struct {
	inner    Streamer
	harness  PlanEvaluator
	execEval ExecutionEvaluator
	registry AgentRegistry
}

// NewHarnessStreamer creates a HarnessStreamer that routes harness-enabled agents through the evaluator.
//
// Expected:
//   - inner is a non-nil Streamer for passthrough and harness evaluation.
//   - planEval is a non-nil PlanEvaluator for plan validation.
//   - registry is a non-nil AgentRegistry for manifest lookup.
//
// Returns:
//   - A configured HarnessStreamer ready to route requests.
//
// Side effects:
//   - None.
func NewHarnessStreamer(inner Streamer, planEval PlanEvaluator, registry AgentRegistry) *HarnessStreamer {
	return &HarnessStreamer{
		inner:    inner,
		harness:  planEval,
		registry: registry,
	}
}

// WithExecutionEvaluator sets the execution evaluator used for agents whose mode is "execution".
//
// Expected:
//   - eval is a non-nil ExecutionEvaluator.
//
// Returns:
//   - The HarnessStreamer for method chaining.
//
// Side effects:
//   - Replaces the stored execution evaluator.
func (s *HarnessStreamer) WithExecutionEvaluator(eval ExecutionEvaluator) *HarnessStreamer {
	s.execEval = eval
	return s
}

// Stream routes the request through the appropriate harness for enabled agents, or passes through to the inner streamer.
//
// Expected:
//   - ctx is a valid context for the streaming operation.
//   - agentID identifies the agent to stream from.
//   - message is the user's input text.
//
// Returns:
//   - A channel of StreamChunk values containing the response.
//   - An error if the harness streaming fails or the inner stream fails.
//
// Side effects:
//   - Delegates to the execution evaluator for agents with mode "execution".
//   - Delegates to StreamEvaluate for plan-harness-enabled agents.
func (s *HarnessStreamer) Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error) {
	manifest, ok := s.registry.Get(agentID)
	if ok && manifest.Mode == "execution" && s.execEval != nil {
		return s.execEval.StreamEvaluate(ctx, s.inner, agentID, message)
	}
	if ok && manifest.HarnessEnabled {
		return s.harness.StreamEvaluate(ctx, s.inner, agentID, message)
	}
	return s.inner.Stream(ctx, agentID, message)
}

const resultChannelBuffer = 2

// PlanResultToChannel converts an EvaluationResult into a buffered stream channel.
//
// This is primarily useful for non-streaming callers and test mocks that wrap
// a synchronous Evaluate call with channel-based delivery.
//
// Expected:
//   - result is a non-nil EvaluationResult with a PlanText field.
//
// Returns:
//   - A receive-only channel that emits the plan text followed by a done sentinel.
//
// Side effects:
//   - Spawns a goroutine that sends two chunks then closes the channel.
func PlanResultToChannel(result *plan.EvaluationResult) <-chan provider.StreamChunk {
	ch := make(chan provider.StreamChunk, resultChannelBuffer)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: result.PlanText}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch
}
