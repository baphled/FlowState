package providers

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
	"github.com/baphled/flowstate/internal/provider/copilot"
	"github.com/baphled/flowstate/internal/provider/ollama"
	"github.com/baphled/flowstate/internal/provider/ollamacloud"
	"github.com/baphled/flowstate/internal/provider/openai"
	"github.com/baphled/flowstate/internal/provider/opencodego"
	"github.com/baphled/flowstate/internal/provider/openzen"
	"github.com/baphled/flowstate/internal/provider/zai"
)

// ErrOpenAINoKey is returned when OpenAI has no API key from any source. It
// is exported so tests and the error-surface helper can match it
// programmatically without coupling to a specific log-message string.
var ErrOpenAINoKey = errors.New(
	"no API key (set OPENAI_API_KEY or providers.openai.api_key)",
)

// Build initialises and registers all configured LLM providers. Use
// BuildWithFailures when callers need the underlying construction errors.
//
// Expected:
//   - cfg is a non-nil AppConfig with provider configuration.
//
// Returns:
//   - A provider.Registry containing all successfully initialised providers.
//   - The Ollama provider instance (may be nil if initialisation failed).
//
// Side effects:
//   - Reads provider env vars; logs warnings on per-provider failure.
//   - Registers providers with the returned registry on success.
func Build(cfg *config.AppConfig) (*provider.Registry, *ollama.Provider) {
	registry, ollamaProv, _ := BuildWithFailures(cfg)
	return registry, ollamaProv
}

// BuildWithFailures initialises providers and also reports why any of them
// failed to register. The failures map is keyed by provider name and
// contains the underlying error returned by the provider constructor (or a
// synthetic ErrOpenAINoKey when OpenAI is skipped because no key is set).
//
// Expected:
//   - cfg is a non-nil AppConfig with provider configuration.
//
// Returns:
//   - A provider.Registry containing all successfully initialised providers.
//   - The Ollama provider instance (may be nil if initialisation failed).
//   - A map of provider-name to constructor error for each failed provider.
//
// Side effects:
//   - Reads OPENAI_API_KEY, ANTHROPIC_API_KEY, GITHUB_TOKEN, ZAI_API_KEY,
//     OPENZEN_API_KEY environment variables.
//   - Logs a warning for each provider that fails to register.
//   - Registers providers with the registry if initialisation succeeds.
func BuildWithFailures(
	cfg *config.AppConfig,
) (*provider.Registry, *ollama.Provider, map[string]error) {
	providerRegistry := provider.NewRegistry()
	failures := make(map[string]error)

	ollamaProvider, ollamaErr := ollama.New(cfg.Providers.Ollama.Host)
	recordProvider(providerRegistry, failures, "ollama", ollamaProvider, ollamaErr,
		cfg.Providers.Ollama.EffectiveMaxConcurrent("ollama"))

	ollamaCloudKey := ResolveProviderKey("OLLAMA_CLOUD_API_KEY", cfg.Providers.OllamaCloud.APIKey)
	ollamaCloudProvider, ollamaCloudErr := ollamacloud.NewFromConfig(ollamaCloudKey, cfg.Providers.OllamaCloud.Host)
	recordProvider(providerRegistry, failures, "ollamacloud", ollamaCloudProvider, ollamaCloudErr,
		cfg.Providers.OllamaCloud.EffectiveMaxConcurrent("ollamacloud"))

	openaiProvider, openaiErr := buildOpenAIProvider(cfg)
	recordProvider(providerRegistry, failures, "openai", openaiProvider, openaiErr,
		cfg.Providers.OpenAI.EffectiveMaxConcurrent("openai"))

	anthropicKey := ResolveProviderKey("ANTHROPIC_API_KEY", cfg.Providers.Anthropic.APIKey)
	anthropicProvider, anthropicErr := anthropic.NewFromConfig(anthropicKey)
	recordProvider(providerRegistry, failures, "anthropic", anthropicProvider, anthropicErr,
		cfg.Providers.Anthropic.EffectiveMaxConcurrent("anthropic"))

	githubToken := ResolveProviderKey("GITHUB_TOKEN", cfg.Providers.GitHub.APIKey)
	copilotProvider, copilotErr := copilot.NewFromConfig(nil, githubToken)
	recordProvider(providerRegistry, failures, "copilot", copilotProvider, copilotErr,
		cfg.Providers.GitHub.EffectiveMaxConcurrent("copilot"))

	zaiKey := ResolveProviderKey("ZAI_API_KEY", cfg.Providers.ZAI.APIKey)
	zaiPlan := zaiPlanFromConfig(cfg)
	zaiProvider, zaiErr := zai.NewFromConfig(zaiKey, zaiPlan)
	recordProvider(providerRegistry, failures, "zai", zaiProvider, zaiErr,
		cfg.Providers.ZAI.EffectiveMaxConcurrent("zai"))
	logZAIPlanResolution(zaiPlan, zaiErr)

	openzenKey := ResolveProviderKey("OPENZEN_API_KEY", cfg.Providers.OpenZen.APIKey)
	openzenProvider, openzenErr := openzen.NewFromConfig(openzenKey)
	recordProvider(providerRegistry, failures, "openzen", openzenProvider, openzenErr,
		cfg.Providers.OpenZen.EffectiveMaxConcurrent("openzen"))

	opencodeGoKey := ResolveProviderKey("OPENCODE_GO_API_KEY", cfg.Providers.OpenCodeGo.APIKey)
	opencodeGoProvider, opencodeGoErr := opencodego.NewFromConfig(opencodeGoKey)
	recordProvider(providerRegistry, failures, "opencode-go", opencodeGoProvider, opencodeGoErr,
		cfg.Providers.OpenCodeGo.EffectiveMaxConcurrent("opencode-go"))

	warnIfOpenCodeAuthPresent(failures)

	return providerRegistry, ollamaProvider, failures
}

