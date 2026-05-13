package ollama

import "github.com/baphled/flowstate/internal/provider/quota"

// NewQuota returns the v1 quota adapter for Ollama.
//
// Ships NotConfigured{Reason:"local-model"} permanently per the
// Provider Quota and Spend Visibility plan (May 2026), per-provider
// matrix line 147: Ollama is local-only, has no published rate-
// limit headers, and has no billing API. The "local-model" reason
// is the operator-visible explanation in the chip tooltip.
//
// accountHash is typically the empty string for local Ollama (no
// API key concept); the AccountHash field on the Snapshot is
// preserved for the rare deployment that proxies Ollama through a
// gateway with key auth.
func NewQuota(accountHash string) quota.Quota {
	return quota.NewNotConfiguredAdapter("ollama", accountHash, "local-model")
}
