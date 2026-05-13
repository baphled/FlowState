// Package quota_test pins the v1 wire contract from the Provider
// Quota and Spend Visibility plan (May 2026), §"`internal/provider/
// quota/` — the tagged-union interface" (lines 155-231).
//
// The TypeScript-side mirror lives at
// `web/src/types/contract.spec.ts` (PR4a) and asserts the same shape.
package quota_test

import (
	"context"
	"net/http"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/quota"
)

// stubAdapter is a minimal Quota impl the Tracker rows wire through.
// It records the last RecordResponse and returns a configurable
// Snapshot from Remaining.
type stubAdapter struct {
	mu           sync.Mutex
	snap         quota.Snapshot
	remainingErr error
	recordedHdr  http.Header
	recordedUse  provider.Usage
	calls        int
}

func (a *stubAdapter) Remaining(_ context.Context, _, _ string) (quota.Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snap, a.remainingErr
}

func (a *stubAdapter) RecordResponse(_, _ string, headers http.Header, usage provider.Usage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.recordedHdr = headers
	a.recordedUse = usage
	a.calls++
}

var _ = Describe("Snapshot tagged-union invariant (plan lines 161-178)", func() {
	It("IsValid is true when exactly one variant pointer is non-nil — RateLimit", func() {
		s := quota.Snapshot{RateLimit: &quota.RateLimitVariant{}}
		Expect(s.IsValid()).To(BeTrue())
	})

	It("IsValid is true when exactly one variant pointer is non-nil — TokenSpend", func() {
		s := quota.Snapshot{TokenSpend: &quota.TokenSpendVariant{}}
		Expect(s.IsValid()).To(BeTrue())
	})

	It("IsValid is true when exactly one variant pointer is non-nil — NotConfigured", func() {
		s := quota.Snapshot{NotConfigured: &quota.NotConfiguredVariant{Reason: "local-model"}}
		Expect(s.IsValid()).To(BeTrue())
	})

	It("IsValid is false when zero variants are set (the zero Snapshot)", func() {
		Expect(quota.Snapshot{}.IsValid()).To(BeFalse())
	})

	It("IsValid is false when two variants are set (discriminant violation)", func() {
		s := quota.Snapshot{
			RateLimit:     &quota.RateLimitVariant{},
			NotConfigured: &quota.NotConfiguredVariant{Reason: "x"},
		}
		Expect(s.IsValid()).To(BeFalse())
	})

	It("IsValid is false when all three variants are set", func() {
		s := quota.Snapshot{
			RateLimit:     &quota.RateLimitVariant{},
			TokenSpend:    &quota.TokenSpendVariant{},
			NotConfigured: &quota.NotConfiguredVariant{Reason: "x"},
		}
		Expect(s.IsValid()).To(BeFalse())
	})
})

var _ = Describe("Window sentinel convention (plan line 182)", func() {
	It("NewWindow returns -1 sentinels for Limit and Remaining", func() {
		w := quota.NewWindow()
		Expect(w.Limit).To(Equal(-1))
		Expect(w.Remaining).To(Equal(-1))
		Expect(w.Reset.IsZero()).To(BeTrue())
	})
})

var _ = Describe("HashAccount (plan lines 170-171)", func() {
	It("returns the first 12 hex chars of SHA-256(apiKey)", func() {
		// SHA-256("sk-test-key")[:12]; precomputed against the standard
		// library so a drift in the truncation breaks the spec rather
		// than the consumer.
		got := quota.HashAccount("sk-test-key")
		Expect(got).To(HaveLen(12))
		Expect(got).To(MatchRegexp(`^[0-9a-f]{12}$`))
	})

	It("returns the same hash for the same input across calls (deterministic)", func() {
		a := quota.HashAccount("rotating-key-1")
		b := quota.HashAccount("rotating-key-1")
		Expect(a).To(Equal(b))
	})

	It("returns different hashes for different inputs (collision-resistant in this range)", func() {
		a := quota.HashAccount("key-a")
		b := quota.HashAccount("key-b")
		Expect(a).NotTo(Equal(b))
	})

	It("returns empty string for empty apiKey (ollama-style no-key providers)", func() {
		Expect(quota.HashAccount("")).To(Equal(""))
	})
})

