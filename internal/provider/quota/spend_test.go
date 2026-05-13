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
	"encoding/json"
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

// Reset clears the Snapshot for the given key. PR5 — used by the
// "Reset spend counter" dashboard button.
func (m *memorySpendStore) Reset(ctx context.Context, key quota.SpendStoreKey) error {
	return m.inner.Reset(ctx, store.Key{
		ProviderID:  key.ProviderID,
		AccountHash: key.AccountHash,
		ModelID:     key.ModelID,
	})
}

// List returns every (Key, Snapshot) the underlying MemoryStore
// holds. PR5 — backs the dashboard aggregator.
func (m *memorySpendStore) List(ctx context.Context) ([]quota.SpendStoreEntry, error) {
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
			snap, err := tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
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

			snap, err := tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
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

			snap, err := tracker.Lookup(ctx, "zai", "", "glm-4.6")
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
			snap, _ := tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
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

			snap, err := tracker.Lookup(ctx, "anthropic", "", "claude-experimental")
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
			snap, _ := tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
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
			snap, _ := tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
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
			snap, _ := tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
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
			snap, _ := tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
			Expect(snap.TokenSpend.PeriodStart).To(Equal(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)),
				"monthly period starts at calendar-month boundary in UTC")
			Expect(snap.TokenSpend.PeriodEnd).To(Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)),
				"monthly period ends at the start of the next calendar month in UTC")
			Expect(snap.TokenSpend.Spent.Amount).To(Equal(int64(3)))

			// Roll the clock to May 2 — past the April PeriodEnd.
			now = time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)

			// A read alone MUST detect the rollover and reset Spent.
			snap2, err := tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
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
			snap, _ := tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
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
			snap, err := tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
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

			snap, err := tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
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
					_, _ = tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
				}()
			}
			wg.Wait()
			snap, err := tracker.Lookup(ctx, "anthropic", "", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.TokenSpend).NotTo(BeNil())
		})
	})
})

