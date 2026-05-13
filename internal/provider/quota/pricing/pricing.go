// Package pricing implements the three-tier pricing-table resolution
// for the Provider Quota and Spend Visibility plan (May 2026):
// operator-override > remote-registry > embedded-default.
//
// Plan §"Pricing table (OD-1 resolution — remote-loadable with cache +
// fallback)" lines 338-388. The package owns:
//
//   - The embedded go:embed default at pricing.json — the v1 baseline.
//     ALWAYS present; the lowest precedence; the source of truth when
//     pricing.registry.enabled=false (the v1 default).
//
//   - The Table value-type — a parsed pricing table keyed by
//     "<provider>/<model>" with per-entry currency support (plan §A1
//     fold lines 376-377).
//
//   - The Resolver — merges the three tiers and answers Lookup(provider,
//     model) with the price entry + the source string that produced it
//     (Snapshot.PricingSource per plan line 199).
//
// Registry-loader and operator-override-file lookups live in
// subpackages and feeder constructors so this file stays a pure
// in-memory composition. No network, no filesystem in pricing.go
// itself — all side-effecting loaders inject a parsed Table via
// constructor argument.
//
// PrecedenceSource constants are the load-bearing audit-trail strings
// the chip's tooltip surfaces to operators — they must remain stable
// across the v1 lifetime. Plan §"Pricing table" line 386.
package pricing

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Source is the audit-trail discriminator the Resolver stamps onto
// every Lookup result. The values are surfaced verbatim into
// Snapshot.PricingSource and from there into the deep-view panel
// (PR4b) so operators can see which tier supplied the live price.
//
// Plan §"Pricing table" line 386 + Snapshot.PricingSource doc comment
// (quota.go:82-89).
type Source string

const (
	// SourceEmbedded indicates the embedded go:embed pricing.json — the
	// FlowState v1 baseline shipped in the binary.
	SourceEmbedded Source = "flowstate-default-v1"

	// SourceRegistry is the prefix for remote-registry-sourced entries.
	// The Resolver returns "registry:<url>" verbatim so the panel
	// distinguishes between operator-self-hosted registries.
	//
	// Use SourceRegistryString to format a concrete value.
	SourceRegistry Source = "registry"

	// SourceOverride is the prefix for operator-override-file entries.
	// The Resolver returns "operator-override:<path>" verbatim.
	//
	// Use SourceOverrideString to format a concrete value.
	SourceOverride Source = "operator-override"
)

// SourceRegistryString formats a Source value for a registry hit.
// Returns "registry:<url>" — the canonical string Snapshot.PricingSource
// carries per plan line 386.
func SourceRegistryString(url string) string {
	return string(SourceRegistry) + ":" + url
}

// SourceOverrideString formats a Source value for an operator-override
// hit. Returns "operator-override:<path>".
func SourceOverrideString(path string) string {
	return string(SourceOverride) + ":" + path
}

// Entry is a single model's pricing record. All four "per million"
// fields are in the entry's Currency in major units (e.g. USD 15.00
// means $15.00 per million tokens). Cache fields are optional —
// providers that don't expose cache pricing (OpenAI, Z.AI) omit them
// and the engine falls back to the non-cache rates per plan §"Engine
// integration" line 309.
//
// Plan §"Pricing table" lines 350-374 (JSON shape).
type Entry struct {
	// Currency is the ISO-4217 code (USD, CNY, EUR, GBP in v1 per OD-6).
	// Empty falls back to the Table's DefaultCurrency at parse time.
	Currency string `json:"currency,omitempty"`

	// InputPerMillion is the price per million input tokens in Currency
	// major units (e.g. 15.00 for $15.00).
	InputPerMillion float64 `json:"input_per_million"`

	// OutputPerMillion is the price per million output tokens.
	OutputPerMillion float64 `json:"output_per_million"`

	// CacheReadPerMillion is the price per million tokens served from
	// the provider's prompt cache. Optional — zero means "not priced";
	// the engine falls back to InputPerMillion per plan line 309.
	CacheReadPerMillion float64 `json:"cache_read_per_million,omitempty"`

	// CacheCreationPerMillion is the price per million tokens written to
	// the provider's prompt cache. Optional — zero means "not priced";
	// the engine falls back to InputPerMillion.
	CacheCreationPerMillion float64 `json:"cache_creation_per_million,omitempty"`
}

