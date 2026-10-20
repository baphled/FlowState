package failover

// localProviders names the provider adapters that serve models from the
// operator's own machine rather than a hosted API. Strict-policy agents
// pin hosted, tool-calling-grade providers; failing one of those chains
// onto a local model silently degrades the agent below its declared bar
// (malformed tool calls, stalled swarms), so the strict fallback tail
// must never include them.
var localProviders = map[string]struct{}{
	"ollama": {},
}

// IsLocalProvider reports whether the named provider adapter serves
// models locally (on-operator-hardware) rather than via a hosted API.
// Used by the strict fallback tail to keep local models out of strict
// chains, and by callers that need the same class distinction.
//
// Expected: providerName is a provider adapter name (case-insensitive).
// Returns: true for local adapters, false for everything else.
// Side effects: none.
func IsLocalProvider(providerName string) bool {
	_, ok := localProviders[lowerASCII(providerName)]
	return ok
}

// lowerASCII lowercases ASCII letters only — provider names in this
// codebase are ASCII, and this avoids strings.ToLower's allocation for
// the already-lowercase common path.
//
// Expected: s may be any string.
// Returns: the ASCII-lowercased string.
// Side effects: none.
func lowerASCII(s string) string {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for ; i < len(b); i++ {
				if b[i] >= 'A' && b[i] <= 'Z' {
					b[i] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}
