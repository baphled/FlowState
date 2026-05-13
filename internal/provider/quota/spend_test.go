// Package quota_test — PR4 spend-accumulator + auto-reset + TokenSpend
// emission specs. Pins the engine-facing seam the engine streaming pipe
// will call (RecordSpend) plus the Lookup-side TokenSpend overlay that
// composes pricing-resolved cumulative spend into the Snapshot the
// chip's Pinia store consumes.
//
// Plan §"Engine integration / spend accumulation rules (A4 resolution)"
// lines 299-318 + §"Rollout Plan" PR4 row 428 + OD-8 auto-reset on
// PeriodStart rollover (lines 511-516) + OD-9 thresholds (lines 517-520).
package quota_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/quota"
	"github.com/baphled/flowstate/internal/provider/quota/store"
)

// memorySpendStore adapts the package-store MemoryStore into the
// narrow quota.SpendStore interface the Tracker's spend layer
// consumes. The interface duplication is intentional — keeps the
// quota package free of the store import that would otherwise cycle
// (store imports quota for the Snapshot type). The engine does the
// same adaptation at wire-up time.
type memorySpendStore struct {
	inner *store.MemoryStore
}

func newMemorySpendStore() *memorySpendStore {
	return &memorySpendStore{inner: store.NewMemoryStore()}
}

