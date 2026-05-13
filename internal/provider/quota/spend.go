// Package quota — spend accumulator + TokenSpend emission for PR4 of
// the Provider Quota and Spend Visibility plan (May 2026).
//
// This file extends the Tracker with the engine-facing spend seam. The
// PR1-PR3 Tracker collected RateLimit/NotConfigured Snapshots from
// per-provider adapters via Register+RecordResponse; PR4 layers on:
//
//  1. PriceEntry / PriceEntryResolver — the richer seam the spend math
//     consumes (vs. PR2's source-only PricingResolver).
//  2. NewTrackerWithSpend — the engine constructor. Wires the
//     PricingResolver, a SpendStore, and a clock so spend snapshots
//     persist across requests and the auto-reset logic stays
//     deterministically testable.
//  3. RecordSpend(ctx, SpendRecord) — the engine-facing API. Called from
//     the streaming pipe after every UsageDelta chunk. Honours the
//     snapshot-not-increment rule (plan §"Engine integration / spend
//     accumulation rules" lines 304-313): cumulative output_tokens is
//     the HIGHEST seen for the (provider, model, request_id) tuple.
//  4. Auto-reset on PeriodStart rollover (OD-8 — plan lines 511-516):
//     monthly period boundaries computed in UTC; Lookup-side lazy
//     reset on read (no goroutine).
//  5. Lookup overlay — when spend is non-zero AND pricing resolved for
//     the key, the returned Snapshot carries the TokenSpend variant
//     instead of the adapter's RateLimit. Discriminator invariant
//     preserved (exactly one variant pointer non-nil).
//
// PR1/PR3 behaviour preserved verbatim: NewTracker / NewTrackerWithPricing
// still work; RecordResponse legacy headers-only path still no-ops when no
// adapter is registered; the contract test ladder still passes.
package quota

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// SpendStoreKey mirrors SpendStoreKey field-for-field. Defined locally to
// avoid the import cycle quota → quota/store (store already imports
// quota for the Snapshot type). The engine adapter at boot constructs
// a tiny shim that wraps a *store.MemoryStore so the Tracker's spend
// layer sees the narrow SpendStore interface without the package
// dependency.
type SpendStoreKey struct {
	ProviderID  string
	AccountHash string
	ModelID     string
}

// SpendStore is the narrow persistence seam the Tracker's spend
// layer consumes. Mirrors the Get/Put subset of SpendStore; Delete /
// Reset / Cleanup are not needed on the hot path so they're omitted
// (the engine wires the full Store separately for the PR5 "Reset
// spend counter" REST endpoint).
//
// Sentinel for "not found" — SpendStore implementations MUST return
// SpendStoreErrNotFound when Get is called for an unrecorded key.
// errors.Is comparison is the load-bearing check in RecordSpend.
type SpendStore interface {
	Get(ctx context.Context, key SpendStoreKey) (Snapshot, error)
	Put(ctx context.Context, key SpendStoreKey, snap Snapshot) error
}

// SpendStoreErrNotFound is the sentinel SpendStore implementations
// return when a key has no recorded Snapshot. RecordSpend treats it
// as the "first call" path rather than an error.
var SpendStoreErrNotFound = errors.New("quota: spend snapshot not found")

// PriceEntry is the per-model pricing record the spend math consumes.
// Mirrors pricing.Entry (the package's own struct) field-for-field so
// the engine can adapt a pricing.Resolver into PriceEntryResolver with
// a trivial closure without leaking pricing.Entry into the quota
// package (the narrow-seam discipline PricingResolver established).
//
// Per-million figures stay in major units of Currency (e.g. USD 15.00
// means $15.00 per million tokens). The spend math converts to minor
// units (Money.Amount) inside RecordSpend.
//
// Plan §"Pricing table" lines 88-109 (pricing.Entry source of truth).
type PriceEntry struct {
	Currency                string  // ISO-4217; empty falls back to PriceEntryResolver default
	InputPerMillion         float64 // major units per 1M input tokens
	OutputPerMillion        float64 // major units per 1M output tokens
	CacheReadPerMillion     float64 // optional; zero falls back to InputPerMillion
	CacheCreationPerMillion float64 // optional; zero falls back to InputPerMillion
}

