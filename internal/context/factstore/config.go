package factstore

// Config carries the Phase B fact-extraction knobs. Mirrors the flat
// shape of compaction.Config so the root config can host both
// side-by-side without nested ordering.
//
// The zero value is intentionally near-default: a missing Config in a
// test fixture still produces sensible behaviour, with one caveat —
// callers that build a Service in production MUST populate from
// DefaultConfig because Enabled is false on the zero value.
type Config struct {
	// MaxFactsPerSession soft-caps how many facts a single session
	// may accumulate before older facts are pruned at Append time.
	// Zero or negative disables the cap. Default 1024.
	MaxFactsPerSession int `json:"max_facts_per_session" yaml:"max_facts_per_session"`
	// RecallTopK is the default top-K returned by Service.Recall when
	// the caller passes a non-positive K. Default 5.
	RecallTopK int `json:"recall_top_k" yaml:"recall_top_k"`
}

// DefaultConfig returns the production-safe Phase B defaults.
//
// Returns:
//   - MaxFactsPerSession: 1024
//   - RecallTopK:         5
func DefaultConfig() Config {
	return Config{
		MaxFactsPerSession: 1024,
		RecallTopK:         5,
	}
}

// ApplyDefaults fills any zero-valued fields on c from DefaultConfig.
//
// Expected:
//   - c is a non-nil Config pointer.
//
// Side effects:
//   - Mutates c in place.
func ApplyDefaults(c *Config) {
	d := DefaultConfig()
	if c.MaxFactsPerSession == 0 {
		c.MaxFactsPerSession = d.MaxFactsPerSession
	}
	if c.RecallTopK == 0 {
		c.RecallTopK = d.RecallTopK
	}
}
