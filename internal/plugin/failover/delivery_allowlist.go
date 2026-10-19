package failover

import (
	"github.com/baphled/flowstate/internal/provider"
)

// DeliveryAllowlist is the delivery-scoped provider/model admission
// predicate enforced at actual dispatch surfaces (M01). When non-nil,
// failover tails appended to strict chains — and the candidate pool the
// retry/stream hooks admit — are filtered to pairs the allowlist
// accepts. A nil allowlist disables filtering (legacy behaviour) so
// non-delivery callers keep full provider parity.
type DeliveryAllowlist struct {
	// Pairs maps "provider/model" (or "provider" for any model on that
	// provider) to allowed=true.
	Pairs map[string]bool
}

// NewDeliveryAllowlist builds an allowlist from explicit provider/model
// pairs. A model of "*" admits every model on that provider.
func NewDeliveryAllowlist(pairs ...[2]string) *DeliveryAllowlist {
	d := &DeliveryAllowlist{Pairs: make(map[string]bool, len(pairs))}
	for _, p := range pairs {
		d.Pairs[p[0]+"/"+p[1]] = true
	}
	return d
}

// DefaultDeliveryAllowlist is the recorded delivery policy: Z.ai
// primary glm-5.2 plus the quick-tier glm-4-flash fallback.
func DefaultDeliveryAllowlist() *DeliveryAllowlist {
	return NewDeliveryAllowlist(
		[2]string{"zai", "glm-5.2"},
		[2]string{"zai", "glm-4-flash"},
	)
}

// Allowed reports whether the pair passes the allowlist. A nil
// allowlist admits everything (legacy behaviour).
func (d *DeliveryAllowlist) Allowed(providerName, model string) bool {
	if d == nil {
		return true
	}
	if d.Pairs[providerName+"/"+model] {
		return true
	}
	return d.Pairs[providerName+"/*"]
}

// filterAllowlisted returns prefs filtered to allowlisted pairs. A nil
// allowlist returns prefs unchanged. The strict head (index 0) is always
// retained — its health filtering happens at attempt time, and dropping
// it would change the manifest-declared chain semantics.
func filterAllowlisted(prefs []provider.ModelPreference, allow *DeliveryAllowlist, retainHead bool) []provider.ModelPreference {
	if allow == nil || len(prefs) == 0 {
		return prefs
	}
	out := make([]provider.ModelPreference, 0, len(prefs))
	for i, pref := range prefs {
		if retainHead && i == 0 {
			out = append(out, pref)
			continue
		}
		if allow.Allowed(pref.Provider, pref.Model) {
			out = append(out, pref)
		}
	}
	return out
}

// StrictChainWithHealthyFallbackDelivery is
// StrictChainWithHealthyFallback with the delivery allowlist applied to
// the appended fallback tail: only allowlisted, healthy, non-duplicate
// global entries are appended. The strict head is preserved verbatim so
// re-resolution can un-fallback after an operator reset. When no
// allowlisted route exists the returned chain contains only the strict
// entries — the engine's attempt-time filtering then produces the
// explicit "no healthy providers available" terminal outcome instead of
// silently crossing to an out-of-scope provider.
func StrictChainWithHealthyFallbackDelivery(strict, global []provider.ModelPreference, health *HealthManager, allow *DeliveryAllowlist) []provider.ModelPreference {
	if allow == nil {
		return StrictChainWithHealthyFallback(strict, global, health)
	}
	return StrictChainWithHealthyFallback(strict, filterAllowlisted(global, allow, false), health)
}