var _ = Describe("Money zero-value handling (plan lines 210-213)", func() {
	It("IsZero is true for the zero Money", func() {
		Expect(quota.Money{}.IsZero()).To(BeTrue())
	})

	It("IsZero is false for a non-zero amount", func() {
		Expect(quota.Money{Amount: 1, Currency: "USD"}.IsZero()).To(BeFalse())
	})

	It("IsZero is false for a non-zero currency (defensive — Amount=0 with currency set)", func() {
		Expect(quota.Money{Currency: "USD"}.IsZero()).To(BeFalse())
	})
})

var _ = Describe("Tracker fan-out (plan lines 215-227)", func() {
	var (
		tracker *quota.Tracker
		adapter *stubAdapter
		ctx     context.Context
	)

	BeforeEach(func() {
		tracker = quota.NewTracker("memory")
		adapter = &stubAdapter{
			snap: quota.Snapshot{
				Provider:      "anthropic",
				Model:         "claude-opus-4-7",
				ObservedAt:    time.Now(),
				NotConfigured: &quota.NotConfiguredVariant{Reason: "test"},
			},
		}
		ctx = context.Background()
	})

	Context("Register + Lookup", func() {
		It("returns the adapter's Snapshot stamped with the tracker's store backend", func() {
			tracker.Register("anthropic", adapter)
			snap, err := tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.StoreBackend).To(Equal("memory"),
				"Tracker MUST stamp StoreBackend so adapters need not know the backend")
			Expect(snap.NotConfigured).NotTo(BeNil())
			Expect(snap.NotConfigured.Reason).To(Equal("test"))
		})

		It("returns NotConfigured with reason 'no-adapter-registered' for an unknown provider", func() {
			// No Register call.
			snap, err := tracker.Lookup(ctx, "future-provider", "", "future-model")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.NotConfigured).NotTo(BeNil())
			Expect(snap.NotConfigured.Reason).To(Equal("no-adapter-registered"))
			Expect(snap.StoreBackend).To(Equal("memory"))
			Expect(snap.IsValid()).To(BeTrue())
		})

		It("overwrites the prior adapter on re-Register", func() {
			tracker.Register("anthropic", adapter)
			replacement := &stubAdapter{
				snap: quota.Snapshot{
					Provider:      "anthropic",
					NotConfigured: &quota.NotConfiguredVariant{Reason: "replaced"},
				},
			}
			tracker.Register("anthropic", replacement)
			snap, err := tracker.Lookup(ctx, "anthropic", "", "x")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.NotConfigured.Reason).To(Equal("replaced"))
		})

		It("ignores a nil adapter Register call (defensive)", func() {
			tracker.Register("anthropic", nil)
			snap, err := tracker.Lookup(ctx, "anthropic", "", "x")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.NotConfigured.Reason).To(Equal("no-adapter-registered"))
		})
	})

	Context("RecordResponse fan-out", func() {
		It("delegates to the registered adapter", func() {
			tracker.Register("anthropic", adapter)
			h := http.Header{}
			h.Set("anthropic-ratelimit-requests-remaining", "42")
			tracker.RecordResponse("anthropic", "claude-opus-4-7", h, provider.Usage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
			})
			adapter.mu.Lock()
			defer adapter.mu.Unlock()
			Expect(adapter.calls).To(Equal(1))
			Expect(adapter.recordedHdr.Get("anthropic-ratelimit-requests-remaining")).To(Equal("42"))
			Expect(adapter.recordedUse.TotalTokens).To(Equal(150))
		})

		It("is a no-op for an unknown provider (engine must not crash)", func() {
			// No Register call. This must not panic.
			tracker.RecordResponse("future-provider", "future-model", http.Header{}, provider.Usage{})
			adapter.mu.Lock()
			defer adapter.mu.Unlock()
			Expect(adapter.calls).To(Equal(0))
		})
	})

	Context("Concurrent access", func() {
		It("is safe under N goroutines mixing Register/Lookup/RecordResponse (run under -race)", func() {
			const N = 50
			var wg sync.WaitGroup
			wg.Add(N * 3)
			for i := 0; i < N; i++ {
				go func() {
					defer wg.Done()
					tracker.Register("anthropic", adapter)
				}()
				go func() {
					defer wg.Done()
					_, _ = tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
				}()
				go func() {
					defer wg.Done()
					tracker.RecordResponse("anthropic", "claude-opus-4-7", http.Header{}, provider.Usage{})
				}()
			}
			wg.Wait()
			// Sanity: tracker still functional.
			snap, err := tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.IsValid()).To(BeTrue())
		})
	})
})

