// Package quota defines the per-provider quota-and-spend interface,
// tagged-union Snapshot return value, and Tracker that fans out to
// per-provider adapters.
//
// The shape is the load-bearing v1 commitment from the Provider Quota
// and Spend Visibility plan (May 2026), §"`internal/provider/quota/`
// — the tagged-union interface" (lines 155-231): exactly one of
// {RateLimit, TokenSpend, NotConfigured} is non-nil on every Snapshot
// returned by Quota.Remaining. The Vue chip (PR4a) discriminates on
// the union to render the matching variant.
//
// OD-3 resolution (plan lines 234-235): the interface is keyed by
// (provider, account_hash, model), NOT principal. Multi-user auth-mode
// deployments share one Snapshot across all authenticated users —
// quota is what the deployment-operator's API key is being charged,
// not what each user has used. The interface deliberately has no
// principal_id parameter.
//
// Engine boundary discipline ([[ADR - FlowState Engine Boundary]]):
// this package is consumer-agnostic. The Tracker is fed by the engine
// after every provider response; consumers (SSE writer, REST endpoint,
// future WS) read Snapshots via Quota.Remaining without importing any
// transport-specific types.
package quota

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// Snapshot is the tagged-union return value of Quota.Remaining.
//
// Discriminant contract: exactly one of {RateLimit, TokenSpend,
// NotConfigured} is non-nil on every Snapshot. Callers branch on
// `switch` over the non-nil variant pointer. The compile-time
// enforcement lives in the Vue side via TypeScript discriminated
// unions; the Go side relies on the contract test ladder
// (contract_test.go) plus IsValid.
//
// Plan §"`internal/provider/quota/`" lines 168-178.
type Snapshot struct {
	// Provider is the stable provider identifier ("anthropic", "openai",
	// "openzen", "zai", "ollama", "ollamacloud", "copilot").
	Provider string

	// AccountHash identifies which API key/account this snapshot covers
	// without exposing the key itself. SHA-256(api_key)[:12]; computed
	// via HashAccount. Empty string for providers with no key concept
	// (ollama local). Per OD-3, this is the partitioning key alongside
	// Provider+Model.
	AccountHash string

	// Model is the provider-side model id (e.g.
	// "claude-opus-4-7-20251031"). Empty when the snapshot covers an
	// account-wide rather than per-model figure (rare in v1).
	Model string

	// ObservedAt is the wall-clock at which this snapshot was last
	// refreshed. Used in concert with the RateLimit window or
	// TokenSpend.PeriodEnd to compute Stale.
	ObservedAt time.Time

	// Stale flips true when ObservedAt + window < now. For RateLimit
	// variants: true past TightestResetAt. For TokenSpend variants:
	// always false in v1 (PeriodEnd handling is the auto-reset path,
	// not the staleness signal). Plan A2 fold (lines 396-397):
	// stale-on-read preserves chip continuity over the reset boundary
	// rather than blanking.
	Stale bool

	// StoreBackend surfaces "memory" | "redis" | "postgres" so the
	// chip's tooltip can disclose single-instance scope (plan B3 fold,
	// lines 285-293). Memory means "FlowState-observed only on this
	// instance".
	StoreBackend string

	// PricingSource is the audit-trail string for the price table that
	// produced TokenSpend figures. One of:
	//   - "flowstate-default-v1"     (embedded go:embed default)
	//   - "operator-override:<path>" (operator-supplied JSON file)
	//   - "registry:<url>"           (remote-fetched cache)
	// Empty for non-TokenSpend variants. PR2 plumbs this end-to-end;
	// PR1 leaves it empty.
	PricingSource string

	// RateLimit is non-nil when the provider exposes per-window
	// rate-limit signals (Anthropic, OpenAI when account-tier-capped,
	// openaicompat-via-proxy when headers pass through). Mutually
	// exclusive with TokenSpend and NotConfigured.
	RateLimit *RateLimitVariant

	// TokenSpend is non-nil when the provider exposes pay-per-token
	// billing tracked via cumulative-usage accounting. Mutually
	// exclusive with RateLimit and NotConfigured. PR1 leaves this
	// always nil; PR4 lights it up.
	TokenSpend *TokenSpendVariant

	// NotConfigured is non-nil when the provider exposes neither
	// rate-limit windows nor pay-per-token billing FlowState can
	// observe. The Reason field carries the operator-visible
	// explanation. Mutually exclusive with RateLimit and TokenSpend.
	NotConfigured *NotConfiguredVariant
}

