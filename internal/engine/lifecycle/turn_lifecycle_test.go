package lifecycle_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine/lifecycle"
)

var _ = Describe("Stage", func() {
	Describe("Execute", func() {
		It("invokes the handler when the hook chain is empty", func() {
			stage := lifecycle.LifecycleStage[int]{
				Name: "example",
				Handler: func(ctx int) (int, error) {
					return ctx + 5, nil
				},
			}
			out, err := stage.Execute(10)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal(15))
		})

		It("wraps the handler with the registered hooks", func() {
			seen := []string{}
			stage := lifecycle.LifecycleStage[int]{
				Name: "observed",
				Hooks: lifecycle.StageHookChain[int]{
					func(ctx int, next func(int) (int, error)) (int, error) {
						seen = append(seen, "before")
						out, err := next(ctx)
						seen = append(seen, "after")
						return out, err
					},
				},
				Handler: func(ctx int) (int, error) {
					seen = append(seen, "handler")
					return ctx, nil
				},
			}
			_, err := stage.Execute(0)
			Expect(err).NotTo(HaveOccurred())
			Expect(seen).To(Equal([]string{"before", "handler", "after"}))
		})

		It("propagates the mutated context returned by the handler", func() {
			stage := lifecycle.LifecycleStage[int]{
				Name: "mutator",
				Handler: func(ctx int) (int, error) {
					return ctx * 3, nil
				},
			}
			out, err := stage.Execute(4)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal(12))
		})

		It("propagates handler errors to the caller", func() {
			stage := lifecycle.LifecycleStage[int]{
				Name: "failing",
				Handler: func(ctx int) (int, error) {
					return ctx, ErrStageFailed
				},
			}
			_, err := stage.Execute(0)
			Expect(err).To(MatchError(ErrStageFailed))
		})
	})

	Describe("zero-value fields", func() {
		It("leaves Hooks as a usable empty chain", func() {
			stage := lifecycle.LifecycleStage[int]{
				Name:    "bare",
				Handler: func(ctx int) (int, error) { return ctx + 1, nil },
			}
			out, err := stage.Execute(0)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal(1))
		})
	})
})
