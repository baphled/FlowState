package openzen

import "github.com/baphled/flowstate/internal/provider/quota"

// NewQuota returns the v1 quota adapter for OpenZen.
//
// PR1 ships NotConfigured{Reason:"awaiting-pr3"} per the Provider
// Quota and Spend Visibility plan (May 2026), per-provider matrix
// line 145: OpenZen inherits the openaicompat success-path lift,
// which is PR3 territory.
//
// accountHash partitions Snapshots across rotated keys; pass
// quota.HashAccount(apiKey) at boot.
func NewQuota(accountHash string) quota.Quota {
	return quota.NewNotConfiguredAdapter("openzen", accountHash, "awaiting-pr3")
}