// PriceEntryResolver is the richer seam the spend math consumes. The
// PR2 PricingResolver returns (source, ok) only — enough for the
// PR1/PR3 audit-trail stamp but not the spend computation. PR4 adds
// Entry(provider, model) which returns the price record itself.
//
// Implementations MAY also satisfy PricingResolver — the spend layer
// uses PricingResolver.Lookup for the audit-trail string and falls
// back to Entry when PricingResolver is absent.
//
// Plan §"Engine integration" lines 308-309 — spend_delta computation.
type PriceEntryResolver interface {
	Entry(provider, model string) (PriceEntry, bool)
}

// Money helpers — minor-unit arithmetic with overflow defence.
//
// minorUnitsPerMajor returns the conventional divisor for the named
// currency. v1 supports the four currencies named in OD-6 (USD, CNY,
// EUR, GBP) and they all use 100 minor units per major. Future
// currencies with different exponents (e.g. JPY = 1 minor per major)
// need per-currency handling; PR4 callers MUST stay inside the OD-6
// set.
const minorUnitsPerMajor = 100

// CapConfig is the per-provider cap + period + thresholds the engine
// passes through on every RecordSpend call. Mirrors the
// config.ProviderQuotaConfig shape after ResolveThresholds /
// ResolvePeriod / ParseCap have produced concrete values — the engine
// does the config-to-runtime translation, the Tracker consumes Money
// + ints + strings.
//
// Cap zero (Money{}) means uncapped — the chip renders without a
// denominator. In the uncapped case ThresholdAmber and ThresholdRed
// MUST be ignored (stamped as -1 sentinels on the emitted Snapshot
// per quota.go:222-228 doc-comment).
//
// Plan §"`internal/provider/quota/`" lines 191-204 (TokenSpendVariant
// shape) + OD-9 thresholds + config.ProviderQuotaConfig at
// config.go:797-815.
type CapConfig struct {
	Cap            Money  // operator-configured cap; zero means uncapped
	Period         string // "monthly" | "rolling-30d" | "session"; "" → "monthly"
	ThresholdAmber int    // 0 → OD-9 default (80) when capped; -1 sentinel emitted when uncapped
	ThresholdRed   int    // 0 → OD-9 default (95) when capped; -1 sentinel emitted when uncapped
	PricingSource  string // audit-trail string for the chosen tier; engine stamps post-Resolver-lookup
}

// SpendRecord is the per-call payload the engine passes into
// Tracker.RecordSpend. The engine constructs one per `UsageDelta`
// chunk seen on the streaming pipe and feeds it through; the Tracker
// handles the snapshot-not-increment dedupe, the pricing lookup, the
// cumulative-add, and the Store.Put.
//
// Plan §"Engine integration / spend accumulation rules" lines 299-318.
type SpendRecord struct {
	Provider    string                 // canonical provider id ("anthropic", "openai", ...)
	Model       string                 // wire-confirmed model id from message_start
	AccountHash string                 // HashAccount(api_key)[:12]; empty for ollama-style local
	RequestID   string                 // upstream message id; partitions per-call dedupe
	Usage       *provider.UsageDelta   // cumulative tokens for the stream so far (snapshot, not increment)
	CapConfig   CapConfig              // per-provider cap + period + thresholds
}

// requestCumulative tracks the highest cumulative output_tokens seen
// for a (provider, model, request_id) tuple. The snapshot-not-increment
// rule (plan lines 306-313) means a request that emits three UsageDelta
// chunks ([0], [200], [350]) MUST cost the price of 350 output, not
// 0+200+350=550 output.
type requestCumulative struct {
	inputTokens         int64
	outputTokens        int64
	cacheCreationTokens int64
	cacheReadTokens     int64
	// chargedCost is the minor-unit cost we've already added to the
	// (provider, account, model) cumulative spend for this request_id.
	// A subsequent UsageDelta with a higher cumulative emits the
	// DELTA between (new cumulative cost) and chargedCost so the
	// running total stays correct.
	chargedCost int64
}

