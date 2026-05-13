package openai

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
	"github.com/baphled/flowstate/internal/provider/quota"
)

// Quota is the OpenAI-side quota.Quota adapter. It owns a live
// RateLimit Snapshot fed by the Provider's success-path response
// observer (registered via Provider.SetResponseObserver) and
// surfaces it back through Remaining for the Tracker to fan out.
//
// Per the Provider Quota and Spend Visibility plan (May 2026), PR3
// row 427 + per-provider matrix line 144: OpenAI was blocked on the
// openaicompat success-path lift, which PR3 closes. The chip now
// surfaces RateLimit live on every 2xx response.
//
// Mirrors the anthropic.Quota shape (anthropic/quota.go from PR1) so
// the chip code can render either provider through the same tagged-
// union without branching.
//
// Concurrency: RecordResponse / Remaining are safe under -race; the
// snapshot is guarded by an internal RWMutex.
type Quota struct {
	mu          sync.RWMutex
	snap        quota.Snapshot
	accountHash string
}

// NewQuota constructs an OpenAI quota adapter bound to the given
// account hash. The account hash partitions snapshots across rotated
// keys; pass quota.HashAccount(apiKey) at boot.
//
// The adapter starts with a NotConfigured Snapshot so Remaining
// returns IsValid()==true before the first response flows. The
// first success-path response from Chat or Stream flips the variant
// to RateLimit.
func NewQuota(accountHash string) *Quota {
	return &Quota{
		accountHash: accountHash,
		snap: quota.Snapshot{
			Provider:    "openai",
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
// with the response headers; the adapter parses the documented
// openai-compat rate-limit headers and refreshes the live Snapshot.
//
// Per plan §"Where we are today" lines 113-114: prior to PR3,
// RateLimit was populated only on Error (openaicompat.go inside
// ParseProviderError). Bind closes that gap by hooking the Provider's
// success-path observer to the adapter's RecordResponse path.
//
// Idempotent: calling Bind a second time replaces the observer (the
// engine's reconfigure path may rewire).
func (q *Quota) Bind(p *Provider) {
	if p == nil {
		return
	}
	p.SetResponseObserver(func(headers http.Header) {
		// Success-path observer: pass an empty provider.Usage. The
		// streaming usage chunks already flow into the engine through
		// the existing context_usage path (via emitStreamUsage at
		// openaicompat.go:456); we don't double-count here. The model
		// id is unknown at observer-call time — the response carries
		// the model in the JSON body which the SDK has parsed, but
		// the headers alone don't. Pass empty modelID; the live
		// RateLimit is account-wide rather than per-model for openai
		// (per-key rate windows, not per-model).
		q.RecordResponse("openai", "", headers, provider.Usage{})
	})
}

// Remaining returns the current Snapshot for (providerID, modelID).
// modelID is currently ignored — openai's rate-limit windows are
// account-wide, not per-model.
//
// The returned Snapshot satisfies quota.Snapshot.IsValid() == true:
// it carries either a RateLimit variant (after the first successful
// response) or a NotConfigured variant (before the first response,
// with Reason "awaiting-first-response").
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
// engine via the Tracker after every response, and by the Provider's
// success-path observer directly (via Bind).
//
// usage is currently unused in PR3 — spend math lands in PR4.
//
// Concurrent-safe: holds the write lock for the duration of the
// snap replacement; readers see either the prior or new snapshot,
// never a torn read.
func (q *Quota) RecordResponse(_, modelID string, headers http.Header, _ provider.Usage) {
	rl := openaicompat.ExtractRateLimitHeadersFromResponse(headers)
	if rl == nil {
		// Headers carried no rate-limit signal — keep the prior
		// Snapshot rather than blanking. Same "no signal, no
		// regression" stance the error path takes.
		return
	}

	variant := openaicompat.RateLimitToVariant(rl)

	q.mu.Lock()
	defer q.mu.Unlock()
	q.snap = quota.Snapshot{
		Provider:    "openai",
		AccountHash: q.accountHash,
		Model:       modelID,
		ObservedAt:  time.Now(),
		RateLimit:   variant,
	}
}

// Compile-time conformance — catches the moment Quota falls behind
// the quota.Quota interface (e.g. a future RecordResponse signature
// change).
var _ quota.Quota = (*Quota)(nil)
