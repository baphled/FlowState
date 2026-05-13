// Package engine_test — PR4 provider_quota emission cadence specs.
//
// Pins the engine-side seams the Stream goroutine consumes:
//
//  1. buildProviderQuotaChunk returns a provider_quota StreamChunk
//     whose Content is a JSON payload matching api.sseProviderQuota's
//     wire shape (FROZEN since PR1 commit ef40f9b0).
//  2. recordQuotaSpend forwards UsageDelta into the Tracker so the
//     next BuildProviderQuotaChunk surfaces a TokenSpend variant.
//  3. makePostTurnQuotaEmitter is nil-safe when the tracker is unwired
//     and emits at most one chunk per call when wired.
//
// Plan §"Engine integration / spend accumulation rules (A4 resolution)"
// lines 299-318 + §"Rollout Plan" PR4 row 428.
package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/quota"
	"github.com/baphled/flowstate/internal/provider/quota/store"
)

// quotaStubResolver implements quota.PriceEntryResolver +
// quota.PricingResolver for the engine quota emission specs.
type quotaStubResolver struct {
	mu      sync.Mutex
	entries map[string]quota.PriceEntry
}

func newQuotaStubResolver() *quotaStubResolver {
	return &quotaStubResolver{entries: make(map[string]quota.PriceEntry)}
}

func (r *quotaStubResolver) Lookup(p, m string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.entries[p+"/"+m]
	if !ok {
		return "", false
	}
	return "stub-resolver", true
}

func (r *quotaStubResolver) Entry(p, m string) (quota.PriceEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[p+"/"+m]
	return e, ok
}

func (r *quotaStubResolver) seed(p, m, currency string, inputPerMillion, outputPerMillion float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[p+"/"+m] = quota.PriceEntry{
		Currency:         currency,
		InputPerMillion:  inputPerMillion,
		OutputPerMillion: outputPerMillion,
	}
}

// memSpendShim adapts store.MemoryStore into quota.SpendStore — the
// production engine wires the same shim at boot time. Duplicated here
// so the engine test does not depend on cli wire-up code.
type memSpendShim struct{ inner *store.MemoryStore }

func newMemSpendShim() *memSpendShim { return &memSpendShim{inner: store.NewMemoryStore()} }

func (m *memSpendShim) Get(ctx context.Context, key quota.SpendStoreKey) (quota.Snapshot, error) {
	snap, err := m.inner.Get(ctx, store.Key{
		ProviderID:  key.ProviderID,
		AccountHash: key.AccountHash,
		ModelID:     key.ModelID,
	})
	if err != nil {
		if errors.Is(err, store.ErrSnapshotNotFound) {
			return quota.Snapshot{}, quota.SpendStoreErrNotFound
		}
		return quota.Snapshot{}, err
	}
	return snap, nil
}

func (m *memSpendShim) Put(ctx context.Context, key quota.SpendStoreKey, snap quota.Snapshot) error {
	return m.inner.Put(ctx, store.Key{
		ProviderID:  key.ProviderID,
		AccountHash: key.AccountHash,
		ModelID:     key.ModelID,
	}, snap)
}

func (m *memSpendShim) Reset(ctx context.Context, key quota.SpendStoreKey) error {
	return m.inner.Reset(ctx, store.Key{
		ProviderID:  key.ProviderID,
		AccountHash: key.AccountHash,
		ModelID:     key.ModelID,
	})
}

// errorListSpendShim is a SpendStore whose List always returns an
// error. Used by the REV-2 'suppress-to-nil branch' spec to drive
// the QuotaSnapshots error path without race conditions on a real
// MemoryStore.
type errorListSpendShim struct{}

func (errorListSpendShim) Get(_ context.Context, _ quota.SpendStoreKey) (quota.Snapshot, error) {
	return quota.Snapshot{}, quota.SpendStoreErrNotFound
}
func (errorListSpendShim) Put(_ context.Context, _ quota.SpendStoreKey, _ quota.Snapshot) error {
	return nil
}
func (errorListSpendShim) Reset(_ context.Context, _ quota.SpendStoreKey) error { return nil }
func (errorListSpendShim) List(_ context.Context) ([]quota.SpendStoreEntry, error) {
	return nil, errors.New("simulated store list failure")
}

