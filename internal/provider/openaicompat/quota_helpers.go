// Package openaicompat — quota_helpers.go
//
// Shared quota helpers for the four openaicompat-routed providers
// (openai, openzen, zai, ollamacloud) whose live RateLimit Snapshots
// share the same two-window (requests + tokens) shape from the
// openai-compat header dialect. Each per-provider quota.go owns its
// own observer-bound *Quota struct (mirroring anthropic/quota.go), but
// the parse-then-fold helpers are shared here so the 4 adapters
// cannot drift on tightest-window arithmetic or sentinel handling.
//
// Per the Provider Quota and Spend Visibility plan (May 2026) PR3 row
// 427 and per-provider matrix lines 144-148.

package openaicompat

import (
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/quota"
)

// RateLimitToVariant adapts the legacy *provider.RateLimit shape into
// the new quota.RateLimitVariant the chip consumes. The openai-compat
// dialect surfaces only the Requests and Tokens windows (vs Anthropic
// which surfaces four — input/output/requests/tokens); the Input and
// Output windows stay at their zero-value Window so the chip can render
// "n/a" rather than confusing -1 sentinels.
//
// Returns nil when the input RateLimit is nil so callers can chain
// directly from ExtractRateLimitHeadersFromResponse.
//
// Expected:
//   - rl may be nil.
//
// Returns:
//   - A populated *quota.RateLimitVariant with TightestPercentRemaining
//     + TightestResetAt computed across non-sentinel windows.
//   - nil when rl is nil.
//
// Side effects:
//   - None.
func RateLimitToVariant(rl *provider.RateLimit) *quota.RateLimitVariant {
	if rl == nil {
		return nil
	}
	v := &quota.RateLimitVariant{
		Requests: quota.Window{
			Limit:     rl.RequestsLimit,
			Remaining: rl.RequestsRemaining,
			Reset:     rl.RequestsReset,
		},
		Tokens: quota.Window{
			Limit:     rl.TokensLimit,
			Remaining: rl.TokensRemaining,
			Reset:     rl.TokensReset,
		},
		// Input / Output windows stay zero-valued — openai-compat
		// dialect doesn't surface them. Chip renders the populated
		// windows only.
		TightestPercentRemaining: -1,
	}
	v.TightestPercentRemaining, v.TightestResetAt = TightestWindow(v)
	return v
}

// TightestWindow walks the four Window triples of v and returns the
// minimum (Remaining/Limit*100) across windows that have BOTH Limit
// and Remaining populated (non--1, non-zero limit). The matching Reset
// is returned alongside.
//
// Returns (-1, zero time) when no window has both signals.
//
// Mirrors anthropic/quota.go:tightestWindow at lines 183-208 so the
// chip's tightest-window calc behaves identically across providers.
//
// Expected:
//   - v is non-nil.
//
// Returns:
//   - The tightest percent-remaining and the matching reset time.
//
// Side effects:
//   - None.
func TightestWindow(v *quota.RateLimitVariant) (int, time.Time) {
	type windowPct struct {
		pct   int
		reset time.Time
	}
	windows := []quota.Window{v.Requests, v.Tokens, v.Input, v.Output}

	pcts := make([]windowPct, 0, len(windows))
	for _, w := range windows {
		if w.Limit <= 0 || w.Remaining < 0 {
			continue
		}
		pct := int(int64(w.Remaining) * 100 / int64(w.Limit))
		pcts = append(pcts, windowPct{pct: pct, reset: w.Reset})
	}
	if len(pcts) == 0 {
		return -1, time.Time{}
	}
	tightest := pcts[0]
	for _, wp := range pcts[1:] {
		if wp.pct < tightest.pct {
			tightest = wp
		}
	}
	return tightest.pct, tightest.reset
}
