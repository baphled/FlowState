package app

import (
	"context"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/execution"
	internharness "github.com/baphled/flowstate/internal/harness"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
)

// executionAdapter wraps an execution.Loop to satisfy streaming.ExecutionEvaluator.
//
// It bridges the streaming.Streamer and harness.Streamer interfaces which are
// structurally identical but defined in separate packages to avoid import cycles.
type executionAdapter struct {
	loop *execution.Loop
}

// Evaluate delegates synchronous evaluation to the underlying execution loop.
//
// Expected:
//   - ctx is a valid context for the evaluation.
//   - streamer satisfies both streaming.Streamer and harness.Streamer.
//   - agentID identifies the agent being evaluated.
//   - message is the user's input text.
//
// Returns:
//   - The evaluation result from the execution loop.
//   - An error if evaluation fails.
//
// Side effects:
//   - None beyond those of the underlying loop.
func (a *executionAdapter) Evaluate(
	ctx context.Context,
	streamer streaming.Streamer,
	agentID string,
	message string,
) (*internharness.EvaluationResult, error) {
	return a.loop.Evaluate(ctx, streamer, agentID, message)
}

// StreamEvaluate delegates streaming evaluation to the underlying execution loop.
//
// Expected:
//   - ctx is a valid context for the evaluation.
//   - streamer satisfies both streaming.Streamer and harness.Streamer.
//   - agentID identifies the agent being evaluated.
//   - message is the user's input text.
//
// Returns:
//   - A read-only channel of StreamChunk values forwarded live from the loop.
//   - An error if evaluation fails to start.
//
// Side effects:
//   - Spawns a goroutine inside the loop for streaming.
func (a *executionAdapter) StreamEvaluate(
	ctx context.Context,
	streamer streaming.Streamer,
	agentID string,
	message string,
) (<-chan provider.StreamChunk, error) {
	return a.loop.StreamEvaluate(ctx, streamer, agentID, message)
}

// createExecutionEvaluator constructs an executionAdapter from the given configuration.
//
// Expected:
//   - cfg is a config.HarnessConfig with optional MaxRetries fields.
//   - registry is a non-nil agent.Registry (reserved for future per-agent configuration).
//   - p is a provider.Provider (reserved for future critic integration).
//
// Returns:
//   - A streaming.ExecutionEvaluator backed by an execution.Loop.
//
// Side effects:
//   - None.
func createExecutionEvaluator(cfg config.HarnessConfig, _ *agent.Registry, _ provider.Provider) streaming.ExecutionEvaluator {
	var opts []execution.Option
	if cfg.MaxRetries > 0 {
		opts = append(opts, execution.WithMaxRetries(cfg.MaxRetries))
	}
	return &executionAdapter{loop: execution.NewLoop(opts...)}
}
