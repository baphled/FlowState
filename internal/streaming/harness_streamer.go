package streaming

import (
	"context"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/provider"
)

// PlanEvaluator abstracts the plan harness evaluation for testability.
type PlanEvaluator interface {
	// Evaluate runs validation on the streamed plan output and returns the evaluation result.
	Evaluate(ctx context.Context, streamer Streamer, agentID string, message string) (*plan.EvaluationResult, error)
}

// AgentRegistry abstracts agent manifest lookup for testability.
type AgentRegistry interface {
	// Get retrieves an agent manifest by ID, returning false if not found.
	Get(id string) (*agent.Manifest, bool)
}

// HarnessStreamer decorates a Streamer with plan harness validation for harness-enabled agents.
type HarnessStreamer struct {
	inner    Streamer
	harness  PlanEvaluator
	registry AgentRegistry
}

// NewHarnessStreamer creates a HarnessStreamer that routes harness-enabled agents through the evaluator.
//
// Expected:
//   - inner is a non-nil Streamer for passthrough and harness evaluation.
//   - harness is a non-nil PlanEvaluator for plan validation.
//   - registry is a non-nil AgentRegistry for manifest lookup.
//
// Returns:
//   - A configured HarnessStreamer ready to route requests.
//
// Side effects:
//   - None.
func NewHarnessStreamer(inner Streamer, harness PlanEvaluator, registry AgentRegistry) *HarnessStreamer {
	return &HarnessStreamer{
		inner:    inner,
		harness:  harness,
		registry: registry,
	}
}

// Stream routes the request through the harness for harness-enabled agents, or passes through to the inner streamer.
//
// Expected:
//   - ctx is a valid context for the streaming operation.
//   - agentID identifies the agent to stream from.
//   - message is the user's input text.
//
// Returns:
//   - A channel of StreamChunk values containing the response.
//   - An error if the harness evaluation fails or the inner stream fails.
//
// Side effects:
//   - Spawns a goroutine to deliver harness results when HarnessEnabled is true.
func (s *HarnessStreamer) Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error) {
	manifest, ok := s.registry.Get(agentID)
	if ok && manifest.HarnessEnabled {
		result, err := s.harness.Evaluate(ctx, s.inner, agentID, message)
		if err != nil {
			return nil, err
		}
		return planResultToChannel(result), nil
	}
	return s.inner.Stream(ctx, agentID, message)
}

const resultChannelBuffer = 2

// planResultToChannel converts an EvaluationResult into a buffered stream channel.
//
// Expected:
//   - result is a non-nil EvaluationResult with a PlanText field.
//
// Returns:
//   - A receive-only channel that emits the plan text followed by a done sentinel.
//
// Side effects:
//   - Spawns a goroutine that sends two chunks then closes the channel.
func planResultToChannel(result *plan.EvaluationResult) <-chan provider.StreamChunk {
	ch := make(chan provider.StreamChunk, resultChannelBuffer)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: result.PlanText}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch
}