// REV-3 backfill — direct specs for Tracker.Snapshots (spend.go:568)
// and Tracker.ResetSpend (spend.go:616). The PR5 functional code
// shipped with only shim methods on the test store; this Describe
// block pins the Tracker behaviour itself.
var _ = Describe("Tracker.Snapshots (PR5 dashboard aggregator — spend.go:568 (Tracker.Snapshots))", func() {
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

	It("returns nil when no spend wiring (PR1/PR3 NewTracker path)", func() {
		pr1Tracker := quota.NewTracker("memory")
		rows, err := pr1Tracker.Snapshots(ctx)
		Expect(err).NotTo(HaveOccurred(),
			"PR1 tracker without spend layer MUST be a quiet no-op so the engine's QuotaSnapshots wrapper degrades cleanly")
		Expect(rows).To(BeEmpty())
	})

	It("returns an empty slice when the underlying store has no entries", func() {
		rows, err := tracker.Snapshots(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(BeEmpty(),
			"empty store MUST be (empty slice, nil error) — dashboard renders [] rather than 500")
	})

	It("returns one row per recorded (provider, account, model) tuple (single + multi-row)", func() {
		resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
		resolver.seed("openai", "gpt-4o", "USD", 2.50, 10.00)
		capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}

		// Single row first.
		err := tracker.RecordSpend(ctx, quota.SpendRecord{
			Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "acc-A",
			RequestID: "r1",
			Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r1"},
			CapConfig: capCfg,
		})
		Expect(err).NotTo(HaveOccurred())

		rows, err := tracker.Snapshots(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Key.ProviderID).To(Equal("anthropic"))
		Expect(rows[0].Key.AccountHash).To(Equal("acc-A"))
		Expect(rows[0].Key.ModelID).To(Equal("claude-opus-4-7"))
		Expect(rows[0].Snapshot.TokenSpend).NotTo(BeNil())

		// Add a second row on a distinct (provider, model) — Snapshots
		// MUST surface both.
		err = tracker.RecordSpend(ctx, quota.SpendRecord{
			Provider: "openai", Model: "gpt-4o", AccountHash: "acc-B",
			RequestID: "r2",
			Usage:     &provider.UsageDelta{InputTokens: 500, OutputTokens: 1000, RequestID: "r2"},
			CapConfig: capCfg,
		})
		Expect(err).NotTo(HaveOccurred())

		rows, err = tracker.Snapshots(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(2))
	})

	It("preserves multi-account-per-provider row separation (R2 fold headline behaviour — commit db789ba6)", func() {
		// R2 fold (commit db789ba6): pre-PR5 the storeKey shim ignored
		// the Tracker and produced an empty-account key, collapsing
		// every account on a provider into one Snapshot row.
		// Snapshots MUST surface distinct accounts on the same
		// (provider, model) as distinct rows — the dashboard renders
		// them as separate cards in multi-account deployments.
		resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
		capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}

		// Two writes — same provider + model, different accountHash.
		err := tracker.RecordSpend(ctx, quota.SpendRecord{
			Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "acc-A",
			RequestID: "r-A",
			Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r-A"},
			CapConfig: capCfg,
		})
		Expect(err).NotTo(HaveOccurred())
		err = tracker.RecordSpend(ctx, quota.SpendRecord{
			Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "acc-B",
			RequestID: "r-B",
			Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r-B"},
			CapConfig: capCfg,
		})
		Expect(err).NotTo(HaveOccurred())

		rows, err := tracker.Snapshots(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(2),
			"two accounts on the same (provider, model) MUST surface as two rows — pre-R2 they collapsed to one bucket")

		seen := map[string]bool{}
		for _, row := range rows {
			Expect(row.Key.ProviderID).To(Equal("anthropic"))
			Expect(row.Key.ModelID).To(Equal("claude-opus-4-7"))
			seen[row.Key.AccountHash] = true
		}
		Expect(seen["acc-A"]).To(BeTrue(), "acc-A row present and distinct")
		Expect(seen["acc-B"]).To(BeTrue(), "acc-B row present and distinct")
	})

	It("propagates a Store List error verbatim", func() {
		// Engine.QuotaSnapshots suppresses to empty; here we pin that
		// the Tracker propagates the error verbatim so the engine can
		// log it before suppressing. Drive via a shim whose List
		// always errors.
		listErr := errors.New("simulated store list failure")
		erroringTracker := quota.NewTrackerWithSpend("memory", resolver, &errorListShim{listErr: listErr}, time.Now)
		rows, err := erroringTracker.Snapshots(ctx)
		Expect(err).To(MatchError(listErr),
			"Tracker.Snapshots MUST surface Store.List errors verbatim so the engine layer can log them")
		Expect(rows).To(BeNil())
	})
})

