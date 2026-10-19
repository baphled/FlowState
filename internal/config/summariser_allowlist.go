package config

import (
	"sync"

	"github.com/baphled/flowstate/internal/engine"
)

// SummariserAllowlist bounds the delivery-scoped summariser chain. When
// installed, SummariserChainEntriesScoped drops every hop whose
// provider/model PAIR is not admitted — mirroring the failover
// package's single delivery admission policy (R2-05): provider-name-only
// filtering could retain an off-policy model (e.g. the legacy glm-4.7
// default) on an allowlisted provider. Unscoped (product) mode is
// preserved: a nil/empty scope emits the legacy chain unchanged.
//
// Recorded pair-set decision (R2-05): the permitted summariser set is
// IDENTICAL to failover.DefaultDeliveryAllowlist — zai/glm-5.2 (primary)
// and zai/glm-4-flash (quick tier) — applied across every call class
// (roots, children, retries, compaction). The previous provider-only
// filter and the glm-4.7 default are superseded under delivery scope;
// the unscoped product default (glm-4.7 when no model is pinned) is
// unchanged for non-delivery callers.
type SummariserAllowlist struct {
	// pairs maps "provider/model" (or "provider/*") to true.
	pairs map[string]bool
}

// NewSummariserAllowlist builds an immutable, pair-scoped allowlist
// from explicit provider/model pairs ("*" admits any model on the
// provider).
func NewSummariserAllowlist(pairs ...[2]string) SummariserAllowlist {
	a := SummariserAllowlist{pairs: make(map[string]bool, len(pairs))}
	for _, p := range pairs {
		a.pairs[p[0]+"/"+p[1]] = true
	}
	return a
}

// DefaultSummariserDeliveryAllowlist is the recorded delivery policy,
// pair-identical to the failover delivery allowlist: zai/glm-5.2 plus
// the glm-4-flash quick tier.
func DefaultSummariserDeliveryAllowlist() SummariserAllowlist {
	return NewSummariserAllowlist(
		[2]string{"zai", "glm-5.2"},
		[2]string{"zai", "glm-4-flash"},
	)
}

// empty reports whether no scope is installed (legacy unscoped mode).
func (a SummariserAllowlist) empty() bool { return len(a.pairs) == 0 }

// allows reports whether the pair passes the scope.
func (a SummariserAllowlist) allows(provider, model string) bool {
	if a.empty() {
		return true
	}
	return a.pairs[provider+"/"+model] || a.pairs[provider+"/*"]
}

// summariserScope guards the process-wide delivery scope. R2-05: the
// scope is an IMMUTABLE value held behind a mutex — a zero-value reset
// can no longer silently restore unscoped behaviour. Once installed,
// only ClearSummariserDeliveryScope (an explicit, auditable action)
// removes it.
var summariserScope struct {
	sync.RWMutex
	scope     SummariserAllowlist
	installed bool
}

// SetSummariserAllowlist installs the delivery scope. An empty
// allowlist is IGNORED once a scope is installed — the Round-2 review
// flagged the old empty-assignment reset as an integration hazard where
// one session's reset could unscope another session's compaction.
// Clearing requires ClearSummariserDeliveryScope.
func SetSummariserAllowlist(a SummariserAllowlist) {
	summariserScope.Lock()
	defer summariserScope.Unlock()
	if summariserScope.installed && a.empty() {
		return
	}
	summariserScope.scope = a
	summariserScope.installed = !a.empty()
}

// ClearSummariserDeliveryScope explicitly removes the installed scope,
// restoring legacy unscoped product behaviour. Auditable by intent.
func ClearSummariserDeliveryScope() {
	summariserScope.Lock()
	defer summariserScope.Unlock()
	summariserScope.scope = SummariserAllowlist{}
	summariserScope.installed = false
}

// currentSummariserScope snapshots the installed scope immutably so
// chain resolution cannot race a concurrent install/clear.
func currentSummariserScope() SummariserAllowlist {
	summariserScope.RLock()
	defer summariserScope.RUnlock()
	return summariserScope.scope
}

// SummariserChainEntriesScoped is SummariserChainEntries with the
// installed delivery scope applied: only pair-admitted providers with a
// non-empty resolved model are emitted. May return an EMPTY slice —
// that is the explicit durable unavailable outcome for compaction
// (engine.NewSummariserChain treats an empty chain as
// ErrSummariserUnavailable at first use), never a crossing: fail
// closed, mirroring the failover package's policy.
func (c *AppConfig) SummariserChainEntriesScoped() []engine.SummariserChainEntry {
	scope := currentSummariserScope()
	raw := c.SummariserChainEntries()
	if scope.empty() {
		return raw
	}
	scoped := make([]engine.SummariserChainEntry, 0, len(raw))
	for _, e := range raw {
		if e.Model == "" {
			continue
		}
		if !scope.allows(e.Provider, e.Model) {
			continue
		}
		scoped = append(scoped, e)
	}
	return scoped
}