// Table is a parsed pricing-table value. Keys are
// "<provider>/<model>" (e.g. "anthropic/claude-opus-4-7"). The Source
// is the audit-trail string the Resolver stamps when this Table is the
// winning tier for a Lookup.
//
// Tables are immutable after parse — the Resolver merges by precedence
// rather than mutating a single Table in place.
type Table struct {
	// Version is the plan-mandated "v1" string. Future schemas bump
	// this; parse rejects unknown versions.
	Version string `json:"version"`

	// UpdatedAt is the table's publication date (ISO-8601 yyyy-mm-dd).
	// Surfaced into the panel's "prices as of" line.
	UpdatedAt string `json:"updated_at"`

	// DefaultCurrency fills in Entry.Currency when an entry omits it.
	// Defaults to "USD" when the table itself omits it.
	DefaultCurrency string `json:"default_currency,omitempty"`

	// Models is the keyed lookup map. Keys MUST be the canonical
	// "<provider>/<model>" form.
	Models map[string]Entry `json:"models"`

	// Source is the audit-trail string the Resolver stamps when this
	// Table provided the winning entry. Set by the loader:
	//   - SourceEmbedded for the go:embed default.
	//   - SourceRegistryString(url) for a registry-loaded table.
	//   - SourceOverrideString(path) for an operator-override file.
	Source string `json:"-"`
}

//go:embed pricing.json
var embeddedJSON []byte

// LoadEmbedded returns the parsed embedded pricing.json. The returned
// Table has Source==SourceEmbedded. Returns an error only when the
// embedded JSON fails to parse — a build-time regression — so callers
// can panic-on-init confidence.
//
// The function copies the parsed map so callers may mutate the
// returned Table without affecting subsequent LoadEmbedded calls.
//
// Plan §"Pricing table" line 346 (embedded fallback always present).
func LoadEmbedded() (Table, error) {
	t, err := parseTable(embeddedJSON)
	if err != nil {
		return Table{}, fmt.Errorf("pricing: parsing embedded pricing.json: %w", err)
	}
	t.Source = string(SourceEmbedded)
	return t, nil
}

// ParseTable parses a raw pricing-table JSON blob (as produced by the
// registry or shipped at the operator-override path) into a Table.
// The Source field is left empty — the caller stamps it via
// Table.WithSource based on which tier supplied the bytes.
//
// Returns an error on malformed JSON, an unknown Version, an empty
// Models map, or any entry with non-positive InputPerMillion /
// OutputPerMillion (a zero price would silently report $0.00 spend
// which violates the honesty stance the plan demands).
func ParseTable(data []byte) (Table, error) {
	return parseTable(data)
}

// WithSource returns a copy of the Table with Source replaced. Used by
// the registry loader and operator-override loader to stamp their
// audit-trail string after parsing.
func (t Table) WithSource(source string) Table {
	clone := t
	// Avoid sharing the models map with the caller's other references —
	// the Resolver may mutate the merged table downstream.
	if t.Models != nil {
		clone.Models = make(map[string]Entry, len(t.Models))
		for k, v := range t.Models {
			clone.Models[k] = v
		}
	}
	return clone
}

func parseTable(data []byte) (Table, error) {
	if len(data) == 0 {
		return Table{}, errors.New("pricing: empty pricing table data")
	}
	var t Table
	if err := json.Unmarshal(data, &t); err != nil {
		return Table{}, fmt.Errorf("pricing: unmarshalling table: %w", err)
	}
	if t.Version != "v1" {
		return Table{}, fmt.Errorf("pricing: unsupported version %q (want %q)", t.Version, "v1")
	}
	if len(t.Models) == 0 {
		return Table{}, errors.New("pricing: empty models map")
	}
	if t.DefaultCurrency == "" {
		t.DefaultCurrency = "USD"
	}
	// Backfill per-entry currency from the table default and validate
	// every entry. Entries without a positive input/output rate are
	// rejected — silent $0.00 spend is the failure mode the plan's
	// honesty stance rejects.
	for k, e := range t.Models {
		if e.Currency == "" {
			e.Currency = t.DefaultCurrency
		}
		if e.InputPerMillion <= 0 {
			return Table{}, fmt.Errorf("pricing: model %q has non-positive input_per_million (%g)", k, e.InputPerMillion)
		}
		if e.OutputPerMillion <= 0 {
			return Table{}, fmt.Errorf("pricing: model %q has non-positive output_per_million (%g)", k, e.OutputPerMillion)
		}
		t.Models[k] = e
	}
	return t, nil
}

