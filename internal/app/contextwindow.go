package app

import (
	"fmt"

	"github.com/baphled/flowstate/internal/agent"
)

// windowTable maps "provider/model" to the model's context window in tokens
// (ADR-010, D3). SOURCE: contextWindow of the anthropic and zai providers in
// Pi's model registry, @earendil-works/pi-ai dist/models.generated.js as
// shipped with pi-coding-agent on 2026-11-24. Matching is on the exact model id
// only: a near miss (a provider prefix, a date suffix not listed here) is
// unknown on purpose, since a wrong guess compacts constantly or never.
// ollama and openrouter are absent on purpose: a local model's window is
// whatever the user loaded it with. The provider name is the one stored on the
// session row; "zai" is normalised to "z.ai" by tableKey.
var windowTable = map[string]int{
	"anthropic/claude-fable-5":             1000000,
	"anthropic/claude-fable-5-1":           1000000,
	"anthropic/claude-fable-5-2":           1000000,
	"anthropic/claude-haiku-4-5":           200000,
	"anthropic/claude-haiku-4-5-20251001":  200000,
	"anthropic/claude-haiku-5-5":           1000000,
	"anthropic/claude-opus-4-5":            200000,
	"anthropic/claude-opus-4-5-20251101":   200000,
	"anthropic/claude-opus-4-6":            1000000,
	"anthropic/claude-opus-4-7":            1000000,
	"anthropic/claude-opus-4-8":            1000000,
	"anthropic/claude-opus-5":              1000000,
	"anthropic/claude-opus-5-5":            1000000,
	"anthropic/claude-sonnet-4-5":          200000,
	"anthropic/claude-sonnet-4-5-20250929": 200000,
	"anthropic/claude-sonnet-4-6":          1000000,
	"anthropic/claude-sonnet-5":            1000000,
	"anthropic/claude-sonnet-5-5":          1000000,
	"z.ai/glm-4.7":                         204800,
	"z.ai/glm-5-turbo":                     200000,
	"z.ai/glm-5.2":                         1000000,
	"z.ai/glm-5.2-highspeed":               1000000,
	"z.ai/glm-5.3":                         1000000,
	"z.ai/glm-5.3-flash":                   1000000,
	"z.ai/glm-5.3-highspeed":               1000000,
}

// outputTable maps "provider/model" to the model's maximum output tokens, the
// maxTokens of the same registry entries (same SOURCE, date and exact-id rule
// as windowTable). It bounds the default and the escalation of max_tokens.
var outputTable = map[string]int{
	"anthropic/claude-fable-5":             128000,
	"anthropic/claude-fable-5-1":           128000,
	"anthropic/claude-fable-5-2":           128000,
	"anthropic/claude-haiku-4-5":           64000,
	"anthropic/claude-haiku-4-5-20251001":  64000,
	"anthropic/claude-haiku-5-5":           128000,
	"anthropic/claude-opus-4-5":            64000,
	"anthropic/claude-opus-4-5-20251101":   64000,
	"anthropic/claude-opus-4-6":            128000,
	"anthropic/claude-opus-4-7":            128000,
	"anthropic/claude-opus-4-8":            128000,
	"anthropic/claude-opus-5":              128000,
	"anthropic/claude-opus-5-5":            128000,
	"anthropic/claude-sonnet-4-5":          64000,
	"anthropic/claude-sonnet-4-5-20250929": 64000,
	"anthropic/claude-sonnet-4-6":          128000,
	"anthropic/claude-sonnet-5":            128000,
	"anthropic/claude-sonnet-5-5":          128000,
	"z.ai/glm-4.7":                         131072,
	"z.ai/glm-5-turbo":                     131072,
	"z.ai/glm-5.2":                         131072,
	"z.ai/glm-5.2-highspeed":               131072,
	"z.ai/glm-5.3":                         131072,
	"z.ai/glm-5.3-flash":                   131072,
	"z.ai/glm-5.3-highspeed":               131072,
}

// ResolveMaxOutput returns the max_tokens a run sends: the explicit
// --max-tokens or config value when positive, else the model's table maximum
// capped at DefaultOutputCap, else (an unknown model) DefaultMaxOutput. The
// compaction line (window - max(reserve, max_tokens)) follows this value, so
// a table model's line sits DefaultOutputCap below its window at most.
func ResolveMaxOutput(providerName, model string, explicit int64) int64 {
	if explicit > 0 {
		return explicit
	}
	if capped, ok := tableMaxOutput(providerName, model); ok {
		return capped
	}
	return DefaultMaxOutput
}

// tableMaxOutput is the table maximum of a known model capped at
// DefaultOutputCap; ok is false for a model the table lacks.
func tableMaxOutput(providerName, model string) (capped int64, ok bool) {
	limit, ok := outputTable[tableKey(providerName, model)]
	return int64(min(limit, DefaultOutputCap)), ok
}

// tableKey builds the windowTable key, accepting the "zai" alias the provider
// registry accepts for "z.ai".
func tableKey(providerName, model string) string {
	if providerName == "zai" {
		providerName = "z.ai"
	}
	return providerName + "/" + model
}

// Resolved applies the defaults: enabled true, reserve DefaultCompactionReserve,
// keep_recent DefaultCompactionKeepRecent, and no configured window (0). It is
// the one place the defaults live.
func (c CompactionConfig) Resolved() CompactionSettings {
	s := CompactionSettings{Enabled: true, Reserve: DefaultCompactionReserve, KeepRecent: DefaultCompactionKeepRecent}
	if c.Enabled != nil {
		s.Enabled = *c.Enabled
	}
	if c.Reserve != nil {
		s.Reserve = *c.Reserve
	}
	if c.KeepRecent != nil {
		s.KeepRecent = *c.KeepRecent
	}
	if c.ContextWindow != nil {
		s.ContextWindow = *c.ContextWindow
	}
	return s
}

// Validate checks the set fields: reserve and keep_recent are at least 1 and
// context_window is 1..agent.MaxContextWindow. A failure is config_invalid
// and names the offending config key.
func (c CompactionConfig) Validate() error {
	for _, f := range []struct {
		key   string
		value *int
		max   int
	}{
		{"compaction.reserve", c.Reserve, 0},
		{"compaction.keep_recent", c.KeepRecent, 0},
		{"compaction.context_window", c.ContextWindow, agent.MaxContextWindow},
	} {
		if f.value == nil {
			continue
		}
		if *f.value < 1 || (f.max > 0 && *f.value > f.max) {
			limit := ""
			if f.max > 0 {
				limit = fmt.Sprintf(" (at most %d)", f.max)
			}
			return fmt.Errorf("%w: %s must be a positive number of tokens%s, got %d",
				ErrConfigInvalid, f.key, limit, *f.value)
		}
	}
	return nil
}

// ResolveContextWindow returns the context window of a session on
// provider/model and where it came from, in the order ADR-010 (D3) gives: the
// profile's context_window when positive (a session under a profile), else the
// config's compaction.context_window, else the exact provider/model entry of
// the registry table. With none of them it returns 0 and WindowSourceUnknown:
// there is no default window, and an unknown window disables automatic
// compaction.
func ResolveContextWindow(providerName, model string, profile int, cfg CompactionConfig) (tokens int, source string) {
	if profile > 0 {
		return profile, WindowSourceProfile
	}
	if cfg.ContextWindow != nil && *cfg.ContextWindow > 0 {
		return *cfg.ContextWindow, WindowSourceConfig
	}
	if window, ok := windowTable[tableKey(providerName, model)]; ok {
		return window, WindowSourceTable
	}
	return 0, WindowSourceUnknown
}
