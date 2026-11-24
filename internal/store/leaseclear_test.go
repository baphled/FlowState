package store

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// clearOwned is setup-style clearing of the delegate-owned lease on item.
func clearOwned(f leaseFixture, item string) {
	if err := f.s.ClearLeaseOwnedBy(f.ctx, item, "delegate:", "delegate"); err != nil {
		panic(err)
	}
}

var _ = Describe("ClearLeaseOwnedBy", func() {
	var ctx context.Context
	var f leaseFixture
	BeforeEach(func() {
		ctx = context.Background()
		f = leaseFixture{s: newTestStore(), ctx: ctx}
	})

	It("frees a lease whose owner starts with the prefix", func() {
		f.acquire("exec:c", "delegate:run-1")
		clearOwned(f, "exec:c")
		Expect(f.s.LeaseToken(ctx, "exec:c")).To(BeZero())
	})

	It("frees a lease owned by the exact legacy name", func() {
		f.acquire("exec:c", "delegate")
		clearOwned(f, "exec:c")
		Expect(f.s.LeaseToken(ctx, "exec:c")).To(BeZero())
	})

	It("keeps a lease owned by another name that only starts like the legacy one", func() {
		f.acquire("exec:c", "delegates")
		clearOwned(f, "exec:c")
		Expect(f.s.LeaseToken(ctx, "exec:c")).To(Equal(int64(1)))
	})

	It("keeps a live lease of another owner", func() {
		f.acquire("exec:c", "cli")
		clearOwned(f, "exec:c")
		Expect(f.s.LeaseToken(ctx, "exec:c")).To(Equal(int64(1)))
	})

	It("keeps the lease on another item", func() {
		f.acquire("exec:c", "delegate:run-1")
		f.acquire("exec:other", "delegate:run-2")
		clearOwned(f, "exec:c")
		Expect(f.s.LeaseToken(ctx, "exec:other")).To(Equal(int64(1)))
	})

	It("refuses an empty prefix", func() {
		f.acquire("exec:c", "cli")
		Expect(f.s.ClearLeaseOwnedBy(ctx, "exec:c", "")).To(MatchError(ErrEmptyLeaseOwner))
	})

	It("keeps the lease when the prefix is empty", func() {
		f.acquire("exec:c", "cli")
		_ = f.s.ClearLeaseOwnedBy(ctx, "exec:c", "")
		Expect(f.s.LeaseToken(ctx, "exec:c")).To(Equal(int64(1)))
	})

	It("refuses an empty exact name", func() {
		f.acquire("exec:c", "cli")
		Expect(f.s.ClearLeaseOwnedBy(ctx, "exec:c", "delegate:", "")).To(MatchError(ErrEmptyLeaseOwner))
	})

	It("does nothing for a free item", func() {
		Expect(f.s.ClearLeaseOwnedBy(ctx, "exec:none", "delegate:")).To(Succeed())
	})

	It("lets the next owner continue the token sequence", func() {
		f.acquire("exec:c", "delegate:run-1")
		clearOwned(f, "exec:c")
		Expect(f.acquire("exec:c", "delegate:run-2")).To(Equal(int64(2)))
	})
})