// Resolver merges the three pricing tiers in precedence order and
// answers Lookup. Constructed via NewResolver; the constructor itself
// performs no I/O — the caller passes already-loaded Tables for each
// tier (nil tables are ignored).
//
// Tier precedence (plan §"Pricing table" lines 342-346):
//  1. Operator-override (highest) — partial files merge over the
//     baseline; entries present here win regardless of tier 2/3.
//  2. Remote registry — entries here win over the embedded baseline
//     for any key the override did not supply.
//  3. Embedded default (lowest) — the v1 baseline.
//
// Resolver is safe for concurrent Lookup calls. Mutation (e.g.
// hot-reload after a registry refresh) is the caller's responsibility
// and serialises through SetRegistry.
type Resolver struct {
	embedded Table
	registry Table
	override Table
}

// NewResolver constructs a Resolver from the three tier tables. Any of
// the three may be a zero Table (loaders that found nothing return the
// zero value) — Lookup falls through cleanly. The embedded tier MUST
// be non-empty in production — pass LoadEmbedded() output. Tests may
// pass a zero embedded Table to assert "no tiers populated" behaviour.
func NewResolver(embedded, registry, override Table) *Resolver {
	return &Resolver{
		embedded: embedded,
		registry: registry,
		override: override,
	}
}

// Lookup returns the price Entry, the Source string the panel
// surfaces, and ok==true when (provider, model) resolved against any
// tier.
//
// Returns (zero Entry, "", false) when no tier had the model. The
// caller (Tracker.Lookup in PR2; per-provider adapter RecordResponse
// in PR4) then surfaces NotConfigured{Reason:"unknown-model:<id>"}
// per plan §"Pricing table" line 388.
//
// Lookup formats the key as "<provider>/<model>" — the canonical form
// every tier uses. Empty provider or model returns (zero, "", false).
func (r *Resolver) Lookup(provider, model string) (Entry, string, bool) {
	if provider == "" || model == "" {
		return Entry{}, "", false
	}
	key := provider + "/" + model

	if entry, ok := r.override.Models[key]; ok {
		return entry, r.override.Source, true
	}
	if entry, ok := r.registry.Models[key]; ok {
		return entry, r.registry.Source, true
	}
	if entry, ok := r.embedded.Models[key]; ok {
		return entry, r.embedded.Source, true
	}
	return Entry{}, "", false
}

// SetRegistry replaces the resolver's registry tier. Used by the
// registry refresh ticker (PR5/PR6) to pick up a hot-reloaded table
// without rebuilding the Resolver from scratch. Single-writer
// discipline expected — callers serialise through their own mutex.
func (r *Resolver) SetRegistry(table Table) {
	r.registry = table
}

// HasModel reports whether ANY tier has the (provider, model) key.
// Convenience helper for callers that only need the existence check;
// equivalent to discarding the first two return values from Lookup.
func (r *Resolver) HasModel(provider, model string) bool {
	_, _, ok := r.Lookup(provider, model)
	return ok
}

// CanonicalKey formats the (provider, model) pair into the canonical
// "<provider>/<model>" string the embedded and registry tables key on.
// Surfaced for tests and for adapters that want to log the lookup
// key for audit purposes.
func CanonicalKey(provider, model string) string {
	return strings.TrimSpace(provider) + "/" + strings.TrimSpace(model)
}

// SourceLookup returns a narrowed lookup function satisfying
// quota.PricingResolver — Lookup(provider, model) -> (source, ok).
// Discards the Entry value-type so the quota package's narrow seam
// (no Entry import) stays one-way: quota → pricing import is forbidden
// (would create a cycle once the engine wires both packages); pricing
// → quota is fine but unnecessary.
//
// Callers wiring the engine pass &pricing.Resolver{} via a closure:
//
//	tracker := quota.NewTrackerWithPricing(
//	    cfg.Quota.Store.Backend,
//	    resolverFunc(pricingResolver),
//	)
//
// where resolverFunc adapts the three-return-value Lookup down.
//
// SourceLookup is exposed so engine wire-up code can express the
// adapter inline without leaking the pricing.Resolver shape into
// the engine signature.
func (r *Resolver) SourceLookup(provider, model string) (string, bool) {
	_, source, ok := r.Lookup(provider, model)
	return source, ok
}

// Sourced is a tiny adapter wrapping a *Resolver into a value with a
// two-return-value Lookup method matching quota.PricingResolver. Use
// when a function-value adapter is awkward (e.g. embedded into a
// struct that needs to expose the resolver for hot-swap).
type Sourced struct {
	Resolver *Resolver
}

// Lookup satisfies quota.PricingResolver. Returns ("", false) when
// the embedded resolver is nil — defensive against zero-value
// constructors.
func (s Sourced) Lookup(provider, model string) (string, bool) {
	if s.Resolver == nil {
		return "", false
	}
	return s.Resolver.SourceLookup(provider, model)
}