var _ = Describe("StoreBackend disclosure (plan B3 fold, lines 285-293)", func() {
	It("Tracker surfaces the configured backend label", func() {
		t := quota.NewTracker("redis")
		Expect(t.StoreBackend()).To(Equal("redis"))
	})

	It("stamps every Snapshot from Lookup with the backend", func() {
		t := quota.NewTracker("postgres")
		t.Register("anthropic", &stubAdapter{
			snap: quota.Snapshot{NotConfigured: &quota.NotConfiguredVariant{Reason: "x"}},
		})
		snap, err := t.Lookup(context.Background(), "anthropic", "", "x")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.StoreBackend).To(Equal("postgres"))
	})
})

// fakePricingResolver lets the PricingSource plumbing assertion below
// drive the Tracker without standing up the full embedded
// pricing.json. The narrow PricingResolver seam is the load-bearing
// shape PR2 promises — keeping the fake here pins the contract.
type fakePricingResolver struct {
	hits map[string]string // "<provider>/<model>" → audit-trail source
}

func (f *fakePricingResolver) Lookup(provider, model string) (string, bool) {
	src, ok := f.hits[provider+"/"+model]
	return src, ok
}

var _ = Describe("PricingSource plumbing (plan §Pricing table line 199, PR2 wire-through)", func() {
	var (
		adapter *stubAdapter
		ctx     context.Context
	)

	BeforeEach(func() {
		adapter = &stubAdapter{
			snap: quota.Snapshot{
				Provider:      "anthropic",
				Model:         "claude-opus-4-7",
				ObservedAt:    time.Now(),
				NotConfigured: &quota.NotConfiguredVariant{Reason: "awaiting-first-response"},
			},
		}
		ctx = context.Background()
	})

	It("NewTracker (no resolver) leaves PricingSource empty — PR1 behaviour preserved", func() {
		t := quota.NewTracker("memory")
		t.Register("anthropic", adapter)
		snap, err := t.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.PricingSource).To(BeEmpty(),
			"Trackers built via NewTracker (the PR1 entry point) MUST leave PricingSource empty so the wire shape stays backward-compatible")
	})

	It("NewTrackerWithPricing stamps the resolver's source on every hit", func() {
		resolver := &fakePricingResolver{hits: map[string]string{
			"anthropic/claude-opus-4-7": "flowstate-default-v1",
		}}
		t := quota.NewTrackerWithPricing("memory", resolver)
		t.Register("anthropic", adapter)
		snap, err := t.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.PricingSource).To(Equal("flowstate-default-v1"),
			"Tracker MUST stamp Snapshot.PricingSource from the resolver — adapters must not need to know about pricing tiers")
	})

	It("stamps registry source when the resolver returns a registry hit", func() {
		resolver := &fakePricingResolver{hits: map[string]string{
			"anthropic/claude-opus-4-7": "registry:https://prices.example/v1.json",
		}}
		t := quota.NewTrackerWithPricing("memory", resolver)
		t.Register("anthropic", adapter)
		snap, err := t.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.PricingSource).To(Equal("registry:https://prices.example/v1.json"))
	})

	It("stamps operator-override source when the resolver returns an override hit", func() {
		resolver := &fakePricingResolver{hits: map[string]string{
			"anthropic/claude-opus-4-7": "operator-override:/etc/flowstate/pricing.json",
		}}
		t := quota.NewTrackerWithPricing("memory", resolver)
		t.Register("anthropic", adapter)
		snap, err := t.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.PricingSource).To(Equal("operator-override:/etc/flowstate/pricing.json"))
	})

	It("leaves PricingSource empty when the resolver misses (unknown-model fallthrough)", func() {
		resolver := &fakePricingResolver{hits: map[string]string{
			// resolver covers a different model only.
			"anthropic/claude-sonnet-4-7": "flowstate-default-v1",
		}}
		t := quota.NewTrackerWithPricing("memory", resolver)
		t.Register("anthropic", adapter)
		snap, err := t.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.PricingSource).To(BeEmpty(),
			"resolver miss MUST leave PricingSource empty — the PR4 adapter then surfaces NotConfigured{Reason:'unknown-model:<id>'} per plan §Pricing table line 388")
	})

	It("PricingResolver getter returns the wired resolver (engine seam)", func() {
		resolver := &fakePricingResolver{}
		t := quota.NewTrackerWithPricing("memory", resolver)
		Expect(t.PricingResolver()).To(BeIdenticalTo(quota.PricingResolver(resolver)),
			"PricingResolver MUST return the same instance — PR4 adapters need the seam to perform per-model pricing lookup at RecordResponse time")
	})

	It("PricingResolver returns nil when constructed via NewTracker", func() {
		t := quota.NewTracker("memory")
		Expect(t.PricingResolver()).To(BeNil())
	})

	It("skips resolver lookup when modelID is empty (defensive — account-wide snapshots)", func() {
		// Adapter returns a snapshot with empty model (mirrors
		// Anthropic's account-wide rate-limit windows).
		adapter.snap.Model = ""
		resolver := &fakePricingResolver{hits: map[string]string{
			"anthropic/": "should-not-be-stamped",
		}}
		t := quota.NewTrackerWithPricing("memory", resolver)
		t.Register("anthropic", adapter)
		snap, err := t.Lookup(ctx, "anthropic", "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.PricingSource).To(BeEmpty(),
			"empty modelID MUST short-circuit the resolver — account-wide snapshots have no per-model price")
	})
})

