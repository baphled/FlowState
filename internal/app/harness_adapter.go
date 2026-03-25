package app

import (
	"context"
	"os"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
)

// harnessAdapter wraps a PlanHarness to satisfy streaming.PlanEvaluator.
//
// This adapter bridges the plan.Streamer and streaming.Streamer interfaces
// which are structurally identical but defined in separate packages to
// avoid import cycles.
type harnessAdapter struct {
	harness *plan.PlanHarness
}

// Evaluate delegates to the wrapped PlanHarness, converting the streaming.Streamer to plan.Streamer.
//
// Expected:
//   - ctx is a valid context for the evaluation.
//   - streamer satisfies both streaming.Streamer and plan.Streamer (same method set).
//   - agentID identifies the agent being evaluated.
//   - message is the user's input text.
//
// Returns:
//   - The evaluation result from the plan harness.
//   - An error if evaluation fails.
//
// Side effects:
//   - Delegates to PlanHarness.Evaluate which may stream multiple attempts.
func (a *harnessAdapter) Evaluate(
	ctx context.Context,
	streamer streaming.Streamer,
	agentID string,
	message string,
) (*plan.EvaluationResult, error) {
	return a.harness.Evaluate(ctx, streamer, agentID, message)
}

// StreamEvaluate delegates to the wrapped PlanHarness, forwarding response chunks live.
//
// Expected:
//   - ctx is a valid context for the evaluation.
//   - streamer satisfies both streaming.Streamer and plan.Streamer (same method set).
//   - agentID identifies the agent being evaluated.
//   - message is the user's input text.
//
// Returns:
//   - A read-only channel of StreamChunk values forwarded live from the LLM.
//   - An error if evaluation fails to start.
//
// Side effects:
//   - Delegates to PlanHarness.StreamEvaluate which spawns a goroutine for streaming and validation.
func (a *harnessAdapter) StreamEvaluate(
	ctx context.Context,
	streamer streaming.Streamer,
	agentID string,
	message string,
) (<-chan provider.StreamChunk, error) {
	return a.harness.StreamEvaluate(ctx, streamer, agentID, message)
}

// createHarnessStreamer builds a HarnessStreamer wrapping the engine with plan harness validation.
//
// Expected:
//   - inner is a non-nil streaming.Streamer (typically the engine).
//   - registry is a non-nil agent.Registry for manifest lookup.
//
// Returns:
//   - A configured HarnessStreamer that routes harness-enabled agents through the evaluator.
//
// Side effects:
//   - Calls os.Getwd to determine the project root directory.
func createHarnessStreamer(inner streaming.Streamer, registry *agent.Registry) *streaming.HarnessStreamer {
	projectRoot, err := os.Getwd()
	if err != nil {
		projectRoot = "."
	}
	harness := plan.NewPlanHarness(projectRoot)
	return streaming.NewHarnessStreamer(inner, &harnessAdapter{harness: harness}, registry)
}
