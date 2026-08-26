package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

var _ = Describe("Delegate context prior messages isolation", func() {
	It("strips parent PriorMessages when creating delegate context", func() {
		ctx := context.Background()
		ctx = session.WithPriorMessages(ctx, []provider.Message{{
			Role:    "user",
			Content: "parent history",
		}})

		_, hasPrior := session.PriorMessagesFromContext(ctx)
		Expect(hasPrior).To(BeTrue(), "parent context should have PriorMessagesKey attached")

		delegateCtx := context.WithValue(ctx, session.IDKey{}, "child-session")
		delegateCtx = session.WithPriorMessages(delegateCtx, nil)

		msgs, hasPrior := session.PriorMessagesFromContext(delegateCtx)
		Expect(hasPrior).To(BeTrue(), "delegate context should have PriorMessagesKey attached")
		Expect(msgs).To(BeEmpty(), "delegate context should carry an empty slice")
	})

	It("returns empty non-nil slice when key is present", func() {
		ctx := context.Background()
		ctx = session.WithPriorMessages(ctx, nil)

		msgs, hasPrior := session.PriorMessagesFromContext(ctx)
		Expect(hasPrior).To(BeTrue())
		Expect(msgs).ToNot(BeNil(), "WithPriorMessages materialises a non-nil slice")
		Expect(msgs).To(BeEmpty())
	})
})
