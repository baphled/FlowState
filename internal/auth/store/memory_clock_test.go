package store_test

// QA BUG-3 fix (May 2026). MemoryStore.Get's read-time expiry check
// used time.Now() directly, ignoring any clock the surrounding system
// injected. Tests that drove SessionConfig.Now to a fixed point in
// the past would see the store mark records "expired" against the
// real wall clock — silent fragility the QA report flagged.
//
// The fix: WithNow(...) functional option threads an injected clock
// into the store. Tests use it to drive deterministic expiry without
// manipulating the wall clock; SessionManager wires its own injected
// clock through when both layers need to agree on "now".
//
// Seam-level Ginkgo per feedback_ginkgo_not_godog. Lives next to the
// contract ladder (interface_contract_test.go) because the expiry
// invariant is part of the same row 3 contract, just driven via a
// MemoryStore-specific knob.

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/auth/store"
)

var _ = Describe("MemoryStore WithNow clock injection (QA BUG-3)", func() {
	var (
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("honours the injected clock for read-time expiry check", func() {
		// Drive the store's clock to a fixed point. A record with
		// ExpiresAt in the wall-clock past but in the FROZEN clock's
		// future must be returned by Get — without WithNow, the store
		// would silently use time.Now and treat the record as expired.
		frozen := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
		s := store.NewMemoryStore(store.WithNow(func() time.Time { return frozen }))

		// Wall clock is "now"; frozen clock is May 2026. ExpiresAt in
		// May 2026 + 1h is the past relative to wall-clock (test runs
		// after May 2026) but the future relative to frozen.
		rec := &store.Record{
			Token:     "tok-clock-injected",
			ExpiresAt: frozen.Add(time.Hour),
		}
		Expect(s.Put(ctx, rec)).To(Succeed())

		got, err := s.Get(ctx, "tok-clock-injected")
		Expect(err).NotTo(HaveOccurred(),
			"injected clock places ExpiresAt in the future; Get must return the record")
		Expect(got).NotTo(BeNil())
	})

	It("returns ErrSessionNotFound when the injected clock is past ExpiresAt", func() {
		// Mirror of the above: same record, but the frozen clock is
		// AFTER ExpiresAt. The injected-clock invariant cuts both ways.
		frozen := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
		s := store.NewMemoryStore(store.WithNow(func() time.Time { return frozen }))

		rec := &store.Record{
			Token:     "tok-clock-expired",
			ExpiresAt: frozen.Add(-time.Hour),
		}
		Expect(s.Put(ctx, rec)).To(Succeed())

		_, err := s.Get(ctx, "tok-clock-expired")
		Expect(errors.Is(err, store.ErrSessionNotFound)).To(BeTrue())
	})

	It("falls back to time.Now when WithNow is not supplied", func() {
		// Zero-option NewMemoryStore must preserve the prior shape:
		// records expire against the wall clock. Pinning so the
		// option's default doesn't drift.
		s := store.NewMemoryStore()

		// ExpiresAt one hour in the future from wall-clock — must be
		// returned by Get.
		rec := &store.Record{
			Token:     "tok-default-clock",
			ExpiresAt: time.Now().Add(time.Hour),
		}
		Expect(s.Put(ctx, rec)).To(Succeed())

		got, err := s.Get(ctx, "tok-default-clock")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
	})

	It("WithNow(nil) is a no-op (preserves time.Now default)", func() {
		// Defensive: caller passes a nil clock factory by accident.
		// Must not panic; must preserve the wall-clock default.
		s := store.NewMemoryStore(store.WithNow(nil))

		rec := &store.Record{
			Token:     "tok-nil-now",
			ExpiresAt: time.Now().Add(time.Hour),
		}
		Expect(s.Put(ctx, rec)).To(Succeed())

		got, err := s.Get(ctx, "tok-nil-now")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
	})
})
