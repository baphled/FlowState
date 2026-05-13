package quota

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// PricingResolver is the narrow seam Tracker depends on for the
// three-tier pricing-table lookup (PR2). The actual implementation
// lives at internal/provider/quota/pricing.Resolver — the seam exists
// here so the quota package does not import pricing (and the test
// suite can pass a fake resolver without standing up the full
// embedded JSON).
//
// The PR2 contract:
//   - Lookup returns (source, true) when ANY tier has the
//     (provider, model) key. source is the Snapshot.PricingSource
//     string the panel surfaces verbatim.
//   - Lookup returns ("", false) when no tier has the key. The
//     Tracker's Lookup caller (per-provider adapter in PR4) then
//     surfaces NotConfigured{Reason:"unknown-model:<id>"} per plan
//     §"Pricing table" line 388.
//
// Plan §"Pricing table (OD-1 resolution)" lines 338-388.
type PricingResolver interface {
	Lookup(provider, model string) (source string, ok bool)
}

// Tracker is the central in-memory accumulator. The engine constructs
// one per session-server (NewEngine wires it in PR4); it subscribes to
// provider responses via the Quota interface and persists the latest
// observed Snapshot per (provider, account_hash, model) tuple via the
// configured Store.
//
// PR1 scope (plan §"Engine integration / spend accumulation rules"
// lines 299-318): the Tracker records the latest observed Snapshot
// from a registered per-provider adapter and serves it back via
// Lookup. Spend math is NOT in scope for PR1 — that's PR4. The
// PR1 Tracker exists so the engine has a callable seam and PR1
// Anthropic responses produce a non-stale Snapshot the chip can read.
//
// PR2 addition (plan §"Pricing table" lines 338-388): Tracker
// optionally carries a PricingResolver. When wired, Lookup stamps
// Snapshot.PricingSource with the resolver's audit-trail string so
// downstream consumers (the SSE writer, the deep-view panel) see
// which tier supplied the price. The resolver is OPTIONAL — a nil
// resolver leaves PricingSource empty, matching PR1 behaviour.
//
// Concurrency: RecordResponse and Lookup are safe for concurrent use
// across goroutines. The Tracker uses a sync.RWMutex internally; per-
// adapter calls go through the adapter's own concurrency contract.
type Tracker struct {
	mu              sync.RWMutex
	adapters        map[string]Quota // providerID → Quota adapter
	storeBackend    string           // surfaced into Snapshot.StoreBackend
	pricingResolver PricingResolver  // PR2: optional; nil leaves PricingSource empty

	// spend is the PR4 spend-accumulator extension. Non-nil when
	// constructed via NewTrackerWithSpend; nil for the PR1/PR3
	// NewTracker / NewTrackerWithPricing paths. The Tracker's spend
	// methods short-circuit cleanly when spend == nil so callers can
	// keep the legacy constructors during incremental rollouts.
	//
	// See spend.go (RecordSpend, NewTrackerWithSpend) for the full
	// PR4 surface.
	spend *spendState
}

// NewTracker constructs an empty Tracker. The storeBackend string
// ("memory" | "redis" | "postgres") is surfaced into every Snapshot
// the Tracker emits so the chip's tooltip can render the
// single-instance-scope disclosure (plan B3 fold, line 174).
//
// No pricing resolver is wired — Snapshot.PricingSource will remain
// empty on every Lookup. Callers wanting the PR2 three-tier pricing
// audit trail use NewTrackerWithPricing.
func NewTracker(storeBackend string) *Tracker {
	return &Tracker{
		adapters:     make(map[string]Quota),
		storeBackend: storeBackend,
	}
}

// NewTrackerWithPricing constructs a Tracker bound to the given
// PricingResolver. The resolver MAY be nil — in which case behaviour
// matches NewTracker (no PricingSource stamping). Non-nil resolver
// stamps Snapshot.PricingSource on every Lookup return when the
// resolver has a hit for (provider, model).
//
// The Tracker does NOT take ownership of the resolver — callers
// remain free to hot-swap pricing tables via pricing.Resolver's
// SetRegistry method without touching the Tracker.
//
// Plan §"Pricing table" lines 338-388 (PR2 plumbing).
func NewTrackerWithPricing(storeBackend string, resolver PricingResolver) *Tracker {
	return &Tracker{
		adapters:        make(map[string]Quota),
		storeBackend:    storeBackend,
		pricingResolver: resolver,
	}
}

// Register binds a per-provider Quota adapter under the given
// providerID. Re-registering the same providerID overwrites the prior
// adapter. The engine calls this once per configured provider at boot.
func (t *Tracker) Register(providerID string, adapter Quota) {
	if adapter == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.adapters[providerID] = adapter
}

