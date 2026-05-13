package quota

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
)

// Conversion-table rates for the v1 currencies (USD, CNY, EUR, GBP).
//
// updated 2026-05-13
//
// Per plan OD-6 resolution (lines 498-503): hard-coded quarterly-
// refreshed table at internal/provider/quota/currencies.go; updates
// land via PR. Operator override via `quota.currency.conversion_table`
// config (full path to a JSON file with the same shape as this
// embedded map). v1 supports the four currencies named in OD-6 — USD,
// CNY, EUR, GBP — and surfaces both native and USD-equivalent on the
// SSE wire (sseProviderQuotaTokenSpend.SpentMinor /
// SpentUSDMinor already wired in PR1).
//
// Storage convention: every rate is "N units of <code> per 1 USD",
// i.e. rates[USD] = 1.0, rates[CNY] = 7.23 means $1 = 7.23 CNY. The
// inverse direction is used for the USD-equivalent computation
// (SpentUSD = SpentNative / rate). Storing one direction avoids the
// "which way does this rate point?" ambiguity the round-1 reviewer
// flagged on the auth track's forex notes.
//
// The numbers are conservative snapshots of public mid-market rates
// as of the comment date. Operators in volatile-currency regions
// override via the JSON file — see LoadCurrencyOverride below.
var defaultCurrencyRates = map[string]float64{
	"USD": 1.00,
	"CNY": 7.23,
	"EUR": 0.92,
	"GBP": 0.79,
}

// ConversionTable is a parsed currency conversion table. Mirrors the
// shape of defaultCurrencyRates so the operator-override path uses
// the same JSON shape as the embedded map.
//
// Plan OD-6: operator override via `quota.currency.conversion_table`
// — full path to a JSON file. The override REPLACES (not merges) the
// embedded table — currency conversion is small enough that a
// partial override would surface as a confusing "EUR works but GBP
// doesn't" failure mode. Operators copy the embedded table, edit the
// rates they care about, and ship the full file.
type ConversionTable struct {
	// Rates is the "1 USD = N <currency>" map. USD MUST be present
	// with rate 1.0 (the identity entry); other currencies are
	// optional but Resolver.ConvertToUSD returns an error for any
	// currency missing from this map.
	Rates map[string]float64 `json:"rates"`

	// UpdatedAt is the table's publication date — surfaced in the
	// panel's "rates as of" line, mirrors pricing.Table.UpdatedAt.
	UpdatedAt string `json:"updated_at,omitempty"`
}

// DefaultConversionTable returns the embedded conversion table value
// — a defensive copy so callers may mutate the returned Rates without
// affecting subsequent calls. The default UpdatedAt matches the doc
// comment above (2026-05-13).
//
// Plan OD-6: this is the v1 baseline; operators override via JSON
// file at quota.currency.conversion_table.
func DefaultConversionTable() ConversionTable {
	rates := make(map[string]float64, len(defaultCurrencyRates))
	for k, v := range defaultCurrencyRates {
		rates[k] = v
	}
	return ConversionTable{
		Rates:     rates,
		UpdatedAt: "2026-05-13",
	}
}

// LoadCurrencyOverride reads an operator-supplied conversion table at
// path and returns it. Empty path returns DefaultConversionTable() and
// a nil error — the "no override configured" case is quiet success.
//
// Returns an error on filesystem read failure, malformed JSON, or a
// missing/non-unity USD rate (the identity entry — a table without
// USD=1.0 would break the SpentUSD computation silently).
func LoadCurrencyOverride(path string) (ConversionTable, error) {
	if path == "" {
		return DefaultConversionTable(), nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path is intentional
	if err != nil {
		return ConversionTable{}, fmt.Errorf("quota: reading currency conversion override %q: %w", path, err)
	}
	var t ConversionTable
	if err := json.Unmarshal(data, &t); err != nil {
		return ConversionTable{}, fmt.Errorf("quota: parsing currency conversion override %q: %w", path, err)
	}
	if len(t.Rates) == 0 {
		return ConversionTable{}, errors.New("quota: currency conversion override has empty rates map")
	}
	usd, ok := t.Rates["USD"]
	if !ok {
		return ConversionTable{}, errors.New("quota: currency conversion override missing USD identity rate")
	}
	if math.Abs(usd-1.0) > 1e-9 {
		return ConversionTable{}, fmt.Errorf("quota: currency conversion override has non-unity USD rate %g (must be 1.0)", usd)
	}
	return t, nil
}

// ConvertToUSD converts an amount in minor units of the given currency
// into minor units of USD using the table's rate. Returns the converted
// Money value and a nil error on success.
//
// USD passes through unchanged. Unknown currencies return an error —
// the caller (Tracker / per-provider adapter) surfaces this in the
// audit trail rather than silently zero-filling SpentUSD. Per memory
// feedback_atomicity_awareness_uneven: the silent-zero failure mode is
// a Recall regression the plan explicitly avoids replicating.
//
// Minor-unit fidelity: the function operates on int64 minor units
// (cents for USD, fen for CNY) throughout. Float arithmetic is
// confined to the rate division; the result is rounded to the nearest
// integer minor unit.
func (t ConversionTable) ConvertToUSD(amountMinor int64, currency string) (int64, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "USD" {
		return amountMinor, nil
	}
	rate, ok := t.Rates[currency]
	if !ok {
		return 0, fmt.Errorf("quota: no conversion rate for currency %q", currency)
	}
	if rate <= 0 {
		return 0, fmt.Errorf("quota: non-positive conversion rate %g for currency %q", rate, currency)
	}
	// rate = "1 USD = N <currency>" so usdMinor = nativeMinor / rate.
	usd := float64(amountMinor) / rate
	return int64(math.Round(usd)), nil
}

// SupportedCurrencies returns the sorted list of currency codes the
// table supports. Exposed for tests and for the panel's "supported
// currencies" tooltip.
func (t ConversionTable) SupportedCurrencies() []string {
	out := make([]string, 0, len(t.Rates))
	for k := range t.Rates {
		out = append(out, k)
	}
	// Stable order matters for the test's assertion and for the panel's
	// rendering. Sort lexicographically — USD will naturally land
	// alphabetically.
	sortStrings(out)
	return out
}

// sortStrings is a tiny shim to avoid importing "sort" just for this
// one call (and to keep currencies.go self-contained against future
// refactors that might decimate the imports).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
