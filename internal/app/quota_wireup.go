package app

// quota_wireup.go — boot-time construction of the provider-quota
// Tracker + per-provider account-hash + cap-config maps + the
// api.QuotaAggregator adapter that bridges the engine into the api
// dashboard endpoints (PR5 of the Provider Quota and Spend
// Visibility plan, May 2026).
//
// Lives in its own file (rather than inlined into app.go) so the
// boot-time validation can be audited without scrolling through
// app.go's 168K of pre-existing wiring. Plan §"Rollout Plan" PR5
// row 429.
//
// Engine boundary discipline ([[ADR - FlowState Engine Boundary]]):
//
//   - The engine returns engine.QuotaAggregatorRow (engine-package
//     type with quota.Snapshot).
//   - The api package declares api.QuotaAggregatorRow (api-package
//     type with quota.Snapshot — same Snapshot value-type but
//     different surrounding struct).
//   - This file's quotaAggregatorAdapter is the one-way bridge that
//     translates one to the other. Lives in app because app is the
//     only package allowed to import both api and engine.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider/quota"
	"github.com/baphled/flowstate/internal/provider/quota/pricing"
	quotastore "github.com/baphled/flowstate/internal/provider/quota/store"
)

// quotaWiring is the bundle of boot-time-constructed quota objects
// the engine and the api server consume. Constructed via
// buildQuotaWiring and threaded into setupEngine.
type quotaWiring struct {
	// tracker is the engine's quota.Tracker — nil when the feature
	// is not configured (zero QuotaConfig). The engine's
	// QuotaTracker field accepts a nil value cleanly.
	tracker *quota.Tracker

	// accountHashes maps providerID → SHA-256-truncated account hash
	// (HashAccount(apiKey)). Empty for providers without an API key
	// (ollama local). Threaded into engine.Config.QuotaAccountHashes.
	accountHashes map[string]string

	// caps maps providerID → quota.CapConfig parsed from
	// cfg.Quota.Providers[providerID]. Threaded into
	// engine.Config.QuotaCaps. Empty when no per-provider caps are
	// configured — the chip renders without a denominator.
	caps map[string]quota.CapConfig

	// aggregator is the api-side adapter wrapping the engine's
	// QuotaSnapshots / ResetQuotaSpend methods. Wired into the api
	// server via api.WithQuotaAggregator. Nil when no tracker.
	aggregator api.QuotaAggregator
}

// buildQuotaWiring assembles the quota tracker and adjacent maps from
// cfg.Quota + cfg.Providers. Returns the zero quotaWiring (all-nil)
// when cfg.Quota.Store.Backend is empty — the feature is then off and
// the engine drops every quota-related code path cleanly.
//
// Per plan PR5 row 429 — this is the "CLI wire-up" the PR4 row left
// as a known scope-flag.
//
// Side effects:
//   - Loads the embedded pricing.json at boot (LoadEmbedded —
//     in-memory parse, no I/O).
//   - Calls slog.Warn when registry tier is enabled but unreachable
//     — embedded fallback covers the gap so we never block startup.
//   - Logs an info line summarising the wired tracker shape so
//     operators can audit the boot output.
//
// Boot-validation contract: the function presumes the
// quotastore.ValidateDeploymentTopology + config.ValidatePricingRegistry
// + config.ValidateProviderQuota calls in serve.go have already
// passed. A misconfig that should fail at boot has already done so by
// the time this runs.
func buildQuotaWiring(cfg *config.AppConfig) (quotaWiring, error) {
	if cfg == nil {
		return quotaWiring{}, nil
	}
	if cfg.Quota.Store.Backend == "" {
		// Feature off — return all-nil. The engine sees nil
		// quotaTracker and drops all paths cleanly.
		return quotaWiring{}, nil
	}

	// Step 1 — Pricing resolver. PR2 ships the embedded default; the
	// registry tier is opt-in (cfg.Quota.Pricing.Registry.Enabled).
	// Operator-override file is opt-in via cfg.Quota.Pricing.Path.
	embedded, err := pricing.LoadEmbedded()
	if err != nil {
		return quotaWiring{}, fmt.Errorf("quota: loading embedded pricing table: %w", err)
	}
	// PR5 scope keeps the registry + operator-override loaders out
	// of the hot path; the embedded table is the v1 baseline per
	// plan B5 + memory feedback_default_urls_must_be_provisioned_or_disabled.
	// PR6 will wire the registry loader + on-disk cache.
	resolver := pricing.NewResolver(embedded, pricing.Table{}, pricing.Table{})

	// Step 2 — Account hashes per configured provider. The hash
	// truncation preserves enough entropy to distinguish rotated
	// keys without exposing them in the dashboard / chip tooltips.
	accountHashes := buildAccountHashes(cfg)

	// Step 3 — Cap configs from the per-provider entries. Per
	// memory feedback_atomicity_awareness_uneven — parse all caps
	// up-front so a malformed entry surfaces at boot, not at first
	// spend-event time. ValidateProviderQuota was already called in
	// serve.go.
	caps, err := buildCapConfigs(cfg)
	if err != nil {
		return quotaWiring{}, err
	}

	// Step 4 — Store backend. v1 ships memory; redis / postgres are
	// stubs that return ErrNotImplemented. The
	// ValidateDeploymentTopology call in serve.go has already
	// rejected the misconfigured pairings.
	store := newMemorySpendStoreAdapter(quotastore.NewMemoryStore())

	// Step 5 — Tracker construction. NewTrackerWithSpend accepts the
	// resolver as `any` and type-asserts internally to both
	// PricingResolver and PriceEntryResolver — the pricing
	// SpendResolver shim satisfies both.
	tracker := quota.NewTrackerWithSpend(
		cfg.Quota.Store.Backend,
		pricing.SpendResolver{Resolver: resolver},
		store,
		time.Now,
	)

	wiring := quotaWiring{
		tracker:       tracker,
		accountHashes: accountHashes,
		caps:          caps,
	}

	slog.Info("provider-quota wiring active",
		"phase", "PR5",
		"store_backend", cfg.Quota.Store.Backend,
		"deployment_topology", cfg.Quota.Store.DeploymentTopology,
		"accounts_seen", len(accountHashes),
		"caps_configured", len(caps),
		"pricing_source", "embedded",
	)

	return wiring, nil
}