var _ = Describe("Tracker.ResetSpend (PR5 dashboard reset — spend.go:616 (Tracker.ResetSpend))", func() {
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

	It("returns (false, nil) on a tracker without spend wiring", func() {
		pr1Tracker := quota.NewTracker("memory")
		found, err := pr1Tracker.ResetSpend(ctx, "anthropic", "acc-A", "claude-opus-4-7")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse(),
			"PR1 tracker MUST be a quiet no-op so the engine's ResetQuotaSpend wrapper degrades cleanly")
	})

	It("returns (false, nil) when the key has no recorded snapshot", func() {
		// No RecordSpend call — Store.Get surfaces SpendStoreErrNotFound
		// which ResetSpend maps to (false, nil) so the handler returns
		// 404 rather than 500.
		found, err := tracker.ResetSpend(ctx, "anthropic", "acc-A", "unknown-model")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())
	})

	It("returns (true, nil) on a successful reset and clears the underlying Snapshot", func() {
		resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
		capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}
		err := tracker.RecordSpend(ctx, quota.SpendRecord{
			Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "acc-A",
			RequestID: "r",
			Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r"},
			CapConfig: capCfg,
		})
		Expect(err).NotTo(HaveOccurred())

		found, err := tracker.ResetSpend(ctx, "anthropic", "acc-A", "claude-opus-4-7")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue(),
			"recorded snapshot MUST surface (true, nil) — handler maps to 200")

		// Underlying store row gone — a follow-up reset is (false, nil).
		found2, err := tracker.ResetSpend(ctx, "anthropic", "acc-A", "claude-opus-4-7")
		Expect(err).NotTo(HaveOccurred())
		Expect(found2).To(BeFalse(),
			"resetting an already-reset key is (false, nil) — handler maps to 404")
	})

	It("propagates Store impl errors verbatim", func() {
		// Drive a Tracker whose Store.Get returns a non-NotFound error;
		// ResetSpend surfaces that error so the handler can map to 500.
		getErr := errors.New("simulated store get failure")
		erroringTracker := quota.NewTrackerWithSpend("memory", resolver, &errorGetShim{getErr: getErr}, time.Now)
		found, err := erroringTracker.ResetSpend(ctx, "anthropic", "acc-A", "claude-opus-4-7")
		Expect(err).To(MatchError(getErr),
			"non-NotFound Store errors MUST propagate so the handler can surface 500")
		Expect(found).To(BeFalse())
	})

	It("partitions the reset by (provider, account, model) — does not touch peer rows", func() {
		// R2 fold corollary: resetting acc-A's spend MUST leave acc-B's
		// spend untouched. Pre-R2 the empty-account collapse would have
		// also broken this — both rows shared a bucket.
		resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
		capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}
		for _, acct := range []string{"acc-A", "acc-B"} {
			err := tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: acct,
				RequestID: "r-" + acct,
				Usage:     &provider.UsageDelta{InputTokens: 100, OutputTokens: 350, RequestID: "r-" + acct},
				CapConfig: capCfg,
			})
			Expect(err).NotTo(HaveOccurred())
		}

		found, err := tracker.ResetSpend(ctx, "anthropic", "acc-A", "claude-opus-4-7")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())

		// acc-B's snapshot MUST survive — the dashboard reset button
		// is per-row, not per-(provider, model). LookupSpend returns
		// (Snapshot, bool); the bool must be true and TokenSpend must
		// still carry the cumulative amount.
		snapB, okB := tracker.LookupSpend(ctx, "anthropic", "acc-B", "claude-opus-4-7")
		Expect(okB).To(BeTrue(),
			"acc-B's spend MUST survive a reset of acc-A — partition is by (provider, account, model)")
		Expect(snapB.TokenSpend).NotTo(BeNil())
		Expect(snapB.TokenSpend.Spent.Amount).To(BeNumerically(">", int64(0)),
			"acc-B's cumulative spend MUST be preserved across acc-A's reset")

		// And acc-A is gone — confirms the reset actually fired.
		_, okA := tracker.LookupSpend(ctx, "anthropic", "acc-A", "claude-opus-4-7")
		Expect(okA).To(BeFalse(),
			"acc-A's spend MUST be gone after ResetSpend")
	})
})

// errorListShim is a SpendStore whose List returns a configured error.
// Used by the REV-3 'propagates Store List error' spec.
type errorListShim struct {
	listErr error
}

func (s *errorListShim) Get(_ context.Context, _ quota.SpendStoreKey) (quota.Snapshot, error) {
	return quota.Snapshot{}, quota.SpendStoreErrNotFound
}

func (s *errorListShim) Put(_ context.Context, _ quota.SpendStoreKey, _ quota.Snapshot) error {
	return nil
}

func (s *errorListShim) Reset(_ context.Context, _ quota.SpendStoreKey) error { return nil }

func (s *errorListShim) List(_ context.Context) ([]quota.SpendStoreEntry, error) {
	return nil, s.listErr
}

