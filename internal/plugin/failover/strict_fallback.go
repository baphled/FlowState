package failover

import (
	"github.com/baphled/flowstate/internal/provider"
)

// strictFallbackWarns tracks which pinned pairs have already emitted
// the fallback WARN so repeated chain resolutions log once per pair
// rather than per resolution. Stub state pending the implementation.
var strictFallbackWarns = map[string]bool{}

// StrictChainWithHealthyFallback resolves a strict manifest's chain
// against the hard-down breaker: when the strict head is healthy the
// chain stays strict; when the head is hard-down the healthy global
// chain is appended as a fallback tail. Stub pending the implementation
// — returns the strict chain unchanged.
//
// Expected:
//   - strict is the manifest's declared chain (non-empty).
//   - global is the engine's healthy global preference chain.
//   - health may be nil (strict behaviour preserved).
//
// Returns:
//   - The strict chain unchanged in the stub.
//
// Side effects:
//   - None in the stub.
func StrictChainWithHealthyFallback(strict, global []provider.ModelPreference, health *HealthManager) []provider.ModelPreference {
	_ = global
	_ = health
	_ = strictFallbackWarns
	return strict
}
