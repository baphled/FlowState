package lifecycle_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine/lifecycle"
)

// ErrStageFailed is a local sentinel used by specs that need a handler to
// return a known error.
var ErrStageFailed = errors.New("stage failed")

var _ = Describe("Lifecycle core types", func() {
	Describe("ErrTurnRejected", func() {
		It("is a sentinel distinct from other errors", func() {
			Expect(errors.Is(lifecycle.ErrTurnRejected, lifecycle.ErrTurnRejected)).To(BeTrue())
			Expect(errors.Is(errors.New("other"), lifecycle.ErrTurnRejected)).To(BeFalse())
		})
	})

	Describe("StageHookChain.Then", func() {
		It("returns the handler unchanged when the chain is empty", func() {
			var chain lifecycle.StageHookChain[int]
			invoked := false
			handler := func(ctx int) (int, error) {
				invoked = true
				return ctx + 1, nil
			}
			wrapped := chain.Then(handler)
			out, err := wrapped(1)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal(2))
			Expect(invoked).To(BeTrue())
		})

		It("returns the handler unchanged when the chain is nil", func() {
			var chain lifecycle.StageHookChain[int]
			called := false
			handler := func(ctx int) (int, error) {
				called = true
				return ctx, nil
			}
			out, err := chain.Then(handler)(7)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal(7))
			Expect(called).To(BeTrue())
		})

		It("wraps a single hook around the handler in registration order", func() {
			var calls []string
			hook := func(ctx int, next func(int) (int, error)) (int, error) {
				calls = append(calls, "before")
				out, err := next(ctx)
				calls = append(calls, "after")
				return out, err
			}
			handler := func(ctx int) (int, error) {
				calls = append(calls, "handler")
				return ctx + 10, nil
			}
			chain := lifecycle.StageHookChain[int]{hook}
			out, err := chain.Then(handler)(5)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal(15))
			Expect(calls).To(Equal([]string{"before", "handler", "after"}))
		})

		It("composes multiple hooks so registration order equals before-hook order", func() {
			var calls []string
			appendHook := func(label string) lifecycle.StageHook[int] {
				return func(ctx int, next func(int) (int, error)) (int, error) {
					calls = append(calls, "before:"+label)
					out, err := next(ctx)
					calls = append(calls, "after:"+label)
					return out, err
				}
			}
			handler := func(ctx int) (int, error) {
				calls = append(calls, "handler")
				return ctx, nil
			}
			chain := lifecycle.StageHookChain[int]{appendHook("first"), appendHook("second")}
			_, err := chain.Then(handler)(0)
			Expect(err).NotTo(HaveOccurred())
			Expect(calls).To(Equal([]string{
				"before:first",
				"before:second",
				"handler",
				"after:second",
				"after:first",
			}))
		})

		It("propagates value-type mutations made by a hook before calling next", func() {
			doubleHook := func(ctx int, next func(int) (int, error)) (int, error) {
				return next(ctx * 2)
			}
			handler := func(ctx int) (int, error) { return ctx + 1, nil }
			chain := lifecycle.StageHookChain[int]{doubleHook}
			out, err := chain.Then(handler)(3)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal(7))
		})

		It("allows a hook to short-circuit by returning an error without calling next", func() {
			shortCircuit := errors.New("short-circuit")
			rejectHook := func(ctx int, next func(int) (int, error)) (int, error) {
				return ctx, shortCircuit
			}
			handlerCalled := false
			handler := func(ctx int) (int, error) {
				handlerCalled = true
				return ctx, nil
			}
			chain := lifecycle.StageHookChain[int]{rejectHook}
			_, err := chain.Then(handler)(0)
			Expect(err).To(MatchError(shortCircuit))
			Expect(handlerCalled).To(BeFalse())
		})
	})
})
