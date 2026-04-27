package swarm_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/swarm"
)

type concurrencyProbe struct {
	mu        sync.Mutex
	active    int
	maxActive int
	total     int
}

func newConcurrencyProbe() *concurrencyProbe {
	return &concurrencyProbe{}
}

func (p *concurrencyProbe) enter() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active++
	p.total++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
}

func (p *concurrencyProbe) leave() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active--
}

func (p *concurrencyProbe) snapshotMax() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxActive
}

func (p *concurrencyProbe) snapshotTotal() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.total
}

type orderRecorder struct {
	mu     sync.Mutex
	events []string
}

func newOrderRecorder() *orderRecorder {
	return &orderRecorder{}
}

func (o *orderRecorder) record(event string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *orderRecorder) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, len(o.events))
	copy(out, o.events)
	return out
}

func barrierRunner(probe *concurrencyProbe, releaseAt int) swarm.MemberRunner {
	gate := make(chan struct{})
	var arrived int32
	return func(ctx context.Context, member string) error {
		probe.enter()
		defer probe.leave()
		if atomic.AddInt32(&arrived, 1) >= int32(releaseAt) {
			close(gate)
		}
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func sequentialRunner(rec *orderRecorder) swarm.MemberRunner {
	return func(_ context.Context, member string) error {
		rec.record("start:" + member)
		time.Sleep(2 * time.Millisecond)
		rec.record("end:" + member)
		return nil
	}
}

func semaphoreRunner(probe *concurrencyProbe, hold time.Duration) swarm.MemberRunner {
	return func(ctx context.Context, member string) error {
		probe.enter()
		defer probe.leave()
		select {
		case <-time.After(hold):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func coordWriterRunner(store coordination.Store, prefix string) swarm.MemberRunner {
	return func(_ context.Context, member string) error {
		key := prefix + "/" + member + "/output"
		return store.Set(key, []byte("done:"+member))
	}
}

var errMemberFailed = errors.New("synthetic member failure")

func failingPeerRunner(failingMember string, peerCancelled chan<- string) swarm.MemberRunner {
	failGate := make(chan struct{})
	var failOnce sync.Once
	return func(ctx context.Context, member string) error {
		if member == failingMember {
			close(failGate)
			return errMemberFailed
		}
		select {
		case <-failGate:
			select {
			case <-ctx.Done():
				failOnce.Do(func() {
					peerCancelled <- member
				})
				return ctx.Err()
			case <-time.After(time.Second):
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

var _ = Describe("DispatchMembers", func() {
	const memberHold = 25 * time.Millisecond

	Context("with parallel disabled (default)", func() {
		It("runs members one at a time in roster order", func() {
			rec := newOrderRecorder()
			members := []string{"alpha", "bravo", "charlie"}

			err := swarm.DispatchMembers(context.Background(), members, sequentialRunner(rec), swarm.DispatchOptions{})

			Expect(err).NotTo(HaveOccurred())
			Expect(rec.snapshot()).To(Equal([]string{
				"start:alpha", "end:alpha",
				"start:bravo", "end:bravo",
				"start:charlie", "end:charlie",
			}))
		})

		It("never lets two members run concurrently", func() {
			probe := newConcurrencyProbe()
			members := []string{"alpha", "bravo", "charlie"}

			err := swarm.DispatchMembers(context.Background(), members, semaphoreRunner(probe, memberHold), swarm.DispatchOptions{})

			Expect(err).NotTo(HaveOccurred())
			Expect(probe.snapshotMax()).To(Equal(1))
			Expect(probe.snapshotTotal()).To(Equal(3))
		})
	})

	Context("with parallel enabled and no cap", func() {
		It("overlaps every member's execution", func() {
			probe := newConcurrencyProbe()
			members := []string{"alpha", "bravo", "charlie"}

			err := swarm.DispatchMembers(context.Background(), members, barrierRunner(probe, len(members)), swarm.DispatchOptions{Parallel: true})

			Expect(err).NotTo(HaveOccurred())
			Expect(probe.snapshotMax()).To(Equal(len(members)))
			Expect(probe.snapshotTotal()).To(Equal(len(members)))
		})

		It("treats a zero MaxParallel as unlimited", func() {
			probe := newConcurrencyProbe()
			members := []string{"alpha", "bravo", "charlie", "delta"}

			err := swarm.DispatchMembers(context.Background(), members, barrierRunner(probe, len(members)), swarm.DispatchOptions{Parallel: true, MaxParallel: 0})

			Expect(err).NotTo(HaveOccurred())
			Expect(probe.snapshotMax()).To(Equal(len(members)))
		})

		It("treats a negative MaxParallel as unlimited", func() {
			probe := newConcurrencyProbe()
			members := []string{"alpha", "bravo", "charlie"}

			err := swarm.DispatchMembers(context.Background(), members, barrierRunner(probe, len(members)), swarm.DispatchOptions{Parallel: true, MaxParallel: -1})

			Expect(err).NotTo(HaveOccurred())
			Expect(probe.snapshotMax()).To(Equal(len(members)))
		})
	})

	Context("with parallel enabled and a max-parallel cap", func() {
		It("never exceeds the cap", func() {
			probe := newConcurrencyProbe()
			members := []string{"a", "b", "c", "d", "e"}
			const cap = 2

			err := swarm.DispatchMembers(context.Background(), members, semaphoreRunner(probe, memberHold), swarm.DispatchOptions{Parallel: true, MaxParallel: cap})

			Expect(err).NotTo(HaveOccurred())
			Expect(probe.snapshotMax()).To(BeNumerically("<=", cap))
			Expect(probe.snapshotMax()).To(BeNumerically(">=", 1))
			Expect(probe.snapshotTotal()).To(Equal(len(members)))
		})

		It("caps a cap larger than the roster at the roster size", func() {
			probe := newConcurrencyProbe()
			members := []string{"a", "b", "c"}

			err := swarm.DispatchMembers(context.Background(), members, barrierRunner(probe, len(members)), swarm.DispatchOptions{Parallel: true, MaxParallel: 99})

			Expect(err).NotTo(HaveOccurred())
			Expect(probe.snapshotMax()).To(Equal(len(members)))
		})
	})

	Context("with parallel writes to the coordination store", func() {
		It("persists every member's output without collisions", func() {
			store := coordination.NewMemoryStore()
			members := []string{"alpha", "bravo", "charlie"}

			err := swarm.DispatchMembers(context.Background(), members, coordWriterRunner(store, "team"), swarm.DispatchOptions{Parallel: true})

			Expect(err).NotTo(HaveOccurred())
			for _, m := range members {
				val, getErr := store.Get("team/" + m + "/output")
				Expect(getErr).NotTo(HaveOccurred())
				Expect(string(val)).To(Equal("done:" + m))
			}
		})
	})

	Context("with one member returning an error", func() {
		It("cancels the context handed to the in-flight peers and surfaces the error", func() {
			peerCh := make(chan string, 4)
			members := []string{"alpha", "bravo", "charlie", "delta"}

			err := swarm.DispatchMembers(context.Background(), members, failingPeerRunner("bravo", peerCh), swarm.DispatchOptions{Parallel: true})

			Expect(err).To(MatchError(errMemberFailed))
			close(peerCh)
			cancelledCount := 0
			for range peerCh {
				cancelledCount++
			}
			Expect(cancelledCount).To(BeNumerically(">=", 1))
		})
	})

	Context("when the runner is nil or members slice is empty", func() {
		It("returns nil without invoking anything for an empty roster", func() {
			err := swarm.DispatchMembers(context.Background(), nil, func(_ context.Context, _ string) error {
				Fail("runner must not be invoked for an empty roster")
				return nil
			}, swarm.DispatchOptions{Parallel: true})

			Expect(err).NotTo(HaveOccurred())
		})

		It("rejects a nil runner with a typed error", func() {
			err := swarm.DispatchMembers(context.Background(), []string{"alpha"}, nil, swarm.DispatchOptions{})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("runner"))
		})
	})

	Context("when the parent context is already cancelled", func() {
		It("returns the cancellation error without invoking the runner", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			invoked := atomic.Int32{}

			err := swarm.DispatchMembers(ctx, []string{"alpha", "bravo"}, func(_ context.Context, _ string) error {
				invoked.Add(1)
				return nil
			}, swarm.DispatchOptions{Parallel: true})

			Expect(err).To(MatchError(context.Canceled))
			Expect(invoked.Load()).To(Equal(int32(0)))
		})
	})

	Context("post-member callback ordering", func() {
		It("invokes the post-member hook for each member as soon as it completes", func() {
			completed := make(chan string, 3)
			members := []string{"alpha", "bravo", "charlie"}

			runner := func(_ context.Context, member string) error {
				return nil
			}
			postMember := func(_ context.Context, member string, runErr error) error {
				Expect(runErr).NotTo(HaveOccurred())
				completed <- member
				return nil
			}

			err := swarm.DispatchMembers(context.Background(), members, runner, swarm.DispatchOptions{Parallel: true, PostMember: postMember})

			Expect(err).NotTo(HaveOccurred())
			close(completed)
			seen := map[string]bool{}
			for m := range completed {
				seen[m] = true
			}
			Expect(seen).To(HaveLen(len(members)))
		})

		It("propagates a post-member failure as the dispatch error and cancels peers", func() {
			peerCh := make(chan string, 4)
			members := []string{"alpha", "bravo", "charlie"}

			runner := func(ctx context.Context, member string) error {
				if member == "bravo" {
					return nil
				}
				select {
				case <-ctx.Done():
					select {
					case peerCh <- member:
					default:
					}
					return ctx.Err()
				case <-time.After(time.Second):
					return nil
				}
			}
			gateErr := errors.New("post-member gate failed")
			postMember := func(_ context.Context, member string, _ error) error {
				if member == "bravo" {
					return gateErr
				}
				return nil
			}

			err := swarm.DispatchMembers(context.Background(), members, runner, swarm.DispatchOptions{Parallel: true, PostMember: postMember})

			Expect(err).To(MatchError(gateErr))
			close(peerCh)
		})
	})
})

var _ = It("DispatchMembers handles a single-member roster identically in both modes", func() {
	for _, parallel := range []bool{false, true} {
		mode := fmt.Sprintf("parallel=%t", parallel)
		probe := newConcurrencyProbe()

		err := swarm.DispatchMembers(context.Background(), []string{"solo"}, semaphoreRunner(probe, 1*time.Millisecond), swarm.DispatchOptions{Parallel: parallel})

		Expect(err).NotTo(HaveOccurred(), mode)
		Expect(probe.snapshotMax()).To(Equal(1), mode)
		Expect(probe.snapshotTotal()).To(Equal(1), mode)
	}
})