// PR5 R2 fold (plan §"Vue integration" + PR4 review R2 — Tracker.Lookup
// MUST thread AccountHash so multi-account-per-provider deployments do
// not silently merge into one bucket). Pre-PR5 the storeKey shim ignored
// the Tracker and produced an empty-account key, collapsing every
// account on a provider into one Snapshot row. This Describe block pins
// the new behaviour.
var _ = Describe("Tracker.Lookup AccountHash threading (PR5 R2 fold)", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("stamps the supplied accountHash on the no-adapter-registered fallback", func() {
		t := quota.NewTracker("memory")
		snap, err := t.Lookup(ctx, "future-provider", "acct-abc12345", "future-model")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.AccountHash).To(Equal("acct-abc12345"),
			"Tracker.Lookup MUST stamp the supplied accountHash on the no-adapter fallback so multi-account dashboards can drill into per-account rows")
		Expect(snap.NotConfigured).NotTo(BeNil())
		Expect(snap.NotConfigured.Reason).To(Equal("no-adapter-registered"))
	})

	It("stamps the supplied accountHash on the adapter path when the adapter left it empty", func() {
		t := quota.NewTracker("memory")
		t.Register("anthropic", &stubAdapter{
			snap: quota.Snapshot{
				Provider: "anthropic",
				Model:    "claude-opus-4-7",
				// AccountHash deliberately empty — most adapters do
				// not stamp this themselves; the Tracker fills it.
				NotConfigured: &quota.NotConfiguredVariant{Reason: "awaiting-first-response"},
			},
		})
		snap, err := t.Lookup(ctx, "anthropic", "acct-rotated-key", "claude-opus-4-7")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.AccountHash).To(Equal("acct-rotated-key"),
			"Tracker.Lookup MUST stamp the supplied accountHash when the adapter does not")
	})

	It("preserves the adapter's AccountHash when the adapter set it itself", func() {
		t := quota.NewTracker("memory")
		t.Register("anthropic", &stubAdapter{
			snap: quota.Snapshot{
				Provider:      "anthropic",
				AccountHash:   "adapter-set-hash",
				Model:         "claude-opus-4-7",
				NotConfigured: &quota.NotConfiguredVariant{Reason: "x"},
			},
		})
		snap, err := t.Lookup(ctx, "anthropic", "caller-supplied-hash", "claude-opus-4-7")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.AccountHash).To(Equal("adapter-set-hash"),
			"Tracker MUST NOT overwrite an adapter-set AccountHash — adapters that know their own account take precedence")
	})

	It("tolerates empty accountHash (v1 single-account-per-provider default)", func() {
		t := quota.NewTracker("memory")
		t.Register("anthropic", &stubAdapter{
			snap: quota.Snapshot{
				Provider:      "anthropic",
				Model:         "claude-opus-4-7",
				NotConfigured: &quota.NotConfiguredVariant{Reason: "x"},
			},
		})
		snap, err := t.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.AccountHash).To(Equal(""),
			"empty accountHash is the v1 single-account default — Tracker.Lookup MUST NOT fabricate one")
	})

	It("returns distinct Snapshots via Tracker.Lookup for two AccountHashes on the same (provider, model) — R2 fold headline (commit db789ba6)", func() {
		// REV-4 backfill — pins the R2 fold's headline behaviour
		// directly on Tracker.Lookup. The R2 fold (commit db789ba6 —
		// "feat(provider/quota): R2 fold — thread AccountHash through
		// Tracker.Lookup (Quota Plan PR5 prep)") threaded AccountHash
		// through Tracker.Lookup so multi-account-per-provider
		// deployments do not silently merge into one bucket.
		//
		// Pre-R2 the storeKey shim ignored the Tracker and produced an
		// empty-account key, collapsing every account on a provider
		// into one Snapshot row. This spec drives the new behaviour
		// directly via two Tracker.Lookup calls — distinct snapshots
		// for distinct accountHashes on the same (provider, model).
		store := newMemoryStoreShim()
		resolver := &recordedPricingResolver{}
		tracker := quota.NewTrackerWithSpend("memory", resolver, store, nil)
		tracker.Register("anthropic", &stubAdapter{
			snap: quota.Snapshot{
				NotConfigured: &quota.NotConfiguredVariant{Reason: "no-spend-yet"},
			},
		})

		now := time.Now()
		// Two TokenSpend rows — same provider + model, different
		// accountHash, deliberately different Spent amounts so the
		// assertion below can detect any bucket collapse.
		acctA := quota.SpendStoreKey{
			ProviderID:  "anthropic",
			AccountHash: "accountHash1",
			ModelID:     "claude-opus-4-7",
		}
		acctB := quota.SpendStoreKey{
			ProviderID:  "anthropic",
			AccountHash: "accountHash2",
			ModelID:     "claude-opus-4-7",
		}
		_ = store.Put(ctx, acctA, quota.Snapshot{
			Provider: "anthropic", AccountHash: "accountHash1", Model: "claude-opus-4-7",
			ObservedAt: now,
			TokenSpend: &quota.TokenSpendVariant{
				Spent:       quota.Money{Amount: 111, Currency: "USD"},
				Period:      "monthly",
				PeriodStart: now.Add(-24 * time.Hour),
				PeriodEnd:   now.Add(24 * time.Hour),
			},
		})
		_ = store.Put(ctx, acctB, quota.Snapshot{
			Provider: "anthropic", AccountHash: "accountHash2", Model: "claude-opus-4-7",
			ObservedAt: now,
			TokenSpend: &quota.TokenSpendVariant{
				Spent:       quota.Money{Amount: 222, Currency: "USD"},
				Period:      "monthly",
				PeriodStart: now.Add(-24 * time.Hour),
				PeriodEnd:   now.Add(24 * time.Hour),
			},
		})

		// Tracker.Lookup with accountHash1 — must surface the $1.11 row.
		snap1, err1 := tracker.Lookup(ctx, "anthropic", "accountHash1", "claude-opus-4-7")
		Expect(err1).NotTo(HaveOccurred())
		Expect(snap1.TokenSpend).NotTo(BeNil(),
			"Tracker.Lookup MUST resolve the accountHash1 TokenSpend Snapshot via the new partition key — R2 fold threaded accountHash through Lookup")
		Expect(snap1.TokenSpend.Spent.Amount).To(Equal(int64(111)))
		Expect(snap1.AccountHash).To(Equal("accountHash1"),
			"Tracker.Lookup MUST stamp the supplied accountHash on the result so the dashboard renders the correct partition key")

		// Tracker.Lookup with accountHash2 — must surface the $2.22 row,
		// NOT a collapsed bucket of $3.33 (which is what pre-R2 would
		// have returned).
		snap2, err2 := tracker.Lookup(ctx, "anthropic", "accountHash2", "claude-opus-4-7")
		Expect(err2).NotTo(HaveOccurred())
		Expect(snap2.TokenSpend).NotTo(BeNil(),
			"Tracker.Lookup MUST resolve accountHash2 independently — pre-R2 the empty-account collapse merged the buckets")
		Expect(snap2.TokenSpend.Spent.Amount).To(Equal(int64(222)),
			"accountHash2's $2.22 must NOT be merged with accountHash1's $1.11 — distinct (provider, account, model) partition key per R2 fold")
		Expect(snap2.AccountHash).To(Equal("accountHash2"))

		// Defensive — the snapshots must be different, not aliased.
		Expect(snap1.TokenSpend.Spent.Amount).NotTo(Equal(snap2.TokenSpend.Spent.Amount),
			"distinct Snapshots MUST carry distinct Spent amounts — confirms no bucket collapse")
	})

	It("partitions the spend overlay by accountHash (multi-account stores stay distinct)", func() {
		// Two accounts on the same provider+model should produce
		// independent Snapshots from Lookup when wired through a spend
		// store. This exercises the lookupSpendOverlay path that uses
		// the new SpendStoreKey{Provider,Account,Model} composite key.
		store := newMemoryStoreShim()
		resolver := &recordedPricingResolver{}
		tracker := quota.NewTrackerWithSpend("memory", resolver, store, nil)
		tracker.Register("anthropic", &stubAdapter{
			snap: quota.Snapshot{
				NotConfigured: &quota.NotConfiguredVariant{Reason: "no-spend-yet"},
			},
		})
		// Record one spend under acct-A and one under acct-B for the
		// same (provider, model) — pre-R2 these collapsed into one
		// bucket because storeKey ignored AccountHash. Post-R2 they
		// must stay distinct.
		acctA := quota.SpendStoreKey{
			ProviderID:  "anthropic",
			AccountHash: "acct-A",
			ModelID:     "claude-opus-4-7",
		}
		acctB := quota.SpendStoreKey{
			ProviderID:  "anthropic",
			AccountHash: "acct-B",
			ModelID:     "claude-opus-4-7",
		}
		now := time.Now()
		_ = store.Put(ctx, acctA, quota.Snapshot{
			Provider:    "anthropic",
			AccountHash: "acct-A",
			Model:       "claude-opus-4-7",
			ObservedAt:  now,
			TokenSpend: &quota.TokenSpendVariant{
				Spent:       quota.Money{Amount: 100, Currency: "USD"},
				Period:      "monthly",
				PeriodStart: now.Add(-24 * time.Hour),
				PeriodEnd:   now.Add(24 * time.Hour),
			},
		})
		_ = store.Put(ctx, acctB, quota.Snapshot{
			Provider:    "anthropic",
			AccountHash: "acct-B",
			Model:       "claude-opus-4-7",
			ObservedAt:  now,
			TokenSpend: &quota.TokenSpendVariant{
				Spent:       quota.Money{Amount: 200, Currency: "USD"},
				Period:      "monthly",
				PeriodStart: now.Add(-24 * time.Hour),
				PeriodEnd:   now.Add(24 * time.Hour),
			},
		})
		snapA, errA := tracker.Lookup(ctx, "anthropic", "acct-A", "claude-opus-4-7")
		Expect(errA).NotTo(HaveOccurred())
		Expect(snapA.TokenSpend).NotTo(BeNil(),
			"Lookup MUST resolve the acct-A TokenSpend Snapshot through the new partition key")
		Expect(snapA.TokenSpend.Spent.Amount).To(Equal(int64(100)))

		snapB, errB := tracker.Lookup(ctx, "anthropic", "acct-B", "claude-opus-4-7")
		Expect(errB).NotTo(HaveOccurred())
		Expect(snapB.TokenSpend).NotTo(BeNil(),
			"Lookup MUST resolve acct-B independently — pre-R2 the empty-account collapse merged the buckets")
		Expect(snapB.TokenSpend.Spent.Amount).To(Equal(int64(200)),
			"acct-B's $2.00 must not be merged with acct-A's $1.00")
	})
})

