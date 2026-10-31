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
	if err != nil {
		panic(err)
	}
	return s
}

var _ = Describe("Session", func() {
	It("rejects an empty session id", func() {
		_, err := New("")
		Expect(err).To(HaveOccurred())
	})

	Context("records ordered turn lifecycle events with ascending sequence numbers", func() {
		var observedEventKinds []EventKind
		var observedExpectedKinds []EventKind
		var observedAscending bool
		BeforeEach(func() {
			s := newTestSession()
			turn, err := s.StartTurn("turn-1")
			Expect(err).NotTo(HaveOccurred())
			if err := s.RecordToolResult(turn, "tool=shell output=ok"); err != nil {
				panic(err)
			}
			if err := s.Complete(turn, "result=done"); err != nil {
				panic(err)
			}

			events := s.Events()
			wantKinds := []EventKind{EventTurnAdmitted, EventToolResult, EventTurnComplete}
			gotKinds := make([]EventKind, len(events))
			ascending := true
			var last uint64
			for i, e := range events {
				gotKinds[i] = e.Kind
				if e.Seq <= last {
					ascending = false
				}
				last = e.Seq
			}
			observedEventKinds = gotKinds
			observedAscending = ascending
			observedExpectedKinds = wantKinds
		})
		It("records the turn lifecycle in order", func() { Expect(observedEventKinds).To(Equal(observedExpectedKinds)) })
		It("assigns strictly increasing event sequences", func() { Expect(observedAscending).To(BeTrue()) })
	})

	Context("makes cancellation terminal", func() {
		var recordErr, completeErr error
		var lastKind EventKind
		BeforeEach(func() {
			s := newTestSession()
			turn, err := s.StartTurn("turn-1")
			Expect(err).NotTo(HaveOccurred())
			err = s.Cancel(turn)
			Expect(err).NotTo(HaveOccurred())
			recordErr = s.RecordToolResult(turn, "late")
			completeErr = s.Complete(turn, "late")
			events := s.Events()
			lastKind = events[len(events)-1].Kind
		})
		It("rejects tool results after cancellation", func() { Expect(recordErr).To(HaveOccurred()) })
		It("rejects completion after cancellation", func() { Expect(completeErr).To(HaveOccurred()) })
		It("keeps cancellation as the final event", func() { Expect(lastKind).To(Equal(EventTurnCancelled)) })
	})

	It("rejects duplicate turn ids within a session", func() {
		s := newTestSession()
		_, err := s.StartTurn("dup")
		Expect(err).NotTo(HaveOccurred())
		_, dupErr := s.StartTurn("dup")
		Expect(dupErr).To(MatchError(ContainSubstring("already exists")))
	})

	Context("rejects turns owned by another session instance", func() {
		var observedRecordForeignErr, observedCompleteForeignErr, observedReverseForeignErr error
		var observedLeaked bool
		BeforeEach(func() {
			s1 := newTestSession()
			s2 := newTestSession()
			turn1, err := s1.StartTurn("turn-1")
			Expect(err).NotTo(HaveOccurred())
			foreign, err := s2.StartTurn("turn-foreign")
			Expect(err).NotTo(HaveOccurred())
			observedRecordForeignErr = s2.RecordToolResult(turn1, "hijack")
			observedCompleteForeignErr = s2.Complete(turn1, "hijack")
			observedReverseForeignErr = s1.RecordToolResult(foreign, "hijack")
			leaked := false
			for _, e := range append(s1.Events(), s2.Events()...) {
				if e.Payload != "" && e.Payload == "hijack" {
					leaked = true
				}
			}
			observedLeaked = leaked
		})
		It("rejects recording a result on a foreign turn", func() { Expect(observedRecordForeignErr).To(HaveOccurred()) })
		It("rejects completing a foreign turn", func() { Expect(observedCompleteForeignErr).To(HaveOccurred()) })
		It("rejects a foreign turn in the original session", func() { Expect(observedReverseForeignErr).To(HaveOccurred()) })
		It("never records the rejected foreign payload", func() { Expect(observedLeaked).To(BeFalse()) })
	})

	Context("returns event snapshots that cannot mutate turn or session state", func() {
		var observedTurnUntampered bool
		var observedSessionUntampered bool
		BeforeEach(func() {
			s := newTestSession()
			turn, err := s.StartTurn("turn-1")
			Expect(err).NotTo(HaveOccurred())
			snap := turn.Events()
			snap[0].Payload = "tampered"
			turnUntampered := turn.Events()[0].Payload != "tampered"
			ssnap := s.Events()
			ssnap[0].Payload = "tampered"
			sessUntampered := s.Events()[0].Payload != "tampered"
			observedTurnUntampered = turnUntampered
			observedSessionUntampered = sessUntampered
		})
		It("protects turn events from snapshot mutation", func() { Expect(observedTurnUntampered).To(BeTrue()) })
		It("protects session events from snapshot mutation", func() { Expect(observedSessionUntampered).To(BeTrue()) })
	})

	Context("sequences events from interleaved turns in global order", func() {
		var observedHasEvents bool
		var observedAscending bool
		var observedLastKind EventKind
		BeforeEach(func() {
			s := newTestSession()
			a, err := s.StartTurn("turn-a")
			Expect(err).NotTo(HaveOccurred())
			b, err := s.StartTurn("turn-b")
			Expect(err).NotTo(HaveOccurred())
			for _, turn := range []*Turn{a, b, a, b, a} {
				if err := s.RecordToolResult(turn, "tool step"); err != nil {
					panic(err)
				}
			}
			if err := s.Complete(b, "b done"); err != nil {
				panic(err)
			}
			events := s.Events()
			ascending := true
			var last uint64
			for _, e := range events {
				if e.Seq <= last {
					ascending = false
				}
				last = e.Seq
			}
			observedHasEvents = len(events) > 0
			observedAscending = ascending
			observedLastKind = events[len(events)-1].Kind
		})
		It("records events for interleaved turns", func() { Expect(observedHasEvents).To(BeTrue()) })
		It("assigns strictly increasing event sequences", func() { Expect(observedAscending).To(BeTrue()) })
		It("records turn completion last", func() { Expect(observedLastKind).To(Equal(EventTurnComplete)) })
	})
})
