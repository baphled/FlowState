package store

import (
	"context"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CreateChildSession", func() {
	var ctx context.Context
	BeforeEach(func() { ctx = context.Background() })

	It("returns the created child linked to its parent", func() {
		s := newTestStore()
		parent := mustSession(s)
		child, err := s.CreateChildSession(ctx, "child-link", parent, "zai", "glm-5.3", "test-harness", "cfgsha")
		Expect(err).NotTo(HaveOccurred())
		Expect(child.ParentID).To(Equal(parent))
	})

	It("reads the parent linkage back through GetSession", func() {
		s := newTestStore()
		parent := mustSession(s)
		_, err := s.CreateChildSession(ctx, "child-read", parent, "zai", "glm-5.3", "test-harness", "cfgsha")
		Expect(err).NotTo(HaveOccurred())
		sess, err := s.GetSession(ctx, "child-read")
		Expect(err).NotTo(HaveOccurred())
		Expect(sess.ParentID).To(Equal(parent))
	})

	It("lists child sessions with their parent linkage", func() {
		s := newTestStore()
		parent := mustSession(s)
		_, err := s.CreateChildSession(ctx, "child-list", parent, "zai", "glm-5.3", "test-harness", "cfgsha")
		Expect(err).NotTo(HaveOccurred())
		all, err := s.ListSessions(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		linked := ""
		for _, row := range all {
			if row.ID == "child-list" {
				linked = row.ParentID
			}
		}
		Expect(linked).To(Equal(parent))
	})

	It("creates a root session when the parent id is empty", func() {
		s := newTestStore()
		child, err := s.CreateChildSession(ctx, "child-root", "", "zai", "glm-5.3", "test-harness", "cfgsha")
		Expect(err).NotTo(HaveOccurred())
		Expect(child.ParentID).To(Equal(""))
	})
})

// cancelledOnceStarted is a context whose cancellation lands just after
// an append began: it reports no error the first time it is asked and is
// cancelled, with its Done channel closed, from then on.
type cancelledOnceStarted struct {
	context.Context
	asked atomic.Bool
}

// Done is always closed: anything still watching the context stops.
func (c *cancelledOnceStarted) Done() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

// Err is nil for the first caller only.
func (c *cancelledOnceStarted) Err() error {
	if c.asked.Swap(true) {
		return context.Canceled
	}
	return nil
}

var _ = Describe("EventWriter.Append under cancellation", func() {
	var (
		s   *Store
		sid string
		w   *EventWriter
	)

	BeforeEach(func() {
		s = newTestStore()
		sid = mustSession(s)
		var err error
		w, err = s.NewEventWriter(context.Background(), sid)
		if err != nil {
			panic(err)
		}
		DeferCleanup(w.Close)
	})

	Context("when the context is already cancelled", func() {
		var err error

		BeforeEach(func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = w.Append(ctx, "engine", "assistant.delta", map[string]string{"delta": "x"})
		})

		It("refuses the append with the cancellation", func() {
			Expect(err).To(MatchError(context.Canceled))
		})

		It("writes nothing", func() {
			Expect(s.Replay(context.Background(), sid)).To(BeEmpty())
		})

		It("gives the next append the refused append's seq", func() {
			Expect(w.Append(context.Background(), "engine", "session.cancelled", nil)).To(Equal(int64(0)))
		})
	})

	Context("when the cancellation lands after the append began", func() {
		var seq int64

		BeforeEach(func() {
			ctx := &cancelledOnceStarted{Context: context.Background()}
			// A failed append reports seq -1, which the first spec rejects.
			seq, _ = w.Append(ctx, "engine", "assistant.delta", map[string]string{"delta": "x"})
		})

		It("completes the append and reports the committed seq", func() {
			Expect(seq).To(Equal(int64(0)))
		})

		It("commits the event", func() {
			Expect(s.Replay(context.Background(), sid)).To(HaveLen(1))
		})

		It("keeps the writer in step with the log, so recording the cancellation does not collide", func() {
			Expect(w.Append(context.Background(), "engine", "session.cancelled", nil)).To(Equal(int64(1)))
		})
	})
})