// ResolveProviderKey returns the value of envVar if set, otherwise the
// fallback from application configuration.
//
// Expected:
//   - envVar is a non-empty environment variable name.
//
// Returns:
//   - The environment variable value, or cfgValue if the variable is unset.
//
// Side effects:
//   - Reads the given environment variable.
func ResolveProviderKey(envVar, cfgValue string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return cfgValue
}

// BuildConfigPreferences constructs a provider preference list from
// application configuration.
//
// Expected:
//   - cfg is a non-nil AppConfig with provider configuration.
//
// Returns:
//   - A slice of ModelPreference values in configuration order, skipping
//     providers with no model configured.
//
// Side effects:
//   - None.
func BuildConfigPreferences(cfg *config.AppConfig) []provider.ModelPreference {
	type namedProvider struct {
		name  string
		model string
	}

	allProviders := []namedProvider{
		{"anthropic", cfg.Providers.Anthropic.Model},
		{"openai", cfg.Providers.OpenAI.Model},
		{"zai", cfg.Providers.ZAI.Model},
		{"copilot", cfg.Providers.GitHub.Model},
		{"openzen", cfg.Providers.OpenZen.Model},
		{"opencode-go", cfg.Providers.OpenCodeGo.Model},
		{"ollamacloud", cfg.Providers.OllamaCloud.Model},
		{"ollama", cfg.Providers.Ollama.Model},
	}

	sorted := allProviders

	var prefs []provider.ModelPreference
	for _, p := range sorted {
		if p.model == "" {
			continue
		}
		prefs = append(prefs, provider.ModelPreference{
			Provider: p.name,
			Model:    p.model,
		})
	}
	return prefs
}

// ResolveDefault validates that the configured default provider exists in the registry.
func ResolveDefault(registry *provider.Registry, failures map[string]error, defaultName string) error {
	if _, err := registry.Get(defaultName); err != nil {
		if failure, ok := failures[defaultName]; ok && failure != nil {
			return fmt.Errorf("default provider %q unavailable: %w", defaultName, failure)
		}
		return fmt.Errorf("default provider %q unavailable: %w", defaultName, err)
	}
	return nil
}

// buildOpenAIProvider constructs the OpenAI provider from the configured
// key, returning ErrOpenAINoKey when no key is available so the caller
// records a uniform failure message.
func buildOpenAIProvider(cfg *config.AppConfig) (*openai.Provider, error) {
	key := ResolveProviderKey("OPENAI_API_KEY", cfg.Providers.OpenAI.APIKey)
	if key == "" {
		return nil, ErrOpenAINoKey
	}
	return openai.NewFromConfig(key)
}