// IsValid reports whether the Snapshot's discriminant invariant holds:
// exactly one of {RateLimit, TokenSpend, NotConfigured} is non-nil.
//
// The contract spec (contract_test.go) asserts this on every Snapshot
// produced by Quota.Remaining for every in-scope provider. Adapters
// MUST honour the invariant or callers see undefined render behaviour.
func (s Snapshot) IsValid() bool {
	set := 0
	if s.RateLimit != nil {
		set++
	}
	if s.TokenSpend != nil {
		set++
	}
	if s.NotConfigured != nil {
		set++
	}
	return set == 1
}

// RateLimitVariant carries the four documented rate-limit windows
// Anthropic and OpenAI emit. Each window has its own limit + remaining
// + reset triple, plus a tightest-window summary the chip renders.
//
// -1 sentinel for "not provided" mirrors provider.RateLimit's
// convention at types.go:430-468. Reset times stay zero-valued when
// the provider does not emit the header.
//
// Plan §"`internal/provider/quota/`" lines 180-189.
type RateLimitVariant struct {
	// Requests is the per-request window (RPM-style budget).
	Requests Window

	// Tokens is the combined per-token window when the provider emits
	// a single bucket rather than split input/output.
	Tokens Window

	// Input is the per-input-token window (Anthropic emits
	// anthropic-ratelimit-input-tokens-{limit,remaining,reset}).
	Input Window

	// Output is the per-output-token window (Anthropic emits
	// anthropic-ratelimit-output-tokens-{limit,remaining,reset}).
	Output Window

	// TightestPercentRemaining is the minimum (Remaining / Limit *
	// 100) across all four windows that have non-sentinel values.
	// The chip renders this as the headline "% remaining" figure.
	// -1 when no window has both Limit and Remaining populated.
	TightestPercentRemaining int

	// TightestResetAt is the Reset of the window that produced
	// TightestPercentRemaining. Zero time when TightestPercentRemaining
	// is -1.
	TightestResetAt time.Time
}

// Window is a single rate-limit window triple. -1 sentinel for "not
// provided"; Reset is zero-valued when the provider omits the header.
type Window struct {
	Limit     int
	Remaining int
	Reset     time.Time
}

// NewWindow returns a Window pre-populated with -1 sentinels so a
// caller building a variant from partial headers can disambiguate
// "field absent" from a real "0 remaining".
func NewWindow() Window {
	return Window{Limit: -1, Remaining: -1}
}

// TokenSpendVariant carries the pay-per-token spend figures for
// providers where FlowState accumulates `(input_tokens × input_price)
// + (output_tokens × output_price)`. PR1 ships the shape; PR4 lights
// it up.
//
// Plan §"`internal/provider/quota/`" lines 191-204.
type TokenSpendVariant struct {
	// Spent is the cumulative spend in the model's native currency
	// across the current Period.
	Spent Money

	// SpentUSD is the USD-equivalent of Spent computed via the OD-6
	// conversion table. Equal to Spent when Currency == "USD".
	SpentUSD Money

	// Cap is the operator-configured monthly cap. Zero (Money{}) when
	// uncapped — the chip renders without a denominator and stays
	// green.
	Cap Money

	// Period is the spend window granularity: "monthly" |
	// "rolling-30d" | "session". v1 ships "monthly" only.
	Period string

	// PeriodStart is the wall-clock at which the current spend window
	// started (e.g. start of the calendar month). Used for auto-reset
	// on rollover.
	PeriodStart time.Time

	// PeriodEnd is the wall-clock at which the current spend window
	// ends. The PR6 ticker resets the counter when now >= PeriodEnd.
	PeriodEnd time.Time

	// PricingSource is the audit-trail string for the price table that
	// computed Spent. Mirrors Snapshot.PricingSource but lives on the
	// variant so it travels with the figure end-to-end. v1 defaults
	// to "flowstate-default-v1".
	PricingSource string

	// ThresholdAmber is the percentage (Spent/Cap * 100) at which the
	// chip transitions green → amber. Default 80 per OD-9. -1 when
	// Cap is zero (uncapped — always green).
	ThresholdAmber int

	// ThresholdRed is the percentage at which the chip transitions
	// amber → red. Default 95 per OD-9. -1 when Cap is zero.
	ThresholdRed int
}

