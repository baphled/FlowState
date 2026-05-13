package engine

import (
	"context"
	"encoding/json"
	"time"

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
	Provider      string                            `json:"provider"`
	AccountHash   string                            `json:"account_hash"`
	Model         string                            `json:"model,omitempty"`
	ObservedAt    string                            `json:"observed_at"`
	Stale         bool                              `json:"stale,omitempty"`
	StoreBackend  string                            `json:"store_backend,omitempty"`
	PricingSource string                            `json:"pricing_source,omitempty"`
	Variant       string                            `json:"variant"`
	RateLimit     *providerQuotaRateLimitPayload    `json:"rate_limit,omitempty"`
	TokenSpend    *providerQuotaTokenSpendPayload   `json:"token_spend,omitempty"`
	NotConfigured *providerQuotaNotConfigPayload    `json:"not_configured,omitempty"`
}

type providerQuotaRateLimitPayload struct {
	Requests                 providerQuotaWindow `json:"requests"`
	Tokens                   providerQuotaWindow `json:"tokens"`
	Input                    providerQuotaWindow `json:"input"`
	Output                   providerQuotaWindow `json:"output"`
	TightestPercentRemaining int                 `json:"tightest_percent_remaining"`
	TightestResetAt          string              `json:"tightest_reset_at,omitempty"`
}

type providerQuotaWindow struct {
	Limit     int    `json:"limit"`
	Remaining int    `json:"remaining"`
	Reset     string `json:"reset,omitempty"`
}

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
	return out, true
}

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

func windowToPayload(w quota.Window) providerQuotaWindow {
	out := providerQuotaWindow{Limit: w.Limit, Remaining: w.Remaining}
	if !w.Reset.IsZero() {
		out.Reset = w.Reset.UTC().Format(time.RFC3339)
	}
	return out
}

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
	payload, ok := snapshotToPayload(snap)
	if !ok {
		return provider.StreamChunk{}, false
	}
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
	payload, ok := snapshotToPayload(snap)
	if !ok {
		return provider.StreamChunk{}, false
	}
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

// tryEmitProviderQuotaInline writes the inline provider_quota chunk
// onto outChan. Suppresses duplicate emissions per session via
// lastProviderQuotaPayload — a chip update that would render the
// exact same string is a no-op so the SSE wire stays quiet between
// real changes. Mirrors the lastUsagePayload pattern in
// emitPostRetryContextUsage (engine.go:5277-5288).
//
// Caller is the Stream goroutine; outChan is the per-Stream channel
// the api SSE bridge subscribes to. Suppression key is sessionID.
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
