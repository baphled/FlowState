package failover

import (
	"log/slog"
	"sync"

	"github.com/baphled/flowstate/internal/provider"
)

// strictFallbackWarnOnce dedupes the pinned-fallback WARN per pinned
// pair so repeated chain resolutions (manifest reseeds, delegate child
// creations) log once per pair per process instead of per resolution.
var strictFallbackWarnOnce sync.Map

// StrictChainWithHealthyFallback resolves a strict manifest's chain
// against the hard-down breaker. When the strict head is healthy the
// chain stays strictly as declared — no config tail leaks in. When the
// head is hard-down the healthy global chain is appended as a fallback
// tail (skipping every unavailable pair, hard-down or cooldowned) so a
// strict-pinned agent completes its turn on a healthy provider instead
// of failing with "all providers failed". The dead head is retained in
// the returned preferences — attempt-time health filtering skips it —
// so re-resolution can un-fallback once the operator resets the pair.
// One WARN per pinned pair names the pair and the fallback.
//
// Expected:
//   - strict is the manifest's declared chain (non-empty).
//   - global is the engine's global preference chain used as the tail
//     source.
//   - health may be nil (strict behaviour preserved).
//
// Returns:
//   - The strict chain unchanged when the head is healthy, health is
//     nil, or the strict chain is empty.
//   - The strict chain with the healthy, deduplicated global tail
//     appended when the head is hard-down.
//
// Side effects:
//   - Emits one slog.Warn per pinned pair on the first fallback.
func StrictChainWithHealthyFallback(strict, global []provider.ModelPreference, health *HealthManager) []provider.ModelPreference {
	if len(strict) == 0 || health == nil {
		return strict
	}
	head := strict[0]
	if !health.IsHardDown(head.Provider, head.Model) {
		return strict
	}

	seen := make(map[string]bool, len(strict)+len(global))
	resolved := make([]provider.ModelPreference, 0, len(strict)+len(global))
	for _, pref := range strict {
		key := pref.Provider + "/" + pref.Model
		if seen[key] {
			continue
		}
		seen[key] = true
		resolved = append(resolved, pref)
	}
	for _, pref := range global {
		key := pref.Provider + "/" + pref.Model
		if seen[key] {
			continue
		}
		if health.IsRateLimited(pref.Provider, pref.Model) {
			continue
		}
		seen[key] = true
		resolved = append(resolved, pref)
	}

	warnKey := head.Provider + "/" + head.Model
	if _, logged := strictFallbackWarnOnce.LoadOrStore(warnKey, true); !logged {
		slog.Warn("pinned provider hard-down; falling back to healthy chain",
			"pinned_provider", head.Provider,
			"pinned_model", head.Model,
			"fallback_entries", len(resolved)-len(strict),
		)
	}
	return resolved
}