// requestKey partitions requestCumulative by (provider, model, request_id).
// Distinct from SpendStoreKey because (account, model) are not enough —
// we need per-request scoping for the snapshot-not-increment dedupe.
type requestKey struct {
	provider  string
	model     string
	requestID string
}

// spendState extends the Tracker with the PR4 spend-cache fields. The
// state is stored on the Tracker struct itself (see trackerSpend field
// added in tracker.go) so the PR1/PR3 Tracker API stays untouched.
type spendState struct {
	mu                 sync.Mutex
	priceEntryResolver PriceEntryResolver
	storeBackend       SpendStore
	nowFunc            func() time.Time
	requestCum         map[requestKey]requestCumulative
}

// newSpendState constructs the spend-cache extension. Returns nil when
// neither a resolver nor a store is supplied (the PR1/PR3 NewTracker
// path) — the Tracker's spend methods then short-circuit cleanly.
func newSpendState(resolver PriceEntryResolver, st SpendStore, nowFunc func() time.Time) *spendState {
	if nowFunc == nil {
		nowFunc = time.Now
	}
	return &spendState{
		priceEntryResolver: resolver,
		storeBackend:       st,
		nowFunc:            nowFunc,
		requestCum:         make(map[requestKey]requestCumulative),
	}
}

// NewTrackerWithSpend constructs a Tracker wired with the full PR4
// spend layer. The engine calls this from NewEngine when
// cfg.Quota.Store.Backend != "" and a pricing.Resolver is available.
//
// Parameters:
//   - backendLabel — "memory" | "redis" | "postgres", stamped into
//     every Snapshot's StoreBackend field for the chip's tooltip.
//   - resolver — the price-entry resolver. MUST satisfy both
//     PricingResolver (for the audit-trail string) and PriceEntryResolver
//     (for the spend math). Pass a type that implements both, or use
//     pricing.SpendResolver to adapt the Resolver from PR2.
//   - st — the cluster-ready Store. v1 production = store.MemoryStore;
//     tests use the same memory store under hand-built keys.
//   - nowFunc — clock injection so the auto-reset rollover is
//     deterministically testable. Pass time.Now in production.
//
// Per memory feedback_atomicity_awareness_uneven: persistence to disk
// is PR6 territory (plan line 430); PR4 stays in-memory via
// store.MemoryStore. The Store interface in place means PR6 swaps in
// an atomic-JSON-sidecar impl without touching Tracker or engine.
// Engine boundary discipline ([[ADR - FlowState Engine Boundary]]):
// the engine adapts its concrete store.MemoryStore into the
// SpendStore interface defined here via a tiny shim. The shim lives
// in the engine's wire-up code, not in this package, so the quota
// package stays free of the store import that would re-cycle into
// quota itself.
func NewTrackerWithSpend(
	backendLabel string,
	resolver any, // accept either PricingResolver or PriceEntryResolver or both
	st SpendStore,
	nowFunc func() time.Time,
) *Tracker {
	t := &Tracker{
		adapters:     make(map[string]Quota),
		storeBackend: backendLabel,
	}
	if pr, ok := resolver.(PricingResolver); ok {
		t.pricingResolver = pr
	}
	var per PriceEntryResolver
	if pr, ok := resolver.(PriceEntryResolver); ok {
		per = pr
	}
	t.spend = newSpendState(per, st, nowFunc)
	return t
}

