package context

// CompressionConfig configures the three-layer context compression system.
//
// All three layers (MicroCompaction, AutoCompaction, SessionMemory) default
// to Enabled: false so that adding CompressionConfig to the root config is
// a no-op for existing deployments. Individual layers are activated by
// setting Enabled: true in the loaded configuration.
//
// Defaults are centralised in DefaultCompressionConfig. No threshold or
// storage path may be hard-coded elsewhere in the codebase — all consumers
// must read from this struct (or a value derived from it).
type CompressionConfig struct {
	MicroCompaction MicroCompactionConfig `json:"micro_compaction" yaml:"micro_compaction"`
	AutoCompaction  AutoCompactionConfig  `json:"auto_compaction" yaml:"auto_compaction"`
	SessionMemory   SessionMemoryConfig   `json:"session_memory" yaml:"session_memory"`
}

// MicroCompactionConfig controls Layer 1 — message-level micro-compaction.
//
// When disabled, no messages are offloaded to disk and the hot tail heuristic
// is not applied. When enabled, messages older than the hot tail and larger
// than TokenThreshold tokens may be replaced by pointer placeholders stored
// under StorageDir.
type MicroCompactionConfig struct {
	// Enabled toggles Layer 1 on or off. Defaults to false.
	Enabled bool `json:"enabled" yaml:"enabled"`
	// HotTailSize is the number of most-recent messages always kept inline.
	// Defaults to 5.
	HotTailSize int `json:"hot_tail_size" yaml:"hot_tail_size"`
	// TokenThreshold is the minimum token count for a message to become a
	// micro-compaction candidate. Defaults to 1000.
	TokenThreshold int `json:"token_threshold" yaml:"token_threshold"`
	// StorageDir is the directory where compacted payloads are persisted.
	// Defaults to ~/.flowstate/compacted (tilde expanded at load time).
	StorageDir string `json:"storage_dir" yaml:"storage_dir"`
	// PlaceholderTokens is the token budget assumed for a pointer placeholder
	// when computing the compacted window size. Defaults to 50.
	PlaceholderTokens int `json:"placeholder_tokens" yaml:"placeholder_tokens"`
}

// AutoCompactionConfig controls Layer 2 — summary-driven window compaction.
//
// The summariser is resolved via the CategoryResolver using the agent's
// summary_tier in a later phase; this struct only owns the enablement flag
// and the utilisation threshold that triggers compaction.
type AutoCompactionConfig struct {
	// Enabled toggles Layer 2 on or off. Defaults to false.
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Threshold is the fraction of the model's context window at which the
	// auto-compaction layer fires. Defaults to 0.75 to match the existing
	// agent manifest default (internal/agent/manifest.go CompactionThreshold).
	Threshold float64 `json:"threshold" yaml:"threshold"`
}

// SessionMemoryConfig controls Layer 3 — cross-session persistent memory.
//
// When enabled, salient decisions and patterns are promoted into a
// session-memory store under StorageDir for retrieval by future sessions.
type SessionMemoryConfig struct {
	// Enabled toggles Layer 3 on or off. Defaults to false.
	Enabled bool `json:"enabled" yaml:"enabled"`
	// StorageDir is the directory where session-memory artefacts are
	// persisted. Defaults to ~/.flowstate/session-memory (tilde expanded
	// at load time).
	StorageDir string `json:"storage_dir" yaml:"storage_dir"`
}

// CompressionMetrics is a per-WindowBuilder counter set that tracks the
// compression layers' activity across the builder's lifetime. The
// counters are intended for observability only — correctness of
// compression does not depend on them — so they are exposed as plain
// int fields rather than atomic types. Callers that share a builder
// across goroutines must synchronise externally, matching the rest of
// the WindowBuilder API.
//
// The four counters are the plan T19 contract:
//
//   - MicroCompactionCount: incremented once per cold message written
//     by HotColdSplitter.Split.
//   - AutoCompactionCount:  incremented once per successful
//     AutoCompactor.Compact call.
//   - TokensSaved:          running total of OriginalTokenCount -
//     SummaryTokenCount across successful auto-compactions.
//   - CacheHits:            reserved for a future summary-view cache
//     (ADR - View-Only Context Compaction §3) and currently left at
//     zero.
type CompressionMetrics struct {
	// MicroCompactionCount counts L1 cold-message offloads.
	MicroCompactionCount int
	// AutoCompactionCount counts successful L2 summary productions.
	AutoCompactionCount int
	// TokensSaved is the running total of tokens eliminated by L2.
	TokensSaved int
	// CacheHits is reserved for a compacted-view cache; zero today.
	CacheHits int
}

// DefaultCompressionConfig returns a CompressionConfig with every layer
// disabled and all numeric/path fields populated to safe defaults.
//
// This constructor is the single source of truth for compression defaults.
// Callers that wish to alter behaviour should start from this value and
// override individual fields rather than constructing the struct directly.
//
// Defaults:
//   - MicroCompaction.Enabled:           false
//   - MicroCompaction.HotTailSize:       5
//   - MicroCompaction.TokenThreshold:    1000
//   - MicroCompaction.StorageDir:        ~/.flowstate/compacted
//   - MicroCompaction.PlaceholderTokens: 50
//   - AutoCompaction.Enabled:            false
//   - AutoCompaction.Threshold:          0.75
//   - SessionMemory.Enabled:             false
//   - SessionMemory.StorageDir:          ~/.flowstate/session-memory
//
// Returns:
//   - A CompressionConfig populated with the safe-default values described above.
//
// Side effects:
//   - None.
func DefaultCompressionConfig() CompressionConfig {
	return CompressionConfig{
		MicroCompaction: MicroCompactionConfig{
			Enabled:           false,
			HotTailSize:       5,
			TokenThreshold:    1000,
			StorageDir:        "~/.flowstate/compacted",
			PlaceholderTokens: 50,
		},
		AutoCompaction: AutoCompactionConfig{
			Enabled:   false,
			Threshold: 0.75,
		},
		SessionMemory: SessionMemoryConfig{
			Enabled:    false,
			StorageDir: "~/.flowstate/session-memory",
		},
	}
}
