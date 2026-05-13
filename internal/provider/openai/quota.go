package openai

import "github.com/baphled/flowstate/internal/provider/quota"

// NewQuota returns the v1 quota adapter for OpenAI.
//
// PR1 ships NotConfigured{Reason:"awaiting-pr3"} per the Provider
// Quota and Spend Visibility plan (May 2026), per-provider matrix
// line 144 and Rollout Plan PR3 row 427: OpenAI uses the
// openaicompat success-path code, which is the PR3 lift target.
// Until PR3 ships, OpenAI's chip honestly surfaces NotConfigured
// with the operator-visible "awaiting-pr3" reason.
//
// accountHash partitions Snapshots across rotated keys; pass
// quota.HashAccount(apiKey) at boot.
func NewQuota(accountHash string) quota.Quota {
	return quota.NewNotConfiguredAdapter("openai", accountHash, "awaiting-pr3")
}
