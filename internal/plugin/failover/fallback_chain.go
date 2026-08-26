package failover

import (
	"errors"

	"github.com/baphled/flowstate/internal/provider"
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
// Returns: a new FallbackChain with the resolved tier mappings.
// Side effects: none.
func NewFallbackChain(providers []ProviderModel, tiers map[string]string) *FallbackChain {
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
	currentIdx := -1
	for i, p := range fc.providers {
		if p.Provider == current.Provider && p.Model == current.Model {
			currentIdx = i
			break
		}
	}

	if currentIdx >= len(fc.providers)-1 {
		return ProviderModel{}, errors.New("all providers rate-limited or exhausted")
	}

	remaining := make([]provider.ModelPreference, 0, len(fc.providers)-(currentIdx+1))
	for i := currentIdx + 1; i < len(fc.providers); i++ {
		candidate := fc.providers[i]
		remaining = append(remaining, provider.ModelPreference{Provider: candidate.Provider, Model: candidate.Model})
	}

	ranked := rankCandidatesByHealth(health, fc.tiers, nil, remaining)
	if len(ranked) == 0 {
		return ProviderModel{}, errors.New("all providers rate-limited or exhausted")
	}

	return ProviderModel{Provider: ranked[0].Provider, Model: ranked[0].Model}, nil
}
