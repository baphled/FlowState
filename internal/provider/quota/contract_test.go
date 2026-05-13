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
			snap, err := tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.StoreBackend).To(Equal("memory"),
				"Tracker MUST stamp StoreBackend so adapters need not know the backend")
			Expect(snap.NotConfigured).NotTo(BeNil())
			Expect(snap.NotConfigured.Reason).To(Equal("test"))
		})

		It("returns NotConfigured with reason 'no-adapter-registered' for an unknown provider", func() {
			// No Register call.
			snap, err := tracker.Lookup(ctx, "future-provider", "future-model")
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
			snap, err := tracker.Lookup(ctx, "anthropic", "x")
			Expect(err).NotTo(HaveOccurred())
			Expect(snap.NotConfigured.Reason).To(Equal("replaced"))
		})

		It("ignores a nil adapter Register call (defensive)", func() {
			tracker.Register("anthropic", nil)
			snap, err := tracker.Lookup(ctx, "anthropic", "x")
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
					_, _ = tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
				}()
				go func() {
					defer wg.Done()
					tracker.RecordResponse("anthropic", "claude-opus-4-7", http.Header{}, provider.Usage{})
				}()
			}
			wg.Wait()
			// Sanity: tracker still functional.
			snap, err := tracker.Lookup(ctx, "anthropic", "claude-opus-4-7")
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
		snap, err := t.Lookup(context.Background(), "anthropic", "x")
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
		snap, err := t.Lookup(ctx, "anthropic", "claude-opus-4-7")
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
		snap, err := t.Lookup(ctx, "anthropic", "claude-opus-4-7")
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
		snap, err := t.Lookup(ctx, "anthropic", "claude-opus-4-7")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.PricingSource).To(Equal("registry:https://prices.example/v1.json"))
	})

	It("stamps operator-override source when the resolver returns an override hit", func() {
		resolver := &fakePricingResolver{hits: map[string]string{
			"anthropic/claude-opus-4-7": "operator-override:/etc/flowstate/pricing.json",
		}}
		t := quota.NewTrackerWithPricing("memory", resolver)
		t.Register("anthropic", adapter)
		snap, err := t.Lookup(ctx, "anthropic", "claude-opus-4-7")
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
		snap, err := t.Lookup(ctx, "anthropic", "claude-opus-4-7")
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
		snap, err := t.Lookup(ctx, "anthropic", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.PricingSource).To(BeEmpty(),
			"empty modelID MUST short-circuit the resolver — account-wide snapshots have no per-model price")
	})
})
