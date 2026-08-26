package engine

import (
	"context"
	"encoding/json"
	"time"

	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/quota"
)

// providerQuotaPayload mirrors api.sseProviderQuota (sse_writers.go:176-189)
// field-for-field. The engine marshals this into StreamChunk.Content;
// writeSSEProviderQuota at internal/api/sse_writers.go:679-691
// (writeSSEProviderQuota) re-parses and re-emits with the canonical
// "type":"provider_quota" discriminant.
//
// PR1 froze the wire shape (commit ef40f9b0); PR4 lights up the
// engine-side emission. Adding a field here MUST be paired with an
// addition on the api/sse_writers.go side AND a contract spec update
// at web/src/types/contract.spec.ts.
//
// The `Type` field is intentionally omitted — writeSSEProviderQuota
// stamps it. The engine MUST NOT emit it (the api fan-out would
// double-stamp).
type providerQuotaPayload struct {
	Provider      string                          `json:"provider"`
	AccountHash   string                          `json:"account_hash"`
	Model         string                          `json:"model,omitempty"`
	ObservedAt    string                          `json:"observed_at"`
	Stale         bool                            `json:"stale,omitempty"`
	StoreBackend  string                          `json:"store_backend,omitempty"`
	PricingSource string                          `json:"pricing_source,omitempty"`
	Variant       string                          `json:"variant"`
	RateLimit     *providerQuotaRateLimitPayload  `json:"rate_limit,omitempty"`
	TokenSpend    *providerQuotaTokenSpendPayload `json:"token_spend,omitempty"`
	NotConfigured *providerQuotaNotConfigPayload  `json:"not_configured,omitempty"`

	// RateLimitedUntil is the failover cooldown expiry, serialised as
	// RFC 3339. Omitted (empty string) when the provider/model is not
	// currently rate-limited by the failover system. ADR 001.
	RateLimitedUntil string `json:"rate_limited_until,omitempty"`

	// Status is a synthesised health indicator that merges the failover
	// cooldown, rate-limit window exhaustion, and cap attainment into
	// a single value. One of:
	//   - "rate_limited" — RateLimitedUntil is set and in the future
	//   - "exhausted"    — tightest_percent_remaining == 0 on the
	//                      rate-limit variant (window exhausted but
	//                      no failover cooldown)
	//   - "spent"        — token_spend variant with Spent >= Cap
	//   - "healthy"      — none of the above
	// Omitted on the not_configured variant.
	// ADR 002: Unified Rate-Limit Visibility.
	Status string `json:"status,omitempty"`
}

// providerQuotaRateLimitPayload is the wire payload for rate-limit quota status.
type providerQuotaRateLimitPayload struct {
	Requests                 providerQuotaWindow `json:"requests"`
	Tokens                   providerQuotaWindow `json:"tokens"`
	Input                    providerQuotaWindow `json:"input"`
	Output                   providerQuotaWindow `json:"output"`
	TightestPercentRemaining int                 `json:"tightest_percent_remaining"`
	TightestResetAt          string              `json:"tightest_reset_at,omitempty"`
}

// providerQuotaWindow describes a single rate-limit window dimension.
type providerQuotaWindow struct {
	Limit     int    `json:"limit"`
	Remaining int    `json:"remaining"`
	Reset     string `json:"reset,omitempty"`
}

// providerQuotaTokenSpendPayload is the wire payload for token spend quota status.
type providerQuotaTokenSpendPayload struct {
	SpentMinor     int64  `json:"spent_minor"`
	SpentCurrency  string `json:"spent_currency"`
	SpentUSDMinor  int64  `json:"spent_usd_minor"`
	CapMinor       int64  `json:"cap_minor,omitempty"`
	CapCurrency    string `json:"cap_currency,omitempty"`
	Period         string `json:"period"`
	PeriodStart    string `json:"period_start"`
	PeriodEnd      string `json:"period_end"`
	ThresholdAmber int    `json:"threshold_amber"`
	ThresholdRed   int    `json:"threshold_red"`
}