// RecordSpend is the engine-facing spend-accumulation API. Called from
// the streaming pipe at the same site as recordSessionOutputTokens
// (engine.go:3724-3726 (processStreamChunks)) after every chunk that
// carries Usage data.
//
// Contract:
//   - Honours the snapshot-not-increment rule. The internal
//     per-(provider, model, request_id) cache tracks the highest
//     cumulative output_tokens seen; subsequent calls with a HIGHER
//     cumulative add the delta cost to the running spend; calls with
//     EQUAL or LOWER cumulative are no-ops (defensive against
//     out-of-order chunk delivery the SDK could theoretically produce).
//   - Pricing absent → writes a NotConfigured{Reason:"unknown-model:<id>"}
//     Snapshot to the Store rather than erroring. The chip surfaces
//     the reason verbatim.
//   - Period rollover handled lazily on read (in Lookup) — RecordSpend
//     itself stamps the CURRENT period's start/end on the Snapshot;
//     a later Lookup that crosses the PeriodEnd boundary zeroes Spent
//     and rotates Period{Start,End}.
//
// Side effects:
//   - Mutates the per-request cumulative cache on the Tracker.
//   - Writes a Snapshot to t.spend.storeBackend (Put with the spend's
//     {provider, account, model} Key).
//
// Returns:
//   - Any error from store.Put. The pricing-absent path is NOT an
//     error — it writes a NotConfigured Snapshot to the Store so the
//     chip surfaces the reason.
//
// Plan §"Engine integration / spend accumulation rules (A4 resolution)"
// lines 299-318.
func (t *Tracker) RecordSpend(ctx context.Context, rec SpendRecord) error {
	if t == nil || t.spend == nil {
		// PR1/PR3 NewTracker path — no spend wiring. Quiet no-op so
		// callers don't need to gate on tracker.HasSpend().
		return nil
	}
	if rec.Usage == nil {
		// No tokens carried — nothing to record. Defensive: the engine
		// already gates on chunk.Usage != nil before this call, but the
		// quiet no-op keeps the API forgiving.
		return nil
	}
	if rec.Provider == "" || rec.Model == "" {
		// Empty (provider, model) tuple — defensive only. The engine's
		// call site has req.Provider / req.Model populated by the
		// chat-request builder.
		return nil
	}

	now := t.spend.nowFunc()
	key := SpendStoreKey{
		ProviderID:  rec.Provider,
		AccountHash: rec.AccountHash,
		ModelID:     rec.Model,
	}

	// Look up pricing for this (provider, model). Absent → write a
	// NotConfigured Snapshot and return.
	var (
		entry        PriceEntry
		hasEntry     bool
		pricingLabel string
	)
	if t.spend.priceEntryResolver != nil {
		entry, hasEntry = t.spend.priceEntryResolver.Entry(rec.Provider, rec.Model)
	}
	if t.pricingResolver != nil {
		if src, ok := t.pricingResolver.Lookup(rec.Provider, rec.Model); ok {
			pricingLabel = src
		}
	}
	if rec.CapConfig.PricingSource != "" {
		// Engine-supplied source string wins — keeps the audit trail
		// pinned to the resolver tier the engine actually consulted.
		pricingLabel = rec.CapConfig.PricingSource
	}

	if !hasEntry {
		// Plan line 388 — unknown-model surfaces NotConfigured rather
		// than silent zero. Write the Snapshot to the Store so a
		// subsequent Lookup returns it without re-running the resolver.
		snap := Snapshot{
			Provider:      rec.Provider,
			AccountHash:   rec.AccountHash,
			Model:         rec.Model,
			ObservedAt:    now,
			StoreBackend:  t.storeBackend,
			PricingSource: pricingLabel,
			NotConfigured: &NotConfiguredVariant{Reason: "unknown-model:" + rec.Model},
		}
		return t.spend.storeBackend.Put(ctx, key, snap)
	}

	// Snapshot-not-increment dedupe per requestID. The Tracker tracks
	// the highest cumulative tokens seen for this request and adds
	// only the COST DELTA (new total cost − previously-charged cost)
	// to the (provider, account, model) spend.
	t.spend.mu.Lock()
	defer t.spend.mu.Unlock()

	rkey := requestKey{
		provider:  rec.Provider,
		model:     rec.Model,
		requestID: rec.RequestID,
	}
	cum := t.spend.requestCum[rkey]
	// Cumulative-or-no-op: take the higher of (stored, incoming) per
	// field so a chunk with a regression (e.g. a usage-less chunk
	// followed by a fresh delta) doesn't decrease the running total.
	if rec.Usage.InputTokens > cum.inputTokens {
		cum.inputTokens = rec.Usage.InputTokens
	}
	if rec.Usage.OutputTokens > cum.outputTokens {
		cum.outputTokens = rec.Usage.OutputTokens
	}
	if rec.Usage.CacheCreationInputTokens > cum.cacheCreationTokens {
		cum.cacheCreationTokens = rec.Usage.CacheCreationInputTokens
	}
	if rec.Usage.CacheReadInputTokens > cum.cacheReadTokens {
		cum.cacheReadTokens = rec.Usage.CacheReadInputTokens
	}

	// Compute the full cumulative cost for this request given the
	// current highest-seen totals, in minor units of entry.Currency.
	totalCost := tokenCostMinor(cum.inputTokens, entry.InputPerMillion, entry.Currency) +
		tokenCostMinor(cum.outputTokens, entry.OutputPerMillion, entry.Currency)

	// Cache pricing — fall back to non-cache rates when the per-million
	// fields are zero (Anthropic carries explicit cache rates; OpenAI /
	// Z.AI do not — plan line 309).
	if cum.cacheCreationTokens > 0 {
		rate := entry.CacheCreationPerMillion
		if rate == 0 {
			rate = entry.InputPerMillion
		}
		totalCost += tokenCostMinor(cum.cacheCreationTokens, rate, entry.Currency)
	}
	if cum.cacheReadTokens > 0 {
		rate := entry.CacheReadPerMillion
		if rate == 0 {
			rate = entry.InputPerMillion
		}
		totalCost += tokenCostMinor(cum.cacheReadTokens, rate, entry.Currency)
	}

	// The delta we need to ADD to the running (provider, account,
	// model) cumulative spend is the difference between this
	// request's current cumulative cost and what we've already
	// charged for it.
	delta := totalCost - cum.chargedCost
	if delta < 0 {
		// Defensive: chargedCost shrinking (impossible with the
		// monotonic-max logic above) — silently no-op the regression
		// rather than corrupt the running total.
		delta = 0
	}
	cum.chargedCost = totalCost
	t.spend.requestCum[rkey] = cum

	// Read-modify-write the (provider, account, model) cumulative
	// Snapshot in the Store. On first call we initialise the Period
	// boundaries; subsequent calls within the same period add delta.
	prev, getErr := t.spend.storeBackend.Get(ctx, key)
	periodStart, periodEnd := monthlyPeriod(now)
	period := rec.CapConfig.Period
	if period == "" {
		period = "monthly"
	}
	var (
		spentMinor   int64
		spentCurrency = entry.Currency
	)
	switch {
	case getErr != nil:
		// Sentinel SpendStoreErrNotFound is the first-call path —
		// quiet init. Any other error propagates.
		if !errors.Is(getErr, SpendStoreErrNotFound) {
			return getErr
		}
		spentMinor = delta
	case prev.TokenSpend != nil:
		// Existing TokenSpend — accumulate.
		spentMinor = prev.TokenSpend.Spent.Amount + delta
		// Carry the existing Period forward; the rollover-on-read in
		// Lookup handles boundary crossings.
		periodStart = prev.TokenSpend.PeriodStart
		periodEnd = prev.TokenSpend.PeriodEnd
	default:
		// Snapshot exists but no TokenSpend variant — typically a
		// NotConfigured carry-over from a previous unknown-model
		// write that just resolved (operator updated pricing.json).
		// Treat as first-call.
		spentMinor = delta
	}

	// USD-equivalent via OD-6 conversion table (currencies.go).
	conv := DefaultConversionTable()
	spentUSD, convErr := conv.ConvertToUSD(spentMinor, spentCurrency)
	if convErr != nil {
		// Unknown currency surfaces as zero SpentUSD with a recognisable
		// signal — the panel will show the native figure and skip the
		// USD column. Logging this is the engine's responsibility (it
		// has the slog handle); the quota package stays pure.
		spentUSD = 0
	}

	amber, red := resolveThresholdsForCap(rec.CapConfig)

	snap := Snapshot{
		Provider:      rec.Provider,
		AccountHash:   rec.AccountHash,
		Model:         rec.Model,
		ObservedAt:    now,
		StoreBackend:  t.storeBackend,
		PricingSource: pricingLabel,
		TokenSpend: &TokenSpendVariant{
			Spent:          Money{Amount: spentMinor, Currency: spentCurrency},
			SpentUSD:       Money{Amount: spentUSD, Currency: "USD"},
			Cap:            rec.CapConfig.Cap,
			Period:         period,
			PeriodStart:    periodStart,
			PeriodEnd:      periodEnd,
			PricingSource:  pricingLabel,
			ThresholdAmber: amber,
			ThresholdRed:   red,
		},
	}
	return t.spend.storeBackend.Put(ctx, key, snap)
}

