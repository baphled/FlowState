package context

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

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
	// Model is the chat model the knowledge extractor uses when issuing
	// distillation calls to the underlying provider. Mandatory when
	// Enabled is true — Ollama and the OpenAI-compatible backends
	// (github-copilot, openzen, ollama-openai-compat, zai) all reject
	// an empty `model` with HTTP 400, and the previous provider-default
	// fallback in internal/app/app.go only covered a subset of
	// providers, leaving custom providers silently broken. Making the
	// model explicit at config load surfaces the misconfiguration at
	// startup with a clear message rather than hiding it inside a
	// background extraction goroutine's 30-second timeout.
	Model string `json:"model" yaml:"model"`
	// WaitTimeout bounds the pre-exit block for in-flight L3
	// knowledge-extraction goroutines on `flowstate run`. The extractor
	// itself runs under a 30-second LLM timeout; the CLI gives it
	// matching headroom plus a small margin for the final atomic disk
	// write. Defaults to 35 seconds. Must be > 0 when Enabled is true.
	WaitTimeout time.Duration `json:"wait_timeout" yaml:"wait_timeout"`
}

// CompressionMetrics is a per-WindowBuilder counter set that tracks the
// compression layers' activity across the builder's lifetime. The
// counters are intended for observability only — correctness of
// compression does not depend on them — so they are exposed as plain
// int fields rather than atomic types. Callers that share a builder
// across goroutines must synchronise externally, matching the rest of
// the WindowBuilder API.
//
// The three enforced counters are the plan T19 contract:
//
//   - MicroCompactionCount: incremented once per cold message written
//     by HotColdSplitter.Split.
//   - AutoCompactionCount:  incremented once per successful
//     AutoCompactor.Compact call.
//   - TokensSaved:          running total of OriginalTokenCount -
//     SummaryTokenCount across successful auto-compactions.
//
// A CacheHits counter is deliberately absent. The governing ADR
// (View-Only Context Compaction §3, "Caching Is a Permitted Extension")
// classifies the compacted-view cache as an out-of-scope extension of
// this delivery. A zero-stub counter with no backing cache is worse
// than no counter at all because it emits misleading observability
// data. When the cache ships, the CacheHits field and its increment
// site must be introduced together.
type CompressionMetrics struct {
	// MicroCompactionCount counts L1 cold-message offloads.
	MicroCompactionCount int
	// AutoCompactionCount counts successful L2 summary productions.
	AutoCompactionCount int
	// TokensSaved is the running total of tokens eliminated by L2.
	TokensSaved int
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
			Enabled:     false,
			StorageDir:  "~/.flowstate/session-memory",
			WaitTimeout: 35 * time.Second,
		},
	}
}

// Validate returns an error when the CompressionConfig is internally
// inconsistent. It is the startup backstop for shapes that cannot
// function at runtime — the intent is to fail loud at load rather than
// produce silently wrong windows.
//
// Current rules:
//
//   - When MicroCompaction.Enabled is true, HotTailSize must be >= 1.
//     findColdBoundary uses `len(units) - HotTailSize` as the naive
//     cold boundary; with HotTailSize <= 0 every unit is cold, which
//     spills the entire conversation to disk on every turn. That is
//     never what the operator wanted — a zero hot tail only makes
//     sense when the feature is off.
//   - When AutoCompaction.Enabled is true, Threshold must be a finite
//     float in the (0.0, 1.0] interval. A value outside this range is
//     either inert (ratios never exceed it) or pathological (fires on
//     every turn). NaN is specifically rejected because Go's ordering
//     operators return false against NaN, so the ratio compare in
//     maybeAutoCompact would silently never trigger.
//
// Expected:
//   - Called from config loaders after defaults have been applied.
//
// Returns:
//   - nil when every rule holds.
//   - A diagnostic error naming the offending field and section when
//     a rule is violated.
//
// Side effects:
//   - None.
func (c CompressionConfig) Validate() error {
	if c.MicroCompaction.Enabled && c.MicroCompaction.HotTailSize < 1 {
		return fmt.Errorf(
			"compression: micro_compaction.hot_tail_size must be >= 1 "+
				"when micro_compaction.enabled is true (got %d); "+
				"a zero or negative hot tail makes every message cold "+
				"and defeats the purpose of the feature",
			c.MicroCompaction.HotTailSize,
		)
	}
	if c.AutoCompaction.Enabled {
		t := c.AutoCompaction.Threshold
		if math.IsNaN(t) {
			return errors.New(
				"compression: auto_compaction.threshold must be a finite " +
					"fraction in (0.0, 1.0] when auto_compaction.enabled is " +
					"true; got NaN, which never compares true and would " +
					"silently disable the layer")
		}
		if t <= 0.0 || t > 1.0 {
			return fmt.Errorf(
				"compression: auto_compaction.threshold must be in the "+
					"(0.0, 1.0] interval when auto_compaction.enabled is "+
					"true (got %v); values <= 0 never trigger, values > 1 "+
					"trigger every turn",
				t,
			)
		}
	}
	if c.SessionMemory.Enabled && strings.TrimSpace(c.SessionMemory.Model) == "" {
		return errors.New(
			"compression: session_memory.model must be a non-empty chat " +
				"model identifier when session_memory.enabled is true; " +
				"the knowledge extractor's chat request requires a " +
				"`model` field that Ollama and OpenAI-compatible backends " +
				"reject if empty")
	}
	if c.SessionMemory.Enabled && c.SessionMemory.WaitTimeout <= 0 {
		return fmt.Errorf(
			"compression: session_memory.wait_timeout must be > 0 when "+
				"session_memory.enabled is true (got %v); a zero or "+
				"negative value would orphan in-flight knowledge-"+
				"extraction goroutines at process exit, leaving partial "+
				"memory.json.tmp files on disk with no log signal",
			c.SessionMemory.WaitTimeout,
		)
	}
	return nil
}
