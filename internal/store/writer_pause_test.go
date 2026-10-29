package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// newStoreWriter builds a temp store, session and writer; the spec
// asserts the returned setup error.
func newStoreWriter() (*Store, *EventWriter, string, error) {
	s, err := Open(filepath.Join(GinkgoT().TempDir(), "ws.db"))
	if err != nil {
		return nil, nil, "", err
	}
	DeferCleanup(func() { _ = s.Close() })
	id := fmt.Sprintf("sid-%d", time.Now().UnixNano())
	if _, err := s.CreateSession(context.Background(), id, "zai", "glm-4.6", "test-harness", "cfgsha"); err != nil {
		return nil, nil, "", err
	}
	w, err := s.NewEventWriter(context.Background(), id)
	return s, w, id, err
}

// pauseFor pauses w with pi, returning any error for the spec to assert.
func pauseFor(w *EventWriter, pi PauseInfo) error {
	return w.Pause(context.Background(), pi)
}

// resumeFor resumes w, returning any error for the spec to assert.
func resumeFor(w *EventWriter) error {
	return w.Resume(context.Background())
}

// replayEvents returns the full event log for sid plus any error.
func replayEvents(s *Store, sid string) ([]RecordedEvent, error) {
	return s.Replay(context.Background(), sid)
}

// getSession returns the session row for sid plus any error.
func getSession(s *Store, sid string) (*Session, error) {
	return s.GetSession(context.Background(), sid)
}

var _ = Describe("EventWriter Pause/Resume", func() {
	It("pause appends a session.paused event with the pause payload", func() {
		s, w, sid, err := newStoreWriter()
		Expect(err).NotTo(HaveOccurred())
		err = pauseFor(w, PauseInfo{Reason: "budget", ConsumedBudget: 42, NextSafeOp: "rerun tool"})
		Expect(err).NotTo(HaveOccurred())
		evs, _ := replayEvents(s, sid)
		var m map[string]any
		mErr := json.Unmarshal([]byte(evs[0].PayloadJSON), &m)
		Expect(mErr).NotTo(HaveOccurred())
		Expect(m).To(Equal(map[string]any{
			"reason":          "budget",
			"consumed_budget": float64(42),
			"next_safe_op":    "rerun tool",
		}))
	})

	It("pause appends a session.paused event", func() {
		s, w, sid, err := newStoreWriter()
		Expect(err).NotTo(HaveOccurred())
		err = pauseFor(w, PauseInfo{Reason: "budget"})
		Expect(err).NotTo(HaveOccurred())
		evs, _ := replayEvents(s, sid)
		Expect(evs[0].Type).To(Equal("session.paused"))
	})

	It("pause records engine as the actor", func() {
		s, w, sid, err := newStoreWriter()
		Expect(err).NotTo(HaveOccurred())
		err = pauseFor(w, PauseInfo{Reason: "budget"})
		Expect(err).NotTo(HaveOccurred())
		evs, _ := replayEvents(s, sid)
		Expect(evs[0].Actor).To(Equal("engine"))
	})

	It("pause with empty NextSafeOp defaults it to session run <id>", func() {
		s, w, sid, err := newStoreWriter()
		Expect(err).NotTo(HaveOccurred())
		err = pauseFor(w, PauseInfo{Reason: "budget"})
		Expect(err).NotTo(HaveOccurred())
		evs, _ := replayEvents(s, sid)
		var m map[string]any
		mErr := json.Unmarshal([]byte(evs[0].PayloadJSON), &m)
		Expect(mErr).NotTo(HaveOccurred())
		Expect(m["next_safe_op"]).To(Equal("session run <id>"))
	})

	It("pause sets the session status to paused", func() {
		s, w, sid, err := newStoreWriter()
		Expect(err).NotTo(HaveOccurred())
		err = pauseFor(w, PauseInfo{Reason: "budget"})
		Expect(err).NotTo(HaveOccurred())
		sess, _ := getSession(s, sid)
		Expect(sess.Status).To(Equal("paused"))
	})

	It("resume appends a session.resumed event with the from_status payload", func() {
		s, w, sid, err := newStoreWriter()
		Expect(err).NotTo(HaveOccurred())
		err = pauseFor(w, PauseInfo{Reason: "budget"})
		Expect(err).NotTo(HaveOccurred())
		err = resumeFor(w)
		Expect(err).NotTo(HaveOccurred())
		evs, _ := replayEvents(s, sid)
		var m map[string]any
		mErr := json.Unmarshal([]byte(evs[1].PayloadJSON), &m)
		Expect(mErr).NotTo(HaveOccurred())
		Expect(m).To(Equal(map[string]any{"from_status": "paused"}))
	})

	It("resume appends a session.resumed event with user actor", func() {
		s, w, sid, err := newStoreWriter()
		Expect(err).NotTo(HaveOccurred())
		err = pauseFor(w, PauseInfo{Reason: "budget"})
		Expect(err).NotTo(HaveOccurred())
		err = resumeFor(w)
		Expect(err).NotTo(HaveOccurred())
		evs, _ := replayEvents(s, sid)
		Expect(evs[1].Actor).To(Equal("user"))
	})

	It("resume appends a session.resumed event", func() {
		s, w, sid, err := newStoreWriter()
		Expect(err).NotTo(HaveOccurred())
		err = pauseFor(w, PauseInfo{Reason: "budget"})
		Expect(err).NotTo(HaveOccurred())
		err = resumeFor(w)
		Expect(err).NotTo(HaveOccurred())
		evs, _ := replayEvents(s, sid)
		Expect(evs[1].Type).To(Equal("session.resumed"))
	})

	It("resume sets the session status back to active", func() {
		s, w, sid, err := newStoreWriter()
		Expect(err).NotTo(HaveOccurred())
		err = pauseFor(w, PauseInfo{Reason: "budget"})
		Expect(err).NotTo(HaveOccurred())
		err = resumeFor(w)
		Expect(err).NotTo(HaveOccurred())
		sess, _ := getSession(s, sid)
		Expect(sess.Status).To(Equal("active"))
	})

	It("resume on a never-paused writer returns ErrNotPaused", func() {
		_, w, _, _ := newStoreWriter()
		err := w.Resume(context.Background())
		Expect(err).To(MatchError(ErrNotPaused))
	})

	It("resume on a never-paused writer appends no event", func() {
		s, w, sid, err := newStoreWriter()
		Expect(err).NotTo(HaveOccurred())
		_ = w.Resume(context.Background())
		evs, _ := replayEvents(s, sid)
		Expect(evs).To(BeEmpty())
	})

	It("pause and resume keep seq contiguous after existing appends", func() {
		s, w, sid, err := newStoreWriter()
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Append(context.Background(), "user", "turn.begin", map[string]any{"n": 1})
		Expect(err).NotTo(HaveOccurred())
		err = pauseFor(w, PauseInfo{Reason: "budget"})
		Expect(err).NotTo(HaveOccurred())
		err = resumeFor(w)
		Expect(err).NotTo(HaveOccurred())
		evs, _ := replayEvents(s, sid)
		seqs := []int64{}
		for _, e := range evs {
			seqs = append(seqs, e.Seq)
		}
		Expect(seqs).To(Equal([]int64{0, 1, 2}))
	})
})
