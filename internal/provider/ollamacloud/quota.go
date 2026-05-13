package ollamacloud

import "github.com/baphled/flowstate/internal/provider/quota"

// NewQuota returns the v1 quota adapter for Ollama Cloud.
//
// PR1 ships NotConfigured{Reason:"awaiting-pr3"} per the Provider
// Quota and Spend Visibility plan (May 2026), per-provider matrix
// line 148: Ollama Cloud uses the openaicompat success-path code
// and is gated on PR3's live-key smoke test to confirm whether the
// upstream proxy actually emits the rate-limit headers.
//
// accountHash partitions Snapshots across rotated keys; pass
// quota.HashAccount(apiKey) at boot.
func NewQuota(accountHash string) quota.Quota {
	return quota.NewNotConfiguredAdapter("ollamacloud", accountHash, "awaiting-pr3")
}
