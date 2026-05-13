package zai

import "github.com/baphled/flowstate/internal/provider/quota"

// NewQuota returns the v1 quota adapter for Z.AI.
//
// PR1 ships NotConfigured{Reason:"awaiting-pr3"} per the Provider
// Quota and Spend Visibility plan (May 2026), per-provider matrix
// line 146: Z.AI inherits the openaicompat success-path lift, with
// additional error-code-1001/1112 refinement (zai.go:305-337). The
// success-path lift lands in PR3.
//
// accountHash partitions Snapshots across rotated keys; pass
// quota.HashAccount(apiKey) at boot.
func NewQuota(accountHash string) quota.Quota {
	return quota.NewNotConfiguredAdapter("zai", accountHash, "awaiting-pr3")
}
