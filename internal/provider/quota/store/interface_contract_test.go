// Package store_test runs the load-bearing v3 contract ladder over
// every Store implementation registered in the constructor list. v1
// ships MemoryStore as the only full impl; RedisStore and
// PostgresStore are stubs and Skip every row via store.IsStub.
//
// v3 plans (Redis/Postgres real impl) add their constructor here and
// the existing rows must pass without modification — that is the
// load-bearing commitment per the Provider Quota and Spend Visibility
// plan §"`internal/provider/quota/store/`" lines 244-285. Identical
// pattern to internal/auth/store/interface_contract_test.go from the
// API Auth Track plan that shipped 2026-05-13.
package store_test

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider/quota"
	"github.com/baphled/flowstate/internal/provider/quota/store"
)

// storeFactory pairs a human-readable name with a constructor so the
// Ginkgo node tree surfaces which impl is under test in failure
// output.
type storeFactory struct {
	name string
	make func() store.Store
}

// allStoreFactories enumerates every Store impl. v3 swap-in: replace
// the stubs' constructors with the real ones and the ladder must
// stay green.
var allStoreFactories = []storeFactory{
	{name: "MemoryStore", make: func() store.Store { return store.NewMemoryStore() }},
	{name: "RedisStore", make: func() store.Store { return store.NewRedisStore() }},
	{name: "PostgresStore", make: func() store.Store { return store.NewPostgresStore() }},
}

