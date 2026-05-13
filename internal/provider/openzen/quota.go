package openzen

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
	"github.com/baphled/flowstate/internal/provider/quota"
)

// Quota is the OpenZen-side quota.Quota adapter. Mirrors
// openai.Quota — OpenZen routes through the openai-compat SDK so it
// inherits the same 6-header rate-limit dialect.
//
// Per Provider Quota and Spend Visibility plan (May 2026) PR3 row
// 427 + per-provider matrix line 145.
type Quota struct {
	mu          sync.RWMutex
	snap        quota.Snapshot
	accountHash string
}

// NewQuota constructs an OpenZen quota adapter — see openai.NewQuota.
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
// observer — see openai.Quota.Bind.
func (q *Quota) Bind(p *Provider) {
	if p == nil {
		return
	}
	p.SetResponseObserver(func(headers http.Header) {
		q.RecordResponse(providerName, "", headers, provider.Usage{})
	})
}

// Remaining returns the current Snapshot — see openai.Quota.Remaining.
func (q *Quota) Remaining(_ context.Context, _, modelID string) (quota.Snapshot, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	snap := q.snap
	snap.Model = modelID
	if snap.RateLimit != nil &&
		!snap.RateLimit.TightestResetAt.IsZero() &&
		time.Now().After(snap.RateLimit.TightestResetAt) {
		snap.Stale = true
	}
	return snap, nil
}

// RecordResponse — see openai.Quota.RecordResponse.
func (q *Quota) RecordResponse(_, modelID string, headers http.Header, _ provider.Usage) {
	rl := openaicompat.ExtractRateLimitHeadersFromResponse(headers)
	if rl == nil {
		return
	}
	variant := openaicompat.RateLimitToVariant(rl)

	q.mu.Lock()
	defer q.mu.Unlock()
	q.snap = quota.Snapshot{
		Provider:    providerName,
		AccountHash: q.accountHash,
		Model:       modelID,
		ObservedAt:  time.Now(),
		RateLimit:   variant,
	}
}

var _ quota.Quota = (*Quota)(nil)
