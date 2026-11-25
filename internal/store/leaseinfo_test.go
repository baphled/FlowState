package store

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// seedLeaseRow inserts a full row, as the judging acquire of a later packet will.
func seedLeaseRow(s *Store, row leaseRow) {
	if err := s.gdb.Create(&row).Error; err != nil {
		panic(err)
	}
}

var _ = Describe("ReadLease", func() {
	var ctx context.Context
	var s *Store
	BeforeEach(func() {
		ctx = context.Background()
		s = newTestStore()
	})

	DescribeTable("returns each field of the written row", func(field func(LeaseInfo) any, want any) {
		seedLeaseRow(s, leaseRow{Item: "exec:a", Agent: "run:1", Token: 4, AcquiredAtUTC: "2026-01-02T03:04:05.000Z",
			Machine: "m-hash", Host: "box", Pid: 4242})
		info, ok, err := s.ReadLease(ctx, "exec:a")
		if err != nil || !ok {
			panic("read failed")
		}
		Expect(field(info)).To(Equal(want))
	},
		Entry("item", func(i LeaseInfo) any { return i.Item }, "exec:a"),
		Entry("owner", func(i LeaseInfo) any { return i.Owner }, "run:1"),
		Entry("token", func(i LeaseInfo) any { return i.Token }, int64(4)),
		Entry("machine", func(i LeaseInfo) any { return i.Machine }, "m-hash"),
		Entry("host", func(i LeaseInfo) any { return i.Host }, "box"),
		Entry("pid", func(i LeaseInfo) any { return i.Pid }, int64(4242)),
		Entry("acquired time", func(i LeaseInfo) any { return i.AcquiredAt }, "2026-01-02T03:04:05.000Z"),
	)

	It("reports a legacy row with an empty machine", func() {
		leaseFixture{s, ctx}.acquire("exec:a", "cli")
		info, _, _ := s.ReadLease(ctx, "exec:a")
		Expect(info.Machine).To(BeEmpty())
	})

	It("returns a zero LeaseInfo for an item with no row", func() {
		info, _, _ := s.ReadLease(ctx, "exec:none")
		Expect(info).To(Equal(LeaseInfo{}))
	})

	It("reports false for an item with no row", func() {
		_, ok, _ := s.ReadLease(ctx, "exec:none")
		Expect(ok).To(BeFalse())
	})

	It("fails on a closed store", func() {
		if err := s.Close(); err != nil {
			panic(err)
		}
		_, _, err := s.ReadLease(ctx, "exec:a")
		Expect(err).To(MatchError(ErrClosed))
	})
})

var _ = Describe("DeleteLeaseIf", func() {
	var ctx context.Context
	var s *Store
	BeforeEach(func() {
		ctx = context.Background()
		s = newTestStore()
		seedLeaseRow(s, leaseRow{Item: "exec:a", Agent: "run:1", Token: 4, AcquiredAtUTC: "t", Machine: "m"})
	})

	It("deletes a row whose owner and token match", func() {
		deleted, _ := s.DeleteLeaseIf(ctx, "exec:a", "run:1", 4)
		Expect(deleted).To(BeTrue())
	})

	It("removes the matching row", func() {
		if _, err := s.DeleteLeaseIf(ctx, "exec:a", "run:1", 4); err != nil {
			panic(err)
		}
		Expect(s.LeaseToken(ctx, "exec:a")).To(BeZero())
	})

	It("keeps a row whose token differs", func() {
		deleted, _ := s.DeleteLeaseIf(ctx, "exec:a", "run:1", 3)
		Expect(deleted).To(BeFalse())
	})

	It("keeps the row when the token differs", func() {
		_, _ = s.DeleteLeaseIf(ctx, "exec:a", "run:1", 5)
		Expect(s.LeaseToken(ctx, "exec:a")).To(Equal(int64(4)))
	})

	It("keeps a row whose owner differs", func() {
		_, _ = s.DeleteLeaseIf(ctx, "exec:a", "run:2", 4)
		Expect(s.LeaseToken(ctx, "exec:a")).To(Equal(int64(4)))
	})

	It("reports false for an owner mismatch", func() {
		deleted, _ := s.DeleteLeaseIf(ctx, "exec:a", "run:2", 4)
		Expect(deleted).To(BeFalse())
	})

	It("keeps the row of another item", func() {
		seedLeaseRow(s, leaseRow{Item: "exec:b", Agent: "run:1", Token: 4, AcquiredAtUTC: "t"})
		_, _ = s.DeleteLeaseIf(ctx, "exec:a", "run:1", 4)
		Expect(s.LeaseToken(ctx, "exec:b")).To(Equal(int64(4)))
	})

	It("reports false for an item with no row", func() {
		deleted, _ := s.DeleteLeaseIf(ctx, "exec:none", "run:1", 4)
		Expect(deleted).To(BeFalse())
	})
})

var _ = Describe("TryItemLock", func() {
	var s *Store
	BeforeEach(func() { s = newTestStore() })

	It("refuses a second lock on the same item", func() {
		l, err := s.TryItemLock("exec:a")
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = l.Release() })
		_, err = s.TryItemLock("exec:a")
		Expect(err).To(MatchError(ErrLockHeld))
	})

	It("locks again once released", func() {
		l, err := s.TryItemLock("exec:a")
		if err != nil {
			panic(err)
		}
		if err := l.Release(); err != nil {
			panic(err)
		}
		again, _ := s.TryItemLock("exec:a")
		DeferCleanup(func() { _ = again.Release() })
		Expect(again).NotTo(BeNil())
	})

	It("excludes within one process on a path-less store", func() {
		mem, err := OpenMemory()
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = mem.Close() })
		l, err := mem.TryItemLock("exec:a")
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = l.Release() })
		_, err = mem.TryItemLock("exec:a")
		Expect(err).To(MatchError(ErrLockHeld))
	})
})