// errorGetSpendShim is a SpendStore whose Get always returns a
// non-NotFound error. Used by the REV-2 ResetSpend error-propagation
// spec — ResetSpend's Get-before-Reset probe surfaces the error
// verbatim.
type errorGetSpendShim struct{}

func (errorGetSpendShim) Get(_ context.Context, _ quota.SpendStoreKey) (quota.Snapshot, error) {
	return quota.Snapshot{}, errors.New("simulated store get failure")
}
func (errorGetSpendShim) Put(_ context.Context, _ quota.SpendStoreKey, _ quota.Snapshot) error {
	return nil
}
func (errorGetSpendShim) Reset(_ context.Context, _ quota.SpendStoreKey) error { return nil }
func (errorGetSpendShim) List(_ context.Context) ([]quota.SpendStoreEntry, error) {
	return nil, nil
}

func (m *memSpendShim) List(ctx context.Context) ([]quota.SpendStoreEntry, error) {
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

// newQuotaEngine constructs an Engine wired with a Tracker bound to
// resolver + a MemoryStore-backed SpendStore. Minimal cfg — the
// emission helpers don't need Stream wiring or the full provider
// fixture chain.
func newQuotaEngine(resolver any) *engine.Engine {
	tracker := quota.NewTrackerWithSpend("memory", resolver, newMemSpendShim(), nil)
	return engine.New(engine.Config{
		Manifest: agent.Manifest{
			ID:           "quota-engine-test",
			Name:         "Quota Engine Test",
			Instructions: agent.Instructions{SystemPrompt: "sys"},
		},
		QuotaTracker: tracker,
		QuotaAccountHashes: map[string]string{
			"anthropic": "deadbeef1234",
		},
		QuotaCaps: map[string]quota.CapConfig{
			"anthropic": {
				Cap:    quota.Money{Amount: 5000, Currency: "USD"},
				Period: "monthly",
			},
		},
	})
}

var _ = Describe("Engine provider_quota emission (PR4 — plan lines 314-316)", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Context("buildProviderQuotaChunk degradation gates", func() {
		It("returns hasQuota=false when the engine has no tracker wired (PR1 dormant path)", func() {
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{
					ID:           "no-tracker",
					Name:         "no tracker",
					Instructions: agent.Instructions{SystemPrompt: "sys"},
				},
			})
			req := &provider.ChatRequest{Provider: "anthropic", Model: "claude-opus-4-7"}
			_, ok := eng.BuildProviderQuotaChunkForTest(ctx, req)
			Expect(ok).To(BeFalse(),
				"engine without QuotaTracker MUST suppress the chunk so PR1's dormant wire shape stays inert")
		})

		It("returns hasQuota=false when provider or model is empty (degraded request)", func() {
			eng := newQuotaEngine(newQuotaStubResolver())
			_, ok := eng.BuildProviderQuotaChunkForTest(ctx, &provider.ChatRequest{Provider: "", Model: "x"})
			Expect(ok).To(BeFalse())
			_, ok = eng.BuildProviderQuotaChunkForTest(ctx, &provider.ChatRequest{Provider: "y", Model: ""})
			Expect(ok).To(BeFalse())
		})

		It("returns a NotConfigured chunk for an unregistered provider (no-adapter-registered path)", func() {
			eng := newQuotaEngine(newQuotaStubResolver())
			req := &provider.ChatRequest{Provider: "anthropic", Model: "claude-opus-4-7"}
			chunk, ok := eng.BuildProviderQuotaChunkForTest(ctx, req)
			Expect(ok).To(BeTrue(),
				"the Tracker's no-adapter-registered NotConfigured Snapshot still satisfies IsValid()")
			Expect(chunk.EventType).To(Equal("provider_quota"))
			var payload struct {
				Variant       string `json:"variant"`
				NotConfigured struct {
					Reason string `json:"reason"`
				} `json:"not_configured"`
			}
			Expect(json.Unmarshal([]byte(chunk.Content), &payload)).To(Succeed())
			Expect(payload.Variant).To(Equal("not_configured"))
			Expect(payload.NotConfigured.Reason).To(Equal("no-adapter-registered"))
		})
	})

	Context("TokenSpend emission after RecordSpend (snapshot-not-increment dedupe)", func() {
		It("ticks the chip's spend figure on each chunk's cumulative output", func() {
			resolver := newQuotaStubResolver()
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
			eng := newQuotaEngine(resolver)

			// Drive three chunks for the same request — the
			// snapshot-not-increment rule means the final spend is the
			// cost of the HIGHEST cumulative, not the sum-of-deltas.
			//
			// Chunk math (Anthropic Opus rates):
			//   100 in × $15/M + 350 out × $75/M = 2.775¢ → 3¢
			// Sum-of-deltas trap would compute:
			//   100 × $15/M + (0+200+350) × $75/M = 4.65¢ → 5¢
			req := "req-1"
			eng.RecordQuotaSpendForTest(ctx, "anthropic", "claude-opus-4-7",
				&provider.UsageDelta{InputTokens: 100, OutputTokens: 0, RequestID: req})
			eng.RecordQuotaSpendForTest(ctx, "anthropic", "claude-opus-4-7",
				&provider.UsageDelta{InputTokens: 100, OutputTokens: 200, RequestID: req})
			eng.RecordQuotaSpendForTest(ctx, "anthropic", "claude-opus-4-7",
				&provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: req})

			chunk, ok := eng.BuildProviderQuotaChunkForTest(ctx, &provider.ChatRequest{
				Provider: "anthropic", Model: "claude-opus-4-7",
			})
			Expect(ok).To(BeTrue())
			Expect(chunk.EventType).To(Equal("provider_quota"))

			var payload struct {
				Variant     string `json:"variant"`
				AccountHash string `json:"account_hash"`
				TokenSpend  struct {
					SpentMinor     int64  `json:"spent_minor"`
					SpentCurrency  string `json:"spent_currency"`
					SpentUSDMinor  int64  `json:"spent_usd_minor"`
					CapMinor       int64  `json:"cap_minor"`
					CapCurrency    string `json:"cap_currency"`
					Period         string `json:"period"`
					ThresholdAmber int    `json:"threshold_amber"`
					ThresholdRed   int    `json:"threshold_red"`
				} `json:"token_spend"`
			}
			Expect(json.Unmarshal([]byte(chunk.Content), &payload)).To(Succeed())
			Expect(payload.Variant).To(Equal("token_spend"))
			Expect(payload.AccountHash).To(Equal("deadbeef1234"),
				"engine stamps the configured QuotaAccountHashes[provider] onto every chunk")
			Expect(payload.TokenSpend.SpentMinor).To(Equal(int64(3)),
				"snapshot-not-increment dedupe MUST land 3¢ — sum-of-deltas would land 5¢")
			Expect(payload.TokenSpend.SpentCurrency).To(Equal("USD"))
			Expect(payload.TokenSpend.SpentUSDMinor).To(Equal(int64(3)))
			Expect(payload.TokenSpend.CapMinor).To(Equal(int64(5000)))
			Expect(payload.TokenSpend.CapCurrency).To(Equal("USD"))
			Expect(payload.TokenSpend.Period).To(Equal("monthly"))
			Expect(payload.TokenSpend.ThresholdAmber).To(Equal(80),
				"OD-9 default amber threshold")
			Expect(payload.TokenSpend.ThresholdRed).To(Equal(95),
				"OD-9 default red threshold")
		})
	})

	Context("QuotaSnapshots (PR5 dashboard aggregator — provider_quota.go:318)", func() {
		// REV-2 backfill — pins the engine-side aggregator surface
		// the PR5 GET /api/v1/providers/quota handler iterates. The
		// existing PR4 specs only exercised the per-turn emitter, not
		// the multi-row dashboard read.
		It("returns nil when the engine has no tracker wired", func() {
			// Handler at quota_dashboard.go:96 iterates the result and
			// uses len(entries) for the slice capacity hint — nil-safe.
			// The 'nil slice protection' the reviewer flagged means the
			// handler's range over nil is a zero-iteration loop, not a
			// panic.
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{
					ID:           "no-tracker-snapshots",
					Name:         "no tracker snapshots",
					Instructions: agent.Instructions{SystemPrompt: "sys"},
				},
			})
			rows := eng.QuotaSnapshots(ctx)
			Expect(rows).To(BeEmpty(),
				"engine without QuotaTracker MUST return empty so handler iteration is a no-op rather than 500-ing the dashboard")
		})

		It("returns empty result when the Store List call errors (suppress-to-nil branch at provider_quota.go:323-330)", func() {
			// Tracker.Snapshots surfaces ctx.Err() / store errors as
			// (nil, err). Engine.QuotaSnapshots suppresses to nil so a
			// transient store hiccup degrades the dashboard to empty
			// rather than 500-ing the whole surface. Drive this via a
			// shim whose List always errors.
			tracker := quota.NewTrackerWithSpend("memory", newQuotaStubResolver(), &errorListSpendShim{}, nil)
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{
					ID:           "list-error-snapshots",
					Name:         "list error snapshots",
					Instructions: agent.Instructions{SystemPrompt: "sys"},
				},
				QuotaTracker: tracker,
			})
			rows := eng.QuotaSnapshots(ctx)
			Expect(rows).To(BeEmpty(),
				"Tracker.Snapshots error MUST be suppressed to empty result — the dashboard renders nothing rather than blanking with 500")
		})

		It("happy path returns one row per recorded (provider, account, model) tuple", func() {
			// Record spend for two distinct keys; QuotaSnapshots MUST
			// surface both as QuotaAggregatorRow rows with the partition
			// key threaded through.
			resolver := newQuotaStubResolver()
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
			resolver.seed("openai", "gpt-4o", "USD", 2.50, 10.00)
			tracker := quota.NewTrackerWithSpend("memory", resolver, newMemSpendShim(), nil)
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{
					ID:           "snapshots-happy",
					Name:         "snapshots happy",
					Instructions: agent.Instructions{SystemPrompt: "sys"},
				},
				QuotaTracker: tracker,
				QuotaAccountHashes: map[string]string{
					"anthropic": "acc-anthropic",
					"openai":    "acc-openai",
				},
				QuotaCaps: map[string]quota.CapConfig{
					"anthropic": {Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"},
					"openai":    {Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"},
				},
			})
			eng.RecordQuotaSpendForTest(ctx, "anthropic", "claude-opus-4-7",
				&provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r1"})
			eng.RecordQuotaSpendForTest(ctx, "openai", "gpt-4o",
				&provider.UsageDelta{InputTokens: 500, OutputTokens: 1000, RequestID: "r2"})

			rows := eng.QuotaSnapshots(ctx)
			Expect(rows).To(HaveLen(2),
				"two distinct (provider, account, model) writes MUST surface as two rows")

			// Order from the underlying MemoryStore is unspecified; key
			// the assertion off (provider, account, model).
			byKey := map[string]engine.QuotaAggregatorRow{}
			for _, row := range rows {
				byKey[row.Provider+"|"+row.AccountHash+"|"+row.Model] = row
			}
			anthRow, ok := byKey["anthropic|acc-anthropic|claude-opus-4-7"]
			Expect(ok).To(BeTrue(), "anthropic row present with the configured account hash threaded through")
			Expect(anthRow.Snapshot.TokenSpend).NotTo(BeNil(),
				"snapshot carries the TokenSpend variant the dashboard renders")

			openaiRow, ok := byKey["openai|acc-openai|gpt-4o"]
			Expect(ok).To(BeTrue(), "openai row present with the configured account hash threaded through")
			Expect(openaiRow.Snapshot.TokenSpend).NotTo(BeNil())
		})
	})

	Context("ResetQuotaSpend (PR5 dashboard reset — provider_quota.go:350)", func() {
		// REV-2 backfill — pins the engine-side reset surface the
		// PR5 POST /api/v1/providers/quota/reset handler calls.
		It("returns (false, nil) when the engine has no tracker wired", func() {
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{
					ID:           "no-tracker-reset",
					Name:         "no tracker reset",
					Instructions: agent.Instructions{SystemPrompt: "sys"},
				},
			})
			found, err := eng.ResetQuotaSpend(ctx, "anthropic", "acc", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred(),
				"unwired tracker MUST be a quiet no-op — handler maps (false, nil) to 404")
			Expect(found).To(BeFalse())
		})

		It("returns (false, nil) when the key has no recorded spend", func() {
			// Tracker wired but no RecordSpend call for this key — the
			// Store returns SpendStoreErrNotFound which ResetSpend maps
			// to (false, nil). Handler maps that to 404.
			resolver := newQuotaStubResolver()
			tracker := quota.NewTrackerWithSpend("memory", resolver, newMemSpendShim(), nil)
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{
					ID:           "reset-not-found",
					Name:         "reset not found",
					Instructions: agent.Instructions{SystemPrompt: "sys"},
				},
				QuotaTracker: tracker,
			})
			found, err := eng.ResetQuotaSpend(ctx, "anthropic", "acc-A", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeFalse(),
				"no Snapshot for the key MUST return (false, nil) — distinct from a Store impl error")
		})

		It("propagates Store impl errors verbatim", func() {
			// Drive a Tracker whose Store fails Get with a non-NotFound
			// error — ResetSpend returns the error verbatim, and the
			// handler maps to 500 internal_error.
			tracker := quota.NewTrackerWithSpend("memory", newQuotaStubResolver(), &errorGetSpendShim{}, nil)
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{
					ID:           "reset-store-error",
					Name:         "reset store error",
					Instructions: agent.Instructions{SystemPrompt: "sys"},
				},
				QuotaTracker: tracker,
			})
			found, err := eng.ResetQuotaSpend(ctx, "anthropic", "acc-A", "claude-opus-4-7")
			Expect(err).To(HaveOccurred(),
				"Store impl errors MUST propagate so the handler can surface 500")
			Expect(found).To(BeFalse())
		})

		It("returns (true, nil) on a successful reset of an existing snapshot", func() {
			resolver := newQuotaStubResolver()
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
			tracker := quota.NewTrackerWithSpend("memory", resolver, newMemSpendShim(), nil)
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{
					ID:           "reset-happy",
					Name:         "reset happy",
					Instructions: agent.Instructions{SystemPrompt: "sys"},
				},
				QuotaTracker: tracker,
				QuotaAccountHashes: map[string]string{
					"anthropic": "deadbeef1234",
				},
				QuotaCaps: map[string]quota.CapConfig{
					"anthropic": {Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"},
				},
			})
			eng.RecordQuotaSpendForTest(ctx, "anthropic", "claude-opus-4-7",
				&provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r"})

			found, err := eng.ResetQuotaSpend(ctx, "anthropic", "deadbeef1234", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue(),
				"a recorded snapshot MUST surface (true, nil) so the handler returns 200")

			// Second reset returns (false, nil) — the row is gone.
			found2, err := eng.ResetQuotaSpend(ctx, "anthropic", "deadbeef1234", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(found2).To(BeFalse(),
				"resetting an already-reset key MUST return (false, nil) — idempotent from the handler's perspective")
		})
	})

	Context("post-turn quota emitter (mirrors makePostTurnUsageEmitter)", func() {
		It("returns nil when the tracker is unwired", func() {
			eng := engine.New(engine.Config{
				Manifest: agent.Manifest{
					ID:           "no-tracker-pt",
					Name:         "no tracker pt",
					Instructions: agent.Instructions{SystemPrompt: "sys"},
				},
			})
			em := eng.MakePostTurnQuotaEmitterForTest(&provider.ChatRequest{
				Provider: "anthropic", Model: "claude-opus-4-7",
			})
			Expect(em).To(BeNil(),
				"unwired tracker MUST short-circuit the post-turn emitter so the runtime gate keeps degraded environments quiet")
		})

		It("emits at most one provider_quota chunk per call", func() {
			resolver := newQuotaStubResolver()
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
			eng := newQuotaEngine(resolver)
			eng.RecordQuotaSpendForTest(ctx, "anthropic", "claude-opus-4-7",
				&provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r"})

			em := eng.MakePostTurnQuotaEmitterForTest(&provider.ChatRequest{
				Provider: "anthropic", Model: "claude-opus-4-7",
			})
			Expect(em).NotTo(BeNil())

			outChan := make(chan provider.StreamChunk, 4)
			em(ctx, outChan)
			close(outChan)

			var chunks []provider.StreamChunk
			for c := range outChan {
				chunks = append(chunks, c)
			}
			Expect(chunks).To(HaveLen(1),
				"post-turn quota emitter MUST emit exactly one chunk per call so the chip pivots once per turn")
			Expect(chunks[0].EventType).To(Equal("provider_quota"))
		})
	})
})