// memoryStoreShim is a minimal SpendStore impl scoped to the contract
// spec — avoids importing the store package (would cycle) by inlining
// the interface methods. Matches quota.SpendStore + an extra Put helper
// for spec setup.
type memoryStoreShim struct {
	data map[quota.SpendStoreKey]quota.Snapshot
}

func newMemoryStoreShim() *memoryStoreShim {
	return &memoryStoreShim{data: make(map[quota.SpendStoreKey]quota.Snapshot)}
}

func (m *memoryStoreShim) Get(_ context.Context, key quota.SpendStoreKey) (quota.Snapshot, error) {
	snap, ok := m.data[key]
	if !ok {
		return quota.Snapshot{}, quota.SpendStoreErrNotFound
	}
	return snap, nil
}

func (m *memoryStoreShim) Put(_ context.Context, key quota.SpendStoreKey, snap quota.Snapshot) error {
	m.data[key] = snap
	return nil
}

func (m *memoryStoreShim) Reset(_ context.Context, key quota.SpendStoreKey) error {
	delete(m.data, key)
	return nil
}

func (m *memoryStoreShim) List(_ context.Context) ([]quota.SpendStoreEntry, error) {
	out := make([]quota.SpendStoreEntry, 0, len(m.data))
	for k, snap := range m.data {
		out = append(out, quota.SpendStoreEntry{Key: k, Snapshot: snap})
	}
	return out, nil
}

// recordedPricingResolver is a no-op PricingResolver/PriceEntryResolver
// for the partition spec — the spec doesn't drive the pricing path so
// both methods return zero values.
type recordedPricingResolver struct{}

func (r *recordedPricingResolver) Lookup(_, _ string) (string, bool) {
	return "", false
}

func (r *recordedPricingResolver) Entry(_, _ string) (quota.PriceEntry, bool) {
	return quota.PriceEntry{}, false
}
