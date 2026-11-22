package app

import (
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SettleError", func() {
	var cause error

	BeforeEach(func() { cause = errors.New("settle blocked") })

	Context("after a cancellation", func() {
		var err error

		BeforeEach(func() {
			err = &SettleError{SessionID: "s1", Cause: cause, Cancelled: ErrSessionCancelled}
		})

		It("renders the line the run command always printed", func() {
			Expect(err.Error()).To(Equal(fmt.Sprintf("%s: reservation_settle_failed: session s1 is cancelled: settle blocked",
				ErrSessionCancelled)))
		})
		It("is the cancelled class", func() { Expect(errors.Is(err, ErrSessionCancelled)).To(BeTrue()) })
		It("is a settle failure", func() { Expect(errors.Is(err, ErrReservationSettle)).To(BeTrue()) })
		It("is its cause", func() { Expect(errors.Is(err, cause)).To(BeTrue()) })
	})

	Context("without a cancellation", func() {
		var err error

		BeforeEach(func() { err = &SettleError{SessionID: "c1", Cause: cause} })

		It("names the session and the cause", func() {
			Expect(err.Error()).To(Equal("reservation_settle_failed: session c1: settle blocked"))
		})
		It("is not the cancelled class", func() { Expect(errors.Is(err, ErrSessionCancelled)).To(BeFalse()) })
	})

	Context("inside a join", func() {
		var got *SettleError

		BeforeEach(func() {
			got = nil
			joined := errors.Join(ErrSessionCancelled,
				fmt.Errorf("cancel child p1: %w", &SettleError{SessionID: "c1", Cause: cause}))
			if !errors.As(joined, &got) {
				panic("errors.As did not find the SettleError")
			}
		})

		It("keeps the session id", func() { Expect(got.SessionID).To(Equal("c1")) })
		It("keeps the cause", func() { Expect(got.SettleCause()).To(MatchError(cause)) })
	})

	Context("with no cause", func() {
		var err *SettleError

		BeforeEach(func() { err = &SettleError{SessionID: "s1"} })

		It("prints no placeholder in the message", func() {
			Expect(err.Error()).To(Equal("reservation_settle_failed: session s1"))
		})
		It("prints no placeholder after a cancellation", func() {
			err.Cancelled = ErrSessionCancelled
			Expect(err.Detail()).To(Equal("session s1 is cancelled"))
		})
		It("unwraps without a nil entry", func() {
			Expect(err.Unwrap()).To(Equal([]error{ErrReservationSettle}))
		})
	})
})

var _ = Describe("SettleErrors", func() {
	var a, b *SettleError

	BeforeEach(func() {
		a = &SettleError{SessionID: "a", Cause: errors.New("x")}
		b = &SettleError{SessionID: "b", Cause: errors.New("y")}
	})

	ids := func(errs []*SettleError) []string {
		out := []string{}
		for _, e := range errs {
			out = append(out, e.SessionID)
		}
		return out
	}

	It("returns every settle failure of a join in order", func() {
		Expect(ids(SettleErrors(errors.Join(ErrSessionCancelled, a, b)))).To(Equal([]string{"a", "b"}))
	})
	It("finds failures under wrapping and nested joins", func() {
		err := fmt.Errorf("run: %w", errors.Join(fmt.Errorf("child: %w", a), errors.Join(b)))
		Expect(ids(SettleErrors(err))).To(Equal([]string{"a", "b"}))
	})
	It("returns one failure for a single error", func() {
		Expect(ids(SettleErrors(a))).To(Equal([]string{"a"}))
	})
	It("returns nothing for an error without a settle failure", func() {
		Expect(SettleErrors(errors.Join(ErrSessionCancelled, errors.New("other")))).To(BeEmpty())
	})
	It("returns nothing for a nil error", func() {
		Expect(SettleErrors(nil)).To(BeEmpty())
	})
})
