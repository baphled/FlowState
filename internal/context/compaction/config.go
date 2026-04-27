package compaction

// Config carries the Phase A micro-compaction knobs. It mirrors the shape
// of internal/context.CompressionConfig (a single flat block per layer)
// so the root config can host both side-by-side without callers having to
// reason about intra-block ordering.
//
// All values default to the production-safe set returned by
// DefaultConfig. The zero value is intentionally near-default so test
// fixtures that omit a Config struct still get sane behaviour, with two
// caveats:
//
//   - MicroEnabled defaults to true in DefaultConfig but to false on the
//     zero value, so callers building an engine.Config in a test that
//     wants compaction MUST set MicroEnabled explicitly OR populate the
//     struct from DefaultConfig.
//   - StoreDir on the zero value is empty, which disables disk writes
//     gracefully — Compact still emits reference messages but the cold
//     payloads are silently dropped. Production wiring sets StoreDir
//     to <sessionsDir>/<sessionID>/compacted via NewMicroCompactor.
type Config struct {
	// MicroEnabled toggles the Phase A compactor on/off. Default true.
	MicroEnabled bool `json:"micro_enabled" yaml:"micro_enabled"`
	// HotTailMinResults is the minimum number of recent compactable tool
	// results kept fully visible. Default 3.
	HotTailMinResults int `json:"hot_tail_min_results" yaml:"hot_tail_min_results"`
	// HotTailSizeBudget is the soft cap (in approximate tokens, where
	// 1 token = 4 bytes per the Anthropic rule of thumb) for the
	// combined size of compactable tool-result content kept in the hot
	// tail. Older results overflow to cold storage. Default 8000.
	HotTailSizeBudget int `json:"hot_tail_size_budget" yaml:"hot_tail_size_budget"`
	// FactExtractionEnabled toggles RLM Phase B Layer 3 (incremental
	// fact extraction). When true, the engine wires a factstore.Service
	// that prepends a "[recalled facts]" system block to each provider
	// request. Default false — Phase B ships behind a flag because the
	// regex extractor is intentionally conservative and the LLM-driven
	// extractor lands in Phase C.
	FactExtractionEnabled bool `json:"fact_extraction_enabled" yaml:"fact_extraction_enabled"`
}

// DefaultConfig returns the production-safe Phase A defaults.
//
// Returns:
//   - MicroEnabled:      true
//   - HotTailMinResults: 3
//   - HotTailSizeBudget: 8000 (≈ 32 KiB of tool-result text)
func DefaultConfig() Config {
	return Config{
		MicroEnabled:      true,
		HotTailMinResults: 3,
		HotTailSizeBudget: 8000,
	}
}

// ApplyDefaults fills any zero-valued numeric fields on c from
// DefaultConfig. MicroEnabled is left untouched — the caller's explicit
// boolean wins, even when it is false.
//
// Expected:
//   - c is a non-nil Config pointer.
//
// Side effects:
//   - Mutates c in place.
func ApplyDefaults(c *Config) {
	d := DefaultConfig()
	if c.HotTailMinResults == 0 {
		c.HotTailMinResults = d.HotTailMinResults
	}
	if c.HotTailSizeBudget == 0 {
		c.HotTailSizeBudget = d.HotTailSizeBudget
	}
}