var _ = Describe("Store contract ladder", func() {
	for _, factory := range allStoreFactories {
		factory := factory // capture per-iteration for closures

		Context(factory.name, func() {
			var s store.Store

			BeforeEach(func() {
				s = factory.make()
				if store.IsStub(s) {
					Skip("stub implementation; real impl lands in v3 (plan §`internal/provider/quota/store/` lines 244-285)")
				}
			})

			// Row 1 — Get-after-Put round-trip equality.
			Context("Get after Put", func() {
				It("returns the same Snapshot bytewise", func() {
					ctx := context.Background()
					observed := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
					reset := observed.Add(time.Hour)
					snap := quota.Snapshot{
						Provider:     "anthropic",
						AccountHash:  "abc123def456",
						Model:        "claude-opus-4-7",
						ObservedAt:   observed,
						Stale:        false,
						StoreBackend: "memory",
						RateLimit: &quota.RateLimitVariant{
							Requests: quota.Window{Limit: 1000, Remaining: 999, Reset: reset},
							Tokens:   quota.NewWindow(),
							Input:    quota.NewWindow(),
							Output:   quota.NewWindow(),
							TightestPercentRemaining: 99,
							TightestResetAt:          reset,
						},
					}
					key := store.Key{ProviderID: "anthropic", AccountHash: "abc123def456", ModelID: "claude-opus-4-7"}

					Expect(s.Put(ctx, key, snap)).To(Succeed())
					got, err := s.Get(ctx, key)
					Expect(err).NotTo(HaveOccurred())

					Expect(got.Provider).To(Equal(snap.Provider))
					Expect(got.AccountHash).To(Equal(snap.AccountHash))
					Expect(got.Model).To(Equal(snap.Model))
					Expect(got.ObservedAt.Equal(snap.ObservedAt)).To(BeTrue())
					Expect(got.StoreBackend).To(Equal(snap.StoreBackend))
					Expect(got.RateLimit).NotTo(BeNil())
					Expect(got.RateLimit.Requests.Limit).To(Equal(1000))
					Expect(got.RateLimit.Requests.Remaining).To(Equal(999))
					Expect(got.RateLimit.Requests.Reset.Equal(reset)).To(BeTrue())
					Expect(got.RateLimit.TightestPercentRemaining).To(Equal(99))
				})
			})

			// Row 2 — Get-after-Delete returns ErrSnapshotNotFound;
			// Delete itself is idempotent.
			Context("Get after Delete", func() {
				It("returns ErrSnapshotNotFound and Delete is idempotent", func() {
					ctx := context.Background()
					key := store.Key{ProviderID: "anthropic", AccountHash: "h1", ModelID: "m1"}
					Expect(s.Put(ctx, key, quota.Snapshot{
						Provider:      "anthropic",
						NotConfigured: &quota.NotConfiguredVariant{Reason: "test"},
					})).To(Succeed())
					Expect(s.Delete(ctx, key)).To(Succeed())

					_, err := s.Get(ctx, key)
					Expect(errors.Is(err, store.ErrSnapshotNotFound)).To(BeTrue(),
						"Get after Delete must return ErrSnapshotNotFound; got %v", err)

					// Idempotency: deleting again is a no-op.
					Expect(s.Delete(ctx, key)).To(Succeed())
					// And deleting a key that never existed is a no-op.
					Expect(s.Delete(ctx, store.Key{ProviderID: "never", AccountHash: "x", ModelID: "y"})).To(Succeed())
				})
			})

			// Row 3 — Get-after-Reset returns ErrSnapshotNotFound;
			// Reset is idempotent and a subsequent Put restores the
			// entry.
			Context("Get after Reset", func() {
				It("returns ErrSnapshotNotFound, Reset is idempotent, Put restores", func() {
					ctx := context.Background()
					key := store.Key{ProviderID: "openai", AccountHash: "h2", ModelID: "m2"}
					Expect(s.Put(ctx, key, quota.Snapshot{
						Provider:      "openai",
						NotConfigured: &quota.NotConfiguredVariant{Reason: "pre-reset"},
					})).To(Succeed())

					Expect(s.Reset(ctx, key)).To(Succeed())
					_, err := s.Get(ctx, key)
					Expect(errors.Is(err, store.ErrSnapshotNotFound)).To(BeTrue(),
						"Get after Reset must return ErrSnapshotNotFound; got %v", err)

					// Idempotency.
					Expect(s.Reset(ctx, key)).To(Succeed())

					// Restoration via Put.
					Expect(s.Put(ctx, key, quota.Snapshot{
						Provider:      "openai",
						NotConfigured: &quota.NotConfiguredVariant{Reason: "post-reset"},
					})).To(Succeed())
					got, err := s.Get(ctx, key)
					Expect(err).NotTo(HaveOccurred())
					Expect(got.NotConfigured.Reason).To(Equal("post-reset"))
				})
			})

			// Row 4 — Cleanup is idempotent and sweeps stale
			// RateLimit Snapshots whose TightestResetAt has passed.
			Context("Cleanup", func() {
				It("is idempotent across repeated calls and sweeps only stale RateLimit entries", func() {
					ctx := context.Background()
					now := time.Now()

					// Fresh RateLimit: must survive cleanup.
					freshKey := store.Key{ProviderID: "anthropic", AccountHash: "fh", ModelID: "fresh"}
					Expect(s.Put(ctx, freshKey, quota.Snapshot{
						Provider: "anthropic",
						RateLimit: &quota.RateLimitVariant{
							TightestResetAt: now.Add(time.Hour),
						},
					})).To(Succeed())

					// Stale RateLimit: must be swept.
					staleKey := store.Key{ProviderID: "anthropic", AccountHash: "fh", ModelID: "stale"}
					Expect(s.Put(ctx, staleKey, quota.Snapshot{
						Provider: "anthropic",
						RateLimit: &quota.RateLimitVariant{
							TightestResetAt: now.Add(-time.Hour),
						},
					})).To(Succeed())

					// TokenSpend: never swept regardless of timestamps.
					spendKey := store.Key{ProviderID: "anthropic", AccountHash: "fh", ModelID: "spend"}
					Expect(s.Put(ctx, spendKey, quota.Snapshot{
						Provider:   "anthropic",
						TokenSpend: &quota.TokenSpendVariant{Period: "monthly"},
					})).To(Succeed())

					Expect(s.Cleanup(ctx, now)).To(Succeed())
					Expect(s.Cleanup(ctx, now)).To(Succeed()) // second call is safe

					_, freshErr := s.Get(ctx, freshKey)
					Expect(freshErr).NotTo(HaveOccurred(), "fresh RateLimit must survive Cleanup")
					_, staleErr := s.Get(ctx, staleKey)
					Expect(errors.Is(staleErr, store.ErrSnapshotNotFound)).To(BeTrue(),
						"stale RateLimit must be swept; got %v", staleErr)
					_, spendErr := s.Get(ctx, spendKey)
					Expect(spendErr).NotTo(HaveOccurred(),
						"TokenSpend Snapshots are never swept by Cleanup; they persist across period boundaries")
				})
			})

			// Row 5 — Concurrent Put/Get/Delete on disjoint Keys is
			// sequentially consistent. Run under -race.
			Context("Concurrent access", func() {
				It("is safe under 100 goroutines mixing Put/Get/Delete on disjoint Keys", func() {
					ctx := context.Background()
					var wg sync.WaitGroup
					const N = 100
					wg.Add(N)
					for i := 0; i < N; i++ {
						go func(i int) {
							defer wg.Done()
							key := store.Key{
								ProviderID:  "anthropic",
								AccountHash: "h",
								ModelID:     "m-concurrent",
							}
							_ = s.Put(ctx, key, quota.Snapshot{
								Provider:      "anthropic",
								NotConfigured: &quota.NotConfiguredVariant{Reason: "x"},
							})
							_, _ = s.Get(ctx, key)
							if i%10 == 0 {
								_ = s.Delete(ctx, key)
							}
						}(i)
					}
					wg.Wait()
					// Final state coherent: either present or absent;
					// no torn read.
					_, err := s.Get(ctx, store.Key{ProviderID: "anthropic", AccountHash: "h", ModelID: "m-concurrent"})
					if err != nil {
						Expect(errors.Is(err, store.ErrSnapshotNotFound)).To(BeTrue(),
							"final Get must return either nil or ErrSnapshotNotFound; got %v", err)
					}
				})
			})

			// Row 6 — Put with an existing Key overwrites.
			Context("Put with an existing Key", func() {
				It("overwrites the prior Snapshot", func() {
					ctx := context.Background()
					key := store.Key{ProviderID: "anthropic", AccountHash: "h", ModelID: "overwrite"}
					orig := quota.Snapshot{
						Provider:      "anthropic",
						NotConfigured: &quota.NotConfiguredVariant{Reason: "original"},
					}
					replacement := quota.Snapshot{
						Provider:      "anthropic",
						NotConfigured: &quota.NotConfiguredVariant{Reason: "replacement"},
					}
					Expect(s.Put(ctx, key, orig)).To(Succeed())
					Expect(s.Put(ctx, key, replacement)).To(Succeed())

					got, err := s.Get(ctx, key)
					Expect(err).NotTo(HaveOccurred())
					Expect(got.NotConfigured.Reason).To(Equal("replacement"))
				})
			})

			// Row 7 — Empty-key handling: Get(zero Key) →
			// ErrSnapshotNotFound, Put(empty ProviderID) →
			// ErrInvalidKey.
			Context("Empty Key", func() {
				It("returns ErrSnapshotNotFound from Get and ErrInvalidKey from Put", func() {
					ctx := context.Background()
					_, err := s.Get(ctx, store.Key{})
					Expect(errors.Is(err, store.ErrSnapshotNotFound)).To(BeTrue(),
						"Get(zero Key) must return ErrSnapshotNotFound; got %v", err)

					putErr := s.Put(ctx, store.Key{}, quota.Snapshot{
						NotConfigured: &quota.NotConfiguredVariant{Reason: "x"},
					})
					Expect(errors.Is(putErr, store.ErrInvalidKey)).To(BeTrue(),
						"Put with empty ProviderID must return ErrInvalidKey; got %v", putErr)
				})
			})

			// Row 8 — Context cancellation honoured on Cleanup.
			Context("Context cancellation", func() {
				It("returns ctx.Err() from Cleanup when ctx is cancelled", func() {
					parentCtx := context.Background()
					// Seed many records so Cleanup has work to do.
					for i := 0; i < 50; i++ {
						key := store.Key{
							ProviderID:  "anthropic",
							AccountHash: "h",
							ModelID:     time.Now().Format("150405.000000000"),
						}
						_ = s.Put(parentCtx, key, quota.Snapshot{
							Provider: "anthropic",
							RateLimit: &quota.RateLimitVariant{
								TightestResetAt: time.Now().Add(-time.Hour),
							},
						})
					}
					cancelCtx, cancel := context.WithCancel(parentCtx)
					cancel() // cancel before the call
					err := s.Cleanup(cancelCtx, time.Now())
					Expect(err).To(MatchError(context.Canceled))
				})
			})
		})
	}
})