// providerQuotaNotConfigPayload is the wire payload used when quota is not configured.
type providerQuotaNotConfigPayload struct {
	Reason string `json:"reason"`
}

// snapshotToPayload translates a quota.Snapshot into the wire payload.
// Returns (payload, true) when the Snapshot satisfies IsValid()
// (exactly one variant non-nil); (zero, false) otherwise so the
// emission site can suppress the malformed event rather than ship
// a chip-blanking payload.
//
// Times are serialised as RFC 3339 strings so the JS Date parser at
// the Vue side handles them with no extra glue.
//
// Expected: parameters for snapshotToPayload.
// Returns: result of snapshotToPayload.
// Side effects: None.
func snapshotToPayload(snap quota.Snapshot) (providerQuotaPayload, bool) {
	if !snap.IsValid() {
		return providerQuotaPayload{}, false
	}
	out := providerQuotaPayload{
		Provider:      snap.Provider,
		AccountHash:   snap.AccountHash,
		Model:         snap.Model,
		ObservedAt:    snap.ObservedAt.UTC().Format(time.RFC3339),
		Stale:         snap.Stale,
		StoreBackend:  snap.StoreBackend,
		PricingSource: snap.PricingSource,
	}
	switch {
	case snap.RateLimit != nil:
		out.Variant = "rate_limit"
		out.RateLimit = rateLimitToPayload(snap.RateLimit)
	case snap.TokenSpend != nil:
		out.Variant = "token_spend"
		out.TokenSpend = tokenSpendToPayload(snap.TokenSpend)
	case snap.NotConfigured != nil:
		out.Variant = "not_configured"
		out.NotConfigured = &providerQuotaNotConfigPayload{Reason: snap.NotConfigured.Reason}
	}

	// RateLimitedUntil: stamp from the Snapshot field that the engine
	// caller (buildProviderQuotaChunk / QuotaSnapshots) may have already
	// populated from the failover HealthManager. ADR 001.
	if !snap.RateLimitedUntil.IsZero() {
		out.RateLimitedUntil = snap.RateLimitedUntil.UTC().Format(time.RFC3339)
	}

	// Status: synthesise a single health indicator from the variant data
	// and the failover cooldown. ADR 002.
	out.Status = synthesiseQuotaStatus(snap)

	return out, true
}

// synthesiseQuotaStatus computes the single-status field described
// in ADR 002 from the variant data and the failover cooldown already
// stamped on the Snapshot.
//
// Priority (first match wins):
//  1. "rate_limited" — RateLimitedUntil is set and still in the future.
//  2. "exhausted"    — rate-limit variant with tightest_percent_remaining == 0
//     (the window is bone-dry but no failover cooldown).
//  3. "spent"        — token-spend variant where Spent >= Cap (cap is set).
//  4. "healthy"      — none of the above.
//
// Expected: parameters for synthesiseQuotaStatus.
// Returns: result of synthesiseQuotaStatus.
// Side effects: None.
func synthesiseQuotaStatus(snap quota.Snapshot) string {
	if !snap.RateLimitedUntil.IsZero() && snap.RateLimitedUntil.After(time.Now()) {
		return "rate_limited"
	}
	if snap.RateLimit != nil && snap.RateLimit.TightestPercentRemaining == 0 {
		return "exhausted"
	}
	if snap.TokenSpend != nil && snap.TokenSpend.Cap.Amount > 0 &&
		snap.TokenSpend.Spent.Amount >= snap.TokenSpend.Cap.Amount {
		return "spent"
	}
	return "healthy"
}

// rateLimitToPayload ...
//
// Expected: parameters for rateLimitToPayload.
//
// Returns: result of rateLimitToPayload.
//
// Side effects: None.
func rateLimitToPayload(rl *quota.RateLimitVariant) *providerQuotaRateLimitPayload {
	p := &providerQuotaRateLimitPayload{
		Requests:                 windowToPayload(rl.Requests),
		Tokens:                   windowToPayload(rl.Tokens),
		Input:                    windowToPayload(rl.Input),
		Output:                   windowToPayload(rl.Output),
		TightestPercentRemaining: rl.TightestPercentRemaining,
	}
	if !rl.TightestResetAt.IsZero() {
		p.TightestResetAt = rl.TightestResetAt.UTC().Format(time.RFC3339)
	}
	return p
}

