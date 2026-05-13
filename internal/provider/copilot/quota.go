package copilot

import "github.com/baphled/flowstate/internal/provider/quota"

// NewQuota returns the v1 quota adapter for GitHub Copilot.
//
// Ships NotConfigured{Reason:"subscription-only"} permanently per
// the Provider Quota and Spend Visibility plan (May 2026), per-
// provider matrix line 149: GitHub Copilot is subscription-based
// — no published rate-limit windows, no monthly token cap, no per-
// request meter a downstream consumer can poll. The subscription
// model doesn't fit either the RateLimit variant or the TokenSpend
// variant; surfacing NotConfigured permanently is the honest stance.
//
// Per plan line 149: "Possibly never fits — surface as NotConfigured
// permanently unless GitHub ships a public per-request meter."
//
// accountHash partitions Snapshots across rotated keys; pass
// quota.HashAccount(apiKey) at boot.
func NewQuota(accountHash string) quota.Quota {
	return quota.NewNotConfiguredAdapter("copilot", accountHash, "subscription-only")
}
