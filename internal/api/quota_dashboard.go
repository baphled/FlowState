package api

// quota_dashboard.go — REST endpoints backing the PR5 dashboard surface
// for the Provider Quota and Spend Visibility plan (May 2026).
//
// Routes:
//   - GET  /api/v1/providers/quota        — aggregator across every
//     (provider, account_hash, model) tuple the engine has observed.
//   - POST /api/v1/providers/quota/reset  — manual reset of the spend
//     counter for one (provider, account_hash, model) row (OD-8).
//
// Both endpoints are protected via registerProtected (auth_routes.go)
// so they participate in the Auth Track PR3 middleware chain
// (RequireOrigin → RequireSession → CSRF). The aggregator is read-only
// (GET; gorilla/csrf passes through); the reset endpoint is state-
// changing (POST; X-CSRF-Token header required).
//
// Plan §"Vue integration" lines 326-336 (PR4b sub-slice) + plan PR5
// row 429 + memory feedback_use_make_ai_commit_in_flowstate.
//
// B8 carry-through (Auth Track PR5/C10 — task brief): the 401 wire
// shape on no-session matches every other protected endpoint
// byte-for-byte; the middleware handles that uniformly via the
// `unauthenticated` literal. Handlers below never run for an
// unauthenticated caller.

import (
	"encoding/json"
	"net/http"

	"github.com/baphled/flowstate/internal/provider/quota"
)

// quotaDashboardEntry is the JSON wire shape one row of the
// aggregator returns. The SPA deserialises these into its
// discriminated-union types.
//
// Phase 4 / Commit 2 (Turn-Based Post-Then-Poll, May 2026) — the
// nested types lived in sse_writers.go before the SSE bridge was
// retired. They are inlined here now that the dashboard endpoint is
// the sole owner of this JSON wire surface; the field-for-field
// shape (snake_case JSON tags) is preserved so the SPA's existing
// TypeScript types deserialise unchanged.
type quotaDashboardEntry struct {
	Provider      string                       `json:"provider"`
	AccountHash   string                       `json:"account_hash"`
	Model         string                       `json:"model,omitempty"`
	ObservedAt    string                       `json:"observed_at"`
	Stale         bool                         `json:"stale,omitempty"`
	StoreBackend  string                       `json:"store_backend,omitempty"`
	PricingSource string                       `json:"pricing_source,omitempty"`
	Variant       string                       `json:"variant"`
	RateLimit     *dashboardProviderQuotaRateLimit  `json:"rate_limit,omitempty"`
	TokenSpend    *dashboardProviderQuotaTokenSpend `json:"token_spend,omitempty"`
	NotConfigured *dashboardProviderQuotaNotConfig  `json:"not_configured,omitempty"`
}

// dashboardProviderQuotaRateLimit is the rate-limit variant of a
// dashboard quota row. JSON wire shape mirrors the pre-Commit-2
// sseProviderQuotaRateLimit field-for-field so existing SPA code
// deserialises unchanged.
type dashboardProviderQuotaRateLimit struct {
	Requests                 dashboardQuotaWindow `json:"requests"`
	Tokens                   dashboardQuotaWindow `json:"tokens"`
	Input                    dashboardQuotaWindow `json:"input"`
	Output                   dashboardQuotaWindow `json:"output"`
	TightestPercentRemaining int                  `json:"tightest_percent_remaining"`
	TightestResetAt          string               `json:"tightest_reset_at,omitempty"`
}

// dashboardQuotaWindow is one rate-limit window (requests / tokens /
// input / output). JSON wire shape preserved from sseQuotaWindow.
type dashboardQuotaWindow struct {
	Limit     int    `json:"limit"`
	Remaining int    `json:"remaining"`
	Reset     string `json:"reset,omitempty"`
}