// windowToPayload ...
//
// Expected: parameters for windowToPayload.
//
// Returns: result of windowToPayload.
//
// Side effects: None.
func windowToPayload(w quota.Window) providerQuotaWindow {
	out := providerQuotaWindow{Limit: w.Limit, Remaining: w.Remaining}
	if !w.Reset.IsZero() {
		out.Reset = w.Reset.UTC().Format(time.RFC3339)
	}
	return out
}

// tokenSpendToPayload ...
//
// Expected: parameters for tokenSpendToPayload.
//
// Returns: result of tokenSpendToPayload.
//
// Side effects: None.
func tokenSpendToPayload(ts *quota.TokenSpendVariant) *providerQuotaTokenSpendPayload {
	return &providerQuotaTokenSpendPayload{
		SpentMinor:     ts.Spent.Amount,
		SpentCurrency:  ts.Spent.Currency,
		SpentUSDMinor:  ts.SpentUSD.Amount,
		CapMinor:       ts.Cap.Amount,
		CapCurrency:    ts.Cap.Currency,
		Period:         ts.Period,
		PeriodStart:    ts.PeriodStart.UTC().Format(time.RFC3339),
		PeriodEnd:      ts.PeriodEnd.UTC().Format(time.RFC3339),
		ThresholdAmber: ts.ThresholdAmber,
		ThresholdRed:   ts.ThresholdRed,
	}
}

// buildProviderQuotaChunk computes the provider_quota payload for req
// and returns it as a StreamChunk{EventType:"provider_quota"}. Mirrors
// engine.go:3108-3120 (buildContextUsageChunk).
//
// Returns hasQuota=false when the engine cannot compute a meaningful
// figure (no tracker wired, no provider/model on req, Snapshot
// invariant violated). A missing chunk is a better degradation than a
// malformed chunk the chip's parser would classify as "unknown" and
// discard — matches the context_usage degradation stance.
//
// Side effects:
//   - None beyond JSON marshalling. The Tracker.Lookup call MAY
//     trigger an auto-reset-on-read write to the SpendStore per OD-8;
//     that is the Tracker's contract, not a side effect of this
//     function.
//
// Expected: parameters for buildProviderQuotaChunk.
// Returns: result of buildProviderQuotaChunk.
func (e *Engine) buildProviderQuotaChunk(ctx context.Context, req *provider.ChatRequest) (provider.StreamChunk, bool) {
	if e == nil || req == nil || e.quotaTracker == nil {
		return provider.StreamChunk{}, false
	}
	if req.Provider == "" || req.Model == "" {
		return provider.StreamChunk{}, false
	}
	accountHash := e.quotaAccountHashes[req.Provider]
	// Prefer LookupSpend (account-aware) — falls through to the
	// adapter-driven Lookup when no TokenSpend is recorded yet. PR5
	// R2 fold: Lookup now also takes accountHash so the Store-overlay
	// fallback inside Lookup uses the same partition key as the
	// LookupSpend call above. Pre-PR5 the empty-account collapse meant
	// multi-account deployments silently merged into one bucket.
	snap, ok := e.quotaTracker.LookupSpend(ctx, req.Provider, accountHash, req.Model)
	if !ok {
		var err error
		snap, err = e.quotaTracker.Lookup(ctx, req.Provider, accountHash, req.Model)
		if err != nil {
			return provider.StreamChunk{}, false
		}
	}
	// Stamp the failover cooldown from the HealthManager so the
	// payload carries both the quota-window state and the rate-limit
	// back-off expiry. ADR 001.
	stampRateLimitedUntil(&snap, e, req.Provider, req.Model)

	payload, ok := snapshotToPayload(snap)
	if !ok {
		return provider.StreamChunk{}, false
	}
	// Publish provider.status_changed bus event on status transition.
	// Must happen before the chunk is emitted so SSE subscribers see
	// the event in flight before the next SSE provider_quota chunk.
	e.trackAndPublishStatusChange(req.Provider, req.Model, payload.Status, snap.RateLimitedUntil)

	body, err := json.Marshal(payload)
	if err != nil {
		// Marshalling a struct of primitives cannot realistically fail;
		// suppress rather than emit a malformed chunk.
		return provider.StreamChunk{}, false
	}
	return provider.StreamChunk{
		EventType: "provider_quota",
		Content:   string(body),
	}, true
}

