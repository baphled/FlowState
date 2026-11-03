package app

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ReservationLedger", func() {
	It("grants and exposes a reservation drawn from the pool", func() {
		l := NewReservationLedger(NewBudgetPool(100))
		_, resErr := l.Reserve("child-1", 40)
		Expect(resErr).NotTo(HaveOccurred())

		shown, ok := l.Granted("child-1")

		Expect(shown).To(Equal(int64(40)))
		Expect(ok).To(BeTrue())
	})

	It("fails closed on a reservation with an empty child id", func() {
		l := NewReservationLedger(NewBudgetPool(100))
		err := func() error { _, e := l.Reserve("", 10); return e }()

		Expect(err).To(MatchError(ErrReservationChildEmpty))
	})

	It("fails closed on a non-positive reserve amount", func() {
		l := NewReservationLedger(NewBudgetPool(100))
		err := func() error { _, e := l.Reserve("child-1", 0); return e }()

		Expect(err).To(MatchError(ErrReservationAmountInvalid))
	})

	It("fails closed on a duplicate reservation for the same child", func() {
		l := NewReservationLedger(NewBudgetPool(100))
		l.mustReserveForTest("child-1", 10)
		err := func() error { _, e := l.Reserve("child-1", 10); return e }()

		Expect(err).To(MatchError(ErrReservationDuplicate))
	})

	It("returns zero without error when the pool is exhausted", func() {
		l := NewReservationLedger(NewBudgetPool(10))
		l.mustReserveForTest("child-1", 10)
		granted, resErr := l.Reserve("child-2", 5)

		Expect(granted).To(Equal(int64(0)))
		Expect(resErr).NotTo(HaveOccurred())
	})

	It("returns unused budget to the pool when charged once", func() {
		l := NewReservationLedger(NewBudgetPool(100))
		l.mustReserveForTest("child-1", 40)
		chargeErr := l.Charge("child-1", 25)

		Expect(l.pool.Remaining()).To(Equal(int64(85)))
		Expect(chargeErr).NotTo(HaveOccurred())
	})

	It("fails closed on charging an unknown child", func() {
		l := NewReservationLedger(NewBudgetPool(100))
		err := l.Charge("ghost", 5)

		Expect(err).To(MatchError(ErrReservationUnknown))
	})

	It("fails closed on charging a reservation twice", func() {
		l := NewReservationLedger(NewBudgetPool(100))
		l.mustReserveForTest("child-1", 40)
		if err := l.Charge("child-1", 10); err != nil {
			panic("fixture charge failed: " + err.Error())
		}
		err := l.Charge("child-1", 10)

		Expect(err).To(MatchError(ErrReservationCharged))
	})

	It("fails closed when the charge exceeds the grant", func() {
		l := NewReservationLedger(NewBudgetPool(100))
		l.mustReserveForTest("child-1", 40)
		err := l.Charge("child-1", 41)

		Expect(err).To(MatchError(ErrReservationOverCharge))
	})

	It("fails closed on a non-positive charge", func() {
		l := NewReservationLedger(NewBudgetPool(100))
		l.mustReserveForTest("child-1", 40)
		err := l.Charge("child-1", 0)

		Expect(err).To(MatchError(ErrReservationAmountInvalid))
	})

	It("reports no grant for a child without a reservation", func() {
		l := NewReservationLedger(NewBudgetPool(100))
		shown, ok := l.Granted("nobody")

		Expect(shown).To(Equal(int64(0)))
		Expect(ok).To(BeFalse())
	})
})

// mustReserveForTest reserves want for child, panicking on failure
// (fixture setup, never the spec's behavioural expectation).
func (l *ReservationLedger) mustReserveForTest(child string, want int64) {
	if _, err := l.Reserve(child, want); err != nil {
		panic("fixture reservation failed: " + err.Error())
	}
}