func (m *memorySpendStore) Get(ctx context.Context, key quota.SpendStoreKey) (quota.Snapshot, error) {
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

func (m *memorySpendStore) Put(ctx context.Context, key quota.SpendStoreKey, snap quota.Snapshot) error {
	return m.inner.Put(ctx, store.Key{
		ProviderID:  key.ProviderID,
		AccountHash: key.AccountHash,
		ModelID:     key.ModelID,
	}, snap)
}

// inner unwraps to the underlying MemoryStore for partition-key
// assertions in the test suite.
func (m *memorySpendStore) underlying() *store.MemoryStore { return m.inner }

// stubPricingResolver implements quota.PricingResolver with an inline
// map. Tests construct one and seed the (provider, model) keys that
// matter to the row; misses surface as NotConfigured.
type stubPricingResolver struct {
	mu      sync.Mutex
	entries map[string]quota.PriceEntry
}

func newStubPricingResolver() *stubPricingResolver {
	return &stubPricingResolver{entries: make(map[string]quota.PriceEntry)}
}

func (r *stubPricingResolver) Lookup(p, m string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.entries[p+"/"+m]
	if !ok {
		return "", false
	}
	return "stub-resolver", true
}

func (r *stubPricingResolver) Entry(p, m string) (quota.PriceEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[p+"/"+m]
	return e, ok
}

func (r *stubPricingResolver) seed(p, m, currency string, inputPerMillion, outputPerMillion float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[p+"/"+m] = quota.PriceEntry{
		Currency:         currency,
		InputPerMillion:  inputPerMillion,
		OutputPerMillion: outputPerMillion,
	}
}

// Compile-time conformance: stubPricingResolver MUST satisfy both
// quota.PricingResolver (the narrow Lookup seam) and
// quota.PriceEntryResolver (the spend-math seam PR4 adds).
var (
	_ quota.PricingResolver    = (*stubPricingResolver)(nil)
	_ quota.PriceEntryResolver = (*stubPricingResolver)(nil)
)

var _ = Describe("Tracker spend accumulator (PR4 — plan lines 299-318)", func() {
	var (
		ctx      context.Context
		resolver *stubPricingResolver
		mem      *memorySpendStore
		tracker  *quota.Tracker
	)

	BeforeEach(func() {
		ctx = context.Background()
		resolver = newStubPricingResolver()
		mem = newMemorySpendStore()
		tracker = quota.NewTrackerWithSpend("memory", resolver, mem, time.Now)
	})

	Context("snapshot-not-increment rule (A4 resolution — plan lines 306-313)", func() {
		BeforeEach(func() {
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
		})

		It("uses the highest cumulative output_tokens across the stream, never the sum", func() {
			// Three chunks for the SAME request: cumulative input stays
			// at 100; cumulative output grows 0 → 200 → 350. Per the
			// snapshot-not-increment rule the final cost MUST use 100
			// input + 350 output, NOT 100 + (0+200+350)=550 output.
			capCfg := quota.CapConfig{
				Cap:            quota.Money{Amount: 5000, Currency: "USD"}, // $50.00
				Period:         "monthly",
				ThresholdAmber: 80,
				ThresholdRed:   95,
			}
			req := "req-1"
			err := tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider:    "anthropic",
				Model:       "claude-opus-4-7",
				AccountHash: "",
				RequestID:   req,
				Usage:       &provider.UsageDelta{InputTokens: 100, OutputTokens: 0, RequestID: req},
				CapConfig:   capCfg,
			})
			Expect(err).NotTo(HaveOccurred())
			err = tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider:    "anthropic",
				Model:       "claude-opus-4-7",
				AccountHash: "",
				RequestID:   req,
				Usage:       &provider.UsageDelta{InputTokens: 100, OutputTokens: 200, RequestID: req},
				CapConfig:   capCfg,
			})
			Expect(err).NotTo(HaveOccurred())
			err = tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider:    "anthropic",
				Model:       "claude-opus-4-7",
				AccountHash: "",
				RequestID:   req,
				Usage:       &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: req},
				CapConfig:   capCfg,
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify the cumulative spend at the Tracker matches
			// 100 input × $15/M + 350 output × $75/M
			//   = 0.0015 USD + 0.02625 USD = 0.02775 USD = 2.775 cents
			//   rounded to nearest minor unit (cents) = 3
			snap, err := tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.IsValid()).To(BeTrue(), "TokenSpend overlay MUST keep discriminant invariant")
			Expect(snap.TokenSpend).NotTo(BeNil())
			Expect(snap.TokenSpend.Spent.Amount).To(Equal(int64(3)),
				"100 in × $15/M + 350 out × $75/M = 2.775¢ → rounds to 3¢; sum-of-deltas would give 8¢")
			Expect(snap.TokenSpend.Spent.Currency).To(Equal("USD"))
		})

		It("accumulates across DIFFERENT request_ids on the same key", func() {
			capCfg := quota.CapConfig{
				Cap:    quota.Money{Amount: 5000, Currency: "USD"},
				Period: "monthly",
			}
			// Request A — 100 in, 350 out = ~3¢
			err := tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "",
				RequestID: "req-A",
				Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "req-A"},
				CapConfig: capCfg,
			})
			Expect(err).NotTo(HaveOccurred())

			// Request B — same math = ~3¢. Cumulative total ~6¢.
			err = tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "",
				RequestID: "req-B",
				Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "req-B"},
				CapConfig: capCfg,
			})
			Expect(err).NotTo(HaveOccurred())

			snap, err := tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.TokenSpend).NotTo(BeNil())
			Expect(snap.TokenSpend.Spent.Amount).To(Equal(int64(6)),
				"distinct request_ids MUST accumulate as separate calls, not deduplicate to last")
		})

		It("partitions cumulative spend by (provider, account, model)", func() {
			capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}
			resolver.seed("openai", "gpt-4o", "USD", 2.50, 10.00)

			// 1M in / 1M out on each of three distinct keys.
			specs := []struct{ provider, account, model string }{
				{"anthropic", "acc-A", "claude-opus-4-7"},
				{"anthropic", "acc-B", "claude-opus-4-7"}, // same provider+model, different account
				{"openai", "acc-A", "gpt-4o"},             // different provider
			}
			for _, s := range specs {
				err := tracker.RecordSpend(ctx, quota.SpendRecord{
					Provider: s.provider, Model: s.model, AccountHash: s.account,
					RequestID: "r",
					Usage:     &provider.UsageDelta{InputTokens: 1_000_000, OutputTokens: 1_000_000, RequestID: "r"},
					CapConfig: capCfg,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			for _, s := range specs {
				key := store.Key{ProviderID: s.provider, AccountHash: s.account, ModelID: s.model}
				snap, err := mem.underlying().Get(ctx, key)
				Expect(err).NotTo(HaveOccurred(),
					"each (provider, account, model) tuple gets its own Snapshot")
				Expect(snap.TokenSpend).NotTo(BeNil())
			}
		})
	})

	Context("USD-equivalent computation via OD-6 conversion table", func() {
		It("populates SpentUSD via DefaultConversionTable for a non-USD price", func() {
			// Z.AI prices in CNY per plan line 369: 5.00 input, 20.00 output.
			resolver.seed("zai", "glm-4.6", "CNY", 5.00, 20.00)

			// 1M input + 1M output = 5 CNY + 20 CNY = 25 CNY major
			//   = 2500 fen (minor units; CNY divides 100 fen per yuan)
			//   USD-equivalent: 2500 / 7.23 ≈ 345.8 → rounds to 346 cents
			capCfg := quota.CapConfig{
				Cap:    quota.Money{Amount: 50000, Currency: "CNY"}, // 500 CNY = ~$69
				Period: "monthly",
			}
			err := tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "zai", Model: "glm-4.6", AccountHash: "",
				RequestID: "r",
				Usage:     &provider.UsageDelta{InputTokens: 1_000_000, OutputTokens: 1_000_000, RequestID: "r"},
				CapConfig: capCfg,
			})
			Expect(err).NotTo(HaveOccurred())

			snap, err := tracker.Lookup(ctx, "zai", "glm-4.6")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.TokenSpend).NotTo(BeNil())
			Expect(snap.TokenSpend.Spent.Amount).To(Equal(int64(2500)),
				"native spend in CNY minor units (fen)")
			Expect(snap.TokenSpend.Spent.Currency).To(Equal("CNY"))
			Expect(snap.TokenSpend.SpentUSD.Currency).To(Equal("USD"))
			// 2500 / 7.23 = 345.78 → round to 346
			Expect(snap.TokenSpend.SpentUSD.Amount).To(Equal(int64(346)),
				"USD-equivalent via OD-6 default conversion table (7.23 CNY = 1 USD)")
		})

		It("leaves SpentUSD equal to Spent when the price is in USD", func() {
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
			capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}
			err := tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "",
				RequestID: "r",
				Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r"},
				CapConfig: capCfg,
			})
			Expect(err).NotTo(HaveOccurred())
			snap, _ := tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
			Expect(snap.TokenSpend.Spent.Amount).To(Equal(snap.TokenSpend.SpentUSD.Amount))
			Expect(snap.TokenSpend.SpentUSD.Currency).To(Equal("USD"))
		})
	})

	Context("NotConfigured fallback when pricing absent (plan line 388)", func() {
		It("surfaces NotConfigured{Reason:unknown-model:<id>} for an un-priced model", func() {
			// No seed for this model.
			capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}
			err := tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-experimental", AccountHash: "",
				RequestID: "r",
				Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r"},
				CapConfig: capCfg,
			})
			// Tracker MUST NOT error on missing pricing — operator's
			// chip surfaces the reason instead.
			Expect(err).NotTo(HaveOccurred())

			snap, err := tracker.Lookup(ctx, "anthropic", "claude-experimental")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.NotConfigured).NotTo(BeNil())
			Expect(snap.NotConfigured.Reason).To(Equal("unknown-model:claude-experimental"))
		})
	})

	Context("threshold defaults (OD-9 — plan lines 517-520)", func() {
		It("stamps green<80% / amber 80-95% / red≥95% defaults when capCfg leaves them at zero", func() {
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
			capCfg := quota.CapConfig{
				Cap:    quota.Money{Amount: 5000, Currency: "USD"},
				Period: "monthly",
				// ThresholdAmber/Red left at zero — defaults must apply.
			}
			err := tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "",
				RequestID: "r",
				Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r"},
				CapConfig: capCfg,
			})
			Expect(err).NotTo(HaveOccurred())
			snap, _ := tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
			Expect(snap.TokenSpend.ThresholdAmber).To(Equal(80))
			Expect(snap.TokenSpend.ThresholdRed).To(Equal(95))
		})

		It("honours operator-supplied thresholds without overriding", func() {
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
			capCfg := quota.CapConfig{
				Cap:            quota.Money{Amount: 5000, Currency: "USD"},
				Period:         "monthly",
				ThresholdAmber: 60,
				ThresholdRed:   90,
			}
			err := tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "",
				RequestID: "r",
				Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r"},
				CapConfig: capCfg,
			})
			Expect(err).NotTo(HaveOccurred())
			snap, _ := tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
			Expect(snap.TokenSpend.ThresholdAmber).To(Equal(60))
			Expect(snap.TokenSpend.ThresholdRed).To(Equal(90))
		})

		It("leaves thresholds at -1 when no cap configured (uncapped → always green)", func() {
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
			capCfg := quota.CapConfig{
				// Cap zero — uncapped path
				Period: "monthly",
			}
			err := tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "",
				RequestID: "r",
				Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r"},
				CapConfig: capCfg,
			})
			Expect(err).NotTo(HaveOccurred())
			snap, _ := tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
			Expect(snap.TokenSpend.Cap.IsZero()).To(BeTrue())
			Expect(snap.TokenSpend.ThresholdAmber).To(Equal(-1),
				"uncapped → -1 sentinel so chip stays green per OD-9 doc")
			Expect(snap.TokenSpend.ThresholdRed).To(Equal(-1))
		})
	})

	Context("auto-reset on PeriodStart rollover (OD-8 — plan lines 511-516)", func() {
		It("zeroes Spent and rotates Period{Start,End} when now >= prior PeriodEnd", func() {
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
			capCfg := quota.CapConfig{
				Cap:    quota.Money{Amount: 5000, Currency: "USD"},
				Period: "monthly",
			}

			// First record sits inside an April-2026 period. The Tracker's
			// nowFunc is overridden so we can simulate the rollover
			// deterministically.
			now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
			tracker = quota.NewTrackerWithSpend("memory", resolver, mem, func() time.Time { return now })

			err := tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "",
				RequestID: "r",
				Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r"},
				CapConfig: capCfg,
			})
			Expect(err).NotTo(HaveOccurred())
			snap, _ := tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
			Expect(snap.TokenSpend.PeriodStart).To(Equal(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)),
				"monthly period starts at calendar-month boundary in UTC")
			Expect(snap.TokenSpend.PeriodEnd).To(Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)),
				"monthly period ends at the start of the next calendar month in UTC")
			Expect(snap.TokenSpend.Spent.Amount).To(Equal(int64(3)))

			// Roll the clock to May 2 — past the April PeriodEnd.
			now = time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)

			// A read alone MUST detect the rollover and reset Spent.
			snap2, err := tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap2.TokenSpend).NotTo(BeNil())
			Expect(snap2.TokenSpend.Spent.Amount).To(Equal(int64(0)),
				"auto-reset on PeriodEnd rollover MUST zero Spent")
			Expect(snap2.TokenSpend.PeriodStart).To(Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)),
				"new PeriodStart is the start of the calendar month containing 'now'")
			Expect(snap2.TokenSpend.PeriodEnd).To(Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
				"new PeriodEnd is the start of the FOLLOWING calendar month")
		})

		It("does NOT reset within the current period (idempotent reads)", func() {
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
			capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}

			now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
			tracker = quota.NewTrackerWithSpend("memory", resolver, mem, func() time.Time { return now })

			err := tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "",
				RequestID: "r",
				Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r"},
				CapConfig: capCfg,
			})
			Expect(err).NotTo(HaveOccurred())

			// Advance still inside April.
			now = time.Date(2026, 4, 28, 23, 59, 0, 0, time.UTC)
			snap, _ := tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
			Expect(snap.TokenSpend.Spent.Amount).To(Equal(int64(3)),
				"reads within the period MUST NOT reset")
		})
	})

	Context("Lookup overlay semantics (composes pricing-resolved spend over adapter Snapshot)", func() {
		It("returns TokenSpend when spend is non-zero AND pricing resolved", func() {
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
			// Register an adapter that would otherwise emit RateLimit —
			// the TokenSpend overlay MUST win when spend is non-zero so
			// the chip surfaces the more actionable figure.
			adapter := &stubAdapter{snap: quota.Snapshot{
				Provider:    "anthropic",
				AccountHash: "",
				RateLimit:   &quota.RateLimitVariant{TightestPercentRemaining: 42},
			}}
			tracker.Register("anthropic", adapter)

			capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}
			_ = tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "",
				RequestID: "r",
				Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r"},
				CapConfig: capCfg,
			})
			snap, err := tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.IsValid()).To(BeTrue(), "discriminant invariant preserved")
			Expect(snap.TokenSpend).NotTo(BeNil(),
				"TokenSpend overlay MUST win over RateLimit when spend > 0")
			Expect(snap.RateLimit).To(BeNil(),
				"RateLimit MUST be cleared by the overlay (discriminator invariant)")
		})

		It("falls through to adapter Snapshot when no spend recorded for the key", func() {
			adapter := &stubAdapter{snap: quota.Snapshot{
				Provider:    "anthropic",
				AccountHash: "",
				RateLimit:   &quota.RateLimitVariant{TightestPercentRemaining: 42},
			}}
			tracker.Register("anthropic", adapter)

			snap, err := tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.RateLimit).NotTo(BeNil(),
				"no spend recorded → adapter Snapshot passes through")
			Expect(snap.TokenSpend).To(BeNil())
		})
	})

	Context("concurrency", func() {
		It("is safe under N goroutines mixing RecordSpend / Lookup (run under -race)", func() {
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
			capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}

			var wg sync.WaitGroup
			const goroutines = 16
			for i := 0; i < goroutines; i++ {
				wg.Add(2)
				go func() {
					defer wg.Done()
					_ = tracker.RecordSpend(ctx, quota.SpendRecord{
						Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "",
						RequestID: "r",
						Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r"},
						CapConfig: capCfg,
					})
				}()
				go func() {
					defer wg.Done()
					_, _ = tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
				}()
			}
			wg.Wait()
			snap, err := tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.TokenSpend).NotTo(BeNil())
		})
	})
})

var _ = Describe("Tracker.Lookup PR1/PR3 behaviour-pin (must not regress)", func() {
	It("Lookup with no registered adapter and no spend recorded returns NotConfigured{no-adapter-registered}", func() {
		// Behaviour-pin from contract_test.go:169-175 — PR4 spend overlay
		// MUST NOT change the no-adapter fallback path.
		ctx := context.Background()
		tracker := quota.NewTracker("memory")
		snap, err := tracker.Lookup(ctx, "future-provider", "future-model")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.NotConfigured).NotTo(BeNil())
		Expect(snap.NotConfigured.Reason).To(Equal("no-adapter-registered"))
	})

	It("RecordResponse (legacy headers-only path) stays a no-op when no adapter is registered", func() {
		// Behaviour-pin from contract_test.go:217-224.
		tracker := quota.NewTracker("memory")
		Expect(func() {
			tracker.RecordResponse("future-provider", "future-model", http.Header{}, provider.Usage{})
		}).NotTo(Panic())
	})
})