// makePostTurnQuotaEmitter mirrors makePostTurnUsageEmitter
// (engine.go:2707-2742). Returns a closure the streamWithToolLoop
// caller invokes before every terminal Done so the chip pivots when
// the cumulative spend ticks up on the just-completed turn.
//
// Returns nil when the engine has no quota tracker wired — the same
// gate makePostTurnUsageEmitter applies for `hasUsage=false`.
//
// The closure captures req.Provider / req.Model by value so a caller
// that mutates req mid-turn (e.g. a model swap) does not change the
// emission target. AccountHash is read from e.quotaAccountHashes at
// emit time so a hot key-rotation visible via SetConfig (PR5/PR6) is
// reflected on the very next chip update.
//
// Expected: parameters for makePostTurnQuotaEmitter.
// Returns: result of makePostTurnQuotaEmitter.
// Side effects: None.
func (e *Engine) makePostTurnQuotaEmitter(req *provider.ChatRequest) postTurnQuotaEmitter {
	if e == nil || e.quotaTracker == nil || req == nil {
		return nil
	}
	providerID := req.Provider
	modelID := req.Model
	if providerID == "" || modelID == "" {
		return nil
	}
	return func(ctx context.Context, outChan chan<- provider.StreamChunk) {
		chunk, ok := e.buildProviderQuotaChunkExplicit(ctx, providerID, modelID)
		if !ok {
			return
		}
		outChan <- chunk
	}
}

// postTurnQuotaEmitter is the closure type returned by
// makePostTurnQuotaEmitter. Defined separately so the function
// signature in streamWithToolLoop callers stays grep-able alongside
// the existing postTurnUsageEmitter.
type postTurnQuotaEmitter func(ctx context.Context, outChan chan<- provider.StreamChunk)

// buildProviderQuotaChunkExplicit is the by-id variant of
// buildProviderQuotaChunk for the post-turn emitter (which captured
// providerID + modelID at construction time rather than holding a
// req pointer). Same body, different parameter shape.
//
// Expected: parameters for buildProviderQuotaChunkExplicit.
// Returns: result of buildProviderQuotaChunkExplicit.
// Side effects: None.
func (e *Engine) buildProviderQuotaChunkExplicit(ctx context.Context, providerID, modelID string) (provider.StreamChunk, bool) {
	if e == nil || e.quotaTracker == nil || providerID == "" || modelID == "" {
		return provider.StreamChunk{}, false
	}
	accountHash := e.quotaAccountHashes[providerID]
	// PR5 R2 fold — accountHash threaded through Lookup to match
	// LookupSpend's partition key.
	snap, ok := e.quotaTracker.LookupSpend(ctx, providerID, accountHash, modelID)
	if !ok {
		var err error
		snap, err = e.quotaTracker.Lookup(ctx, providerID, accountHash, modelID)
		if err != nil {
			return provider.StreamChunk{}, false
		}
	}
	stampRateLimitedUntil(&snap, e, providerID, modelID)

	payload, ok := snapshotToPayload(snap)
	if !ok {
		return provider.StreamChunk{}, false
	}
	e.trackAndPublishStatusChange(providerID, modelID, payload.Status, snap.RateLimitedUntil)

	body, err := json.Marshal(payload)
	if err != nil {
		return provider.StreamChunk{}, false
	}
	return provider.StreamChunk{
		EventType: "provider_quota",
		Content:   string(body),
	}, true
}