// tokenCostMinor returns the minor-unit cost of `tokens` at
// `perMillion` major units per million. Currency parameter is for
// the per-currency minor-unit divisor (v1 always 100; documented
// here so future non-100-divisor currencies have an explicit
// hook).
//
// Float arithmetic is confined to this function — the rate division
// produces a fractional result that is rounded once to int64 minor
// units. All upstream addition stays in int64 to avoid drift.
func tokenCostMinor(tokens int64, perMillion float64, currency string) int64 {
	_ = currency // v1 OD-6 currencies all use 100-minor-per-major; keep the seam
	if tokens <= 0 || perMillion <= 0 {
		return 0
	}
	// cost (major units) = tokens / 1_000_000 * perMillion
	// minor units       = cost * 100   (USD/CNY/EUR/GBP convention)
	major := float64(tokens) / 1_000_000.0 * perMillion
	minor := major * float64(minorUnitsPerMajor)
	return int64(math.Round(minor))
}

// resolveThresholdsForCap returns the (amber, red) pair the chip
// renders. Per OD-9 defaults: capped → green<80% amber 80-95% red≥95%;
// uncapped → -1 sentinels (chip stays green).
//
// Mirrors config.ProviderQuotaConfig.ResolveThresholds at
// config.go:935-945 (ResolveThresholds) — duplicated here rather than
// imported to keep the quota package free of the config dependency
// (config already imports quota for the Money type; the reverse
// would cycle).
func resolveThresholdsForCap(cap CapConfig) (amber, red int) {
	if cap.Cap.IsZero() {
		return -1, -1
	}
	amber = cap.ThresholdAmber
	red = cap.ThresholdRed
	if amber <= 0 {
		amber = 80
	}
	if red <= 0 {
		red = 95
	}
	return amber, red
}

