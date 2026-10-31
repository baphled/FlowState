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
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = w.Close() })
	return s, w, sid
}

func payloadMap(e RecordedEvent) map[string]any {
	var m map[string]any
	err := json.Unmarshal([]byte(e.PayloadJSON), &m)
	if err != nil {
		panic(err)
	}
	return m
}

func pauseForTest(w *EventWriter) {
	if err := w.Pause(context.Background(), PauseInfo{Reason: "budget"}); err != nil {
		panic(err)
	}
}

func replayForTest(s *Store, sid string) []RecordedEvent {
	evs, err := s.Replay(context.Background(), sid)
	if err != nil {
		panic(err)
	}
	return evs
}

var _ = Describe("EventWriter Pause/Resume", func() {
	It("pause appends a session.paused event", func() {
		s, w, sid := newPausedFixture()
		err := w.Pause(context.Background(), PauseInfo{Reason: "budget", ConsumedBudget: 42, NextSafeOp: "rerun tool"})
		if err != nil {
			panic(err)
		}
		evs := replayForTest(s, sid)
		Expect(evs[0].Type).To(Equal("session.paused"))
	})

	It("pause appends the pause payload with reason, budget and next_safe_op", func() {
		s, w, sid := newPausedFixture()
		err := w.Pause(context.Background(), PauseInfo{Reason: "budget", ConsumedBudget: 42, NextSafeOp: "rerun tool"})
		if err != nil {
			panic(err)
		}
		evs := replayForTest(s, sid)
		Expect(payloadMap(evs[0])).To(Equal(map[string]any{
			"reason":          "budget",
			"consumed_budget": float64(42),
			"next_safe_op":    "rerun tool",
		}))
	})

	It("pause sets the session status to paused", func() {
		s, w, sid := newPausedFixture()
		err := w.Pause(context.Background(), PauseInfo{Reason: "budget"})
		if err != nil {
			panic(err)
		}
		sess, err := s.GetSession(context.Background(), sid)
		if err != nil {
			panic(err)
		}
		Expect(sess.Status).To(Equal("paused"))
	})

	It("pause records engine as the actor", func() {
		s, w, sid := newPausedFixture()
		err := w.Pause(context.Background(), PauseInfo{Reason: "budget"})
		if err != nil {
			panic(err)
		}
		evs := replayForTest(s, sid)
		Expect(evs[0].Actor).To(Equal("engine"))
	})

	It("pause with empty NextSafeOp defaults it to session run <id>", func() {
		s, w, sid := newPausedFixture()
		err := w.Pause(context.Background(), PauseInfo{Reason: "budget"})
		if err != nil {
			panic(err)
		}
		evs := replayForTest(s, sid)
		Expect(payloadMap(evs[0])["next_safe_op"]).To(Equal("session run <id>"))
	})

	It("resume appends a session.resumed event", func() {
		s, w, sid := newPausedFixture()
		pauseForTest(w)
		err := w.Resume(context.Background())
		if err != nil {
			panic(err)
		}
		evs := replayForTest(s, sid)
		Expect(evs[1].Type).To(Equal("session.resumed"))
	})

	It("resume records the user as the actor", func() {
		s, w, sid := newPausedFixture()
		pauseForTest(w)
		err := w.Resume(context.Background())
		if err != nil {
			panic(err)
		}
		evs := replayForTest(s, sid)
		Expect(evs[1].Actor).To(Equal("user"))
	})

	It("resume appends a from_status paused payload", func() {
		s, w, sid := newPausedFixture()
		pauseForTest(w)
		err := w.Resume(context.Background())
		if err != nil {
			panic(err)
		}
		evs := replayForTest(s, sid)
		Expect(payloadMap(evs[1])).To(Equal(map[string]any{"from_status": "paused"}))
	})

	It("resume sets the session status back to active", func() {
		s, w, sid := newPausedFixture()
		pauseForTest(w)
		err := w.Resume(context.Background())
		if err != nil {
			panic(err)
		}
		sess, err := s.GetSession(context.Background(), sid)
		if err != nil {
			panic(err)
		}
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
		evs := replayForTest(s, sid)
		Expect(evs).To(BeEmpty())
	})

	It("pause and resume append exactly three events after an existing append", func() {
		s, w, sid := newPausedFixture()
		_, err := w.Append(context.Background(), "user", "turn.begin", map[string]any{"n": 1})
		if err != nil {
			panic(err)
		}
		pauseForTest(w)
		if err := w.Resume(context.Background()); err != nil {
			panic(err)
		}
		evs := replayForTest(s, sid)
		Expect(evs).To(HaveLen(3))
	})

	It("pause and resume keep seq contiguous after existing appends", func() {
		s, w, sid := newPausedFixture()
		_, err := w.Append(context.Background(), "user", "turn.begin", map[string]any{"n": 1})
		if err != nil {
			panic(err)
		}
		pauseForTest(w)
		if err := w.Resume(context.Background()); err != nil {
			panic(err)
		}
		evs := replayForTest(s, sid)
		Expect([]int64{evs[0].Seq, evs[1].Seq, evs[2].Seq}).To(Equal([]int64{0, 1, 2}))
	})
})