// NotConfiguredVariant explains why a provider exposes no quota
// signal. The Reason string is operator-visible verbatim via the chip
// tooltip and the deep-view panel.
//
// Recognised values (plan line 207 + per-provider matrix):
//   - "local-model"        — ollama (local; no quota by definition).
//   - "no-quota-headers"   — proxy strips rate-limit headers.
//   - "subscription-only"  — copilot (subscription model; no
//     per-request meter or monthly token cap).
//   - "awaiting-pr3"       — openaicompat success-path lift not yet
//     shipped (PR3 territory).
//   - "unknown-model:<id>" — pricing table missing this model.
type NotConfiguredVariant struct {
	Reason string
}

// Money is a fixed-point currency amount in minor units (cents for
// USD, fen for CNY, pence for GBP, cents for EUR). int64 ranges to
// ~$92 quadrillion — practical bound: never overflows in v1.
//
// Plan §"`internal/provider/quota/`" lines 210-213.
type Money struct {
	Amount   int64  // minor units
	Currency string // ISO 4217
}

// IsZero reports whether the Money is the zero value. Used to
// distinguish "uncapped" (Cap.IsZero() == true) from "capped at 0"
// (impossible; defensive check only).
func (m Money) IsZero() bool {
	return m.Amount == 0 && m.Currency == ""
}

// HashAccount returns the first 12 hex chars of SHA-256(apiKey). The
// truncation preserves enough entropy to distinguish the keys a single
// operator might rotate through without exposing the key itself in
// logs or the dashboard.
//
// Empty apiKey returns "" so providers with no key concept (ollama
// local) produce an empty AccountHash rather than a hash of the empty
// string.
//
// Plan §"`internal/provider/quota/`" lines 170-171.
func HashAccount(apiKey string) string {
	if apiKey == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:])[:12]
}

// Quota is the per-provider interface. Every provider package ships a
// `quota.go` with a `Quota` adapter implementing this interface.
//
// OD-3: no principal_id parameter. Quota is account-scoped, not
// user-scoped. Multi-user auth-mode deployments share one Snapshot
// per (provider, account, model) across all authenticated users.
//
// Plan §"`internal/provider/quota/`" lines 215-227.
type Quota interface {
	// Remaining returns the current snapshot for (providerID, modelID)
	// scoped to the adapter's bound account. The returned Snapshot
	// MUST satisfy Snapshot.IsValid() — exactly one variant pointer
	// non-nil.
	//
	// ctx.Err() returns honoured by the implementation; long-running
	// work (e.g. PR5's registry refresh) MUST honour cancellation.
	Remaining(ctx context.Context, providerID, modelID string) (Snapshot, error)

	// RecordResponse is called by the engine after every provider
	// response to update the adapter's internal cache. headers carries
	// the HTTP response headers (may be empty for streaming providers
	// that don't surface them); usage carries the token deltas
	// extracted from the response.
	//
	// Implementations are concurrent-safe — the engine may call this
	// from multiple goroutines if streaming and chat run in parallel.
	RecordResponse(providerID, modelID string, headers http.Header, usage provider.Usage)
}