// errorGetShim is a SpendStore whose Get returns a configured non-
// NotFound error. Used by the REV-3 'propagates Store impl errors'
// spec on ResetSpend.
type errorGetShim struct {
	getErr error
}

func (s *errorGetShim) Get(_ context.Context, _ quota.SpendStoreKey) (quota.Snapshot, error) {
	return quota.Snapshot{}, s.getErr
}

func (s *errorGetShim) Put(_ context.Context, _ quota.SpendStoreKey, _ quota.Snapshot) error {
	return nil
}

func (s *errorGetShim) Reset(_ context.Context, _ quota.SpendStoreKey) error { return nil }

func (s *errorGetShim) List(_ context.Context) ([]quota.SpendStoreEntry, error) { return nil, nil }

var _ = Describe("Tracker.Lookup PR1/PR3 behaviour-pin (must not regress)", func() {
	It("Lookup with no registered adapter and no spend recorded returns NotConfigured{no-adapter-registered}", func() {
		// Behaviour-pin from contract_test.go:169-175 — PR4 spend overlay
		// MUST NOT change the no-adapter fallback path.
		ctx := context.Background()
		tracker := quota.NewTracker("memory")
		snap, err := tracker.Lookup(ctx, "future-provider", "", "future-model")
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

// PR6 — persisted cache + LoadSpend hydration specs. Pins the
// serialisation contract (MarshalCache / UnmarshalCache + v1 envelope
// tag) and the boot-time round-trip the app ticker performs.
//
// Plan §"Rollout Plan" PR6 row 430 — "JSON-on-disk persistence via
// HealthManager pattern (versioned envelope), versioned schema (v1),
// boot-time load of persisted state."
var _ = Describe("Tracker cache round-trip (PR6 — cache.go (MarshalCache, UnmarshalCache, LoadSpend))", func() {
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

	Context("MarshalCache / UnmarshalCache (cache.go (MarshalCache))", func() {
		It("round-trips TokenSpend snapshots without loss across encode/decode", func() {
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
			capCfg := quota.CapConfig{
				Cap:    quota.Money{Amount: 5000, Currency: "USD"},
				Period: "monthly",
			}
			err := tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider:    "anthropic",
				Model:       "claude-opus-4-7",
				AccountHash: "acc-A",
				RequestID:   "req-1",
				Usage:       &provider.UsageDelta{InputTokens: 1_000_000, OutputTokens: 1_000_000, RequestID: "req-1"},
				CapConfig:   capCfg,
			})
			Expect(err).NotTo(HaveOccurred())

			entries, err := tracker.Snapshots(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(1))

			now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
			data, err := quota.MarshalCache(entries, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).NotTo(BeEmpty())

			// Decode back and rebuild a fresh tracker — the LoadSpend
			// path must hydrate to byte-identical TokenSpend figures.
			decoded, err := quota.UnmarshalCache(data)
			Expect(err).NotTo(HaveOccurred())
			Expect(decoded).To(HaveLen(1))

			fresh := quota.NewTrackerWithSpend("memory", resolver, newMemorySpendStore(), time.Now)
			Expect(fresh.LoadSpend(ctx, decoded)).To(Succeed())

			snap, ok := fresh.LookupSpend(ctx, "anthropic", "acc-A", "claude-opus-4-7")
			Expect(ok).To(BeTrue())
			Expect(snap.TokenSpend).NotTo(BeNil())
			Expect(snap.TokenSpend.Spent.Amount).To(Equal(entries[0].Snapshot.TokenSpend.Spent.Amount),
				"hydrated spent amount MUST equal the persisted figure")
			Expect(snap.TokenSpend.Spent.Currency).To(Equal("USD"))
			Expect(snap.TokenSpend.PricingSource).To(Equal(entries[0].Snapshot.TokenSpend.PricingSource))
		})

		It("filters non-TokenSpend variants out of the persisted envelope (OD-2)", func() {
			// Hand-build a RateLimit + TokenSpend mix and pass through
			// MarshalCache. Only the TokenSpend row should survive —
			// RateLimit variants reset on the provider's own clock and
			// are pointless to persist.
			entries := []quota.SpendStoreEntry{
				{
					Key: quota.SpendStoreKey{ProviderID: "anthropic", AccountHash: "acc-A", ModelID: "ignored"},
					Snapshot: quota.Snapshot{
						Provider: "anthropic",
						RateLimit: &quota.RateLimitVariant{
							TightestPercentRemaining: 50,
						},
					},
				},
				{
					Key: quota.SpendStoreKey{ProviderID: "openai", AccountHash: "acc-A", ModelID: "gpt-4o"},
					Snapshot: quota.Snapshot{
						Provider: "openai",
						TokenSpend: &quota.TokenSpendVariant{
							Spent: quota.Money{Amount: 42, Currency: "USD"},
						},
					},
				},
				{
					Key: quota.SpendStoreKey{ProviderID: "ollama", AccountHash: "", ModelID: "llama3"},
					Snapshot: quota.Snapshot{
						Provider:      "ollama",
						NotConfigured: &quota.NotConfiguredVariant{Reason: "local-model"},
					},
				},
			}

			data, err := quota.MarshalCache(entries, time.Now())
			Expect(err).NotTo(HaveOccurred())

			decoded, err := quota.UnmarshalCache(data)
			Expect(err).NotTo(HaveOccurred())
			Expect(decoded).To(HaveLen(1),
				"OD-2: only TokenSpend variants persist; RateLimit + NotConfigured are filtered")
			Expect(decoded[0].Snapshot.TokenSpend).NotTo(BeNil())
			Expect(decoded[0].Snapshot.TokenSpend.Spent.Amount).To(Equal(int64(42)))
		})

		It("stamps the v1 envelope tag on every Marshal", func() {
			data, err := quota.MarshalCache(nil, time.Now())
			Expect(err).NotTo(HaveOccurred())
			// Decode into a CacheEnvelope to confirm the tag.
			var env quota.CacheEnvelope
			Expect(jsonUnmarshalForTest(data, &env)).To(Succeed())
			Expect(env.Version).To(Equal(quota.CacheEnvelopeVersion))
			Expect(env.Version).To(Equal("v1"))
		})

		It("returns ErrUnknownCacheVersion when the envelope tag is unrecognised", func() {
			// Hand-build a future-v2 envelope. The current loader must
			// not panic and must surface a sentinel the app wireup can
			// errors.Is against.
			future := []byte(`{"version":"v2","saved_at":"2026-05-13T12:00:00Z","snapshots":[]}`)
			_, err := quota.UnmarshalCache(future)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, quota.ErrUnknownCacheVersion)).To(BeTrue(),
				"app wireup compares with errors.Is to log+degrade to empty Tracker")
		})

		It("returns a wrapped JSON error on malformed bytes", func() {
			_, err := quota.UnmarshalCache([]byte("{not valid json"))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("quota:"))
		})

		It("returns (nil, nil) on empty input — first-boot path", func() {
			entries, err := quota.UnmarshalCache(nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(BeNil())

			entries, err = quota.UnmarshalCache([]byte{})
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(BeNil())
		})
	})

	Context("LoadSpend (cache.go (LoadSpend))", func() {
		It("no-ops when the Tracker has no spend wiring", func() {
			// NewTracker constructor — spend is nil.
			bare := quota.NewTracker("memory")
			err := bare.LoadSpend(ctx, []quota.SpendStoreEntry{
				{
					Key: quota.SpendStoreKey{ProviderID: "anthropic", ModelID: "claude-opus-4-7"},
					Snapshot: quota.Snapshot{
						TokenSpend: &quota.TokenSpendVariant{
							Spent: quota.Money{Amount: 1, Currency: "USD"},
						},
					},
				},
			})
			Expect(err).NotTo(HaveOccurred(),
				"LoadSpend on a no-spend Tracker MUST be a quiet no-op (mirrors RecordSpend)")
		})

		It("skips entries that have a nil TokenSpend variant on hydrate", func() {
			// LoadSpend must defend against malformed cache files in
			// case the writer ever leaks a RateLimit row in.
			err := tracker.LoadSpend(ctx, []quota.SpendStoreEntry{
				{
					Key: quota.SpendStoreKey{ProviderID: "anthropic", AccountHash: "acc-A", ModelID: "claude-opus-4-7"},
					Snapshot: quota.Snapshot{
						RateLimit: &quota.RateLimitVariant{TightestPercentRemaining: 50},
					},
				},
				{
					Key: quota.SpendStoreKey{ProviderID: "openai", AccountHash: "acc-A", ModelID: "gpt-4o"},
					Snapshot: quota.Snapshot{
						TokenSpend: &quota.TokenSpendVariant{
							Spent: quota.Money{Amount: 42, Currency: "USD"},
						},
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Only the TokenSpend row should be queryable.
			_, anthropicOK := tracker.LookupSpend(ctx, "anthropic", "acc-A", "claude-opus-4-7")
			Expect(anthropicOK).To(BeFalse(),
				"RateLimit row in the input MUST be filtered out by LoadSpend")
			openaiSnap, openaiOK := tracker.LookupSpend(ctx, "openai", "acc-A", "gpt-4o")
			Expect(openaiOK).To(BeTrue())
			Expect(openaiSnap.TokenSpend.Spent.Amount).To(Equal(int64(42)))
		})

		It("preserves period boundaries across the round-trip so rollover stays deterministic", func() {
			// A persisted Snapshot in the CURRENT period must hydrate
			// to the same period boundaries — the auto-reset logic in
			// LookupSpend triggers on `now >= PeriodEnd`, so a sloppy
			// hydrate that loses PeriodEnd would either fire a spurious
			// reset (PeriodEnd zero) or never reset.
			resolver.seed("anthropic", "claude-opus-4-7", "USD", 15.00, 75.00)
			capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}
			err := tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "acc-A",
				RequestID: "r1",
				Usage:     &provider.UsageDelta{InputTokens: 1_000_000, OutputTokens: 1_000_000, RequestID: "r1"},
				CapConfig: capCfg,
			})
			Expect(err).NotTo(HaveOccurred())

			entries, err := tracker.Snapshots(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(1))
			origPeriodStart := entries[0].Snapshot.TokenSpend.PeriodStart
			origPeriodEnd := entries[0].Snapshot.TokenSpend.PeriodEnd
			Expect(origPeriodEnd).NotTo(BeZero())

			data, err := quota.MarshalCache(entries, time.Now())
			Expect(err).NotTo(HaveOccurred())
			decoded, err := quota.UnmarshalCache(data)
			Expect(err).NotTo(HaveOccurred())

			fresh := quota.NewTrackerWithSpend("memory", resolver, newMemorySpendStore(), time.Now)
			Expect(fresh.LoadSpend(ctx, decoded)).To(Succeed())

			snap, ok := fresh.LookupSpend(ctx, "anthropic", "acc-A", "claude-opus-4-7")
			Expect(ok).To(BeTrue())
			Expect(snap.TokenSpend.PeriodStart.Equal(origPeriodStart)).To(BeTrue(),
				"PeriodStart MUST round-trip exactly — auto-reset rollover depends on it")
			Expect(snap.TokenSpend.PeriodEnd.Equal(origPeriodEnd)).To(BeTrue(),
				"PeriodEnd MUST round-trip exactly")
		})
	})
})

// jsonUnmarshalForTest is the encoding/json indirection the PR6
// envelope-tag spec uses to confirm the v1 tag is stamped on every
// Marshal. Kept narrow so the rest of the spend suite continues to
// rely on Gomega's matchers rather than raw JSON decoding.
func jsonUnmarshalForTest(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
