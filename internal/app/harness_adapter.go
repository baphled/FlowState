package app

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/plan/harness"
	"github.com/baphled/flowstate/internal/plan/validation"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
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
	coordStore coordination.Store,
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
				opts = append(opts,
					harness.WithCritic(critic, p),
					harness.WithCriticEnabledFunc(
						newCriticEnabler(registry, cfg.CriticEnabled)),
				)
			}
		}
	}

	if cfg.VotingEnabled {
		voterCfg := harness.VoterConfig{Enabled: true, Variants: 3, Threshold: 0.8}
		voter := harness.NewConsistencyVoter(voterCfg, projectRoot)
		opts = append(opts, harness.WithVoter(voter))
	}

	// Wave fan-in barrier: when any registered agent declares waves on
	// its manifest's harness.waves field, wire a coord-store-backed
	// validator. The harness then re-prompts the orchestrator before
	// it can yield to the user with any wave's outputs missing.
	if coordStore != nil {
		if stages := collectAgentWaves(registry); len(stages) > 0 {
			validator := &coordWaveValidator{store: coordStore}
			opts = append(opts, harness.WithWaves(stages, validator))
			// Bump the retry budget: stage-walking the planner through
			// (evidence → analysis → writing → review) needs 4+
			// re-prompt cycles in the worst case. The default of 1
			// would exhaust on the first wave-incomplete check.
			if cfg.MaxRetries == 0 {
				opts = append(opts, harness.WithMaxRetries(8))
			}
		}
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
//
// Expected:
//   - registry is the agent.Registry to scan; nil yields false.
//
// Returns:
//   - True when at least one manifest has Harness.CriticEnabled=true.
//
// Side effects:
//   - None.
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
// configured critic should fire. The agent's manifest override takes
// precedence; the global config flag is the fallback when the agent
// doesn't express an opinion. An empty agentID falls back to the global
// setting (defensive default for unit-test paths that skip the streamer).
//
// Expected:
//   - registry is the manifest source; may be nil, in which case the
//     predicate defers to globalDefault.
//   - globalDefault is the fallback value when no manifest override is found.
//
// Returns:
//   - A predicate that, given an agentID, returns whether the critic should run.
//
// Side effects:
//   - None at call time; the returned predicate performs only registry lookups.
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

// coordWaveValidator is the harness.WaveValidator implementation backed
// by FlowState's coordination_store. It resolves expected keys against
// the active chain id (extracted from ctx via session.IDKey) and falls
// back to a suffix-glob scan of the store when no chain id is available.
//
// Two-tier lookup:
//
//  1. If ctx carries session.IDKey, treat that as the chain prefix.
//     For each expected key in the wave, substitute "{chainID}" and
//     check store.Get on the resolved key.
//
//  2. If no chain id is in ctx, strip the "{chainID}/" template prefix
//     from each expected key and walk store.List(""), looking for
//     ANY key that ends with /<suffix>. Reports missing as
//     "<suffix> (any chain)" so the planner can react.
//
// The fallback covers the bootstrap case where the planner hasn't
// yet allocated a chain id (or runs outside a session). It can over-
// approve when multiple stale chains exist, but that's a tolerable
// MVP behaviour — the planner's prompt discipline catches the
// remaining cases.
type coordWaveValidator struct {
	store coordination.Store
}

// MissingForChain implements harness.WaveValidator. See
// coordWaveValidator's doc comment for the resolution rules.
func (v *coordWaveValidator) MissingForChain(
	ctx context.Context, _ string, wave harness.WaveStage,
) ([]string, error) {
	if v == nil || v.store == nil {
		return nil, nil
	}
	chainID, _ := ctx.Value(session.IDKey{}).(string)

	var missing []string
	for _, key := range wave.ExpectedKeys {
		if strings.Contains(key, "{chainID}") {
			if chainID != "" {
				resolved := strings.ReplaceAll(key, "{chainID}", chainID)
				if _, err := v.store.Get(resolved); err != nil {
					missing = append(missing, resolved)
				}
				continue
			}
			suffix := strings.TrimPrefix(key, "{chainID}/")
			found, err := v.suffixPresent(suffix)
			if err != nil {
				return nil, err
			}
			if !found {
				missing = append(missing, suffix+" (any chain)")
			}
			continue
		}
		if _, err := v.store.Get(key); err != nil {
			missing = append(missing, key)
		}
	}
	return missing, nil
}

func (v *coordWaveValidator) suffixPresent(suffix string) (bool, error) {
	keys, err := v.store.List("")
	if err != nil {
		return false, err
	}
	target := "/" + suffix
	for _, k := range keys {
		if strings.HasSuffix(k, target) {
			return true, nil
		}
	}
	return false, nil
}

// collectAgentWaves walks the registry and returns the union of all
// agents' wave stages. The harness checks every wave on every turn,
// but stages declared on the orchestrator agent (typically planner)
// are the load-bearing ones — others are no-ops on non-planner runs
// because the planner-allocated chain id won't have their keys.
//
// Returns nil when no agent declares waves; the harness adapter then
// skips wiring the validator entirely.
func collectAgentWaves(registry *agent.Registry) []harness.WaveStage {
	if registry == nil {
		return nil
	}
	var stages []harness.WaveStage
	for _, m := range registry.List() {
		if m.Harness == nil {
			continue
		}
		for _, w := range m.Harness.Waves {
			stages = append(stages, harness.WaveStage{
				Name:         w.Name,
				ExpectedKeys: append([]string(nil), w.ExpectedKeys...),
				Description:  w.Description,
			})
		}
	}
	return stages
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
	return createHarnessStreamer(inner, registry, cfg, p, "", nil)
}
