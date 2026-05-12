// Package store_test runs the load-bearing v3 contract ladder over every
// Store implementation registered in the constructor list. v1 ships
// MemoryStore as the only full impl; RedisStore and PostgresStore are
// stubs and Skip every row via store.IsStub.
//
// v3 plans (Redis/Postgres real impl) add their constructor here and the
// existing rows must pass without modification — that is the load-bearing
// commitment per the API Auth Track plan §"Session Store Interface"
// lines 263-285.
package store_test

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/auth/store"
)

// storeFactory pairs a human-readable name with a constructor so the
// Ginkgo node tree surfaces which impl is under test in failure output.
type storeFactory struct {
	name string
	make func() store.Store
}

// allStoreFactories enumerates every Store impl. v3 swap-in: replace the
// stubs' constructors with the real ones and the ladder must stay green.
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
					Skip("stub implementation; real impl lands in v3 (plan §Session Store Interface lines 263-285)")
				}
			})

			// Row 1 — Get-after-Put round-trip equality.
			Context("Get after Put", func() {
				It("returns the same record bytewise", func() {
					ctx := context.Background()
					rec := &store.Record{
						Token:       "tok-roundtrip",
						Mode:        "shared-secret",
						PrincipalID: "user-1",
						CSRFToken:   "csrf-roundtrip",
						CreatedAt:   time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
						ExpiresAt:   time.Now().Add(time.Hour),
						Data:        map[string]string{"display_name": "Alice"},
					}
					Expect(s.Put(ctx, rec)).To(Succeed())
					got, err := s.Get(ctx, "tok-roundtrip")
					Expect(err).NotTo(HaveOccurred())
					Expect(got).NotTo(BeNil())
					Expect(got.Token).To(Equal(rec.Token))
					Expect(got.Mode).To(Equal(rec.Mode))
					Expect(got.PrincipalID).To(Equal(rec.PrincipalID))
					Expect(got.CSRFToken).To(Equal(rec.CSRFToken))
					Expect(got.CreatedAt.Equal(rec.CreatedAt)).To(BeTrue())
					Expect(got.ExpiresAt.Equal(rec.ExpiresAt)).To(BeTrue())
					Expect(got.Data).To(Equal(rec.Data))
				})
			})

			// Row 2 — Get-after-Delete returns ErrSessionNotFound;
			// Delete itself is idempotent (returns nil even when the
			// token does not exist).
			Context("Get after Delete", func() {
				It("returns ErrSessionNotFound and Delete is idempotent", func() {
					ctx := context.Background()
					rec := &store.Record{
						Token:     "tok-delete",
						ExpiresAt: time.Now().Add(time.Hour),
					}
					Expect(s.Put(ctx, rec)).To(Succeed())
					Expect(s.Delete(ctx, "tok-delete")).To(Succeed())

					_, err := s.Get(ctx, "tok-delete")
					Expect(errors.Is(err, store.ErrSessionNotFound)).To(BeTrue(),
						"Get after Delete must return ErrSessionNotFound; got %v", err)

					// Idempotency: deleting again is a no-op.
					Expect(s.Delete(ctx, "tok-delete")).To(Succeed())
					// And deleting a token that never existed is a no-op.
					Expect(s.Delete(ctx, "never-existed")).To(Succeed())
				})
			})

			// Row 3 — Get-after-expiry returns ErrSessionNotFound at
			// read time, before Cleanup runs.
			Context("Get after expiry", func() {
				It("returns ErrSessionNotFound without waiting for Cleanup", func() {
					ctx := context.Background()
					rec := &store.Record{
						Token:     "tok-expired",
						CreatedAt: time.Now().Add(-2 * time.Hour),
						ExpiresAt: time.Now().Add(-time.Hour), // expired
					}
					Expect(s.Put(ctx, rec)).To(Succeed())
					_, err := s.Get(ctx, "tok-expired")
					Expect(errors.Is(err, store.ErrSessionNotFound)).To(BeTrue(),
						"expired record must surface ErrSessionNotFound from Get; got %v", err)
				})
			})

			// Row 4 — Cleanup is idempotent.
			Context("Cleanup", func() {
				It("is idempotent across repeated calls with the same `before`", func() {
					ctx := context.Background()
					now := time.Now()
					Expect(s.Put(ctx, &store.Record{Token: "tok-fresh", ExpiresAt: now.Add(time.Hour)})).To(Succeed())
					Expect(s.Put(ctx, &store.Record{Token: "tok-stale", ExpiresAt: now.Add(-time.Hour)})).To(Succeed())

					Expect(s.Cleanup(ctx, now)).To(Succeed())
					Expect(s.Cleanup(ctx, now)).To(Succeed()) // second call is safe

					_, freshErr := s.Get(ctx, "tok-fresh")
					Expect(freshErr).NotTo(HaveOccurred(), "fresh record must survive Cleanup")
					_, staleErr := s.Get(ctx, "tok-stale")
					Expect(errors.Is(staleErr, store.ErrSessionNotFound)).To(BeTrue(),
						"stale record must be swept by Cleanup")
				})
			})

			// Row 5 — Concurrent Put/Get/Delete is sequentially
			// consistent. Run under -race.
			Context("Concurrent access", func() {
				It("is safe under 100 goroutines mixing Put/Get/Delete", func() {
					ctx := context.Background()
					var wg sync.WaitGroup
					const N = 100
					wg.Add(N)
					for i := 0; i < N; i++ {
						go func(i int) {
							defer wg.Done()
							tok := "tok-concurrent"
							rec := &store.Record{
								Token:     tok,
								Mode:      "shared-secret",
								ExpiresAt: time.Now().Add(time.Hour),
							}
							_ = s.Put(ctx, rec)
							_, _ = s.Get(ctx, tok)
							if i%10 == 0 {
								_ = s.Delete(ctx, tok)
							}
						}(i)
					}
					wg.Wait()
					// Final state must be coherent: either present
					// (last writer won) or absent (Delete won); no
					// torn read.
					_, err := s.Get(ctx, "tok-concurrent")
					if err != nil {
						Expect(errors.Is(err, store.ErrSessionNotFound)).To(BeTrue(),
							"final Get must return either nil or ErrSessionNotFound; got %v", err)
					}
				})
			})

			// Row 6 — Put with an existing token overwrites.
			Context("Put with an existing token", func() {
				It("overwrites the prior record", func() {
					ctx := context.Background()
					orig := &store.Record{
						Token:       "tok-overwrite",
						PrincipalID: "user-original",
						ExpiresAt:   time.Now().Add(time.Hour),
					}
					replacement := &store.Record{
						Token:       "tok-overwrite",
						PrincipalID: "user-replacement",
						ExpiresAt:   time.Now().Add(2 * time.Hour),
					}
					Expect(s.Put(ctx, orig)).To(Succeed())
					Expect(s.Put(ctx, replacement)).To(Succeed())

					got, err := s.Get(ctx, "tok-overwrite")
					Expect(err).NotTo(HaveOccurred())
					Expect(got.PrincipalID).To(Equal("user-replacement"))
				})
			})

			// Row 7 — Empty-token handling: Get("") → ErrSessionNotFound,
			// Put("") → ErrInvalidToken.
			Context("Empty token", func() {
				It("returns ErrSessionNotFound from Get and ErrInvalidToken from Put", func() {
					ctx := context.Background()
					_, err := s.Get(ctx, "")
					Expect(errors.Is(err, store.ErrSessionNotFound)).To(BeTrue(),
						"Get(\"\") must return ErrSessionNotFound; got %v", err)

					putErr := s.Put(ctx, &store.Record{Token: "", ExpiresAt: time.Now().Add(time.Hour)})
					Expect(errors.Is(putErr, store.ErrInvalidToken)).To(BeTrue(),
						"Put with empty token must return ErrInvalidToken; got %v", putErr)
				})
			})

			// Row 8 — Context cancellation honoured on Cleanup.
			Context("Context cancellation", func() {
				It("returns ctx.Err() from Cleanup when ctx is cancelled", func() {
					parentCtx := context.Background()
					// Seed many records so Cleanup has work to do.
					for i := 0; i < 50; i++ {
						tok := "tok-ctx-" + time.Now().Format("150405.000000000") + string(rune('a'+(i%26)))
						_ = s.Put(parentCtx, &store.Record{
							Token:     tok,
							ExpiresAt: time.Now().Add(-time.Hour),
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
	// interface_compat_test.go equivalent (plan line 271). The package-
	// level `var _ Store = (*Foo)(nil)` checks in store.go fail the
	// build the moment any impl falls behind. This spec exists so a
	// reader scanning the contract test sees the guarantee explicitly.
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
		for _, s := range []store.Store{store.NewRedisStore(), store.NewPostgresStore()} {
			_, err := s.Get(ctx, "tok")
			Expect(errors.Is(err, store.ErrNotImplemented)).To(BeTrue())
			Expect(errors.Is(s.Put(ctx, &store.Record{Token: "tok"}), store.ErrNotImplemented)).To(BeTrue())
			Expect(errors.Is(s.Delete(ctx, "tok"), store.ErrNotImplemented)).To(BeTrue())
			Expect(errors.Is(s.Cleanup(ctx, time.Now()), store.ErrNotImplemented)).To(BeTrue())
		}
	})
})
