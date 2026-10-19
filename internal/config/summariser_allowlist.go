package config

import "github.com/baphled/flowstate/internal/engine"

// SummariserAllowlist bounds the delivery-scoped summariser chain. When
// non-empty, SummariserChainEntries drops every hop whose provider is
// not listed — the last-resort Ollama hop and the Anthropic hop are
// only appended when their providers are allowlisted AND their
// resolved model is non-empty (a blank model hop can never succeed and
// only wastes the compaction latency budget). M01: the operator's
// global change blanked ollama/anthropic models; with the default
// delivery scope (zai) the chain becomes zai-only so the last resort
// never crosses the delivery allowlist.
type SummariserAllowlist []string

// DefaultSummariserDeliveryAllowlist is the recorded delivery policy:
// Z.ai only (glm-5.2 primary, glm-4-flash quick tier).
func DefaultSummariserDeliveryAllowlist() SummariserAllowlist {
	return SummariserAllowlist{"zai"}
}

// contains reports whether the provider is allowlisted. Empty list =
// legacy behaviour (everything allowed).
func (a SummariserAllowlist) contains(provider string) bool {
	if len(a) == 0 {
		return true
	}
	for _, p := range a {
		if p == provider {
			return true
		}
	}
	return false
}

// summariserAllowlist is the process-wide delivery scope for the
// compaction summariser chain. Installed by the app bootstrap from the
// delivery policy; zero value (nil) preserves the legacy three-hop
// matrix for non-delivery callers and tests.
var summariserAllowlist SummariserAllowlist

// SetSummariserAllowlist installs the delivery scope. Passing an empty
// list resets to the legacy unscoped behaviour.
func SetSummariserAllowlist(a SummariserAllowlist) { summariserAllowlist = a }

// SummariserChainEntriesScoped is SummariserChainEntries with the
// installed delivery scope applied: only allowlisted providers with a
// non-empty resolved model are emitted. May return an EMPTY slice —
// that is the explicit durable unavailable outcome for compaction
// (engine.NewSummariserChain treats an empty chain as
// ErrSummariserUnavailable at first use), not a crossing.
func (c *AppConfig) SummariserChainEntriesScoped() []engine.SummariserChainEntry {
	raw := c.SummariserChainEntries()
	if len(summariserAllowlist) == 0 {
		return raw
	}
	scoped := make([]engine.SummariserChainEntry, 0, len(raw))
	for _, e := range raw {
		if e.Model == "" {
			continue
		}
		if !summariserAllowlist.contains(e.Provider) {
			continue
		}
		scoped = append(scoped, e)
	}
	if len(scoped) > 0 {
		return scoped
	}
	// Empty scoped chain would silently disable compaction for callers
	// that did not opt into the delivery scope; fall back to the legacy
	// matrix and let attempt-time health filtering surface failures.
	return raw
}
