package store

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DrainSteeringMessages sequence accounting", func() {
	var s *Store
	var w *EventWriter
	var ctx context.Context

	// failDelivery makes the second message's delivered-mark update
	// abort, so a drain fails after it has already inserted and marked
	// the first message, and rolls both back.
	failDelivery := func() {
		mustRawExec(s, `CREATE TRIGGER fail_delivery BEFORE UPDATE ON session_messages WHEN OLD.body = 'two'
			BEGIN SELECT RAISE(ABORT, 'forced delivery failure'); END`)
	}

	BeforeEach(func() {
		ctx = context.Background()
		s = newTestStore()
		seedChat(s)
		for _, body := range []string{"one", "two"} {
			if _, err := s.SendMessage(ctx, "chat", "root", body); err != nil {
				panic(err)
			}
		}
		var err error
		w, err = s.NewEventWriter(ctx, "chat")
		if err != nil {
			panic(err)
		}
		DeferCleanup(func() { _ = w.Close() })
	})

	It("advances the writer by the number of events a drain appended", func() {
		if _, err := w.DrainSteeringMessages(ctx, "chat"); err != nil {
			panic(err)
		}
		Expect(w.NextSeq()).To(Equal(int64(2)))
	})

	It("assigns the next append seq 2 after a successful two-message drain", func() {
		if _, err := w.DrainSteeringMessages(ctx, "chat"); err != nil {
			panic(err)
		}
		seq, err := w.Append(ctx, "user", "after", nil)
		if err != nil {
			panic(err)
		}
		Expect(seq).To(Equal(int64(2)))
	})

	It("rejects a drain for a session other than the writer's", func() {
		_, err := w.DrainSteeringMessages(ctx, "root")
		Expect(err).To(MatchError(ErrDrainSessionMismatch))
	})

	Context("when a drain names a sibling session that has a pending message", func() {
		BeforeEach(func() {
			if _, err := s.CreateChildSession(ctx, "chat2", "root", "zai", "glm-5.3", "test-harness", "cfgsha"); err != nil {
				panic(err)
			}
			if _, err := s.SendMessage(ctx, "chat2", "root", "for the sibling"); err != nil {
				panic(err)
			}
			if _, err := w.DrainSteeringMessages(ctx, "chat2"); err == nil {
				panic("drain unexpectedly succeeded")
			}
		})

		It("writes none of the sibling's messages into the writer's log", func() {
			evs, err := s.Replay(ctx, "chat")
			if err != nil {
				panic(err)
			}
			Expect(evs).To(BeEmpty())
		})

		It("leaves the sibling's message pending", func() {
			pending, err := s.PendingSteeringMessages(ctx, "chat2")
			if err != nil {
				panic(err)
			}
			Expect(pending).To(HaveLen(1))
		})

		It("leaves the writer's own messages pending", func() {
			n, err := w.DrainSteeringMessages(ctx, "chat")
			if err != nil {
				panic(err)
			}
			Expect(n).To(Equal(2))
		})
	})

	It("leaves the writer where it was when the drain rolls back", func() {
		failDelivery()
		if _, err := w.DrainSteeringMessages(ctx, "chat"); err == nil {
			panic("drain unexpectedly succeeded")
		}
		Expect(w.NextSeq()).To(Equal(int64(0)))
	})

	It("assigns the next append the first free seq after a rolled-back drain", func() {
		failDelivery()
		if _, err := w.DrainSteeringMessages(ctx, "chat"); err == nil {
			panic("drain unexpectedly succeeded")
		}
		seq, err := w.Append(ctx, "user", "after", nil)
		if err != nil {
			panic(err)
		}
		Expect(seq).To(Equal(int64(0)))
	})

	It("keeps the log replayable after a rolled-back drain and a later append", func() {
		failDelivery()
		if _, err := w.DrainSteeringMessages(ctx, "chat"); err == nil {
			panic("drain unexpectedly succeeded")
		}
		if _, err := w.Append(ctx, "user", "after", nil); err != nil {
			panic(err)
		}
		evs, err := s.Replay(ctx, "chat")
		if err != nil {
			panic(err)
		}
		Expect(evs).To(HaveLen(1))
	})
})
