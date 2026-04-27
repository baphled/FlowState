package swarm_test

import (
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("CategorisedError", func() {
	Context("category surfacing via errors.As", func() {
		It("retains the category through a wrap chain", func() {
			cause := errors.New("transient connection reset")
			wrapped := swarm.NewCategorisedError(swarm.CategoryRetryable, cause, "explorer")
			outer := fmt.Errorf("dispatch: %w", wrapped)

			var ce *swarm.CategorisedError
			Expect(errors.As(outer, &ce)).To(BeTrue())
			Expect(ce.Category).To(Equal(swarm.CategoryRetryable))
			Expect(errors.Is(outer, cause)).To(BeTrue())
		})

		It("classifies a terminal error as non-retryable", func() {
			err := swarm.NewCategorisedError(swarm.CategoryTerminal, errors.New("manifest invalid"), "")

			Expect(swarm.IsRetryable(err)).To(BeFalse())
			Expect(err.Category).To(Equal(swarm.CategoryTerminal))
		})

		It("classifies a retryable error as retryable", func() {
			err := swarm.NewCategorisedError(swarm.CategoryRetryable, errors.New("io timeout"), "explorer")

			Expect(swarm.IsRetryable(err)).To(BeTrue())
		})

		It("treats an uncategorised error as terminal by default", func() {
			err := errors.New("plain error")

			Expect(swarm.IsRetryable(err)).To(BeFalse())
		})
	})

	Context("sub_swarm_path tracing", func() {
		It("renders the path in the error message", func() {
			cause := errors.New("dispatch failed")
			err := swarm.NewCategorisedError(swarm.CategoryTerminal, cause, "explorer")
			err.SubSwarmPath = "bug-hunt/cluster-2"

			Expect(err.Error()).To(ContainSubstring("bug-hunt/cluster-2/explorer"))
		})

		It("falls back to member-only when path is empty", func() {
			err := swarm.NewCategorisedError(swarm.CategoryTerminal, errors.New("boom"), "explorer")

			Expect(err.Error()).To(ContainSubstring("explorer"))
		})

		It("renders just the path when member is empty", func() {
			err := swarm.NewCategorisedError(swarm.CategoryTerminal, errors.New("boom"), "")
			err.SubSwarmPath = "root/child"

			Expect(err.Error()).To(ContainSubstring("root/child"))
		})
	})
})