// recordQuotaSpend translates a UsageDelta chunk into a SpendRecord
// and calls Tracker.RecordSpend. Called from processStreamChunks at
// the same site as recordSessionOutputTokens
// (engine.go:3724-3726 (processStreamChunks)).
//
// Quiet no-op when no tracker is wired. Errors from the tracker are
// swallowed silently — the chip degrading to "—" is preferable to a
// turn-aborting failure when the spend write fails (in-memory store
// cannot fail in v1; PR6 atomic-write may produce IO errors logged
// at warn level once the engine has the slog handle threaded
// through).
//
// Plan §"Engine integration / spend accumulation rules
// (A4 resolution)" lines 299-318.
//
// Expected: parameters for recordQuotaSpend.
// Returns: result of recordQuotaSpend.
// Side effects: None.
func (e *Engine) recordQuotaSpend(ctx context.Context, providerID, modelID string, usage *provider.UsageDelta) {
	if e == nil || e.quotaTracker == nil || usage == nil || providerID == "" || modelID == "" {
		return
	}
	accountHash := e.quotaAccountHashes[providerID]
	capCfg := e.quotaCaps[providerID]
	_ = e.quotaTracker.RecordSpend(ctx, quota.SpendRecord{
		Provider:    providerID,
		Model:       modelID,
		AccountHash: accountHash,
		RequestID:   usage.RequestID,
		Usage:       usage,
		CapConfig:   capCfg,
	})
}

// QuotaAggregatorRow is the engine-side projection one row of the
// PR5 dashboard aggregator returns. Mirrors the api package's
// QuotaAggregatorEntry shape but stays in the engine to keep the
// engine consumer-agnostic (api → engine import is fine; the engine
// must not import api).
//
// Plan PR5 row 429 — backs GET /api/v1/providers/quota.
type QuotaAggregatorRow struct {
	Provider    string
	AccountHash string
	Model       string
	Snapshot    quota.Snapshot
}

// QuotaSnapshots returns every (provider, account_hash, model)
// Snapshot the engine's quota tracker holds. The returned slice is
// empty (not nil) when no tracker is wired or the store is empty.
//
// PR5 dashboard aggregator entry point — the api package's
// quota_dashboard.go handler iterates the result and projects each
// Snapshot into the wire shape.
//
// Expected: parameters for QuotaSnapshots.
// Returns: result of QuotaSnapshots.
// Side effects: None.
func (e *Engine) QuotaSnapshots(ctx context.Context) []QuotaAggregatorRow {
	if e == nil || e.quotaTracker == nil {
		return nil
	}
	entries, err := e.quotaTracker.Snapshots(ctx)
	if err != nil {
		// Defensive — the in-memory MemoryStore cannot fail in v1; a
		// future PR6 atomic-write impl may surface IO errors. Suppress
		// the error to nil so the dashboard renders empty rather than
		// 500-ing the whole surface. The api layer logs the error if
		// it cares (it doesn't today).
		return nil
	}
	out := make([]QuotaAggregatorRow, 0, len(entries))
	for _, entry := range entries {
		snap := entry.Snapshot
		stampRateLimitedUntil(&snap, e, entry.Key.ProviderID, entry.Key.ModelID)
		out = append(out, QuotaAggregatorRow{
			Provider:    entry.Key.ProviderID,
			AccountHash: entry.Key.AccountHash,
			Model:       entry.Key.ModelID,
			Snapshot:    snap,
		})
		// Publish status change for each snapshot so the dashboard
		// aggregator path also triggers provider.status_changed.
		status := synthesiseQuotaStatus(snap)
		e.trackAndPublishStatusChange(entry.Key.ProviderID, entry.Key.ModelID, status, snap.RateLimitedUntil)
	}
	return out
}