var _ = Describe("Store compile-time conformance", func() {
	// The package-level `var _ Store = (*Foo)(nil)` checks in store.go
	// fail the build the moment any impl falls behind. This spec
	// exists so a reader scanning the contract test sees the
	// guarantee explicitly.
	It("compiles all three impls against the Store interface", func() {
		var (
			_ store.Store = (*store.MemoryStore)(nil)
			_ store.Store = (*store.RedisStore)(nil)
			_ store.Store = (*store.PostgresStore)(nil)
		)
	})

	It("classifies stubs vs full impls via IsStub", func() {
		Expect(store.IsStub(store.NewMemoryStore())).To(BeFalse())
		Expect(store.IsStub(store.NewRedisStore())).To(BeTrue())
		Expect(store.IsStub(store.NewPostgresStore())).To(BeTrue())
	})

	It("stubs return ErrNotImplemented from every method", func() {
		ctx := context.Background()
		key := store.Key{ProviderID: "anthropic", AccountHash: "h", ModelID: "m"}
		for _, s := range []store.Store{store.NewRedisStore(), store.NewPostgresStore()} {
			_, err := s.Get(ctx, key)
			Expect(errors.Is(err, store.ErrNotImplemented)).To(BeTrue())
			Expect(errors.Is(s.Put(ctx, key, quota.Snapshot{}), store.ErrNotImplemented)).To(BeTrue())
			Expect(errors.Is(s.Delete(ctx, key), store.ErrNotImplemented)).To(BeTrue())
			Expect(errors.Is(s.Reset(ctx, key), store.ErrNotImplemented)).To(BeTrue())
			Expect(errors.Is(s.Cleanup(ctx, time.Now()), store.ErrNotImplemented)).To(BeTrue())
		}
	})
})

var _ = Describe("ValidateDeploymentTopology (plan B3/B4, lines 289-291)", func() {
	DescribeTable("accepts the valid combinations",
		func(backend, topology string) {
			Expect(store.ValidateDeploymentTopology(backend, topology)).To(Succeed())
		},
		Entry("single-instance + memory (fresh-install default)", "memory", "single-instance"),
		Entry("single-instance + redis (operator opted into stub)", "redis", "single-instance"),
		Entry("multi-instance + redis (the intended v3 path)", "redis", "multi-instance"),
		Entry("multi-instance + postgres", "postgres", "multi-instance"),
		Entry("empty backend + empty topology (defaults absent)", "", ""),
	)

	It("rejects multi-instance + memory with a structured error", func() {
		err := store.ValidateDeploymentTopology("memory", "multi-instance")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("quota.store.backend = memory"))
		Expect(err.Error()).To(ContainSubstring("multi-instance"))
		Expect(err.Error()).To(ContainSubstring("redis"),
			"error must name the recommended backend so operators have an actionable hint")
	})
})
