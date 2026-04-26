package app

import (
	"context"
	"log/slog"
	"os"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/plan/harness"
	"github.com/baphled/flowstate/internal/plan/validation"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
)

// resolveCriticModel decides which chat model the LLM critic runs against.
//
// Precedence:
//  1. cfg.CriticModel — explicit override from harness configuration.
//  2. The provider's primary model resolved from cfg.Providers.<default>
//     — passed in as fallback so the critic uses the same model as the
//     agent under critique by default. Zero-config sane behaviour.
//
// Returns the empty string when neither is set; callers MUST treat that
// as "skip critic wiring" rather than passing an empty model id through
// to the LLM SDK.
func resolveCriticModel(criticOverride, providerFallback string) string {
	if criticOverride != "" {
		return criticOverride
	}
	return providerFallback
}

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
//   - defaultProviderModel is the chat model configured for the active default
//     provider. Used as the critic model fallback when cfg.CriticModel is empty.
//     May be empty when no provider model is configured.
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
	defaultProviderModel string,
) *streaming.HarnessStreamer {
	projectRoot := cfg.ProjectRoot
	if projectRoot == "" {
		var err error
		projectRoot, err = os.Getwd()
		if err != nil {
			projectRoot = "."
		}
	}

	var opts []harness.Option
	if cfg.MaxRetries > 0 {
		opts = append(opts, harness.WithMaxRetries(cfg.MaxRetries))
	}

	// Wire a critic instance whenever a provider is available — the
	// per-agent enabler below decides whether it actually fires for any
	// given evaluation. This means an agent whose manifest opts into
	// the critic gets it even when the global cfg.CriticEnabled is
	// false; conversely, the legacy global flag still works for callers
	// that want a blanket enable.
	if p != nil && (cfg.CriticEnabled || registryHasCriticEnabledAgent(registry)) {
		criticModel := resolveCriticModel(cfg.CriticModel, defaultProviderModel)
		if criticModel == "" {
			slog.Warn("harness: critic enabled but no model resolved; skipping critic wiring",
				"hint", "set harness.critic_model in config or ensure the default provider has a model configured")
		} else {
			critic, err := harness.NewLLMCritic(true, criticModel)
			if err == nil {
				opts = append(opts, harness.WithCritic(critic, p))
				opts = append(opts, harness.WithCriticEnabledFunc(
					newCriticEnabler(registry, cfg.CriticEnabled)))
			}
		}
	}

	if cfg.VotingEnabled {
		voterCfg := harness.VoterConfig{Enabled: true, Variants: 3, Threshold: 0.8}
		voter := harness.NewConsistencyVoter(voterCfg, projectRoot)
		opts = append(opts, harness.WithVoter(voter))
	}

	opts = append(validation.DefaultValidators(), opts...)
	h := harness.NewHarness(projectRoot, opts...)
	hs := streaming.NewHarnessStreamer(inner, &harnessAdapter{h: h}, registry)
	if cfg.Mode == "execution" || cfg.LearningEnabled {
		execEval := createExecutionEvaluator(cfg, registry, p)
		hs.WithExecutionEvaluator(execEval)
	}
	return hs
}

// registryHasCriticEnabledAgent reports whether any registered agent has
// opted into the LLM critic via its manifest's harness.critic_enabled
// override. Used so a critic instance is constructed even when the global
// config flag is off — agents with the per-manifest override get the
// critic without forcing it on the rest of the system.
func registryHasCriticEnabledAgent(registry *agent.Registry) bool {
	if registry == nil {
		return false
	}
	for _, m := range registry.List() {
		if m.Harness != nil && m.Harness.CriticEnabled {
			return true
		}
	}
	return false
}

// newCriticEnabler returns a per-agent predicate that decides whether the
// configured critic should fire. The agent's manifest override always wins;
// the global config flag is the fallback when the agent doesn't express an
// opinion. An empty agentID falls back to the global setting (defensive
// default for unit-test paths that bypass the streamer).
func newCriticEnabler(registry *agent.Registry, globalDefault bool) func(string) bool {
	return func(agentID string) bool {
		if registry != nil && agentID != "" {
			if m, ok := registry.Get(agentID); ok && m.Harness != nil {
				return m.Harness.CriticEnabled
			}
		}
		return globalDefault
	}
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
//
// Tests pass an empty defaultProviderModel; the critic is only wired when
// CriticEnabled is true AND a provider is supplied AND a model resolves,
// so the test-helper signature is stable for callers that don't exercise
// the critic path.
func CreateHarnessStreamerForTest(
	inner streaming.Streamer,
	registry *agent.Registry,
	cfg config.HarnessConfig,
	p provider.Provider,
) *streaming.HarnessStreamer {
	return createHarnessStreamer(inner, registry, cfg, p, "")
}