// ResetQuotaSpend zeros the spend counter for the given (provider,
// account_hash, model). Returns true when a Snapshot existed and was
// reset; false when no row was found. Errors from the Store impl
// propagate.
//
// PR5 dashboard "Reset spend counter" button — backs
// POST /api/v1/providers/quota/reset.
//
// Expected: parameters for ResetQuotaSpend.
// Returns: result of ResetQuotaSpend.
// Side effects: None.
func (e *Engine) ResetQuotaSpend(ctx context.Context, providerID, accountHash, modelID string) (bool, error) {
	if e == nil || e.quotaTracker == nil {
		return false, nil
	}
	return e.quotaTracker.ResetSpend(ctx, providerID, accountHash, modelID)
}

// stampRateLimitedUntil queries the engine's failover HealthManager
// for the given provider/model pair and stamps the cooldown expiry
// onto the snapshot. No-op when the engine has no failover manager
// wired or when the HealthManager reports no active rate-limit.
//
// ADR 001: Converge Failover Health State with Quota Tracker.
//
// Expected: parameters for stampRateLimitedUntil.
// Side effects: None.
func stampRateLimitedUntil(snap *quota.Snapshot, e *Engine, provider, model string) {
	if e == nil || e.failoverManager == nil {
		return
	}
	health := e.failoverManager.Health()
	expiry, ok := health.RateLimitedUntil(provider, model)
	if ok {
		snap.RateLimitedUntil = expiry
	}
}

// trackAndPublishStatusChange detects provider status transitions and
// publishes a provider.status_changed bus event when the status differs
// from the last observed value for the (provider, model) pair.
//
// The first observation for a given pair always fires (PreviousStatus
// is empty string, signalling "no prior state known"). Subsequent calls
// with the same status are no-ops.
//
// No-op when the engine has no bus wired or when the status has not
// changed from the last tracked value.
//
// Expected: parameters for trackAndPublishStatusChange.
// Returns: result of trackAndPublishStatusChange.
// Side effects: None.
func (e *Engine) trackAndPublishStatusChange(provider, model, status string, rateLimitedUntil time.Time) {
	if e == nil || e.bus == nil {
		return
	}
	key := provider + ":" + model

	e.providerStatusMu.Lock()
	if e.providerStatusMap == nil {
		e.providerStatusMap = make(map[string]string)
	}
	prev := e.providerStatusMap[key]
	if prev == status {
		e.providerStatusMu.Unlock()
		return
	}
	e.providerStatusMap[key] = status
	e.providerStatusMu.Unlock()

	e.bus.Publish(
		events.EventProviderStatusChanged,
		events.NewProviderStatusChangedEvent(events.ProviderStatusChangedEventData{
			Provider:         provider,
			Model:            model,
			PreviousStatus:   prev,
			Status:           status,
			RateLimitedUntil: rateLimitedUntil,
			ObservedAt:       time.Now(),
		}),
	)
}

// tryEmitProviderQuotaInline writes the inline provider_quota chunk
// onto outChan. Suppresses duplicate emissions per session via
// lastProviderQuotaPayload — a chip update that would render the
// exact same string is a no-op so the SSE wire stays quiet between
// real changes. Mirrors the lastUsagePayload pattern in
// emitPostRetryContextUsage (engine.go:5277-5288).
//
// Caller is the Stream goroutine; outChan is the per-Stream channel
// the api SSE bridge subscribes to. Suppression key is sessionID.
//
// Expected: parameters for tryEmitProviderQuotaInline.
// Returns: result of tryEmitProviderQuotaInline.
// Side effects: None.
func (e *Engine) tryEmitProviderQuotaInline(
	sessionID string,
	chunk provider.StreamChunk,
	outChan chan<- provider.StreamChunk,
) {
	if chunk.EventType != "provider_quota" || chunk.Content == "" {
		return
	}
	e.lastProviderQuotaPayloadMu.Lock()
	if e.lastProviderQuotaPayload[sessionID] == chunk.Content {
		e.lastProviderQuotaPayloadMu.Unlock()
		return
	}
	e.lastProviderQuotaPayload[sessionID] = chunk.Content
	e.lastProviderQuotaPayloadMu.Unlock()
	outChan <- chunk
}
