package anthropic

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/quota"
)

// Quota is the Anthropic-side quota.Quota adapter. It owns a live
// RateLimit Snapshot fed by the Provider's success-path response
// observer (registered via Provider.SetResponseObserver) and
// surfaces it back through Remaining for the Tracker to fan out.
//
// Per the Provider Quota and Spend Visibility plan (May 2026), PR1
// row 425 + per-provider matrix line 143: Anthropic is the only
// provider that ships with a populated RateLimitVariant in PR1.
// The five openaicompat providers ship NotConfigured{Reason:
// "awaiting-pr3"} and Copilot ships NotConfigured{Reason:"
// subscription-only"} permanently.
//
// Concurrency: RecordResponse / Remaining are safe under -race;
// the snapshot is guarded by an internal RWMutex.
type Quota struct {
	mu          sync.RWMutex
	snap        quota.Snapshot
	accountHash string
}

// NewQuota constructs an Anthropic quota adapter bound to the given
// account hash. The account hash partitions snapshots across rotated
// keys; pass quota.HashAccount(apiKey) at boot.
//
// The adapter starts with a NotConfigured Snapshot so Remaining
// returns IsValid()==true before the first response flows. The
// first success-path response from Chat or streamMessages flips the
// variant to RateLimit.
func NewQuota(accountHash string) *Quota {
	return &Quota{
		accountHash: accountHash,
		snap: quota.Snapshot{
			Provider:    providerName,
			AccountHash: accountHash,
			ObservedAt:  time.Now(),
			NotConfigured: &quota.NotConfiguredVariant{
				Reason: "awaiting-first-response",
			},
		},
	}
}

// Bind wires the adapter into a Provider's success-path response
// observer. The Provider invokes the observer on every 2xx response
// with the response headers; the adapter parses the 14 documented
// Anthropic rate-limit headers and refreshes the live Snapshot.
//
// Plan §"Where we are today" lines 113-114: today RateLimit is only
// populated on Error (anthropic.go inside buildProviderError). Bind
// closes that gap by hooking the Provider's success-path observer
// to the adapter's RecordResponse path.
//
// Idempotent: calling Bind a second time replaces the observer (the
// engine's reconfigure path may rewire).
func (q *Quota) Bind(p *Provider) {
	if p == nil {
		return
	}
	p.SetResponseObserver(func(headers http.Header) {
		// Success-path observer: pass an empty provider.Usage (the
		// streaming usage chunks already flow into the engine through
		// the existing context_usage path; we don't double-count
		// here). The model id is unknown at observer-call time —
		// the response itself carries the model in the JSON body
		// which the SDK has already parsed, but the headers alone
		// don't. Pass empty modelID; the live RateLimit is account-
		// wide rather than per-model for Anthropic (the rate-limit
		// windows are per-key, not per-model).
		q.RecordResponse(providerName, "", headers, provider.Usage{})
	})
}

// Remaining returns the current Snapshot for (providerID, modelID).
// modelID is currently ignored — Anthropic's rate-limit windows are
// account-wide, not per-model; a future revision may key per-model
// if Anthropic publishes per-model headers.
//
// The returned Snapshot satisfies quota.Snapshot.IsValid() ==
// true: it carries either a RateLimit variant (after the first
// successful response) or a NotConfigured variant (before the
// first response, with Reason "awaiting-first-response").
func (q *Quota) Remaining(_ context.Context, _, modelID string) (quota.Snapshot, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	snap := q.snap
	// Stamp the requested modelID into the returned Snapshot so the
	// caller's audit trail has it; the adapter's internal snap stays
	// modelID-agnostic.
	snap.Model = modelID
	// Staleness: RateLimit Snapshots are stale-on-read past
	// TightestResetAt per plan A2 fold (lines 396-397).
	if snap.RateLimit != nil &&
		!snap.RateLimit.TightestResetAt.IsZero() &&
		time.Now().After(snap.RateLimit.TightestResetAt) {
		snap.Stale = true
	}
	return snap, nil
}

// RecordResponse parses the response headers into a RateLimit
// Snapshot and replaces the adapter's cached snap. Called by the
// engine via the Tracker after every response, and by the
// Provider's success-path observer directly (via Bind).
//
// usage is currently unused in PR1 — spend math lands in PR4.
//
// Concurrent-safe: holds the write lock for the duration of the
// snap replacement; readers see either the prior or new snapshot,
// never a torn read.
func (q *Quota) RecordResponse(_, modelID string, headers http.Header, _ provider.Usage) {
	rl := extractRateLimitHeadersFromResponse(headers, "")
	if rl == nil {
		// Headers carried no rate-limit signal — keep the prior
		// Snapshot rather than blanking. This is the same "no
		// signal, no regression" stance the error-path takes (an
		// unparseable retry-after leaves RateLimit nil — see
		// anthropic_test.go:904-913).
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.snap = quota.Snapshot{
		Provider:     providerName,
		AccountHash:  q.accountHash,
		Model:        modelID,
		ObservedAt:   time.Now(),
		RateLimit:    rateLimitToVariant(rl),
	}
}

// rateLimitToVariant adapts the legacy *provider.RateLimit shape at
// types.go:430-468 into the new quota.RateLimitVariant. The legacy
// shape has eleven flat fields; the variant has four Window structs
// + a tightest-percentage summary. The adapter also computes the
// TightestPercentRemaining the chip renders.
func rateLimitToVariant(rl *provider.RateLimit) *quota.RateLimitVariant {
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
		Input: quota.Window{
			Limit:     rl.InputTokensLimit,
			Remaining: rl.InputTokensRemaining,
			Reset:     rl.InputTokensReset,
		},
		Output: quota.Window{
			Limit:     rl.OutputTokensLimit,
			Remaining: rl.OutputTokensRemaining,
			Reset:     rl.OutputTokensReset,
		},
		TightestPercentRemaining: -1,
	}
	v.TightestPercentRemaining, v.TightestResetAt = tightestWindow(v)
	return v
}

// tightestWindow walks the four Window triples and returns the
// minimum (Remaining/Limit*100) across windows that have BOTH Limit
// and Remaining populated (non--1). The matching Reset is returned
// alongside.
//
// Returns (-1, zero time) when no window has both signals.
func tightestWindow(v *quota.RateLimitVariant) (int, time.Time) {
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

// Compile-time conformance — catches the moment Quota falls behind
// the quota.Quota interface (e.g. a future RecordResponse signature
// change).
var _ quota.Quota = (*Quota)(nil)