// LookupSpend returns the most recent TokenSpend Snapshot for
// (provider, account, model) WITHOUT touching the adapter overlay.
// Exposed for the engine's per-turn emission cadence so the
// provider_quota chunk reflects the spend write that JUST happened
// without losing data to the no-account Lookup shim. PR5/PR6
// dashboard endpoints will iterate the Store directly through this
// helper or the underlying SpendStore.
//
// Returns ok=false when no Snapshot exists for the key OR when the
// Tracker has no spend wiring.
func (t *Tracker) LookupSpend(ctx context.Context, providerID, accountHash, modelID string) (Snapshot, bool) {
	if t == nil || t.spend == nil || t.spend.storeBackend == nil {
		return Snapshot{}, false
	}
	key := SpendStoreKey{ProviderID: providerID, AccountHash: accountHash, ModelID: modelID}
	snap, err := t.spend.storeBackend.Get(ctx, key)
	if err != nil {
		return Snapshot{}, false
	}
	now := t.spend.nowFunc()
	// Mirror lookupSpendOverlay's rollover semantics so the post-turn
	// emitter sees the same period rotation a chip-side read would.
	if snap.TokenSpend != nil && !snap.TokenSpend.PeriodEnd.IsZero() && !now.Before(snap.TokenSpend.PeriodEnd) {
		newStart, newEnd := monthlyPeriod(now)
		snap.ObservedAt = now
		snap.TokenSpend.Spent = Money{Amount: 0, Currency: snap.TokenSpend.Spent.Currency}
		snap.TokenSpend.SpentUSD = Money{Amount: 0, Currency: "USD"}
		snap.TokenSpend.PeriodStart = newStart
		snap.TokenSpend.PeriodEnd = newEnd
		_ = t.spend.storeBackend.Put(ctx, key, snap)
	}
	snap.StoreBackend = t.storeBackend
	return snap, true
}

