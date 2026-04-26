package coordination_test

import (
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/coordination"
)

// PersistingStore wraps any coordination.Store with an approval observer:
// any Set on `<chainID>/review` carrying "APPROVE" fires a callback so a
// downstream component (App.PersistApprovedPlan) can flush the
// `<chainID>/plan` text to disk. The wrapper is a defence-in-depth backup
// behind the agent-facing plan_write tool — the agent's primary flow
// already writes the plan to disk, but if it forgets, the post-review
// approval still triggers persistence.
//
// These specs lock the trigger conditions, the chainID extraction, the
// async-callback contract, and the safety properties (nil callback,
// non-review writes, non-approval verdicts).
var _ = Describe("PersistingStore", func() {
	type capture struct {
		chainID atomic.Pointer[string]
		called  atomic.Int32
	}

	// awaitCallback polls the call counter for up to 500ms so the spec
	// is robust to the goroutine the wrapper spawns. Used by every
	// "did fire" assertion.
	awaitCallback := func(c *capture) {
		Eventually(func() int32 {
			return c.called.Load()
		}, 500*time.Millisecond, 10*time.Millisecond).Should(BeNumerically(">", 0))
	}

	// observe builds a callback + capture pair with a small async delay
	// so we can verify the callback is run asynchronously (the wrapper's
	// Set must not block on it).
	observe := func() (*capture, coordination.ApprovalCallback) {
		c := &capture{}
		return c, func(chainID string, _ coordination.Store) {
			id := chainID
			c.chainID.Store(&id)
			c.called.Add(1)
		}
	}

	It("fires the approval callback when <chainID>/review carries APPROVE", func() {
		inner := coordination.NewMemoryStore()
		c, cb := observe()
		ps := coordination.NewPersistingStore(inner, cb)

		Expect(ps.Set("plan-1/review", []byte("Verdict: APPROVE"))).To(Succeed())

		awaitCallback(c)
		Expect(*c.chainID.Load()).To(Equal("plan-1"),
			"callback must receive the chainID with the trailing /review stripped")
		Expect(c.called.Load()).To(Equal(int32(1)))
	})

	It("does NOT fire on review writes that do not contain APPROVE", func() {
		inner := coordination.NewMemoryStore()
		c, cb := observe()
		ps := coordination.NewPersistingStore(inner, cb)

		Expect(ps.Set("plan-1/review", []byte("Verdict: REJECT — schema mismatch"))).To(Succeed())

		// Give the goroutine a chance to run if it was going to.
		Consistently(func() int32 {
			return c.called.Load()
		}, 100*time.Millisecond, 10*time.Millisecond).Should(Equal(int32(0)),
			"REJECT verdicts must not trigger persistence")
	})

	It("does NOT fire on writes to non-review keys even if they contain APPROVE text", func() {
		inner := coordination.NewMemoryStore()
		c, cb := observe()
		ps := coordination.NewPersistingStore(inner, cb)

		Expect(ps.Set("plan-1/plan", []byte("# Plan\n\nThe approval workflow is..."))).To(Succeed())
		Expect(ps.Set("plan-1/analysis", []byte("APPROVE the analysis"))).To(Succeed())

		Consistently(func() int32 {
			return c.called.Load()
		}, 100*time.Millisecond, 10*time.Millisecond).Should(Equal(int32(0)),
			"only the canonical /review key triggers the callback")
	})

	It("does NOT fire on a bare 'review' key with no chain prefix", func() {
		inner := coordination.NewMemoryStore()
		c, cb := observe()
		ps := coordination.NewPersistingStore(inner, cb)

		Expect(ps.Set("review", []byte("APPROVE"))).To(Succeed())

		Consistently(func() int32 {
			return c.called.Load()
		}, 100*time.Millisecond, 10*time.Millisecond).Should(Equal(int32(0)),
			"the chainID prefix is required so the callback can scope its work")
	})

	It("propagates errors from the inner Set without firing the callback", func() {
		inner := &erroringStore{err: errFakeWrite}
		c, cb := observe()
		ps := coordination.NewPersistingStore(inner, cb)

		err := ps.Set("plan-1/review", []byte("APPROVE"))
		Expect(err).To(MatchError(errFakeWrite),
			"failed inner Set must surface the underlying error")

		Consistently(func() int32 {
			return c.called.Load()
		}, 100*time.Millisecond, 10*time.Millisecond).Should(Equal(int32(0)),
			"a write that didn't actually persist must not trigger downstream side effects")
	})

	It("acts as a transparent passthrough when the callback is nil", func() {
		inner := coordination.NewMemoryStore()
		ps := coordination.NewPersistingStore(inner, nil)

		Expect(ps.Set("plan-1/review", []byte("APPROVE"))).To(Succeed())
		got, err := ps.Get("plan-1/review")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal("APPROVE"))
	})

	It("delegates Get/List/Delete/Increment to the inner store", func() {
		inner := coordination.NewMemoryStore()
		ps := coordination.NewPersistingStore(inner, nil)

		Expect(ps.Set("ns/key1", []byte("v1"))).To(Succeed())
		Expect(ps.Set("ns/key2", []byte("v2"))).To(Succeed())
		Expect(ps.Set("other/key", []byte("v3"))).To(Succeed())

		v, err := ps.Get("ns/key1")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(v)).To(Equal("v1"))

		keys, err := ps.List("ns/")
		Expect(err).NotTo(HaveOccurred())
		Expect(keys).To(ConsistOf("ns/key1", "ns/key2"))

		Expect(ps.Delete("ns/key1")).To(Succeed())
		_, err = ps.Get("ns/key1")
		Expect(err).To(MatchError(coordination.ErrKeyNotFound))

		n, err := ps.Increment("counter")
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
	})
})

// errFakeWrite is the sentinel returned by erroringStore.Set so the spec
// can assert exact error propagation via MatchError.
var errFakeWrite = &fakeErr{msg: "simulated coord-store write failure"}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

// erroringStore is a minimal coordination.Store that fails Set with a
// configurable error. Used to verify PersistingStore's error-propagation
// behaviour without standing up the file-backed store and breaking it.
type erroringStore struct{ err error }

func (s *erroringStore) Get(string) ([]byte, error)         { return nil, s.err }
func (s *erroringStore) Set(string, []byte) error           { return s.err }
func (s *erroringStore) List(string) ([]string, error)      { return nil, s.err }
func (s *erroringStore) Delete(string) error                { return s.err }
func (s *erroringStore) Increment(string) (int, error)      { return 0, s.err }