// Lookup returns the current Snapshot for (providerID, accountHash,
// modelID) by delegating to the registered adapter. Returns a
// NotConfigured Snapshot when no adapter is registered for providerID
// — this is the v1 fallback for unknown providers (e.g. a future
// provider not yet wired into the per-provider matrix).
//
// PR5 R2 fold: the accountHash parameter threads through to the
// Store-overlay key so multi-account-per-provider deployments do not
// silently merge into a single bucket. Pre-PR5 the storeKey shim
// ignored the Tracker and produced an empty-account key, collapsing
// every account on a provider into one Snapshot row. Callers that
// don't care about account scoping (single-account-per-provider
// deployments, tests) pass "" — the v1 default that matches the
// RecordSpend write path with empty AccountHash.
//
// The returned Snapshot is stamped with t.storeBackend so consumers
// don't need to thread it separately. When a PricingResolver is
// wired (PR2 plumbing), Snapshot.PricingSource is also stamped with
// the resolver's audit-trail string for hits on (providerID, modelID).
func (t *Tracker) Lookup(ctx context.Context, providerID, accountHash, modelID string) (Snapshot, error) {
	// PR4 overlay: when spend is wired AND the Store has a Snapshot
	// for this key, prefer it. The TokenSpend variant carries the
	// figure the chip is most likely to act on (operator wants to
	// see "what have I spent"); the RateLimit variant from the
	// adapter only matters when no spend has been recorded. This
	// preserves the discriminator-union invariant — Lookup returns
	// EXACTLY one variant pointer non-nil.
	//
	// Auto-reset on PeriodStart rollover (OD-8 — plan lines 511-516):
	// when the stored Snapshot's PeriodEnd has passed, rotate to the
	// next period and zero Spent before returning. The Tracker writes
	// the rotated Snapshot back to the Store so subsequent reads in
	// the new period start clean.
	if t != nil && t.spend != nil {
		key := SpendStoreKey{
			ProviderID:  providerID,
			AccountHash: accountHash,
			ModelID:     modelID,
		}
		if snap, ok := t.lookupSpendOverlay(ctx, providerID, modelID, key); ok {
			return snap, nil
		}
	}

	t.mu.RLock()
	adapter, ok := t.adapters[providerID]
	resolver := t.pricingResolver
	t.mu.RUnlock()
	if !ok {
		return Snapshot{
			Provider:      providerID,
			AccountHash:   accountHash,
			Model:         modelID,
			ObservedAt:    time.Now(),
			StoreBackend:  t.storeBackend,
			NotConfigured: &NotConfiguredVariant{Reason: "no-adapter-registered"},
		}, nil
	}
	snap, err := adapter.Remaining(ctx, providerID, modelID)
	if err != nil {
		return Snapshot{}, err
	}
	// Stamp the store-backend into the snapshot so the chip tooltip
	// can disclose single-instance scope. Adapters need not know the
	// backend.
	snap.StoreBackend = t.storeBackend
	// Stamp the account hash post-Remaining when the adapter left it
	// empty so the chip's partition key is always populated when the
	// caller supplied one. R2 fold: previously the empty-account
	// collapse meant a multi-account caller couldn't drill into a
	// specific account from the dashboard.
	if snap.AccountHash == "" && accountHash != "" {
		snap.AccountHash = accountHash
	}
	// PR2: stamp PricingSource when a resolver is wired and the
	// (provider, model) tuple resolves. Adapters do not need to know
	// about pricing tiers — the Tracker is the single source of
	// truth for the audit-trail string. The override-to-
	// NotConfigured{unknown-model} surfacing happens in PR4 when
	// TokenSpend goes live; PR2 only plumbs the source string.
	if resolver != nil && providerID != "" && modelID != "" {
		if source, ok := resolver.Lookup(providerID, modelID); ok {
			snap.PricingSource = source
		}
	}
	return snap, nil
}

// PricingResolver returns the resolver wired into the Tracker, or nil
// when constructed via NewTracker (PR1 path). Exposed for tests and
// for the engine boundary so PR4 adapters can perform the per-model
// pricing lookup at RecordResponse time.
func (t *Tracker) PricingResolver() PricingResolver {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.pricingResolver
}

// RecordResponse fans out a provider response to the registered
// adapter. The engine calls this from the streaming pipe after every
// chunk that carries Usage data, and from the chat pipe after every
// non-stream response.
//
// No-op when no adapter is registered for providerID — the engine
// must not crash because a future provider isn't wired in yet.
func (t *Tracker) RecordResponse(providerID, modelID string, headers http.Header, usage provider.Usage) {
	t.mu.RLock()
	adapter, ok := t.adapters[providerID]
	t.mu.RUnlock()
	if !ok {
		return
	}
	adapter.RecordResponse(providerID, modelID, headers, usage)
}

// StoreBackend returns the backend label the Tracker stamps into
// every Snapshot. Exposed for tests and for the boot-validation
// audit trail.
func (t *Tracker) StoreBackend() string {
	return t.storeBackend
}
