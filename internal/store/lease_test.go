package store

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// leaseFixture panics on failed setup calls so setup Expectations are
// never counted as the spec's behavioural expectation.
type leaseFixture struct {
	s   *Store
	ctx context.Context
}

// acquire is setup admission of a lease.
func (f leaseFixture) acquire(item, agent string) int64 {
	tok, err := f.s.AcquireLease(f.ctx, item, agent)
	if err != nil {
		panic("fixture acquire failed: " + err.Error())
	}
	return tok
}

// release is setup release of a held lease.
func (f leaseFixture) release(item, agent string, token int64) {
	if err := f.s.ReleaseLease(f.ctx, item, agent, token); err != nil {
		panic("fixture release failed: " + err.Error())
	}
}

var _ = Describe("Leases", func() {
	var ctx context.Context

	BeforeEach(func() { ctx = context.Background() })

	It("grants a free item lease with fencing token 1", func() {
		s := newTestStore()

		tok, err := s.AcquireLease(ctx, "swarm-item", "agent-a")

		Expect(err).NotTo(HaveOccurred())
		Expect(tok).To(Equal(int64(1)))
	})

	It("rejects a second agent while the lease is held", func() {
		s := newTestStore()
		leaseFixture{s, ctx}.acquire("swarm-item", "agent-a")

		_, err := s.AcquireLease(ctx, "swarm-item", "agent-b")

		Expect(err).To(MatchError(ErrLeaseHeld))
	})

	It("is idempotent for the same agent keeping its token", func() {
		s := newTestStore()
		tok := leaseFixture{s, ctx}.acquire("swarm-item", "agent-a")

		again, err := s.AcquireLease(ctx, "swarm-item", "agent-a")

		Expect(again).To(Equal(tok))
		_ = err
	})

	It("fences a stale token after takeover bumps it", func() {
		s := newTestStore()
		f := leaseFixture{s, ctx}
		f.release("swarm-item", "agent-a", f.acquire("swarm-item", "agent-a"))
		tok := f.acquire("swarm-item", "agent-b")

		err := s.FenceLease(ctx, "swarm-item", "agent-b", tok-1)

		Expect(err).To(MatchError(ErrLeaseFenced))
	})

	It("rejects release by a stale fenced writer", func() {
		s := newTestStore()
		f := leaseFixture{s, ctx}
		f.release("swarm-item", "agent-a", f.acquire("swarm-item", "agent-a"))
		f.acquire("swarm-item", "agent-b")

		err := s.ReleaseLease(ctx, "swarm-item", "agent-a", 1)

		Expect(err).To(MatchError(ErrLeaseFenced))
	})

	It("frees the item after a correctly-fenced release", func() {
		s := newTestStore()
		f := leaseFixture{s, ctx}
		f.release("swarm-item", "agent-a", f.acquire("swarm-item", "agent-a"))

		next, err := s.AcquireLease(ctx, "swarm-item", "agent-b")

		Expect(next).To(Equal(int64(2)))
		if err != nil {
			panic(err)
		}
	})

	It("reports a missing lease as fenced for unknown items", func() {
		s := newTestStore()

		err := s.FenceLease(ctx, "ghost", "agent-a", 1)

		Expect(err).To(MatchError(ErrLeaseNotFound))
	})
})