// recordProvider registers a provider on success or records the error
// under name in the failures map. Logs a warning on failure so startup
// diagnostics remain visible even when the failure is not fatal.
//
// When maxConcurrent > 0 the provider is wrapped in a
// provider.ConcurrencyLimitedProvider before registration so FlowState never
// exceeds the provider's concurrent-request cap, regardless of how the
// concurrency arises (e.g. a swarm lead's batched delegate tool calls each
// opening their own stream). Wrapping here — rather than at the traced-provider
// seam in the composition root — ensures all failover targets resolved from
// this registry inherit the cap. A maxConcurrent of 0 leaves the provider
// unwrapped, preserving prior behaviour.
func recordProvider(
	registry *provider.Registry,
	failures map[string]error,
	name string,
	p provider.Provider,
	err error,
	maxConcurrent int,
) {
	if err == nil {
		if maxConcurrent > 0 {
			p = provider.NewConcurrencyLimitedProvider(p, maxConcurrent)
		}
		registry.Register(p)
		return
	}
	failures[name] = err
	log.Printf("warning: provider %q unavailable: %v", name, err)
}

// zaiAllProvidersFailed reports whether every authenticated provider in
// the failures map is failing — i.e. nothing was successfully registered
// for any authenticated provider. We use this to decide whether to emit
// the OpenCode-migration WARN.
func zaiAllProvidersFailed(failures map[string]error) bool {
	authProviders := []string{"anthropic", "copilot", "zai", "openzen"}
	for _, name := range authProviders {
		if _, failed := failures[name]; !failed {
			return false
		}
	}
	return true
}

// warnIfOpenCodeAuthPresent emits a one-time WARN when the user appears to
// have an OpenCode auth.json on disk and no FlowState provider authenticated
// successfully. The OpenCode credential bridge has been removed, so the
// user must paste keys into config.yaml or run `flowstate auth <provider>`.
func warnIfOpenCodeAuthPresent(failures map[string]error) {
	if !zaiAllProvidersFailed(failures) {
		return
	}
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		return
	}
	opencodePath := filepath.Join(homeDir, ".local", "share", "opencode", "auth.json")
	if _, err := os.Stat(opencodePath); err != nil {
		return
	}
	slog.Warn(
		"detected OpenCode auth.json but no FlowState provider authenticated; "+
			"FlowState no longer reads OpenCode credentials. "+
			"Run `flowstate auth anthropic` / `flowstate auth github-copilot` "+
			"or set provider keys directly in ~/.config/flowstate/config.yaml.",
		"opencode_auth_path", opencodePath,
	)
}

// logZAIPlanResolution emits a single INFO line at startup describing the
// Z.AI plan + endpoint the provider resolved to. Operators hitting HTTP 429
// "code 1113 (billing)" against Z.AI almost always have a coding-plan key
// pointed at the general endpoint (or vice versa); the log line is the
// fastest way to confirm the routing without adding a CLI status command.
//
// Skips when the provider failed to initialise (no point announcing a
// non-existent route).
func logZAIPlanResolution(plan string, initErr error) {
	if initErr != nil {
		return
	}
	planLabel := "general (pay-per-token)"
	endpoint := "https://api.z.ai/api/paas/v4"
	if plan == zai.PlanCoding {
		planLabel = "coding-plan subscription"
		endpoint = "https://api.z.ai/api/coding/paas/v4"
	}
	slog.Info("z.ai provider routed", "plan", planLabel, "endpoint", endpoint)
}

// zaiPlanFromConfig returns the Z.AI plan tag for the configured provider.
// "coding" selects the coding-plan subscription endpoint; anything else
// (including empty) selects the general pay-per-token endpoint.
//
// Resolution precedence:
//  1. Explicit cfg.Providers.ZAI.Plan ("coding" or "general", case-insensitive
//     trim) — wins, even when contradicted by Host.
//  2. Empty Plan but Host equal to the coding-plan URL — back-compat
//     inference for legacy configs that encoded the plan in Host.
//  3. Otherwise — empty string ("general").
func zaiPlanFromConfig(cfg *config.AppConfig) string {
	plan := strings.ToLower(strings.TrimSpace(cfg.Providers.ZAI.Plan))
	if plan == zai.PlanCoding {
		return zai.PlanCoding
	}
	if plan != "" {
		return ""
	}
	if cfg.Providers.ZAI.Host == "https://api.z.ai/api/coding/paas/v4" {
		return zai.PlanCoding
	}
	return ""
}