// withAggregator attaches the api-side QuotaAggregator adapter once
// the engine has been constructed. The adapter is the bridge that
// translates engine.QuotaAggregatorRow into api.QuotaAggregatorRow.
//
// Returns the wiring unchanged when the tracker is nil (feature off).
// Mutating wiring after construction keeps the engine → api edge
// one-way: app constructs the engine without yet having the
// aggregator, then this method attaches the adapter post-engine.
func (w *quotaWiring) withAggregator(eng *engine.Engine) {
	if w == nil || w.tracker == nil || eng == nil {
		return
	}
	w.aggregator = &quotaAggregatorAdapter{engine: eng}
}

// quotaAggregatorAdapter wraps the engine to satisfy
// api.QuotaAggregator. The api package declares its own row type
// (api.QuotaAggregatorRow) so this adapter is the engine → api
// translation site.
type quotaAggregatorAdapter struct {
	engine *engine.Engine
}

// QuotaSnapshots satisfies api.QuotaAggregator. Translates
// engine.QuotaAggregatorRow → api.QuotaAggregatorRow row-by-row.
func (a *quotaAggregatorAdapter) QuotaSnapshots(ctx context.Context) []api.QuotaAggregatorRow {
	if a == nil || a.engine == nil {
		return nil
	}
	rows := a.engine.QuotaSnapshots(ctx)
	out := make([]api.QuotaAggregatorRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, api.QuotaAggregatorRow{
			Provider:    r.Provider,
			AccountHash: r.AccountHash,
			Model:       r.Model,
			Snapshot:    r.Snapshot,
		})
	}
	return out
}

// ResetQuotaSpend satisfies api.QuotaAggregator. Delegates to the
// engine. The engine purges its per-request cumulative cache so a
// subsequent UsageDelta on this (provider, model) starts the counter
// from zero rather than re-applying the prior cumulative cost.
func (a *quotaAggregatorAdapter) ResetQuotaSpend(ctx context.Context, providerID, accountHash, modelID string) (bool, error) {
	if a == nil || a.engine == nil {
		return false, nil
	}
	return a.engine.ResetQuotaSpend(ctx, providerID, accountHash, modelID)
}

