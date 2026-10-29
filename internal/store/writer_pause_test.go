package store

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// newPausedFixture opens a fresh temp-DB store, creates a session and
// writer, and returns them for the spec to drive.
func newPausedFixture() (*Store, *EventWriter, string) {
	s := newTestStore()
	sid := mustSession(s)
	w, err := s.NewEventWriter(context.Background(), sid)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = w.Close() })
	return s, w, sid
}

func payloadMap(e RecordedEvent) map[string]any {
	var m map[string]any
	err := json.Unmarshal([]byte(e.PayloadJSON), &m)
	Expect(err).NotTo(HaveOccurred())
	return m
}

var _ = Describe("EventWriter Pause/Resume", func() {
	It("pause appends a session.paused event with engine actor and pause payload", func() {
		s, w, sid := newPausedFixture()
		err := w.Pause(context.Background(), PauseInfo{Reason: "budget", ConsumedBudget: 42, NextSafeOp: "rerun tool"})
		Expect(err).NotTo(HaveOccurred())
		evs, err := s.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs[0].Type).To(Equal("session.paused"))
		m := payloadMap(evs[0])
		Expect(m).To(Equal(map[string]any{
			"reason":          "budget",
			"consumed_budget": float64(42),
			"next_safe_op":    "rerun tool",
		}))
	})

	It("pause sets the session status to paused", func() {
		s, w, sid := newPausedFixture()
		err := w.Pause(context.Background(), PauseInfo{Reason: "budget"})
		Expect(err).NotTo(HaveOccurred())
		sess, err := s.GetSession(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(sess.Status).To(Equal("paused"))
	})

	It("pause records engine as the actor", func() {
		s, w, sid := newPausedFixture()
		err := w.Pause(context.Background(), PauseInfo{Reason: "budget"})
		Expect(err).NotTo(HaveOccurred())
		evs, err := s.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs[0].Actor).To(Equal("engine"))
	})

	It("pause with empty NextSafeOp defaults it to session run <id>", func() {
		s, w, sid := newPausedFixture()
		err := w.Pause(context.Background(), PauseInfo{Reason: "budget"})
		Expect(err).NotTo(HaveOccurred())
		evs, err := s.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(payloadMap(evs[0])["next_safe_op"]).To(Equal("session run <id>"))
	})

	It("resume appends a session.resumed event with user actor and from_status paused", func() {
		s, w, sid := newPausedFixture()
		Expect(w.Pause(context.Background(), PauseInfo{Reason: "budget"})).To(Succeed())
		err := w.Resume(context.Background())
		Expect(err).NotTo(HaveOccurred())
		evs, err := s.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs[1].Type).To(Equal("session.resumed"))
		Expect(evs[1].Actor).To(Equal("user"))
		Expect(payloadMap(evs[1])).To(Equal(map[string]any{"from_status": "paused"}))
	})

	It("resume sets the session status back to active", func() {
		s, w, sid := newPausedFixture()
		Expect(w.Pause(context.Background(), PauseInfo{Reason: "budget"})).To(Succeed())
		err := w.Resume(context.Background())
		Expect(err).NotTo(HaveOccurred())
		sess, err := s.GetSession(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(sess.Status).To(Equal("active"))
	})

	It("resume on a never-paused writer returns ErrNotPaused", func() {
		_, w, _ := newPausedFixture()
		err := w.Resume(context.Background())
		Expect(err).To(MatchError(ErrNotPaused))
	})

	It("resume on a never-paused writer appends no event", func() {
		s, w, sid := newPausedFixture()
		_ = w.Resume(context.Background())
		evs, err := s.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(BeEmpty())
	})

	It("pause and resume keep seq contiguous after existing appends", func() {
		s, w, sid := newPausedFixture()
		_, err := w.Append(context.Background(), "user", "turn.begin", map[string]any{"n": 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Pause(context.Background(), PauseInfo{Reason: "budget"})).To(Succeed())
		Expect(w.Resume(context.Background())).To(Succeed())
		evs, err := s.Replay(context.Background(), sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(HaveLen(3))
		Expect(evs[0].Seq).To(Equal(int64(0)))
		Expect(evs[1].Seq).To(Equal(int64(1)))
		Expect(evs[2].Seq).To(Equal(int64(2)))
	})
})
