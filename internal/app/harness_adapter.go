package app

import (
	"context"
	"os"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/plan/harness"
	"github.com/baphled/flowstate/internal/plan/validation"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
)

// harnessAdapter wraps a Harness to satisfy streaming.PlanEvaluator.
//
// This adapter bridges the harness.Streamer and streaming.Streamer interfaces
// which are structurally identical but defined in separate packages to
// avoid import cycles.
type harnessAdapter struct {
	h *harness.Harness
}

// Evaluate delegates to the wrapped Harness, converting the streaming.Streamer to harness.Streamer.
//
// Expected:
//   - ctx is a valid context for the evaluation.
//   - streamer satisfies both streaming.Streamer and harness.Streamer (same method set).
//   - agentID identifies the agent being evaluated.
//   - message is the user's input text.
//
// Returns:
//   - The evaluation result from the plan harness.
//   - An error if evaluation fails.
//
// Side effects:
//   - Delegates to Harness.Evaluate which may stream multiple attempts.
func (a *harnessAdapter) Evaluate(
	ctx context.Context,
	streamer streaming.Streamer,
	agentID string,
	message string,
) (*plan.EvaluationResult, error) {
	return a.h.Evaluate(ctx, streamer, agentID, message)
}

// StreamEvaluate delegates to the wrapped Harness, forwarding response chunks live.
//
// Expected:
//   - ctx is a valid context for the evaluation.
//   - streamer satisfies both streaming.Streamer and harness.Streamer (same method set).
//   - agentID identifies the agent being evaluated.
//   - message is the user's input text.
//
// Returns:
//   - A read-only channel of StreamChunk values forwarded live from the LLM.
//   - An error if evaluation fails to start.
//
// Side effects:
//   - Delegates to Harness.StreamEvaluate which spawns a goroutine for streaming and validation.
func (a *harnessAdapter) StreamEvaluate(
	ctx context.Context,
	streamer streaming.Streamer,
	agentID string,
	message string,
) (<-chan provider.StreamChunk, error) {
	return a.h.StreamEvaluate(ctx, streamer, agentID, message)
}

// createHarnessStreamer builds a HarnessStreamer wrapping the engine with plan
// harness validation.
//
// Expected:
//   - inner is a non-nil streaming.Streamer (typically the engine).
//   - registry is a non-nil agent.Registry for manifest lookup.
//   - cfg is a config.HarnessConfig specifying whether the critic is enabled.
//   - p is a provider.Provider for the LLM critic (required when CriticEnabled is true).
//
// Returns:
//   - A configured HarnessStreamer that routes harness-enabled agents through the evaluator.
//   - When CriticEnabled is true in cfg and p is non-nil, the harness includes an LLM critic.
//
// Side effects:
//   - Calls os.Getwd to determine the project root directory if not set in cfg.
func createHarnessStreamer(
	inner streaming.Streamer,
	registry *agent.Registry,
	cfg config.HarnessConfig,
	p provider.Provider,
) *streaming.HarnessStreamer {
	projectRoot := cfg.ProjectRoot
	if projectRoot == "" {
		var err error
		projectRoot, err = os.Getwd()
		if err != nil {
			projectRoot = "."
		}
	}

	var opts []harness.HarnessOption
	if cfg.MaxRetries > 0 {
		opts = append(opts, harness.WithMaxRetries(cfg.MaxRetries))
	}

	if cfg.CriticEnabled && p != nil {
		critic, err := harness.NewLLMCritic(true, "claude-sonnet-4-6")
		if err == nil {
			opts = append(opts, harness.WithCritic(critic, p))
		}
	}

	if cfg.VotingEnabled {
		voterCfg := harness.VoterConfig{Enabled: true, Variants: 3, Threshold: 0.8}
		voter := harness.NewConsistencyVoter(voterCfg, projectRoot)
		opts = append(opts, harness.WithVoter(voter))
	}

	opts = append(validation.DefaultValidators(), opts...)
	h := harness.NewHarness(projectRoot, opts...)
	return streaming.NewHarnessStreamer(inner, &harnessAdapter{h: h}, registry)
}

// CreateHarnessStreamerForTest is a test helper that exposes createHarnessStreamer for
// testing.
//
// Expected:
//   - inner is a streaming.Streamer (may be nil for testing).
//   - registry is a non-nil agent.Registry for manifest lookup.
//   - cfg is a config.HarnessConfig specifying whether the critic is enabled.
//   - p is a provider.Provider for the LLM critic (may be nil when CriticEnabled is false).
//
// Returns:
//   - A configured HarnessStreamer for testing.
//
// Side effects:
//   - None.
func CreateHarnessStreamerForTest(
	inner streaming.Streamer,
	registry *agent.Registry,
	cfg config.HarnessConfig,
	p provider.Provider,
) *streaming.HarnessStreamer {
	return createHarnessStreamer(inner, registry, cfg, p)
}