// dashboardProviderQuotaTokenSpend is the token-spend variant of a
// dashboard quota row. JSON wire shape preserved.
type dashboardProviderQuotaTokenSpend struct {
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

// dashboardProviderQuotaNotConfig is the not-configured variant.
type dashboardProviderQuotaNotConfig struct {
	Reason string `json:"reason"`
}

// quotaResetRequest is the JSON request body for POST .../quota/reset.
// All three fields are required — the handler returns 400 with a
// uniform "invalid_request" body when any is missing or when extra
// fields are present (DisallowUnknownFields per the auth track's
// strict-body discipline).
type quotaResetRequest struct {
	Provider    string `json:"provider"`
	AccountHash string `json:"account_hash"`
	Model       string `json:"model"`
}

// handleListProviderQuotas backs GET /api/v1/providers/quota.
//
// Returns:
//   - 200 with a JSON array of quotaDashboardEntry rows when the
//     aggregator is wired (always — empty array when no providers
//     have been observed yet).
//   - 501 not_implemented with an empty body when the aggregator is
//     nil (PR4 wiring incomplete or the server was constructed
//     without WithQuotaAggregator).
//   - 405 method_not_allowed on non-GET methods (defensive — net/http
//     pattern matching already filters by method, but a future route
//     restructure could land here).
//
// Authentication is handled upstream by registerProtected; an
// unauthenticated caller never reaches this handler.
//
// Per OD-3 (account-scoped, not principal-scoped): every authenticated
// principal sees the SAME aggregated view. The Snapshot is keyed by
// (provider, account_hash, model), not by the calling principal_id.
func (s *Server) handleListProviderQuotas(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.quotaAggregator == nil {
		// PR4 wiring incomplete — surface as 501 so the SPA can
		// distinguish "feature not wired" (501) from "no providers
		// observed yet" (200 + empty array).
		http.Error(w, "not_implemented", http.StatusNotImplemented)
		return
	}
	entries := s.quotaAggregator.QuotaSnapshots(r.Context())
	rows := make([]quotaDashboardEntry, 0, len(entries))
	for _, entry := range entries {
		row, ok := snapshotToDashboardEntry(entry.Snapshot)
		if !ok {
			// Defensive — skip malformed snapshots rather than 500 so a
			// single bad row doesn't blank the whole dashboard.
			continue
		}
		// Stamp the partition-key fields from the aggregator row when
		// the Snapshot left them empty (some adapters delegate stamping
		// to the Tracker; the partition key is always populated on the
		// engine side).
		if row.Provider == "" {
			row.Provider = entry.Provider
		}
		if row.AccountHash == "" {
			row.AccountHash = entry.AccountHash
		}
		if row.Model == "" {
			row.Model = entry.Model
		}
		rows = append(rows, row)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(rows)
}

// handleResetProviderQuota backs POST /api/v1/providers/quota/reset.
//
// Body (required, all three fields, exact match — DisallowUnknownFields):
//
//	{"provider":"anthropic","account_hash":"abc12345","model":"claude-opus-4-7"}
//
// Returns:
//   - 200 with an empty body when the spend Snapshot was reset.
//   - 404 not_found when no Snapshot existed for the tuple.
//   - 400 invalid_request when the body is malformed / missing fields /
//     carries unknown fields.
//   - 501 not_implemented when the aggregator is nil.
//   - 405 method_not_allowed on non-POST methods.
//   - 500 internal_error on a Store impl error.
//
// CSRF: the X-CSRF-Token header is required (state-changing endpoint
// per the Auth Track PR3 middleware composition). The middleware
// rejects with 403 before reaching this handler when the token is
// missing or invalid.
func (s *Server) handleResetProviderQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.quotaAggregator == nil {
		http.Error(w, "not_implemented", http.StatusNotImplemented)
		return
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req quotaResetRequest
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid_request", http.StatusBadRequest)
		return
	}
	if req.Provider == "" || req.Model == "" {
		// account_hash MAY be empty (v1 single-account-per-provider
		// default) but provider + model are load-bearing — the
		// (provider, "", model) key still picks a unique row in the
		// single-account-per-provider deployment.
		http.Error(w, "invalid_request", http.StatusBadRequest)
		return
	}

	found, err := s.quotaAggregator.ResetQuotaSpend(r.Context(), req.Provider, req.AccountHash, req.Model)
	if err != nil {
		http.Error(w, "internal_error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "not_found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"reset"}`))
}

// snapshotToDashboardEntry translates a quota.Snapshot into the
// dashboard JSON wire row. Mirrors snapshotToPayload in
// engine/provider_quota.go but lives in the api package because the
// wire shape is the api-package contract (matching sseProviderQuota
// field-for-field).
//
// Returns (row, true) when the Snapshot satisfies the discriminant
// invariant; (zero, false) otherwise so callers suppress malformed
// rows.
func snapshotToDashboardEntry(snap quota.Snapshot) (quotaDashboardEntry, bool) {
	if !snap.IsValid() {
		return quotaDashboardEntry{}, false
	}
	row := quotaDashboardEntry{
		Provider:      snap.Provider,
		AccountHash:   snap.AccountHash,
		Model:         snap.Model,
		ObservedAt:    snap.ObservedAt.UTC().Format(timeRFC3339),
		Stale:         snap.Stale,
		StoreBackend:  snap.StoreBackend,
		PricingSource: snap.PricingSource,
	}
	switch {
	case snap.RateLimit != nil:
		row.Variant = "rate_limit"
		row.RateLimit = &dashboardProviderQuotaRateLimit{
			Requests:                 dashboardWindow(snap.RateLimit.Requests),
			Tokens:                   dashboardWindow(snap.RateLimit.Tokens),
			Input:                    dashboardWindow(snap.RateLimit.Input),
			Output:                   dashboardWindow(snap.RateLimit.Output),
			TightestPercentRemaining: snap.RateLimit.TightestPercentRemaining,
		}
		if !snap.RateLimit.TightestResetAt.IsZero() {
			row.RateLimit.TightestResetAt = snap.RateLimit.TightestResetAt.UTC().Format(timeRFC3339)
		}
	case snap.TokenSpend != nil:
		row.Variant = "token_spend"
		row.TokenSpend = &dashboardProviderQuotaTokenSpend{
			SpentMinor:     snap.TokenSpend.Spent.Amount,
			SpentCurrency:  snap.TokenSpend.Spent.Currency,
			SpentUSDMinor:  snap.TokenSpend.SpentUSD.Amount,
			CapMinor:       snap.TokenSpend.Cap.Amount,
			CapCurrency:    snap.TokenSpend.Cap.Currency,
			Period:         snap.TokenSpend.Period,
			PeriodStart:    snap.TokenSpend.PeriodStart.UTC().Format(timeRFC3339),
			PeriodEnd:      snap.TokenSpend.PeriodEnd.UTC().Format(timeRFC3339),
			ThresholdAmber: snap.TokenSpend.ThresholdAmber,
			ThresholdRed:   snap.TokenSpend.ThresholdRed,
		}
	case snap.NotConfigured != nil:
		row.Variant = "not_configured"
		row.NotConfigured = &dashboardProviderQuotaNotConfig{Reason: snap.NotConfigured.Reason}
	}
	return row, true
}

func dashboardWindow(w quota.Window) dashboardQuotaWindow {
	out := dashboardQuotaWindow{Limit: w.Limit, Remaining: w.Remaining}
	if !w.Reset.IsZero() {
		out.Reset = w.Reset.UTC().Format(timeRFC3339)
	}
	return out
}

// timeRFC3339 is the layout string used end-to-end for the
// provider_quota event family. Keeping it as a package-level const
// rather than time.RFC3339 mirrors the engine-side payload and pins
// the wire format in one place.
const timeRFC3339 = "2006-01-02T15:04:05Z07:00"