// lookupSpendOverlay returns the TokenSpend variant for (provider,
// model) when one exists in the Store, applying the OD-8 auto-reset
// rollover on read. Returns ok=false when no TokenSpend Snapshot
// exists — the caller then falls through to the adapter-driven
// RateLimit path.
//
// Plan §"`internal/provider/quota/`" lines 191-204 (TokenSpend
// variant shape) + OD-8 auto-reset lines 511-516.
func (t *Tracker) lookupSpendOverlay(
	ctx context.Context,
	providerID, modelID string,
	key SpendStoreKey,
) (Snapshot, bool) {
	if t.spend == nil || t.spend.storeBackend == nil {
		return Snapshot{}, false
	}
	snap, err := t.spend.storeBackend.Get(ctx, key)
	if err != nil {
		// Missing key or any other error — fall through. The PR1
		// adapter path produces the no-adapter-registered or
		// awaiting-first-response NotConfigured Snapshot.
		return Snapshot{}, false
	}
	// Pass through NotConfigured Snapshots — the engine wrote one
	// for an unknown-model lookup, and the chip should surface the
	// reason rather than the adapter's RateLimit (which would mask
	// the operator-actionable pricing gap).
	if snap.NotConfigured != nil {
		snap.StoreBackend = t.storeBackend
		return snap, true
	}
	if snap.TokenSpend == nil {
		// No TokenSpend variant — fall through.
		return Snapshot{}, false
	}

	// Auto-reset on PeriodStart rollover (OD-8): if `now` has
	// crossed PeriodEnd, rotate the period and zero Spent.
	now := t.spend.nowFunc()
	if !snap.TokenSpend.PeriodEnd.IsZero() && !now.Before(snap.TokenSpend.PeriodEnd) {
		newStart, newEnd := monthlyPeriod(now)
		snap.ObservedAt = now
		snap.TokenSpend.Spent = Money{Amount: 0, Currency: snap.TokenSpend.Spent.Currency}
		snap.TokenSpend.SpentUSD = Money{Amount: 0, Currency: "USD"}
		snap.TokenSpend.PeriodStart = newStart
		snap.TokenSpend.PeriodEnd = newEnd
		// Persist the rotated Snapshot so subsequent reads start
		// clean. Errors are swallowed — the chip still gets the
		// rotated figure even if the write back failed (in-memory
		// store cannot fail in v1 anyway).
		_ = t.spend.storeBackend.Put(ctx, key, snap)
		// Also purge per-request cumulative cache for this
		// (provider, model) — old request_ids are no longer
		// relevant in the new period. Single-pass; cheap.
		t.spend.mu.Lock()
		for rk := range t.spend.requestCum {
			if rk.provider == providerID && rk.model == modelID {
				delete(t.spend.requestCum, rk)
			}
		}
		t.spend.mu.Unlock()
	}
	snap.StoreBackend = t.storeBackend
	return snap, true
}

// monthlyPeriod returns the (start, end) of the calendar month
// containing `now`, in UTC. start is the first instant of the
// month; end is the first instant of the FOLLOWING month — the
// half-open [start, end) convention.
//
// OD-8 resolution (plan lines 511-516): monthly is the v1-only
// period; rolling-30d and session are accepted by config validation
// but Tracker treats them all as monthly for v1. Future expansion
// adds dispatch on CapConfig.Period.
func monthlyPeriod(now time.Time) (start, end time.Time) {
	utc := now.UTC()
	start = time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
	end = start.AddDate(0, 1, 0)
	return start, end
}
