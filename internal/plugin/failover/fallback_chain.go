package failover

import (
	"errors"
)

// Tier0 is the primary tier (Anthropic).
const Tier0 = "tier-0"

// Tier1 is the first fallback tier (GitHub Copilot).
const Tier1 = "tier-1"

// Tier2 is the second fallback tier (OpenAI).
const Tier2 = "tier-2"

// Tier3 is the local fallback tier (Ollama).
const Tier3 = "tier-3"

// FallbackChain manages an ordered list of providers with tier-based fallback logic.
type FallbackChain struct {
	providers []ProviderModel
	tiers     map[string]string
}

// NewFallbackChain creates a new FallbackChain with providers in order.
//
// Expected: providers is non-empty and ordered by preference tier (Tier0→Tier3).
// Tiers maps model names to tier constants (e.g. "claude-sonnet-4-20250514"→Tier0).
// When tiers is nil or empty, sensible defaults are used based on model families:
// premium models (Claude/GPT-4)→Tier0, standard models→Tier1, budget models→Tier2,
// local models (Ollama)→Tier3.
// Returns: a new FallbackChain with the resolved tier mappings.
// Side effects: allocates a new map for tier mappings.
func NewFallbackChain(providers []ProviderModel, tiers map[string]string) *FallbackChain {
	if len(tiers) == 0 {
		tiers = map[string]string{
			"claude-sonnet-4-20250514":   Tier0,
			"claude-3-5-sonnet-20241022": Tier0,
			"gpt-4o":                     Tier1,
			"gpt-4o-mini":                Tier2,
			"llama3.2":                   Tier3,
			"llama3":                     Tier3,
		}
	}

	fc := &FallbackChain{
		providers: providers,
		tiers:     tiers,
	}
	return fc
}

// NextHealthy returns the next healthy (non-rate-limited) provider after current.
//
// Expected: current is a valid ProviderModel, health is a non-nil HealthManager instance.
// Returns: the first healthy provider in chain order after current, or zero ProviderModel
// with error if all remaining providers are rate-limited or chain is exhausted.
// Side effects: none (read-only operation on health state).
func (fc *FallbackChain) NextHealthy(current ProviderModel, health *HealthManager) (ProviderModel, error) {
	// Find current provider index
	currentIdx := -1
	for i, p := range fc.providers {
		if p.Provider == current.Provider && p.Model == current.Model {
			currentIdx = i
			break
		}
	}

	// Walk through remaining providers
	for i := currentIdx + 1; i < len(fc.providers); i++ {
		provider := fc.providers[i]
		if !health.IsRateLimited(provider.Provider, provider.Model) {
			return provider, nil
		}
	}

	// No healthy provider found
	return ProviderModel{}, errors.New("all providers rate-limited or exhausted")
}