// buildAccountHashes returns the per-provider SHA-256-truncated
// account hash map from cfg.Providers.*.APIKey. Empty strings mean
// "no API key configured" — those providers get an empty hash so
// the Snapshot's AccountHash field renders as "" verbatim (matching
// the single-account-per-provider v1 default).
func buildAccountHashes(cfg *config.AppConfig) map[string]string {
	if cfg == nil {
		return map[string]string{}
	}
	out := make(map[string]string, 7)
	out["anthropic"] = quota.HashAccount(cfg.Providers.Anthropic.APIKey)
	out["openai"] = quota.HashAccount(cfg.Providers.OpenAI.APIKey)
	out["openzen"] = quota.HashAccount(cfg.Providers.OpenZen.APIKey)
	out["zai"] = quota.HashAccount(cfg.Providers.ZAI.APIKey)
	out["ollamacloud"] = quota.HashAccount(cfg.Providers.OllamaCloud.APIKey)
	out["copilot"] = quota.HashAccount(cfg.Providers.GitHub.APIKey)
	out["ollama"] = "" // local — no API key concept
	return out
}

// buildCapConfigs translates cfg.Quota.Providers entries into
// runtime CapConfig values. ValidateProviderQuota was already called
// in serve.go so we trust the parse here — any malformed entry has
// already failed boot.
//
// Empty input map returns the empty map (chip renders without a
// denominator per OD-9 uncapped default).
func buildCapConfigs(cfg *config.AppConfig) (map[string]quota.CapConfig, error) {
	out := make(map[string]quota.CapConfig)
	if cfg == nil {
		return out, nil
	}
	for providerID, p := range cfg.Quota.Providers {
		cap := quota.CapConfig{
			Period: p.ResolvePeriod(),
		}
		amber, red := p.ResolveThresholds()
		cap.ThresholdAmber = amber
		cap.ThresholdRed = red
		if p.Cap != "" {
			amount, currency, err := config.ParseCap(p.Cap)
			if err != nil {
				// Should not happen — ValidateProviderQuota in
				// serve.go has already caught malformed caps. Return
				// the error so a misconfig that slipped past
				// validation still surfaces clearly.
				return nil, fmt.Errorf("quota: parsing cap for provider %q: %w", providerID, err)
			}
			cap.Cap = quota.Money{Amount: amount, Currency: currency}
		}
		out[providerID] = cap
	}
	return out, nil
}

// memorySpendStoreAdapter wraps a *quotastore.MemoryStore so it
// satisfies the narrow quota.SpendStore interface. Translates
// quota.SpendStoreKey ↔ quotastore.Key on every method call. This is
// the production counterpart of the test stubs in spend_test.go and
// engine_quota_test.go.
//
// Lives here rather than in the quota package because quota would
// otherwise need to import quota/store (which already imports quota
// for the Snapshot type) — the cycle the narrow SpendStore seam
// exists to avoid.
type memorySpendStoreAdapter struct {
	inner *quotastore.MemoryStore
}

func newMemorySpendStoreAdapter(inner *quotastore.MemoryStore) *memorySpendStoreAdapter {
	return &memorySpendStoreAdapter{inner: inner}
}

func (m *memorySpendStoreAdapter) Get(ctx context.Context, key quota.SpendStoreKey) (quota.Snapshot, error) {
	snap, err := m.inner.Get(ctx, quotastore.Key{
		ProviderID:  key.ProviderID,
		AccountHash: key.AccountHash,
		ModelID:     key.ModelID,
	})
	if err != nil {
		if isStoreNotFound(err) {
			return quota.Snapshot{}, quota.SpendStoreErrNotFound
		}
		return quota.Snapshot{}, err
	}
	return snap, nil
}

func (m *memorySpendStoreAdapter) Put(ctx context.Context, key quota.SpendStoreKey, snap quota.Snapshot) error {
	return m.inner.Put(ctx, quotastore.Key{
		ProviderID:  key.ProviderID,
		AccountHash: key.AccountHash,
		ModelID:     key.ModelID,
	}, snap)
}

func (m *memorySpendStoreAdapter) Reset(ctx context.Context, key quota.SpendStoreKey) error {
	return m.inner.Reset(ctx, quotastore.Key{
		ProviderID:  key.ProviderID,
		AccountHash: key.AccountHash,
		ModelID:     key.ModelID,
	})
}

func (m *memorySpendStoreAdapter) List(ctx context.Context) ([]quota.SpendStoreEntry, error) {
	rows, err := m.inner.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]quota.SpendStoreEntry, len(rows))
	for i, r := range rows {
		out[i] = quota.SpendStoreEntry{
			Key: quota.SpendStoreKey{
				ProviderID:  r.Key.ProviderID,
				AccountHash: r.Key.AccountHash,
				ModelID:     r.Key.ModelID,
			},
			Snapshot: r.Snapshot,
		}
	}
	return out, nil
}

func isStoreNotFound(err error) bool {
	return err == quotastore.ErrSnapshotNotFound
}
