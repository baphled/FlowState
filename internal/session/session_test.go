package session

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSession(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Session Suite")
}

func newTestSession() *Session {
	s, err := New("sess-1")
	Expect(err).NotTo(HaveOccurred())
	return s
}

var _ = Describe("Session", func() {
	It("rejects an empty session id", func() {
		_, err := New("")
		Expect(err).To(HaveOccurred())
	})

	It("records ordered turn lifecycle events with ascending sequence numbers", func() {
		s := newTestSession()
		turn, err := s.StartTurn("turn-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.RecordToolResult(turn, "tool=shell output=ok")).To(Succeed())
		Expect(s.Complete(turn, "result=done")).To(Succeed())

		events := s.Events()
		wantKinds := []EventKind{EventTurnAdmitted, EventToolResult, EventTurnComplete}
		Expect(events).To(HaveLen(len(wantKinds)))
		var last uint64
		for i, e := range events {
			Expect(e.Kind).To(Equal(wantKinds[i]))
			Expect(e.Seq).To(BeNumerically(">", last))
			last = e.Seq
		}
	})

	It("makes cancellation terminal", func() {
		s := newTestSession()
		turn, err := s.StartTurn("turn-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Cancel(turn)).To(Succeed())
		Expect(s.RecordToolResult(turn, "late")).To(HaveOccurred())
		Expect(s.Complete(turn, "late")).To(HaveOccurred())
		Expect(s.Events()[len(s.Events())-1].Kind).To(Equal(EventTurnCancelled))
	})

	It("rejects duplicate turn ids within a session", func() {
		s := newTestSession()
		_, err := s.StartTurn("dup")
		Expect(err).NotTo(HaveOccurred())
		_, err = s.StartTurn("dup")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("already exists"))
	})

	It("rejects turns owned by another session instance", func() {
		s1 := newTestSession()
		s2 := newTestSession()
		turn1, err := s1.StartTurn("turn-1")
		Expect(err).NotTo(HaveOccurred())
		foreign, err := s2.StartTurn("turn-foreign")
		Expect(err).NotTo(HaveOccurred())
		Expect(s2.RecordToolResult(turn1, "hijack")).To(HaveOccurred())
		Expect(s2.Complete(turn1, "hijack")).To(HaveOccurred())
		Expect(s1.RecordToolResult(foreign, "hijack")).To(HaveOccurred())
		for _, e := range s1.Events() {
			Expect(e.Payload).NotTo(ContainSubstring("hijack"))
		}
		for _, e := range s2.Events() {
			Expect(e.Payload).NotTo(ContainSubstring("hijack"))
		}
	})

	It("returns event snapshots that cannot mutate turn or session state", func() {
		s := newTestSession()
		turn, err := s.StartTurn("turn-1")
		Expect(err).NotTo(HaveOccurred())
		snap := turn.Events()
		Expect(snap).To(HaveLen(1))
		snap[0].Payload = "tampered"
		Expect(turn.Events()[0].Payload).NotTo(Equal("tampered"))
		ssnap := s.Events()
		ssnap[0].Payload = "tampered"
		Expect(s.Events()[0].Payload).NotTo(Equal("tampered"))
	})

	It("sequences events from interleaved turns in global order", func() {
		s := newTestSession()
		a, err := s.StartTurn("turn-a")
		Expect(err).NotTo(HaveOccurred())
		b, err := s.StartTurn("turn-b")
		Expect(err).NotTo(HaveOccurred())
		for _, turn := range []*Turn{a, b, a, b, a} {
			Expect(s.RecordToolResult(turn, "tool step")).To(Succeed())
		}
		Expect(s.Complete(b, "b done")).To(Succeed())
		events := s.Events()
		Expect(events).NotTo(BeEmpty())
		var last uint64
		for i, e := range events {
			Expect(e.Seq).To(BeNumerically(">", last), "event %d seq %d not greater than %d", i, e.Seq, last)
			last = e.Seq
		}
		Expect(events[len(events)-1].Kind).To(Equal(EventTurnComplete))
	})
})
